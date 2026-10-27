package vectorseg

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"
)

// A segment file:
//
//	header   magic, format, dims, row count, chunk count, table length
//	table    per row: id, content hash, chunk count (uvarint lengths and count)
//	padding  zeros up to a 64-byte boundary
//	vectors  chunk count * dims little-endian float32, a row's chunks
//	         contiguous and in table order
const (
	magic           = "agvecseg"
	format          = 1
	headerSize      = 32
	vectorAlignment = 64
)

var order = binary.LittleEndian

// littleEndian lets a mapped vector block be read in place: on a big-endian
// host it is decoded into a copy instead.
var littleEndian = func() bool {
	probe := uint16(1)

	return *(*byte)(unsafe.Pointer(&probe)) == 1
}()

type row struct {
	id    string
	hash  string
	first int
	count int
}

type parsed struct {
	dims    int
	rows    []row
	vectors []float32
}

func alignUp(offset int, alignment int) int {
	return (offset + alignment - 1) / alignment * alignment
}

func encode(dims int, rows []Row) ([]byte, error) {
	var table []byte

	chunks := 0

	for _, held := range rows {
		table = binary.AppendUvarint(table, uint64(len(held.ID)))
		table = append(table, held.ID...)
		table = binary.AppendUvarint(table, uint64(len(held.Hash)))
		table = append(table, held.Hash...)
		table = binary.AppendUvarint(table, uint64(len(held.Chunks)))
		chunks += len(held.Chunks)
	}

	vectorsAt := alignUp(headerSize+len(table), vectorAlignment)
	data := make([]byte, vectorsAt+chunks*dims*4)

	copy(data, magic)
	order.PutUint32(data[8:], format)
	order.PutUint32(data[12:], uint32(dims))
	order.PutUint32(data[16:], uint32(len(rows)))
	order.PutUint32(data[20:], uint32(chunks))
	order.PutUint32(data[24:], uint32(len(table)))
	copy(data[headerSize:], table)

	offset := vectorsAt

	for _, held := range rows {
		for _, chunk := range held.Chunks {
			if len(chunk) != dims {
				return nil, fmt.Errorf("%w: %s holds a vector of %d dimensions, the segment %d", ErrDims, held.ID, len(chunk), dims)
			}

			for _, value := range chunk {
				order.PutUint32(data[offset:], math.Float32bits(value))
				offset += 4
			}
		}
	}

	return data, nil
}

func parse(data []byte) (parsed, error) {
	if len(data) < headerSize || string(data[:len(magic)]) != magic {
		return parsed{}, fmt.Errorf("%w: no segment header", ErrCorrupt)
	}

	if version := order.Uint32(data[8:]); version != format {
		return parsed{}, fmt.Errorf("%w: format %d", ErrCorrupt, version)
	}

	dims := int(order.Uint32(data[12:]))
	rowCount := int(order.Uint32(data[16:]))
	chunks := int(order.Uint32(data[20:]))
	tableLength := int(order.Uint32(data[24:]))

	if tableLength > len(data)-headerSize {
		return parsed{}, fmt.Errorf("%w: the table runs past the file", ErrCorrupt)
	}

	vectorsAt := alignUp(headerSize+tableLength, vectorAlignment)
	if dims <= 0 || chunks < 0 || len(data) != vectorsAt+chunks*dims*4 {
		return parsed{}, fmt.Errorf("%w: the file is not the size its header names", ErrCorrupt)
	}

	table := data[headerSize : headerSize+tableLength]
	rows := make([]row, 0, rowCount)
	first := 0

	for range rowCount {
		id, rest, err := field(table)
		if err != nil {
			return parsed{}, err
		}

		hash, rest, err := field(rest)
		if err != nil {
			return parsed{}, err
		}

		count, read := binary.Uvarint(rest)
		if read <= 0 || count > uint64(chunks-first) {
			return parsed{}, fmt.Errorf("%w: a row's chunk count is unreadable", ErrCorrupt)
		}

		table = rest[read:]
		rows = append(rows, row{id: id, hash: hash, first: first, count: int(count)})
		first += int(count)
	}

	if len(table) != 0 || first != chunks {
		return parsed{}, fmt.Errorf("%w: the table does not account for the vectors", ErrCorrupt)
	}

	return parsed{dims: dims, rows: rows, vectors: floats(data[vectorsAt:])}, nil
}

func field(table []byte) (string, []byte, error) {
	length, read := binary.Uvarint(table)
	if read <= 0 || length > uint64(len(table)-read) {
		return "", nil, fmt.Errorf("%w: a row's field runs past the table", ErrCorrupt)
	}

	end := read + int(length)

	return string(table[read:end]), table[end:], nil
}

// floats views block as float32s in place where the host reads them as stored,
// so a search over a mapping copies no vector.
func floats(block []byte) []float32 {
	count := len(block) / 4
	if count == 0 {
		return nil
	}

	if littleEndian && uintptr(unsafe.Pointer(&block[0]))%4 == 0 {
		return unsafe.Slice((*float32)(unsafe.Pointer(&block[0])), count)
	}

	values := make([]float32, count)
	for index := range values {
		values[index] = math.Float32frombits(order.Uint32(block[index*4:]))
	}

	return values
}

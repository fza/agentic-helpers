package test

import (
	"context"
	"math"
	"strings"
)

const tableDims = 16

// VectorTable is an embedder answering each text with the vector of the
// first row whose key the text holds, so a test sets the cosine of two texts
// exactly. A text holding no key gets the table's last axis, at a cosine of
// 0 to every vector Toward builds.
type VectorTable struct {
	Rows []TableRow
}

type TableRow struct {
	Key    string
	Vector []float32
}

// Toward is a unit vector at cosine to Reference, the rest of its length on
// axis, so two vectors built on different axes sit at the product of their
// cosines to each other.
func Toward(cosine float32, axis int) []float32 {
	vector := make([]float32, tableDims)
	vector[0] = cosine
	vector[axis] = float32(math.Sqrt(float64(1 - cosine*cosine)))

	return vector
}

// Reference is the table's first axis, the vector Toward measures from.
func Reference() []float32 {
	return Toward(1, 1)
}

func (table *VectorTable) Embed(_ context.Context, texts []string) ([][]float32, error) {
	vectors := make([][]float32, 0, len(texts))

	for _, text := range texts {
		vector := make([]float32, tableDims)
		vector[tableDims-1] = 1

		for _, row := range table.Rows {
			if strings.Contains(text, row.Key) {
				vector = row.Vector

				break
			}
		}

		vectors = append(vectors, vector)
	}

	return vectors, nil
}

func (table *VectorTable) Fingerprint() string {
	return "table/16"
}

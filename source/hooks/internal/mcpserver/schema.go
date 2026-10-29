package mcpserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

var (
	timeType = reflect.TypeFor[time.Time]()
	rawType  = reflect.TypeFor[json.RawMessage]()
)

// SchemaOf is the JSON Schema of what encoding/json makes of value's type, for
// a tool's OutputSchema: derived from the type the answer is built from, so the
// two cannot drift apart. A field the encoder may leave out is optional, a
// nil slice, map or pointer encodes as null, and objects stay open to keys
// added later.
func SchemaOf(value any) json.RawMessage {
	schema := schemaOf(reflect.TypeOf(value), map[reflect.Type]bool{})

	// Every schema value is a map, string, bool or slice of those, which
	// always encode.
	data, _ := json.Marshal(schema)

	return data
}

type schemaNode = map[string]any

// Alternative is a struct type whose own MarshalJSON may write another shape
// than its fields: SchemaAlternative returns a value of that shape's type, and
// the type's schema admits either.
type Alternative interface {
	SchemaAlternative() any
}

func schemaOf(kind reflect.Type, seen map[reflect.Type]bool) schemaNode {
	if kind == nil || kind == rawType || kind.Kind() == reflect.Interface {
		return schemaNode{}
	}

	if kind == timeType {
		return schemaNode{"type": "string", "format": "date-time"}
	}

	if seen[kind] {
		return schemaNode{}
	}

	switch kind.Kind() {
	case reflect.Pointer:
		return nullable(schemaOf(kind.Elem(), seen))
	case reflect.Bool:
		return schemaNode{"type": "boolean"}
	case reflect.String:
		return schemaNode{"type": "string"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return schemaNode{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return schemaNode{"type": "number"}
	case reflect.Slice:
		if kind.Elem().Kind() == reflect.Uint8 {
			return nullable(schemaNode{"type": "string"})
		}

		return nullable(schemaNode{"type": "array", "items": schemaOf(kind.Elem(), seen)})
	case reflect.Array:
		return schemaNode{"type": "array", "items": schemaOf(kind.Elem(), seen)}
	case reflect.Map:
		return nullable(schemaNode{"type": "object", "additionalProperties": schemaOf(kind.Elem(), seen)})
	case reflect.Struct:
		seen[kind] = true
		defer delete(seen, kind)

		properties := schemaNode{}
		required := []string{}

		structFields(kind, seen, properties, &required)

		node := schemaNode{"type": "object", "properties": properties}
		if len(required) > 0 {
			node["required"] = required
		}

		alternative, writesAnother := reflect.Zero(kind).Interface().(Alternative)
		if writesAnother {
			return schemaNode{"anyOf": []any{node, schemaOf(reflect.TypeOf(alternative.SchemaAlternative()), seen)}}
		}

		return node
	default:
		return schemaNode{}
	}
}

// structFields adds a struct's encoded fields, an embedded struct's promoted
// as encoding/json promotes them: a field of the outer struct shadows an
// embedded one of the same name, wherever either is declared.
func structFields(kind reflect.Type, seen map[reflect.Type]bool, properties schemaNode, required *[]string) {
	var embedded []reflect.Type

	own := map[string]bool{}

	for field := range kind.Fields() {
		tag := field.Tag.Get("json")
		if tag == "-" {
			continue
		}

		name, options, _ := strings.Cut(tag, ",")

		if field.Anonymous && name == "" && field.Type.Kind() == reflect.Struct {
			embedded = append(embedded, field.Type)

			continue
		}

		if !field.IsExported() {
			continue
		}

		if name == "" {
			name = field.Name
		}

		own[name] = true
		properties[name] = schemaOf(field.Type, seen)

		if !strings.Contains(options, "omitempty") && !strings.Contains(options, "omitzero") {
			*required = append(*required, name)
		}
	}

	for _, inner := range embedded {
		promoted := schemaNode{}
		var promotedRequired []string

		structFields(inner, seen, promoted, &promotedRequired)

		for name, node := range promoted {
			if !own[name] {
				properties[name] = node
			}
		}

		for _, name := range promotedRequired {
			if !own[name] {
				*required = append(*required, name)
			}
		}
	}
}

func nullable(node schemaNode) schemaNode {
	return schemaNode{"anyOf": []any{node, schemaNode{"type": "null"}}}
}

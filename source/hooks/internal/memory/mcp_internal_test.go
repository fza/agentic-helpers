package memory

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEverySchemaKeyIsOneTheCommandReads(t *testing.T) {
	read := map[string]bool{}

	fields := reflect.TypeFor[request]()
	for index := range fields.NumField() {
		name, _, _ := strings.Cut(fields.Field(index).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			read[name] = true
		}
	}

	for _, held := range commands() {
		keys, err := schemaKeys(held.schema)
		if err != nil {
			t.Fatalf("the %s schema should list its properties: %v", held.name, err)
		}

		for _, key := range keys {
			if !read[key] {
				t.Errorf("%s lists %s, a key no command reads", held.name, key)
			}
		}
	}
}

func TestEveryServedSchemaIsClosed(t *testing.T) {
	for _, held := range commands() {
		var served struct {
			AdditionalProperties *bool `json:"additionalProperties"`
		}

		err := json.Unmarshal([]byte(closedSchema(held.schema)), &served)
		if err != nil {
			t.Fatalf("the served %s schema should stay valid JSON: %v", held.name, err)
		}

		if served.AdditionalProperties == nil || *served.AdditionalProperties {
			t.Errorf("the served %s schema should take no key beyond its properties", held.name)
		}
	}
}

func TestSchemaKeysKeepTheSchemaOrder(t *testing.T) {
	keys, err := schemaKeys(`{"type":"object","properties":{"ids":{"type":"array"},"no_heat":{"type":"boolean"},"all":{}}}`)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(keys, ",") != "ids,no_heat,all" {
		t.Errorf("schema keys should come in the order the schema names them, got %v", keys)
	}
}

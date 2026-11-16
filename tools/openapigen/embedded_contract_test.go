package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEmbeddedSchemaMatchesNamedAndNestedJSONFields(t *testing.T) {
	dir := t.TempDir()
	const source = "package fixture\n type Base struct{ ID int `json:\"id\"` }; type View struct{ Base; Name string `json:\"name\"` }; type Status struct{ Verified bool `json:\"verified\"` }; type Detail struct{ View; Status Status `json:\"status\"` }"
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	schemas, _, err := walkPackages([]packageRequest{{Path: dir}})
	if err != nil {
		t.Fatal(err)
	}
	resolved := flattenEmbedded(schemas)
	type Base struct {
		ID int `json:"id"`
	}
	type View struct {
		Base
		Name string `json:"name"`
	}
	type Status struct {
		Verified bool `json:"verified"`
	}
	type Detail struct {
		View
		Status Status `json:"status"`
	}
	data, _ := json.Marshal(Detail{View: View{Base: Base{ID: 1}, Name: "test"}, Status: Status{Verified: true}})
	var actual map[string]json.RawMessage
	_ = json.Unmarshal(data, &actual)
	for _, schema := range resolved {
		if schema.Name != "Detail" {
			continue
		}
		if len(schema.Fields) != len(actual) {
			t.Fatalf("generated fields=%+v, JSON=%s", schema.Fields, data)
		}
		for _, field := range schema.Fields {
			if _, ok := actual[field.JSONName]; !ok {
				t.Fatalf("schema invented field %q", field.JSONName)
			}
		}
		return
	}
	t.Fatal("detail schema missing")
}

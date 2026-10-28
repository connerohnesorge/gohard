package config

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
)

// scalarSlots are the free-string value positions of the key table: a
// YAML template with %s where the scalar goes, and the path to that
// value's schema in gohard.schema.json.
var scalarSlots = []struct {
	yaml   string
	schema []string
}{
	{"defines:\n  FOO: %s\n",
		[]string{"properties", "defines", "additionalProperties"}},
	{"simulation:\n  env:\n    vars:\n      A: %s\n",
		[]string{"properties", "simulation", "properties", "env",
			"properties", "vars", "additionalProperties"}},
	{"simulation:\n  models:\n    a.b: %s\n",
		[]string{"properties", "simulation", "properties", "models",
			"additionalProperties"}},
	{"simulation:\n  dump_scope: [%s]\n",
		[]string{"properties", "simulation", "properties", "dump_scope",
			"items"}},
	{"libraries:\n  lib: [%s]\n",
		[]string{"properties", "libraries", "additionalProperties",
			"items"}},
}

// jsonType is the JSON Schema type a YAML scalar becomes in a
// YAML-to-JSON schema check.
func jsonType(t *testing.T, scalar string) string {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte("v: "+scalar), &n); err != nil {
		t.Fatalf("%q: %v", scalar, err)
	}
	switch tag := n.Content[0].Content[1].ShortTag(); tag {
	case "!!int", "!!float":
		return "number"
	case "!!str", "!!timestamp":
		return "string"
	case "!!bool":
		return "boolean"
	case "!!null":
		return "null"
	default:
		t.Fatalf("%q: tag %s", scalar, tag)
	}
	return ""
}

// TestSchemaAgreesOnScalarTypes: for every free-string slot and every
// kind of YAML scalar (ints, floats, hex, octal, dates, nulls, bools,
// quoted and plain strings), the loader accepts the value exactly when
// the generated schema's type allows it. The one documented exception is
// an unquoted YAML 1.1 bool spelling that YAML 1.2 reads as a string:
// the loader rejects it (#28) and JSON Schema cannot tell it apart.
func TestSchemaAgreesOnScalarTypes(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(Schema(), &schema); err != nil {
		t.Fatal(err)
	}
	scalars := []string{"1", "12", "-3", "1.5", "1.50", "1e3", "0x10",
		"0o17", ".inf", "-.inf", ".nan", "2000-01-01",
		"2000-01-01T00:00:00Z", "~", "null", "true", "false", "x",
		"tb.dut", `"x"`, `'q'`, `"1"`, `"yes"`, "a b"}
	for _, slot := range scalarSlots {
		var node any = schema
		for _, p := range slot.schema {
			node = node.(map[string]any)[p]
		}
		var types []string
		switch ty := node.(map[string]any)["type"].(type) {
		case string:
			types = []string{ty}
		case []any:
			for _, x := range ty {
				types = append(types, x.(string))
			}
		}
		for _, sc := range scalars {
			src := strings.Replace(slot.yaml, "%s", sc, 1)
			_, ds := Parse(FileName, []byte(src))
			loader := !slices.ContainsFunc(ds, func(d diag.Diagnostic) bool {
				return d.Severity == diag.Error
			})
			jt := jsonType(t, sc)
			if jt == "string" && !strings.ContainsAny(sc, `"'`) &&
				slices.Contains(YAML11Bools(), sc) {
				continue
			}
			if schemaOK := slices.Contains(types, jt); schemaOK != loader {
				t.Errorf("%q: loader %v, schema (%v, %s) %v; %v", src,
					loader, types, jt, schemaOK, ds)
			}
		}
	}
}

// TestUnquotedBoolEverySpelling: every YAML 1.1 bool spelling, unquoted,
// in every key that holds strings, is config/unquoted-bool; quoted, it
// is not.
func TestUnquotedBoolEverySpelling(t *testing.T) {
	for _, slot := range stringSlots() {
		for _, b := range YAML11Bools() {
			src := strings.Replace(slot.src, "%s", b, 1)
			_, ds := Parse(FileName, []byte(src))
			if len(ds) == 0 || ds[0].Code != diag.ConfigUnquotedBool {
				t.Errorf("%s %q: want config/unquoted-bool, got %v",
					slot.title, b, ds)
			}
			src = strings.Replace(slot.src, "%s", `"`+b+`"`, 1)
			_, ds = Parse(FileName, []byte(src))
			if slices.ContainsFunc(ds, func(d diag.Diagnostic) bool {
				return d.Code == diag.ConfigUnquotedBool
			}) {
				t.Errorf("%s %q quoted: got %v", slot.title, b, ds)
			}
		}
	}
}

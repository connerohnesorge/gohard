package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedFilesInSync fails when a committed generated file differs
// from what the generator writes now (the #30 sync check, also run by
// scripts/ci/gen-check.sh through go generate).
func TestGeneratedFilesInSync(t *testing.T) {
	files, err := Generate("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		old, err := os.ReadFile(filepath.Join("../..", f.Path))
		if err != nil || !bytes.Equal(old, f.Data) {
			t.Errorf("%s is stale: run go generate ./internal/cst", f.Path)
		}
		if !bytes.HasPrefix(f.Data, []byte("// Code generated")) {
			t.Errorf("%s lacks the generated-file header", f.Path)
		}
	}
}

// TestDeterministic: two runs give identical bytes.
func TestDeterministic(t *testing.T) {
	a, err := Generate("../..")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Generate("../..")
	for i := range a {
		if !bytes.Equal(a[i].Data, b[i].Data) {
			t.Errorf("%s differs between runs", a[i].Path)
		}
	}
}

// TestValidation: the generator rejects bad tables.
func TestValidation(t *testing.T) {
	root := t.TempDir()
	kinds, _ := os.ReadFile("../../internal/cst/kinds.yaml")
	kw, _ := os.ReadFile("../../internal/token/keywords.yaml")
	cases := map[string][2]string{
		"duplicate node": {string(kinds) + "  - name: DesignFile\n", ""},
		"unknown field": {
			strings.Replace(
				string(kinds),
				"node: DesignUnit, list",
				"node: Nope, list",
				1,
			),
			"",
		},
		"duplicate word": {
			"",
			string(
				kw,
			) + "  - {word: \"abs\", class: vhdl, first: \"1993\", psl: false, context: x}\n",
		},
		"bad add count": {
			"",
			strings.Replace(string(kw), "add: 17", "add: 16", 1),
		},
		"last with nth": {
			strings.Replace(string(kinds),
				"{name: actual, node: Expr, last: true}",
				"{name: actual, node: Expr, last: true, nth: 1}", 1),
			"",
		},
		"unknown with token": {
			strings.Replace(string(kinds),
				"{name: formal, node: Expr, with: Arrow}",
				"{name: formal, node: Expr, with: Nope}", 1),
			"",
		},
		"reserved method": {
			strings.Replace(
				string(kinds),
				"{name: eof, token: EOF}",
				"{name: kind, token: EOF}",
				1,
			),
			"",
		},
	}
	for name, c := range cases {
		k, w := c[0], c[1]
		if k == "" {
			k = string(kinds)
		}
		if w == "" {
			w = string(kw)
		}
		for path, data := range map[string]string{kindsPath: k,
			keywordsPath: w} {
			full := filepath.Join(root, filepath.FromSlash(path))
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := Generate(root); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

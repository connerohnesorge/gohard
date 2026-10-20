package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpToDate: the committed codec_gen.go is what the generator
// writes from the current sema types, and generating is deterministic.
func TestUpToDate(t *testing.T) {
	a, err := generate("../..")
	if err != nil {
		t.Fatal(err)
	}
	b, err := generate("../..")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("two runs differ")
	}
	old, err := os.ReadFile(filepath.Join("../..",
		filepath.FromSlash(outPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, old) {
		t.Fatal(outPath + " is stale: run go generate ./internal/sema")
	}
	// Every sema struct the bundle reaches has its encoder and decoder.
	for _, name := range []string{"Result", "File", "Unit", "Decl",
		"Region", "Type", "Expr", "SeqStmt", "Stmt", "View", "Library",
		"parser_Result", "eval_Type"} {
		for _, f := range []string{"func (e *encoder) s_" + name + "(",
			"func (d *decoder) s_" + name + "("} {
			if !strings.Contains(string(a), f) {
				t.Errorf("missing %s", f)
			}
		}
	}
}

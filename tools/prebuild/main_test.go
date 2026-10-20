package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/connerohnesorge/gohard/internal/sema"
)

// TestUpToDate: the committed prebuilt.bin is what prebuild writes now.
func TestUpToDate(t *testing.T) {
	old, err := os.ReadFile(filepath.Join("../..",
		filepath.FromSlash(outPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sema.PrebuildBundle(), old) {
		t.Fatal(outPath + " is stale: run go generate ./internal/sema")
	}
}

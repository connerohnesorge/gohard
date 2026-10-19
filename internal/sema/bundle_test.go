package sema

import (
	"testing"
)

// TestBundleClean: analyzing the bundled std/ieee text finds no errors
// (an error there is a tool failure for every run, #33).
func TestBundleClean(t *testing.T) {
	b := Bundle()
	for _, d := range b.Diagnostics {
		t.Errorf("%s:%d:%d: %s: %s", d.Path, d.Line, d.Col, d.Code,
			d.Message)
	}
	if len(b.Units) == 0 {
		t.Fatal("no bundled units")
	}
}

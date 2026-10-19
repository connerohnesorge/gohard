package cli

import (
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/format"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

// TestFmtNodeLimitNotParsed shows a file over the CST node limit (here
// lowered to 2 nodes) is format/not-parsed next to the parser's
// workspace/file-too-large, and is not formatted: it won't parse, so
// fmt --check must not report it clean (#20, #32).
func TestFmtNodeLimitNotParsed(t *testing.T) {
	s := workspace.Source{
		File: workspace.File{Path: "a.vhd", Name: "a.vhd"},
		Text: []byte("entity  a is end;\narchitecture r of a is " +
			"begin end;\n"),
	}
	popts := parser.Options{MaxNodes: 2}
	o := formatSource(s, popts, format.Default())
	if o.res != nil {
		t.Fatalf("a file over the node limit was formatted")
	}
	var codes []diag.Code
	for _, d := range o.diags {
		codes = append(codes, d.Code)
	}
	if len(codes) != 2 || codes[0] != diag.WorkspaceFileTooLarge ||
		codes[1] != diag.FormatNotParsed {
		t.Fatalf("diagnostics %v, want file-too-large and not-parsed",
			codes)
	}
	// Under the limit the same file formats.
	if o := formatSource(s, parser.Options{}, format.Default()); o.res ==
		nil || !o.res.Changed() {
		t.Fatalf("file under the node limit was not formatted: %v",
			o.diags)
	}
}

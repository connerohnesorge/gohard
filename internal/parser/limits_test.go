package parser

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/token"
	"github.com/connerohnesorge/gohard/v2/internal/workspace"
)

// countNodes is the number of CST nodes (root included, tokens not) of
// a parse, the quantity MaxNodes limits.
func countNodes(r *Result) int {
	n := 0
	for range r.Tree().Descendants() {
		n++
	}
	return n
}

// checkParsedClean: src parsed (not too large), round-trips, and has no
// problem at all, so in particular the budget was never exhausted.
func checkParsedClean(t *testing.T, what string, src []byte, r *Result) {
	t.Helper()
	if r.TooLarge {
		t.Fatalf("%s: reported too large", what)
	}
	if !bytes.Equal([]byte(r.Root.String()), src) {
		t.Fatalf("%s: round trip failed", what)
	}
	for _, p := range r.Problems {
		t.Errorf("%s: unexpected %s %v at %d", what, p.Code, p.Args,
			p.Start)
	}
}

// checkTooLarge: src was not parsed, the tree is still lossless, and
// the only diagnostic is the workspace/file-too-large warning naming
// limit.
func checkTooLarge(t *testing.T, what string, src []byte, r *Result,
	limit string,
) {
	t.Helper()
	if !r.TooLarge {
		t.Fatalf("%s: parsed, want workspace/file-too-large", what)
	}
	if !bytes.Equal([]byte(r.Root.String()), src) {
		t.Fatalf("%s: round trip failed", what)
	}
	ds := r.Diagnostics("big.vhd")
	if len(ds) != 1 || ds[0].Code != diag.WorkspaceFileTooLarge ||
		ds[0].Severity != diag.Warning || ds[0].Line != 1 ||
		!strings.Contains(ds[0].Message, limit) {
		t.Errorf("%s: diagnostics %+v", what, ds)
	}
}

// nodeLimitInput returns a valid file of exactly nodes CST nodes: one
// context clause `library l0, l1, ...;` (each extra name is one
// SimpleName node) followed by repeated empty entities. Node costs are
// measured, not assumed, so the test follows the grammar.
func nodeLimitInput(t *testing.T, nodes int) []byte {
	t.Helper()
	const unit = "entity e is end;\n"
	measure := func(src string) int {
		r := Parse([]byte(src), Options{})
		checkParsedClean(t, "measure", []byte(src), r)
		return countNodes(r)
	}
	lib := func(names int) string {
		var b strings.Builder
		b.WriteString("library l0")
		for i := 1; i < names; i++ {
			b.WriteString(", l" + strconv.Itoa(i))
		}
		b.WriteString(";\n")
		return b.String()
	}
	base := measure(lib(1) + unit)
	perUnit := measure(lib(1)+unit+unit) - base
	perName := measure(lib(2)+unit) - base
	if perUnit < 1 || perName != 1 {
		t.Fatalf("node costs: %d per entity, %d per library name, "+
			"want >= 1 and 1", perUnit, perName)
	}
	units := 1 + (nodes-base)/perUnit
	names := 1 + (nodes-base)%perUnit
	src := lib(names) + strings.Repeat(unit, units)
	return []byte(src)
}

// TestNodeLimit: a file of exactly MaxNodes (1,000,000) nodes parses
// within the default budget with no problem; one node more is not
// parsed and gets workspace/file-too-large (#31).
func TestNodeLimit(t *testing.T) {
	if MaxNodes != 1_000_000 {
		t.Fatalf("MaxNodes = %d, #31 says 1,000,000", MaxNodes)
	}
	at := nodeLimitInput(t, MaxNodes)
	r := Parse(at, Options{})
	checkParsedClean(t, "at the node limit", at, r)
	if n := countNodes(r); n != MaxNodes {
		t.Fatalf("built %d nodes, want exactly %d", n, MaxNodes)
	}
	// One more library name is one more node.
	over := bytes.Replace(at, []byte("l0"), []byte("l0, lx"), 1)
	checkTooLarge(t, "one node over", over, Parse(over, Options{}),
		"1000000 nodes")
}

// TestByteLimit: a file of exactly MaxBytes parses within the default
// budget with no problem, and one byte more is not parsed and gets
// workspace/file-too-large. The file is real VHDL (40,000 entities)
// padded with comment lines.
func TestByteLimit(t *testing.T) {
	var b bytes.Buffer
	for range 40_000 {
		b.WriteString("entity e is end;\n")
	}
	line := "-- padding comment line\n"
	for b.Len()+len(line) <= MaxBytes {
		b.WriteString(line)
	}
	b.WriteString(strings.Repeat("-", MaxBytes-b.Len()))
	at := b.Bytes()
	if len(at) != MaxBytes {
		t.Fatalf("built %d bytes", len(at))
	}
	checkParsedClean(t, "at the byte limit", at, Parse(at, Options{}))
	over := append(bytes.Clone(at), '\n')
	checkTooLarge(t, "one byte over", over, Parse(over, Options{}),
		limitText(MaxBytes))
}

// TestByteLimitMatchesWorkspace: the parser, the workspace reader (the
// CLI) and so the language server share one 10 MB limit (#10, #20), and
// the message names the limit as 10 MB.
func TestByteLimitMatchesWorkspace(t *testing.T) {
	if MaxBytes != workspace.MaxFileSize {
		t.Fatalf("parser.MaxBytes = %d, workspace.MaxFileSize = %d",
			MaxBytes, workspace.MaxFileSize)
	}
	if limitText(MaxBytes) != "10 MB" {
		t.Errorf("limit text %q", limitText(MaxBytes))
	}
	src := bytes.Repeat([]byte(" "), workspace.MaxFileSize+1)
	if r := Parse(src, Options{}); !r.TooLarge {
		t.Errorf("%d bytes parsed; the CLI rejects them", len(src))
	}
}

// TestLimitsExact: on every corpus file and every standard, the limits
// are exact: MaxBytes = len(src) and MaxNodes = the node count parse,
// one less of either is workspace/file-too-large, and the tree is
// lossless either way.
func TestLimitsExact(t *testing.T) {
	for _, f := range corpusFiles(t) {
		src := readFile(t, f)
		for _, std := range token.Standards {
			o := Options{Standard: std}
			r := Parse(src, o)
			n := countNodes(r)
			if r.TooLarge || r.Root.Kind() != cst.KindDesignFile {
				t.Fatalf("%s: too large by default", f)
			}
			o.MaxNodes, o.MaxBytes = n, len(src)
			if got := Parse(src, o); got.TooLarge ||
				got.Root.ID() != r.Root.ID() {
				t.Errorf("%s %v: not parsed at its own limits", f, std)
			}
			o.MaxNodes = n - 1
			checkTooLarge(t, f, src, Parse(src, o),
				strconv.Itoa(n-1)+" nodes")
			o.MaxNodes, o.MaxBytes = n, len(src)-1
			checkTooLarge(t, f, src, Parse(src, o),
				strconv.Itoa(len(src)-1)+" bytes")
		}
	}
}

// TestNestedRegionsDepthLimit: unclosed nested packages, package
// bodies, subprogram bodies and processes far past maxDepth (in a file
// of about 100 KB) report exactly one "nested too deeply" error, never
// exhaust the step budget, and keep every byte (#31: work bounded by a
// budget linear in the input, depth limit on every recursion path).
func TestNestedRegionsDepthLimit(t *testing.T) {
	units := [][2]string{{"", "package p is "},
		{"", "package body p is "},
		{"", "package body p is procedure q is "},
		{"", "package body p is function f return t is "},
		{"", "package p is package body q is "},
		{"architecture a of e is begin ", "b: block is begin "},
		{"architecture a of e is begin process begin ", "if c then "}}
	for _, pu := range units {
		u := pu[1]
		src := pu[0] + strings.Repeat(u, 8000)
		r := Parse([]byte(src), Options{Standard: token.Std2008})
		if r.Root.String() != src {
			t.Fatalf("%q: round trip failed", u)
		}
		deep := 0
		for _, p := range r.Problems {
			msg := strings.Join(p.Args, " ")
			if strings.Contains(msg, "budget") {
				t.Errorf("%q: budget exhausted: %s", u, msg)
			}
			if strings.Contains(msg, "nested too deeply") {
				deep++
			}
		}
		if deep != 1 || len(r.Problems) != 1 {
			t.Errorf("%q: %d problems, %d depth errors; want 1 and 1",
				u, len(r.Problems), deep)
		}
	}
}

// Package parser is GoHard's hand-written, error-tolerant recursive
// descent parser for VHDL 1993, 2002, 2008 and 2019 with embedded PSL
// (#31). It turns one file's bytes into an immutable lossless green tree
// (package cst) plus diagnostics, and is shared by sema, the formatter and
// the language server.
//
// Invariants:
//
//   - Lossless: the text of the tree is exactly the input bytes, for any
//     input, including invalid UTF-8.
//   - Never panics and never hangs: parse work is bounded by a budget
//     linear in the input size (P7); every loop ends at end of input.
//   - The tree shape does not depend on the standard: gates only add
//     syntax/standard diagnostics.
//   - Every reported problem points at a real offset of the input.
//   - Deterministic: the same bytes and options give the same tree and
//     the same problems (P1).
//
// Files over MaxBytes, or whose tree would exceed MaxNodes nodes, are
// not parsed: the root then holds the whole text in one Unparsed token
// and the result carries workspace/file-too-large.
package parser

import (
	"slices"
	"strconv"
	"sync"

	"github.com/connerohnesorge/gohard/internal/condanal"
	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/lexer"
	"github.com/connerohnesorge/gohard/internal/span"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Size limits (#10, #31).
const (
	// MaxBytes is the largest file that is parsed: 10 MB, the same
	// limit as workspace.MaxFileSize (#10, #20), so the CLI and the
	// language server agree on every file.
	MaxBytes = 10_000_000
	// MaxNodes is the largest number of CST nodes (not counting tokens)
	// a parsed file may have.
	MaxNodes = 1_000_000
	// MaxProblems is the most problems one parse records. Error-dense
	// input (a file of NUL bytes) would otherwise keep one problem per
	// byte alive in every snapshot (P7); the problems after the cap are
	// dropped and one syntax/error "too many errors" stands for them.
	MaxProblems = 10_000
)

// Budget constants: the parser may peek at most stepsPerByte*len+
// baseSteps times. A legal parse needs far fewer; running out means a
// parser bug and ends the parse with a diagnostic instead of a hang.
const (
	stepsPerByte = 256
	baseSteps    = 1 << 16
	// maxFuel is the most peeks allowed without consuming a token.
	maxFuel = 1 << 16
	// maxDepth is the deepest nesting of enter calls.
	maxDepth = 1000
)

// Options configure Parse.
type Options struct {
	// Standard is the selected VHDL standard (default 2008).
	Standard token.Standard
	// PSLComments parses `-- psl` comment lines (psl.comments).
	PSLComments bool
	// PSLStrictKeywords reserves the PSL-only 2008 words everywhere
	// (psl.strict_keywords).
	PSLStrictKeywords bool
	// Defines are the user conditional-analysis identifiers, already
	// merged so that --define overrides defines: (config.ApplyDefines).
	Defines []condanal.Define
	// ToolVersion is the value of TOOL_VERSION.
	ToolVersion string
	// Budget overrides the step budget (0: linear default). Tests only.
	Budget int
	// MaxBytes and MaxNodes override the size limits (0: the
	// defaults). Tests only.
	MaxBytes int
	MaxNodes int
}

// FromConfig returns the parse options of a loaded configuration
// (standard, psl.comments, psl.strict_keywords, defines) for the given
// gohard version (TOOL_VERSION).
func FromConfig(c config.Config, toolVersion string) Options {
	std, ok := token.ParseStandard(c.Standard)
	if !ok {
		std = token.Std2008
	}
	o := Options{
		Standard:          std,
		PSLComments:       c.PSL.Comments,
		PSLStrictKeywords: c.PSL.StrictKeywords,
		ToolVersion:       toolVersion,
	}
	for _, d := range c.Defines {
		o.Defines = append(o.Defines, condanal.Define{Name: d.Name,
			Value: d.Value})
	}
	return o
}

// Problem is a diagnostic found while parsing, located by byte offsets.
type Problem struct {
	Start int
	End   int
	Code  diag.Code
	Args  []string
	// Inactive reports that the problem is inside an inactive
	// conditional-analysis branch: an error there is reported as the
	// faded hint syntax/inactive.
	Inactive bool
}

// Diagnostic returns p as a diagnostic without a location.
func (p Problem) Diagnostic() diag.Diagnostic {
	d := diag.New(p.Code, p.Args...)
	if p.Inactive && d.Severity == diag.Error {
		return diag.New(diag.SyntaxInactive, d.Message)
	}
	return d
}

// Result is the outcome of Parse. It is immutable.
type Result struct {
	// Root is the green root: a DesignFile node.
	Root *cst.Green
	// Problems are in source order.
	Problems []Problem
	// TooLarge reports that the file was not parsed.
	TooLarge bool
	// Inactive lists the inactive conditional-analysis branches.
	Inactive []lexer.Region
	// Directives lists the tool directives.
	Directives []condanal.Directive

	src []byte
	// memo holds what later stages derive from this tree alone.
	memo sync.Map
}

// Memo returns the values later stages derived from r's tree (sema's
// unit node IDs, #53). The editor reuses a Result for an unchanged
// file, so they survive across analyses.
func (r *Result) Memo() *sync.Map { return &r.memo }

// Tree returns the red root of the tree.
func (r *Result) Tree() cst.Node { return cst.NewRoot(r.Root) }

// Diagnostics returns the problems as sorted diagnostics located in path
// (line and column from the parsed bytes).
func (r *Result) Diagnostics(path string) []diag.Diagnostic {
	idx := span.NewIndex(r.src)
	out := make([]diag.Diagnostic, 0, len(r.Problems))
	for _, p := range r.Problems {
		d := p.Diagnostic()
		l, c := idx.LineCol(p.Start)
		el, ec := idx.LineCol(max(p.End, p.Start))
		out = append(out, d.At(path, l, c).To(el, ec))
	}
	return diag.Normalize(out)
}

// Parse parses one file. src must not be modified while the result is
// in use (the tree shares no memory with it, but Result.Diagnostics
// reads it).
func Parse(src []byte, opts Options) *Result {
	if opts.Standard == 0 {
		opts.Standard = token.Std2008
	}
	maxBytes := cmpOr(opts.MaxBytes, MaxBytes)
	if len(src) > maxBytes {
		return tooLarge(src, "its size ("+strconv.Itoa(len(src))+
			" bytes)", limitText(maxBytes))
	}
	lo := lexerOptions(opts)
	ca := condanal.Analyze(src, condanal.Options{Lexer: lo,
		Defines: opts.Defines, ToolVersion: opts.ToolVersion})
	lo.Inactive = ca.Inactive
	p := newParser(src, opts, lo)
	for _, pr := range ca.Problems {
		p.addProblem(Problem{Start: pr.Start, End: pr.End,
			Code: pr.Code, Args: pr.Args})
	}
	p.parseDesignFile()
	if p.tooLarge || p.b.Nodes() > p.maxNodes {
		return tooLarge(src, "its syntax tree (over "+
			strconv.Itoa(p.maxNodes)+" nodes)",
			strconv.Itoa(p.maxNodes)+" nodes")
	}
	probs := p.probs
	if p.dropped > 0 {
		probs = append(probs, Problem{Start: p.firstDropped,
			End: p.firstDropped, Code: diag.SyntaxError,
			Args: []string{"too many errors; " +
				strconv.Itoa(p.dropped) + " more not shown"}})
	}
	slices.SortStableFunc(probs, func(a, b Problem) int {
		return a.Start - b.Start
	})
	return &Result{
		Root:       p.b.Finish(),
		Problems:   probs,
		Inactive:   ca.Inactive,
		Directives: ca.Directives,
		src:        src,
	}
}

func lexerOptions(o Options) lexer.Options {
	return lexer.Options{
		Standard:       o.Standard,
		PSLComments:    o.PSLComments,
		StrictKeywords: o.PSLStrictKeywords,
	}
}

func cmpOr(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

func limitText(n int) string {
	if n == MaxBytes {
		return "10 MB"
	}
	return strconv.Itoa(n) + " bytes"
}

// tooLarge returns the result for a file that is not parsed: the whole
// text as one Unparsed token, so the tree is still lossless.
func tooLarge(src []byte, what, limit string) *Result {
	b := cst.NewBuilder()
	b.StartNode(cst.KindDesignFile)
	if len(src) > 0 {
		b.Token(token.Unparsed, string(src), nil, nil, 0)
	}
	b.Token(token.EOF, "", nil, nil, 0)
	b.FinishNode()
	return &Result{
		Root:     b.Finish(),
		TooLarge: true,
		Problems: []Problem{{Code: diag.WorkspaceFileTooLarge,
			Args: []string{what, limit}}},
		src: src,
	}
}

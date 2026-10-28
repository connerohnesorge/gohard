package format

import (
	"errors"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
)

// Edit replaces the input bytes [Start, End) with NewText. Offsets are
// byte offsets into the formatted input.
type Edit struct {
	Start   int
	End     int
	NewText string
}

// Result is the outcome of formatting a whole file.
type Result struct {
	// Text is the formatted file.
	Text []byte
	// Edits turn the input into Text. They are sorted, do not overlap
	// and are empty when the file is already formatted.
	Edits []Edit
}

// Changed reports whether formatting changes the file.
func (r Result) Changed() bool { return len(r.Edits) > 0 }

// ErrNotParsed is returned by Source for a file that does not parse
// (format/not-parsed): it has a syntax error outside inactive branches
// or is too large to parse.
var ErrNotParsed = errors.New("file does not parse; it was not formatted")

// Parses reports whether res is a tree the formatter may rewrite: the
// file was parsed and has no syntax error outside inactive
// conditional-analysis branches. Format itself accepts any tree (error
// regions are kept verbatim); the CLI refuses files that do not parse.
func Parses(res *parser.Result) bool {
	if res == nil || res.TooLarge {
		return false
	}
	for _, p := range res.Problems {
		if p.Code == diag.SyntaxError && !p.Inactive {
			return false
		}
	}
	return true
}

// Source parses src and formats it. It returns ErrNotParsed, with a
// zero Result, when the file does not parse.
func Source(src []byte, popts parser.Options, opts Options) (Result,
	error) {
	res := parser.Parse(src, popts)
	if !Parses(res) {
		return Result{}, ErrNotParsed
	}
	return Format(res, opts), nil
}

// Layout passes run after the base layout, in this order. Each is set
// by the file that implements it (wrap.go, align.go) from an init
// function, and is nil until that file exists:
//
//   - wrapPass breaks lines longer than opts.MaxLineLength (golines
//     style; wrapped port and generic maps one association per line),
//     rebuilding doc.lines with the indenter as it goes. It runs first,
//     so alignment sees the final lines.
//   - alignPass aligns `:` in port and generic declarations, `=>` in
//     maps and aggregates and `<=`/`:=` in runs of consecutive
//     assignments by widening in-line gaps (doc.gap). It reads
//     opts.AlignPorts itself, because wrapped maps align `=>` either
//     way.
//
// A pass may only change gaps, line breaks and levels: atoms are
// fixed, so every invariant of the package holds whatever a pass does.
var (
	wrapPass  func(*doc)
	alignPass func(*doc)
)

// Format formats a parsed file with opts and returns the new text and
// the edits from the old text to it. It is a pure function of the tree,
// the inactive regions of res and opts, and it never fails: regions it
// cannot format (errors, inactive branches, directives) are kept
// verbatim.
func Format(res *parser.Result, opts Options) Result {
	d := build(res, opts.normalized())
	if wrapPass != nil {
		wrapPass(d)
	}
	if alignPass != nil {
		alignPass(d)
	}
	out, edits := d.render()
	return Result{Text: out, Edits: edits}
}

// Range formats only the bytes [start, end) of the parsed file: it
// returns the edits of Format that lie entirely inside the range, so
// nothing outside the range changes. An insertion belongs to the range
// when it is at start or inside; one at end belongs to the text after
// the range (for a range of whole lines, the next line's indentation).
// The edits are independent, so any subset of them applied alone still
// gives a file with the same tokens.
func Range(res *parser.Result, opts Options, start, end int) []Edit {
	if end < start {
		start, end = end, start
	}
	var out []Edit
	for _, e := range Format(res, opts).Edits {
		inside := e.Start >= start && e.End <= end
		if e.Start == e.End && e.Start == end && end > start {
			inside = false
		}
		if inside {
			out = append(out, e)
		}
	}
	return out
}

// Apply applies sorted, non-overlapping edits to src.
func Apply(src []byte, edits []Edit) []byte {
	out := make([]byte, 0, len(src))
	pos := 0
	for _, e := range edits {
		out = append(out, src[pos:e.Start]...)
		out = append(out, e.NewText...)
		pos = e.End
	}
	return append(out, src[pos:]...)
}

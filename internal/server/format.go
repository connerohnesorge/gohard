package server

import (
	"errors"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/format"
	"github.com/connerohnesorge/gohard/internal/parser"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

// WithFormatting returns an analyzer that provides whole-file and range
// formatting (#32) and delegates every other feature to an. It formats
// with the functions `gohard fmt` uses: parser.FromConfig and
// format.FromConfig on the snapshot's configuration, format.Parses,
// then format.Format or format.Range, so the editor and the CLI give
// the same text. toolVersion is the gohard version (TOOL_VERSION for
// conditional analysis), as for the CLI.
//
// A document that is not VHDL (by languageId or extension) or that
// does not parse (format/not-parsed in `gohard fmt`) gets no edits.
func WithFormatting(an Analyzer, toolVersion string) Analyzer {
	if an == nil {
		an = Nop{}
	}
	return formatting{Analyzer: an, version: toolVersion}
}

type formatting struct {
	Analyzer
	version string
}

// Features implements Analyzer.
func (f formatting) Features() Features {
	fs := f.Analyzer.Features()
	fs.Set |= FeatureFormatting | FeatureRangeFormatting
	return fs
}

// Format implements Analyzer. It takes two slices: the parse, then the
// layout, so a cancel or an edit between them stops the work.
func (f formatting) Format(snap *Snapshot, uri string, r *Span,
) Task[[]Edit] {
	return TaskFunc[[]Edit](func(int) Slice[[]Edit] {
		d, ok := snap.Document(uri)
		if !ok || !isVHDL(d) {
			return Slice[[]Edit]{Done: true}
		}
		res := parser.Parse(d.Text(),
			parser.FromConfig(snap.Config, f.version))
		if !format.Parses(res) {
			return Slice[[]Edit]{Done: true}
		}
		opts := format.FromConfig(snap.Config)
		return Slice[[]Edit]{Next: TaskFunc[[]Edit](
			func(int) Slice[[]Edit] {
				return Slice[[]Edit]{Done: true,
					Value: FormatEdits(res, opts, r)}
			})}
	})
}

// FormatEdits returns the formatting edits of a parsed file: those of
// format.Format for the whole file (r nil), else those of format.Range,
// which all lie inside r.
func FormatEdits(res *parser.Result, opts format.Options, r *Span,
) []Edit {
	var es []format.Edit
	if r == nil {
		es = format.Format(res, opts).Edits
	} else {
		es = format.Range(res, opts, r.Start, r.End)
	}
	out := make([]Edit, 0, len(es))
	for _, e := range es {
		out = append(out, Edit{Span: Span{Start: e.Start, End: e.End},
			NewText: e.NewText})
	}
	return out
}

func isVHDL(d *Document) bool {
	if d.LanguageID != "" {
		return d.LanguageID == "vhdl"
	}
	if d.Path != "" {
		return workspace.IsVHDL(d.Path)
	}
	return workspace.IsVHDL(d.URI)
}

// parseLineCol parses a one-based LINE:COL (COL in bytes) into a
// zero-based UTF-8 Position.
func parseLineCol(s string) (Position, bool) {
	l, c, ok := strings.Cut(s, ":")
	line, err1 := strconv.Atoi(l)
	col, err2 := strconv.Atoi(c)
	if !ok || err1 != nil || err2 != nil || line < 1 || col < 1 {
		return Position{}, false
	}
	return Position{Line: line - 1, Character: col - 1}, true
}

func init() {
	RegisterAction(Action{
		Name: "range-format", Method: "textDocument/rangeFormatting",
		Summary: "formatting edits inside [--at, END)",
		Args:    "END_LINE:END_COL",
		Params: func(uri string, pos Position, args []string) (any,
			error,
		) {
			if len(args) != 1 {
				return nil, errors.New("gohard lsp range-format needs " +
					"one argument, the range end LINE:COL")
			}
			end, ok := parseLineCol(args[0])
			if !ok {
				return nil, errors.New("gohard lsp range-format: the " +
					"range end must be LINE:COL with a line and column " +
					"of at least 1, not " + strconv.Quote(args[0]))
			}
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Range   Range          `json:"range"`
				Options map[string]any `json:"options"`
			}
			p.TextDocument.URI = uri
			p.Range = Range{Start: pos, End: end}
			p.Options = map[string]any{"tabSize": 2, "insertSpaces": true}
			return p, nil
		},
	})
}

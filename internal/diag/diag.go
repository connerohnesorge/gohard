// Package diag is the single source of truth for GoHard diagnostics
// (#20): the table of every diagnostic code with its default severity,
// exit class and message template, the Diagnostic value, deterministic
// sorting, the exit-code rules, and the text, JSON (schema 1), SARIF
// 2.1.0 and JUnit XML renderers.
//
// Codes and message wording are locked when v1.0 ships. Until then this
// table is the only place they are defined; fixtures are refreshed with
// `go test -update`.
package diag

import (
	"cmp"
	"slices"
	"strings"
)

// Severity is how serious a diagnostic is. The order of the constants is
// the sort order: errors first.
type Severity uint8

// Severities. LSP "information" is not used by GoHard.
const (
	Error Severity = iota + 1
	Warning
	Hint
)

// String returns "error", "warning" or "hint".
func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	case Hint:
		return "hint"
	}
	return "unknown"
}

// ParseSeverity is the inverse of Severity.String.
func ParseSeverity(s string) (Severity, bool) {
	for _, v := range []Severity{Error, Warning, Hint} {
		if v.String() == s {
			return v, true
		}
	}
	return 0, false
}

// Class says how a diagnostic code takes part in the exit code (#20).
type Class uint8

// Exit classes.
const (
	// Design problems: an error exits 1, a warning exits 1 only with
	// --strict, and a hint never fails a run.
	Design Class = iota + 1
	// Tool or setup failures, and opaque units: GoHard could not give a
	// trustworthy answer, so the run exits 2. Always severity error.
	Tool
	// Unchecked units: the run is at least exit 1 whatever its other
	// diagnostics say, because a run with unchecked units never exits 0.
	// Always severity warning.
	Unchecked
)

// String returns the class name used in docs and tests.
func (c Class) String() string {
	switch c {
	case Design:
		return "design"
	case Tool:
		return "tool"
	case Unchecked:
		return "unchecked"
	}
	return "unknown"
}

// Code is a diagnostic code such as "syntax/standard": a lowercase area,
// a slash, and a lowercase kebab-case name.
type Code string

// Area returns the part of the code before the slash.
func (c Code) Area() string {
	area, _, _ := strings.Cut(string(c), "/")
	return area
}

// Info returns the table entry for c. ok is false for unknown codes.
func (c Code) Info() (Info, bool) {
	i, ok := index[c]
	if !ok {
		return Info{}, false
	}
	return table[i], true
}

// Related is a secondary location attached to a diagnostic (for example
// the first declaration of a duplicate unit).
type Related struct {
	Path    string
	Line    int
	Col     int
	EndLine int
	EndCol  int
	Message string
}

// Diagnostic is one reported problem.
//
// Path is relative to the project root and uses '/' separators; it is
// empty for diagnostics about the run itself (bad usage, a crash). Line
// and Col are one-based and Col counts UTF-8 bytes, like `gohard lsp
// --at` (#26); a zero Line means the diagnostic covers the whole file.
// EndLine and EndCol give the exclusive end, or zero when unknown.
// Severity starts as the table default for Code; producers may lower it
// only where an issue says so (for example sim/budget-exceeded is a
// warning in the editor, #35).
type Diagnostic struct {
	Path     string
	Line     int
	Col      int
	EndLine  int
	EndCol   int
	Severity Severity
	Code     Code
	Message  string
	Related  []Related
}

// New returns a diagnostic for code with its default severity and the
// message built from the code's template and args (see Format). An
// unknown code gives an internal/unknown-code diagnostic so a typo can
// never be silently dropped (P6).
func New(code Code, args ...string) Diagnostic {
	info, ok := code.Info()
	if !ok {
		return New(InternalUnknownCode, string(code))
	}
	return Diagnostic{
		Severity: info.Severity,
		Code:     code,
		Message:  info.Format(args...),
	}
}

// NewVariant is New with a named message variant (see Info.Variants).
func NewVariant(code Code, variant string, args ...string) Diagnostic {
	d := New(code, args...)
	info, ok := code.Info()
	if !ok {
		return d
	}
	d.Message = info.FormatVariant(variant, args...)
	return d
}

// At returns a copy of d located at path:line:col. A diagnostic with a
// path always has a position (#20: text is file:line:col): a line or
// column below 1, as for a file-level diagnostic, becomes 1, so
// file-level diagnostics sit at 1:1 in text, JSON, SARIF and the LSP.
func (d Diagnostic) At(path string, line, col int) Diagnostic {
	if path != "" {
		line, col = max(line, 1), max(col, 1)
	}
	d.Path, d.Line, d.Col = path, line, col
	return d
}

// To returns a copy of d with the exclusive end endLine:endCol.
func (d Diagnostic) To(endLine, endCol int) Diagnostic {
	d.EndLine, d.EndCol = endLine, endCol
	return d
}

// WithRelated returns a copy of d with r appended to its related
// locations.
func (d Diagnostic) WithRelated(r ...Related) Diagnostic {
	d.Related = append(slices.Clip(d.Related), r...)
	return d
}

// Class returns the exit class of d's code (Tool for unknown codes).
func (d Diagnostic) Class() Class {
	info, ok := d.Code.Info()
	if !ok {
		return Tool
	}
	return info.Class
}

// Compare orders diagnostics by path (pathless first), line, column, end,
// severity (errors first), code, message, and then related locations.
// It is a total order, so sorting is deterministic.
func Compare(a, b Diagnostic) int {
	if c := cmp.Or(
		cmp.Compare(a.Path, b.Path),
		cmp.Compare(a.Line, b.Line),
		cmp.Compare(a.Col, b.Col),
		cmp.Compare(a.EndLine, b.EndLine),
		cmp.Compare(a.EndCol, b.EndCol),
		cmp.Compare(a.Severity, b.Severity),
		cmp.Compare(a.Code, b.Code),
		cmp.Compare(a.Message, b.Message),
	); c != 0 {
		return c
	}
	return slices.CompareFunc(a.Related, b.Related, compareRelated)
}

func compareRelated(a, b Related) int {
	return cmp.Or(
		cmp.Compare(a.Path, b.Path),
		cmp.Compare(a.Line, b.Line),
		cmp.Compare(a.Col, b.Col),
		cmp.Compare(a.EndLine, b.EndLine),
		cmp.Compare(a.EndCol, b.EndCol),
		cmp.Compare(a.Message, b.Message),
	)
}

// Normalize returns ds sorted by Compare with exact duplicates removed.
// The input slice is not modified.
func Normalize(ds []Diagnostic) []Diagnostic {
	out := slices.Clone(ds)
	slices.SortStableFunc(out, Compare)
	return slices.CompactFunc(out, func(a, b Diagnostic) bool {
		return Compare(a, b) == 0
	})
}

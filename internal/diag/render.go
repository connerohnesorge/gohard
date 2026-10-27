package diag

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Format is an output format (#20, #61).
type Format string

// Output formats.
const (
	FormatText  Format = "text"
	FormatJSON  Format = "json"
	FormatSARIF Format = "sarif"
	FormatJUnit Format = "junit"
)

// Formats lists every output format in documentation order.
var Formats = []Format{FormatText, FormatJSON, FormatSARIF, FormatJUnit}

// ParseFormat checks a --format value.
func ParseFormat(s string) (Format, bool) {
	f := Format(s)
	return f, slices.Contains(Formats, f)
}

// Render renders r in format f. Output depends only on r: diagnostics are
// normalized first, so input order never matters (P1). An unknown format
// renders as text.
func Render(f Format, r *Report) []byte {
	switch f {
	case FormatJSON:
		return RenderJSON(r)
	case FormatSARIF:
		return RenderSARIF(r)
	case FormatJUnit:
		return RenderJUnit(r)
	}
	return RenderText(r)
}

// RenderText renders one line per diagnostic,
// `file:line:col: severity[code]: message`, related locations indented
// below it, and the summary line with all four counts.
func RenderText(r *Report) []byte {
	var b bytes.Buffer
	for _, d := range Normalize(r.Diagnostics) {
		writeText(&b, d)
	}
	b.WriteString(SummaryLine(r.Counts()))
	b.WriteByte('\n')
	return b.Bytes()
}

// writeText writes the text form of d: its line and one indented note
// line per related location.
func writeText(b *bytes.Buffer, d Diagnostic) {
	fmt.Fprintf(b, "%s: %s[%s]: %s\n",
		location(d.Path, d.Line, d.Col), d.Severity, d.Code,
		escapeText(d.Message))
	for _, rel := range d.Related {
		fmt.Fprintf(b, "  %s: note: %s\n",
			location(rel.Path, rel.Line, rel.Col),
			escapeText(rel.Message))
	}
}

// SummaryLine is the text summary. The plural forms are fixed so the line
// is easy to match.
func SummaryLine(c Counts) string {
	return fmt.Sprintf("summary: %d errors, %d warnings, %d hints, "+
		"%d files", c.Errors, c.Warnings, c.Hints, c.Files)
}

// location is "path:line:col", shortened when parts are unknown; a
// diagnostic about the run itself is located at "gohard".
func location(path string, line, col int) string {
	switch {
	case path == "":
		return "gohard"
	}
	// Always file:line:col (#20); At already places file-level
	// diagnostics at 1:1, this covers literals built without At.
	line, col = max(line, 1), max(col, 1)
	return escapeText(path) + ":" + strconv.Itoa(line) + ":" +
		strconv.Itoa(col)
}

// escapeText keeps one diagnostic on one line: a control character or a
// Unicode line break in a path or message (a file may be named
// "a\nb.vhd") is written as a Go-style escape, so it can never start a
// forged diagnostic line. Text without such characters is unchanged.
func escapeText(s string) string {
	if !strings.ContainsFunc(s, needsEscape) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case needsEscape(r) && r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		case needsEscape(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func needsEscape(r rune) bool {
	return r < 0x20 || r == 0x7f || r >= 0x80 && r < 0xa0 ||
		r == 0x2028 || r == 0x2029
}

// The JSON form (schema 1). Struct field order is the key order.
type jsonReport struct {
	Schema      int        `json:"schema"`
	Status      Status     `json:"status"`
	Exit        int        `json:"exit"`
	Summary     jsonCounts `json:"summary"`
	Diagnostics []jsonDiag `json:"diagnostics"`
	Incomplete  []string   `json:"incomplete"`
	Error       *jsonError `json:"error,omitempty"`
}

type jsonCounts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Hints    int `json:"hints"`
	Files    int `json:"files"`
}

type jsonDiag struct {
	Path     string        `json:"path"`
	Line     int           `json:"line"`
	Col      int           `json:"col"`
	EndLine  int           `json:"end_line"`
	EndCol   int           `json:"end_col"`
	Severity string        `json:"severity"`
	Code     Code          `json:"code"`
	Message  string        `json:"message"`
	Related  []jsonRelated `json:"related"`
}

type jsonRelated struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"end_line"`
	EndCol  int    `json:"end_col"`
	Message string `json:"message"`
}

type jsonError struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

// RenderJSON renders the schema-1 JSON form: schema, status, exit,
// summary, diagnostics, incomplete, and error (only on exit 2).
func RenderJSON(r *Report) []byte {
	ds := Normalize(r.Diagnostics)
	c := r.Counts()
	exit := r.Exit()
	out := jsonReport{
		Schema:      1,
		Status:      StatusOf(exit),
		Exit:        exit,
		Summary:     jsonCounts(c),
		Diagnostics: make([]jsonDiag, 0, len(ds)),
		Incomplete:  sortedUnique(r.Incomplete),
	}
	for _, d := range ds {
		jd := jsonDiag{
			Path: d.Path, Line: d.Line, Col: d.Col,
			EndLine: d.EndLine, EndCol: d.EndCol,
			Severity: d.Severity.String(), Code: d.Code,
			Message: d.Message,
			Related: make([]jsonRelated, 0, len(d.Related)),
		}
		for _, rel := range d.Related {
			jd.Related = append(jd.Related, jsonRelated(rel))
		}
		out.Diagnostics = append(out.Diagnostics, jd)
	}
	if exit == ExitTool {
		if d, ok := firstTool(ds); ok {
			out.Error = &jsonError{Code: d.Code, Message: d.Message}
		}
	}
	return marshal(out)
}

func sortedUnique(xs []string) []string {
	out := slices.Clone(xs)
	slices.Sort(out)
	out = slices.Compact(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// marshal encodes v with two-space indentation, no HTML escaping and a
// trailing newline.
func marshal(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		// Only plain structs of strings and ints are encoded here.
		panic("diag: " + err.Error())
	}
	return b.Bytes()
}

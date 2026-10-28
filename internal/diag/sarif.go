package diag

import (
	"cmp"
	"net/url"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/span"
)

// SARIF 2.1.0 output. Struct field order is the key order, so the bytes
// depend only on the report.

const (
	sarifSchema  = "https://json.schemastore.org/sarif-2.1.0.json"
	sarifVersion = "2.1.0"
	sarifInfoURI = "https://github.com/connerohnesorge/gohard"
)

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}

type sarifRun struct {
	Tool        sarifTool         `json:"tool"`
	Invocations []sarifInvocation `json:"invocations"`
	Results     []sarifResult     `json:"results"`
	ColumnKind  string            `json:"columnKind"`
}

type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

type sarifDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version,omitempty"`
	InformationURI string      `json:"informationUri"`
	Rules          []sarifRule `json:"rules"`
}

type sarifRule struct {
	ID                   string       `json:"id"`
	ShortDescription     sarifMessage `json:"shortDescription"`
	DefaultConfiguration sarifConfig  `json:"defaultConfiguration"`
}

type sarifConfig struct {
	Level string `json:"level"`
}

type sarifMessage struct {
	Text string `json:"text"`
}

type sarifInvocation struct {
	ExecutionSuccessful bool       `json:"executionSuccessful"`
	ExitCode            int        `json:"exitCode"`
	Notes               sarifNotes `json:"toolExecutionNotifications,omitempty"`
}

type sarifNotes = []sarifNotification

type sarifNotification struct {
	Level      string          `json:"level"`
	Message    sarifMessage    `json:"message"`
	Descriptor sarifDescriptor `json:"descriptor"`
	Locations  []sarifLocation `json:"locations,omitempty"`
}

type sarifDescriptor struct {
	ID string `json:"id"`
}

type sarifResult struct {
	RuleID           string          `json:"ruleId"`
	Level            string          `json:"level"`
	Message          sarifMessage    `json:"message"`
	Locations        []sarifLocation `json:"locations,omitempty"`
	RelatedLocations []sarifLocation `json:"relatedLocations,omitempty"`
}

type sarifLocation struct {
	ID               *int                  `json:"id,omitempty"`
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
	Message          *sarifMessage         `json:"message,omitempty"`
}

type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifact `json:"artifactLocation"`
	Region           *sarifRegion  `json:"region,omitempty"`
}

type sarifArtifact struct {
	URI string `json:"uri"`
}

type sarifRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn,omitempty"`
	EndLine     int `json:"endLine,omitempty"`
	EndColumn   int `json:"endColumn,omitempty"`
}

// RenderSARIF renders a SARIF 2.1.0 log with one run. The rules list the
// codes that occur, sorted. On exit 2, invocations[0].executionSuccessful
// is false and every Tool-class diagnostic is repeated in
// toolExecutionNotifications as the reason (#20).
//
// SARIF columns count UTF-16 code units (columnKind utf16CodeUnits, the
// SARIF default, stated explicitly) while GoHard columns count UTF-8
// bytes. Columns are converted with the file text from r.Source; a
// column after the first of a file whose text is unknown is left out,
// so the region gives the line only and is never wrong.
func RenderSARIF(r *Report) []byte {
	ds := Normalize(r.Diagnostics)
	cols := columns{source: r.Source, index: map[string]*span.Index{}}
	exit := r.Exit()
	run := sarifRun{
		Tool: sarifTool{Driver: sarifDriver{
			Name:           cmp.Or(r.Tool, "gohard"),
			Version:        r.Version,
			InformationURI: sarifInfoURI,
			Rules:          []sarifRule{},
		}},
		Invocations: []sarifInvocation{{
			ExecutionSuccessful: exit == ExitClean ||
				exit == ExitProblems,
			ExitCode: exit,
		}},
		Results:    make([]sarifResult, 0, len(ds)),
		ColumnKind: "utf16CodeUnits",
	}
	var codes []Code
	for _, d := range ds {
		codes = append(codes, d.Code)
		res := sarifResult{
			RuleID:  string(d.Code),
			Level:   sarifLevel(d.Severity),
			Message: sarifMessage{Text: d.Message},
		}
		if loc, ok := cols.loc(d.Path, d.Line, d.Col, d.EndLine,
			d.EndCol); ok {
			res.Locations = []sarifLocation{loc}
		}
		for i, rel := range d.Related {
			loc, ok := cols.loc(rel.Path, rel.Line, rel.Col,
				rel.EndLine, rel.EndCol)
			if !ok {
				continue
			}
			id := i
			loc.ID = &id
			loc.Message = &sarifMessage{Text: rel.Message}
			res.RelatedLocations = append(res.RelatedLocations, loc)
		}
		run.Results = append(run.Results, res)
		if exit == ExitTool && d.Class() == Tool {
			n := sarifNotification{
				Level:      "error",
				Message:    sarifMessage{Text: d.Message},
				Descriptor: sarifDescriptor{ID: string(d.Code)},
				Locations:  res.Locations,
			}
			inv := &run.Invocations[0]
			inv.Notes = append(inv.Notes, n)
		}
	}
	slices.Sort(codes)
	for _, c := range slices.Compact(codes) {
		info, _ := c.Info()
		run.Tool.Driver.Rules = append(run.Tool.Driver.Rules, sarifRule{
			ID:               string(c),
			ShortDescription: sarifMessage{Text: info.Summary},
			DefaultConfiguration: sarifConfig{
				Level: sarifLevel(info.Severity),
			},
		})
	}
	return marshal(sarifLog{
		Schema:  sarifSchema,
		Version: sarifVersion,
		Runs:    []sarifRun{run},
	})
}

// columns converts UTF-8 byte columns to UTF-16 code units.
type columns struct {
	source func(string) ([]byte, bool)
	index  map[string]*span.Index // by path; nil: text unknown
}

// utf16 returns the one-based UTF-16 column of the one-based byte
// column col of line in path, or 0 (left out) when it is unknown.
// Columns 0 and 1 mean the same in every encoding.
func (c *columns) utf16(path string, line, col int) int {
	if col <= 1 || line < 1 {
		return max(col, 0)
	}
	x, seen := c.index[path]
	if !seen {
		if c.source != nil {
			if src, ok := c.source(path); ok {
				x = span.NewIndex(src)
			}
		}
		c.index[path] = x
	}
	if x == nil || line > x.LineCount() {
		return 0
	}
	off, ok := x.OffsetOf(line, col)
	if !ok {
		return 0
	}
	return x.Position(off, span.UTF16).Col + 1
}

func (c *columns) loc(path string, line, col, endLine, endCol int) (
	sarifLocation, bool,
) {
	if line > 0 {
		col = c.utf16(path, line, col)
		endCol = c.utf16(path, cmp.Or(max(endLine, 0), line), endCol)
		if col == 0 {
			endCol = 0
		}
	}
	return sarifLoc(path, line, col, endLine, endCol)
}

func sarifLoc(path string, line, col, endLine, endCol int) (
	sarifLocation, bool,
) {
	if path == "" {
		return sarifLocation{}, false
	}
	loc := sarifLocation{PhysicalLocation: sarifPhysicalLocation{
		ArtifactLocation: sarifArtifact{URI: sarifURI(path)},
	}}
	if line > 0 {
		loc.PhysicalLocation.Region = &sarifRegion{
			StartLine:   line,
			StartColumn: max(col, 0),
			EndLine:     max(endLine, 0),
			EndColumn:   max(endCol, 0),
		}
	}
	return loc, true
}

// sarifURI turns a slash path into an RFC 3986 relative reference
// (SARIF 2.1.0 §3.4.3): each segment is percent-encoded, so a space,
// '#', '%' or '?' in a file name cannot start a fragment, an escape or
// a query, and ':' is encoded so a first segment never reads as a
// scheme.
func sarifURI(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = strings.ReplaceAll(url.PathEscape(s), ":", "%3A")
	}
	return strings.Join(segs, "/")
}

func sarifLevel(s Severity) string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	}
	return "note"
}

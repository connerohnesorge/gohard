package diag

import (
	"bytes"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestExitCodeTable(t *testing.T) {
	var (
		syntax   = New(SyntaxError, "x").At("a.vhd", 1, 1)
		warn     = New(WorkspaceFileTooLarge, "size", "10 MB")
		hint     = New(SyntaxUTF8Comment).At("a.vhd", 2, 3)
		tool     = New(CLIUsage, "bad flag")
		opaque   = NewVariant(UnsupportedConstruct, PlannedV11, "x")
		unchk    = New(SemaUnchecked, "work.a", "work.b")
		allowed  = New(SemaOpaqueAllowed, "unisim.x")
		simWarn  = New(SimAssertWarning, "assert", "w")
		simNote  = New(SimAssertNote, "report", "n")
		simError = New(SimAssertError, "assert", "failure", "f")
	)
	tests := []struct {
		name   string
		ds     []Diagnostic
		strict bool
		sig    Signal
		want   int
	}{
		{"empty", nil, false, NoSignal, 0},
		{"empty strict", nil, true, NoSignal, 0},
		{"hint", []Diagnostic{hint}, false, NoSignal, 0},
		{"hint strict", []Diagnostic{hint}, true, NoSignal, 0},
		{"warning", []Diagnostic{warn}, false, NoSignal, 0},
		{"warning strict", []Diagnostic{warn}, true, NoSignal, 1},
		{"error", []Diagnostic{syntax}, false, NoSignal, 1},
		{"tool", []Diagnostic{tool}, false, NoSignal, 2},
		{"tool wins", []Diagnostic{syntax, tool}, false, NoSignal, 2},
		{"tool wins strict", []Diagnostic{warn, tool}, true, NoSignal, 2},
		{"opaque", []Diagnostic{opaque}, false, NoSignal, 2},
		{"unchecked", []Diagnostic{unchk}, false, NoSignal, 1},
		{"allowed opaque + unchecked", []Diagnostic{allowed, unchk},
			false, NoSignal, 1},
		{"allowed opaque alone", []Diagnostic{allowed}, true,
			NoSignal, 0},
		{"sim warning", []Diagnostic{simWarn}, false, NoSignal, 0},
		{"sim warning strict", []Diagnostic{simWarn}, true, NoSignal, 1},
		{"sim note strict", []Diagnostic{simNote}, true, NoSignal, 0},
		{"sim error", []Diagnostic{simError}, false, NoSignal, 1},
		{"sigint", []Diagnostic{tool}, false, SIGINT, 130},
		{"sigterm", nil, false, SIGTERM, 143},
		{"downgraded budget in editor", []Diagnostic{func() Diagnostic {
			d := New(SimBudgetExceeded, "delta", "10000", "1 ns")
			d.Severity = Warning
			return d
		}()}, false, NoSignal, 0},
	}
	for _, tt := range tests {
		if got := ExitCode(tt.ds, tt.strict, tt.sig); got != tt.want {
			t.Errorf("%s: ExitCode = %d, want %d", tt.name, got, tt.want)
		}
	}
}

// sample builds diagnostics from every table row with varied locations.
func sample() []Diagnostic {
	var ds []Diagnostic
	paths := []string{"", "b.vhd", "a.vhd", "lib/c.vhd"}
	for i, info := range Table() {
		d := New(info.Code, "x")
		if p := paths[i%len(paths)]; p != "" {
			d = d.At(p, i%7, i%3).To(i%7+1, 1)
		}
		ds = append(ds, d)
	}
	return ds
}

func shuffled(ds []Diagnostic, seed uint64) []Diagnostic {
	out := slices.Clone(ds)
	s := seed
	for i := len(out) - 1; i > 0; i-- {
		s = s*6364136223846793005 + 1442695040888963407
		j := int((s >> 33) % uint64(i+1))
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// rank orders exit codes by how bad they are, for the monotonicity law.
func rank(code int) int {
	return map[int]int{0: 0, 1: 1, 2: 2, 130: 3, 143: 3}[code]
}

func TestExitCodeLaws(t *testing.T) {
	all := sample()
	for n := 0; n <= len(all); n++ {
		for _, strict := range []bool{false, true} {
			base := all[:n]
			code := ExitCode(base, strict, NoSignal)
			// Only defined codes, independent of order.
			if !slices.Contains([]int{0, 1, 2}, code) {
				t.Fatalf("undefined exit code %d", code)
			}
			for seed := range uint64(4) {
				if got := ExitCode(shuffled(base, seed), strict,
					NoSignal); got != code {
					t.Fatalf("n=%d: order changed exit %d -> %d",
						n, code, got)
				}
			}
			// Adding diagnostics never lowers the exit code.
			if n > 0 {
				prev := ExitCode(all[:n-1], strict, NoSignal)
				if rank(code) < rank(prev) {
					t.Fatalf("n=%d: adding %v lowered exit %d -> %d",
						n, all[n-1].Code, prev, code)
				}
			}
			// Hints never fail a run.
			hint := New(SyntaxUTF8Comment)
			if got := ExitCode(append(slices.Clone(base), hint),
				strict, NoSignal); got != code {
				t.Fatalf("n=%d: a hint changed exit %d -> %d",
					n, code, got)
			}
			// --strict only ever raises a 0 to a 1.
			lax := ExitCode(base, false, NoSignal)
			strictCode := ExitCode(base, true, NoSignal)
			if lax != strictCode && !(lax == 0 && strictCode == 1) {
				t.Fatalf("n=%d: strict changed %d -> %d",
					n, lax, strictCode)
			}
		}
	}
}

func TestNormalize(t *testing.T) {
	ds := sample()
	ds = append(ds, ds[3], ds[5]) // exact duplicates collapse
	want := Normalize(ds)
	if len(want) != len(sample()) {
		t.Fatalf("Normalize kept %d of %d", len(want), len(sample()))
	}
	if !slices.IsSortedFunc(want, Compare) {
		t.Fatal("Normalize output not sorted")
	}
	for seed := range uint64(8) {
		got := Normalize(shuffled(ds, seed))
		if !slices.EqualFunc(got, want, func(a, b Diagnostic) bool {
			return Compare(a, b) == 0
		}) {
			t.Fatalf("seed %d: Normalize depends on input order", seed)
		}
	}
	if !slices.EqualFunc(Normalize(want), want,
		func(a, b Diagnostic) bool { return Compare(a, b) == 0 }) {
		t.Fatal("Normalize not idempotent")
	}
}

func TestRenderersIgnoreInputOrder(t *testing.T) {
	ds := sample()
	for _, f := range Formats {
		want := Render(f, &Report{Diagnostics: ds, Files: 3})
		for seed := range uint64(6) {
			got := Render(f, &Report{
				Diagnostics: shuffled(ds, seed), Files: 3,
			})
			if !bytes.Equal(got, want) {
				t.Fatalf("%s: seed %d changed the output", f, seed)
			}
		}
	}
}

func TestRenderText(t *testing.T) {
	r := &Report{
		Files: 2,
		Diagnostics: []Diagnostic{
			New(SyntaxUTF8Comment).At("b.vhd", 3, 1),
			New(LibraryDuplicateUnit, "work.top", "work", "a.vhd",
				"b.vhd").At("b.vhd", 1, 8).WithRelated(Related{
				Path: "a.vhd", Line: 1, Col: 8,
				Message: "first declared here",
			}),
			New(CLIMissingPath, "c.vhd"),
			New(WorkspaceFileTooLarge, "size", "10 MB").At("big.vhd",
				0, 0),
		},
	}
	want := "" +
		"gohard: error[cli/missing-path]: c.vhd does not exist\n" +
		"b.vhd:1:8: error[library/duplicate-unit]: unit work.top is " +
		"declared twice in library work: a.vhd and b.vhd\n" +
		"  a.vhd:1:8: note: first declared here\n" +
		"b.vhd:3:1: hint[syntax/utf8-comment]: non-ASCII text in a " +
		"comment is not portable under VHDL-1993\n" +
		"big.vhd:1:1: warning[workspace/file-too-large]: file not parsed: " +
		"size exceeds the limit of 10 MB\n" +
		"summary: 2 errors, 1 warnings, 1 hints, 2 files\n"
	if got := string(RenderText(r)); got != want {
		t.Errorf("RenderText:\n%s\nwant:\n%s", got, want)
	}
	if got := string(RenderText(&Report{})); got !=
		"summary: 0 errors, 0 warnings, 0 hints, 0 files\n" {
		t.Errorf("empty RenderText = %q", got)
	}
}

func TestRenderJSON(t *testing.T) {
	clean := RenderJSON(&Report{Files: 1, Incomplete: []string{
		"tb.b", "tb.a", "tb.b",
	}})
	want := `{
  "schema": 1,
  "status": "clean",
  "exit": 0,
  "summary": {
    "errors": 0,
    "warnings": 0,
    "hints": 0,
    "files": 1
  },
  "diagnostics": [],
  "incomplete": [
    "tb.a",
    "tb.b"
  ]
}
`
	if string(clean) != want {
		t.Errorf("clean JSON:\n%s\nwant:\n%s", clean, want)
	}
	cases := []struct {
		r      Report
		status string
		err    bool
	}{
		{Report{Diagnostics: []Diagnostic{New(SyntaxError, "<x>")}},
			"problems", false},
		{Report{Diagnostics: []Diagnostic{New(SyntaxError, "x"),
			New(CLIUsage, "y")}}, "tool_error", true},
		{Report{Diagnostics: []Diagnostic{New(CLIUsage, "y")},
			Signal: SIGINT}, "interrupted", false},
	}
	for _, c := range cases {
		out := RenderJSON(&c.r)
		var m map[string]any
		if err := json.Unmarshal(out, &m); err != nil {
			t.Fatalf("invalid JSON: %v\n%s", err, out)
		}
		if m["status"] != c.status {
			t.Errorf("status = %v, want %s", m["status"], c.status)
		}
		if _, has := m["error"]; has != c.err {
			t.Errorf("%s: error present = %v", c.status, has)
		}
		if strings.Contains(string(out), `\u003c`) {
			t.Error("JSON must not HTML-escape")
		}
	}
	// Fixed key order.
	out := string(RenderJSON(&cases[1].r))
	keys := []string{`"schema"`, `"status"`, `"exit"`, `"summary"`,
		`"diagnostics"`, `"incomplete"`, `"error"`}
	last := -1
	for _, k := range keys {
		i := strings.Index(out, "\n  "+k)
		if i <= last {
			t.Fatalf("key %s out of order in\n%s", k, out)
		}
		last = i
	}
}

func TestRenderSARIF(t *testing.T) {
	type inv struct {
		ExecutionSuccessful        bool `json:"executionSuccessful"`
		ExitCode                   int  `json:"exitCode"`
		ToolExecutionNotifications []struct {
			Descriptor struct{ ID string } `json:"descriptor"`
		} `json:"toolExecutionNotifications"`
	}
	type log struct {
		Version string
		Runs    []struct {
			Tool struct {
				Driver struct {
					Rules []struct{ ID string }
				}
			}
			Invocations []inv
			Results     []struct {
				RuleID string `json:"ruleId"`
				Level  string
			}
		}
	}
	parse := func(r *Report) log {
		var l log
		if err := json.Unmarshal(RenderSARIF(r), &l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	l := parse(&Report{Diagnostics: []Diagnostic{
		New(SyntaxUTF8Comment).At("a.vhd", 1, 1),
		New(SyntaxError, "x").At("a.vhd", 2, 1),
		New(SyntaxError, "y").At("a.vhd", 3, 1),
	}})
	run := l.Runs[0]
	if l.Version != "2.1.0" || !run.Invocations[0].ExecutionSuccessful ||
		run.Invocations[0].ExitCode != 1 {
		t.Fatalf("bad SARIF header: %+v", l)
	}
	if len(run.Tool.Driver.Rules) != 2 || len(run.Results) != 3 ||
		run.Results[0].Level != "note" {
		t.Fatalf("rules/results: %+v", run)
	}
	l = parse(&Report{Diagnostics: []Diagnostic{
		New(SyntaxError, "x").At("a.vhd", 2, 1),
		New(StdlibIntegrity, "ieee/numeric_std.vhdl"),
	}})
	in := l.Runs[0].Invocations[0]
	if in.ExecutionSuccessful || in.ExitCode != 2 ||
		len(in.ToolExecutionNotifications) != 1 ||
		in.ToolExecutionNotifications[0].Descriptor.ID !=
			string(StdlibIntegrity) {
		t.Fatalf("exit-2 invocation: %+v", in)
	}
}

// TestNormalizeTieBreakers: diagnostics that differ in a single field,
// including the end position and the related locations, are distinct.
// Normalize keeps every one, and the rendered bytes, compared as bytes
// and not through Compare, do not depend on the input order.
func TestNormalizeTieBreakers(t *testing.T) {
	base := New(LibraryDuplicateUnit, "work.top", "work", "a.vhd",
		"b.vhd").At("b.vhd", 3, 4).To(3, 9)
	rel := Related{Path: "a.vhd", Line: 1, Col: 2, EndLine: 1,
		EndCol: 5, Message: "first"}
	edits := []func(*Diagnostic){
		func(d *Diagnostic) { d.EndLine++ },
		func(d *Diagnostic) { d.EndCol++ },
		func(d *Diagnostic) { d.Related = []Related{rel} },
		func(d *Diagnostic) { d.Related = []Related{rel, rel} },
	}
	relEdits := []func(*Related){
		func(r *Related) { r.Path = "c.vhd" },
		func(r *Related) { r.Line++ },
		func(r *Related) { r.Col++ },
		func(r *Related) { r.EndLine++ },
		func(r *Related) { r.EndCol++ },
		func(r *Related) { r.Message = "second" },
	}
	for _, e := range relEdits {
		edits = append(edits, func(d *Diagnostic) {
			r := rel
			e(&r)
			d.Related = []Related{r}
		})
	}
	ds := []Diagnostic{base}
	for _, e := range edits {
		d := base
		e(&d)
		ds = append(ds, d)
	}
	if got := len(Normalize(ds)); got != len(ds) {
		t.Fatalf("Normalize kept %d of %d distinct diagnostics", got,
			len(ds))
	}
	if got := len(Normalize(append(ds, ds...))); got != len(ds) {
		t.Fatalf("Normalize kept %d duplicates of %d", got, len(ds))
	}
	for _, f := range Formats {
		want := Render(f, &Report{Diagnostics: ds})
		for seed := range uint64(24) {
			got := Render(f, &Report{Diagnostics: shuffled(ds, seed)})
			if !bytes.Equal(got, want) {
				t.Fatalf("%s: seed %d changed the bytes:\n%s\nwant:\n%s",
					f, seed, got, want)
			}
		}
	}
}

// TestFileLevelPosition: #20 fixes text as file:line:col, so a
// diagnostic with a path always carries a position of at least 1:1.
func TestFileLevelPosition(t *testing.T) {
	for _, lc := range [][2]int{{0, 0}, {-3, 0}, {0, 7}, {4, 0}, {2, 3}} {
		d := New(WorkspaceUnreadable, "f.vhd", "x").At("f.vhd", lc[0],
			lc[1])
		if d.Line < 1 || d.Col < 1 || (lc[0] >= 1 && d.Line != lc[0]) ||
			(lc[0] >= 1 && lc[1] >= 1 && d.Col != lc[1]) {
			t.Errorf("At(%d, %d) = %d:%d", lc[0], lc[1], d.Line, d.Col)
		}
		line := string(RenderText(&Report{Diagnostics: []Diagnostic{d}}))
		if !strings.HasPrefix(line, "f.vhd:"+strconv.Itoa(d.Line)+":"+
			strconv.Itoa(d.Col)+": error[") {
			t.Errorf("text %q", line)
		}
	}
	// A literal built without At still prints file:line:col.
	lit := Diagnostic{Path: "g.vhd", Severity: Error,
		Code: WorkspaceUnreadable, Message: "m"}
	if got := string(RenderText(&Report{Diagnostics: []Diagnostic{
		lit}})); !strings.HasPrefix(got, "g.vhd:1:1: error[") {
		t.Errorf("literal text %q", got)
	}
	if d := New(CLIUsage, "x").At("", 0, 0); d.Line != 0 || d.Col != 0 {
		t.Errorf("pathless diagnostic got a position %d:%d", d.Line,
			d.Col)
	}
}

// TestSARIFURI: artifact URIs are RFC 3986 relative references; every
// path maps to a distinct URI that decodes back to the path.
func TestSARIFURI(t *testing.T) {
	for path, want := range map[string]string{
		"src/a.vhd":        "src/a.vhd",
		"my file#1%.vhd":   "my%20file%231%25.vhd",
		"a:b/c?d.vhd":      "a%3Ab/c%3Fd.vhd",
		"../ext/x.vhd":     "../ext/x.vhd",
		"d/é.vhd":          "d/%C3%A9.vhd",
		"/abs/semi;co.vhd": "/abs/semi%3Bco.vhd",
	} {
		got := sarifURI(path)
		if got != want {
			t.Errorf("sarifURI(%q) = %q, want %q", path, got, want)
		}
		u, err := url.Parse(got)
		if err != nil || u.Scheme != "" || u.Fragment != "" ||
			u.RawQuery != "" || u.Path != path {
			t.Errorf("%q: parses as %+v (%v)", got, u, err)
		}
	}
	out := string(RenderSARIF(&Report{Diagnostics: []Diagnostic{
		New(SyntaxError, "x").At("my file#1%.vhd", 1, 1)}}))
	if !strings.Contains(out, `"uri": "my%20file%231%25.vhd"`) {
		t.Errorf("SARIF uri not encoded:\n%s", out)
	}
}

// TestCountsMatchRendered: the summary counts are the diagnostics that
// are printed. Normalize drops exact duplicates, so a duplicated
// diagnostic is counted once, in every format.
func TestCountsMatchRendered(t *testing.T) {
	a := New(SyntaxError, "x").At("a.vhd", 1, 1)
	w := New(SyntaxUTF8Comment).At("a.vhd", 2, 1)
	for _, ds := range [][]Diagnostic{
		{a, a}, {a, w, a, w, w}, {w}, {a, a, a, New(CLIUsage, "u"),
			New(CLIUsage, "u")},
	} {
		r := &Report{Diagnostics: ds}
		c := r.Counts()
		n := len(Normalize(ds))
		if c.Errors+c.Warnings+c.Hints != n {
			t.Errorf("counts %+v for %d printed diagnostics", c, n)
		}
		text := string(RenderText(r))
		if lines := strings.Count(text, "\n") - 1; lines != n {
			t.Errorf("text prints %d lines, want %d", lines, n)
		}
		var j struct {
			Summary     struct{ Errors, Warnings, Hints int }
			Diagnostics []json.RawMessage
		}
		if err := json.Unmarshal(RenderJSON(r), &j); err != nil {
			t.Fatal(err)
		}
		if len(j.Diagnostics) != n || j.Summary.Errors != c.Errors ||
			j.Summary.Warnings != c.Warnings ||
			j.Summary.Hints != c.Hints {
			t.Errorf("JSON %+v, want %d diagnostics %+v", j, n, c)
		}
		var s struct {
			Runs []struct{ Results []json.RawMessage }
		}
		if err := json.Unmarshal(RenderSARIF(r), &s); err != nil {
			t.Fatal(err)
		}
		if got := len(s.Runs[0].Results); got != n {
			t.Errorf("SARIF has %d results, want %d", got, n)
		}
	}
}

// TestTextOneLinePerDiagnostic: whatever bytes a path or message holds,
// the text form prints exactly one line per diagnostic (plus one per
// related note and the summary), so a file named with a newline cannot
// forge a diagnostic line. Plain text is printed unchanged.
func TestTextOneLinePerDiagnostic(t *testing.T) {
	evil := "src/evil\nx.vhd:9:9: error[fake]: injected.vhd"
	d := New(WorkspaceUnreadable, evil, "permission denied").At(evil, 1,
		1)
	got := string(RenderText(&Report{Diagnostics: []Diagnostic{d}}))
	want := `src/evil\nx.vhd:9:9: error[fake]: injected.vhd:1:1: ` +
		`error[workspace/unreadable]: cannot read src/evil\nx.vhd:9:9: ` +
		`error[fake]: injected.vhd: permission denied` + "\n" +
		"summary: 1 errors, 0 warnings, 0 hints, 0 files\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	g := uint64(7)
	pick := func(n int) int {
		g = g*6364136223846793005 + 1442695040888963407
		return int(g>>33) % n
	}
	alphabet := []rune{'a', '/', ':', '\n', '\r', '\t', 0, 0x1b, 0x7f,
		0x85, 0x2028, 0x2029, 'é', '\\'}
	for range 2000 {
		var p, m []rune
		for range pick(12) {
			p = append(p, alphabet[pick(len(alphabet))])
		}
		for range pick(12) {
			m = append(m, alphabet[pick(len(alphabet))])
		}
		d := Diagnostic{Path: string(p), Line: 1, Col: 1, Severity: Error,
			Code: WorkspaceUnreadable, Message: string(m),
			Related: []Related{{Path: string(p), Line: 1, Col: 1,
				Message: string(m)}}}
		text := string(RenderText(&Report{Diagnostics: []Diagnostic{d}}))
		lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
		if len(lines) != 3 || strings.ContainsAny(text, "\r\x00\x7f") ||
			strings.ContainsRune(text, 0x2028) {
			t.Fatalf("path %q message %q gave %q", d.Path, d.Message,
				text)
		}
	}
	if escapeText("src/é ok.vhd") != "src/é ok.vhd" {
		t.Error("plain text was changed")
	}
}

// TestSARIFColumnsUTF16: SARIF columns count UTF-16 code units. With the
// file text, byte columns are converted (a column after "éé" or an
// astral rune moves left); without it, a column after the first is left
// out rather than given in the wrong unit.
func TestSARIFColumnsUTF16(t *testing.T) {
	src := []byte("x\n{\"éé\": \"x\", lenient: yes}\r\n\U0001F600ab\n")
	type region struct {
		StartLine, StartColumn, EndLine, EndColumn int
	}
	render := func(source func(string) ([]byte, bool),
		ds ...Diagnostic,
	) []region {
		var log struct {
			Runs []struct {
				ColumnKind string
				Results    []struct {
					Locations []struct {
						PhysicalLocation struct{ Region region }
					}
				}
			}
		}
		out := RenderSARIF(&Report{Diagnostics: ds, Source: source})
		if err := json.Unmarshal(out, &log); err != nil {
			t.Fatal(err)
		}
		if log.Runs[0].ColumnKind != "utf16CodeUnits" {
			t.Fatalf("columnKind %q", log.Runs[0].ColumnKind)
		}
		var rs []region
		for _, r := range log.Runs[0].Results {
			rs = append(rs, r.Locations[0].PhysicalLocation.Region)
		}
		return rs
	}
	of := func(p string) ([]byte, bool) { return src, p == "gohard.yaml" }
	yes := New(ConfigBadBool, "lenient", "yes").At("gohard.yaml", 2, 24)
	yes.EndLine, yes.EndCol = 2, 27
	smile := New(ConfigBadBool, "a", "b").At("gohard.yaml", 3, 5)
	got := render(of, yes, smile)
	want := []region{{2, 22, 2, 25}, {3, 3, 0, 0}}
	if !slices.Equal(got, want) {
		t.Errorf("with source: %v, want %v", got, want)
	}
	got = render(nil, yes, New(ConfigBadBool, "a", "b").At("o.vhd", 4,
		1))
	want = []region{{2, 0, 2, 0}, {4, 1, 0, 0}}
	if !slices.Equal(got, want) {
		t.Errorf("without source: %v, want %v", got, want)
	}
	// Property: on any line, the converted column is one more than the
	// UTF-16 length of the bytes before the byte column.
	g := uint64(11)
	pick := func(n int) int {
		g = g*6364136223846793005 + 1442695040888963407
		return int(g>>33) % n
	}
	runes := []string{"a", "é", "€", "\U0001F600", ":", " "}
	for range 500 {
		var line strings.Builder
		for range pick(10) {
			line.WriteString(runes[pick(len(runes))])
		}
		text := line.String()
		var starts []int
		for i := range text {
			starts = append(starts, i)
		}
		starts = append(starts, len(text))
		b := starts[pick(len(starts))]
		d := New(ConfigBadBool, "a", "b").At("f", 1, b+1)
		f := func(string) ([]byte, bool) { return []byte(text), true }
		r := render(f, d)[0]
		units := 0
		for _, c := range text[:b] {
			units += len(utf16.Encode([]rune{c}))
		}
		if wantCol := units + 1; r.StartColumn != wantCol {
			t.Fatalf("%q byte col %d: got %d, want %d", text, b+1,
				r.StartColumn, wantCol)
		}
	}
}

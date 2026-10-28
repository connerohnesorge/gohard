package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/format"
	"github.com/connerohnesorge/gohard/v2/internal/parser"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// The format section (#32): GHDL and NVC analyze every corpus file as
// written and as formatted, and the golden files record that the
// formatted text adds no errors.

// fieldFormat is the corpus.yaml hash field of the format section.
const fieldFormat = "format_oracle_sha256"

// toolVersion is TOOL_VERSION for conditional analysis while
// formatting (the corpus does not test it).
const toolVersion = "0.0.0-dev"

// analyzer is how one tool analyzes one file; psl is psl.comments.
type analyzer struct {
	tool   string
	golden string
	args   func(std token.Standard, defs []config.Define, psl bool,
		file string) []string
	parse func(out, file string) []toolDiag
}

var analyzers = []analyzer{
	{tool: "ghdl", golden: "format.ghdl.txt",
		args: func(std token.Standard, _ []config.Define, psl bool,
			file string) []string {
			// GHDL has no user conditional-analysis identifiers.
			a := append([]string{"-a", "--std=" + ghdlStd(std)},
				pslFlags("ghdl", psl)...)
			return append(a, file)
		},
		parse: parseGHDL},
	{tool: "nvc", golden: "format.nvc.txt",
		args: func(std token.Standard, defs []config.Define, psl bool,
			file string) []string {
			a := append([]string{"--std=" + std.String(), "-a"},
				pslFlags("nvc", psl)...)
			for _, d := range defs {
				a = append(a, "-D", d.Name+"="+d.Value)
			}
			return append(a, file)
		},
		parse: parseNVC},
}

func ghdlStd(s token.Standard) string {
	return fmt.Sprintf("%02d", int(s)%100)
}

var formatSection = &section{
	name: "format",
	targets: func(x *ctx) ([]target, error) {
		var ts []target
		for _, d := range x.man.Designs {
			var names []string
			for _, a := range analyzers {
				names = append(names, a.golden)
			}
			ts = append(ts, target{
				Ref: entryRef{List: "designs", Name: d.Name,
					Field: fieldFormat, After: "oracle_sha256"},
				Dir:   path.Join(d.dir(), "golden"),
				Files: names,
			})
		}
		return ts, nil
	},
	record: recordFormat,
	check:  checkFormat,
}

// fmtFile is one corpus file, formatted under the standard of its
// first listing (the main run's, for a file the variants repeat), as
// gohard fmt formats it in the design's project. A variant that runs
// the file under another standard analyzes that same formatted text.
type fmtFile struct {
	path   string // relative to the design directory
	std    token.Standard
	src    []byte
	out    []byte // the formatted text, or src when not parsed
	edits  []format.Edit
	parsed bool
}

func (f *fmtFile) state() string {
	switch {
	case !f.parsed:
		return "not-parsed"
	case bytes.Equal(f.src, f.out):
		return "unchanged"
	}
	return "changed"
}

// fmtRun is one analysis run: files analyzed in order under one
// standard, as the design (main) or one of its variants.
type fmtRun struct {
	name    string
	std     token.Standard
	defines []config.Define // the config's, or a variant's --define
	files   []*fmtFile
}

// fmtPlan is the pure part of the format section for one design.
type fmtPlan struct {
	opts format.Options
	psl  bool // psl.comments
	runs []fmtRun
}

// planFormat formats the files of design name. The main run analyzes
// the stand-in models first, then `files`; each variant is a run of
// its own files.
func planFormat(x *ctx, name string) (*fmtPlan, error) {
	if p, ok := x.plans["format/"+name].(*fmtPlan); ok {
		return p, nil
	}
	d, ok := x.design(name)
	if !ok {
		return nil, fmt.Errorf("no design %s", name)
	}
	cfg := config.Defaults()
	if d.Config != "" {
		src, err := readRepo(x.root, path.Join(d.dir(), d.Config))
		if err != nil {
			return nil, err
		}
		c, ds := config.Parse(config.FileName, src)
		for _, dg := range ds {
			if dg.Severity == diag.Error {
				return nil, fmt.Errorf("%s: %s:%d:%d: %s", name,
					d.Config, dg.Line, dg.Col, dg.Message)
			}
		}
		cfg = c
	}
	p := &fmtPlan{opts: format.FromConfig(cfg), psl: cfg.PSL.Comments}
	cache := map[string]*fmtFile{}
	file := func(e fileEntry) (*fmtFile, error) {
		std, ok := token.ParseStandard(e.Standard)
		if !ok {
			return nil, fmt.Errorf("%s: %s: standard %q", name, e.Path,
				e.Standard)
		}
		if f := cache[e.Path]; f != nil {
			return f, nil // the first listing's standard wins
		}
		src, err := readRepo(x.root, path.Join(d.dir(), e.Path))
		if err != nil {
			return nil, err
		}
		f := &fmtFile{path: e.Path, std: std, src: src, out: src}
		popts := parser.FromConfig(cfg, toolVersion)
		popts.Standard = std
		if res := parser.Parse(src, popts); format.Parses(res) {
			opts := p.opts
			opts.Standard = std
			r := format.Format(res, opts)
			f.out, f.edits, f.parsed = r.Text, r.Edits, true
		}
		cache[e.Path] = f
		return f, nil
	}
	addRun := func(rn, rs string, over map[string]string,
		entries []fileEntry) error {
		std, ok := token.ParseStandard(rs)
		if !ok {
			return fmt.Errorf("%s: run %s: standard %q", name, rn, rs)
		}
		defs, err := runDefines(cfg, over)
		if err != nil {
			return fmt.Errorf("%s: run %s: %w", name, rn, err)
		}
		r := fmtRun{name: rn, std: std, defines: defs}
		for _, e := range entries {
			f, err := file(e)
			if err != nil {
				return err
			}
			r.files = append(r.files, f)
		}
		p.runs = append(p.runs, r)
		return nil
	}
	var main []fileEntry
	for _, m := range d.modelPaths() {
		main = append(main, fileEntry{Path: m, Standard: d.Standard})
	}
	main = append(main, d.Files...)
	if err := addRun("main", d.Standard, nil, main); err != nil {
		return nil, err
	}
	for _, v := range d.Variants {
		if err := addRun(v.Name, v.Standard, v.Defines,
			v.Files); err != nil {
			return nil, err
		}
	}
	x.plans["format/"+name] = p
	return p, nil
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// planLines are the golden lines that depend only on GoHard and the
// corpus: options, runs and files. checkFormat compares them.
func (p *fmtPlan) planLines() []string {
	o := p.opts
	out := []string{fmt.Sprintf("options indent=%d keyword_case=%s "+
		"align_ports=%t max_line_length=%d", o.Indent, o.KeywordCase,
		o.AlignPorts, o.MaxLineLength)}
	for _, r := range p.runs {
		out = append(out, p.runLine(r))
		for _, f := range r.files {
			out = append(out, fileLine(f))
		}
	}
	return out
}

func (p *fmtPlan) runLine(r fmtRun) string {
	s := "run " + r.name + " std=" + r.std.String()
	for _, d := range r.defines {
		s += " define=" + d.Name + "=" + d.Value
	}
	if p.psl {
		s += " psl"
	}
	return s
}

func fileLine(f *fmtFile) string {
	return fmt.Sprintf("file %s std=%s sha256=%s formatted=%s %s",
		f.path, f.std, sha(f.src), sha(f.out), f.state())
}

func isPlanLine(l string) bool {
	return strings.HasPrefix(l, "options ") ||
		strings.HasPrefix(l, "run ") || strings.HasPrefix(l, "file ")
}

const formatHeader = `# GoHard formatter oracle (#32): %s, %s.
# Written by go run ./tools/oracle --update; do not edit.
# Every file is formatted once with the design's format.* options
# under the standard it is first listed with (variants reuse that
# text). The original and the formatted files of each run are analyzed
# in order, one file per command, in two fresh work directories. Error
# lines of the formatted text are mapped back to the original lines;
# "new" lists errors only the formatted text has, "gone" errors only
# the original has. The check needs "result new=0".
`

func recordFormat(x *ctx, tl *toolset, t target) ([]goldenFile, error) {
	p, err := planFormat(x, t.Ref.Name)
	if err != nil {
		return nil, err
	}
	var out []goldenFile
	for _, a := range analyzers {
		tt, err := tl.get(a.tool)
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, formatHeader, t.Ref.Name, a.tool)
		fmt.Fprintf(&b, "tool %s %s\n", a.tool, tt.version)
		lines := p.planLines()
		b.WriteString(lines[0] + "\n")
		news := 0
		for _, r := range p.runs {
			b.WriteString(p.runLine(r) + "\n")
			n, err := p.analyzeRun(&b, a, tt, r)
			if err != nil {
				return nil, err
			}
			news += n
		}
		fmt.Fprintf(&b, "result new=%d\n", news)
		out = append(out, goldenFile{Name: a.golden,
			Data: []byte(b.String())})
	}
	return out, nil
}

// fileResult is one file's analysis: exit code and errors.
type fileResult struct {
	exit  int
	diags []toolDiag
}

// analyzeRun analyzes the original and the formatted files of r and
// writes one block per file. It returns the number of new errors.
func (p *fmtPlan) analyzeRun(b *strings.Builder, a analyzer, tt *tool,
	r fmtRun) (int, error) {
	orig, err := p.analyzeAll(a, tt, r, false)
	if err != nil {
		return 0, err
	}
	formatted, err := p.analyzeAll(a, tt, r, true)
	if err != nil {
		return 0, err
	}
	byPath := map[string]*fmtFile{}
	for _, f := range r.files {
		byPath[f.path] = f
	}
	news := 0
	for i, f := range r.files {
		o, fm := orig[i], formatted[i]
		fmt.Fprintf(b, "%s\n  exit original=%d formatted=%d\n",
			fileLine(f), o.exit, fm.exit)
		// Map formatted positions back to original lines.
		mapped := make([]toolDiag, len(fm.diags))
		for j, d := range fm.diags {
			if g := byPath[d.File]; g != nil && d.Line > 0 {
				d.Line = g.origLine(d.Line, d.Col)
			}
			mapped[j] = d
		}
		for _, d := range o.diags {
			fmt.Fprintf(b, "  error %s\n", diagText(f, d))
		}
		added := minus(mapped, o.diags)
		for _, j := range added {
			d := fm.diags[j]
			fmt.Fprintf(b, "  new %s (formatted line %d)\n",
				diagText(f, mapped[j]), d.Line)
		}
		for _, j := range minus(o.diags, mapped) {
			fmt.Fprintf(b, "  gone %s\n", diagText(f, o.diags[j]))
		}
		news += len(added)
		if len(added) == 0 && o.exit == 0 && fm.exit != 0 {
			fmt.Fprintf(b, "  new exit %d without an error line\n",
				fm.exit)
			news++
		}
	}
	return news, nil
}

func diagText(f *fmtFile, d toolDiag) string {
	at := strconv.Itoa(d.Line)
	if d.File != f.path {
		at = d.File + ":" + at
	}
	return at + ": " + d.Msg
}

// minus returns the indexes of the items of a left over after taking
// out one equal item of b for each item of b (multiset difference by
// file, line and message key).
func minus(a, b []toolDiag) []int {
	count := map[string]int{}
	key := func(d toolDiag) string {
		return d.File + "\x00" + strconv.Itoa(d.Line) + "\x00" +
			msgKey(d.Msg)
	}
	for _, d := range b {
		count[key(d)]++
	}
	var out []int
	for i, d := range a {
		k := key(d)
		if count[k] > 0 {
			count[k]--
			continue
		}
		out = append(out, i)
	}
	return out
}

// analyzeAll analyzes the files of r in order in a fresh work
// directory, as written or as formatted.
func (p *fmtPlan) analyzeAll(a analyzer, tt *tool, r fmtRun,
	formatted bool) ([]fileResult, error) {
	files := map[string][]byte{}
	var order []string
	for _, f := range r.files {
		if _, dup := files[f.path]; dup {
			return nil, fmt.Errorf("run %s lists %s twice", r.name,
				f.path)
		}
		files[f.path] = f.src
		if formatted {
			files[f.path] = f.out
		}
		order = append(order, f.path)
	}
	w, err := newWorkDir(files, order)
	if err != nil {
		return nil, err
	}
	defer w.close()
	var out []fileResult
	for _, f := range r.files {
		text, code, err := tt.run(w.dir, a.args(r.std, r.defines, p.psl,
			f.path)...)
		if err != nil {
			return nil, err
		}
		out = append(out, fileResult{exit: code,
			diags: a.parse(text, f.path)})
	}
	return out, nil
}

// origLine maps a 1-based line (and 1-based byte column, 0 for none)
// of the formatted text back to the line of the original text.
func (f *fmtFile) origLine(line, col int) int {
	if !f.parsed {
		return line
	}
	starts := lineStarts(f.out)
	if line > len(starts) {
		return line
	}
	o := starts[line-1]
	end := len(f.out)
	if line < len(starts) {
		end = starts[line] - 1
	}
	if col > 0 {
		o = min(o+col-1, end)
	} else {
		for o < end && (f.out[o] == ' ' || f.out[o] == '\t') {
			o++
		}
	}
	in := backOffset(f.edits, o)
	return bytes.Count(f.src[:in], []byte("\n")) + 1
}

// backOffset maps an offset in the edited text to the original text.
// An offset inside replaced text maps to the same distance into the
// old text, or to the end of the old text when that is shorter.
func backOffset(edits []format.Edit, o int) int {
	delta := 0 // formatted minus original, before the current edit
	for _, e := range edits {
		start := e.Start + delta
		if o < start {
			break
		}
		if k := o - start; k < len(e.NewText) {
			return e.Start + min(k, e.End-e.Start)
		}
		delta += len(e.NewText) - (e.End - e.Start)
	}
	return o - delta
}

// checkFormat compares the recorded runs and files with the current
// formatter output and requires that no new errors were recorded.
func checkFormat(x *ctx, t target, golden []goldenFile) []string {
	p, err := planFormat(x, t.Ref.Name)
	if err != nil {
		return []string{err.Error()}
	}
	want := p.planLines()
	var probs []string
	for _, g := range golden {
		var have []string
		result := ""
		for _, l := range strings.Split(string(g.Data), "\n") {
			if isPlanLine(l) {
				have = append(have, l)
			}
			if strings.HasPrefix(l, "result ") {
				result = l
			}
		}
		if i := firstDiff(have, want); i >= 0 {
			probs = append(probs, fmt.Sprintf("%s/%s: formatted output "+
				"changed and no matching result is recorded (want %q, "+
				"recorded %q): %s", t.Dir, g.Name, at(want, i),
				at(have, i), rerun))
			continue
		}
		if result != "result new=0" {
			probs = append(probs, fmt.Sprintf("%s/%s: the formatted "+
				"text adds errors (%q); fix the formatter, then %s",
				t.Dir, g.Name, result, rerun))
		}
	}
	return probs
}

func firstDiff(a, b []string) int {
	for i := range max(len(a), len(b)) {
		if i >= len(a) || i >= len(b) || a[i] != b[i] {
			return i
		}
	}
	return -1
}

func at(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "<end>"
}

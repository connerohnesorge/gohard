package main

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/format"
	"go.yaml.in/yaml/v3"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRecorded is the CI gate (#30, #32): the recorded oracle results
// match the current GoHard output and their hashes are in corpus.yaml.
// It runs no tool.
func TestRecorded(t *testing.T) {
	probs, err := doCheck(repoRoot(t), sections, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range probs {
		t.Error(p)
	}
}

func TestSectionsRegistered(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"-list"}, &out, &out); code != 0 {
		t.Fatalf("exit %d: %s", code, out.String())
	}
	if out.String() != "format\nsemerr\ntrace\ntree\ngolden\n"+
		"coverage\nstdlib\nconformance\n" {
		t.Fatalf("sections %q", out.String())
	}
	if code := run([]string{"nope"}, &out, &out); code != 2 {
		t.Fatalf("unknown section: exit %d", code)
	}
}

func TestGoldenHash(t *testing.T) {
	a := []goldenFile{{"x", []byte("1")}, {"y", []byte("22")}}
	b := []goldenFile{{"y", []byte("22")}, {"x", []byte("1")}}
	if goldenHash(a) != goldenHash(b) {
		t.Error("hash depends on input order")
	}
	// Framing: moving bytes between files or names changes the hash.
	c := []goldenFile{{"x", []byte("12")}, {"y", []byte("2")}}
	d := []goldenFile{{"x1", nil}, {"y", []byte("22")}}
	seen := map[string]bool{}
	for _, fs := range [][]goldenFile{a, c, d, nil} {
		h := goldenHash(fs)
		if seen[h] || len(h) != 64 {
			t.Errorf("hash %q repeats or is malformed", h)
		}
		seen[h] = true
	}
}

// TestSetFieldOnCorpus: on the real corpus.yaml, setting any hash field
// of any entry changes only that scalar's bytes, reads back, and leaves
// the parsed manifest otherwise equal; removing a field's line and
// setting it again restores the file byte for byte.
func TestSetFieldOnCorpus(t *testing.T) {
	src, err := readRepo(repoRoot(t), manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := parseManifest(src)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for _, d := range m.Designs {
		for _, field := range []string{"trace_sha256", "oracle_sha256",
			fieldFormat} {
			ref := entryRef{List: "designs", Name: d.Name, Field: field,
				After: "oracle_sha256"}
			old, ok, err := fieldValue(src, ref)
			if err != nil || !ok {
				t.Fatalf("%s: %v %v", ref, ok, err)
			}
			val := fmt.Sprintf("%064x", rng.Uint64())
			out, changed, err := setField(src, ref, val)
			if err != nil || !changed {
				t.Fatalf("%s: %v %v", ref, changed, err)
			}
			checkSplice(t, ref, src, out, old, val)
			again, changed, err := setField(out, ref, val)
			if err != nil || changed || !bytes.Equal(again, out) {
				t.Fatalf("%s: second set changed the file", ref)
			}
			m2, err := parseManifest(out)
			if err != nil || !reflect.DeepEqual(m, m2) {
				t.Fatalf("%s: manifest changed: %v", ref, err)
			}
		}
		// Insertion round trip.
		ref := entryRef{List: "designs", Name: d.Name, Field: fieldFormat,
			After: "oracle_sha256"}
		val, _, _ := fieldValue(src, ref)
		cut := removeFieldLine(t, src, d.Name, fieldFormat)
		if _, ok, _ := fieldValue(cut, ref); ok {
			t.Fatalf("%s: line not removed", ref)
		}
		back, changed, err := setField(cut, ref, val)
		if err != nil || !changed || !bytes.Equal(back, src) {
			t.Fatalf("%s: insert did not restore the file: %v", ref, err)
		}
	}
}

// checkSplice: out differs from src only where old became val.
func checkSplice(t *testing.T, ref entryRef, src, out []byte, old,
	val string) {
	t.Helper()
	p := 0
	for p < len(src) && p < len(out) && src[p] == out[p] {
		p++
	}
	s := 0
	for s < len(src)-p && s < len(out)-p &&
		src[len(src)-1-s] == out[len(out)-1-s] {
		s++
	}
	// The common prefix and suffix may eat a shared leading or
	// trailing part of old and val.
	gotOld := string(src[p : len(src)-s])
	gotNew := string(out[p : len(out)-s])
	ok := false
	for i := 0; i <= len(old) && i <= len(val) && old[:i] == val[:i]; i++ {
		o, v := old[i:], val[i:]
		if strings.HasPrefix(o, gotOld) && strings.HasPrefix(v, gotNew) &&
			o[len(gotOld):] == v[len(gotNew):] {
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("%s: changed %q to %q, want %q to %q", ref, gotOld,
			gotNew, old, val)
	}
	v, ok, err := fieldValue(out, ref)
	if err != nil || !ok || v != val {
		t.Fatalf("%s: reads back %q %v %v", ref, v, ok, err)
	}
}

// removeFieldLine deletes the field's line from the named entry.
func removeFieldLine(t *testing.T, src []byte, name, field string) []byte {
	t.Helper()
	ref := entryRef{List: "designs", Name: name, Field: field}
	_, val, err := locate(src, ref)
	if err != nil || val == nil {
		t.Fatalf("%s: %v", ref, err)
	}
	lines := lineStarts(src)
	start := lines[val.Line-1]
	end := lines[val.Line]
	return append(bytes.Clone(src[:start]), src[end:]...)
}

func TestSetFieldKeepsLayout(t *testing.T) {
	src := []byte(`# top comment
designs:
  - name: a   # trailing
    oracle_sha256: "pending"  # quoted, commented
    notes: >-
      folded
      text
  - name: b
    oracle_sha256: 'pending'
    format_oracle_sha256: pending # keep me
`)
	a := entryRef{List: "designs", Name: "a", Field: fieldFormat,
		After: "oracle_sha256"}
	out, _, err := setField(src, a, "abc")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(string(src), "commented\n",
		"commented\n    format_oracle_sha256: abc\n", 1)
	if string(out) != want {
		t.Fatalf("insert:\n%s", out)
	}
	for _, c := range []struct{ ref entryRef }{
		{entryRef{List: "designs", Name: "a", Field: "oracle_sha256"}},
		{entryRef{List: "designs", Name: "b", Field: "oracle_sha256"}},
		{entryRef{List: "designs", Name: "b", Field: fieldFormat}},
	} {
		got, _, err := setField(out, c.ref, "0123")
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(got, []byte("0123")) != 1 ||
			len(got)-len(out) != 4-len("pending")-quoteLen(out, c.ref) {
			t.Fatalf("%s:\n%s", c.ref, got)
		}
		if !bytes.Contains(got, []byte("# keep me")) ||
			!bytes.Contains(got, []byte("# trailing")) {
			t.Fatalf("%s lost a comment:\n%s", c.ref, got)
		}
	}
	if _, _, err := setField(src, entryRef{List: "designs", Name: "zz",
		Field: "x"}, "1"); err == nil {
		t.Error("unknown entry: no error")
	}
	if _, _, err := setField(src, entryRef{List: "designs", Name: "a",
		Field: "x", After: "notes"}, "1"); err == nil {
		t.Error("multi-line anchor: no error")
	}
}

func quoteLen(src []byte, ref entryRef) int {
	_, v, _ := locate(src, ref)
	if v.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
		return 2
	}
	return 0
}

func TestParseToolOutput(t *testing.T) {
	ghdl := "a.vhd:12:5:error: missing \";\"\n" +
		"  x <= 1\n    ^\n" +
		"a.vhd:13:1:warning: unused\n" +
		"ghdl:error: compilation error\n"
	got := parseGHDL(ghdl, "a.vhd")
	want := []toolDiag{{"a.vhd", 12, 5, `missing ";"`},
		{"a.vhd", 0, 0, "compilation error"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ghdl: %+v", got)
	}
	nvc := "** Error: unexpected force\n    > a.vhd:12\n    |\n" +
		" 12 |  force\n" +
		"** Warning: w\n    > a.vhd:3\n" +
		"** Fatal: no position\n" +
		"** Error: depends on b.vhd:4\n    > b.vhd:4\n"
	got = parseNVC(nvc, "a.vhd")
	want = []toolDiag{{"a.vhd", 12, 0, "unexpected force"},
		{"a.vhd", 0, 0, "no position"},
		{"b.vhd", 4, 0, "depends on b.vhd:4"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("nvc: %+v", got)
	}
	if msgKey("Seen at X.vhd:3:4 and y.vhdl:9") !=
		"seen at x.vhd and y.vhdl" {
		t.Error(msgKey("Seen at X.vhd:3:4 and y.vhdl:9"))
	}
	if v, err := parseVersion("ghdl",
		"GHDL 6.0.0 (tarball) [Dunoon edition]\n"); v != "6.0.0" ||
		err != nil {
		t.Error(v, err)
	}
	if v, err := parseVersion("nvc", "nvc 1.23.0 (x)\n"); v != "1.23.0" ||
		err != nil {
		t.Error(v, err)
	}
	if _, err := parseVersion("nvc", "other 1\n"); err == nil {
		t.Error("bad version line accepted")
	}
}

// TestBackOffset: for random whitespace edits, every byte outside the
// edits maps back to itself.
func TestBackOffset(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 4))
	for range 500 {
		var src []byte
		for range rng.IntN(30) {
			if rng.IntN(2) == 0 {
				src = append(src, strings.Repeat(" \n\t"[rng.IntN(3):][:1],
					rng.IntN(4))...)
			} else {
				src = append(src, "abc;("[rng.IntN(5)])
			}
		}
		var edits []format.Edit
		for i := 0; i < len(src); i++ {
			if rng.IntN(3) != 0 {
				continue
			}
			j := min(i+rng.IntN(3), len(src))
			e := format.Edit{Start: i, End: j,
				NewText: strings.Repeat(" ", rng.IntN(3))}
			if j == i && e.NewText == "" {
				continue
			}
			edits = append(edits, e)
			i = j // the next edit starts after this one
		}
		out := format.Apply(src, edits)
		fwd := forward(len(src), edits)
		for i, o := range fwd {
			if o < 0 {
				continue
			}
			if out[o] != src[i] {
				t.Fatalf("forward map wrong at %d", i)
			}
			if b := backOffset(edits, o); b != i {
				t.Fatalf("src %q edits %+v: %d -> %d -> %d", src, edits, i,
					o, b)
			}
		}
	}
}

// forward maps each original offset to the edited text, -1 for bytes
// the edits replace.
func forward(n int, edits []format.Edit) []int {
	fwd := make([]int, n)
	pos, o := 0, 0
	for _, e := range edits {
		for ; pos < e.Start; pos++ {
			fwd[pos] = o
			o++
		}
		for ; pos < e.End; pos++ {
			fwd[pos] = -1
		}
		o += len(e.NewText)
	}
	for ; pos < n; pos++ {
		fwd[pos] = o
		o++
	}
	return fwd
}

// TestOrigLineOnCorpus: the first word of every formatted corpus line
// maps back to an original line that holds the same word.
func TestOrigLineOnCorpus(t *testing.T) {
	x, err := loadCtx(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range x.man.Designs {
		p, err := planFormat(x, d.Name)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range p.runs {
			for _, f := range r.files {
				checkOrigLines(t, d.Name, f)
			}
		}
	}
}

func checkOrigLines(t *testing.T, name string, f *fmtFile) {
	t.Helper()
	orig := strings.Split(string(f.src), "\n")
	for i, l := range strings.Split(string(f.out), "\n") {
		w := strings.Fields(l)
		if len(w) == 0 {
			continue
		}
		col := strings.Index(l, w[0]) + 1
		for _, c := range []int{0, col} {
			ol := f.origLine(i+1, c)
			if ol < 1 || ol > len(orig) || !strings.Contains(
				strings.ToLower(orig[ol-1]), strings.ToLower(w[0])) {
				t.Fatalf("%s/%s: formatted line %d %q maps to %d", name,
					f.path, i+1, l, ol)
			}
		}
	}
}

// fakeTools stand in for GHDL and NVC: each reports one error per line
// whose first word is a lower-case "end".
func fakeTools() *toolset {
	fake := func(nvc bool) func(string, []string) (string, int) {
		return func(dir string, args []string) (string, int) {
			file := args[len(args)-1]
			b, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil {
				return err.Error(), 2
			}
			var out strings.Builder
			for i, l := range strings.Split(string(b), "\n") {
				if w := strings.Fields(l); len(w) == 0 || w[0] != "end" {
					continue
				}
				if nvc {
					fmt.Fprintf(&out, "** Error: lower end\n    > %s:%d\n",
						file, i+1)
				} else {
					fmt.Fprintf(&out, "%s:%d:%d:error: lower end\n", file,
						i+1, strings.Index(l, "end")+1)
				}
			}
			if out.Len() > 0 {
				return out.String(), 1
			}
			return "", 0
		}
	}
	return &toolset{found: map[string]*tool{
		"ghdl": {name: "ghdl", version: "6.0.0", fake: fake(false)},
		"nvc":  {name: "nvc", version: "1.23.0", fake: fake(true)},
	}}
}

const fakeManifest = `# comment kept
schema: 1
designs:
  - name: demo   # trailing comment
    standard: "2008"
    files:
      - path: a.vhd
        standard: "2008"
    config: gohard.yaml
    notes: >-
      folded
      text
    trace_sha256: pending
    oracle_sha256: pending   # later
regression: []
`

func fakeRepo(t *testing.T, vhd string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "testdata", "corpus", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, filepath.FromSlash(manifestPath)),
		fakeManifest)
	write(t, filepath.Join(dir, "a.vhd"), vhd)
	write(t, filepath.Join(dir, "gohard.yaml"), "format:\n  indent: 3\n")
	return root
}

func write(t *testing.T, name, s string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func update(t *testing.T, root string) string {
	t.Helper()
	var out bytes.Buffer
	// The fake repository tests the format section; semerr_test.go
	// drives the semerr section.
	if err := doUpdate(root, []*section{formatSection}, nil, fakeTools(),
		&out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func check(t *testing.T, root string) []string {
	t.Helper()
	probs, err := doCheck(root, []*section{formatSection}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return probs
}

func hasProblem(probs []string, sub string) bool {
	for _, p := range probs {
		if strings.Contains(p, sub) {
			return true
		}
	}
	return false
}

// TestUpdateAndCheck drives the whole flow with fake tools: record,
// check, staleness after a source, config or golden change, and the
// hash line in corpus.yaml.
func TestUpdateAndCheck(t *testing.T) {
	const clean = "entity a is\nend entity a;\n"
	root := fakeRepo(t, clean)
	if probs := check(t, root); !hasProblem(probs, "is not recorded: "+
		rerun) {
		t.Fatalf("before update: %q", probs)
	}
	log := update(t, root)
	if !strings.Contains(log, "wrote testdata/corpus/demo/golden/"+
		"format.ghdl.txt") || !strings.Contains(log, fieldFormat+" = ") {
		t.Fatalf("update log:\n%s", log)
	}
	if probs := check(t, root); len(probs) > 0 {
		t.Fatalf("after update: %q", probs)
	}
	man := readFile(t, root, manifestPath)
	want := strings.Replace(fakeManifest, "# later\n", "# later\n"+
		"    "+fieldFormat+": \n", 1)
	h, _, _ := fieldValue([]byte(man), entryRef{List: "designs",
		Name: "demo", Field: fieldFormat})
	if len(h) != 64 || strings.Replace(man, h, "", 1) != want {
		t.Fatalf("corpus.yaml:\n%s", man)
	}
	g := readFile(t, root, "testdata/corpus/demo/golden/format.nvc.txt")
	if !strings.Contains(g, "  error 2: lower end\n") ||
		!strings.Contains(g, "result new=0\n") {
		t.Fatalf("golden:\n%s", g)
	}
	// A second update changes nothing.
	if log := update(t, root); strings.Contains(log, "wrote") ||
		strings.Contains(log, " = ") {
		t.Fatalf("second update:\n%s", log)
	}

	// Formatted output changes (indent 4): stale until updated.
	write(t, filepath.Join(root, "testdata", "corpus", "demo",
		"a.vhd"), "entity a is\nport (x : in bit);\nend entity a;\n")
	update(t, root)
	write(t, filepath.Join(root, "testdata", "corpus", "demo",
		"gohard.yaml"), "format:\n  indent: 4\n")
	probs := check(t, root)
	if !hasProblem(probs, "formatted output changed and no matching "+
		"result is recorded") || !hasProblem(probs, rerun) {
		t.Fatalf("stale formatter output: %q", probs)
	}
	update(t, root)
	if probs := check(t, root); len(probs) > 0 {
		t.Fatalf("after re-update: %q", probs)
	}

	// A hand-edited golden or hash is caught.
	gp := filepath.Join(root, "testdata", "corpus", "demo", "golden",
		"format.ghdl.txt")
	b, _ := os.ReadFile(gp)
	write(t, gp, string(b)+"\n")
	if probs := check(t, root); !hasProblem(probs, "golden files hash "+
		"to") {
		t.Fatalf("edited golden: %q", probs)
	}
}

// TestNewErrorsFail: a formatter change the tools reject (here the
// fake tools reject the lower-cased END) is recorded as "new" on the
// original line and fails the check.
func TestNewErrorsFail(t *testing.T) {
	root := fakeRepo(t, "entity a is\n\n\n   END entity a;\n")
	update(t, root)
	g := readFile(t, root, "testdata/corpus/demo/golden/format.ghdl.txt")
	if !strings.Contains(g, "  new 4: lower end (formatted line 2)\n") ||
		!strings.Contains(g, "result new=1\n") {
		t.Fatalf("golden:\n%s", g)
	}
	probs := check(t, root)
	if len(probs) != 2 || !hasProblem(probs, "the formatted text adds "+
		"errors") {
		t.Fatalf("problems: %q", probs)
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := readRepo(root, rel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

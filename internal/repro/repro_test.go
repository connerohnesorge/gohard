package repro

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
)

func sample() Header {
	return Header{
		Version:   "1.0.0",
		Standard:  "2008",
		Testbench: "tb_top",
		Seed:      18446744073709551615,
		Defines:   map[string]string{"WIDTH": "8", "DEBUG": "<on>&"},
		Budgets:   Budgets{Time: "1 ms", Delta: 10000, Step: 50000000},
		Models:    map[string]string{"work.ram": "models/ram.vhd"},
		Config:    &File{Path: "gohard.yaml", SHA256: Hash([]byte("x"))},
		Sources: []File{
			{Path: "src/b.vhd", SHA256: Hash([]byte("b"))},
			{Path: "src/a.vhd", SHA256: Hash([]byte("a"))},
		},
	}
}

const golden = `{
  "schema": 1,
  "version": "1.0.0",
  "standard": "2008",
  "testbench": "tb_top",
  "seed": 18446744073709551615,
  "defines": {
    "DEBUG": "<on>&",
    "WIDTH": "8"
  },
  "budgets": {
    "time": "1 ms",
    "delta": 10000,
    "step": 50000000
  },
  "models": {
    "work.ram": "models/ram.vhd"
  },
  "config": {
    "path": "gohard.yaml",
    "sha256": "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881"
  },
  "sources": [
    {
      "path": "src/a.vhd",
      "sha256": "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"
    },
    {
      "path": "src/b.vhd",
      "sha256": "3e23e8160039594a33894f6564e1b1348bbd7a0088d42c4acb73eeaed59c009d"
    }
  ]
}
`

func TestGolden(t *testing.T) {
	got, err := Marshal(sample())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != golden {
		t.Fatalf("got\n%s", got)
	}
	line, err := MarshalLine(sample())
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	json.Compact(&want, []byte(golden))
	want.WriteByte('\n')
	if !bytes.Equal(line, want.Bytes()) {
		t.Fatalf("line form\n%s", line)
	}
}

// minBudgets are the smallest valid budgets.
var minBudgets = Budgets{Time: "0 fs", Delta: 1, Step: 1}

func TestEmptyCollections(t *testing.T) {
	got, err := MarshalLine(Header{Budgets: minBudgets})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":1,"version":"","standard":"","testbench":"",` +
		`"seed":0,"defines":{},"budgets":{"time":"0 fs","delta":1,` +
		`"step":1},` +
		`"models":{},"config":null,"sources":[]}` + "\n"
	if string(got) != want {
		t.Fatalf("got %s", got)
	}
}

// randomHeader builds a header from g, with sources in random order and
// strings that need JSON escaping.
func randomHeader(g *env.SplitMix64) Header {
	alphabet := []string{"a", "Z", "_", "\"", "\\", "\n", "é", "<", "1",
		" ", "/"}
	str := func() string {
		var b strings.Builder
		for range env.Intn(g, 6) {
			b.WriteString(alphabet[env.Intn(g, len(alphabet))])
		}
		return b.String()
	}
	stds := []string{"", "1993", "2002", "2008", "2019"}
	times := []string{"1 ms", "0 fs", "2.5 ns", "10 US", "1000 sec"}
	idents := []string{"WIDTH", "a", "Debug_1", "x_y", "z9"}
	h := Header{
		Version: str(), Testbench: str(),
		Standard: stds[env.Intn(g, len(stds))],
		Seed:     g.Uint64() >> env.Intn(g, 64),
		Budgets: Budgets{Time: times[env.Intn(g, len(times))],
			Delta: 1 + int64(g.Uint64()>>2),
			Step:  1 + int64(env.Intn(g, 100))},
	}
	if env.Intn(g, 2) == 0 {
		h.Defines = map[string]string{}
		for range env.Intn(g, 5) {
			h.Defines[idents[env.Intn(g, len(idents))]] = str()
		}
	}
	if env.Intn(g, 2) == 0 {
		h.Models = map[string]string{str(): str()}
	}
	if env.Intn(g, 2) == 0 {
		h.Config = &File{Path: ConfigFile, SHA256: Hash([]byte(str()))}
	}
	for i := range env.Intn(g, 6) {
		h.Sources = append(h.Sources, File{
			Path:   fmt.Sprintf("d%d/f%d.vhd", env.Intn(g, 3), i),
			SHA256: Hash([]byte(str())),
		})
	}
	for i := len(h.Sources) - 1; i > 0; i-- {
		j := env.Intn(g, i+1)
		h.Sources[i], h.Sources[j] = h.Sources[j], h.Sources[i]
	}
	return h
}

// TestRoundTripByteStable: Marshal, Unmarshal and Marshal again gives the
// same bytes, in both forms, and the decoded header is the canonical
// form of the original.
func TestRoundTripByteStable(t *testing.T) {
	g := &env.SplitMix64{State: 1}
	for i := range 2000 {
		h := randomHeader(g)
		for _, enc := range []func(Header) ([]byte, error){
			Marshal, MarshalLine,
		} {
			a, err := enc(h)
			if err != nil {
				t.Fatalf("case %d: %v", i, err)
			}
			back, err := Unmarshal(a)
			if err != nil {
				t.Fatalf("case %d: %v\n%s", i, err, a)
			}
			if !reflect.DeepEqual(back, canonical(h)) {
				t.Fatalf("case %d: decoded %+v, want %+v", i, back,
					canonical(h))
			}
			b, _ := enc(back)
			if !bytes.Equal(a, b) {
				t.Fatalf("case %d: not byte-stable:\n%s\n%s", i, a, b)
			}
		}
	}
}

// TestOrderIndependent: the order sources are listed in does not reach
// the bytes, and Marshal does not reorder the caller's slice.
func TestOrderIndependent(t *testing.T) {
	h := sample()
	a, _ := Marshal(h)
	if h.Sources[0].Path != "src/b.vhd" {
		t.Fatal("Marshal modified the caller's sources")
	}
	slices.Reverse(h.Sources)
	b, _ := Marshal(h)
	if !bytes.Equal(a, b) {
		t.Fatal("source order reached the output")
	}
}

func TestUnmarshalRejects(t *testing.T) {
	good, _ := MarshalLine(sample())
	g := string(good)
	for name, in := range map[string]string{
		"not json":    "nope",
		"no schema":   `{"version":"1"}`,
		"schema 2":    strings.Replace(g, `"schema":1`, `"schema":2`, 1),
		"unknown key": strings.Replace(g, `"seed"`, `"extra":1,"seed"`, 1),
		"trailing":    g + "{}",
		"bad sha": strings.Replace(g, `"sha256":"2d71`,
			`"sha256":"2D71`, 1),
		"short sha": strings.Replace(g, `"sha256":"2d71`, `"sha256":"`, 1),
		"abs path":  strings.Replace(g, `"src/a.vhd"`, `"/src/a.vhd"`, 1),
		"dotdot path": strings.Replace(g, `"src/a.vhd"`,
			`"src/../a.vhd"`, 1),
		"dot path":      strings.Replace(g, `"src/a.vhd"`, `"."`, 1),
		"only dotdot":   strings.Replace(g, `"src/a.vhd"`, `"../.."`, 1),
		"duplicate key": strings.Replace(g, `"seed":`, `"seed":7,"seed":`, 1),
		"upper key":     strings.Replace(g, `"seed":`, `"SEED":`, 1),
		"missing key":   strings.Replace(g, `"testbench":"tb_top",`, ``, 1),
		"null time":     strings.Replace(g, `"time":"1 ms"`, `"time":null`, 1),
		"nested duplicate": strings.Replace(g, `"delta":`,
			`"delta":1,"delta":`, 1),
		"unclean path": strings.Replace(g, `"src/a.vhd"`, `"src//a.vhd"`, 1),
		"backslash":    strings.Replace(g, `"src/a.vhd"`, `"src\\a.vhd"`, 1),
		"duplicate":    strings.Replace(g, `"src/a.vhd"`, `"src/b.vhd"`, 1),
		"negative seed": strings.Replace(g, `"seed":18446744073709551615`,
			`"seed":-1`, 1),
		"bad standard": strings.Replace(g, `"standard":"2008"`,
			`"standard":"banana"`, 1),
		"bad time": strings.Replace(g, `"time":"1 ms"`,
			`"time":"forever"`, 1),
		"negative delta": strings.Replace(g, `"delta":10000`,
			`"delta":-5`, 1),
		"zero step":  strings.Replace(g, `"step":50000000`, `"step":0`, 1),
		"bad define": strings.Replace(g, `"WIDTH":`, `"1 bad":`, 1),
		"config path": strings.Replace(g, `"path":"gohard.yaml"`,
			`"path":"src/a.vhd"`, 1),
	} {
		if in == g {
			t.Fatalf("%s: the mutation did not apply", name)
		}
		if _, err := Unmarshal([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := Unmarshal(good); err != nil {
		t.Fatal(err)
	}
	// A file outside the root, named on the command line, is recorded
	// root-relative (F9) and survives the round trip.
	out := strings.Replace(g, `"src/a.vhd"`, `"../../ext/a.vhd"`, 1)
	h, err := Unmarshal([]byte(out))
	if err != nil {
		t.Fatalf("../ path rejected: %v", err)
	}
	if b, _ := MarshalLine(h); string(b) != out {
		t.Errorf("../ path round trip:\n%s\nwant\n%s", b, out)
	}
}

func TestFileName(t *testing.T) {
	for tb, want := range map[string]string{
		"tb_top":      ".gohard/traces/tb_top-7.repro.json",
		"work.tb":     ".gohard/traces/work.tb-7.repro.json",
		`\a/b\`:       ".gohard/traces/%5Ca%2Fb%5C-7.repro.json",
		"..":          ".gohard/traces/%2E.-7.repro.json",
		"":            ".gohard/traces/-7.repro.json",
		"100%":        ".gohard/traces/100%25-7.repro.json",
		"tb-1":        ".gohard/traces/tb-1-7.repro.json",
		"café":        ".gohard/traces/caf%C3%A9-7.repro.json",
		"two words":   ".gohard/traces/two%20words-7.repro.json",
		"tb.vhd.json": ".gohard/traces/tb.vhd.json-7.repro.json",
		`\Foo\`:       ".gohard/traces/%5C%46oo%5C-7.repro.json",
		`\foo\`:       ".gohard/traces/%5Cfoo%5C-7.repro.json",
	} {
		if got := FileName(tb, 7); got != want {
			t.Errorf("FileName(%q) = %q, want %q", tb, got, want)
		}
	}
	// escape is injective and yields only safe bytes.
	safe := regexp.MustCompile(`^[A-Za-z0-9_.%-]*$`)
	g := &env.SplitMix64{State: 5}
	seen := map[string]string{}
	for range 5000 {
		b := make([]byte, env.Intn(g, 4))
		for i := range b {
			b[i] = byte(g.Uint64())
		}
		e := escape(string(b))
		if !safe.MatchString(e) || strings.HasPrefix(e, ".") {
			t.Fatalf("escape(%q) = %q", b, e)
		}
		// Names must differ even on a case-insensitive file system.
		fold := strings.ToLower(e)
		if prev, ok := seen[fold]; ok && prev != string(b) {
			t.Fatalf("escape(%q) and escape(%q) differ only in case",
				prev, b)
		}
		seen[fold] = string(b)
	}
}

func TestCopyCommand(t *testing.T) {
	for in, want := range map[string]string{
		".gohard/traces/tb-0.repro.json": "gohard check --repro " +
			".gohard/traces/tb-0.repro.json",
		"it's here": `gohard check --repro 'it'\''s here'`,
		"":          "gohard check --repro ''",
	} {
		if got := CopyCommand(in); got != want {
			t.Errorf("CopyCommand(%q) = %q", in, got)
		}
	}
}

func project(t *testing.T, faults simenv.Faults, seed uint64) *simenv.Sim {
	t.Helper()
	return simenv.New(simenv.Options{
		Seed: seed,
		Dir:  "/proj",
		Files: map[string][]byte{
			"src/a.vhd":   []byte("entity a is end;"),
			"src/b.vhd":   []byte("entity b is end;"),
			"gohard.yaml": []byte("standard: \"2008\"\n"),
		},
		Faults: faults,
	})
}

func recordHeader(t *testing.T, fsys env.FS) Header {
	t.Helper()
	srcs, err := HashFiles(fsys, "/proj", []string{"src/b.vhd",
		"src/a.vhd"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := HashConfig(fsys, "/proj")
	if err != nil || cfg == nil {
		t.Fatalf("HashConfig = %v, %v", cfg, err)
	}
	h := sample()
	h.Sources, h.Config = srcs, cfg
	return h
}

func TestWriteReadVerify(t *testing.T) {
	s := project(t, simenv.Faults{}, 0)
	fsys := s.Env().FS
	h := recordHeader(t, fsys)
	name, err := Write(fsys, "/proj", h)
	if err != nil {
		t.Fatal(err)
	}
	if name != FileName("tb_top", h.Seed) {
		t.Fatalf("Write returned %q", name)
	}
	first, _ := fsys.ReadFile("/proj/" + name)
	// Writing the same run again gives the same bytes and no stray
	// temporary files.
	if _, err := Write(fsys, "/proj", h); err != nil {
		t.Fatal(err)
	}
	second, _ := fsys.ReadFile("/proj/" + name)
	ents, _ := fsys.ReadDir("/proj/" + TraceDir)
	if !bytes.Equal(first, second) || len(ents) != 1 {
		t.Fatalf("rewrite changed bytes or left %d entries", len(ents))
	}
	back, err := Read(fsys, "/proj/"+name)
	if err != nil || !reflect.DeepEqual(back, canonical(h)) {
		t.Fatalf("Read = %+v, %v", back, err)
	}

	check := func(version string) Check {
		t.Helper()
		c, err := Verify(fsys, "/proj", back, version)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	c := check("1.0.0")
	if !c.OK() || c.VersionMismatch() || len(c.Diagnostics(name)) != 0 {
		t.Fatalf("matching run: %+v", c)
	}
	exit := func(c Check, strict bool) int {
		return diag.ExitCode(c.Diagnostics(name), strict, diag.NoSignal)
	}

	c = check("1.1.0")
	if !c.OK() || !c.VersionMismatch() || exit(c, false) != 0 {
		t.Fatalf("version mismatch must only warn: %+v", c)
	}
	ds := c.Diagnostics(name)
	if len(ds) != 1 || ds[0].Code != diag.ReproVersionMismatch ||
		!strings.Contains(ds[0].Message, "1.0.0") ||
		!strings.Contains(ds[0].Message, "1.1.0") {
		t.Fatalf("version diagnostic = %+v", ds)
	}

	fsys.WriteFile("/proj/src/b.vhd", []byte("changed"), 0o644)
	fsys.Remove("/proj/src/a.vhd")
	fsys.WriteFile("/proj/gohard.yaml", []byte("lenient: true\n"), 0o644)
	c = check("1.0.0")
	want := []string{"gohard.yaml", "src/a.vhd", "src/b.vhd"}
	if !slices.Equal(c.Changed, want) || exit(c, false) != 2 {
		t.Fatalf("changed = %v, exit %d", c.Changed, exit(c, false))
	}
	ds = c.Diagnostics(name)
	if ds[0].Code != diag.ReproSourceChanged || !strings.Contains(
		ds[0].Message, "gohard.yaml, src/a.vhd, src/b.vhd") {
		t.Fatalf("source diagnostic = %+v", ds[0])
	}

	// A config file that appears after the recording is a change.
	noCfg := back
	noCfg.Config = nil
	fsys.WriteFile("/proj/src/a.vhd", []byte("entity a is end;"), 0o644)
	fsys.WriteFile("/proj/src/b.vhd", []byte("entity b is end;"), 0o644)
	c, _ = Verify(fsys, "/proj", noCfg, "1.0.0")
	if !slices.Equal(c.Changed, []string{"gohard.yaml"}) {
		t.Fatalf("new config: %v", c.Changed)
	}

	d := Invalid(name, errors.New("bad"))
	if d.Code != diag.ReproInvalid || d.Class() != diag.Tool {
		t.Fatalf("Invalid = %+v", d)
	}
}

// TestWriteUnderFaults: with write faults, a repro file is either
// absent, the previous version, or complete; never half written.
func TestWriteUnderFaults(t *testing.T) {
	failed := 0
	for seed := range uint64(100) {
		s := project(t, simenv.Faults{WriteError: 400}, seed)
		fsys := s.Env().FS
		h := recordHeader(t, fsys)
		_, err := Write(fsys, "/proj", h)
		if err != nil {
			failed++
		}
		data, rerr := fsys.ReadFile("/proj/" + FileName(h.Testbench,
			h.Seed))
		switch {
		case errors.Is(rerr, fs.ErrNotExist):
			if err == nil {
				t.Fatalf("seed %d: success without a file", seed)
			}
		case rerr != nil:
			t.Fatal(rerr)
		default:
			if _, perr := Unmarshal(data); perr != nil {
				t.Fatalf("seed %d: half-written file: %v", seed, perr)
			}
		}
	}
	if failed == 0 {
		t.Fatal("no write failed; faults not applied")
	}
}

func TestSession(t *testing.T) {
	msgs := []json.RawMessage{
		json.RawMessage(`{ "jsonrpc": "2.0", "id": 1,
			"method": "initialize", "params": {} }`),
		json.RawMessage(`{"jsonrpc":"2.0","method":"exit"}`),
	}
	s := Session{Header: sample(), Messages: msgs}
	data, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	header, _ := MarshalLine(sample())
	if len(lines) != 3 || lines[0]+"\n" != string(header) ||
		lines[1] != `{"jsonrpc":"2.0","id":1,"method":"initialize",`+
			`"params":{}}` {
		t.Fatalf("session:\n%s", data)
	}
	back, err := ParseSession(data)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := back.Marshal()
	if !bytes.Equal(data, again) {
		t.Fatal("session round trip is not byte-stable")
	}
	// The same header schema as a repro file.
	if !reflect.DeepEqual(back.Header, canonical(sample())) {
		t.Fatal("header changed")
	}
	for name, in := range map[string]string{
		"no newline": strings.TrimSuffix(string(data), "\n"),
		"empty line": string(data) + "\n",
		"bad header": "{}\n",
		"bad json":   string(header) + "{\n",
		"empty":      "",
	} {
		if _, err := ParseSession([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	empty := Session{Header: Header{Budgets: minBudgets}}
	data, _ = empty.Marshal()
	if back, err := ParseSession(data); err != nil ||
		len(back.Messages) != 0 {
		t.Fatalf("header-only session: %v %v", back, err)
	}
}

type failWriter struct{ n int }

func (w *failWriter) Write(p []byte) (int, error) {
	if w.n == 0 {
		return 0, errors.New("disk full")
	}
	w.n--
	return len(p), nil
}

func TestRecorder(t *testing.T) {
	w := &failWriter{n: 2}
	r, err := NewRecorder(w, sample())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Record([]byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := r.Record([]byte(`not json`)); err == nil {
		t.Fatal("invalid message accepted")
	}
	e1 := r.Record([]byte(`{}`))
	e2 := r.Record([]byte(`{}`))
	if e1 == nil || e1 != e2 {
		t.Fatalf("errors %v, %v", e1, e2)
	}
}

func TestNextSession(t *testing.T) {
	s := simenv.New(simenv.Options{})
	fsys := s.Env().FS
	n, err := NextSession(fsys, "rec")
	if err != nil || n != 1 {
		t.Fatalf("missing dir: %d %v", n, err)
	}
	fsys.MkdirAll("rec", 0o755)
	for _, f := range []string{"1.jsonl", "3.jsonl", "10.jsonl",
		"x.jsonl", "07.jsonl", "99.json", "-5.jsonl", "0.jsonl"} {
		fsys.WriteFile("rec/"+f, nil, 0o644)
	}
	n, err = NextSession(fsys, "rec")
	if err != nil || n != 11 {
		t.Fatalf("NextSession = %d %v", n, err)
	}
	// SessionName is an env.FS name, an OS path.
	if SessionName("rec", n) != filepath.FromSlash("rec/11.jsonl") {
		t.Fatal(SessionName("rec", n))
	}
}

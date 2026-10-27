package server_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/repro"
	"github.com/connerohnesorge/gohard/internal/server"
)

var update = flag.Bool("update", false,
	"rewrite testdata/lsp/session want files")

const (
	sessionRoot = "../../testdata/lsp/session"
	seedsDir    = "../../testdata/seeds/lsp"
	// testVersion is the gohard version the fixtures run as, so
	// fixtures do not change with the release version.
	testVersion = "0.0.0-test"
	// workDir is the simulated project folder of every fixture.
	workDir = "/work"
)

// TestLSPSessions runs every testdata/lsp/session/<case> (see
// testdata/lsp/README.md): the scripted client session goes through
// the same replay path as `gohard lsp replay`, the server messages
// must equal want.jsonl, the exit code want.exit, and the session must
// keep the server invariants. A failing case is written to
// testdata/seeds/lsp/<case>.jsonl for replay.
func TestLSPSessions(t *testing.T) {
	ents, err := os.ReadDir(sessionRoot)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		n++
		dir := filepath.Join(sessionRoot, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			runCase(t, dir, seedsDir)
		})
	}
	if n == 0 {
		t.Fatal("no sessions under " + sessionRoot)
	}
}

// tb is what runCase needs from testing.TB, so the harness self-test
// can run it against a recording fake.
type tb interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

// fixture is one loaded case.
type fixture struct {
	name     string
	messages []json.RawMessage
	files    map[string][]byte
	analyzer server.Analyzer
	// anName is the analyzer's name in the analyzer file ("" for
	// Nop).
	anName string
}

// analyzerNamed returns the test analyzer a fixture or a seed session
// names.
func analyzerNamed(name string) (server.Analyzer, bool) {
	switch name {
	case "echo":
		return echo{}, true
	case "words":
		return words{}, true
	case "format":
		return server.WithFormatting(server.Nop{}, testVersion), true
	case "nop", "":
		return server.Nop{}, true
	}
	return nil, false
}

func loadFixture(t tb, dir string) fixture {
	t.Helper()
	f := fixture{name: filepath.Base(dir), files: map[string][]byte{}}
	raw, err := os.ReadFile(filepath.Join(dir, "client.jsonl"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	f.messages, err = parseLines(raw)
	if err != nil {
		t.Fatalf("client.jsonl: %v", err)
	}
	f.analyzer = server.Nop{}
	if b, err := os.ReadFile(filepath.Join(dir, "analyzer")); err == nil {
		name := strings.TrimSpace(string(b))
		an, ok := analyzerNamed(name)
		if !ok {
			t.Fatalf("analyzer: unknown analyzer %q", name)
		}
		f.analyzer = an
		if name != "nop" {
			f.anName = name
		}
	}
	in := filepath.Join(dir, "in")
	if _, err := os.Stat(in); err == nil {
		err := fs.WalkDir(os.DirFS(in), ".", func(p string,
			d fs.DirEntry, err error,
		) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(os.DirFS(in), p)
			f.files[path.Join(workDir, p)] = b
			return err
		})
		if err != nil {
			t.Fatalf("in/: %v", err)
		}
	}
	return f
}

// parseLines reads JSON lines; blank lines and lines starting with #
// are skipped.
func parseLines(raw []byte) ([]json.RawMessage, error) {
	var out []json.RawMessage
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var b bytes.Buffer
		if err := json.Compact(&b, []byte(line)); err != nil {
			return nil, fmt.Errorf("line %d: %v", i+1, err)
		}
		out = append(out, b.Bytes())
	}
	return out, nil
}

// session is the recorded form of f: its header as `gohard lsp
// --record` would write it, then the analyzer and project files
// embedded (so a failing case written to testdata/seeds replays on
// its own, #25), then the client messages.
func (f fixture) session(t tb) (repro.Session, *simenv.Sim) {
	sim := simenv.New(simenv.Options{Dir: workDir, Files: f.files})
	h, err := server.SessionHeader(sim.Env(), workDir, testVersion)
	if err != nil {
		t.Fatalf("header: %v", err)
	}
	var lines []json.RawMessage
	if f.anName != "" {
		lines = append(lines, server.AnalyzerLine(f.anName))
	}
	for _, p := range slices.Sorted(maps.Keys(f.files)) {
		rel := strings.TrimPrefix(p, workDir+"/")
		lines = append(lines, server.FileLine(rel, f.files[p]))
	}
	lines = append(lines, f.messages...)
	return repro.Session{Header: h, Messages: lines}, sim
}

// play replays f and returns the server bodies, one per line, and the
// exit code.
func (f fixture) play(t tb) (repro.Session, string, int) {
	sess, sim := f.session(t)
	got, err := server.Replay(sess, sim.Env().FS, server.Options{
		Analyzer: f.analyzer, Version: testVersion}, f.name)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(got.Diagnostics) > 0 {
		t.Fatalf("replay: %v", got.Diagnostics)
	}
	bodies, err := server.Frames(got.Output)
	if err != nil {
		t.Fatalf("frames: %v", err)
	}
	var b strings.Builder
	for _, m := range bodies {
		b.Write(m)
		b.WriteByte('\n')
	}
	for _, p := range checkSession(f.messages, bodies) {
		t.Errorf("invariant: %s", p)
	}
	return sess, b.String(), got.Exit
}

func runCase(t tb, dir, seeds string) {
	t.Helper()
	f := loadFixture(t, dir)
	failed := false
	fail := func(format string, args ...any) {
		failed = true
		t.Errorf(format, args...)
	}
	probe := &failTB{tb: t, failed: &failed}
	sess, got, exit := f.play(probe)
	wantFile := filepath.Join(dir, "want.jsonl")
	exitFile := filepath.Join(dir, "want.exit")
	if *update && !failed {
		write(t, wantFile, []byte(got))
		write(t, exitFile, []byte(strconv.Itoa(exit)+"\n"))
		return
	}
	want, err := os.ReadFile(wantFile)
	if err != nil {
		fail("%v (run with -update)", err)
	} else if got != string(want) {
		fail("server messages differ from want.jsonl:\n%s",
			lineDiff(string(want), got))
	}
	wantExit, err := os.ReadFile(exitFile)
	if err != nil {
		fail("%v (run with -update)", err)
	} else if strings.TrimSpace(string(wantExit)) != strconv.Itoa(exit) {
		fail("exit %d, want %s", exit, strings.TrimSpace(
			string(wantExit)))
	}
	if failed {
		name := filepath.Join(seeds, f.name+".jsonl")
		if err := writeSeed(name, sess); err != nil {
			t.Errorf("writing the failing session: %v", err)
			return
		}
		t.Logf("failing session written to %s; replay it with "+
			"gohard lsp replay %s", name, name)
	}
}

// failTB records whether an invariant check failed.
type failTB struct {
	tb
	failed *bool
}

func (f *failTB) Errorf(format string, args ...any) {
	*f.failed = true
	f.tb.Errorf(format, args...)
}

func writeSeed(name string, sess repro.Session) error {
	data, err := sess.Marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, data, 0o644)
}

func write(t tb, name string, data []byte) {
	if err := os.WriteFile(name, data, 0o644); err != nil {
		t.Fatalf("%v", err)
	}
}

// lineDiff shows the first differing line of two texts.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(w), len(g)) {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d:\nwant %s\ngot  %s", i+1, a, b)
		}
	}
	return "(equal)"
}

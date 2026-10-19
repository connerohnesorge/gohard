package server_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/repro"
	"github.com/connerohnesorge/gohard/internal/server"
)

var commitSeed = flag.Bool("commit-seed", false,
	"write the deliberately failing harness session to testdata/seeds")

// replaySeed replays one committed session twice, with the analyzer
// and project files it embeds (an empty simulated project when it
// embeds none): the project must match the header, both runs must give
// the same bytes and the session must keep the server invariants (#29,
// P5).
func replaySeed(t tb, data []byte, name string) []byte {
	t.Helper()
	sess, err := repro.ParseSession(data)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	anName, err := server.SessionAnalyzer(sess)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	an, ok := analyzerNamed(anName)
	if !ok {
		t.Fatalf("%s: unknown analyzer %q", name, anName)
	}
	var outs [2]server.Replayed
	for i := range outs {
		sim := simenv.New(simenv.Options{Seed: sess.Header.Seed,
			Dir: workDir})
		outs[i], err = server.Replay(sess, sim.Env().FS, server.Options{
			Analyzer: an, Version: sess.Header.Version}, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(outs[i].Diagnostics) > 0 {
			t.Fatalf("%s: cannot replay: %v", name,
				outs[i].Diagnostics)
		}
	}
	if n := server.Mismatch(outs[0], outs[1]); n > 0 {
		t.Fatalf("%s: replays differ after message %d", name, n)
	}
	bodies, err := server.Frames(outs[0].Output)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	for _, p := range checkSession(outs[0].Client, bodies) {
		t.Errorf("%s: invariant: %s", name, p)
	}
	return outs[0].Output
}

// TestSeeds replays every session committed under testdata/seeds/
// (the nightly job runs the same test, #30).
func TestSeeds(t *testing.T) {
	names, err := filepath.Glob(filepath.Join(seedsDir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("no sessions under " + seedsDir)
	}
	for _, name := range names {
		t.Run(filepath.Base(name), func(t *testing.T) {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			replaySeed(t, data, filepath.ToSlash(name))
		})
	}
}

// fakeTB records failures instead of failing the test, so the harness
// can be run on a case that must fail.
type fakeTB struct {
	*testing.T
	errs []string
}

func (f *fakeTB) Errorf(format string, args ...any) {
	f.errs = append(f.errs, format)
}

// TestHarnessWritesFailingSession is the deliberately failing harness
// session of #29: a case whose want.jsonl is wrong fails, and the
// harness writes it as a session file that replays to the output the
// harness saw. With -commit-seed it is also written to
// testdata/seeds/lsp/, where TestSeeds replays it on every run.
func TestHarnessWritesFailingSession(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "harness-deliberate-failure")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	client := `{"jsonrpc":"2.0","id":1,"method":"initialize",` +
		`"params":{"rootUri":"file:///work","capabilities":{}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"initialized","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}` + "\n" +
		`{"jsonrpc":"2.0","method":"exit"}` + "\n"
	for name, body := range map[string]string{
		"client.jsonl": client,
		"want.jsonl":   "this is not what the server sends\n",
		"want.exit":    "0\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body),
			0o644); err != nil {
			t.Fatal(err)
		}
	}
	seeds := t.TempDir()
	f := &fakeTB{T: t}
	runCase(f, dir, seeds)
	if len(f.errs) == 0 {
		t.Fatal("the deliberately failing case passed")
	}
	name := filepath.Join(seeds, "harness-deliberate-failure.jsonl")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("no session written: %v", err)
	}
	_, got, _ := loadFixture(t, dir).play(t)
	out := replaySeed(t, data, name)
	bodies, err := server.Frames(out)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, m := range bodies {
		b.Write(m)
		b.WriteByte('\n')
	}
	if b.String() != got {
		t.Fatalf("the written session replays to\n%s\nwant\n%s",
			b.String(), got)
	}
	if *commitSeed {
		dst := filepath.Join(seedsDir, filepath.Base(name))
		if err := writeSeedFile(dst, data); err != nil {
			t.Fatal(err)
		}
	}
}

// FuzzLSPReplay replays arbitrary client messages, one JSON value per
// line of the input after a fixed header: the server never panics, the
// replay is deterministic and the invariants hold (#29, #30). The
// committed seed sessions and the saved inputs in the repository's
// testdata/fuzz/FuzzLSPReplay/ seed the corpus, so plain go test
// replays them. A failing input is written to
// testdata/seeds/lsp/FuzzLSPReplay-failure.jsonl for gohard lsp replay.
func FuzzLSPReplay(f *testing.F) {
	fuzzSeed := filepath.Join(seedsDir, "FuzzLSPReplay-failure.jsonl")
	names, _ := filepath.Glob(filepath.Join(seedsDir, "*.jsonl"))
	for _, name := range names {
		if data, err := os.ReadFile(name); err == nil {
			_, msgs, _ := bytes.Cut(data, []byte("\n"))
			f.Add(msgs)
		}
	}
	saved, _ := filepath.Glob("../../testdata/fuzz/FuzzLSPReplay/*")
	for _, name := range saved {
		b, err := readCorpusFile(name)
		if err != nil {
			f.Fatalf("%s: %v", name, err)
		}
		f.Add(b)
	}
	sim := simenv.New(simenv.Options{Dir: workDir})
	h, err := server.SessionHeader(sim.Env(), workDir, testVersion)
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		sess := repro.Session{Header: h}
		for line := range bytes.SplitSeq(data, []byte("\n")) {
			if json.Valid(line) && len(bytes.TrimSpace(line)) > 0 {
				sess.Messages = append(sess.Messages, line)
			}
		}
		if len(sess.Messages) > 200 {
			return
		}
		file, err := sess.Marshal()
		if err != nil {
			return
		}
		// Inputs that are not a replayable session (a malformed
		// "$gohard" entry, embedded files that do not match the
		// header) are rejected by Replay, which is not a bug.
		if r, err := server.Replay(sess, simenv.New(simenv.Options{
			Dir: workDir}).Env().FS, server.Options{
			Version: h.Version}, "fuzz"); err != nil ||
			len(r.Diagnostics) > 0 {
			return
		}
		defer func() {
			// A failing input is also written as a replayable session
			// (#29). Minimizing calls this again with smaller inputs,
			// so the file ends up holding the last, smallest one.
			if t.Failed() {
				_ = writeSeedFile(fuzzSeed, file)
				t.Logf("session written to %s", fuzzSeed)
			}
		}()
		replaySeed(t, file, "fuzz")
	})
}

// readCorpusFile decodes a one-argument []byte corpus file.
func readCorpusFile(name string) ([]byte, error) {
	raw, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	head, body, _ := strings.Cut(string(raw), "\n")
	lit, ok := strings.CutPrefix(strings.TrimSpace(body), "[]byte(")
	lit, ok2 := strings.CutSuffix(lit, ")")
	if head != "go test fuzz v1" || !ok || !ok2 {
		return nil, errors.New("not a one-argument []byte corpus file")
	}
	s, err := strconv.Unquote(lit)
	return []byte(s), err
}

func writeSeedFile(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, data, 0o644)
}

// TestFailingSeedWithFiles: a failing case with project files (in/)
// and a test analyzer writes a self-contained seed that replays, on
// its own, to the output the harness saw, both in the test and with
// `gohard lsp replay` semantics (Replay against a file system that
// holds nothing of the project).
func TestFailingSeedWithFiles(t *testing.T) {
	src := filepath.Join(sessionRoot, "definition-cross-file")
	dir := filepath.Join(t.TempDir(), "definition-cross-file")
	if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "want.jsonl"),
		[]byte("not the output\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seeds := t.TempDir()
	f := &fakeTB{T: t}
	runCase(f, dir, seeds)
	if len(f.errs) == 0 {
		t.Fatal("the failing case passed")
	}
	data, err := os.ReadFile(filepath.Join(seeds,
		"definition-cross-file.jsonl"))
	if err != nil {
		t.Fatalf("no session written: %v", err)
	}
	_, want, _ := loadFixture(t, dir).play(t)
	out := replaySeed(t, data, "seed")
	bodies, err := server.Frames(out)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, m := range bodies {
		b.Write(m)
		b.WriteByte('\n')
	}
	if b.String() != want {
		t.Fatalf("the seed replays to\n%s\nwant\n%s", b.String(), want)
	}
	if !strings.Contains(want, "adder.vhd") {
		t.Fatal("the case no longer answers from a project file")
	}
}

package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/cli"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/repro"
)

const recordRoot = "../../testdata/lsp/record"

// lspCase is a testdata/lsp/record/<case>: client.jsonl and in/.
type lspCase struct {
	frames []byte
	files  map[string][]byte
}

func loadLSPCase(t *testing.T, dir string) lspCase {
	t.Helper()
	c := lspCase{files: map[string][]byte{}}
	raw, err := os.ReadFile(filepath.Join(dir, "client.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if raw, ok := strings.CutPrefix(line, "!raw "); ok {
			c.frames = jsonrpc2.AppendFrame(c.frames, []byte(raw))
			continue
		}
		var b bytes.Buffer
		if err := json.Compact(&b, []byte(line)); err != nil {
			t.Fatal(err)
		}
		c.frames = jsonrpc2.AppendFrame(c.frames, b.Bytes())
	}
	in := os.DirFS(filepath.Join(dir, "in"))
	err = fs.WalkDir(in, ".", func(p string, d fs.DirEntry,
		err error,
	) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(in, p)
		c.files[path.Join("/work", p)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func runIn(sim *simenv.Sim, stdin []byte, args ...string) (int,
	[]byte, []byte,
) {
	e := sim.Env()
	var out, errb bytes.Buffer
	e.Transport.Stdin = simenv.NewScript(stdin)
	e.Transport.Stdout, e.Transport.Stderr = &out, &errb
	code := cli.Run(context.Background(), e, args)
	return code, out.Bytes(), errb.Bytes()
}

// TestLSPRecordReplay is the #25/#26/#29 fixture: `gohard lsp --record
// DIR` writes DIR/1.jsonl, then DIR/2.jsonl, and `gohard lsp replay`
// of each prints exactly the bytes the live server wrote. Without
// --record nothing is written.
func TestLSPRecordReplay(t *testing.T) {
	cases, err := os.ReadDir(recordRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range cases {
		t.Run(ent.Name(), func(t *testing.T) {
			c := loadLSPCase(t, filepath.Join(recordRoot, ent.Name()))
			// gohard lsp runs in a directory other than the
			// workspace folder: the session folder comes from
			// initialize, wherever it is in the session.
			sim := simenv.New(simenv.Options{Dir: "/elsewhere",
				Files: c.files})
			before := sim.FS.Dump()
			code, plain, _ := runIn(sim, c.frames, "lsp")
			if code != 0 || len(plain) == 0 {
				t.Fatalf("gohard lsp: exit %d, %d bytes", code,
					len(plain))
			}
			if sim.FS.Dump() != before {
				t.Fatal("gohard lsp without --record wrote files")
			}
			var lives [][]byte
			for range 2 {
				code, live, stderr := runIn(sim, c.frames, "lsp",
					"--record", "rec")
				if code != 0 || len(stderr) > 0 {
					t.Fatalf("record: exit %d: %s", code, stderr)
				}
				if !bytes.Equal(live, plain) {
					t.Fatal("recording changed the server output")
				}
				lives = append(lives, live)
			}
			for i, name := range []string{"rec/1.jsonl", "rec/2.jsonl"} {
				code, got, stderr := runIn(sim, nil, "lsp", "replay",
					name)
				if code != 0 {
					t.Fatalf("replay %s: exit %d\n%s%s", name, code, got,
						stderr)
				}
				if !bytes.Equal(got, lives[i]) {
					t.Fatalf("replay of %s differs:\n%s\n---\n%s", name,
						got, lives[i])
				}
			}
			// Every client message is in the recording, also those
			// that are not JSON-RPC.
			rec, err := sim.FS.ReadFile("rec/1.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			sess, err := repro.ParseSession(rec)
			if err != nil {
				t.Fatal(err)
			}
			sent := 0
			rd := jsonrpc2.NewReader(bytes.NewReader(c.frames), 0)
			for {
				if _, err := rd.ReadFrame(); err != nil {
					break
				}
				sent++
			}
			got := 0
			for _, m := range sess.Messages {
				// The end of the input is recorded when it arrived
				// while the server was busy.
				if !strings.HasPrefix(string(m), `{"$gohard":"end"`) &&
					!strings.HasPrefix(string(m),
						`{"$gohard":"config"`) {
					got++
				}
			}
			if got != sent {
				t.Fatalf("recorded %d messages, sent %d:\n%s", got,
					sent, rec)
			}
			// A source that changed since the recording: exit 2.
			// (gohard.yaml is not checked: the session carries every
			// read of it.)
			if err := sim.FS.WriteFile("/work/top.vhd",
				[]byte("entity changed is end;\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			code, out, _ := runIn(sim, nil, "lsp", "replay",
				"rec/1.jsonl")
			if code != 2 || !bytes.Contains(out,
				[]byte("repro/source-changed")) {
				t.Fatalf("changed source: exit %d\n%s", code, out)
			}
		})
	}
}

// TestLSPReplaySeeds: `gohard lsp replay` reads the committed seed
// sessions (#26).
func TestLSPReplaySeeds(t *testing.T) {
	names, _ := filepath.Glob("../../testdata/seeds/lsp/*.jsonl")
	if len(names) == 0 {
		t.Fatal("no seed sessions")
	}
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sim := simenv.New(simenv.Options{Files: map[string][]byte{
			"/seed.jsonl": data}})
		code, out, stderr := runIn(sim, nil, "lsp", "replay",
			"/seed.jsonl")
		if code != 0 || len(out) == 0 {
			t.Fatalf("%s: exit %d\n%s%s", name, code, out, stderr)
		}
	}
}

// TestLSPActions covers the `gohard lsp <action>` dispatcher.
func TestLSPActions(t *testing.T) {
	sim := simenv.New(simenv.Options{Files: map[string][]byte{
		"/work/a.vhd":   []byte("entity a is end;\n"),
		"/work/s.jsonl": []byte(`{"schema":2}` + "\n"),
	}})
	for _, c := range []struct {
		args []string
		exit int
		want string
	}{
		{[]string{"lsp", "hover", "--at", "a.vhd:1:1"}, 2,
			"gohard lsp hover is not implemented"},
		{[]string{"lsp", "hover"}, 2, "needs --at PATH:LINE:COL"},
		{[]string{"lsp", "hover", "--at", "a.vhd:0:1"}, 2,
			"--at must be PATH:LINE:COL"},
		{[]string{"lsp", "hover", "--at", "b.vhd:1:1"}, 2,
			"b.vhd does not exist"},
		{[]string{"lsp", "bogus", "--at", "a.vhd:1:1"}, 2,
			`unknown action gohard lsp "bogus"`},
		{[]string{"lsp", "replay"}, 2, "needs one session file"},
		{[]string{"lsp", "replay", "s.jsonl"}, 2, "repro/invalid"},
	} {
		code, out, stderr := runIn(sim, nil, c.args...)
		all := string(out) + string(stderr)
		if code != c.exit || !strings.Contains(all, c.want) {
			t.Errorf("%v: exit %d\n%s", c.args, code, all)
		}
	}
}

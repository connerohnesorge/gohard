package cli_test

import (
	"encoding/json"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/cli"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/ide"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/v2/internal/server"
)

// notify sends a client notification with params p.
func (s *session) notify(method string, p any) {
	s.t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0",
		"method": method, "params": p})
	s.send(string(b))
}

func (s *session) watched(rel string, typ int) {
	s.notify("workspace/didChangeWatchedFiles", map[string]any{
		"changes": []any{map[string]any{
			"uri": "file:///work/" + rel, "type": typ}}})
}

func (s *session) saved(rel string) {
	s.notify("textDocument/didSave", map[string]any{
		"textDocument": map[string]any{"uri": "file:///work/" + rel}})
}

// TestLSPDiskChangesReachDiagnostics: files changed outside the editor
// (deleted, created, rewritten, or saved while closed) are reread, so
// the quiescent diagnostics keep equalling `gohard check` (#25).
func TestLSPDiskChangesReachDiagnostics(t *testing.T) {
	pkg := []byte("package p is\n  constant K : integer := 1;\n" +
		"end package;\n")
	use := []byte("use work.p.all;\nentity b is end;\n" +
		"architecture a of b is\n  constant C : integer := K;\n" +
		"begin\nend;\n")
	sim := simenv.New(simenv.Options{Dir: "/work",
		Files: map[string][]byte{"/work/a.vhd": pkg,
			"/work/b.vhd": use}})
	s := newSession(t, sim, 2)
	s.open("b.vhd", use)
	compareQuiet(t, s, sim, "initial")
	if len(s.pubs) != 0 {
		t.Fatalf("initial diagnostics: %v", s.pubs)
	}

	// Deleted on disk: b loses its package.
	if err := sim.FS.Remove("/work/a.vhd"); err != nil {
		t.Fatal(err)
	}
	s.watched("a.vhd", 3)
	compareQuiet(t, s, sim, "after delete")
	if len(s.pubs["b.vhd"]) == 0 {
		t.Fatal("deleting the package left b.vhd without errors")
	}

	// Created again, reported only by a save of a closed file.
	if err := sim.FS.WriteFile("/work/a.vhd", pkg, 0o644); err != nil {
		t.Fatal(err)
	}
	s.saved("a.vhd")
	compareQuiet(t, s, sim, "after create")
	if len(s.pubs) != 0 {
		t.Fatalf("recreated package still reported: %v", s.pubs)
	}

	// Rewritten on disk (a checkout) without the constant.
	bad := []byte("package p is\nend package;\n")
	if err := sim.FS.WriteFile("/work/a.vhd", bad, 0o644); err != nil {
		t.Fatal(err)
	}
	s.watched("a.vhd", 2)
	compareQuiet(t, s, sim, "after rewrite")
	if len(s.pubs["b.vhd"]) == 0 {
		t.Fatal("rewritten package left b.vhd without errors")
	}

	// Changes outside the root and to other files are ignored.
	s.watched("../elsewhere/x.vhd", 1)
	s.watched("notes.txt", 1)
	compareQuiet(t, s, sim, "after unrelated")
}

// TestLSPRegistersWatchers: a client with dynamic registration is
// asked to watch the VHDL files and gohard.yaml; others are not.
func TestLSPRegistersWatchers(t *testing.T) {
	for _, dyn := range []bool{false, true} {
		sim := simenv.New(simenv.Options{Dir: "/work",
			Files: map[string][]byte{"/work/a.vhd": []byte(
				"entity a is end;\n")}})
		var regs []string
		d := newRawDriver(t, sim, func(method string,
			params json.RawMessage,
		) {
			if method == "client/registerCapability" {
				regs = append(regs, string(params))
			}
		})
		caps := `{}`
		if dyn {
			caps = `{"workspace":{"didChangeWatchedFiles":` +
				`{"dynamicRegistration":true}}}`
		}
		deliver(t, d, `{"jsonrpc":"2.0","id":1,"method":"initialize",`+
			`"params":{"processId":null,"rootUri":"file:///work",`+
			`"capabilities":`+caps+`}}`)
		deliver(t, d, `{"jsonrpc":"2.0","method":"initialized",`+
			`"params":{}}`)
		want := 0
		if dyn {
			want = 1
		}
		if len(regs) != want {
			t.Fatalf("dynamic=%v: registrations %v", dyn, regs)
		}
		if dyn {
			const w = `{"registrations":[{"id":"gohard-watch",` +
				`"method":"workspace/didChangeWatchedFiles",` +
				`"registerOptions":{"watchers":[` +
				`{"globPattern":"**/*.vhd"},{"globPattern":"**/*.vhdl"},` +
				`{"globPattern":"**/gohard.yaml"}]}}]}`
			if regs[0] != w {
				t.Fatalf("registration %s", regs[0])
			}
		}
	}
}

func newRawDriver(t *testing.T, sim *simenv.Sim,
	f func(method string, params json.RawMessage),
) *server.Driver {
	an := server.WithFormatting(ide.New(ide.Options{
		ToolVersion: cli.Version, Jobs: sim.Env().Jobs, Limit: 1}),
		cli.Version)
	return server.NewDriver(server.Options{Analyzer: an,
		Version: cli.Version}, sim.Env(), 1, func(m server.Send) error {
		if r, ok := m.Msg.(*jsonrpc2.Request); ok {
			f(r.Method, r.Params)
		}
		return nil
	})
}

func deliver(t *testing.T, d *server.Driver, body string) {
	t.Helper()
	if err := d.Deliver([]byte(body)); err != nil {
		t.Fatal(err)
	}
}

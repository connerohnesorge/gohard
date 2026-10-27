package server

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/stdlib"
)

// TestEndAnswersEveryRequest: when shutdown or exit arrives, every
// request still running, parked on the workspace load or parked on a
// reread gets exactly one RequestCancelled, and a request after
// shutdown gets InvalidRequest (#25).
func TestEndAnswersEveryRequest(t *testing.T) {
	ends := []struct{ name, msg string }{
		{"shutdown", `{"jsonrpc":"2.0","id":99,"method":"shutdown"}`},
		{"exit", `{"jsonrpc":"2.0","method":"exit"}`},
	}
	for _, park := range []string{"load", "reread"} {
		for _, end := range ends {
			name := end.name
			h := newHarness(t, slow{hover: 1000, diags: 1})
			want := []int{}
			if park == "load" {
				h.hold = true
				h.start(`{}`)
				h.open(uri, "x")
				h.hover(1, uri)
				h.hover(2, uri)
				h.drain()
				want = append(want, 1, 2)
			} else {
				h.start(`{}`)
				h.open(uri, "x")
				h.drain()
				h.hover(1, uri) // running: 1000 slices
				h.step()
				h.step()
				h.hold = true
				h.client(`{"jsonrpc":"2.0","method":` +
					`"workspace/didChangeWatchedFiles","params":` +
					`{"changes":[{"uri":"file:///work/b.vhd","type":1}]}}`)
				h.step()
				h.hover(2, uri) // parked on the reread
				h.step()
				want = append(want, 1, 2)
			}
			h.client("%s", end.msg)
			h.hover(3, uri)
			h.drain()
			got := h.responses()
			for _, id := range want {
				rs := got[strconv.Itoa(id)]
				if len(rs) != 1 || rs[0].Error == nil ||
					rs[0].Error.Code != jsonrpc2.CodeRequestCancelled {
					t.Errorf("%s/%s: request %d: %d responses %+v",
						park, name, id, len(rs), rs)
				}
			}
			rs := got["3"]
			switch name {
			case "shutdown":
				if len(got["99"]) != 1 || got["99"][0].Error != nil {
					t.Errorf("%s/shutdown: shutdown answered %+v",
						park, got["99"])
				}
				if len(rs) != 1 || rs[0].Error == nil ||
					rs[0].Error.Code != jsonrpc2.CodeInvalidRequest {
					t.Errorf("%s/shutdown: request after it: %+v",
						park, rs)
				}
			case "exit":
				if len(rs) != 0 || h.exit != 1 {
					t.Errorf("%s/exit: after exit %+v, code %d",
						park, rs, h.exit)
				}
			}
		}
	}
}

// TestAdoptsFolderOfFirstFile: a client that names no workspace folder
// gets the folder of the first file it opens as its project folder
// (#48), loaded like a workspace folder: a request waits for the load
// and is answered from it. Later files and other URIs do not move it,
// and a cached bundled std/ieee file does not set it.
func TestAdoptsFolderOfFirstFile(t *testing.T) {
	sub := filepath.FromSlash("/work/sub")
	cases := []struct {
		name string
		msgs []string
		want string // the project root after the session
	}{
		{"first file", []string{initNoFolder, initialized,
			openURI("file:///work/sub/a.vhd"),
			openURI("file:///other/b.vhd")}, sub},
		{"untitled first", []string{initNoFolder, initialized,
			openURI("untitled:Untitled-1"),
			openURI("file:///work/sub/a.vhd")}, sub},
		{"cached bundled file first", []string{initNoFolder, initialized,
			openURI(PathToURI(filepath.Join(sub, "gohard", "bundle",
				stdlib.CacheKey(), "ieee", "numeric_std.vhdl"))),
			openURI("file:///work/sub/a.vhd")}, sub},
		{"before initialized", []string{initNoFolder,
			openURI("file:///work/sub/a.vhd"), initialized}, sub},
		{"workspace folder wins", []string{
			`{"jsonrpc":"2.0","id":1,"method":"initialize",` +
				`"params":{"rootUri":"file:///work","capabilities":{}}}`,
			initialized, openURI("file:///work/sub/a.vhd")},
			filepath.FromSlash("/work")},
		{"after shutdown", []string{initNoFolder, initialized,
			`{"jsonrpc":"2.0","id":9,"method":"shutdown"}`,
			openURI("file:///work/sub/a.vhd")}, ""},
	}
	for _, c := range cases {
		h := newHarness(t, slow{hover: 1, diags: 1})
		for _, m := range c.msgs {
			h.client("%s", m)
		}
		h.hover(2, "file:///work/sub/a.vhd")
		h.drain()
		if h.s.root != c.want || !h.s.loaded {
			t.Errorf("%s: root %q (loaded %v), want %q", c.name,
				h.s.root, h.s.loaded, c.want)
		}
		if c.want == sub && h.s.Snapshot().Root != sub {
			t.Errorf("%s: snapshot root %q", c.name,
				h.s.Snapshot().Root)
		}
		if rs := h.responses()["2"]; len(rs) != 1 {
			t.Errorf("%s: hover answered %d times", c.name, len(rs))
		}
	}
}

// TestFolderScanAgreesWithServer: for any order of initialize requests
// (with and without a folder, invalid), initialized, didOpen of files,
// of cached bundled files, of other URIs and with invalid params,
// shutdown and exit, the folder SessionFolder computes for a recording
// is the folder the server works in (#29, #48), both in the live
// session and in its replay, which has another cache root, so a
// recorded session replays against the same project. The bodies go
// through the Driver, which maps cached files to bundle URIs (#33).
func TestFolderScanAgreesWithServer(t *testing.T) {
	live := filepath.FromSlash("/home/u/.cache")
	root, err := stdlib.CacheRoot(func(k string) (string, bool) {
		return live, k == "XDG_CACHE_HOME"
	}, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	cached := PathToURI(filepath.Join(root, "ieee", "std_logic_1164.vhdl"))
	alphabet := []string{
		initNoFolder,
		`{"jsonrpc":"2.0","id":1,"method":"initialize",` +
			`"params":{"rootUri":"file:///w","capabilities":{}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize",` +
			`"params":{"rootUri":5}}`,
		initialized,
		openURI("file:///a/x.vhd"),
		openURI("file:///b/c/y.vhd"),
		openURI(cached),
		openURI("untitled:1"),
		openURI(""),
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":5}`,
		`{"jsonrpc":"2.0","id":7,"method":"textDocument/didOpen",` +
			`"params":{"textDocument":{"uri":"file:///r/z.vhd"}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	}
	caches := []string{live, filepath.FromSlash("/gohard-cache")}
	r := &rng{env.SplitMix64{State: 48}}
	for i := range 2000 {
		var bodies [][]byte
		for range 1 + r.n(8) {
			bodies = append(bodies, []byte(alphabet[r.n(len(alphabet))]))
		}
		var scan FolderScan
		known := false
		for _, b := range bodies {
			known = scan.Add(b)
		}
		got := scan.Folder("")
		if want := SessionFolder(bodies, ""); got != want {
			t.Fatalf("case %d: FolderScan %q, SessionFolder %q", i, got,
				want)
		}
		for _, cache := range caches {
			d := NewDriver(Options{Analyzer: Nop{}, Version: "t"},
				simenv.New(simenv.Options{Dir: filepath.FromSlash("/w"),
					Vars: map[string]string{"XDG_CACHE_HOME": cache},
				}).Env(), 1, func(Send) error { return nil })
			for _, b := range bodies {
				d.Push(b)
			}
			if err := d.Run(); err != nil {
				t.Fatal(err)
			}
			if got != d.State.folder {
				t.Fatalf("case %d, cache %s: FolderScan %q, server %q\n%s",
					i, cache, got, d.State.folder,
					bytes.Join(bodies, []byte("\n")))
			}
			if known && got == "" && d.State.phase == phaseRunning {
				t.Fatalf("case %d: known without a folder while "+
					"running\n%s", i, bytes.Join(bodies, []byte("\n")))
			}
		}
	}
}

const (
	initNoFolder = `{"jsonrpc":"2.0","id":1,"method":"initialize",` +
		`"params":{"capabilities":{}}}`
	initialized = `{"jsonrpc":"2.0","method":"initialized","params":{}}`
)

func openURI(u string) string {
	return `{"jsonrpc":"2.0","method":"textDocument/didOpen","params":` +
		`{"textDocument":{"uri":"` + u + `","languageId":"vhdl",` +
		`"version":1,"text":"x"}}}`
}

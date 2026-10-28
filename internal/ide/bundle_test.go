package ide

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/v2/internal/server"
	"github.com/connerohnesorge/gohard/v2/internal/stdlib"
)

// The bundled std/ieee sources reach the editor as read-only files of
// the cache directory (#33): definition into ieee returns a file URI,
// and opening that file gives hover and definition in the bundled
// unit.

const bundleTop = `library ieee;
use ieee.std_logic_1164.all;

entity top is
  port (clk : in std_logic);
end entity top;

architecture rtl of top is
begin
end architecture rtl;
`

// lspClient drives one server session with this package's analyzer.
type lspClient struct {
	t    *testing.T
	d    *server.Driver
	id   int
	got  map[int]*jsonrpc2.Response
	msgs []*jsonrpc2.Notification
}

func newClient(t *testing.T, e env.Env, root string) *lspClient {
	t.Helper()
	c := &lspClient{t: t, got: map[int]*jsonrpc2.Response{}}
	c.d = server.NewDriver(server.Options{Analyzer: New(Options{})}, e, 1,
		func(s server.Send) error {
			switch m := s.Msg.(type) {
			case *jsonrpc2.Response:
				if n, ok := m.ID.Int(); ok {
					c.got[int(n)] = m
				}
			case *jsonrpc2.Notification:
				c.msgs = append(c.msgs, m)
			}
			return nil
		})
	c.request("initialize", map[string]any{"processId": nil,
		"rootUri": server.PathToURI(root), "capabilities": map[string]any{
			"general": map[string]any{
				"positionEncodings": []string{"utf-8"}}}})
	c.notify("initialized", map[string]any{})
	return c
}

func (c *lspClient) deliver(v map[string]any) {
	c.t.Helper()
	v["jsonrpc"] = "2.0"
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.d.Deliver(b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *lspClient) notify(method string, params any) {
	c.deliver(map[string]any{"method": method, "params": params})
}

func (c *lspClient) request(method string, params any) json.RawMessage {
	c.t.Helper()
	c.id++
	c.deliver(map[string]any{"id": c.id, "method": method,
		"params": params})
	r := c.got[c.id]
	if r == nil || r.Error != nil {
		c.t.Fatalf("%s: response %+v", method, r)
	}
	return r.Result
}

func (c *lspClient) open(uri string, text []byte) {
	c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "vhdl",
			"version": 1, "text": string(text)}})
}

// at is the query params at the first occurrence of word in text
// (utf-8 positions).
func at(t *testing.T, uri string, text []byte, word string, nth int,
) map[string]any {
	t.Helper()
	off := -1
	for range nth + 1 {
		i := bytes.Index(text[off+1:], []byte(word))
		if i < 0 {
			t.Fatalf("%q not in %s", word, uri)
		}
		off += 1 + i
	}
	line := bytes.Count(text[:off], []byte("\n"))
	col := off - (bytes.LastIndexByte(text[:off], '\n') + 1)
	return map[string]any{"textDocument": map[string]any{"uri": uri},
		"position": map[string]any{"line": line, "character": col}}
}

type wireLocation struct {
	URI   string `json:"uri"`
	Range struct {
		Start struct{ Line, Character int } `json:"start"`
	} `json:"range"`
}

func locations(t *testing.T, raw json.RawMessage) []wireLocation {
	t.Helper()
	var ls []wireLocation
	if err := json.Unmarshal(raw, &ls); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return ls
}

// checkBundleSession runs definition on std_logic, opens the file it
// names and queries inside it; root is the project folder.
func checkBundleSession(t *testing.T, e env.Env, root string,
	stat func(string) (fs.FileInfo, []byte, error),
) {
	topURI := server.PathToURI(filepath.Join(root, "top.vhd"))
	c := newClient(t, e, root)
	c.open(topURI, []byte(bundleTop))
	def := locations(t, c.request("textDocument/definition",
		at(t, topURI, []byte(bundleTop), "std_logic)", 0)))
	if len(def) != 1 || !strings.HasPrefix(def[0].URI, "file://") ||
		!strings.HasSuffix(def[0].URI, "/ieee/std_logic_1164.vhdl") {
		t.Fatalf("definition %+v, want the cached std_logic_1164.vhdl", def)
	}
	name, ok := server.URIToPath(def[0].URI)
	if !ok {
		t.Fatal(def[0].URI)
	}
	fi, data, err := stat(name)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := stdlib.Source("ieee/std_logic_1164.vhdl")
	if !bytes.Equal(data, want) {
		t.Fatal("the cached file differs from the bundled bytes")
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o444 {
		t.Fatalf("mode %v, want 0444", fi.Mode().Perm())
	}
	if runtime.GOOS == "windows" && fi.Mode().Perm()&0o200 != 0 {
		t.Fatalf("mode %v, want read-only", fi.Mode().Perm())
	}

	// The editor opens the cached file: it is the bundled unit.
	c.open(def[0].URI, data)
	var h struct {
		Contents struct{ Value string } `json:"contents"`
	}
	raw := c.request("textDocument/hover",
		at(t, def[0].URI, data, "STD_ULOGIC;", 0))
	if err := json.Unmarshal(raw, &h); err != nil ||
		!strings.Contains(h.Contents.Value, "type STD_ULOGIC is") ||
		!strings.Contains(h.Contents.Value, "from ieee.std_logic_1164") {
		t.Fatalf("hover in the cached file: %s", raw)
	}
	in := locations(t, c.request("textDocument/definition",
		at(t, def[0].URI, data, "STD_ULOGIC;", 0)))
	if len(in) != 1 || in[0].URI != def[0].URI {
		t.Fatalf("definition in the cached file: %+v", in)
	}
	for _, m := range c.msgs {
		if bytes.Contains(m.Params, []byte(server.BundleScheme)) {
			t.Fatalf("a bundle URI reached the client: %s %s", m.Method,
				m.Params)
		}
		if m.Method == "textDocument/publishDiagnostics" &&
			bytes.Contains(m.Params, []byte("std_logic_1164.vhdl")) {
			t.Fatalf("the cached file was analyzed as a project file: %s",
				m.Params)
		}
	}
}

func TestBundleFileURIsReal(t *testing.T) {
	work, cache := t.TempDir(), t.TempDir()
	t.Cleanup(func() { unlock(cache) })
	if err := os.WriteFile(filepath.Join(work, "top.vhd"),
		[]byte(bundleTop), 0o644); err != nil {
		t.Fatal(err)
	}
	e := realenv.New(realenv.Options{Dir: work})
	e.Getenv = env.LookupFunc(map[string]string{"XDG_CACHE_HOME": cache})
	checkBundleSession(t, e, work, func(n string) (fs.FileInfo, []byte,
		error,
	) {
		fi, err := os.Stat(n)
		if err != nil {
			return nil, nil, err
		}
		b, err := os.ReadFile(n)
		return fi, b, err
	})
	root, _ := stdlib.CacheRoot(e.Getenv, runtime.GOOS)
	if !strings.HasPrefix(root, cache) {
		t.Fatalf("cache root %s not under %s", root, cache)
	}
}

func unlock(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			_ = os.Chmod(p, 0o644)
		}
		return nil
	})
}

func simBundle(vars map[string]string, files map[string][]byte,
) *simenv.Sim {
	return simBundleFaults(vars, files, simenv.Faults{})
}

func simBundleFaults(vars map[string]string, files map[string][]byte,
	faults simenv.Faults,
) *simenv.Sim {
	all := map[string][]byte{"/work/top.vhd": []byte(bundleTop)}
	for k, v := range files {
		all[k] = v
	}
	return simenv.New(simenv.Options{Dir: "/work", Files: all, Vars: vars,
		Faults: faults})
}

func TestBundleFileURIsSim(t *testing.T) {
	sim := simBundle(map[string]string{"HOME": "/home/sim",
		"LocalAppData": "/home/sim/AppData/Local"}, nil)
	checkBundleSession(t, sim.Env(), "/work", func(n string) (fs.FileInfo,
		[]byte, error,
	) {
		fi, err := sim.FS.Stat(n)
		if err != nil {
			return nil, nil, err
		}
		b, err := sim.FS.ReadFile(n)
		return fi, b, err
	})
}

// A stale or tampered cached file is rewritten before its URI is sent.
func TestBundleRewritesMismatch(t *testing.T) {
	root := filepath.Join("/cache", "gohard", "bundle", stdlib.CacheKey())
	name := filepath.Join(root, "ieee", "std_logic_1164.vhdl")
	sim := simBundle(map[string]string{"XDG_CACHE_HOME": "/cache"},
		map[string][]byte{name: []byte("-- tampered\n")})
	checkBundleSession(t, sim.Env(), "/work", func(n string) (fs.FileInfo,
		[]byte, error,
	) {
		fi, err := sim.FS.Stat(n)
		if err != nil {
			return nil, nil, err
		}
		b, err := sim.FS.ReadFile(n)
		return fi, b, err
	})
}

// When the cache cannot be written (or found) the server does not
// crash and sends no bundle URI: the client gets one error message
// and the bundled locations are left out.
func TestBundleCacheFaults(t *testing.T) {
	cases := map[string]func() *simenv.Sim{
		"no cache dir": func() *simenv.Sim { return simBundle(nil, nil) },
		"cache is a file": func() *simenv.Sim {
			return simBundle(map[string]string{"XDG_CACHE_HOME": "/cache"},
				map[string][]byte{"/cache/gohard": []byte("x")})
		},
		"writes fail": func() *simenv.Sim {
			return simBundleFaults(
				map[string]string{"XDG_CACHE_HOME": "/cache"}, nil,
				simenv.Faults{WriteError: simenv.Always})
		},
		"mkdir fails": func() *simenv.Sim {
			s := simBundle(map[string]string{"XDG_CACHE_HOME": "/cache"},
				nil)
			s.FS.FailNext(simenv.OpMkdir, filepath.Join("/cache", "gohard",
				"bundle", stdlib.CacheKey()), nil)
			return s
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			sim := mk()
			c := newClient(t, sim.Env(), "/work")
			topURI := "file:///work/top.vhd"
			c.open(topURI, []byte(bundleTop))
			for range 2 {
				raw := c.request("textDocument/definition",
					at(t, topURI, []byte(bundleTop), "std_logic)", 0))
				if string(raw) != "[]" {
					t.Fatalf("definition %s, want []", raw)
				}
			}
			raw := c.request("textDocument/references", map[string]any{
				"textDocument": map[string]any{"uri": topURI},
				"position": at(t, topURI, []byte(bundleTop),
					"std_logic)", 0)["position"],
				"context": map[string]any{"includeDeclaration": true}})
			if bytes.Contains(raw, []byte(server.BundleScheme)) ||
				len(locations(t, raw)) == 0 {
				t.Fatalf("references %s", raw)
			}
			n := 0
			for _, m := range c.msgs {
				if m.Method == "window/showMessage" && bytes.Contains(
					m.Params, []byte("cannot write the bundled")) {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("%d error messages, want 1: %+v", n, c.msgs)
			}
		})
	}
}

package ide

import (
	"encoding/json"
	"flag"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env/simenv"
	"github.com/connerohnesorge/gohard/internal/server"
)

var neorv32 = flag.String("neorv32", "../../testdata/neorv32",
	"the NEORV32 checkout for the large-project query tests")

// project is a workspace under /work: files by slash path relative to
// /work (gohard.yaml included).
type project map[string][]byte

// snapshotOf returns the snapshot a server holds for p after it read
// the workspace and opened the documents open (path -> text), through
// the server's own code path (server.Driver over a simulated Env).
func snapshotOf(t testing.TB, p project, open map[string]string,
) *server.Snapshot {
	t.Helper()
	files := map[string][]byte{}
	for k, v := range p {
		files["/work/"+k] = v
	}
	sim := simenv.New(simenv.Options{Files: files, Dir: "/work"})
	d := server.NewDriver(server.Options{}, sim.Env(), 1,
		func(server.Send) error { return nil })
	send := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Deliver(b); err != nil {
			t.Fatal(err)
		}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"processId": nil,
			"rootUri": "file:///work", "capabilities": map[string]any{
				"general": map[string]any{
					"positionEncodings": []string{"utf-8"}}}}})
	send(map[string]any{"jsonrpc": "2.0", "method": "initialized",
		"params": map[string]any{}})
	for _, k := range slices.Sorted(maps.Keys(open)) {
		send(map[string]any{"jsonrpc": "2.0",
			"method": "textDocument/didOpen", "params": map[string]any{
				"textDocument": map[string]any{"uri": uri(k),
					"languageId": "vhdl", "version": 1, "text": open[k]}}})
	}
	return d.State.Snapshot()
}

func uri(rel string) string { return "file:///work/" + rel }

// run runs a task to the end with the server's slice budget and
// returns its value and the number of slices it took.
func run[T any](t server.Task[T]) (T, int) {
	for n := 1; ; n++ {
		sl := t.Run(server.SliceBudget)
		if sl.Done || sl.Next == nil {
			return sl.Value, n
		}
		t = sl.Next
	}
}

// analyze returns the analysis of a fresh analyzer.
func analyze(t testing.TB, snap *server.Snapshot) *Analysis {
	t.Helper()
	an, _ := run(New(Options{ToolVersion: "test"}).Analysis(snap))
	if an == nil || an.Snap != snap {
		t.Fatal("no analysis of the snapshot")
	}
	return an
}

// readTree reads the files below dir/sub (matching keep) into p under
// prefix.
func readTree(t testing.TB, p project, dir, sub, prefix string,
	keep func(string) bool,
) int {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(dir, sub))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range es {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".vhd") ||
			keep != nil && !keep(name) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, sub, name))
		if err != nil {
			t.Fatal(err)
		}
		p[prefix+name] = b
		n++
	}
	return n
}

// corpus returns every oracle corpus design (#8) as a project.
func corpus(t testing.TB) map[string]project {
	t.Helper()
	const dir = "../../testdata/corpus"
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]project{}
	for _, e := range es {
		if !e.IsDir() || e.Name() == "golden" {
			continue
		}
		p := project{}
		base := filepath.Join(dir, e.Name())
		err := filepath.WalkDir(base, func(f string, d fs.DirEntry,
			err error,
		) error {
			if err != nil || d.IsDir() {
				if d != nil && d.Name() == "golden" {
					return fs.SkipDir
				}
				return err
			}
			b, err := os.ReadFile(f)
			rel, _ := filepath.Rel(base, f)
			p[filepath.ToSlash(rel)] = b
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = p
	}
	return out
}

// osvvm is OSVVM with the files its osvvm.pro selects for VHDL-2019
// in a v1.0 tool (as sema's TestOSVVM), library osvvm in osvvm/.
func osvvm(t testing.TB) project {
	t.Helper()
	const root = "../../testdata/libs/osvvm"
	stubbed := []string{"AssertApiPkg", "FileLinePathPkg",
		"LanguageSupport2019Pkg"}
	p := project{"gohard.yaml": []byte("standard: \"2019\"\n" +
		"libraries:\n  osvvm: [osvvm]\n")}
	n := readTree(t, p, root, ".", "osvvm/", func(n string) bool {
		for _, s := range []string{"_Aldec", "_NVC", "_template",
			"MemoryPkgIs01"} {
			if strings.Contains(n, s) {
				return false
			}
		}
		return !slices.Contains(stubbed, strings.TrimSuffix(n, ".vhd"))
	})
	if n == 0 {
		t.Skip("testdata/libs/osvvm is not checked out")
	}
	readTree(t, p, root, "deprecated", "osvvm/deprecated/",
		func(n string) bool {
			for _, s := range stubbed {
				if n == s+"_c.vhd" {
					return true
				}
			}
			return false
		})
	return p
}

// uvvm is UVVM Utility Library, the VVC framework and the SBI VIP, in
// their three libraries.
func uvvm(t testing.TB) project {
	t.Helper()
	const root = "../../testdata/libs/uvvm"
	p := project{"gohard.yaml": []byte("libraries:\n" +
		"  uvvm_util: [uvvm_util/src]\n" +
		"  uvvm_vvc_framework: [uvvm_vvc_framework/src]\n" +
		"  bitvis_vip_sbi: [bitvis_vip_sbi/src]\n")}
	n := 0
	for _, d := range []string{"uvvm_util/src", "uvvm_vvc_framework/src",
		"bitvis_vip_sbi/src"} {
		n += readTree(t, p, root, d, d+"/", nil)
	}
	if n == 0 {
		t.Skip("testdata/libs/uvvm is not checked out")
	}
	return p
}

// neorv is the NEORV32 core and testbench (the large project of #19)
// in library neorv32.
func neorv(t testing.TB) project {
	t.Helper()
	p := project{"gohard.yaml": []byte("libraries:\n" +
		"  neorv32: [rtl/core, sim]\n")}
	n := readTree(t, p, *neorv32, "rtl/core", "rtl/core/", nil)
	n += readTree(t, p, *neorv32, "sim", "sim/", nil)
	if n == 0 {
		t.Skip("NEORV32 is not checked out (-neorv32 DIR)")
	}
	return p
}

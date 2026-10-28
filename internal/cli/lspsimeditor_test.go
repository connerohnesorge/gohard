package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/simrun"
)

// TestLSPRunSharesSimFixtures: the editor's run command (the code
// lens, `gohard lsp run`) gives every `gohard lsp sim` fixture's
// output byte for byte, and writes the same trace and repro file (#25,
// #35): the editor and the CLI share one code path.
func TestLSPRunSharesSimFixtures(t *testing.T) {
	if simrun.DefaultCode == nil {
		t.Skip("needs the sequential interpreter")
	}
	dirs, err := filepath.Glob(filepath.Join("..", "..", "testdata", "cli",
		"lsp-sim-*"))
	if err != nil || len(dirs) == 0 {
		t.Fatal("no lsp sim fixtures", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(dir, "want.stdout"))
			if err != nil {
				t.Fatal(err)
			}
			var rep struct {
				Testbench string  `json:"testbench"`
				Seed      *uint64 `json:"seed"`
				Trace     string  `json:"trace"`
				Repro     string  `json:"repro"`
			}
			if err := json.Unmarshal(want, &rep); err != nil {
				t.Fatal(err)
			}
			cmd, _ := os.ReadFile(filepath.Join(dir, "cmd"))
			f := strings.Fields(string(cmd))
			tb := f[slices.Index(f, "--tb")+1]
			files := map[string][]byte{}
			at := ""
			ents, _ := os.ReadDir(filepath.Join(dir, "in"))
			for _, e := range ents {
				b, _ := os.ReadFile(filepath.Join(dir, "in", e.Name()))
				files["/work/"+e.Name()] = b
				if bytes.Contains(bytes.ToLower(b),
					[]byte("entity "+tb+" ")) {
					at = e.Name()
				}
			}
			seed := ""
			if rep.Seed != nil {
				seed = strconv.FormatUint(*rep.Seed, 10)
			}
			cliArgs := []string{"lsp", "sim", "--tb", tb}
			edArgs := []string{"lsp", "run", "--at", at + ":1:1"}
			if seed != "" {
				cliArgs = append(cliArgs, "--seed", seed)
				edArgs = append(edArgs, seed)
			}
			run := func(args []string) ([]byte, *simenv.Sim) {
				sim := simenv.New(simenv.Options{Dir: "/work",
					Files: files})
				_, out, _ := runIn(sim, nil, args...)
				return out, sim
			}
			cliOut, cliSim := run(cliArgs)
			edOut, edSim := run(edArgs)
			if !bytes.Equal(cliOut, want) {
				t.Fatalf("gohard lsp sim:\n%s\nwant\n%s", cliOut, want)
			}
			if !bytes.Equal(edOut, want) {
				t.Fatalf("gohard lsp run:\n%s\nwant\n%s", edOut, want)
			}
			for _, name := range []string{rep.Trace, rep.Repro} {
				if name == "" {
					continue
				}
				a, err1 := cliSim.Env().FS.ReadFile("/work/" + name)
				b, err2 := edSim.Env().FS.ReadFile("/work/" + name)
				if err1 != nil || err2 != nil || !bytes.Equal(a, b) {
					t.Fatalf("%s differs (%v, %v):\n%s\nvs\n%s", name,
						err1, err2, a, b)
				}
			}
		})
	}
}

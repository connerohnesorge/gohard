package cli_test

import (
	"bytes"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env/simenv"
)

// TestCacheConfigLayout (#37): the analysis cache never changes
// results, also when only the layout of gohard.yaml changes. The
// library/unmapped quick fix points at the libraries: key, so moving
// the key, adding it or removing it must give a warm run the output of
// an uncached one.
func TestCacheConfigLayout(t *testing.T) {
	files := map[string][]byte{
		"/work/.git/HEAD": []byte("ref: refs/heads/main\n"),
		"/work/src/top.vhd": []byte("library nowhere;\n" +
			"use nowhere.npkg.all;\n\nentity top is\nend entity top;\n"),
		"/work/ip/nowhere/npkg.vhd": []byte("package npkg is\n" +
			"  constant k : integer := 3;\nend package npkg;\n"),
	}
	steps := []string{
		"libraries: {}\n",
		"# comment\n\nlibraries: {}\n",
		"",
		"standard: \"2008\"\n",
		"standard: \"2008\"\nlibraries: {}\n",
		"libraries: {}\nstandard: \"2008\"\n",
		"libraries: {}\n",
		"   \nlibraries: {}\n",
	}
	warm := simenv.New(simenv.Options{Dir: "/work", Files: files})
	for i, yaml := range steps {
		fresh := map[string][]byte{}
		for k, v := range files {
			fresh[k] = v
		}
		if yaml != "" {
			fresh["/work/gohard.yaml"] = []byte(yaml)
			if err := warm.FS.WriteFile("/work/gohard.yaml",
				[]byte(yaml), 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			_ = warm.FS.Remove("/work/gohard.yaml")
		}
		// gohard check, and the language server's quick fix.
		for _, args := range [][]string{{"check"},
			{"lsp", "codeaction", "--at", "src/top.vhd:1:9"}} {
			wc, wo, we := runIn(warm, nil, args...)
			if _, err := warm.FS.Stat(
				"/work/.gohard/cache/index"); err != nil {
				t.Fatalf("step %d: no cache written: %v", i, err)
			}
			cold := simenv.New(simenv.Options{Dir: "/work",
				Files: fresh})
			cc, co, ce := runIn(cold, nil, args...)
			if wc != cc || !bytes.Equal(wo, co) || !bytes.Equal(we, ce) {
				t.Errorf("step %d (%q) %v: warm exit %d\n%s%s\n"+
					"uncached exit %d\n%s%s", i, yaml, args, wc, wo, we,
					cc, co, ce)
			}
		}
	}
}

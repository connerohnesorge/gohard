package cli_test

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/connerohnesorge/gohard/v2/internal/cli"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
	"github.com/connerohnesorge/gohard/v2/internal/libs"
)

const guideRoot = "../../testdata/libsguide"

// guideBlocks returns the fenced blocks of docs/libraries.md that
// follow an `<!-- example: NAME WHAT -->` marker, by "NAME WHAT".
func guideBlocks(t *testing.T) (map[string]string, string) {
	t.Helper()
	raw, err := os.ReadFile("../../docs/libraries.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	re := regexp.MustCompile("(?s)<!-- example: (\\S+) (\\S+) -->\n" +
		"```\\w+\n(.*?)```\n")
	out := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(doc, -1) {
		k := m[1] + " " + m[2]
		if _, dup := out[k]; dup {
			t.Fatalf("docs/libraries.md: example %s shown twice", k)
		}
		out[k] = m[3]
	}
	return out, doc
}

// TestLibrariesGuide is the CI check of docs/libraries.md (#1): every
// example project under testdata/libsguide loads with no diagnostic
// at all (so every use resolves), the guide shows its VHDL files
// verbatim and maps the same libraries on Linux, macOS and Windows,
// with Windows folders that read back as drive-letter paths.
func TestLibrariesGuide(t *testing.T) {
	blocks, doc := guideBlocks(t)
	first := doc[strings.Index(doc, "\n## ")+4:]
	if !strings.HasPrefix(first, "Zero-install") {
		t.Errorf("the guide does not open with the zero-install "+
			"section: %q", first[:min(40, len(first))])
	}
	exs, err := os.ReadDir(guideRoot)
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	want := []string{"ghdl", "intel", "lattice", "osvvm", "uvvm",
		"vunit", "work", "xilinx"}
	var names []string
	for _, e := range exs {
		if !e.IsDir() {
			continue
		}
		names = append(names, e.Name())
		dir := filepath.Join(guideRoot, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			checkExample(t, dir, e.Name(), blocks, used)
		})
	}
	if !slices.Equal(names, want) {
		t.Errorf("examples = %v, want %v", names, want)
	}
	for k := range blocks {
		if !used[k] {
			t.Errorf("docs/libraries.md: example block %s has no "+
				"project", k)
		}
	}
}

// noCacheFS refuses to create any .gohard folder.
type noCacheFS struct{ env.FS }

func (f noCacheFS) MkdirAll(name string, perm fs.FileMode) error {
	if strings.Contains(filepath.ToSlash(name), ".gohard") {
		return fs.ErrPermission
	}
	return f.FS.MkdirAll(name, perm)
}

func checkExample(t *testing.T, dir, name string,
	blocks map[string]string, used map[string]bool,
) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	e := realenv.New(realenv.Options{Stdout: &stdout, Stderr: &stderr,
		Dir: abs})
	// The examples are committed folders: their analysis cache (#37)
	// cannot be created, so the check leaves the tree as it is.
	e.FS = noCacheFS{e.FS}
	code := cli.Run(context.Background(), e, []string{"check"})
	if code != 0 || !strings.Contains(stdout.String(),
		"summary: 0 errors, 0 warnings, 0 hints") {
		t.Errorf("gohard check: exit %d\n%s%s", code, stdout.String(),
			stderr.String())
	}
	// The VHDL files are the ones the guide shows.
	srcs, _ := filepath.Glob(filepath.Join(dir, "src", "*.vhd"))
	if len(srcs) == 0 {
		t.Fatal("no src/*.vhd")
	}
	for _, s := range srcs {
		k := name + " src/" + filepath.Base(s)
		used[k] = true
		text, err := os.ReadFile(s)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := blocks[k]
		if !ok {
			t.Errorf("docs/libraries.md does not show %s", k)
			continue
		}
		if got != strings.ReplaceAll(string(text), "\r\n", "\n") {
			t.Errorf("docs/libraries.md shows a different %s", k)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "gohard.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	ci := libraryNames(t, "gohard.yaml", string(raw))
	if name == "work" {
		if len(ci) != 0 {
			t.Errorf("the zero-install example maps %v", ci)
		}
		return
	}
	for _, osName := range []string{"linux", "macos", "windows"} {
		k := name + " " + osName
		used[k] = true
		text, ok := blocks[k]
		if !ok {
			t.Errorf("docs/libraries.md has no %s paths", k)
			continue
		}
		got := libraryNames(t, k, text)
		if !slices.Equal(keys(got), keys(ci)) {
			t.Errorf("%s maps %v, the CI project %v", k, keys(got),
				keys(ci))
		}
		for lib, folders := range got {
			for _, f := range folders {
				if !folderFor(osName, f) {
					t.Errorf("%s: %s folder %q is not a %s path", k,
						lib, f, osName)
				}
				if libs.IsStubPath(f) {
					t.Errorf("%s: %s maps a stub", k, lib)
				}
			}
		}
	}
}

var driveRE = regexp.MustCompile(`^[A-Z]:\\[^/]*$`)

// folderFor reports whether a parsed folder is an absolute path of the
// OS: a drive letter and backslashes on Windows.
func folderFor(osName, f string) bool {
	if osName == "windows" {
		return driveRE.MatchString(f) && !strings.Contains(f, `\\`)
	}
	return strings.HasPrefix(f, "/") && !strings.Contains(f, `\`)
}

func libraryNames(t *testing.T, what, text string) map[string][]string {
	t.Helper()
	var doc struct {
		Libraries map[string][]string `yaml:"libraries"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("%s is not YAML: %v", what, err)
	}
	return doc.Libraries
}

func keys(m map[string][]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestGuideGHDLIndexIsStub: the GHDL example's library index is
// followed back to the CI stubs, which detect reports and never maps
// (#1).
func TestGuideGHDLIndexIsStub(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join(guideRoot, "ghdl"))
	if err != nil {
		t.Fatal(err)
	}
	e := realenv.New(realenv.Options{Dir: abs})
	h := libs.Host{OS: "linux", FS: e.FS, Getenv: func(string) (string,
		bool,
	) {
		return "", false
	}}
	if filepath.Separator == '\\' {
		h.OS = "windows"
	}
	r := libs.Detect(h, abs)
	var found bool
	for _, in := range r.Installs {
		for _, l := range in.Libraries {
			if in.Product == "ghdl" && l.Name == "unisim" {
				found = true
				if !l.Stub || l.Mappable() {
					t.Errorf("unisim from the index: %+v", l)
				}
			}
		}
	}
	if !found {
		t.Fatalf("index not followed: %+v", r)
	}
}

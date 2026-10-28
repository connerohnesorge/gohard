package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// rerun is the hint printed with every stale-recording problem.
const rerun = "rerun go run ./tools/oracle --update"

// section is one kind of recording (see the package doc).
type section struct {
	name string
	// targets lists what the section records, in a fixed order. It is
	// pure: corpus.yaml and the repository only.
	targets func(x *ctx) ([]target, error)
	// record runs the tools for one target and returns exactly the
	// golden files named in t.Files.
	record func(x *ctx, tl *toolset, t target) ([]goldenFile, error)
	// check compares the recorded golden files of t with the current
	// GoHard output without running any tool. Each problem is one line.
	check func(x *ctx, t target, golden []goldenFile) []string
}

// target is one recorded unit of a section: the golden files it owns
// and the corpus.yaml field their hash goes to.
type target struct {
	Ref   entryRef
	Dir   string   // golden directory, repository-relative
	Files []string // golden file names in Dir owned by the section
}

// sections is the registry, in run order. Later stages append theirs
// (trace, tree).
var sections = []*section{formatSection, semerrSection,
	traceSection, treeSection, goldenSection, coverageSection,
	stdlibSection, conformanceSection}

// ctx is the state shared by the sections: the repository root, the
// corpus.yaml bytes and their parsed form.
type ctx struct {
	root string
	src  []byte
	man  *manifest
	// plans caches pure per-design work of the sections.
	plans map[string]any
}

func loadCtx(root string) (*ctx, error) {
	src, err := readRepo(root, manifestPath)
	if err != nil {
		return nil, err
	}
	m, err := parseManifest(src)
	if err != nil {
		return nil, err
	}
	return &ctx{root: root, src: src, man: m, plans: map[string]any{}}, nil
}

// design returns the corpus design named name.
func (x *ctx) design(name string) (design, bool) {
	for _, d := range x.man.Designs {
		if d.Name == name {
			return d, true
		}
	}
	return design{}, false
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("oracle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	update := fs.Bool("update", false,
		"run GHDL/NVC and rewrite changed goldens and hashes")
	designs := fs.String("design", "",
		"comma-separated entry names to limit the run to")
	list := fs.Bool("list", false, "list the sections and exit")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: go run ./tools/oracle [--update] "+
			"[-design a,b] [section ...]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *list {
		for _, s := range sections {
			fmt.Fprintln(stdout, s.name)
		}
		return 0
	}
	sel, err := selectSections(fs.Args())
	if err != nil {
		fmt.Fprintln(stderr, "oracle:", err)
		return 2
	}
	root, err := findRoot()
	if err != nil {
		fmt.Fprintln(stderr, "oracle:", err)
		return 2
	}
	var only []string
	if *designs != "" {
		only = strings.Split(*designs, ",")
	}
	if *update {
		if err := doUpdate(root, sel, only, &toolset{},
			stdout); err != nil {
			fmt.Fprintln(stderr, "oracle:", err)
			return 2
		}
	}
	probs, err := doCheck(root, sel, only)
	if err != nil {
		fmt.Fprintln(stderr, "oracle:", err)
		return 2
	}
	for _, p := range probs {
		fmt.Fprintln(stdout, p)
	}
	if len(probs) > 0 {
		return 1
	}
	fmt.Fprintln(stdout, "oracle: recorded results match")
	return 0
}

func selectSections(names []string) ([]*section, error) {
	if len(names) == 0 {
		return sections, nil
	}
	var out []*section
	for _, n := range names {
		i := slices.IndexFunc(sections, func(s *section) bool {
			return s.name == n
		})
		if i < 0 {
			return nil, fmt.Errorf("unknown section %q", n)
		}
		if !slices.Contains(out, sections[i]) {
			out = append(out, sections[i])
		}
	}
	return out, nil
}

// findRoot walks up from the working directory to the module root.
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && bytes.Contains(b,
			[]byte("module github.com/connerohnesorge/gohard/v2\n")) {
			return dir, nil
		}
		up := filepath.Dir(dir)
		if up == dir {
			return "", errors.New("not inside the gohard module")
		}
		dir = up
	}
}

func selected(t target, only []string) bool {
	return len(only) == 0 || slices.Contains(only, t.Ref.Name)
}

// doUpdate records every selected target, writes the golden files
// whose bytes changed and rewrites the changed hashes in corpus.yaml.
func doUpdate(root string, sel []*section, only []string, tl *toolset,
	stdout io.Writer) error {
	x, err := loadCtx(root)
	if err != nil {
		return err
	}
	var refs []entryRef
	for _, s := range sel {
		ts, err := s.targets(x)
		if err != nil {
			return err
		}
		for _, t := range ts {
			if !selected(t, only) {
				continue
			}
			fmt.Fprintf(stdout, "oracle: %s %s\n", s.name, t.Ref.Name)
			files, err := s.record(x, tl, t)
			if err != nil {
				return fmt.Errorf("%s %s: %w", s.name, t.Ref.Name, err)
			}
			if err := writeGolden(root, t, files, stdout); err != nil {
				return err
			}
			// A target without a hash field feeds no corpus.yaml hash
			// (the stdlib section).
			if t.Ref.Field != "" && !slices.Contains(refs, t.Ref) {
				refs = append(refs, t.Ref)
			}
		}
	}
	src := x.src
	for _, ref := range refs {
		h, err := fieldHash(x, ref)
		if err != nil {
			return err
		}
		out, changed, err := setField(src, ref, h)
		if err != nil {
			return err
		}
		if changed {
			fmt.Fprintf(stdout, "oracle: %s = %s\n", ref, h)
			src = out
		}
	}
	if !bytes.Equal(src, x.src) {
		return writeFile(root, manifestPath, src)
	}
	return nil
}

// writeGolden writes the files of t whose bytes differ from disk.
func writeGolden(root string, t target, files []goldenFile,
	stdout io.Writer) error {
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	if !slices.Equal(names, t.Files) {
		return fmt.Errorf("%s: recorded %v, want %v", t.Ref, names,
			t.Files)
	}
	for _, f := range files {
		rel := path.Join(t.Dir, f.Name)
		old, err := readRepo(root, rel)
		if err == nil && bytes.Equal(old, f.Data) {
			continue
		}
		if err := writeFile(root, rel, f.Data); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "oracle: wrote %s\n", rel)
	}
	return nil
}

// writeFile writes a repository-relative file through a temporary file
// and a rename.
func writeFile(root, rel string, data []byte) error {
	name := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".oracle-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), name)
}

// fieldHash hashes the golden files of every registered section's
// targets that feed ref, read from disk, named relative to their
// golden directory.
func fieldHash(x *ctx, ref entryRef) (string, error) {
	var files []goldenFile
	for _, s := range sections {
		ts, err := s.targets(x)
		if err != nil {
			return "", err
		}
		for _, t := range ts {
			if t.Ref != ref {
				continue
			}
			got, missing := readGolden(x.root, t)
			if len(missing) > 0 {
				return "", fmt.Errorf("%s: missing %s", ref,
					strings.Join(missing, ", "))
			}
			files = append(files, got...)
		}
	}
	return goldenHash(files), nil
}

// readGolden reads the golden files of t and lists the missing ones.
func readGolden(root string, t target) ([]goldenFile, []string) {
	var got []goldenFile
	var missing []string
	for _, n := range t.Files {
		rel := path.Join(t.Dir, n)
		b, err := readRepo(root, rel)
		if err != nil {
			missing = append(missing, rel)
			continue
		}
		got = append(got, goldenFile{Name: n, Data: b})
	}
	return got, missing
}

// doCheck verifies the recorded results of the selected sections
// without running any tool.
func doCheck(root string, sel []*section, only []string) ([]string,
	error) {
	x, err := loadCtx(root)
	if err != nil {
		return nil, err
	}
	var probs []string
	var refs []entryRef
	for _, s := range sel {
		ts, err := s.targets(x)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			if !selected(t, only) {
				continue
			}
			got, missing := readGolden(root, t)
			for _, m := range missing {
				probs = append(probs, fmt.Sprintf(
					"%s %s: %s is not recorded: %s", s.name,
					t.Ref.Name, m, rerun))
			}
			if len(missing) > 0 {
				continue
			}
			for _, p := range s.check(x, t, got) {
				probs = append(probs, s.name+" "+t.Ref.Name+": "+p)
			}
			// A target without a hash field feeds no corpus.yaml hash
			// (the stdlib section).
			if t.Ref.Field != "" && !slices.Contains(refs, t.Ref) {
				refs = append(refs, t.Ref)
			}
		}
	}
	for _, ref := range refs {
		want, err := fieldHash(x, ref)
		if err != nil {
			probs = append(probs, err.Error()+": "+rerun)
			continue
		}
		have, ok, err := fieldValue(x.src, ref)
		if err != nil {
			return nil, err
		}
		switch {
		case !ok:
			probs = append(probs, fmt.Sprintf("%s: %s is missing: %s",
				manifestPath, ref, rerun))
		case have != want:
			probs = append(probs, fmt.Sprintf(
				"%s: %s is %s, the golden files hash to %s: %s",
				manifestPath, ref, have, want, rerun))
		}
	}
	return probs, nil
}

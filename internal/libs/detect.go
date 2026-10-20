package libs

import (
	"cmp"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Install is one vendor tool install, or one GHDL library index, that
// the search found.
type Install struct {
	// Product is the tool id: vivado, quartus, diamond or ghdl.
	Product string
	// Name is the tool as people call it ("Xilinx Vivado").
	Name string
	// Version is the version folder ("2023.2"); "" for a GHDL index.
	Version string
	// Dir is the install folder (for GHDL the index file) as the host
	// writes it.
	Dir string
	// Via says how it was found: a variable name, "vivado on PATH",
	// "the usual install folder" or "the project".
	Via string
	// Libraries are the libraries of the install, in rule order.
	Libraries []Library
}

// Library is one library folder of an install.
type Library struct {
	// Name is the VHDL library name.
	Name string
	// Folder is the source folder as gohard.yaml writes it: a host
	// path, or a project-relative slash path inside the project.
	Folder string
	// Unreadable, when set, is why the library cannot be read as VHDL
	// source ("IEEE 1735 encrypted IP", "precompiled for
	// ModelSim/Questa"): no config line is proposed for it.
	Unreadable string
	// Opaque are the encrypted units (lib.unit) inside a readable
	// folder, for allow_opaque.
	Opaque []string
	// Stub is set for GoHard's own CI stub packages (testdata/stubs),
	// which are never written into a user's gohard.yaml.
	Stub bool
}

// Mappable reports whether the library gets a libraries: line.
func (l Library) Mappable() bool { return l.Unreadable == "" && !l.Stub }

// Result is the outcome of Detect.
type Result struct {
	// Installs are sorted by product (in Products order), then
	// version, then folder.
	Installs []Install
}

// Detect searches h for vendor library sources (#1): the usual
// Vivado, Quartus and Diamond install folders of h.OS, the folders
// named by the vendor variables and the vendor programs on PATH, and
// GHDL library indexes (in GHDL's vendors folder and anywhere in the
// project) followed back to their VHDL sources. root is the project
// root as an FS path ("" for none). Nothing is written; the result
// depends only on the files and variables of h.
func Detect(h Host, root string) Result {
	d := &detector{h: h, root: h.fromFS(root)}
	var out []Install
	for _, p := range products {
		out = append(out, d.product(p)...)
	}
	out = append(out, d.ghdl()...)
	return Result{Installs: out}
}

type detector struct {
	h    Host
	root string // project root, slash host path or ""
}

// fromFS turns an FS path into a slash host path ("" when it is not
// under Root).
func (h Host) fromFS(p string) string {
	if p == "" {
		return ""
	}
	if h.Root == "" {
		return h.slash(p)
	}
	rel, err := filepath.Rel(h.Root, p)
	if err != nil || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if h.windows() {
		vol, rest, _ := strings.Cut(rel, "/")
		if len(vol) != 1 || !isLetter(vol[0]) {
			return ""
		}
		return h.slash(vol + ":/" + rest)
	}
	return path.Clean("/" + rel)
}

func (d *detector) bases(m map[string][]string) []string {
	bs, ok := m[d.h.OS]
	if !ok {
		bs = m["linux"]
	}
	var out []string
	for _, b := range bs {
		if rest, ok := strings.CutPrefix(b, "~/"); ok {
			home := d.h.home()
			if home == "" {
				continue
			}
			b = path.Join(home, rest)
		}
		out = append(out, b)
	}
	return out
}

type found struct{ dir, via string }

// product finds the installs of p: variables first, then PATH, then
// the usual folders; one install per version, the first found.
func (d *detector) product(p product) []Install {
	var cands []found
	for _, e := range p.envs {
		if v := d.h.slash(d.h.getenv(e.name)); v != "" {
			cands = append(cands, found{up(v, e.up), e.name})
		}
	}
	for _, pr := range p.progs {
		if exe, ok := d.h.lookPath(pr.name); ok {
			cands = append(cands, found{up(path.Dir(exe), pr.up),
				pr.name + " on PATH"})
		}
	}
	for _, b := range d.bases(p.bases) {
		for _, l := range p.layouts {
			for _, dir := range d.glob(b, l) {
				cands = append(cands, found{dir,
					"the usual install folder"})
			}
		}
	}
	var out []Install
	seen := map[string]bool{}
	for _, c := range cands {
		ver := versionOf(c.dir)
		if seen[c.dir] || seen["v:"+ver] ||
			!d.h.isDir(path.Join(c.dir, p.marker)) {
			continue
		}
		seen[c.dir], seen["v:"+ver] = true, true
		in := Install{Product: p.id, Name: p.name, Version: ver,
			Dir: d.h.Show(c.dir), Via: c.via}
		for _, r := range p.libs {
			in.Libraries = append(in.Libraries, d.rule(c.dir, r)...)
		}
		out = append(out, in)
	}
	slices.SortStableFunc(out, func(a, b Install) int {
		return cmp.Or(compareVersions(a.Version, b.Version),
			cmp.Compare(a.Dir, b.Dir))
	})
	return out
}

// glob expands layout (one "*" element) under base.
func (d *detector) glob(base, layout string) []string {
	before, after, ok := strings.Cut(layout, "*")
	if !ok {
		return []string{path.Join(base, layout)}
	}
	parent := path.Join(base, before)
	var out []string
	for _, e := range d.h.readDir(parent) {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dir := path.Join(parent, e.Name(), after)
		if d.h.isDir(dir) {
			out = append(out, dir)
		}
	}
	return out
}

// versionOf is the version of an install folder: its own name or its
// parent's, whichever starts with a digit.
func versionOf(dir string) string {
	for _, p := range []string{dir, path.Dir(dir)} {
		if b := path.Base(p); b != "" && '0' <= b[0] && b[0] <= '9' {
			return b
		}
	}
	return path.Base(dir)
}

// compareVersions orders versions by their numbers, then as text.
func compareVersions(a, b string) int {
	na, nb := numbers(a), numbers(b)
	for i := range min(len(na), len(nb)) {
		if c := cmp.Compare(na[i], nb[i]); c != 0 {
			return c
		}
	}
	return cmp.Or(cmp.Compare(len(na), len(nb)), cmp.Compare(a, b))
}

func numbers(s string) []int {
	var out []int
	n, in := 0, false
	for i := 0; i <= len(s); i++ {
		if i < len(s) && '0' <= s[i] && s[i] <= '9' {
			n, in = n*10+int(s[i]-'0'), true
			continue
		}
		if in {
			out = append(out, n)
		}
		n, in = 0, false
	}
	return out
}

// rule returns the libraries of r under the install folder dir that
// exist.
func (d *detector) rule(dir string, r libRule) []Library {
	folder := path.Join(dir, r.rel)
	if !r.each {
		if !d.h.isDir(folder) {
			return nil
		}
		return []Library{d.library(r.name, folder)}
	}
	var out []Library
	for _, e := range d.h.readDir(folder) {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		f := path.Join(folder, e.Name(), r.sub)
		if d.h.isDir(f) {
			out = append(out, d.library(strings.ToLower(e.Name()), f))
		}
	}
	return out
}

// library classifies the folder of library name.
func (d *detector) library(name, folder string) Library {
	c := d.h.classify(folder)
	l := Library{Name: name, Folder: d.show(folder),
		Stub: c.stub || IsStubPath(folder)}
	switch {
	case c.plain > 0:
		for _, u := range c.opaque {
			l.Opaque = append(l.Opaque, name+"."+u)
		}
		slices.Sort(l.Opaque)
		l.Opaque = slices.Compact(l.Opaque)
	case c.encrypted > 0:
		l.Unreadable = "IEEE 1735 encrypted IP"
	case c.tool != "":
		l.Unreadable = "precompiled for " + c.tool
	default:
		l.Unreadable = "a folder without VHDL source files"
	}
	return l
}

// show writes a slash host path for gohard.yaml: project-relative
// inside the project, else as the host writes it.
func (d *detector) show(p string) string {
	if d.root != "" {
		if p == d.root {
			return "."
		}
		if rest, ok := strings.CutPrefix(p, strings.TrimSuffix(d.root,
			"/")+"/"); ok {
			return rest
		}
	}
	return d.h.Show(p)
}

// IsStubPath reports whether a slash or OS path lies in a
// testdata/stubs folder: GoHard's CI stub packages (#1), never a
// substitute for the vendor files.
func IsStubPath(p string) bool {
	parts := strings.Split(strings.ReplaceAll(p, `\`, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], "testdata") &&
			strings.EqualFold(parts[i+1], "stubs") {
			return true
		}
	}
	return false
}

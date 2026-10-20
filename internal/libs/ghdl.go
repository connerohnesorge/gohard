package libs

import (
	"path"
	"slices"
	"strings"
)

// ghdl finds GHDL library indexes (LIB-objNN.cf): under the vendors
// folder of every GHDL library folder (GHDL_PREFIX, ghdl on PATH and
// the usual folders) and anywhere in the project, and follows each
// back to the VHDL sources it was compiled from.
func (d *detector) ghdl() []Install {
	type dir struct{ p, via string }
	var dirs []dir
	if v := d.h.slash(d.h.getenv("GHDL_PREFIX")); v != "" {
		dirs = append(dirs, dir{path.Join(v, "vendors"), "GHDL_PREFIX"})
	}
	if exe, ok := d.h.lookPath("ghdl"); ok {
		dirs = append(dirs, dir{path.Join(up(exe, 2), "lib/ghdl/vendors"),
			"ghdl on PATH"})
	}
	for _, b := range d.bases(ghdlBases) {
		dirs = append(dirs, dir{path.Join(b, "vendors"),
			"the usual install folder"})
	}
	if d.root != "" {
		dirs = append(dirs, dir{d.root, "the project"})
	}
	var out []Install
	seen := map[string]bool{}
	for _, dr := range dirs {
		for _, cf := range d.indexes(dr.p) {
			if seen[cf] {
				continue
			}
			seen[cf] = true
			if l, ok := d.index(cf); ok {
				out = append(out, Install{Product: "ghdl",
					Name: "GHDL library index", Dir: d.show(cf),
					Via: dr.via, Libraries: []Library{l}})
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Install) int {
		return strings.Compare(a.Dir, b.Dir)
	})
	return out
}

// indexes returns the GHDL library index files under dir, skipping
// dot folders.
func (d *detector) indexes(dir string) []string {
	var out []string
	n := 0
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		for _, e := range d.h.readDir(dir) {
			if n++; n > maxEntries {
				return
			}
			name := e.Name()
			p := path.Join(dir, name)
			switch {
			case e.IsDir():
				if !strings.HasPrefix(name, ".") && depth < maxDepth {
					walk(p, depth+1)
				}
			case ghdlLibrary(name) != "":
				out = append(out, p)
			}
		}
	}
	walk(dir, 0)
	return out
}

// ghdlLibrary is the library of an index file name (unisim-obj08.cf),
// or "" for other names and for the bundled std and ieee.
func ghdlLibrary(name string) string {
	base, ok := strings.CutSuffix(strings.ToLower(name), ".cf")
	if !ok {
		return ""
	}
	i := strings.LastIndex(base, "-obj")
	if i <= 0 {
		return ""
	}
	lib := base[:i]
	if lib == "std" || lib == "ieee" {
		return ""
	}
	return lib
}

// index reads one GHDL library index and returns its library mapped to
// the deepest folder holding every source file it lists.
func (d *detector) index(cf string) (Library, bool) {
	text, ok := d.h.readFile(cf)
	if !ok {
		return Library{}, false
	}
	name := ghdlLibrary(path.Base(cf))
	var srcs []string
	for _, line := range strings.Split(string(text), "\n") {
		if src, ok := d.indexFile(cf, line); ok {
			srcs = append(srcs, src)
		}
	}
	if len(srcs) == 0 {
		return Library{Name: name, Folder: d.show(path.Dir(cf)),
			Unreadable: "precompiled for GHDL without its sources"}, true
	}
	folder := commonDir(srcs)
	for _, s := range srcs {
		if !d.h.isFile(s) {
			return Library{Name: name, Folder: d.show(folder),
				Unreadable: "a GHDL library whose VHDL sources are " +
					"missing"}, true
		}
	}
	return d.library(name, folder), true
}

// indexFile parses one `file DIR "NAME" "SUM" "TIME":` line of a GHDL
// index (DIR is "/" for an absolute NAME, "." for the folder of the
// index, or a quoted folder) and returns the source path.
func (d *detector) indexFile(cf, line string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "file ")
	if !ok {
		return "", false
	}
	var dir string
	switch {
	case strings.HasPrefix(rest, "/ "):
		rest = rest[2:]
	case strings.HasPrefix(rest, ". "):
		dir, rest = path.Dir(cf), rest[2:]
	default:
		q, r, ok := quoted(rest)
		if !ok {
			return "", false
		}
		dir, rest = q, strings.TrimPrefix(r, " ")
	}
	name, _, ok := quoted(rest)
	if !ok || name == "" {
		return "", false
	}
	if abs := d.h.slash(name); abs != "" {
		return abs, true
	}
	if dir == "" {
		return "", false
	}
	if abs := d.h.slash(dir); abs != "" {
		dir = abs
	} else {
		dir = path.Join(path.Dir(cf), strings.ReplaceAll(dir, `\`, "/"))
	}
	return path.Join(dir, strings.ReplaceAll(name, `\`, "/")), true
}

// quoted splits a leading "..." string (GHDL doubles a quote inside).
func quoted(s string) (string, string, bool) {
	if !strings.HasPrefix(s, `"`) {
		return "", s, false
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		if s[i] != '"' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '"' {
			b.WriteByte('"')
			i++
			continue
		}
		return b.String(), s[i+1:], true
	}
	return "", s, false
}

// commonDir returns the deepest folder holding every file.
func commonDir(files []string) string {
	common := strings.Split(path.Dir(files[0]), "/")
	for _, f := range files[1:] {
		parts := strings.Split(path.Dir(f), "/")
		n := 0
		for n < len(common) && n < len(parts) && common[n] == parts[n] {
			n++
		}
		common = common[:n]
	}
	if len(common) == 1 && common[0] == "" {
		return "/"
	}
	return strings.Join(common, "/")
}

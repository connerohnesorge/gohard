package libs

import (
	"bytes"
	"path"
	"strings"
)

// Walk limits: a library folder is classified from at most this many
// entries and this depth, so a huge or looping tree stays bounded.
const (
	maxEntries = 20000
	maxDepth   = 8
)

// StubMarker starts the first line of every testdata/stubs file: a
// folder holding one is a CI stub wherever it was copied.
const StubMarker = "-- gohard-ci-stub"

// class is what a folder holds.
type class struct {
	plain, encrypted int
	opaque           []string // encrypted unit names (lower case)
	tool             string   // precompiled-library tool, if any
	stub             bool
}

// classify walks folder (slash host path) and counts its plain and
// encrypted VHDL files and the precompiled-library files of
// simulators.
func (h Host) classify(folder string) class {
	var c class
	n := 0
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		for _, e := range h.readDir(dir) {
			if n++; n > maxEntries {
				return
			}
			name := e.Name()
			p := path.Join(dir, name)
			if e.IsDir() {
				if !strings.HasPrefix(name, ".") && depth < maxDepth {
					walk(p, depth+1)
				}
				continue
			}
			if t := toolOf(name); t != "" && c.tool == "" {
				c.tool = t
			}
			if !isVHDL(name) {
				continue
			}
			text, ok := h.readFile(p)
			if !ok {
				continue
			}
			if bytes.HasPrefix(text, []byte(StubMarker)) {
				c.stub = true
			}
			if i := encryptedAt(text); i >= 0 {
				c.encrypted++
				if u := unitName(text[:i]); u != "" {
					c.opaque = append(c.opaque, u)
				}
				continue
			}
			c.plain++
		}
	}
	walk(folder, 0)
	return c
}

func isVHDL(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return ext == ".vhd" || ext == ".vhdl"
}

// toolOf names the simulator whose precompiled library file name is.
func toolOf(name string) string {
	l := strings.ToLower(name)
	switch {
	case l == "_info" || strings.HasSuffix(l, ".qdb") ||
		strings.HasSuffix(l, ".qpg") || strings.HasSuffix(l, ".qtl"):
		return "ModelSim/Questa"
	case strings.HasSuffix(l, ".cf"):
		return "GHDL"
	case strings.HasSuffix(l, ".lib") || l == "library.cfg":
		return "Active-HDL/Riviera-PRO"
	}
	return ""
}

// encryptedAt returns the offset of the first IEEE 1735 protected
// envelope (`protect begin_protected, VHDL-2019 24.1) in text, or -1.
func encryptedAt(text []byte) int {
	low := bytes.ToLower(text)
	for _, m := range []string{"`protect begin_protected",
		"`pragma protect begin_protected"} {
		if i := bytes.Index(low, []byte(m)); i >= 0 {
			return i
		}
	}
	return -1
}

// unitName returns the entity or package the plain text before an
// envelope declares (its interface is often left readable), or "".
func unitName(text []byte) string {
	var words []string
	for _, line := range strings.Split(string(text), "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		words = append(words, strings.FieldsFunc(strings.ToLower(line),
			func(r rune) bool {
				return !(r == '_' || 'a' <= r && r <= 'z' ||
					'0' <= r && r <= '9')
			})...)
	}
	for i := 0; i+2 < len(words); i++ {
		if (words[i] == "entity" || words[i] == "package") &&
			words[i+1] != "body" && words[i+2] == "is" {
			return words[i+1]
		}
	}
	return ""
}

package importer

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"path"
	"slices"
	"strings"
)

// xprFileSets are the FileSet types whose files are imported: the
// design sources (sources_1) and the simulation sources (sim_1).
var xprFileSets = []string{"DesignSrcs", "SimulationSrcs"}

// xprStandards maps a Vivado file type (FileInfo SFType, the FILE_TYPE
// property without spaces) to gohard standards. Vivado's "VHDL" is
// VHDL-93.
var xprStandards = map[string]string{
	"VHDL": "1993", "VHDL2008": "2008", "VHDL2019": "2019",
}

// xprFile is one File element being read.
type xprFile struct {
	path, sfType, library string
	off                   int
	disabled              bool
}

// ParseXPR reads a Vivado .xpr project (#2): the File elements of the
// DesignSrcs and SimulationSrcs filesets, with their Library attribute
// (xil_defaultlib, Vivado's default, when absent) and file type
// (SFType "VHDL" or none on a .vhd/.vhdl file is VHDL-1993, "VHDL2008"
// and "VHDL2019", also written with a space). Paths start with
// $PPRDIR (the folder of the .xpr), $PSRCDIR, $PGENDIR or
// $PIPUSERFILESDIR (its NAME.srcs, NAME.gen and NAME.ip_user_files
// folders), or are absolute or relative to the .xpr folder. Other
// filesets (constraints, utilities, IP) are skipped silently; a file
// that is not VHDL, is disabled or uses another path variable is
// skipped with an import/ignored hint.
func ParseXPR(name string, src []byte) Project {
	l := newLines(name, src)
	proj := strings.TrimSuffix(path.Base(name), path.Ext(name))
	dec := xml.NewDecoder(bytes.NewReader(src))
	var p Project
	var stack []string
	fileset := ""
	var cur *xprFile
	root := false
	for {
		off := int(dec.InputOffset())
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if !root {
				p.Diagnostics = append(p.Diagnostics, l.syntax(
					"Vivado .xpr project", 0, "no Project element"))
			}
			return p
		}
		if err != nil {
			p.Diagnostics = append(p.Diagnostics, l.syntax(
				"Vivado .xpr project", int(dec.InputOffset()),
				err.Error()))
			return p
		}
		switch t := tok.(type) {
		case xml.StartElement:
			el := t.Name.Local
			root = true
			if len(stack) == 0 && el != "Project" {
				p.Diagnostics = append(p.Diagnostics, l.syntax(
					"Vivado .xpr project", off, "the root element is "+
						el+", not Project"))
				return p
			}
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			stack = append(stack, el)
			switch {
			case el == "FileSet" && parent == "FileSets":
				fileset = attr(t, "Type")
			case el == "File" && parent == "FileSet" &&
				slices.Contains(xprFileSets, fileset):
				cur = &xprFile{path: attr(t, "Path"), off: off,
					library: "xil_defaultlib"}
			case el == "FileInfo" && parent == "File" && cur != nil:
				cur.sfType = attr(t, "SFType")
			case el == "Attr" && parent == "FileInfo" && cur != nil:
				p.xprAttr(cur, attr(t, "Name"), attr(t, "Val"))
			}
		case xml.EndElement:
			if len(stack) == 0 {
				continue
			}
			el := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			switch {
			case el == "File" && cur != nil:
				p.xprEntry(l, proj, *cur)
				cur = nil
			case el == "FileSet":
				fileset = ""
			}
		}
	}
}

func attr(t xml.StartElement, name string) string {
	for _, a := range t.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// xprAttr applies one FileInfo Attr of the file being read.
func (p *Project) xprAttr(f *xprFile, name, val string) {
	switch name {
	case "Library":
		f.library = val
	case "IsEnabled":
		f.disabled = f.disabled || val == "0" ||
			strings.EqualFold(val, "false")
	case "AutoDisabled":
		f.disabled = f.disabled || val == "1" ||
			strings.EqualFold(val, "true")
	}
}

// xprEntry adds the file f, or the hint that skips it.
func (p *Project) xprEntry(l lines, proj string, f xprFile) {
	file, ok := xprPath(f.path, proj)
	if !ok {
		p.Diagnostics = append(p.Diagnostics, l.ignored(f.off, f.path,
			"the path variable is not $PPRDIR, $PSRCDIR, $PGENDIR or "+
				"$PIPUSERFILESDIR"))
		return
	}
	if f.disabled {
		p.Diagnostics = append(p.Diagnostics, l.ignored(f.off, f.path,
			"the file is disabled in the project"))
		return
	}
	typ := strings.ToUpper(strings.ReplaceAll(f.sfType, " ", ""))
	std, vhdl := xprStandards[typ]
	switch {
	case typ == "" && isVHDL(file):
		std = "1993"
	case !vhdl:
		reason := "not a VHDL file"
		if typ != "" {
			reason = "file type " + f.sfType + " is not VHDL"
		}
		p.Diagnostics = append(p.Diagnostics, l.ignored(f.off, f.path,
			reason))
		return
	}
	e, d := l.file(file, f.library, std, f.off)
	if d != nil {
		p.Diagnostics = append(p.Diagnostics, *d)
		return
	}
	p.Entries = append(p.Entries, e)
}

// xprPath expands the Vivado path variable of p for the project proj,
// giving a path relative to the .xpr folder (or absolute).
func xprPath(p, proj string) (string, bool) {
	p = strings.ReplaceAll(p, `\`, "/")
	if !strings.HasPrefix(p, "$") {
		return p, true
	}
	v, rest, _ := strings.Cut(p[1:], "/")
	dir := ""
	switch v {
	case "PPRDIR":
		dir = "."
	case "PSRCDIR":
		dir = proj + ".srcs"
	case "PGENDIR":
		dir = proj + ".gen"
	case "PIPUSERFILESDIR":
		dir = proj + ".ip_user_files"
	default:
		return "", false
	}
	return path.Join(dir, rest), true
}

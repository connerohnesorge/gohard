package importer

import (
	"path"
	"slices"
	"strings"
)

// doStandards maps the vcom standard options to gohard standards.
// GoHard's oldest standard is VHDL-1993, so -87 reads as 1993.
var doStandards = map[string]string{
	"-87": "1993", "-93": "1993", "-2002": "2002", "-2008": "2008",
	"-2019": "2019",
}

// vcomValued are the vcom options that take a value. Their values are
// skipped; -work and -f are handled on their own.
var vcomValued = []string{
	"-cover", "-coveropt", "-error", "-fatal", "-just", "-l",
	"-modelsimini", "-note", "-outf", "-pslfile", "-reportprogress",
	"-skip", "-suppress", "-warning",
}

// substReason is the import/ignored reason of a command that uses Tcl
// substitution.
const substReason = "it uses Tcl substitution ($ or [...]), which " +
	"gohard import does not evaluate"

// vmapping is one vmap NAME PATH line.
type vmapping struct{ name, dir string }

// ParseDo reads a ModelSim/Questa .do script (#2): vlib, vmap and
// vcom. vcom gives the files, its -work option the library (work when
// absent; a vmap logical name, or a library folder whose last element
// is the name) and -87, -93, -2002, -2008 or -2019 the standard (none
// when absent: the tool's default comes from modelsim.ini, which is not
// read). Relative paths are relative to the folder of the script.
// Every other command, and a vlib, vmap or vcom that uses Tcl
// substitution, is skipped with an import/ignored hint naming its
// line; other vcom options are skipped silently.
func ParseDo(name string, src []byte) Project {
	l := newLines(name, src)
	cmds, serr := splitTcl(src)
	var p Project
	var maps []vmapping
	for _, c := range cmds {
		head := c.Words[0]
		switch head.Text {
		case "vlib", "vmap", "vcom":
		default:
			p.Diagnostics = append(p.Diagnostics, l.ignored(c.Off,
				head.Text, "gohard import reads only vlib, vmap "+
					"and vcom"))
			continue
		}
		if slices.ContainsFunc(c.Words, func(w word) bool {
			return w.Subst
		}) {
			p.Diagnostics = append(p.Diagnostics, l.ignored(c.Off,
				head.Text, substReason))
			continue
		}
		args := c.Words[1:]
		switch head.Text {
		case "vlib":
			// A library folder; its name comes from vcom -work and
			// vmap, so there is nothing to record.
		case "vmap":
			var pos []string
			for _, a := range args {
				if !strings.HasPrefix(a.Text, "-") {
					pos = append(pos, a.Text)
				}
			}
			if len(pos) != 2 {
				p.Diagnostics = append(p.Diagnostics, l.ignored(c.Off,
					"vmap", "not of the form vmap NAME PATH"))
				continue
			}
			maps = append(maps, vmapping{pos[0], cleanDir(pos[1])})
		case "vcom":
			p.vcom(l, args, maps)
		}
	}
	if serr != nil {
		p.Diagnostics = append(p.Diagnostics, l.syntax(
			"ModelSim .do script", serr.Off, serr.Reason))
	}
	return p
}

// vcom reads the options and files of one vcom command. Options apply
// to every file of the command wherever they stand, as in vcom.
func (p *Project) vcom(l lines, args []word, maps []vmapping) {
	lib, std := "work", ""
	var files []word
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a.Text == "-work" && i+1 < len(args):
			i++
			lib = libraryOf(args[i].Text, maps)
		case a.Text == "-f" && i+1 < len(args):
			i++
			p.Diagnostics = append(p.Diagnostics, l.ignored(a.Off,
				"vcom -f "+args[i].Text, "argument files are not read"))
		case doStandards[a.Text] != "":
			std = doStandards[a.Text]
		case slices.Contains(vcomValued, a.Text):
			i++
		case strings.HasPrefix(a.Text, "-") ||
			strings.HasPrefix(a.Text, "+"):
		default:
			files = append(files, a)
		}
	}
	for _, f := range files {
		e, d := l.file(f.Text, lib, std, f.Off)
		if d != nil {
			p.Diagnostics = append(p.Diagnostics, *d)
			continue
		}
		p.Entries = append(p.Entries, e)
	}
}

// libraryOf returns the library name of vcom -work w: w itself when a
// vmap names it, the vmap name of the folder w, else the last element
// of the folder w.
func libraryOf(w string, maps []vmapping) string {
	for _, m := range maps {
		if strings.EqualFold(m.name, w) {
			return m.name
		}
	}
	dir := cleanDir(w)
	for _, m := range maps {
		if m.dir == dir {
			return m.name
		}
	}
	return path.Base(dir)
}

func cleanDir(d string) string {
	return path.Clean(strings.ReplaceAll(d, `\`, "/"))
}

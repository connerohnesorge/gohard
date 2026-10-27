package importer

import (
	"slices"
	"strings"
)

// qsfStandards maps Quartus HDL versions to gohard standards. GoHard's
// oldest standard is VHDL-1993, so VHDL_1987 reads as 1993.
var qsfStandards = map[string]string{
	"VHDL_1987": "1993", "VHDL_1993": "1993", "VHDL_2008": "2008",
	"VHDL_2019": "2019",
}

// qsfCommands are the Quartus assignment commands of a .qsf file.
// Assignments that name no source file are skipped silently.
var qsfCommands = []string{
	"set_global_assignment", "set_instance_assignment",
	"set_io_assignment", "set_location_assignment", "set_parameter",
	"set_user_option",
}

// qsfFlags are the assignment options that take no value.
var qsfFlags = []string{"-disable", "-fall", "-remove", "-rise"}

// qsfAssignment is one set_global_assignment.
type qsfAssignment struct {
	name, value, library, version string
	valueOff                      int
	disabled                      bool
}

// ParseQSF reads a Quartus .qsf settings file (#2): every
// set_global_assignment -name VHDL_FILE with its -library (work when
// absent) and -hdl_version (VHDL_1987 reads as 1993, VHDL_1993,
// VHDL_2008, VHDL_2019), and the global VHDL_INPUT_VERSION for files
// without -hdl_version. Relative paths are relative to the folder of
// the .qsf file. Another *_FILE assignment (Verilog, IP, constraint
// files), a disabled VHDL_FILE and any command that is not an
// assignment are skipped with an import/ignored hint naming the line;
// other assignments are skipped silently.
func ParseQSF(name string, src []byte) Project {
	l := newLines(name, src)
	cmds, serr := splitTcl(src)
	var p Project
	var files []qsfAssignment
	global := ""
	for _, c := range cmds {
		head := c.Words[0]
		if !slices.Contains(qsfCommands, head.Text) {
			p.Diagnostics = append(p.Diagnostics, l.ignored(c.Off,
				head.Text, "not a Quartus assignment"))
			continue
		}
		if head.Text != "set_global_assignment" {
			continue
		}
		if slices.ContainsFunc(c.Words, func(w word) bool {
			return w.Subst
		}) {
			p.Diagnostics = append(p.Diagnostics, l.ignored(c.Off,
				head.Text, substReason))
			continue
		}
		a := qsfAssign(c.Words[1:])
		switch {
		case a.name == "VHDL_INPUT_VERSION":
			if std, ok := qsfStandards[a.value]; ok {
				global = std
			} else {
				p.Diagnostics = append(p.Diagnostics, l.ignored(c.Off,
					"VHDL_INPUT_VERSION "+a.value, "not VHDL_1987, "+
						"VHDL_1993, VHDL_2008 or VHDL_2019"))
			}
		case a.name == "VHDL_FILE" && a.disabled:
			p.Diagnostics = append(p.Diagnostics, l.ignored(c.Off,
				a.value, "the assignment is disabled or removed"))
		case a.name == "VHDL_FILE":
			if _, ok := qsfStandards[a.version]; a.version != "" && !ok {
				p.Diagnostics = append(p.Diagnostics, l.ignored(c.Off,
					"-hdl_version "+a.version, "not VHDL_1987, "+
						"VHDL_1993, VHDL_2008 or VHDL_2019"))
			}
			if a.valueOff < 0 {
				p.Diagnostics = append(p.Diagnostics, l.ignored(c.Off,
					"VHDL_FILE", "the assignment names no file"))
				continue
			}
			files = append(files, a)
		case strings.HasSuffix(a.name, "_FILE"):
			p.Diagnostics = append(p.Diagnostics, l.ignored(c.Off,
				a.name+" "+a.value, "not a VHDL_FILE assignment"))
		}
	}
	for _, a := range files {
		std := qsfStandards[a.version]
		if a.version == "" {
			std = global
		}
		e, d := l.file(a.value, a.library, std, a.valueOff)
		if d != nil {
			p.Diagnostics = append(p.Diagnostics, *d)
			continue
		}
		p.Entries = append(p.Entries, e)
	}
	if serr != nil {
		p.Diagnostics = append(p.Diagnostics, l.syntax(
			"Quartus .qsf file", serr.Off, serr.Reason))
	}
	return p
}

// qsfAssign reads the options and the value of one assignment.
func qsfAssign(args []word) qsfAssignment {
	a := qsfAssignment{library: "work", valueOff: -1}
	for i := 0; i < len(args); i++ {
		w := args[i].Text
		switch {
		case slices.Contains(qsfFlags, w):
			a.disabled = a.disabled || w == "-disable" || w == "-remove"
		case strings.HasPrefix(w, "-") && i+1 < len(args):
			i++
			v := args[i].Text
			switch w {
			case "-name":
				a.name = strings.ToUpper(v)
			case "-library":
				a.library = v
			case "-hdl_version":
				a.version = strings.ToUpper(v)
			}
		case a.valueOff < 0:
			a.value, a.valueOff = w, args[i].Off
		}
	}
	return a
}

// Package importer reads the project files of other VHDL tools and
// turns them into a gohard.yaml (#2): ModelSim/Questa .do scripts,
// Vivado .xpr projects and Quartus .qsf settings files. It reads
// sources only, never compiled libraries (.dat, _info, .dbs), and runs
// nothing: Tcl is split into words, not evaluated.
//
// Every function here is pure. A Format turns the bytes of one project
// file into the VHDL files it lists, each with its library and VHDL
// standard (Parse). Plan groups those entries into the libraries and
// the one standard of a gohard.yaml, and Render writes its text. The
// CLI (`gohard import`) reads the file, resolves the listed paths and
// checks that they exist (import/missing-file), then writes the
// result. New formats (#64) add a row to Formats.
package importer

import (
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/span"
)

// Entry is one VHDL source file a project file lists.
type Entry struct {
	// Path is the file as the project names it, with '/' separators:
	// absolute, or relative to the folder of the project file (for a
	// Script, the folder it runs from). After the CLI resolves it,
	// relative to the project root.
	Path string
	// Library is the library the file is compiled into, as written.
	Library string
	// Standard is "1993", "2002", "2008" or "2019"; "" when the project
	// does not say.
	Standard string
	// Line and Col are where the project file lists the file.
	Line, Col int
}

// Project is what a Format reads from one project file.
type Project struct {
	Entries []Entry
	// Diagnostics are located in the project file (import/ignored,
	// import/syntax).
	Diagnostics []diag.Diagnostic
}

// Format is one project-file format gohard import reads.
type Format struct {
	// Ext is the file extension, lower case, with the dot.
	Ext string
	// Name names the format in messages ("ModelSim .do script").
	Name string
	// Parse reads src, the content of the project file shown as name
	// (the path diagnostics use; the Vivado project name comes from
	// it).
	Parse func(name string, src []byte) Project
	// Script reports a script the tool runs from a folder of the
	// user's choosing (ModelSim .do): relative paths are relative to
	// that folder, not to the file's, and a file path may hold the
	// wildcards *, ? and [...], which the tool expands (vcom does).
	Script bool
}

// Formats is every format gohard import reads, by extension.
var Formats = []Format{
	{Ext: ".do", Name: "ModelSim .do script", Parse: ParseDo,
		Script: true},
	{Ext: ".qsf", Name: "Quartus .qsf file", Parse: ParseQSF},
	{Ext: ".xpr", Name: "Vivado .xpr project", Parse: ParseXPR},
}

// FormatOf returns the format of the file name by its extension, in any
// letter case.
func FormatOf(name string) (Format, bool) {
	ext := strings.ToLower(path.Ext(strings.ReplaceAll(name, `\`, "/")))
	i := slices.IndexFunc(Formats, func(f Format) bool {
		return f.Ext == ext
	})
	if i < 0 {
		return Format{}, false
	}
	return Formats[i], true
}

// Extensions lists the extensions of Formats for messages: ".do, .qsf
// and .xpr".
func Extensions() string {
	var exts []string
	for _, f := range Formats {
		exts = append(exts, f.Ext)
	}
	last := len(exts) - 1
	return strings.Join(exts[:last], ", ") + " and " + exts[last]
}

// Library is one library of the written gohard.yaml.
type Library struct {
	Name  string
	Files []string // root-relative slash paths, in project order
}

// Config is the content of the gohard.yaml gohard import writes.
type Config struct {
	// Standard is the newest standard any entry states, or "" when no
	// entry states one or it is the default ("2008").
	Standard  string
	Libraries []Library // sorted by name, case-insensitively
	Files     int       // the number of distinct files
}

// Plan groups the entries of the project file name into libraries and
// picks the standard. A path gohard.yaml cannot hold (a control
// character, not UTF-8) is skipped with an import/ignored hint. A file
// listed twice keeps its first library;
// listed in another library it is import/library-conflict. Entries
// whose standard is not the chosen one get one import/standard hint
// per standard, at the first such entry.
func Plan(name string, entries []Entry) (Config, []diag.Diagnostic) {
	var ds []diag.Diagnostic
	var c Config
	first := map[string]Entry{}
	var kept []Entry
	for _, e := range entries {
		if !printable(e.Path) {
			ds = append(ds, diag.New(diag.ImportIgnored, e.Path,
				"the path holds a control character or is not UTF-8").
				At(name, e.Line, e.Col))
			continue
		}
		if f, ok := first[e.Path]; ok {
			if !strings.EqualFold(f.Library, e.Library) {
				ds = append(ds, diag.New(diag.ImportLibraryConflict,
					e.Path, f.Library, e.Library).
					At(name, e.Line, e.Col).WithRelated(diag.Related{
					Path: name, Line: f.Line, Col: f.Col,
					Message: "first listed here"}))
			}
			continue
		}
		first[e.Path] = e
		kept = append(kept, e)
	}
	c.Files = len(kept)
	chosen := ""
	for _, e := range kept {
		chosen = max(chosen, e.Standard)
	}
	for _, std := range []string{"1993", "2002", "2008", "2019"} {
		if std == chosen {
			continue
		}
		n := 0
		var at Entry
		for _, e := range kept {
			if e.Standard == std {
				if n == 0 {
					at = e
				}
				n++
			}
		}
		if n > 0 {
			ds = append(ds, diag.New(diag.ImportStandard, std,
				strconv.Itoa(n), chosen).At(name, at.Line, at.Col))
		}
	}
	if chosen != config.Defaults().Standard {
		c.Standard = chosen
	}
	for _, e := range kept {
		i := slices.IndexFunc(c.Libraries, func(l Library) bool {
			return strings.EqualFold(l.Name, e.Library)
		})
		if i < 0 {
			c.Libraries = append(c.Libraries, Library{Name: e.Library})
			i = len(c.Libraries) - 1
		}
		c.Libraries[i].Files = append(c.Libraries[i].Files, e.Path)
	}
	slices.SortStableFunc(c.Libraries, func(a, b Library) int {
		return strings.Compare(strings.ToLower(a.Name),
			strings.ToLower(b.Name))
	})
	return c, diag.Normalize(ds)
}

// Render returns the gohard.yaml text of c, written from the project
// file name.
func Render(name string, c Config) []byte {
	var b strings.Builder
	b.WriteString("# Written by gohard import from " +
		strings.Map(func(r rune) rune {
			if !unicode.IsPrint(r) {
				return '?'
			}
			return r
		}, name) + ".\n")
	if c.Standard != "" {
		b.WriteString("standard: \"" + c.Standard + "\"\n")
	}
	if len(c.Libraries) > 0 {
		b.WriteString("libraries:\n")
	}
	for _, l := range c.Libraries {
		b.WriteString("  " + scalar(l.Name) + ":\n")
		for _, f := range l.Files {
			b.WriteString("    - " + scalar(f) + "\n")
		}
	}
	return []byte(b.String())
}

// yamlWords are plain scalars YAML 1.1 reads as bools or null.
var yamlWords = []string{"y", "n", "yes", "no", "true", "false", "on",
	"off", "null"}

// scalar writes s as a YAML scalar: plain when it holds only letters,
// digits and "_./+-", starts with a letter, digit, '_' or '.', and is
// not one of yamlWords; single-quoted otherwise.
func scalar(s string) string {
	plain := s != "" && !slices.Contains(yamlWords, strings.ToLower(s))
	for i := range len(s) {
		c := s[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' || c == '_' || c == '.' ||
			i > 0 && (c == '/' || c == '+' || c == '-')
		plain = plain && ok
	}
	if plain {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// printable reports whether s is UTF-8 made of printable characters
// and spaces only, so gohard.yaml can hold it (YAML allows no control
// characters, and reads U+0085, U+2028 and U+2029 as line breaks).
func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// isVHDL reports a .vhd or .vhdl extension in any letter case, the
// files gohard reads (workspace.IsVHDL).
func isVHDL(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return ext == ".vhd" || ext == ".vhdl"
}

// lines locates byte offsets of one project file.
type lines struct {
	name string
	idx  *span.Index
}

func newLines(name string, src []byte) lines {
	return lines{name: name, idx: span.NewIndex(src)}
}

// at returns d located at offset off.
func (l lines) at(d diag.Diagnostic, off int) diag.Diagnostic {
	line, col := l.idx.LineCol(off)
	return d.At(l.name, line, col)
}

// entry returns an Entry located at offset off.
func (l lines) entry(p, lib, std string, off int) Entry {
	line, col := l.idx.LineCol(off)
	return Entry{Path: p, Library: lib, Standard: std, Line: line,
		Col: col}
}

// ignored returns the import/ignored hint for item at off.
func (l lines) ignored(off int, item, reason string) diag.Diagnostic {
	return l.at(diag.New(diag.ImportIgnored, item, reason), off)
}

// file checks one listed source file: a VHDL extension and a library
// name gohard.yaml accepts. It returns the entry, or the
// import/ignored hint that skips it.
func (l lines) file(p, lib, std string, off int) (Entry, *diag.Diagnostic) {
	if !isVHDL(p) {
		d := l.ignored(off, p, "not a .vhd or .vhdl file")
		return Entry{}, &d
	}
	if !config.IsIdentifier(lib) {
		d := l.ignored(off, p, "library name "+strconv.Quote(lib)+
			" is not a VHDL identifier")
		return Entry{}, &d
	}
	return l.entry(p, lib, std, off), nil
}

// syntax returns the import/syntax error of format at off.
func (l lines) syntax(format string, off int, reason string) diag.Diagnostic {
	return l.at(diag.New(diag.ImportSyntax, format, reason), off)
}

package importer

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/workspace"
)

// proFormat names the format in import/syntax messages.
const proFormat = "OSVVM .pro script"

// proBudget is the most commands one import of a .pro script reads,
// with the scripts it includes and the branches it takes, and
// proByteBudget the most script text it splits, counting a script
// included twice twice (P7). The 83 .pro scripts of PoC together hold
// 2,624 lines in 97 kilobytes; the budgets stop scripts that include
// each other without end.
const (
	proBudget     = 20000
	proByteBudget = 4 << 20
)

// proMaxDepth is the deepest nesting of includes, if branches and
// [command] substitutions a .pro import follows.
const proMaxDepth = 100

// proCommands lists the commands a .pro import reads, for the hint
// that skips any other.
const proCommands = "gohard import reads only library, analyze, " +
	"RunTest, include, build, SetVHDLVersion and if"

// proProfile is the fixed variable profile (#64) that .pro if
// conditions and $variable substitutions are evaluated over: GoHard as
// an OSVVM tool that supports generic packages, and PoC's vendor-free
// (GENERIC) build with its default configuration files. The names are
// written without the leading "::". ::osvvm::VhdlVersion is not here:
// it follows SetVHDLVersion, and is 2008 until a script sets it. Any
// other variable is unknown, and a command that needs it is skipped
// with an import/ignored hint. docs/libraries.md lists the profile;
// TestProProfileDocumented keeps the two in step.
var proProfile = map[string]string{
	"osvvm::ClockResetVersion":                       "2026.09",
	"osvvm::FunctionalCoverageIntegratedInSimulator": "default",
	"osvvm::OsvvmDevDerivePackageHeaders":            "false",
	"osvvm::Supports2019AssertApi":                   "true",
	"osvvm::Supports2019FilePath":                    "true",
	"osvvm::Supports2019Generics":                    "true",
	"osvvm::Supports2019ImpureFunctions":             "true",
	"osvvm::ToolName":                                "GoHard",
	"osvvm::ToolNameVersion":                         "GoHard",
	"osvvm::ToolSupportsGenericPackages":             "true",
	"osvvm::ToolVendor":                              "GoHard",
	"osvvm::UseMemoryPkg01":                          "false",
	"poc::localConfigurationFile": "../tb/common/" +
		"local_configuration.vhdl",
	"poc::projectConfigurationFile": "../tb/common/" +
		"project_configuration_GENERIC.vhdl",
	"poc::vendorName": "GENERIC",
}

// proStandards maps the SetVHDLVersion arguments to gohard standards.
// GoHard's oldest standard is VHDL-1993, so 1987 reads as 1993.
var proStandards = map[string]string{
	"87": "1993", "1987": "1993", "93": "1993", "1993": "1993",
	"02": "2002", "2002": "2002", "08": "2008", "2008": "2008",
	"19": "2019", "2019": "2019",
}

// proDefaultLibrary is the library OSVVM analyzes into before a script
// names one.
const proDefaultLibrary = "default"

// ParsePro reads an OSVVM .pro script (#64) the way OSVVM runs it, but
// without running Tcl: library sets the library of the files that
// follow ("default" before any), analyze and RunTest give a file,
// include and build read another script (a folder: its NAME.pro, else
// its build.pro; a name without extension: NAME.pro),
// SetVHDLVersion sets the standard of the files that follow, and
// if/elseif/else takes the branch its conditions select under
// proProfile. Paths are relative to the folder of the script that
// names them. A $variable outside the profile, a [command] other than
// OSVVM's FindOsvvmScriptSettingsPkg and FindSettingsPkgBody (which
// give their default files) and string compare, and any other
// command skip their command (for if, every branch) with an
// import/ignored hint naming the line; the options after an analyze
// file are skipped silently. A missing included script is
// import/missing-file. The import reads at most proBudget commands and
// proByteBudget bytes of script.
func ParsePro(name string, src []byte, files Files) Project {
	self := path.Base(name)
	r := &proRun{files: files, main: name, stack: []string{self},
		srcs: map[string][]byte{}}
	r.script(proScript{dir: ".", src: src, l: newLines(name, src)}, 0,
		len(src), 0)
	return r.p
}

// proRun is one import of a .pro script and the scripts it includes.
type proRun struct {
	files Files
	main  string // the project file, as diagnostics show it
	p     Project
	lib   string // the library analyze uses; "" before any
	std   string // the standard SetVHDLVersion set; "" before any
	steps int
	bytes int      // the script text split so far
	stop  bool     // a budget ran out
	stack []string // the scripts being read, outermost first
	srcs  map[string][]byte
}

// proScript is one script being read.
type proScript struct {
	dir string // its folder, relative to the project file's folder
	src []byte
	l   lines
}

// script reads the commands of s.src[from:to].
func (r *proRun) script(s proScript, from, to, depth int) {
	if r.stop {
		return
	}
	if r.bytes += to - from; r.bytes > proByteBudget {
		r.over(s, from, "the scripts hold more than "+
			strconv.Itoa(proByteBudget>>20)+" MiB, counting each "+
			"include")
		return
	}
	cmds, serr := splitTclRange(s.src, from, to)
	for _, c := range cmds {
		if r.stop {
			return
		}
		if r.steps++; r.steps > proBudget {
			r.over(s, c.Off, "the scripts run more than "+
				strconv.Itoa(proBudget)+" commands")
			return
		}
		r.command(s, c, depth)
	}
	if serr != nil && !r.stop {
		r.p.Diagnostics = append(r.p.Diagnostics, s.l.syntax(proFormat,
			serr.Off, serr.Reason))
	}
}

// over stops the import at off of s: a budget ran out.
func (r *proRun) over(s proScript, off int, reason string) {
	r.p.Diagnostics = append(r.p.Diagnostics, s.l.syntax(proFormat, off,
		reason))
	r.stop = true
}

// ignored adds the import/ignored hint for item at off of s.
func (r *proRun) ignored(s proScript, off int, item, reason string) {
	r.p.Diagnostics = append(r.p.Diagnostics, s.l.ignored(off, item,
		reason))
}

// command reads one command.
func (r *proRun) command(s proScript, c command, depth int) {
	head := c.Words[0]
	name, why := r.word(s.src, head, depth)
	if why != "" {
		r.ignored(s, c.Off, head.Text, why)
		return
	}
	args := c.Words[1:]
	arg := func(what string) (string, bool) {
		if len(args) == 0 {
			r.ignored(s, c.Off, name, "it names no "+what)
			return "", false
		}
		v, why := r.word(s.src, args[0], depth)
		if why != "" {
			r.ignored(s, c.Off, name, why)
			return "", false
		}
		return v, true
	}
	switch name {
	case "library":
		if v, ok := arg("library"); ok {
			r.lib = v
		}
	case "analyze", "RunTest":
		v, ok := arg("file")
		if !ok {
			return
		}
		lib := r.lib
		if lib == "" {
			lib = proDefaultLibrary
		}
		e, d := s.l.file(join(s.dir, v), lib, r.std, args[0].Off)
		if d != nil {
			r.p.Diagnostics = append(r.p.Diagnostics, *d)
			return
		}
		r.p.Entries = append(r.p.Entries, e)
	case "include", "build":
		if v, ok := arg("script"); ok {
			r.include(s, args[0].Off, join(s.dir, v), depth)
		}
	case "SetVHDLVersion":
		v, ok := arg("version")
		if !ok {
			return
		}
		std, known := proStandards[v]
		if !known {
			r.ignored(s, c.Off, name+" "+v, "not 1993, 2002, 2008 or "+
				"2019 (or 87, 93, 02, 08, 19)")
			return
		}
		r.std = std
	case "if":
		r.branch(s, c, depth)
	default:
		r.ignored(s, c.Off, name, proCommands)
	}
}

// include reads the one script that the path p (relative to the
// project file's folder), named by s at off, stands for, as OSVVM's
// FindIncludeFile picks it: for a folder, NAME.pro when it exists
// (NAME the folder's own name without extension, also for "." and
// ".."), else build.pro; for a .pro file, the file; for a name without
// extension, NAME.pro.
func (r *proRun) include(s proScript, off int, p string, depth int) {
	t := p
	name, dir := r.files.Folder(p)
	ext := strings.ToLower(path.Ext(p))
	switch {
	case dir:
		t = path.Join(p, strings.TrimSuffix(name, path.Ext(name))+".pro")
		if _, err := r.read(t); errors.Is(err, fs.ErrNotExist) {
			t = path.Join(p, "build.pro")
		}
	case ext == ".pro":
	case ext == "":
		t = p + ".pro"
	default:
		r.ignored(s, off, "include "+p, "gohard import reads only "+
			".pro scripts")
		return
	}
	r.run(s, off, t, depth)
}

// run reads the included script t.
func (r *proRun) run(s proScript, off int, t string, depth int) {
	shown := join(path.Dir(r.main), t)
	switch {
	case slices.Contains(r.stack, t):
		r.ignored(s, off, "include "+t, "the script is already being "+
			"read, so including it again would never end")
		return
	case depth >= proMaxDepth:
		r.ignored(s, off, "include "+t, "includes nest more than "+
			strconv.Itoa(proMaxDepth)+" deep")
		return
	}
	src, err := r.read(t)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.p.Diagnostics = append(r.p.Diagnostics, s.l.at(diag.New(
			diag.ImportMissingFile, shown), off))
		return
	case err != nil:
		r.p.Diagnostics = append(r.p.Diagnostics, s.l.at(diag.New(
			diag.WorkspaceUnreadable, shown, workspace.Reason(err)),
			off))
		return
	}
	r.stack = append(r.stack, t)
	r.script(proScript{dir: path.Dir(t), src: src,
		l: newLines(shown, src)}, 0, len(src), depth+1)
	r.stack = r.stack[:len(r.stack)-1]
}

// read returns the script t, read once.
func (r *proRun) read(t string) ([]byte, error) {
	if b, ok := r.srcs[t]; ok {
		return b, nil
	}
	b, err := r.files.Read(t)
	if err == nil {
		r.srcs[t] = b
	}
	return b, err
}

// proArm is one branch of an if command; cond is nil for else.
type proArm struct {
	keyword string
	cond    *word
	body    word
}

// branch reads an if command: if EXPR ?then? BODY ?elseif EXPR ?then?
// BODY ...? ?else? ?BODY?. It reads the body of the first branch whose
// condition is true; a condition it cannot evaluate skips the command.
func (r *proRun) branch(s proScript, c command, depth int) {
	arms, ok := ifArms(c.Words)
	if !ok {
		r.ignored(s, c.Off, "if", "not of the form if {expr} {body} "+
			"?elseif {expr} {body}? ?else {body}?")
		return
	}
	for _, a := range arms {
		if a.cond != nil {
			v, why := r.condition(s.src, *a.cond, depth)
			if why != "" {
				r.ignored(s, a.cond.Off, a.keyword, why)
				return
			}
			if !v {
				continue
			}
		}
		switch {
		case s.src[a.body.Off] != '{':
			r.ignored(s, a.body.Off, a.keyword, "the body is not a "+
				"{braced} script")
		case depth >= proMaxDepth:
			r.ignored(s, a.body.Off, a.keyword, "branches nest more "+
				"than "+strconv.Itoa(proMaxDepth)+" deep")
		default:
			r.script(s, a.body.Off+1, a.body.End-1, depth+1)
		}
		return
	}
}

// ifArms splits the words of an if command into its branches.
func ifArms(w []word) ([]proArm, bool) {
	var arms []proArm
	i, keyword := 1, "if"
	for {
		if i+1 >= len(w) {
			return nil, false
		}
		cond := w[i]
		i++
		if w[i].Text == "then" && i+1 < len(w) {
			i++
		}
		arms = append(arms, proArm{keyword: keyword, cond: &cond,
			body: w[i]})
		i++
		switch {
		case i == len(w):
			return arms, true
		case w[i].Text == "elseif":
			keyword = "elseif"
			i++
		case w[i].Text == "else" && i+2 == len(w):
			return append(arms, proArm{keyword: "else",
				body: w[i+1]}), true
		case w[i].Text == "else":
			return nil, false
		case i+1 == len(w):
			return append(arms, proArm{keyword: "else", body: w[i]}),
				true
		default:
			return nil, false
		}
	}
}

// word returns the value of w, a word of src: its text, with the
// variables and commands of a bare or "quoted" word substituted. why
// is the reason it cannot be evaluated.
func (r *proRun) word(src []byte, w word, depth int) (v, why string) {
	if !w.Subst {
		return w.Text, ""
	}
	raw := string(src[w.Off:w.End])
	if raw[0] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	return r.subst(raw, depth)
}

// subst performs the Tcl substitutions of raw, the text of a bare or
// quoted word: backslash sequences, $name and ${name} from the
// profile, and [command] for the commands call knows.
func (r *proRun) subst(raw string, depth int) (string, string) {
	var b strings.Builder
	for i := 0; i < len(raw); {
		switch c := raw[i]; c {
		case '\\':
			n, s := backslash(raw[i:])
			b.WriteString(s)
			i += n
		case '$':
			name, n := varName(raw[i+1:])
			if n == 0 {
				b.WriteByte('$')
				i++
				continue
			}
			v, why := r.variable(name, raw[i+1+n:])
			if why != "" {
				return "", why
			}
			b.WriteString(v)
			i += 1 + n
		case '[':
			end := closeBracket(raw, i)
			if end < 0 {
				return "", "it holds an unterminated ["
			}
			v, why := r.call(raw[i+1:end], depth)
			if why != "" {
				return "", why
			}
			b.WriteString(v)
			i = end + 1
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), ""
}

// backslash returns the length and value of the backslash sequence at
// the start of s, as the scanner reads it.
func backslash(s string) (int, string) {
	if len(s) < 2 {
		return len(s), `\`
	}
	switch c := s[1]; c {
	case '\n', '\r':
		n := 2
		if c == '\r' && len(s) > 2 && s[2] == '\n' {
			n = 3
		}
		for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
			n++
		}
		return n, " "
	case 'n':
		return 2, "\n"
	case 't':
		return 2, "\t"
	default:
		return 2, string(c)
	}
}

// varName reads the variable name after a '$' at the start of s:
// ${any text} or letters, digits, '_' and "::" separators. n is the
// length read, 0 when s holds no name (the '$' is literal).
func varName(s string) (name string, n int) {
	if strings.HasPrefix(s, "{") {
		end := strings.IndexByte(s, '}')
		if end < 0 {
			return "", 0
		}
		return s[1:end], end + 1
	}
	for n < len(s) {
		c := s[n]
		switch {
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9':
			n++
		case c == ':' && n+1 < len(s) && s[n+1] == ':':
			for n < len(s) && s[n] == ':' {
				n++
			}
		default:
			return s[:n], n
		}
	}
	return s[:n], n
}

// variable returns the profile value of the variable name; rest is the
// text after the name (an array element starts with '(').
func (r *proRun) variable(name, rest string) (string, string) {
	if strings.HasPrefix(rest, "(") {
		return "", "it uses the array element $" + name + "(...), " +
			"which the import profile does not set"
	}
	key := strings.TrimPrefix(name, "::")
	if key == "osvvm::VhdlVersion" {
		if r.std == "" {
			return config.Defaults().Standard, ""
		}
		return r.std, ""
	}
	if v, ok := proProfile[key]; ok {
		return v, ""
	}
	return "", "it uses $" + name + ", which the import profile " +
		"does not set"
}

// closeBracket returns the index of the ']' that closes the '[' at
// open in s, or -1.
func closeBracket(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '[':
			depth++
		case ']':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// call returns the value of the command substitution [script]: the
// default file of OSVVM's FindOsvvmScriptSettingsPkg and
// FindSettingsPkgBody NAME, or Tcl's string compare. Any other command
// cannot be evaluated.
func (r *proRun) call(script string, depth int) (string, string) {
	src := []byte(script)
	cmds, serr := splitTcl(src)
	if serr != nil || len(cmds) != 1 {
		return "", "it uses [" + strings.TrimSpace(script) + "], " +
			"which gohard import does not evaluate"
	}
	ws := cmds[0].Words
	unknown := "it uses [" + ws[0].Text + "], which gohard import " +
		"does not evaluate"
	if len(ws) > 1 {
		unknown = "it uses [" + ws[0].Text + " ...], which gohard " +
			"import does not evaluate"
	}
	if depth >= proMaxDepth {
		return "", unknown
	}
	var args []string
	for _, w := range ws {
		v, why := r.word(src, w, depth+1)
		if why != "" {
			return "", why
		}
		args = append(args, v)
	}
	switch {
	case len(args) == 1 && args[0] == "FindOsvvmScriptSettingsPkg":
		return "OsvvmScriptSettingsPkg_default.vhd", ""
	case len(args) == 2 && args[0] == "FindSettingsPkgBody":
		return args[1] + "_default.vhd", ""
	case len(args) == 4 && args[0] == "string" && args[1] == "compare":
		return strconv.Itoa(strings.Compare(args[2], args[3])), ""
	}
	return "", unknown
}

package parser

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/token"
)

// This file reads GHDL's own expectations (testdata/corpus/corpus.yaml,
// "Expected-failure extraction", GHDL): which testsuite files GHDL
// expects to fail analysis, and which it analyses and requires to
// succeed. The testsuite.sh scripts are read by a small model of the
// POSIX shell subset they use (words, quotes, variables, for loops,
// globs, redirections, here-documents); nothing is executed.

// ghdlStd maps a GHDL --std value to a GoHard standard. GHDL's 87 is
// read as 1993 (GoHard has no 1987 mode; see ghdlRun.v87) and 00 as
// 2002 (no separate 2000 mode, #31).
func ghdlStd(v string) (token.Standard, bool) {
	switch v {
	case "87", "93", "93c":
		return token.Std1993, true
	case "00", "02":
		return token.Std2002, true
	case "08":
		return token.Std2008, true
	case "19":
		return token.Std2019, true
	}
	return 0, false
}

// ghdlRun is how GHDL analyses one file in one command.
type ghdlRun struct {
	std token.Standard
	psl bool // -fpsl: `-- psl` comments are PSL
	// v87 is --std=87: GoHard has no VHDL-1987 mode (1987 code may
	// use 1993 reserved words as identifiers), so the run states no
	// expectation GoHard can check.
	v87 bool
	// relaxed is GHDL's default --std=93c, VHDL-1993 with relaxed
	// rules: GHDL accepts some later constructs there (a selected
	// signal assignment in a process, synth/case01/case07), so
	// syntax/standard does not fail such a run; syntax/error does.
	relaxed bool
	// flags are the other recorded options (-frelaxed, ...); they do
	// not change the expectation.
	flags []string
}

// shWord is one shell word: a list of parts, each quoted or not.
type shWord []shPart

type shPart struct {
	text   string
	quoted byte // 0, '\'' or '"'
}

// shCmd is one simple command, or a for loop (forVar set).
type shCmd struct {
	line  int
	words []shWord
	cond  bool // after if, elif, while, until or !
	// inBlock: inside an if or while block, so it may not run.
	inBlock bool
	forVar  string
	forIn   []shWord
	body    []shCmd
}

// shLexer splits a script into commands.
type shLexer struct {
	src      []byte
	i, line  int
	heredocs []heredoc
}

type heredoc struct {
	delim string
	strip bool // <<-: leading tabs are stripped
}

const shOps = ";&|()<>\n"

// commands returns the commands of src as flat word lists, one per
// simple command, with their 1-based line.
func (l *shLexer) commands() []shCmd {
	var (
		out   []shCmd
		cur   shCmd
		redir bool
	)
	cur.line = 1
	l.line = 1
	flush := func() {
		if len(cur.words) > 0 {
			out = append(out, cur)
		}
		cur = shCmd{line: l.line}
	}
	for l.i < len(l.src) {
		c := l.src[l.i]
		switch {
		case c == '\\' && l.i+1 < len(l.src) && l.src[l.i+1] == '\n':
			l.i += 2
			l.line++
		case c == ' ' || c == '\t' || c == '\r':
			l.i++
		case c == '#':
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.i++
			}
		case c == '\n':
			l.i++
			l.line++
			flush()
			l.skipHeredocs()
		case c == '<' || c == '>':
			l.i++
			if c == '<' && l.i < len(l.src) && l.src[l.i] == '<' {
				l.i++
				strip := l.i < len(l.src) && l.src[l.i] == '-'
				if strip {
					l.i++
				}
				l.skipBlanks()
				d := l.word()
				l.heredocs = append(l.heredocs,
					heredoc{delim: d.literal(), strip: strip})
				continue
			}
			for l.i < len(l.src) && (l.src[l.i] == '>' ||
				l.src[l.i] == '&' || l.src[l.i] == '|') {
				l.i++
			}
			redir = true // the next word is the target
		case strings.IndexByte(shOps, c) >= 0:
			l.i++
			flush()
		default:
			w := l.word()
			if redir {
				redir = false
				continue
			}
			// "2>" and "2>&1": a word of digits right before a
			// redirection is its file descriptor.
			if l.i < len(l.src) && (l.src[l.i] == '>' ||
				l.src[l.i] == '<') && isDigits(w.literal()) &&
				len(w) == 1 && w[0].quoted == 0 {
				continue
			}
			cur.words = append(cur.words, w)
		}
	}
	flush()
	return out
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

func (l *shLexer) skipBlanks() {
	for l.i < len(l.src) && (l.src[l.i] == ' ' || l.src[l.i] == '\t') {
		l.i++
	}
}

// skipHeredocs skips the bodies of the here-documents opened on the
// line just ended.
func (l *shLexer) skipHeredocs() {
	for _, h := range l.heredocs {
		for l.i < len(l.src) {
			end := bytes.IndexByte(l.src[l.i:], '\n')
			var ln []byte
			if end < 0 {
				ln, l.i = l.src[l.i:], len(l.src)
			} else {
				ln, l.i = l.src[l.i:l.i+end], l.i+end+1
				l.line++
			}
			s := strings.TrimRight(string(ln), "\r")
			if h.strip {
				s = strings.TrimLeft(s, "\t")
			}
			if s == h.delim {
				break
			}
		}
	}
	l.heredocs = l.heredocs[:0]
}

// word reads one word at l.i.
func (l *shLexer) word() shWord {
	var w shWord
	var lit strings.Builder
	push := func() {
		if lit.Len() > 0 {
			w = append(w, shPart{text: lit.String()})
			lit.Reset()
		}
	}
	for l.i < len(l.src) {
		c := l.src[l.i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' ||
			strings.IndexByte(shOps, c) >= 0:
			push()
			return w
		case c == '\\' && l.i+1 < len(l.src):
			if l.src[l.i+1] == '\n' {
				l.line++
			} else {
				lit.WriteByte(l.src[l.i+1])
			}
			l.i += 2
		case c == '\'' || c == '"':
			push()
			w = append(w, shPart{text: l.quoted(c), quoted: c})
		case c == '$' && l.i+1 < len(l.src) && l.src[l.i+1] == '(':
			// Command substitution: kept as an opaque $() part.
			l.i++ // the '$'; the loop ends after the matching ')'
			depth := 0
			for l.i < len(l.src) {
				switch l.src[l.i] {
				case '(':
					depth++
				case ')':
					depth--
				case '\n':
					l.line++
				}
				l.i++
				if depth == 0 {
					break
				}
			}
			lit.WriteString("$()")
		case c == '`':
			l.i++
			for l.i < len(l.src) && l.src[l.i] != '`' {
				if l.src[l.i] == '\n' {
					l.line++
				}
				l.i++
			}
			l.i++
			lit.WriteString("$()")
		default:
			lit.WriteByte(c)
			l.i++
		}
	}
	push()
	return w
}

// quoted reads a quoted string opened by q at l.i.
func (l *shLexer) quoted(q byte) string {
	l.i++
	var b strings.Builder
	for l.i < len(l.src) && l.src[l.i] != q {
		c := l.src[l.i]
		if c == '\n' {
			l.line++
		}
		if q == '"' && c == '\\' && l.i+1 < len(l.src) &&
			strings.IndexByte("\"\\$`\n", l.src[l.i+1]) >= 0 {
			if l.src[l.i+1] != '\n' {
				b.WriteByte(l.src[l.i+1])
			} else {
				l.line++
			}
			l.i += 2
			continue
		}
		b.WriteByte(c)
		l.i++
	}
	l.i++
	return b.String()
}

// literal is the word's text without expansion.
func (w shWord) literal() string {
	var b strings.Builder
	for _, p := range w {
		b.WriteString(p.text)
	}
	return b.String()
}

var shVar = regexp.MustCompile(`\$(\{[A-Za-z_][A-Za-z0-9_]*\}|` +
	`[A-Za-z_][A-Za-z0-9_]*|[0-9@*#?$!])`)

func expandVars(s string, vars map[string]string) string {
	return shVar.ReplaceAllStringFunc(s, func(m string) string {
		name := strings.Trim(m[1:], "{}")
		return vars[name]
	})
}

// expand performs variable expansion, field splitting of unquoted
// expansions, and pathname expansion of unquoted globs relative to dir.
func (w shWord) expand(vars map[string]string, dir string) []string {
	var (
		b     strings.Builder
		split bool
		glob  bool
	)
	for _, p := range w {
		switch p.quoted {
		case '\'':
			b.WriteString(p.text)
		case '"':
			b.WriteString(expandVars(p.text, vars))
		default:
			if strings.Contains(p.text, "$") {
				split = true
			}
			if strings.ContainsAny(p.text, "*?[") {
				glob = true
			}
			b.WriteString(expandVars(p.text, vars))
		}
	}
	s := b.String()
	if !split && !glob {
		return []string{s}
	}
	fields := []string{s}
	if split {
		fields = strings.Fields(s)
	}
	if !glob {
		return fields
	}
	var out []string
	for _, f := range fields {
		if !strings.ContainsAny(f, "*?[") {
			out = append(out, f)
			continue
		}
		m, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(f)))
		if len(m) == 0 {
			out = append(out, f) // a glob without match stays as is
			continue
		}
		slices.Sort(m)
		for _, x := range m {
			rel, _ := filepath.Rel(dir, x)
			out = append(out, filepath.ToSlash(rel))
		}
	}
	return out
}

// parseScript groups the commands of a script into for loops and marks
// condition commands.
func parseScript(src []byte) []shCmd {
	l := &shLexer{src: src}
	flat := l.commands()
	i := 0
	return groupCmds(flat, &i, false, false)
}

// groupCmds reads commands from flat[*i] up to the `done` of a for
// loop (when inLoop) or the end. Commands inside if and while blocks
// are marked inBlock (they may not run).
func groupCmds(flat []shCmd, i *int, inLoop, inBlock bool) []shCmd {
	var (
		out []shCmd
		// open are the if/while blocks opened at this level.
		open []string
	)
	for *i < len(flat) {
		c := flat[*i]
		*i++
		c.inBlock = inBlock || len(open) > 0
		// Leading reserved words: then, else, do, { and } introduce
		// a command; if, elif, while, until and ! make it a condition.
		for len(c.words) > 0 {
			switch w := c.words[0].literal(); w {
			case "then", "else", "do", "{", "}":
				c.words = c.words[1:]
				continue
			case "if", "while", "until":
				open = append(open, w)
				c.words = c.words[1:]
				c.cond, c.inBlock = true, true
				continue
			case "elif", "!":
				c.words = c.words[1:]
				c.cond = true
				continue
			}
			break
		}
		if len(c.words) == 0 {
			continue
		}
		switch c.words[0].literal() {
		case "done":
			if n := len(open); n > 0 && open[n-1] != "if" {
				open = open[:n-1] // end of a while/until loop
				continue
			}
			if inLoop {
				return out
			}
			continue
		case "fi":
			if n := len(open); n > 0 && open[n-1] == "if" {
				open = open[:n-1]
			}
			continue
		case "esac":
			continue
		case "for":
			if len(c.words) >= 3 && c.words[2].literal() == "in" {
				c.forVar = c.words[1].literal()
				c.forIn = c.words[3:]
				c.words = nil
				c.body = groupCmds(flat, i, true, c.inBlock)
				out = append(out, c)
			}
			continue
		}
		out = append(out, c)
	}
	return out
}

// ghdlRef is one file a script names with an expectation.
type ghdlRef struct {
	file string // slash path relative to the testsuite folder
	fail bool   // expected to fail analysis
	run  ghdlRun
	why  string // script:line command
}

// ghdlMust are the commands whose file operands must analyse
// (corpus.yaml): analyze, elab_simulate, synth* (not synth_failure).
var ghdlMust = map[string]bool{
	"analyze": true, "elab_simulate": true, "synth": true,
}

// ghdlHelpers are testenv.sh helpers that take a unit name and analyse
// files derived from it: synth_only/synth_analyze NAME analyse
// NAME.vhdl, synth_tb NAME analyses NAME.vhdl and tb_NAME.vhdl,
// synth_vlg_tb NAME analyses tb_NAME.vhdl (the rest is generated).
var ghdlHelpers = map[string][]string{
	"synth_only":    {"%s.vhdl"},
	"synth_analyze": {"%s.vhdl"},
	"synth_tb":      {"%s.vhdl", "tb_%s.vhdl"},
	"synth_vlg_tb":  {"tb_%s.vhdl"},
}

// readGHDLScript returns the references of one testsuite.sh (a slash
// path relative to the testsuite folder root, on disk). The model
// follows assignments (with or without export), for loops, `cd DIR`
// and a top-level `exit`, which ends the script (disabled tests start
// with `exit 0`).
func readGHDLScript(root, script string) ([]ghdlRef, error) {
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(script)))
	if err != nil {
		return nil, err
	}
	return scriptRefs(root, script, src), nil
}

// scriptRefs is readGHDLScript on the script text src.
func scriptRefs(root, script string, src []byte) []ghdlRef {
	dir := path.Dir(script)
	vars := map[string]string{}
	var refs []ghdlRef
	stopped := false
	var run func(cmds []shCmd)
	run = func(cmds []shCmd) {
		for _, c := range cmds {
			if stopped {
				return
			}
			osDir := filepath.Join(root, filepath.FromSlash(dir))
			if c.forVar != "" {
				var vals []string
				for _, w := range c.forIn {
					vals = append(vals, w.expand(vars, osDir)...)
				}
				old, had := vars[c.forVar]
				for _, v := range vals {
					vars[c.forVar] = v
					run(c.body)
				}
				if had {
					vars[c.forVar] = old
				} else {
					delete(vars, c.forVar)
				}
				continue
			}
			switch c.words[0].literal() {
			case "exit":
				if !c.inBlock && !c.cond {
					stopped = true
				}
				continue
			case "cd":
				if len(c.words) == 2 && !c.cond {
					to := c.words[1].literal()
					if !strings.Contains(to, "$") &&
						!path.IsAbs(to) {
						dir = path.Join(dir, to)
					}
				}
				continue
			}
			refs = append(refs, ghdlCommand(c, vars, osDir, dir,
				script)...)
		}
	}
	run(parseScript(src))
	return refs
}

var shAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// ghdlCommand applies one simple command: assignments update vars;
// analysis helpers give references.
func ghdlCommand(c shCmd, vars map[string]string, osDir, dir,
	script string,
) []ghdlRef {
	words := c.words
	if len(words) > 0 && words[0].literal() == "export" {
		words = words[1:]
		for _, w := range words {
			if lit := w.literal(); shAssign.MatchString(lit) {
				name, _, _ := strings.Cut(lit, "=")
				vars[name] = assignValue(w, vars, osDir)
			}
		}
		return nil
	}
	// Leading NAME=VALUE words: alone they set shell variables; before
	// a command they apply to that command only.
	local := vars
	n := 0
	for n < len(words) && shAssign.MatchString(words[n].literal()) {
		n++
	}
	if n > 0 {
		if n < len(words) {
			local = maps(vars)
		}
		for _, w := range words[:n] {
			name, _, _ := strings.Cut(w.literal(), "=")
			local[name] = assignValue(w, vars, osDir)
		}
		words = words[n:]
	}
	if len(words) == 0 {
		return nil
	}
	name := words[0].literal()
	fail := name == "analyze_failure"
	pattern, helper := ghdlHelpers[name]
	if !fail && !ghdlMust[name] && !helper {
		return nil
	}
	if c.cond {
		// `if analyze_failure ...` re-checks an already listed file;
		// `if analyze ...; then echo "error expected"` and `! cmd`
		// expect the command (often its options) to fail: neither
		// states an analysis expectation for the file.
		return nil
	}
	var args []string
	for _, w := range words[1:] {
		args = append(args, w.expand(local, osDir)...)
	}
	r := ghdlRun{std: token.Std1993, relaxed: true}
	// testenv.sh passes $GHDL_STD_FLAGS $GHDL_FLAGS (and, for synth,
	// $GHDL_SYNTH_FLAGS) before the arguments.
	opts := strings.Fields(local["GHDL_STD_FLAGS"] + " " +
		local["GHDL_FLAGS"])
	if strings.HasPrefix(name, "synth") {
		opts = append(opts, strings.Fields(local["GHDL_SYNTH_FLAGS"])...)
	}
	var files []string
	if helper && len(args) > 0 {
		for _, p := range pattern {
			files = append(files, fmt.Sprintf(p, args[0]))
		}
		args = args[1:]
	}
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "-"):
			opts = append(opts, a)
		case isVHDLName(a):
			files = append(files, a)
		}
	}
	for _, o := range opts {
		if v, ok := strings.CutPrefix(o, "--std="); ok {
			if s, ok := ghdlStd(v); ok {
				r.std, r.v87, r.relaxed = s, v == "87", v == "93c"
			}
			continue
		}
		if o == "-fpsl" {
			r.psl = true
			continue
		}
		r.flags = append(r.flags, o)
	}
	why := fmt.Sprintf("%s:%d %s", script, c.line, name)
	var refs []ghdlRef
	for _, f := range files {
		refs = append(refs, ghdlRef{file: path.Clean(path.Join(dir, f)),
			fail: fail, run: r, why: why})
	}
	return refs
}

func maps(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// assignValue expands the value of a NAME=VALUE word (no field
// splitting or globbing, as in the shell).
func assignValue(w shWord, vars map[string]string, dir string) string {
	var b strings.Builder
	for _, p := range w {
		if p.quoted == '\'' {
			b.WriteString(p.text)
		} else {
			b.WriteString(expandVars(p.text, vars))
		}
	}
	_, v, _ := strings.Cut(b.String(), "=")
	return v
}

func isVHDLName(s string) bool {
	e := strings.ToLower(path.Ext(s))
	return e == ".vhd" || e == ".vhdl"
}

// vestsDriver is GHDL's testsuite/vests/testsuite.sh: it defines the
// VESTS helper functions (each a handle_test mode), sets common_args
// (--std) and sources the .exp lists, each with dir= its directory.
// readVESTS returns every file the sourced lists name: mode ana_err
// expects an analysis failure, compile, run and run_err require
// analysis to succeed.
func readVESTS(root string) ([]ghdlRef, error) {
	const driver = "vests/testsuite.sh"
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(driver)))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	modeOf := map[string]string{}
	defRe := regexp.MustCompile(`^\s*([a-z_]+)\s*\(\)\s*\{\s*` +
		`handle_test\s+([a-z_]+)\b`)
	stdRe := regexp.MustCompile(`^\s*common_args="[^"]*--std=(\w+)`)
	dirRe := regexp.MustCompile(`^\s*dir=(\S+)\s*$`)
	srcRe := regexp.MustCompile(`^\s*\.\s+\$dir/(\S+\.exp)\s*$`)
	std, dir := token.Std1993, ""
	var refs []ghdlRef
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		s := sc.Text()
		if m := defRe.FindStringSubmatch(s); m != nil {
			modeOf[m[1]] = m[2]
		} else if m := stdRe.FindStringSubmatch(s); m != nil {
			if v, ok := ghdlStd(m[1]); ok {
				std = v
			}
		} else if m := dirRe.FindStringSubmatch(s); m != nil {
			dir = path.Join("vests", m[1])
		} else if m := srcRe.FindStringSubmatch(s); m != nil {
			exp := path.Join(dir, m[1])
			got, err := readExp(root, exp, std, func(fn string) string {
				return modeOf[fn]
			})
			if err != nil {
				return nil, err
			}
			refs = append(refs, got...)
		}
	}
	return refs, sc.Err()
}

// readExp reads one VESTS .exp list: lines `<helper> <file> [opts]`,
// files relative to the list's directory. mode maps a helper to its
// handle_test mode ("" for other lines).
func readExp(root, exp string, std token.Standard,
	mode func(string) string,
) ([]ghdlRef, error) {
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(exp)))
	if err != nil {
		return nil, err
	}
	var refs []ghdlRef
	for i, s := range strings.Split(string(src), "\n") {
		w := strings.Fields(s)
		if len(w) < 2 || strings.HasPrefix(w[0], "#") {
			continue
		}
		m := mode(w[0])
		if m == "" || !isVHDLName(w[1]) {
			continue
		}
		refs = append(refs, ghdlRef{
			file: path.Join(path.Dir(exp), w[1]),
			fail: m == "ana_err",
			run:  ghdlRun{std: std},
			why:  fmt.Sprintf("%s:%d %s (%s)", exp, i+1, w[0], m),
		})
	}
	return refs, nil
}

// exactExpMode is the reading of a .exp file listed under
// expected_failures: run_non_compliant_test lines are analysis
// failures (corpus.yaml).
func exactExpMode(fn string) string {
	if fn == "run_non_compliant_test" {
		return "ana_err"
	}
	return ""
}

func sortRefs(refs []ghdlRef) {
	slices.SortStableFunc(refs, func(a, b ghdlRef) int {
		return cmp.Compare(a.file, b.file)
	})
}

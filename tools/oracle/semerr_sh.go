package main

import (
	"bytes"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A small model of the POSIX shell subset GHDL's testsuite.sh scripts
// use (words, quotes, variables, for loops, globs, redirections,
// here-documents); nothing is executed. It is the model of the parser
// regression runner (internal/parser/regress_ghdl_test.go, test code
// that cannot be imported), copied unchanged; semerr_plan.go
// interprets the commands in analysis order.

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

var shAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

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

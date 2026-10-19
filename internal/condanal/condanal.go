// Package condanal is VHDL-2019 conditional analysis (#31, #18): a pass
// over the token stream, before parsing, that evaluates the `if, `elsif,
// `else and `end [if] directives and the `warning and `error directives.
//
// The directives stay in the token stream as Directive trivia, so the
// tree is lossless. The result lists the inactive branches as byte
// regions; the lexer flags every token that starts in one, and the
// parser builds each inactive branch into a real subtree marked inactive
// (#10). Under a standard before 2019 every conditional directive gives
// syntax/standard, and is still evaluated so the tree shape never
// depends on the standard.
package condanal

import (
	"bytes"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/lexer"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Define is one user identifier (from defines: or --define, already
// merged so that --define wins, #28).
type Define struct {
	Name  string
	Value string
}

// Options configure Analyze.
type Options struct {
	// Lexer gives the standard and the PSL settings used to find the
	// directives; its Inactive field is ignored.
	Lexer lexer.Options
	// Defines are the user identifiers, compared case-insensitively.
	Defines []Define
	// ToolVersion is the value of TOOL_VERSION (the gohard release).
	ToolVersion string
}

// Builtin identifiers and their fixed values (#31; they match std.env).
const (
	ToolType    = "SIMULATION"
	ToolVendor  = "gohard"
	ToolName    = "gohard"
	ToolEdition = "community"
)

// Builtins returns the built-in identifiers and their values under std,
// sorted by name.
func Builtins(std token.Standard, toolVersion string) []Define {
	return []Define{
		{"TOOL_EDITION", ToolEdition},
		{"TOOL_NAME", ToolName},
		{"TOOL_TYPE", ToolType},
		{"TOOL_VENDOR", ToolVendor},
		{"TOOL_VERSION", toolVersion},
		{"VHDL_VERSION", std.String()},
	}
}

// Construct is the rules.csv construct text of the conditional-analysis
// gate (rule condanal, #7).
const Construct = "`if/`elsif/`else/`end/`error/`warning"

// DirectiveKind classifies a directive.
type DirectiveKind uint8

// Directive kinds.
const (
	Other DirectiveKind = iota // `protect and unknown tool directives
	If
	Elsif
	Else
	End
	Warning
	Error
)

var directiveNames = map[string]DirectiveKind{
	"if": If, "elsif": Elsif, "else": Else, "end": End,
	"warning": Warning, "error": Error,
}

// Directive is one tool directive line.
type Directive struct {
	Start, End int
	Kind       DirectiveKind
	// Active reports whether the directive itself is in active code.
	Active bool
	// Taken reports, for `if, `elsif and `else, whether the branch it
	// opens is the active one.
	Taken bool
}

// Result is the outcome of Analyze.
type Result struct {
	// Inactive lists the inactive branches in order, disjoint. A branch
	// runs from the end of its directive line to the start of the next
	// directive of its `if.
	Inactive []lexer.Region
	// Directives lists every tool directive in order.
	Directives []Directive
	// Problems are the directive diagnostics in source order.
	Problems []lexer.Problem
}

type frame struct {
	at           int // offset of the `if directive
	parentActive bool
	active       bool // the current branch is active
	taken        bool // some branch has been taken
	sawElse      bool
	branch       int // start of the current branch
}

// Analyze runs conditional analysis over src.
func Analyze(src []byte, opts Options) Result {
	var res Result
	if bytes.IndexByte(src, '`') < 0 {
		return res
	}
	lo := opts.Lexer
	lo.Inactive = nil
	if lo.Standard == 0 {
		lo.Standard = token.Std2008
	}
	a := &analyzer{src: src, opts: opts, std: lo.Standard, res: &res}
	lx := lexer.New(src, lo)
	st := lexer.Initial(lexer.VHDL)
	for off := 0; ; {
		t, next := lx.Lex(off, st)
		for _, tr := range lx.Leading(t) {
			if tr.Kind == token.Directive {
				a.directive(tr.Start, tr.End)
			}
		}
		lx.Reset()
		if t.Kind == token.EOF {
			break
		}
		off, st = t.FullEnd, next
	}
	for i := len(a.stack) - 1; i >= 0; i-- {
		f := a.stack[i]
		a.problem(f.at, f.at+3, "`if has no matching `end if")
		a.close(&a.stack[i], len(src))
	}
	return res
}

type analyzer struct {
	src   []byte
	opts  Options
	std   token.Standard
	res   *Result
	stack []frame
}

func (a *analyzer) active() bool {
	n := len(a.stack)
	return n == 0 || a.stack[n-1].active
}

func (a *analyzer) problem(start, end int, msg string) {
	end = min(end, len(a.src))
	start = min(start, end)
	a.res.Problems = append(a.res.Problems, lexer.Problem{
		Start: start, End: end, Code: diag.SyntaxError, Args: []string{msg},
	})
}

// close ends the current branch of f at end, recording it if inactive.
func (a *analyzer) close(f *frame, end int) {
	if f.parentActive && !f.active && end > f.branch {
		a.res.Inactive = append(a.res.Inactive,
			lexer.Region{Start: f.branch, End: end})
	}
}

func (a *analyzer) directive(start, end int) {
	sc := &scanner{src: a.src, i: start + 1, end: end}
	name := strings.ToLower(sc.word())
	kind := directiveNames[name]
	d := Directive{Start: start, End: end, Kind: kind, Active: a.active()}
	if kind != Other && a.std < token.Std2019 {
		a.res.Problems = append(a.res.Problems, lexer.Problem{
			Start: start, End: end, Code: diag.SyntaxStandard,
			Args: []string{Construct, "2019",
				a.std.String()},
		})
	}
	switch kind {
	case If:
		parent := a.active()
		cond := a.condition(sc, parent)
		a.stack = append(a.stack, frame{
			at: start, parentActive: parent, active: parent && cond,
			taken: parent && cond, branch: end,
		})
		d.Taken = parent && cond
	case Elsif, Else:
		n := len(a.stack)
		if n == 0 {
			a.problem(start, end, "`"+name+" without `if")
			a.res.Directives = append(a.res.Directives, d)
			return
		}
		f := &a.stack[n-1]
		d.Active = f.parentActive
		a.close(f, start)
		if f.sawElse {
			a.problem(start, end, "`"+name+" after `else")
		}
		evaluate := f.parentActive && !f.taken
		cond := true
		if kind == Elsif {
			cond = a.condition(sc, evaluate)
		} else {
			f.sawElse = true
			a.rest(sc)
		}
		f.active = evaluate && cond
		f.taken = f.taken || f.active
		f.branch = end
		d.Taken = f.active
	case End:
		n := len(a.stack)
		if n == 0 {
			a.problem(start, end, "`end without `if")
			a.res.Directives = append(a.res.Directives, d)
			return
		}
		f := &a.stack[n-1]
		d.Active = f.parentActive
		a.close(f, start)
		a.stack = a.stack[:n-1]
		if w := sc.peek(); strings.EqualFold(w.text, "if") {
			sc.next()
		}
		a.rest(sc)
	case Warning, Error:
		msg, ok := a.message(sc)
		if ok && d.Active {
			code := diag.SyntaxDirectiveWarning
			if kind == Error {
				code = diag.SyntaxDirectiveError
			}
			a.res.Problems = append(a.res.Problems, lexer.Problem{
				Start: start, End: end, Code: code, Args: []string{msg},
			})
		}
	}
	a.res.Directives = append(a.res.Directives, d)
}

// rest reports trailing junk after a complete directive.
func (a *analyzer) rest(sc *scanner) {
	if t := sc.peek(); t.kind != tEnd && a.active() {
		a.problem(t.start, sc.end, "unexpected text after directive")
	}
}

func (a *analyzer) message(sc *scanner) (string, bool) {
	t := sc.next()
	if t.kind != tString {
		if a.active() {
			a.problem(t.start, max(t.end, t.start+1),
				"expected a string literal")
		}
		return "", false
	}
	a.rest(sc)
	return t.text, true
}

// condition parses `cond then` and returns its value. With report false
// (an inactive or already-taken branch) it reports nothing.
func (a *analyzer) condition(sc *scanner, report bool) bool {
	p := &condParser{a: a, sc: sc, report: report}
	v := p.expr()
	t := sc.next()
	if !strings.EqualFold(t.text, "then") || t.kind != tWord {
		p.fail(t, "expected then")
		return false
	}
	if t := sc.peek(); t.kind != tEnd {
		p.fail(t, "unexpected text after then")
	}
	return v && !p.failed
}

type condParser struct {
	a      *analyzer
	sc     *scanner
	report bool
	failed bool
}

func (p *condParser) fail(t tok, msg string) {
	if p.report && !p.failed {
		p.a.problem(t.start, max(t.end, t.start+1), msg)
	}
	p.failed = true
}

var logical = []string{"and", "or", "xor", "xnor"}

// expr := relation { logical_op relation }
func (p *condParser) expr() bool {
	v := p.relation()
	for {
		t := p.sc.peek()
		op := strings.ToLower(t.text)
		if t.kind != tWord || !slices.Contains(logical, op) {
			return v
		}
		p.sc.next()
		w := p.relation()
		switch op {
		case "and":
			v = v && w
		case "or":
			v = v || w
		case "xor":
			v = v != w
		case "xnor":
			v = v == w
		}
	}
}

// relation := not primary | ( expr ) | identifier relop string
func (p *condParser) relation() bool {
	t := p.sc.next()
	switch {
	case t.kind == tWord && strings.EqualFold(t.text, "not"):
		return !p.relation()
	case t.kind == tLParen:
		v := p.expr()
		if c := p.sc.next(); c.kind != tRParen {
			p.fail(c, "expected )")
		}
		return v
	case t.kind == tWord && !isReserved(t.text):
		op := p.sc.next()
		if op.kind != tOp {
			p.fail(op, "expected =, /=, <, <=, > or >=")
			return false
		}
		s := p.sc.next()
		if s.kind != tString {
			p.fail(s, "expected a string literal")
			return false
		}
		val, ok := p.a.value(t.text)
		if !ok {
			if p.report {
				p.a.res.Problems = append(p.a.res.Problems,
					lexer.Problem{Start: t.start, End: t.end,
						Code: diag.SyntaxUndefinedCondition,
						Args: []string{t.text}})
			}
			return false
		}
		return compare(val, op.text, s.text)
	}
	p.fail(t, "expected an identifier, ( or not")
	return false
}

func isReserved(w string) bool {
	switch strings.ToLower(w) {
	case "and", "or", "xor", "xnor", "not", "then":
		return true
	}
	return false
}

func compare(a, op, b string) bool {
	switch op {
	case "=":
		return a == b
	case "/=":
		return a != b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case ">":
		return a > b
	case ">=":
		return a >= b
	}
	return false
}

// value returns the value of a built-in or user identifier.
func (a *analyzer) value(name string) (string, bool) {
	for _, b := range Builtins(a.std, a.opts.ToolVersion) {
		if strings.EqualFold(b.Name, name) {
			return b.Value, true
		}
	}
	for _, d := range a.opts.Defines {
		if strings.EqualFold(d.Name, name) {
			return d.Value, true
		}
	}
	return "", false
}

// Directive-line scanner.

type tokKind uint8

const (
	tEnd tokKind = iota
	tWord
	tString
	tOp
	tLParen
	tRParen
	tBad
)

type tok struct {
	kind       tokKind
	text       string // word, operator, or the string's value
	start, end int
}

type scanner struct {
	src    []byte
	i, end int
	ahead  *tok
}

func (s *scanner) skip() {
	for s.i < s.end && (s.src[s.i] == ' ' || s.src[s.i] == '\t' ||
		s.src[s.i] == '\v' || s.src[s.i] == '\f') {
		s.i++
	}
}

// word scans the directive name right after the backtick.
func (s *scanner) word() string {
	j := s.i
	for j < s.end && isWordByte(s.src[j]) {
		j++
	}
	w := string(s.src[s.i:j])
	s.i = j
	return w
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '_'
}

func (s *scanner) peek() tok {
	if s.ahead == nil {
		t := s.scan()
		s.ahead = &t
	}
	return *s.ahead
}

func (s *scanner) next() tok {
	t := s.peek()
	s.ahead = nil
	return t
}

func (s *scanner) scan() tok {
	s.skip()
	start := s.i
	if s.i >= s.end {
		return tok{kind: tEnd, start: s.end, end: s.end}
	}
	c := s.src[s.i]
	switch {
	case c == '-' && s.i+1 < s.end && s.src[s.i+1] == '-':
		s.i = s.end // a comment ends the directive
		return tok{kind: tEnd, start: start, end: start}
	case isWordByte(c):
		w := s.word()
		return tok{kind: tWord, text: w, start: start, end: s.i}
	case c == '"':
		var b []byte
		j := s.i + 1
		for j < s.end {
			if s.src[j] == '"' {
				if j+1 < s.end && s.src[j+1] == '"' {
					b = append(b, '"')
					j += 2
					continue
				}
				s.i = j + 1
				return tok{kind: tString, text: string(b), start: start,
					end: s.i}
			}
			b = append(b, s.src[j])
			j++
		}
		s.i = s.end
		return tok{kind: tBad, start: start, end: s.end}
	case c == '(':
		s.i++
		return tok{kind: tLParen, text: "(", start: start, end: s.i}
	case c == ')':
		s.i++
		return tok{kind: tRParen, text: ")", start: start, end: s.i}
	}
	for _, op := range []string{"/=", "<=", ">=", "=", "<", ">"} {
		if bytes.HasPrefix(s.src[s.i:s.end], []byte(op)) {
			s.i += len(op)
			return tok{kind: tOp, text: op, start: start, end: s.i}
		}
	}
	s.i++
	return tok{kind: tBad, text: string(c), start: start, end: s.i}
}

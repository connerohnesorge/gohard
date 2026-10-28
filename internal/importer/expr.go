package importer

import (
	"strconv"
	"strings"
)

// The evaluator of .pro if conditions: the part of Tcl's expr that
// OSVVM and PoC scripts use. Operators, by Tcl precedence from the
// lowest: || && (in ni) (eq ne) (== !=) (< <= > >=) and the unary
// ! - +; operands are numbers, the boolean words, "quoted" and
// {braced} strings, $variables of the profile, [commands] that call
// knows and (parenthesized) expressions. A comparison is numeric when
// both sides are numbers, else it compares strings, as in Tcl. An
// operand that cannot be evaluated makes the value unknown; && and ||
// still give a known result when the other side decides it.

// notExpr is the reason for a condition the evaluator cannot read.
const notExpr = "the condition is not an expression gohard import " +
	"reads"

// condition evaluates the if condition w, a word of src.
func (r *proRun) condition(src []byte, w word, depth int) (bool,
	string,
) {
	text := w.Text
	if src[w.Off] == '{' {
		text = string(src[w.Off+1 : w.End-1])
	} else if w.Subst {
		v, why := r.word(src, w, depth)
		if why != "" {
			return false, why
		}
		text = v
	}
	e := &exprParser{r: r, s: text, depth: depth}
	v := e.or()
	e.space()
	if v.why == "" && (e.bad || e.pos < len(e.s)) {
		v.why = notExpr
	}
	if v.why != "" {
		return false, v.why
	}
	b, ok := tclBool(v.s)
	if !ok {
		return false, notExpr
	}
	return b, ""
}

// tval is a value of an expression; why, when set, is the reason it is
// unknown.
type tval struct {
	s   string
	why string
}

func boolVal(b bool) tval {
	if b {
		return tval{s: "1"}
	}
	return tval{s: "0"}
}

type exprParser struct {
	r     *proRun
	s     string
	pos   int
	depth int
	bad   bool // a syntax error: the value is notExpr
}

// space skips white space and backslash-newlines.
func (e *exprParser) space() {
	for e.pos < len(e.s) {
		switch c := e.s[e.pos]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' ||
			c == '\f' || c == '\v':
			e.pos++
		case c == '\\' && e.pos+1 < len(e.s) &&
			(e.s[e.pos+1] == '\n' || e.s[e.pos+1] == '\r'):
			e.pos += 2
		default:
			return
		}
	}
}

// op consumes the operator o when it comes next. A word operator (eq,
// ne, in, ni) must not run on into a longer word.
func (e *exprParser) op(o string) bool {
	e.space()
	if !strings.HasPrefix(e.s[e.pos:], o) {
		return false
	}
	end := e.pos + len(o)
	if isWordByte(o[0]) && end < len(e.s) && isWordByte(e.s[end]) {
		return false
	}
	// "<" must not take the first half of "<=", nor "!" of "!=".
	if (o == "<" || o == ">" || o == "!" || o == "=") &&
		end < len(e.s) && e.s[end] == '=' {
		return false
	}
	e.pos = end
	return true
}

func isWordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9'
}

// logic combines a and b with && (and) or || (or) in three-valued
// logic: an unknown side matters only when the other does not decide.
func logic(a, b tval, and bool) tval {
	ab, aok := tclBool(a.s)
	bb, bok := tclBool(b.s)
	aKnown := a.why == "" && aok
	bKnown := b.why == "" && bok
	switch {
	case aKnown && ab != and:
		return boolVal(ab)
	case bKnown && bb != and:
		return boolVal(bb)
	case aKnown && bKnown:
		return boolVal(and)
	case a.why != "":
		return a
	case b.why != "":
		return b
	}
	return tval{why: notExpr}
}

func (e *exprParser) or() tval {
	v := e.and()
	for e.op("||") {
		v = logic(v, e.and(), false)
	}
	return v
}

func (e *exprParser) and() tval {
	v := e.in()
	for e.op("&&") {
		v = logic(v, e.in(), true)
	}
	return v
}

func (e *exprParser) in() tval {
	v := e.streq()
	for {
		var neg bool
		switch {
		case e.op("in"):
		case e.op("ni"):
			neg = true
		default:
			return v
		}
		list := e.streq()
		if v.why != "" || list.why != "" {
			v = unknown(v, list)
			continue
		}
		items, ok := tclList(list.s)
		if !ok {
			v = tval{why: notExpr}
			continue
		}
		found := false
		for _, it := range items {
			found = found || it == v.s
		}
		v = boolVal(found != neg)
	}
}

// unknown returns the first unknown of a and b.
func unknown(a, b tval) tval {
	if a.why != "" {
		return a
	}
	return b
}

func (e *exprParser) streq() tval {
	v := e.eq()
	for {
		var neg bool
		switch {
		case e.op("eq"):
		case e.op("ne"):
			neg = true
		default:
			return v
		}
		w := e.eq()
		if v.why != "" || w.why != "" {
			v = unknown(v, w)
			continue
		}
		v = boolVal((v.s == w.s) != neg)
	}
}

func (e *exprParser) eq() tval {
	v := e.rel()
	for {
		var neg bool
		switch {
		case e.op("=="):
		case e.op("!="):
			neg = true
		default:
			return v
		}
		w := e.rel()
		if v.why != "" || w.why != "" {
			v = unknown(v, w)
			continue
		}
		v = boolVal((compare(v.s, w.s) == 0) != neg)
	}
}

func (e *exprParser) rel() tval {
	v := e.unary()
	for {
		var test func(int) bool
		switch {
		case e.op("<="):
			test = func(c int) bool { return c <= 0 }
		case e.op(">="):
			test = func(c int) bool { return c >= 0 }
		case e.op("<"):
			test = func(c int) bool { return c < 0 }
		case e.op(">"):
			test = func(c int) bool { return c > 0 }
		default:
			return v
		}
		w := e.unary()
		if v.why != "" || w.why != "" {
			v = unknown(v, w)
			continue
		}
		v = boolVal(test(compare(v.s, w.s)))
	}
}

// compare compares two operands as Tcl does: as numbers when both are
// numbers, else as strings.
func compare(a, b string) int {
	x, xok := tclNumber(a)
	y, yok := tclNumber(b)
	if !xok || !yok {
		return strings.Compare(a, b)
	}
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

func (e *exprParser) unary() tval {
	if e.depth++; e.depth > proMaxDepth {
		e.bad = true
		return tval{why: notExpr}
	}
	defer func() { e.depth-- }()
	switch {
	case e.op("!"):
		v := e.unary()
		if v.why != "" {
			return v
		}
		b, ok := tclBool(v.s)
		if !ok {
			return tval{why: notExpr}
		}
		return boolVal(!b)
	case e.op("-"), e.op("+"):
		neg := e.s[e.pos-1] == '-'
		v := e.unary()
		if v.why != "" {
			return v
		}
		x, ok := tclNumber(v.s)
		if !ok {
			return tval{why: notExpr}
		}
		if neg {
			x = -x
		}
		return tval{s: strconv.FormatFloat(x, 'g', -1, 64)}
	}
	return e.primary()
}

// primary reads an operand.
func (e *exprParser) primary() tval {
	e.space()
	if e.pos >= len(e.s) {
		e.bad = true
		return tval{why: notExpr}
	}
	switch c := e.s[e.pos]; {
	case c == '(':
		e.pos++
		v := e.or()
		if !e.op(")") {
			e.bad = true
		}
		return v
	case c == '$':
		name, n := varName(e.s[e.pos+1:])
		if n == 0 {
			e.bad = true
			return tval{why: notExpr}
		}
		e.pos += 1 + n
		v, why := e.r.variable(name, e.s[e.pos:])
		return tval{s: v, why: why}
	case c == '"':
		end := e.pos + 1
		for end < len(e.s) && e.s[end] != '"' {
			if e.s[end] == '\\' {
				end++
			}
			end++
		}
		if end >= len(e.s) {
			e.bad = true
			return tval{why: notExpr}
		}
		v, why := e.r.subst(e.s[e.pos+1:end], e.depth)
		e.pos = end + 1
		return tval{s: v, why: why}
	case c == '{':
		end := closeBrace(e.s, e.pos)
		if end < 0 {
			e.bad = true
			return tval{why: notExpr}
		}
		v := e.s[e.pos+1 : end]
		e.pos = end + 1
		return tval{s: v}
	case c == '[':
		end := closeBracket(e.s, e.pos)
		if end < 0 {
			e.bad = true
			return tval{why: notExpr}
		}
		v, why := e.r.call(e.s[e.pos+1:end], e.depth)
		e.pos = end + 1
		return tval{s: v, why: why}
	case c == '.' || c >= '0' && c <= '9':
		start := e.pos
		for e.pos < len(e.s) && (isWordByte(e.s[e.pos]) ||
			e.s[e.pos] == '.') {
			e.pos++
		}
		return tval{s: e.s[start:e.pos]}
	case isWordByte(c):
		start := e.pos
		for e.pos < len(e.s) && isWordByte(e.s[e.pos]) {
			e.pos++
		}
		w := e.s[start:e.pos]
		if _, ok := tclBool(w); ok {
			return tval{s: w}
		}
		// A function call or a bare word Tcl refuses.
		e.bad = true
		return tval{why: notExpr}
	}
	e.bad = true
	return tval{why: notExpr}
}

// closeBrace returns the index of the '}' that closes the '{' at open
// in s, or -1.
func closeBrace(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// tclNumber reads s as a Tcl number: an integer (with a 0x, 0o or 0b
// prefix too) or a decimal real.
func tclNumber(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "_pP") {
		return 0, false
	}
	if i, err := strconv.ParseInt(s, 0, 64); err == nil {
		return float64(i), true
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "0x") || strings.Contains(lower, "inf") ||
		strings.Contains(lower, "nan") {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// tclBool reads s as a Tcl boolean: a number (true when not zero) or
// true, false, yes, no, on, off in any letter case.
func tclBool(s string) (bool, bool) {
	if x, ok := tclNumber(s); ok {
		return x != 0, true
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on":
		return true, true
	case "false", "no", "off":
		return false, true
	}
	return false, false
}

// tclList splits s into the elements of a Tcl list: words separated by
// white space, {braced} or "quoted" when they hold some. A list that
// holds ';', '$' or '[' is not read.
func tclList(s string) ([]string, bool) {
	if strings.ContainsAny(s, ";$[") {
		return nil, false
	}
	sc := &tclScanner{src: []byte(s)}
	var out []string
	for {
		for !sc.atEOF() {
			if n := sc.continuation(); n > 0 {
				sc.pos += n
				continue
			}
			if !isSpace(sc.peek()) && !isEnd(sc.peek()) {
				break
			}
			sc.pos++
		}
		if sc.atEOF() {
			return out, true
		}
		w, ok := sc.word()
		if !ok {
			return nil, false
		}
		out = append(out, w.Text)
	}
}

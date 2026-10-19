// Package lexer turns VHDL 1993-2019 source bytes into tokens with
// trivia (#31): identifiers, extended identifiers, abstract, character,
// string and bit-string literals, comments (including 2008 block
// comments), tool directives, and the PSL lexer modes with their
// PSL-only operator tokens.
//
// The lexer is lazy and pure: Lex(off, state) scans the one token at
// byte offset off, with its leading and trailing trivia, and returns it
// with the state for the next call. The state is a small value (the
// mode, the previous token kind for the tick rule, and the `-- psl`
// comment status), so the token stream is a pure function of (bytes,
// options, position, mode) and the parser can switch modes by lexing
// again from any token (#6). Lex never panics on any bytes, including
// invalid UTF-8, and every byte of the input ends up in exactly one
// token or trivia piece.
package lexer

import (
	"bytes"
	"sort"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Mode is a lexer mode chosen by the parser (#6). PSL_COMMENT is not a
// Mode: the lexer enters and leaves it by itself from the bytes (a
// `-- psl` comment line, when Options.PSLComments is set).
type Mode uint8

// Lexer modes.
const (
	// VHDL is plain VHDL: no PSL-only tokens, PSL words are identifiers.
	VHDL Mode = iota
	// PSL is embedded PSL: PSL keywords and PSL-only tokens.
	PSL
)

// String returns "VHDL" or "PSL".
func (m Mode) String() string {
	if m == PSL {
		return "PSL"
	}
	return "VHDL"
}

// Region is a half-open byte range of an inactive conditional-analysis
// branch (from package condanal).
type Region struct {
	Start int
	End   int
}

// Options configure a Lexer.
type Options struct {
	Standard token.Standard
	// PSLComments enables `-- psl` comment lines (psl.comments).
	PSLComments bool
	// StrictKeywords reserves the PSL-only 2008 words everywhere
	// (psl.strict_keywords).
	StrictKeywords bool
	// Inactive lists the inactive conditional-analysis branches, sorted
	// and disjoint. Tokens starting inside one are flagged Inactive.
	Inactive []Region
}

// State is the lexer state between tokens.
type State struct {
	Mode Mode
	prev token.Kind // previous token, for the tick rule and suffixes
	pslc bool       // inside a `-- psl` comment directive
	deep uint16     // bracket depth inside the comment directive
}

// Initial returns the state at the start of a file in mode m.
func Initial(m Mode) State { return State{Mode: m} }

// WithMode returns s with the parser mode m.
func (s State) WithMode(m Mode) State {
	s.Mode = m
	return s
}

// InPSLComment reports whether the next token is inside a `-- psl`
// comment directive.
func (s State) InPSLComment() bool { return s.pslc }

// Flags describe a token.
type Flags uint8

// Token flags.
const (
	// Inactive: the token starts inside an inactive branch.
	Inactive Flags = 1 << iota
	// PSLComment: the token was scanned from a `-- psl` comment line.
	PSLComment
)

// Trivia is one piece of trivia: its kind and byte range.
type Trivia struct {
	Kind  token.Kind
	Start int
	End   int
}

// Problem is a lexical diagnostic: a byte range, a code from the
// diagnostic table and the message arguments.
type Problem struct {
	Start int
	End   int
	Code  diag.Code
	Args  []string
}

// Token is one scanned token.
type Token struct {
	Kind token.Kind
	// Word is the keyword the token's text spells in any context (for
	// an Identifier, the keyword it would be under another standard or
	// mode), or 0.
	Word  token.Kind
	Flags Flags
	// Region is 1 + the index of the inactive region the token starts
	// in, or 0 for an active token.
	Region int
	// Start and End delimit the token's text.
	Start int
	End   int
	// FullStart and FullEnd include the leading and trailing trivia.
	FullStart int
	FullEnd   int

	triv0, trivN, trivE int32 // lead = [triv0,trivN), trail = [trivN,trivE)
	prob0, probE        int32
}

// Lexer scans one file. It keeps the trivia and problems of every token
// it has returned in arenas, so tokens are small values.
type Lexer struct {
	src    []byte
	opts   Options
	trivia []Trivia
	probs  []Problem
}

// New returns a Lexer over src. src must not change afterwards.
func New(src []byte, opts Options) *Lexer {
	if opts.Standard == 0 {
		opts.Standard = token.Std2008
	}
	return &Lexer{src: src, opts: opts}
}

// Options returns the lexer's options.
func (l *Lexer) Options() Options { return l.opts }

// Leading returns the leading trivia of t (t must come from l).
func (l *Lexer) Leading(t Token) []Trivia { return l.trivia[t.triv0:t.trivN] }

// Trailing returns the trailing trivia of t.
func (l *Lexer) Trailing(t Token) []Trivia { return l.trivia[t.trivN:t.trivE] }

// Problems returns the lexical problems of t and its trivia.
func (l *Lexer) Problems(t Token) []Problem { return l.probs[t.prob0:t.probE] }

// Reset drops the trivia and problems arenas; tokens returned earlier
// must not be used with Leading, Trailing or Problems afterwards.
func (l *Lexer) Reset() {
	l.trivia = l.trivia[:0]
	l.probs = l.probs[:0]
}

// All scans the whole of src in mode m and returns every token, ending
// with EOF, plus the lexer that holds their trivia.
func All(src []byte, opts Options, m Mode) ([]Token, *Lexer) {
	l := New(src, opts)
	var out []Token
	st := Initial(m)
	off := 0
	for {
		var t Token
		t, st = l.Lex(off, st)
		out = append(out, t)
		if t.Kind == token.EOF {
			return out, l
		}
		off = t.FullEnd
	}
}

func (l *Lexer) at(i int) byte {
	if i >= 0 && i < len(l.src) {
		return l.src[i]
	}
	return 0
}

func (l *Lexer) addTrivia(k token.Kind, start, end int) {
	if end > start {
		l.trivia = append(l.trivia, Trivia{k, start, end})
	}
}

func (l *Lexer) problem(start, end int, code diag.Code, args ...string) {
	l.probs = append(l.probs, Problem{start, end, code, args})
}

func (l *Lexer) syntaxError(start, end int, msg string) {
	l.problem(start, end, diag.SyntaxError, msg)
}

func (l *Lexer) standardError(start, end int, construct string,
	first token.Standard,
) {
	if l.opts.Standard < first {
		l.problem(start, end, diag.SyntaxStandard, construct,
			first.String(), l.opts.Standard.String())
	}
}

// Lex scans the token at off (the end of the previous token's trailing
// trivia, or 0) in state st and returns it with the next state. At the
// end of the input it returns an EOF token (repeatedly).
func (l *Lexer) Lex(off int, st State) (Token, State) {
	off = max(0, min(off, len(l.src)))
	t := Token{FullStart: off, triv0: int32(len(l.trivia)),
		prob0: int32(len(l.probs))}
	i := l.leading(off, &st)
	t.trivN = int32(len(l.trivia))
	t.Start = i
	adjacent := i == off
	if st.pslc {
		t.Flags |= PSLComment
	}
	if i >= len(l.src) {
		t.Kind, t.End = token.EOF, i
		st.pslc = false
	} else {
		psl := st.Mode == PSL || st.pslc
		t.Kind, t.Word, t.End = l.scan(i, st, psl, adjacent)
	}
	endsPSL := false
	if st.pslc {
		switch t.Kind {
		case token.LParen, token.LBracket, token.LBrace, token.PSLRepStar,
			token.PSLRepEq, token.PSLRepGoto:
			st.deep++
		case token.RParen, token.RBracket, token.RBrace:
			if st.deep > 0 {
				st.deep--
			}
		case token.Semicolon:
			endsPSL = st.deep == 0
		}
	}
	if t.Kind != token.EOF {
		st.prev = t.Kind
		if t.Kind == token.Identifier && t.Word == token.KwForce {
			// Before 2008 force lexes as an identifier, but in
			// `s <= force '1'` the ' starts a character literal (an
			// attribute of a signal named force, force'image, is
			// still a tick: charLit fails on it).
			st.prev = token.KwForce
		}
	}
	j := t.End
	if endsPSL {
		// The `;` ends the comment directive; the rest of the comment
		// line is comment text (#6).
		st.pslc, st.deep = false, 0
		e := lineEnd(l.src, j)
		l.addTrivia(token.LineComment, j, e)
		j = e
	} else if t.Kind != token.EOF {
		j = l.trailing(j, st)
	}
	t.trivE = int32(len(l.trivia))
	t.FullEnd = j
	t.probE = int32(len(l.probs))
	if r := l.region(t.Start); r > 0 && t.Kind != token.EOF {
		t.Region = r
		t.Flags |= Inactive
	}
	return t, st
}

// region returns 1 + the index of the inactive region containing off.
func (l *Lexer) region(off int) int {
	rs := l.opts.Inactive
	i := sort.Search(len(rs), func(i int) bool { return rs[i].End > off })
	if i < len(rs) && rs[i].Start <= off {
		return i + 1
	}
	return 0
}

func lineEnd(src []byte, i int) int {
	for i < len(src) && src[i] != '\n' && src[i] != '\r' {
		i++
	}
	return i
}

// whitespace returns the length of the whitespace character at i (space,
// tab, VT, FF or UTF-8 NBSP), or 0.
func (l *Lexer) whitespace(i int) int {
	switch l.at(i) {
	case ' ', '\t', '\v', '\f':
		return 1
	case 0xc2:
		if l.at(i+1) == 0xa0 {
			return 2
		}
	}
	return 0
}

func (l *Lexer) newline(i int) int {
	switch l.at(i) {
	case '\n':
		return 1
	case '\r':
		if l.at(i+1) == '\n' {
			return 2
		}
		return 1
	}
	return 0
}

// pslMarker reports whether a `-- psl` comment starts at i and returns
// the end of the marker (after "psl").
func (l *Lexer) pslMarker(i int) (int, bool) {
	if !l.opts.PSLComments || l.at(i) != '-' || l.at(i+1) != '-' {
		return 0, false
	}
	j := i + 2
	for l.at(j) == ' ' || l.at(j) == '\t' {
		j++
	}
	if lower(l.at(j)) != 'p' || lower(l.at(j+1)) != 's' ||
		lower(l.at(j+2)) != 'l' {
		return 0, false
	}
	k := j + 3
	if k < len(l.src) && l.whitespace(k) == 0 && l.newline(k) == 0 {
		return 0, false
	}
	return k, true
}

// continuation returns the end of a `--` (optionally `-- psl`)
// continuation marker at i.
func (l *Lexer) continuation(i int) int {
	if end, ok := l.pslMarker(i); ok {
		return end
	}
	return i + 2
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// leading scans the leading trivia at i and returns the token start.
func (l *Lexer) leading(i int, st *State) int {
	for i < len(l.src) {
		if n := l.whitespace(i); n > 0 {
			j := i + n
			for m := l.whitespace(j); m > 0; m = l.whitespace(j) {
				j += m
			}
			l.addTrivia(token.Whitespace, i, j)
			i = j
			continue
		}
		if n := l.newline(i); n > 0 {
			l.addTrivia(token.Newline, i, i+n)
			i += n
			if st.pslc {
				i = l.pslContinuation(i, st)
			}
			continue
		}
		c := l.at(i)
		switch {
		case c == '-' && l.at(i+1) == '-':
			if end, ok := l.pslMarker(i); ok && !st.pslc {
				l.addTrivia(token.PSLCommentMarker, i, end)
				st.pslc, st.deep = true, 0
				i = end
				continue
			}
			i = l.lineComment(i)
		case c == '/' && l.at(i+1) == '*':
			i = l.blockComment(i)
		case c == '`' && isLetter(l.at(i+1)):
			i = l.directive(i)
		default:
			return i
		}
	}
	return i
}

// pslContinuation handles the start of a line inside a `-- psl` comment
// directive: a line starting with `--` continues it, any other line ends
// it.
func (l *Lexer) pslContinuation(i int, st *State) int {
	j := i
	for m := l.whitespace(j); m > 0; m = l.whitespace(j) {
		j += m
	}
	if l.at(j) == '-' && l.at(j+1) == '-' {
		l.addTrivia(token.Whitespace, i, j)
		end := l.continuation(j)
		l.addTrivia(token.PSLCommentMarker, j, end)
		return end
	}
	st.pslc, st.deep = false, 0
	return i
}

func (l *Lexer) lineComment(i int) int {
	e := lineEnd(l.src, i)
	l.addTrivia(token.LineComment, i, e)
	l.utf8Hint(i, e)
	return e
}

func (l *Lexer) utf8Hint(start, end int) {
	if l.opts.Standard != token.Std1993 {
		return
	}
	for k := start; k < end; k++ {
		if l.src[k] >= 0x80 {
			l.problem(start, end, diag.SyntaxUTF8Comment)
			return
		}
	}
}

func (l *Lexer) blockComment(i int) int {
	end := bytes.Index(l.src[i+2:], []byte("*/"))
	e := len(l.src)
	if end >= 0 {
		e = i + 2 + end + 2
	} else {
		l.syntaxError(i, i+2, "unterminated block comment")
	}
	l.addTrivia(token.BlockComment, i, e)
	l.standardError(i, i+2, "/* */ delimited comment", token.Std2008)
	l.utf8Hint(i, e)
	return e
}

// directive scans a tool directive line (`protect, `if, ...) as trivia.
// After an IEEE 1735 encoded block header (`protect key_block,
// data_block or digest_block) the encoded lines up to the next
// `protect line are one ProtectedData piece, so encrypted IP never
// reaches the parser as tokens (the unit is opaque to sema, #20).
func (l *Lexer) directive(i int) int {
	e := lineEnd(l.src, i)
	l.addTrivia(token.Directive, i, e)
	line := bytes.ToLower(l.src[i:e])
	if !bytes.HasPrefix(line, []byte("`protect")) ||
		!encodedBlock(line) {
		return e
	}
	n := l.newline(e)
	if n == 0 {
		return e
	}
	l.addTrivia(token.Newline, e, e+n)
	start := e + n
	j := start
	for j < len(l.src) {
		k := j
		for m := l.whitespace(k); m > 0; m = l.whitespace(k) {
			k += m
		}
		le := lineEnd(l.src, k)
		if bytes.HasPrefix(bytes.ToLower(l.src[k:le]),
			[]byte("`protect")) {
			break
		}
		j = le + l.newline(le)
		if j == le {
			j = len(l.src)
		}
	}
	l.addTrivia(token.ProtectedData, start, j)
	return j
}

// encodedBlock reports whether a lower-cased `protect line starts an
// encoded block.
func encodedBlock(line []byte) bool {
	for _, w := range [][]byte{[]byte("key_block"),
		[]byte("data_block"), []byte("digest_block")} {
		if bytes.Contains(line, w) {
			return true
		}
	}
	return false
}

// trailing scans the trailing trivia after a token ending at i: blanks
// and comments up to (not including) the end of the line. A directive, a
// `-- psl` comment or a block comment spanning lines is left for the
// next token's leading trivia.
func (l *Lexer) trailing(i int, st State) int {
	for i < len(l.src) {
		if n := l.whitespace(i); n > 0 {
			j := i + n
			for m := l.whitespace(j); m > 0; m = l.whitespace(j) {
				j += m
			}
			l.addTrivia(token.Whitespace, i, j)
			i = j
			continue
		}
		c := l.at(i)
		switch {
		case c == '-' && l.at(i+1) == '-':
			if _, ok := l.pslMarker(i); ok && !st.pslc {
				return i
			}
			return l.lineComment(i)
		case c == '/' && l.at(i+1) == '*':
			end := bytes.Index(l.src[i+2:], []byte("*/"))
			if end < 0 {
				return i
			}
			body := l.src[i : i+2+end+2]
			if bytes.ContainsAny(body, "\n\r") {
				return i
			}
			i = l.blockComment(i)
		default:
			return i
		}
	}
	return i
}

// scan scans one token at i (not trivia) and returns its kind, the
// keyword it spells and its end.
func (l *Lexer) scan(i int, st State, psl, adjacent bool) (kind,
	word token.Kind, end int,
) {
	c := l.src[i]
	switch {
	case isLetter(c):
		return l.word(i, st, psl)
	case isDigit(c):
		k, e := l.number(i, psl)
		return k, 0, e
	case c >= 0x80:
		if n := latin1Letter(l.src[i:]); n > 0 {
			return l.word(i, st, psl)
		}
	}
	if psl {
		if k, n := l.pslDelimiter(i, st, adjacent); n > 0 {
			return k, 0, i + n
		}
	}
	switch c {
	case '"', '%':
		// % is the replacement for " (LRM 15.10).
		return token.StringLiteral, 0, l.stringLit(i)
	case '!':
		// ! is the replacement for | outside PSL (LRM 15.10).
		return token.Bar, 0, i + 1
	case '\\':
		return token.ExtendedIdentifier, 0, l.extended(i)
	case '\'':
		// Right after a character literal a ' is a tick:
		// THIS_PROCESS.'1''a'RIGHT names attribute a of '1'.
		if !tickPrev(st.prev) &&
			(!adjacent || st.prev != token.CharacterLiteral ||
				l.at(i-1) != '\'') {
			if e, ok := l.charLit(i); ok {
				return token.CharacterLiteral, 0, e
			}
		}
		return token.Tick, 0, i + 1
	}
	if k, n := l.delimiter(i); n > 0 {
		return k, 0, i + n
	}
	_, n := utf8.DecodeRune(l.src[i:])
	if c < 0x80 {
		l.syntaxError(i, i+1, "invalid character "+quoteByte(c))
	} else {
		l.syntaxError(i, i+n, "invalid character in source text")
	}
	return token.Error, 0, i + n
}

func quoteByte(c byte) string {
	if c >= 0x20 && c < 0x7f {
		return "'" + string(rune(c)) + "'"
	}
	const hex = "0123456789abcdef"
	return "0x" + string([]byte{hex[c>>4], hex[c&15]})
}

// latin1Letter returns the length of a UTF-8 encoded Latin-1 letter
// (U+00C0..U+00FF except U+00D7 and U+00F7) at the start of b, or 0.
func latin1Letter(b []byte) int {
	if len(b) < 2 || (b[0] != 0xc3) || b[1] < 0x80 || b[1] > 0xbf {
		return 0
	}
	if b[1] == 0x97 || b[1] == 0xb7 {
		return 0
	}
	return 2
}

// tickPrev reports whether a ' after a token of kind k is an attribute
// tick rather than the start of a character literal.
func tickPrev(k token.Kind) bool {
	switch k {
	case token.Identifier, token.ExtendedIdentifier, token.RParen,
		token.RBracket, token.KwAll:
		return true
	}
	if kw, ok := k.Keyword(); ok {
		// PSL words are names in HDL operands (`a'event` in PSL).
		return kw.Class == token.ClassPSL || kw.Class == token.ClassPSL2008
	}
	return false
}

func (l *Lexer) charLit(i int) (int, bool) {
	r, n := utf8.DecodeRune(l.src[i+1:])
	if n == 0 || r == '\n' || r == '\r' {
		return 0, false
	}
	if l.at(i+1+n) == '\'' {
		return i + 2 + n, true
	}
	return 0, false
}

// word scans an identifier, keyword or bit-string literal.
func (l *Lexer) word(i int, st State, psl bool) (kind, word token.Kind,
	end int,
) {
	j := i
	for j < len(l.src) {
		c := l.src[j]
		if isLetter(c) || isDigit(c) || c == '_' {
			j++
			continue
		}
		if n := latin1Letter(l.src[j:]); n > 0 {
			j += n
			continue
		}
		break
	}
	if psl && j-i > 1 && l.src[j-1] == '_' {
		j-- // the inclusive suffix: before_, until_ (#6)
	}
	if q := l.at(j); q == '"' || q == '%' {
		if baseSpec(l.src[i:j]) {
			return token.BitStringLiteral, 0, l.bitString(i, i, j)
		}
	}
	for k := i; k < j; k++ {
		if l.src[k] == '_' && (k+1 == j || l.src[k+1] == '_') {
			if k+1 == j {
				l.syntaxError(k, k+1, "an identifier cannot end with "+
					"an underscore")
			} else {
				l.syntaxError(k, k+2, "an identifier cannot contain "+
					"two underscores in a row")
			}
			break
		}
	}
	kind, word = token.Lookup(l.src[i:j], l.opts.Standard, psl,
		l.opts.StrictKeywords)
	if kind == token.KwEventually && l.at(j) != '!' {
		l.syntaxError(i, j, "eventually must be written eventually!")
	}
	return kind, word, j
}

// baseSpec reports whether w is a bit-string base specifier.
func baseSpec(w []byte) bool {
	switch len(w) {
	case 1:
		switch lower(w[0]) {
		case 'b', 'o', 'x', 'd':
			return true
		}
	case 2:
		a, b := lower(w[0]), lower(w[1])
		return (a == 'u' || a == 's') && (b == 'b' || b == 'o' || b == 'x')
	}
	return false
}

// bitString scans a bit-string literal: [size] base "value" (or with
// the replacement delimiter %). start is the token start, base and
// quote delimit the base specifier.
func (l *Lexer) bitString(start, base, quote int) int {
	q := l.src[quote]
	e := quote + 1
	for e < len(l.src) && l.src[e] != q && l.src[e] != '\n' &&
		l.src[e] != '\r' {
		e++
	}
	if l.at(e) == q {
		e++
	} else {
		l.syntaxError(start, e, "unterminated bit string literal")
	}
	b := l.src[base:quote]
	if base > start || len(b) == 2 || lower(b[0]) == 'd' {
		l.standardError(start, e, "sized/decimal bit string", token.Std2008)
	}
	return e
}

// stringLit scans a string literal delimited by " or by its
// replacement % (a doubled delimiter stands for one).
func (l *Lexer) stringLit(i int) int {
	q := l.src[i]
	j := i + 1
	for j < len(l.src) {
		switch c := l.src[j]; {
		case c == q:
			if l.at(j+1) == q {
				j += 2
				continue
			}
			return j + 1
		case c == '\n', c == '\r':
			l.syntaxError(i, j, "unterminated string literal")
			return j
		}
		j++
	}
	l.syntaxError(i, j, "unterminated string literal")
	return j
}

func (l *Lexer) extended(i int) int {
	j := i + 1
	for j < len(l.src) {
		switch l.src[j] {
		case '\\':
			if l.at(j+1) == '\\' {
				j += 2
				continue
			}
			if j == i+1 {
				l.syntaxError(i, j+1, "an extended identifier cannot "+
					"be empty")
			}
			return j + 1
		case '\n', '\r':
			l.syntaxError(i, j, "unterminated extended identifier")
			return j
		}
		j++
	}
	l.syntaxError(i, j, "unterminated extended identifier")
	return j
}

// digits scans digits separated by single underscores starting at i,
// where ok(c) accepts a digit. It reports malformed underscores.
func (l *Lexer) digits(i int, ok func(byte) bool) int {
	j := i
	for j < len(l.src) {
		c := l.src[j]
		if ok(c) {
			j++
			continue
		}
		if c == '_' {
			if j == i || !ok(l.at(j+1)) {
				l.syntaxError(j, j+1, "misplaced underscore in a "+
					"literal")
			}
			j++
			continue
		}
		break
	}
	return j
}

func isExtDigit(c byte) bool { return isDigit(c) || isLetter(c) }

func digitValue(c byte) int {
	switch {
	case isDigit(c):
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 99
}

// number scans an abstract literal or a sized bit-string literal.
// Outside PSL a based literal may use the replacement delimiter : for
// # (2:1_0:, LRM 15.10) when a matching : closes it; inside PSL `2:3`
// is a range.
func (l *Lexer) number(i int, psl bool) (token.Kind, int) {
	j := l.digits(i, isDigit)
	kind := token.IntegerLiteral
	sharp := byte('#')
	if !psl && l.at(j) == ':' && l.colonBased(j) {
		sharp = ':'
	}
	if l.at(j) == sharp {
		base := 0
		for _, c := range l.src[i:j] {
			if c != '_' && base <= 16 {
				base = base*10 + int(c-'0')
			}
		}
		if base < 2 || base > 16 {
			l.syntaxError(i, j, "the base of a based literal must be "+
				"2 to 16")
			base = 16
		}
		k := j + 1
		e := l.digits(k, isExtDigit)
		if l.at(e) == '.' && isExtDigit(l.at(e+1)) {
			kind = token.RealLiteral
			e = l.digits(e+1, isExtDigit)
		}
		for _, c := range l.src[k:e] {
			if c != '_' && c != '.' && digitValue(c) >= base {
				l.syntaxError(k, e, "digit "+string(rune(c))+
					" is not valid in base "+itoa(base))
				break
			}
		}
		if l.at(e) != sharp {
			l.syntaxError(i, e, "unterminated based literal")
			return kind, e
		}
		return kind, l.exponent(e + 1)
	}
	if l.at(j) == '.' && isDigit(l.at(j+1)) {
		kind = token.RealLiteral
		j = l.digits(j+1, isDigit)
	}
	if e := l.exponent(j); e > j {
		return kind, e
	}
	if kind == token.IntegerLiteral {
		// Sized bit string: 8x"FF", 12UB"...".
		k := j
		for k < len(l.src) && isLetter(l.src[k]) && k-j < 2 {
			k++
		}
		if q := l.at(k); k > j && (q == '"' || q == '%') &&
			baseSpec(l.src[j:k]) {
			return token.BitStringLiteral, l.bitString(i, j, k)
		}
	}
	return kind, j
}

// colonBased reports whether the : at j opens a based literal written
// with the replacement delimiter: extended digits, underscores and at
// most one point, then a closing :.
func (l *Lexer) colonBased(j int) bool {
	k := j + 1
	if !isExtDigit(l.at(k)) {
		return false
	}
	for k < len(l.src) && (isExtDigit(l.src[k]) || l.src[k] == '_' ||
		l.src[k] == '.') {
		k++
	}
	return l.at(k) == ':' && l.at(k+1) != '='
}

func (l *Lexer) exponent(i int) int {
	if c := l.at(i); c != 'e' && c != 'E' {
		return i
	}
	j := i + 1
	if c := l.at(j); c == '+' || c == '-' {
		j++
	}
	if !isDigit(l.at(j)) {
		return i
	}
	return l.digits(j, isDigit)
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string([]byte{byte('0' + n/10), byte('0' + n%10)})
}

// delimiter scans a VHDL delimiter at i.
func (l *Lexer) delimiter(i int) (token.Kind, int) {
	c, d := l.at(i), l.at(i+1)
	switch c {
	case '&':
		return token.Ampersand, 1
	case '(':
		return token.LParen, 1
	case ')':
		return token.RParen, 1
	case '*':
		if d == '*' {
			return token.DoubleStar, 2
		}
		return token.Star, 1
	case '+':
		return token.Plus, 1
	case ',':
		return token.Comma, 1
	case '-':
		return token.Minus, 1
	case '.':
		return token.Dot, 1
	case '/':
		if d == '=' {
			return token.Ne, 2
		}
		return token.Slash, 1
	case ':':
		if d == '=' {
			return token.VarAssign, 2
		}
		return token.Colon, 1
	case ';':
		return token.Semicolon, 1
	case '<':
		switch d {
		case '=':
			return token.Le, 2
		case '>':
			return token.Box, 2
		case '<':
			return token.LtLt, 2
		}
		return token.Lt, 1
	case '=':
		if d == '>' {
			return token.Arrow, 2
		}
		return token.Eq, 1
	case '>':
		switch d {
		case '=':
			return token.Ge, 2
		case '>':
			return token.GtGt, 2
		}
		return token.Gt, 1
	case '|':
		return token.Bar, 1
	case '[':
		return token.LBracket, 1
	case ']':
		return token.RBracket, 1
	case '?':
		switch d {
		case '?':
			return token.Condition, 2
		case '=':
			return token.MatchEq, 2
		case '/':
			if l.at(i+2) == '=' {
				return token.MatchNe, 3
			}
		case '<':
			if l.at(i+2) == '=' {
				return token.MatchLe, 3
			}
			return token.MatchLt, 2
		case '>':
			if l.at(i+2) == '=' {
				return token.MatchGe, 3
			}
			return token.MatchGt, 2
		}
		return token.Question, 1
	case '@':
		return token.At, 1
	case '^':
		return token.Caret, 1
	case '{':
		return token.LBrace, 1
	case '}':
		return token.RBrace, 1
	}
	return 0, 0
}

// pslDelimiter scans a PSL-only token at i (#6): -> <-> |-> |=> [* [+]
// [= [-> && || ! and the ! and _ suffixes.
func (l *Lexer) pslDelimiter(i int, st State, adjacent bool) (token.Kind,
	int,
) {
	c, d, e := l.at(i), l.at(i+1), l.at(i+2)
	switch c {
	case '-':
		if d == '>' {
			return token.PSLImplies, 2
		}
	case '<':
		if d == '-' && e == '>' {
			return token.PSLIff, 3
		}
	case '|':
		switch {
		case d == '-' && e == '>':
			return token.PSLOverlapImplies, 3
		case d == '=' && e == '>':
			return token.PSLNextImplies, 3
		case d == '|':
			return token.PSLOrOr, 2
		}
	case '[':
		switch {
		case d == '*':
			return token.PSLRepStar, 2
		case d == '+' && e == ']':
			return token.PSLRepPlus, 3
		case d == '=':
			return token.PSLRepEq, 2
		case d == '-' && e == '>':
			return token.PSLRepGoto, 3
		}
	case '&':
		if d == '&' {
			return token.PSLAndAnd, 2
		}
	case '!':
		if adjacent && takesSuffix(st.prev, token.SuffixBang) {
			return token.PSLBangSuffix, 1
		}
		return token.PSLBang, 1
	case '_':
		if adjacent && (takesSuffix(st.prev, token.SuffixUnderscore) ||
			st.prev == token.PSLBangSuffix) {
			return token.PSLUnderscoreSuffix, 1
		}
	}
	return 0, 0
}

func takesSuffix(k token.Kind, s token.Suffix) bool {
	kw, ok := k.Keyword()
	return ok && kw.Suffixes&s != 0
}

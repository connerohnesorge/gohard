package lexer

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/token"
)

// keywordEdge is one generated edge value (keywords_gen_test.go): a word
// under a standard, mode and strict setting, and the kind it must lex
// as. The expected kinds come from tools/genkinds' own statement of the
// rules, an independent oracle for token.Keyword.Reserved.
type keywordEdge struct {
	word   string
	std    token.Standard
	psl    bool
	strict bool
	want   token.Kind
}

func lexOne(t *testing.T, src string, opts Options, m Mode) []Token {
	t.Helper()
	toks, _ := All([]byte(src), opts, m)
	return toks
}

func modeOf(psl bool) Mode {
	if psl {
		return PSL
	}
	return VHDL
}

// TestKeywordEdges lexes every keyword under every standard, lexer mode
// and psl.strict_keywords setting, in lower, upper and mixed case.
func TestKeywordEdges(t *testing.T) {
	if len(keywordEdges) != len(token.Keywords())*4*2*2 {
		t.Fatalf("got %d edges for %d keywords", len(keywordEdges),
			len(token.Keywords()))
	}
	for _, e := range keywordEdges {
		opts := Options{Standard: e.std, StrictKeywords: e.strict}
		for _, w := range []string{e.word, strings.ToUpper(e.word),
			mixed(e.word)} {
			toks := lexOne(t, w, opts, modeOf(e.psl))
			if len(toks) < 2 {
				t.Fatalf("%q: %d tokens", w, len(toks))
			}
			got := toks[0]
			if got.Kind != e.want || got.End != len(w) {
				t.Errorf("%q std %v psl %v strict %v: got %v (end %d), "+
					"want %v", w, e.std, e.psl, e.strict, got.Kind,
					got.End, e.want)
			}
			if got.Word != token.LookupWord(e.word) {
				t.Errorf("%q: Word %v", w, got.Word)
			}
		}
	}
}

func mixed(w string) string {
	b := []byte(w)
	for i := range b {
		if i%2 == 0 && b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 'a' - 'A'
		}
	}
	return string(b)
}

// TestInheritIsIdentifier: `inherit` is never a keyword (#31).
func TestInheritIsIdentifier(t *testing.T) {
	for _, std := range token.Standards {
		for _, m := range []Mode{VHDL, PSL} {
			for _, strict := range []bool{false, true} {
				toks := lexOne(t, "inherit", Options{Standard: std,
					StrictKeywords: strict}, m)
				if toks[0].Kind != token.Identifier ||
					toks[0].Word != token.KwInherit {
					t.Errorf("%v %v %v: %v", std, m, strict, toks[0].Kind)
				}
			}
		}
	}
}

// TestReservationClasses checks the table data against #6.
func TestReservationClasses(t *testing.T) {
	everywhere := []string{"context", "cover", "default", "force",
		"parameter", "release"}
	pslOnly := []string{"assume", "assume_guarantee", "fairness",
		"property", "restrict", "restrict_guarantee", "sequence", "strong",
		"vmode", "vprop", "vunit"}
	n2008 := 0
	for _, kw := range token.Keywords() {
		if kw.First == token.Std2008 && (kw.Class == token.ClassVHDL ||
			kw.Class == token.ClassPSL2008) {
			n2008++
		}
	}
	if n2008 != 17 {
		t.Errorf("2008 adds %d words, want 17", n2008)
	}
	for _, w := range everywhere {
		kw, _ := token.LookupWord(w).Keyword()
		if kw.Class != token.ClassVHDL || kw.First != token.Std2008 {
			t.Errorf("%s: %v %v", w, kw.Class, kw.First)
		}
	}
	for _, w := range pslOnly {
		kw, _ := token.LookupWord(w).Keyword()
		if kw.Class != token.ClassPSL2008 {
			t.Errorf("%s: class %v", w, kw.Class)
		}
	}
	for _, w := range []string{"private", "view"} {
		kw, _ := token.LookupWord(w).Keyword()
		if kw.First != token.Std2019 || kw.Class != token.ClassVHDL {
			t.Errorf("%s: %v", w, kw.First)
		}
	}
	for _, w := range []string{"vpkg", "converse"} {
		if token.LookupWord(w) != 0 {
			t.Errorf("%s must not be a keyword", w)
		}
	}
}

type lexCase struct {
	src   string
	std   token.Standard
	mode  Mode
	kinds []token.Kind // without the EOF
}

func kindsOf(toks []Token) []token.Kind {
	var out []token.Kind
	for _, t := range toks {
		if t.Kind != token.EOF {
			out = append(out, t.Kind)
		}
	}
	return out
}

// TestFixedTokens lexes the fixed spelling of every non-keyword token
// kind on its own, in the mode that scans it.
func TestFixedTokens(t *testing.T) {
	special := map[token.Kind]lexCase{
		token.PSLBangSuffix: {src: "eventually!", mode: PSL,
			kinds: []token.Kind{token.KwEventually, token.PSLBangSuffix}},
		token.PSLUnderscoreSuffix: {src: "until_", mode: PSL,
			kinds: []token.Kind{token.KwUntil, token.PSLUnderscoreSuffix}},
	}
	for _, k := range token.Kinds() {
		if k.IsKeyword() || k.Text() == "" {
			continue
		}
		c, ok := special[k]
		if !ok {
			c = lexCase{src: k.Text(), kinds: []token.Kind{k}}
			if k.IsPSLOnly() {
				c.mode = PSL
			}
		}
		got := kindsOf(lexOne(t, c.src, Options{Standard: token.Std2019},
			c.mode))
		if !slices.Equal(got, c.kinds) {
			t.Errorf("%v %q: got %v, want %v", k, c.src, got, c.kinds)
		}
	}
}

// TestPSLOnlyTokensOutsidePSL: PSL operators split into VHDL tokens in
// VHDL mode.
func TestPSLOnlyTokensOutsidePSL(t *testing.T) {
	cases := map[string][]token.Kind{
		"->":  {token.Minus, token.Gt},
		"<->": {token.Lt, token.Minus, token.Gt},
		"|=>": {token.Bar, token.Arrow},
		"[*":  {token.LBracket, token.Star},
		"&&":  {token.Ampersand, token.Ampersand},
		"||":  {token.Bar, token.Bar},
		"!":   {token.Bar}, // the replacement character for |
	}
	for src, want := range cases {
		got := kindsOf(lexOne(t, src, Options{}, VHDL))
		if !slices.Equal(got, want) {
			t.Errorf("%q: got %v, want %v", src, got, want)
		}
	}
}

func TestTokenClasses(t *testing.T) {
	I, X := token.Identifier, token.ExtendedIdentifier
	cases := []lexCase{
		{src: "abc A_1 \\a\\\\b\\ \\x\\", kinds: []token.Kind{I, I, X, X}},
		{src: "0 1_000 1.5 1.0e-3 2E6 16#FF# 2#1.1#e2 8#7_7#",
			kinds: []token.Kind{token.IntegerLiteral,
				token.IntegerLiteral, token.RealLiteral, token.RealLiteral,
				token.IntegerLiteral, token.IntegerLiteral,
				token.RealLiteral, token.IntegerLiteral}},
		{src: `"ab""c" "" x"F0" B"1_0" o"7" 8x"FF" d"12" 12UB"1" sx"F"`,
			kinds: []token.Kind{token.StringLiteral, token.StringLiteral,
				token.BitStringLiteral, token.BitStringLiteral,
				token.BitStringLiteral, token.BitStringLiteral,
				token.BitStringLiteral, token.BitStringLiteral,
				token.BitStringLiteral}},
		{src: "'a' ''' ' '", kinds: []token.Kind{token.CharacterLiteral,
			token.CharacterLiteral, token.CharacterLiteral}},
		// Tick vs character literal.
		{src: "s'event", kinds: []token.Kind{I, token.Tick, I}},
		{src: "t'('a')", kinds: []token.Kind{I, token.Tick, token.LParen,
			token.CharacterLiteral, token.RParen}},
		{src: "f(x)'length", kinds: []token.Kind{I, token.LParen, I,
			token.RParen, token.Tick, I}},
		{src: "range 'a' to 'z'", kinds: []token.Kind{token.KwRange,
			token.CharacterLiteral, token.KwTo, token.CharacterLiteral}},
		{src: "a'b'c", kinds: []token.Kind{I, token.Tick, I, token.Tick,
			I}},
		{src: "10ns", kinds: []token.Kind{token.IntegerLiteral, I}},
		{src: "a<=b>=c/=d:=e=>f**g<>h", kinds: []token.Kind{I, token.Le,
			I, token.Ge, I, token.Ne, I, token.VarAssign, I, token.Arrow,
			I, token.DoubleStar, I, token.Box, I}},
		{src: "?? ?= ?/= ?< ?<= ?> ?>= << >> @ ^",
			kinds: []token.Kind{token.Condition, token.MatchEq,
				token.MatchNe, token.MatchLt, token.MatchLe, token.MatchGt,
				token.MatchGe, token.LtLt, token.GtGt, token.At,
				token.Caret}},
		// PSL suffixes and splitting.
		{src: "before!_ until_ next_a! a -> b", mode: PSL,
			kinds: []token.Kind{token.KwBefore, token.PSLBangSuffix,
				token.PSLUnderscoreSuffix, token.KwUntil,
				token.PSLUnderscoreSuffix, token.KwNextA,
				token.PSLBangSuffix, token.KwA, token.PSLImplies, I}},
		{src: "!a", mode: PSL, kinds: []token.Kind{token.PSLBang, token.KwA}},
		{src: "{a;b}[*2] |-> c [+] [=1] [->]", mode: PSL,
			kinds: []token.Kind{token.LBrace, token.KwA, token.Semicolon,
				I, token.RBrace, token.PSLRepStar, token.IntegerLiteral,
				token.RBracket, token.PSLOverlapImplies, I,
				token.PSLRepPlus, token.PSLRepEq, token.IntegerLiteral,
				token.RBracket, token.PSLRepGoto, token.RBracket}},
		{src: "a'event", mode: PSL, kinds: []token.Kind{token.KwA,
			token.Tick, I}},
	}
	for _, c := range cases {
		std := c.std
		if std == 0 {
			std = token.Std2019
		}
		got := kindsOf(lexOne(t, c.src, Options{Standard: std}, c.mode))
		if !slices.Equal(got, c.kinds) {
			t.Errorf("%q: got %v, want %v", c.src, got, c.kinds)
		}
	}
}

func problems(src string, opts Options, m Mode) []Problem {
	l := New([]byte(src), opts)
	var out []Problem
	st := Initial(m)
	off := 0
	for {
		tk, next := l.Lex(off, st)
		out = append(out, l.Problems(tk)...)
		if tk.Kind == token.EOF {
			return out
		}
		off, st = tk.FullEnd, next
	}
}

func codes(ps []Problem) []string {
	var out []string
	for _, p := range ps {
		s := string(p.Code)
		if len(p.Args) > 0 {
			s += ":" + p.Args[0]
		}
		out = append(out, s)
	}
	return out
}

func TestLexProblems(t *testing.T) {
	cases := []struct {
		src  string
		std  token.Standard
		mode Mode
		want []string
	}{
		{src: "a_", want: []string{"syntax/error:an identifier cannot " +
			"end with an underscore"}},
		{src: "a__b", want: []string{"syntax/error:an identifier " +
			"cannot contain two underscores in a row"}},
		{src: "until_", mode: PSL},
		{src: "eventually", mode: PSL, want: []string{
			"syntax/error:eventually must be written eventually!"}},
		{src: "eventually", mode: VHDL},
		{src: "/* x */", std: token.Std2002, want: []string{
			"syntax/standard:/* */ delimited comment"}},
		{src: "/* x */", std: token.Std2008},
		{src: "/* x", want: []string{
			"syntax/error:unterminated block comment"}},
		{src: `8x"FF"`, std: token.Std1993, want: []string{
			"syntax/standard:sized/decimal bit string"}},
		{src: `x"FF"`, std: token.Std1993},
		{src: `"abc`, want: []string{
			"syntax/error:unterminated string literal"}},
		{src: "\\abc", want: []string{
			"syntax/error:unterminated extended identifier"}},
		{src: "17#1#", want: []string{"syntax/error:the base of a " +
			"based literal must be 2 to 16"}},
		{src: "2#12#", want: []string{"syntax/error:digit 2 is not " +
			"valid in base 2"}},
		{src: "16#FF", want: []string{
			"syntax/error:unterminated based literal"}},
		{src: "1__0", want: []string{
			"syntax/error:misplaced underscore in a literal"}},
		{src: "$", want: []string{"syntax/error:invalid character '$'"}},
		{src: "-- é", std: token.Std1993, want: []string{
			"syntax/utf8-comment"}},
		{src: "-- é", std: token.Std2008},
	}
	for _, c := range cases {
		std := c.std
		if std == 0 {
			std = token.Std2008
		}
		got := codes(problems(c.src, Options{Standard: std}, c.mode))
		if !slices.Equal(got, c.want) {
			t.Errorf("%q (%v %v): got %q, want %q", c.src, std, c.mode,
				got, c.want)
		}
	}
}

// TestPSLComments: `-- psl` lines are lexed as PSL tokens when
// psl.comments is set, continue on `--` lines and end at the `;`.
func TestPSLComments(t *testing.T) {
	src := "a <= b; -- psl assert always\n  --   a -> b; -- note\n" +
		"-- plain\nc"
	opts := Options{Standard: token.Std2008, PSLComments: true}
	toks, l := All([]byte(src), opts, VHDL)
	var got []string
	for _, tk := range toks {
		s := tk.Kind.String()
		if tk.Flags&PSLComment != 0 {
			s += "*"
		}
		got = append(got, s)
	}
	want := []string{"Identifier", "Le", "Identifier", "Semicolon",
		"KwAssert*", "KwAlways*", "KwA*", "PSLImplies*", "Identifier*",
		"Semicolon*", "Identifier", "EOF"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
	// The `;` ends the directive: the rest of its line is a comment,
	// the next `--` line is a plain comment.
	semi := toks[9]
	tr := l.Trailing(semi)
	if len(tr) != 1 || tr[0].Kind != token.LineComment ||
		src[tr[0].Start:tr[0].End] != " -- note" {
		t.Errorf("trailing of ; = %v", tr)
	}
	// Off: the whole line is a comment.
	toks, _ = All([]byte(src), Options{Standard: token.Std2008}, VHDL)
	if len(toks) != 6 {
		t.Errorf("psl.comments off: %d tokens", len(toks))
	}
}

func TestDirectives(t *testing.T) {
	src := "`if X = \"1\" then\na\n`protect data_block\nGARBAGE \" '\n" +
		"`protect end_protected\nb"
	toks, l := All([]byte(src), Options{Standard: token.Std2019}, VHDL)
	if got := kindsOf(toks); !slices.Equal(got, []token.Kind{
		token.Identifier, token.Identifier}) {
		t.Fatalf("kinds %v", got)
	}
	var kinds []token.Kind
	for _, tk := range toks {
		for _, tr := range l.Leading(tk) {
			kinds = append(kinds, tr.Kind)
		}
	}
	want := []token.Kind{token.Directive, token.Newline, token.Newline,
		token.Directive, token.Newline, token.ProtectedData,
		token.Directive, token.Newline}
	if !slices.Equal(kinds, want) {
		t.Errorf("trivia %v, want %v", kinds, want)
	}
}

// pieces are the alphabet of the random inputs: VHDL and PSL fragments
// that exercise every scanner path, plus invalid UTF-8.
var pieces = []string{
	"a", "Z", "_", "0", "9", "#", ".", "e", "x", "\"", "'", "\\", " ",
	"\t", "\n", "\r", "\r\n", "--", "-- psl ", "/*", "*/", "`", "`if",
	"`protect data_block", "`protect end_protected", "`protect key_block",
	"%", "2:", "!", "-", ">", "<", "=",
	"|", "[", "]", "*", "+", "!", "&", "?", "/", ":", ";", "(", ")",
	"{", "}", "@", "^", "é", "\xff", "\xc3", "\xc2\xa0", "\x00",
	"entity", "eventually", "before", "until", "next_a", "16#", "8x\"",
	"ub\"", "'''", "1.5e", "\v", "\f",
}

func randomInput(r *env.SplitMix64) []byte {
	n := int(r.Uint64() % 40)
	var b []byte
	for range n {
		b = append(b, pieces[r.Uint64()%uint64(len(pieces))]...)
	}
	return b
}

// checkCover checks the round-trip invariant: tokens and their trivia
// tile the input exactly, in order.
func checkCover(t *testing.T, src []byte, toks []Token, l *Lexer) {
	t.Helper()
	at := 0
	for i, tk := range toks {
		if tk.FullStart != at {
			t.Fatalf("%q: token %d starts at %d, want %d", src, i,
				tk.FullStart, at)
		}
		for _, tr := range l.Leading(tk) {
			if tr.Start != at || tr.End <= tr.Start {
				t.Fatalf("%q: bad leading trivia %v at %d", src, tr, at)
			}
			at = tr.End
		}
		if tk.Start != at || tk.End < tk.Start {
			t.Fatalf("%q: token %d text %d..%d at %d", src, i, tk.Start,
				tk.End, at)
		}
		if tk.Kind != token.EOF && tk.End == tk.Start {
			t.Fatalf("%q: empty token %v", src, tk.Kind)
		}
		at = tk.End
		for _, tr := range l.Trailing(tk) {
			if tr.Start != at || tr.End <= tr.Start {
				t.Fatalf("%q: bad trailing trivia %v", src, tr)
			}
			at = tr.End
		}
		if tk.FullEnd != at {
			t.Fatalf("%q: FullEnd %d, want %d", src, tk.FullEnd, at)
		}
	}
	if at != len(src) || toks[len(toks)-1].Kind != token.EOF {
		t.Fatalf("%q: covered %d of %d bytes", src, at, len(src))
	}
}

// TestRoundTripRandom: on generated inputs (edge pieces first, then
// random sequences) under every standard and mode, the tokens tile the
// input, lexing is a pure function of (offset, state), and nothing
// panics.
func TestRoundTripRandom(t *testing.T) {
	r := &env.SplitMix64{State: 31}
	var inputs [][]byte
	for _, p := range pieces {
		inputs = append(inputs, []byte(p))
	}
	for range 3000 {
		inputs = append(inputs, randomInput(r))
	}
	for _, src := range inputs {
		for _, std := range token.Standards {
			for _, m := range []Mode{VHDL, PSL} {
				opts := Options{Standard: std, PSLComments: r.Uint64()%2 == 0,
					StrictKeywords: r.Uint64()%2 == 0}
				toks, l := All(src, opts, m)
				checkCover(t, src, toks, l)
				checkPure(t, src, opts, m, toks)
			}
		}
	}
}

// checkPure re-lexes from every token start with the state that led to
// it and expects the same token.
func checkPure(t *testing.T, src []byte, opts Options, m Mode,
	toks []Token,
) {
	t.Helper()
	l := New(src, opts)
	st := Initial(m)
	for i, want := range toks {
		got, next := l.Lex(want.FullStart, st)
		again, next2 := l.Lex(want.FullStart, st)
		if got.Kind != want.Kind || got.FullEnd != want.FullEnd ||
			again.Kind != got.Kind || again.FullEnd != got.FullEnd ||
			next != next2 {
			t.Fatalf("%q: token %d not reproducible", src, i)
		}
		st = next
	}
}

func TestInactiveFlag(t *testing.T) {
	src := "a b c d"
	opts := Options{Inactive: []Region{{2, 3}, {5, 7}}}
	toks, _ := All([]byte(src), opts, VHDL)
	var got []int
	for _, tk := range toks {
		got = append(got, tk.Region)
	}
	if !slices.Equal(got, []int{0, 1, 0, 2, 0}) {
		t.Errorf("regions %v", got)
	}
}

func TestLatin1Identifiers(t *testing.T) {
	toks := lexOne(t, "café × b", Options{}, VHDL)
	got := kindsOf(toks)
	want := []token.Kind{token.Identifier, token.Error, token.Identifier}
	if !slices.Equal(got, want) || !bytes.Equal([]byte("café"),
		[]byte("café")[toks[0].Start:toks[0].End]) {
		t.Errorf("got %v", got)
	}
}

// TestReplacementCharacters: LRM 15.10 replacement characters lex as
// the characters they replace outside PSL: ! for |, : for # in based
// literals and % for " in string and bit-string literals.
func TestReplacementCharacters(t *testing.T) {
	cases := []struct {
		src  string
		want []token.Kind
	}{
		{"0 ! 1", []token.Kind{token.IntegerLiteral, token.Bar,
			token.IntegerLiteral}},
		{"2:1_0:", []token.Kind{token.IntegerLiteral}},
		{"16:F.FF:E+2", []token.Kind{token.RealLiteral}},
		{"16:E:E1;", []token.Kind{token.IntegerLiteral,
			token.Semicolon}},
		{"%%%%", []token.Kind{token.StringLiteral}},
		{"%a\"b%", []token.Kind{token.StringLiteral}},
		{"X%FF%", []token.Kind{token.BitStringLiteral}},
		{"b%1111_1111%", []token.Kind{token.BitStringLiteral}},
		// Not a based literal: no closing colon, or :=.
		{"2:a", []token.Kind{token.IntegerLiteral, token.Colon,
			token.Identifier}},
		{"2:a:=", []token.Kind{token.IntegerLiteral, token.Colon,
			token.Identifier, token.VarAssign}},
	}
	for _, c := range cases {
		toks, l := All([]byte(c.src), Options{}, VHDL)
		if got := kindsOf(toks); !slices.Equal(got, c.want) {
			t.Errorf("%q: got %v, want %v", c.src, got, c.want)
		}
		for _, tk := range toks {
			if ps := l.Problems(tk); len(ps) > 0 {
				t.Errorf("%q: unexpected problems %v", c.src, ps)
			}
		}
	}
	// Inside PSL `2:3` is a range bound pair, and ! is PSL's not.
	got := kindsOf(lexOne(t, "2:3:", Options{}, PSL))
	want := []token.Kind{token.IntegerLiteral, token.Colon,
		token.IntegerLiteral, token.Colon}
	if !slices.Equal(got, want) {
		t.Errorf("PSL 2:3: got %v, want %v", got, want)
	}
	if got := kindsOf(lexOne(t, "!a", Options{}, PSL)); got[0] !=
		token.PSLBang {
		t.Errorf("PSL !: got %v", got)
	}
}

// TestTickAfterCharacterLiteral: a ' right after a character literal
// is an attribute tick ('1”a'RIGHT is '1' ' a ' RIGHT).
func TestTickAfterCharacterLiteral(t *testing.T) {
	got := kindsOf(lexOne(t, "P.'1''a'RIGHT", Options{}, VHDL))
	want := []token.Kind{token.Identifier, token.Dot,
		token.CharacterLiteral, token.Tick, token.Identifier, token.Tick,
		token.Identifier}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestProtectedBlocks: the encoded lines after `protect key_block,
// data_block and digest_block are one ProtectedData trivia piece each,
// up to the next `protect line; no token is lexed from them.
func TestProtectedBlocks(t *testing.T) {
	src := "`protect begin_protected\n`protect key_block\nabc+/=\n" +
		"de f;\n`protect data_block\nxyz end;\n  qq\n" +
		"`protect end_protected\nentity"
	toks, l := All([]byte(src), Options{}, VHDL)
	if got := kindsOf(toks); !slices.Equal(got,
		[]token.Kind{token.KwEntity}) {
		t.Fatalf("tokens %v, want only entity", got)
	}
	var data []string
	for _, tr := range l.Leading(toks[0]) {
		if tr.Kind == token.ProtectedData {
			data = append(data, src[tr.Start:tr.End])
		}
	}
	want := []string{"abc+/=\nde f;\n", "xyz end;\n  qq\n"}
	if !slices.Equal(data, want) {
		t.Errorf("protected data %q, want %q", data, want)
	}
}

// TestTrailingTriviaSplit pins the trailing-trivia rule the doc
// comments depend on (#31): blanks and a same-line comment after a
// token are its trailing trivia; the newline and everything after it
// lead the next token.
func TestTrailingTriviaSplit(t *testing.T) {
	src := "a; -- about a\n-- doc b\nb;"
	toks, l := All([]byte(src), Options{}, VHDL)
	semi := toks[1]
	var trail []token.Kind
	for _, tr := range l.Trailing(semi) {
		trail = append(trail, tr.Kind)
	}
	if !slices.Equal(trail, []token.Kind{token.Whitespace,
		token.LineComment}) {
		t.Errorf("trailing of ; = %v", trail)
	}
	var lead []string
	for _, tr := range l.Leading(toks[2]) {
		lead = append(lead, src[tr.Start:tr.End])
	}
	if !slices.Equal(lead, []string{"\n", "-- doc b", "\n"}) {
		t.Errorf("leading of b = %q", lead)
	}
}

// TestKeywordEdgesPSLComment covers the third lexer mode (#6, #31): a
// keyword inside a `-- psl` comment line lexes exactly as in PSL mode,
// for every keyword, standard and psl.strict_keywords setting.
func TestKeywordEdgesPSLComment(t *testing.T) {
	n := 0
	for _, e := range keywordEdges {
		if !e.psl {
			continue
		}
		n++
		opts := Options{Standard: e.std, StrictKeywords: e.strict,
			PSLComments: true}
		for _, w := range []string{e.word, strings.ToUpper(e.word),
			mixed(e.word)} {
			src := "-- psl " + w + "\n"
			toks, _ := All([]byte(src), opts, VHDL)
			if len(toks) < 2 {
				t.Fatalf("%q: %d tokens", src, len(toks))
			}
			got := toks[0]
			if got.Kind != e.want || got.Start != 7 ||
				got.End != 7+len(w) || got.Flags&PSLComment == 0 {
				t.Errorf("%q std %v strict %v: got %v [%d,%d) flags "+
					"%v, want %v", src, e.std, e.strict, got.Kind,
					got.Start, got.End, got.Flags, e.want)
			}
		}
	}
	if n != len(token.Keywords())*4*2 {
		t.Fatalf("got %d PSL edges", n)
	}
}

// TestMultiLineBlockCommentLeads: a block comment that spans lines is
// not trailing trivia of the token before it; it leads the next token
// (where it can be a doc comment, #10). A one-line block comment after
// a token stays trailing.
func TestMultiLineBlockCommentLeads(t *testing.T) {
	for _, nl := range []string{"\n", "\r\n", "\r"} {
		src := "a; /* x" + nl + " y */" + nl + "b; /* z */" + nl + "c;"
		toks, l := All([]byte(src), Options{}, VHDL)
		for _, tr := range l.Trailing(toks[1]) {
			if tr.Kind == token.BlockComment {
				t.Errorf("%q: multi-line block comment trails ;", nl)
			}
		}
		var lead []token.Kind
		for _, tr := range l.Leading(toks[2]) {
			lead = append(lead, tr.Kind)
		}
		if !slices.Contains(lead, token.BlockComment) {
			t.Errorf("%q: leading of b = %v", nl, lead)
		}
		var trail []token.Kind
		for _, tr := range l.Trailing(toks[3]) {
			trail = append(trail, tr.Kind)
		}
		if !slices.Equal(trail, []token.Kind{token.Whitespace,
			token.BlockComment}) {
			t.Errorf("%q: trailing of second ; = %v", nl, trail)
		}
	}
}

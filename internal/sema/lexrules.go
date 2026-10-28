package sema

import (
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// lexRules checks the direct tokens of n for the lexical rules the
// lexer leaves to analysis (syntaxChecks calls it once per node).
func (a *analyzer) lexRules(n cst.Node) {
	for t := range n.Tokens() {
		if t.Kind() == token.IntegerLiteral && integerExponent(t) {
			a.semaError(t.Span(), "an integer literal cannot have a "+
				"negative exponent")
		}
		if t.Kind() == token.BitStringLiteral {
			if p := bitStringProblem(t.Text(), a.std); p != "" {
				a.semaError(t.Span(), p)
			}
		}
	}
}

// bitStringProblem returns what is wrong with the bit-string literal s
// under std, or "" (LRM 15.8):
//
//   - bit_value ::= graphic_character { [underscore] graphic_character
//     }: no underscore first, last or next to another;
//   - before VHDL-2008 every character is a digit of the base (B 0-1,
//     O 0-7, X 0-F); from VHDL-2008 a graphic character other than a
//     digit stands for itself, but a decimal digit is still one of the
//     base, and D takes decimal digits only;
//   - a signed base (SB, SO, SX) needs a value to extend;
//   - the length is a value of NATURAL, the index subtype of every
//     predefined one-dimensional array type;
//   - VHDL-2008 drops the replacement delimiter % (LRM-2002 13.10).
func bitStringProblem(s string, std token.Standard) string {
	q := strings.IndexAny(s, `"%`)
	if q < 0 || len(s) < q+2 {
		return ""
	}
	if s[q] == '%' && std >= token.Std2008 {
		return "the replacement delimiter % is not allowed from VHDL-2008"
	}
	prefix := lowerASCII(s[:q])
	val := s[q+1 : len(s)-1]
	i := 0
	for i < len(prefix) && prefix[i] >= '0' && prefix[i] <= '9' {
		i++
	}
	if i > 0 {
		if n, err := strconv.ParseInt(prefix[:i], 10, 64); err != nil ||
			n > 1<<31-1 {
			return "the length of the bit string is more than " +
				"NATURAL'HIGH"
		}
	}
	base := prefix[i:]
	switch {
	case strings.HasPrefix(val, "_"):
		return "a bit string cannot start with an underscore"
	case strings.HasSuffix(val, "_"):
		return "a bit string cannot end with an underscore"
	case strings.Contains(val, "__"):
		return "a bit string cannot hold two underscores in a row"
	}
	if strings.HasPrefix(base, "s") && val == "" {
		return "a signed bit string cannot be empty"
	}
	digits := map[string]string{"b": "01", "o": "01234567",
		"x": "0123456789abcdef", "d": "0123456789"}[strings.TrimLeft(
		base, "us")]
	if digits == "" {
		return ""
	}
	for _, c := range lowerASCII(strings.ReplaceAll(val, "_", "")) {
		if strings.ContainsRune(digits, c) {
			continue
		}
		digit := c >= '0' && c <= '9'
		if std < token.Std2008 || digit || base == "d" {
			return "invalid digit " + strconv.QuoteRune(c) + " in a " +
				"bit string of base " + strings.ToUpper(base)
		}
	}
	return ""
}

// directiveRules reports the tool directives of f its standard does
// not have (LRM-2019 16.10, 24.1): before VHDL-2008 a grave accent is
// no lexical element outside comments and strings; VHDL-2008 has only
// the protect directives. The conditional analysis directives (`if,
// `else, ...) the parser reports itself.
func directiveRules(f *File, std token.Standard) []diag.Diagnostic {
	if f.Bundled || f.Parse == nil || std >= token.Std2019 {
		return nil
	}
	var out []diag.Diagnostic
	check := func(text string, start int) {
		name := strings.TrimPrefix(text, "`")
		end := strings.IndexFunc(name, func(r rune) bool {
			return !(r == '_' || r >= 'a' && r <= 'z' ||
				r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
		})
		if end >= 0 {
			name = name[:end]
		}
		switch lowerASCII(name) {
		case "if", "elsif", "else", "end", "error", "warning":
			return
		case "protect":
			if std >= token.Std2008 {
				return
			}
		}
		out = append(out, at(f, cst.Range{Start: start,
			End: start + 1}, diag.New(diag.SemaError, "the grave "+
			"accent ` starts a tool directive only from VHDL-2019")))
	}
	for t := range f.Root().DescendantTokens() {
		lead := t.Leading()
		off := t.Span().Start
		for i := len(lead) - 1; i >= 0; i-- {
			off -= len(lead[i].Text)
			if lead[i].Kind == token.Directive {
				check(lead[i].Text, off)
			}
		}
		off = t.Span().End
		for _, tr := range t.Trailing() {
			if tr.Kind == token.Directive {
				check(tr.Text, off)
			}
			off += len(tr.Text)
		}
	}
	return out
}

package sema

import (
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// Key is a normalized designator, the identity names are compared by
// (LRM 15.4): basic identifiers in lower case (ASCII letters only, so
// the result never depends on Unicode tables), extended identifiers
// exactly as written with their backslashes, operator symbols as a
// quoted lower-case string ("\"and\""), character literals as written
// ("'a'").
type Key string

// lowerASCII lowers the ASCII letters of s.
func lowerASCII(s string) string {
	for i := range len(s) {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if c := b[j]; c >= 'A' && c <= 'Z' {
					b[j] = c + 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// KeyOf returns the key of an identifier, operator symbol or character
// literal spelled text with token kind k.
func KeyOf(k token.Kind, text string) Key {
	switch k {
	case token.ExtendedIdentifier, token.CharacterLiteral:
		return Key(text)
	case token.StringLiteral:
		// An operator symbol: "AND" and "and" are the same operator.
		return Key(lowerASCII(text))
	}
	return Key(lowerASCII(text))
}

// TokenKey returns the key of a name token; keywords used as names
// ("all", "range") give their lower-case spelling.
func TokenKey(t cst.Token) Key {
	if t.IsZero() || t.Missing() {
		return ""
	}
	return KeyOf(t.Kind(), t.Text())
}

// IdentKey returns the key of a plain identifier string such as a
// library name from gohard.yaml.
func IdentKey(s string) Key {
	if strings.HasPrefix(s, `\`) {
		return Key(s)
	}
	return Key(lowerASCII(s))
}

// OperatorKey returns the key of the operator symbol op ("+", "and").
func OperatorKey(op string) Key { return Key(`"` + lowerASCII(op) + `"`) }

// IsOperator reports whether k is an operator symbol key.
func (k Key) IsOperator() bool { return strings.HasPrefix(string(k), `"`) }

// IsCharacter reports whether k is a character literal key.
func (k Key) IsCharacter() bool { return strings.HasPrefix(string(k), "'") }

// Upper returns k in the upper-case form used in messages for basic
// identifiers (extended identifiers and literals unchanged).
func (k Key) Upper() string {
	s := string(k)
	if strings.HasPrefix(s, `\`) || k.IsCharacter() {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 'a' + 'A'
		}
	}
	return string(b)
}

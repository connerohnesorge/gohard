// Package token defines the token kinds of VHDL 1993-2019 with embedded
// PSL, the VHDL standards, and the reserved-word table (#31, data from
// #5 and #6).
//
// The kinds are generated from internal/cst/kinds.yaml (ordinary tokens,
// numbered from 1) and internal/token/keywords.yaml (keywords Kw<Word>,
// numbered from 256) by tools/genkinds; numbers are only appended. The
// reserved-word table is data: Keyword.Reserved is the only place the
// reservation rules are applied.
package token

import (
	"math/bits"
	"strconv"
)

// Kind is a token kind. The zero Kind is not a valid kind.
type Kind uint16

// FirstKeyword is the kind number of the first keyword.
const FirstKeyword Kind = 256

// Standard is a VHDL standard revision, ordered by year. There is no
// separate 2000 mode (#7).
type Standard uint16

// Standards.
const (
	Std1993 Standard = 1993
	Std2002 Standard = 2002
	Std2008 Standard = 2008
	Std2019 Standard = 2019
)

// Standards lists every supported standard in order.
var Standards = []Standard{Std1993, Std2002, Std2008, Std2019}

// String returns the year, for example "2008".
func (s Standard) String() string { return strconv.Itoa(int(s)) }

// ParseStandard parses "1993", "2002", "2008" or "2019" (also the short
// forms "93", "02", "08", "19"). "2000" and "00" give 2002, as in NVC.
func ParseStandard(s string) (Standard, bool) {
	switch s {
	case "1993", "93":
		return Std1993, true
	case "2000", "00", "2002", "02":
		return Std2002, true
	case "2008", "08":
		return Std2008, true
	case "2019", "19":
		return Std2019, true
	}
	return 0, false
}

type info struct {
	name  string
	text  string
	std   Standard
	flags uint8
}

const (
	flagTrivia = 1 << iota
	flagPSL
)

// String returns the kind's name from kinds.yaml or keywords.yaml, for
// example "Semicolon" or "KwEntity".
func (k Kind) String() string {
	if k > 0 && k < numTokens {
		return tokenInfo[k].name
	}
	if kw, ok := k.Keyword(); ok {
		return keywordName(kw.Word)
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Text returns the fixed spelling of k (lower case for keywords), or ""
// when the kind has no fixed spelling (identifiers, literals, trivia).
func (k Kind) Text() string {
	if k > 0 && k < numTokens {
		return tokenInfo[k].text
	}
	if kw, ok := k.Keyword(); ok {
		return kw.Word
	}
	return ""
}

// Valid reports whether k is a defined kind.
func (k Kind) Valid() bool {
	_, isKw := k.Keyword()
	return (k > 0 && k < numTokens) || isKw
}

// IsKeyword reports whether k is a keyword kind.
func (k Kind) IsKeyword() bool {
	_, ok := k.Keyword()
	return ok
}

// Keyword returns the table entry of a keyword kind.
func (k Kind) Keyword() (Keyword, bool) {
	if k < FirstKeyword || k >= FirstKeyword+numKeywords {
		return Keyword{}, false
	}
	return keywordTable[k-FirstKeyword], true
}

// IsTrivia reports whether k is a trivia kind.
func (k Kind) IsTrivia() bool {
	return k > 0 && k < numTokens && tokenInfo[k].flags&flagTrivia != 0
}

// IsPSLOnly reports whether k is scanned only in the PSL lexer modes.
func (k Kind) IsPSLOnly() bool {
	return k > 0 && k < numTokens && tokenInfo[k].flags&flagPSL != 0
}

// Std returns the first standard with this kind: for ordinary tokens the
// kinds.yaml std, for keywords the table's first standard.
func (k Kind) Std() Standard {
	if k > 0 && k < numTokens {
		return tokenInfo[k].std
	}
	if kw, ok := k.Keyword(); ok {
		return kw.First
	}
	return 0
}

// Kinds returns every defined kind in number order.
func Kinds() []Kind {
	out := make([]Kind, 0, numTokens-1+numKeywords)
	for k := Kind(1); k < numTokens; k++ {
		out = append(out, k)
	}
	for i := range Kind(numKeywords) {
		out = append(out, FirstKeyword+i)
	}
	return out
}

func keywordName(word string) string {
	b := []byte("Kw")
	up := true
	for i := 0; i < len(word); i++ {
		c := word[i]
		if c == '_' {
			up = true
			continue
		}
		if up && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		up = false
		b = append(b, c)
	}
	return string(b)
}

// Set is a set of token kinds.
type Set struct {
	bits [(FirstKeyword + 512) / 64]uint64
}

// NewSet returns the set of the given kinds.
func NewSet(ks ...Kind) Set {
	var s Set
	for _, k := range ks {
		s = s.With(k)
	}
	return s
}

// With returns s plus k.
func (s Set) With(k Kind) Set {
	if int(k) < len(s.bits)*64 {
		s.bits[k/64] |= 1 << (k % 64)
	}
	return s
}

// Union returns the union of s and t.
func (s Set) Union(t Set) Set {
	for i := range s.bits {
		s.bits[i] |= t.bits[i]
	}
	return s
}

// Has reports whether k is in s.
func (s Set) Has(k Kind) bool {
	return int(k) < len(s.bits)*64 && s.bits[k/64]&(1<<(k%64)) != 0
}

// Len returns the number of kinds in s.
func (s Set) Len() int {
	n := 0
	for _, w := range s.bits {
		n += bits.OnesCount64(w)
	}
	return n
}

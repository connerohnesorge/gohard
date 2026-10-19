package token

// Class is the reservation class of a keyword (#6).
type Class uint8

// Reservation classes.
const (
	// ClassVHDL words are reserved from their first standard on, in
	// VHDL and PSL code alike.
	ClassVHDL Class = iota + 1
	// ClassPSL2008 words are the eleven PSL-only 2008 words: reserved
	// inside PSL; outside PSL identifiers with a portability hint (NVC
	// behaviour), or reserved everywhere with psl.strict_keywords (GHDL
	// behaviour), #11 entry 3.
	ClassPSL2008
	// ClassPSL words are reserved only inside PSL.
	ClassPSL
	// ClassNever words are never reserved (`inherit`); they have a kind
	// so the parser can match them by spelling.
	ClassNever
)

// String returns the keywords.yaml name of the class.
func (c Class) String() string {
	switch c {
	case ClassVHDL:
		return "vhdl"
	case ClassPSL2008:
		return "psl2008"
	case ClassPSL:
		return "psl"
	case ClassNever:
		return "never"
	}
	return "unknown"
}

// Suffix is a set of PSL suffix forms a word takes.
type Suffix uint8

// PSL suffix forms.
const (
	SuffixBang           Suffix = 1 << iota // word!
	SuffixUnderscore                        // word_
	SuffixBangUnderscore                    // word!_
)

// Keyword is one row of the reserved-word table.
type Keyword struct {
	Word  string // lower case
	Kind  Kind
	Class Class
	// First is the first standard that reserves the word (VHDL classes)
	// or whose embedded PSL defines it.
	First Standard
	// Removed is the first standard that no longer reserves the word,
	// or 0 (no word has been removed so far).
	Removed Standard
	// PSL reports whether the word has a PSL meaning. A ClassVHDL word
	// with PSL set is reserved in PSL code under every standard.
	PSL      bool
	Suffixes Suffix
	Context  string
}

// Reserved reports whether the word is a keyword under std, inside PSL
// code (psl) or outside it, with psl.strict_keywords set or not. This is
// the single implementation of the reservation rules of #6 and #11
// entry 3.
func (k Keyword) Reserved(std Standard, psl, strict bool) bool {
	switch k.Class {
	case ClassVHDL:
		in := std >= k.First && (k.Removed == 0 || std < k.Removed)
		return in || (psl && k.PSL)
	case ClassPSL2008:
		return psl || (strict && std >= Std2008)
	case ClassPSL:
		return psl
	}
	return false
}

// Gated reports whether the word is a ClassVHDL keyword that std does
// not reserve yet: the parser may still treat it as the keyword where
// the grammar needs it, reporting syntax/standard (#31).
func (k Keyword) Gated(std Standard) bool {
	return k.Class == ClassVHDL && std < k.First
}

// Keywords returns a copy of the reserved-word table in kind order.
func Keywords() []Keyword {
	out := make([]Keyword, len(keywordTable))
	copy(out, keywordTable[:])
	return out
}

// LookupWord returns the keyword kind spelled by the lower-case word, or
// 0. Callers lower-case ASCII letters first; no keyword has other
// letters.
func LookupWord(lower string) Kind {
	return keywordByWord[lower]
}

// Lookup returns the kind that the word w (any case) lexes as: its
// keyword kind when Reserved under (std, psl, strict), else Identifier.
// word is the keyword w spells whatever the context, or 0.
func Lookup(w []byte, std Standard, psl, strict bool) (kind, word Kind) {
	const maxWord = 24
	if len(w) > maxWord {
		return Identifier, 0
	}
	var buf [maxWord]byte
	for i, c := range w {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		buf[i] = c
	}
	word = keywordByWord[string(buf[:len(w)])]
	if word == 0 {
		return Identifier, 0
	}
	if keywordTable[word-FirstKeyword].Reserved(std, psl, strict) {
		return word, word
	}
	return Identifier, word
}

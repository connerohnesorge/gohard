package token

import (
	"strings"
	"testing"
)

func TestStandards(t *testing.T) {
	for _, s := range Standards {
		got, ok := ParseStandard(s.String())
		if !ok || got != s {
			t.Errorf("%v does not round-trip", s)
		}
		short, ok := ParseStandard(s.String()[2:])
		if !ok || short != s {
			t.Errorf("short form of %v", s)
		}
	}
	if s, ok := ParseStandard("2000"); !ok || s != Std2002 {
		t.Errorf("2000 = %v", s)
	}
	for _, bad := range []string{"", "1987", "2009", "x"} {
		if _, ok := ParseStandard(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestKinds: every kind has a unique name, keyword kinds spell their
// word, trivia and PSL flags are where kinds.yaml puts them.
func TestKinds(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Kinds() {
		n := k.String()
		if seen[n] || !k.Valid() || k.Std() == 0 {
			t.Errorf("kind %d %q", k, n)
		}
		seen[n] = true
		if k.IsKeyword() {
			kw, _ := k.Keyword()
			if k.Text() != kw.Word || LookupWord(kw.Word) != k ||
				!strings.HasPrefix(n, "Kw") {
				t.Errorf("keyword %v", k)
			}
		}
	}
	for _, k := range []Kind{Whitespace, Newline, LineComment,
		BlockComment, Directive, ProtectedData, PSLCommentMarker} {
		if !k.IsTrivia() {
			t.Errorf("%v is not trivia", k)
		}
	}
	if Identifier.IsTrivia() || !PSLImplies.IsPSLOnly() ||
		Arrow.IsPSLOnly() {
		t.Error("flags")
	}
	if Kind(0).Valid() || Kind(FirstKeyword-1).Valid() ||
		Kind(FirstKeyword+numKeywords).Valid() {
		t.Error("out-of-range kinds are valid")
	}
	if Kind(9999).String() != "Kind(9999)" {
		t.Error(Kind(9999).String())
	}
}

func TestSet(t *testing.T) {
	s := NewSet(EOF, KwXor, Semicolon)
	if !s.Has(EOF) || !s.Has(KwXor) || s.Has(KwAbs) || s.Len() != 3 {
		t.Error("NewSet")
	}
	u := s.Union(NewSet(KwAbs))
	if !u.Has(KwAbs) || u.Len() != 4 || s.Len() != 3 {
		t.Error("Union")
	}
	if NewSet(Kind(60000)).Len() != 0 {
		t.Error("out-of-range kind added")
	}
}

func TestLookup(t *testing.T) {
	if k, w := Lookup([]byte("ENTITY"), Std1993, false, false); k !=
		KwEntity || w != KwEntity {
		t.Errorf("ENTITY: %v %v", k, w)
	}
	if k, w := Lookup([]byte("View"), Std2008, false, false); k !=
		Identifier || w != KwView {
		t.Errorf("view under 2008: %v %v", k, w)
	}
	long := []byte(strings.Repeat("a", 100))
	if k, w := Lookup(long, Std2019, true, true); k != Identifier || w != 0 {
		t.Errorf("long word: %v %v", k, w)
	}
	kw, _ := KwContext.Keyword()
	if !kw.Gated(Std2002) || kw.Gated(Std2008) {
		t.Error("Gated")
	}
}

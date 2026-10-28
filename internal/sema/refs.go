package sema

import (
	"cmp"
	"slices"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
)

// RefKind says how a name occurrence uses its declaration.
type RefKind uint8

// Reference kinds.
const (
	RefRead   RefKind = iota // an ordinary use
	RefWrite                 // the target of an assignment
	RefCall                  // a subprogram call or operator
	RefType                  // a type mark
	RefEnd                   // the designator of an end clause
	RefFormal                // a formal in a named association
	RefSelect                // a prefix of a selected name or use clause
	// RefBody is the name of a body or full declaration, naming the
	// declaration it completes.
	RefBody
	RefLabel // a label in exit, next or a configuration
)

// Ref is one occurrence of a name in the source and what it denotes.
// Every name occurrence sema resolves is recorded, so the language
// server answers definition, references, hover and rename from Refs
// alone (#25).
type Ref struct {
	File *File
	// Span is the occurrence's name token (an operator symbol, a
	// character literal or an identifier).
	Span cst.Range
	// Decl is what the name denotes; nil when only the overload
	// candidates are known (see Overload).
	Decl     *Decl
	Overload *Overload
	Kind     RefKind
}

// compareRefs orders refs by position, then kind.
func compareRefs(a, b Ref) int {
	return cmp.Or(
		cmp.Compare(a.Span.Start, b.Span.Start),
		cmp.Compare(a.Span.End, b.Span.End),
		cmp.Compare(a.Kind, b.Kind),
	)
}

// sortRefs sorts refs by position and drops exact repeats (a name
// resolved twice records one occurrence).
func sortRefs(rs []Ref) []Ref {
	slices.SortStableFunc(rs, compareRefs)
	return slices.CompactFunc(rs, func(a, b Ref) bool {
		return a.Span == b.Span && a.Decl == b.Decl
	})
}

// refAt returns the ref whose span contains off in the sorted refs rs.
// An offset at the end of a name still finds it (the cursor after the
// last letter).
func refAt(rs []Ref, off int) (Ref, bool) {
	i, _ := slices.BinarySearchFunc(rs, off, func(r Ref, o int) int {
		return cmp.Compare(r.Span.Start, o)
	})
	for _, j := range []int{i, i - 1} {
		if j >= 0 && j < len(rs) {
			r := rs[j]
			if r.Span.Start <= off && off <= r.Span.End {
				return r, true
			}
		}
	}
	return Ref{}, false
}

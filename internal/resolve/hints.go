package resolve

import (
	"cmp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

// Inlay hints (#56). Each kind has its own inlay_hints.* setting; later
// kinds (inferred widths #97, waveform-cursor values #76) add a field
// to HintKinds and a pass to Hints.

// HintKinds selects the kinds of inlay hints (the inlay_hints.* keys).
type HintKinds struct {
	// ParameterNames: the formal before each positional actual of a
	// call, generic map or port map.
	ParameterNames bool
	// Types: the type of an object alias without a subtype indication
	// and of a loop parameter.
	Types bool
	// Values: the static value of a constant or a generic default.
	Values bool
}

// HintKind is what a hint shows.
type HintKind uint8

// Hint kinds.
const (
	HintParameter HintKind = iota + 1
	HintType
	HintValue
)

// Hint is one inlay hint: Label shown at the byte offset Off, which is
// always the start or the end of a token: a parameter name goes before
// the actual's first token, a type after the name, a value after the
// value's last token.
type Hint struct {
	Off   int
	Label string
	Kind  HintKind
}

// Hint label limits: a value image longer than maxValueLen bytes is
// cut, and a composite value of more than maxValueElems scalars gets
// no hint (its image would be too long to read).
const (
	maxValueLen   = 40
	maxValueElems = 1024
)

// valueSteps is the step budget the value hints of one request share
// (P7): however many constants a file has, a request evaluates at most
// as much as one static evaluation of the editor (eval.EditorSteps).
const valueSteps = eval.EditorSteps

// Hints returns the inlay hints of the kinds k in the file at path
// whose offset lies in [start, end], sorted by offset, kind and label.
// They are a pure function of the result and never depend on the range
// asked for. Values come from static evaluation with a fresh evaluator
// per declaration, in source order, each within the editor's step
// limit and what is left of the request's budget (valueHints).
func Hints(r *sema.Result, path string, start, end int,
	k HintKinds,
) []Hint {
	f, ok := r.File(path)
	if !ok || !k.ParameterNames && !k.Types && !k.Values {
		return nil
	}
	var out []Hint
	in := func(off int) bool { return off >= start && off <= end }
	add := func(off int, label string, kind HintKind) {
		if in(off) && label != "" {
			out = append(out, Hint{Off: off, Label: label, Kind: kind})
		}
	}
	if k.ParameterNames {
		parameterHints(f, add)
	}
	if k.Types {
		for _, u := range f.Units {
			if u.Opaque {
				continue
			}
			for _, d := range u.Decls {
				if d.File != f || d.Implicit || d.Node.IsZero() ||
					!in(d.Span.End) {
					continue
				}
				if t, ok := typeHint(d); ok {
					add(d.Span.End, ": "+t, HintType)
				}
			}
		}
	}
	if k.Values {
		valueHints(r, f, end, add)
	}
	slices.SortFunc(out, func(a, b Hint) int {
		return cmp.Or(cmp.Compare(a.Off, b.Off),
			cmp.Compare(a.Kind, b.Kind), strings.Compare(a.Label, b.Label))
	})
	return slices.Compact(out)
}

// parameterHints adds the formal name before each positional actual
// of the file's calls and maps. An actual that is the formal's own
// name, and a formal without a name (a predefined unary operation),
// get none.
func parameterHints(f *sema.File, add func(int, string, HintKind)) {
	hint := func(it cst.AssociationElement, formal *sema.Decl) {
		if formal == nil || formal.Name == "anonymous" ||
			!it.Formal().IsZero() {
			return
		}
		act := it.Actual()
		if act.IsZero() || act.Span().End <= act.Span().Start {
			return
		}
		if act.Kind() == cst.KindSimpleName &&
			sema.TokenKey(cst.SimpleName{Node: act.Node}.Ident()) ==
				formal.Name {
			return
		}
		add(act.Span().Start, formal.Text+" =>", HintParameter)
	}
	// Positional actuals come first (LRM 6.5.7.1): the i-th one
	// associates the i-th parameter of the subprogram the call resolves
	// to.
	eachCall(f, func(c call) {
		if c.list.IsZero() || c.ref.Decl == nil {
			return
		}
		params := c.ref.Decl.Denotes().Params
		i := 0
		for it := range c.list.Items() {
			if !it.Formal().IsZero() || i >= len(params) {
				return
			}
			hint(it, params[i])
			i++
		}
	})
	for _, u := range f.Units {
		if u.Opaque {
			continue
		}
		eachMap(u, func(m assocMap) {
			for _, as := range m.assocs {
				if it, ok := cst.As[cst.AssociationElement](as.Node); ok {
					hint(it, as.Formal)
				}
			}
		})
	}
}

// typeHint returns the type shown after the name of an object alias
// declared without a subtype indication, or of a loop parameter.
func typeHint(d *sema.Decl) (string, bool) {
	if d.Type == nil || d.Type.Unknown() {
		return "", false
	}
	switch d.Kind {
	case sema.DeclLoopParam:
		return d.Type.Name(), true
	case sema.DeclAlias:
		// An object alias holds the object name as its Value.
		a, ok := cst.As[cst.AliasDecl](d.Node)
		if !ok || !a.Subtype().IsZero() || !a.Signature().IsZero() ||
			d.Value == nil || d.View != nil {
			return "", false
		}
		return d.Type.Name(), true
	}
	return "", false
}

// valueHints adds the static value of each constant and generic
// default of the file f whose value ends at or before the offset end,
// when static evaluation succeeds. A value written as a literal gets
// none: it is already there. The values are evaluated in source order
// from the start of the file, each with the editor's step limit or
// what is left of valueSteps if that is less, so whether a value gets
// a hint depends on the values before it and never on the range asked
// for; once the budget is spent, the rest get none. It returns the
// steps spent.
func valueHints(r *sema.Result, f *sema.File, end int,
	add func(int, string, HintKind),
) int64 {
	var ds []*sema.Decl
	for _, u := range f.Units {
		if u.Opaque {
			continue
		}
		for _, d := range u.Decls {
			if d.File == f && !d.Implicit && !d.Node.IsZero() &&
				(d.Kind == sema.DeclConstant || d.Kind == sema.DeclGeneric) &&
				d.Value != nil && !d.Value.Node.IsZero() &&
				!literal(d.Value) {
				ds = append(ds, d)
			}
		}
	}
	slices.SortStableFunc(ds, func(a, b *sema.Decl) int {
		return cmp.Compare(a.Value.Node.Span().End, b.Value.Node.Span().End)
	})
	var spent int64
	for _, d := range ds {
		at := d.Value.Node.Span().End
		if at > end || spent >= valueSteps {
			break
		}
		limit := min(eval.EditorSteps, valueSteps-spent)
		ev := sema.NewEvaluator(r, limit)
		var v eval.Value
		var err error
		if d.Kind == sema.DeclConstant {
			v, err = ev.Constant(d)
		} else {
			v, err = ev.Value(d.Value)
		}
		spent += min(ev.Used(), limit)
		if err != nil {
			continue
		}
		if s, ok := image(v); ok {
			add(at, "= "+s, HintValue)
		}
	}
	return spent
}

// literal reports whether e is written as a literal value: a numeric,
// string, bit-string or character literal, a physical literal or an
// enumeration literal, possibly negated.
func literal(e *sema.Expr) bool {
	switch e.Kind {
	case sema.ExprLiteral, sema.ExprPhysical:
		return true
	case sema.ExprName:
		return e.Decl != nil && e.Decl.Kind == sema.DeclEnumLiteral
	}
	if u, ok := cst.As[cst.UnaryExpr](e.Node); ok &&
		u.Op().Text() == "-" {
		switch u.Operand().Kind() {
		case cst.KindLiteralExpr, cst.KindPhysicalLiteral:
			return true
		}
	}
	return false
}

// image is the value as eval.Value.Image writes it (a string or a
// bit string in quotes, other composites as aggregates), cut to
// maxValueLen bytes; ok is false for a composite value of more than
// maxValueElems scalars.
func image(v eval.Value) (string, bool) {
	n := 0
	var count func(v eval.Value) bool
	count = func(v eval.Value) bool {
		n++
		if n > maxValueElems {
			return false
		}
		for _, e := range v.E {
			if !count(e) {
				return false
			}
		}
		return true
	}
	if !count(v) {
		return "", false
	}
	s := strings.ToValidUTF8(v.Image(), "?")
	if len(s) > maxValueLen {
		cut := maxValueLen
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s, true
}

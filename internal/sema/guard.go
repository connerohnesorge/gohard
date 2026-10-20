package sema

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/token"
)

// guard.go checks the targets of signal and variable assignments
// against the rules for guarded signals and aggregate targets:
//
//   - a null waveform element needs a guarded target (LRM 10.5.2.2);
//   - an unguarded concurrent signal assignment may not drive a
//     guarded target, and an aggregate target is either all guarded
//     or all unguarded (LRM 11.6);
//   - an aggregate target identifies each object or subelement once
//     (LRM 10.5.2.1, 10.6.2.1).
//
// It reads the syntax tree and the unit's name references only.

// targetChecks reports the target errors of unit u's statements.
func targetChecks(u *Unit) []diag.Diagnostic {
	if u.Node.IsZero() || u.File == nil {
		return nil
	}
	var refs map[int]*Decl
	declAt := func(off int) *Decl {
		if refs == nil {
			refs = map[int]*Decl{}
			for _, r := range u.Refs {
				if r.Decl != nil && r.File == u.File {
					refs[r.Span.Start] = r.Decl
				}
			}
		}
		return refs[off]
	}
	var out []diag.Diagnostic
	report := func(n cst.Node, msg string) {
		out = append(out, at(u.File, n.Span(),
			diag.New(diag.SemaError, msg)))
	}
	for n := range u.Node.Descendants() {
		if n.Inactive() {
			continue
		}
		conc := false
		switch n.Kind() {
		case cst.KindConcurrentSimpleSignalAssign,
			cst.KindConcurrentConditionalSignalAssign,
			cst.KindConcurrentSelectedSignalAssign:
			conc = true
		case cst.KindSignalAssignStmt, cst.KindConditionalSignalAssignStmt,
			cst.KindSelectedSignalAssignStmt, cst.KindVariableAssignStmt:
		default:
			continue
		}
		tn := n.Child[cst.Target](0).Target().Node
		if tn.IsZero() {
			continue
		}
		elems := []cst.Node{tn}
		if tn.Kind() == cst.KindAggregate {
			elems = elems[:0]
			for as := range (cst.Aggregate{Node: tn}).Associations() {
				elems = append(elems, as.Value().Node)
			}
			if dup, ok := duplicateElem(elems, declAt); ok {
				report(dup, "the aggregate target identifies "+
					u.File.text(dup)+" more than once")
			}
		}
		if n.Kind() == cst.KindVariableAssignStmt {
			continue
		}
		guarded, unguarded := 0, 0
		for _, e := range elems {
			switch d := targetSignal(e, declAt); {
			case d == nil:
				unguarded = -len(elems) // unknown: no claim
			case guardedSignal(d):
				guarded++
			default:
				unguarded++
			}
		}
		known := unguarded >= 0
		switch {
		case !known:
		case conc && guarded > 0 && unguarded > 0:
			report(tn, "an aggregate target must be all guarded or "+
				"all unguarded signals")
		case conc && guarded > 0 && n.Token(token.KwGuarded, 0).IsZero():
			report(tn, "guarded signal "+u.File.text(tn)+" cannot be the "+
				"target of an unguarded concurrent signal assignment")
		}
		if known && guarded == 0 {
			if el := nullElement(n); !el.IsZero() {
				report(el, "a null waveform element needs a guarded "+
					"signal as its target")
			}
		}
	}
	return out
}

// nullElement returns the first null waveform element of assignment
// statement n.
func nullElement(n cst.Node) cst.Node {
	for d := range n.Descendants() {
		if d.Kind() != cst.KindWaveformElement || d.Inactive() {
			continue
		}
		v := (cst.WaveformElement{Node: d}).Value().Node
		if v.IsZero() {
			continue
		}
		f := v.FirstToken()
		if f.Kind() == token.KwNull && f.Span() == v.LastToken().Span() {
			return v
		}
	}
	return cst.Node{}
}

// targetSignal returns the object a name target e (a signal name with
// static, literal indices) denotes, or nil when that is not known.
func targetSignal(e cst.Node, declAt func(int) *Decl) *Decl {
	if !(cst.Name{}).Accepts(e.Kind()) {
		return nil
	}
	f := e.FirstToken()
	if f.Kind() != token.Identifier {
		return nil
	}
	d := declAt(f.Span().Start)
	if d == nil || d.Kind == DeclAlias {
		return nil
	}
	switch d.Kind {
	case DeclSignal, DeclPort:
		return d
	}
	return nil
}

// guardedSignal reports whether d is a guarded signal: a signal or
// signal port declared with the signal kind bus or register (LRM
// 6.4.2.3, 6.5.2).
func guardedSignal(d *Decl) bool {
	n := d.Node
	for range 4 {
		if n.IsZero() {
			return false
		}
		switch n.Kind() {
		case cst.KindSignalDecl, cst.KindInterfaceObjectDecl:
			return !n.Token(token.KwBus, 0).IsZero() ||
				!n.Token(token.KwRegister, 0).IsZero()
		}
		n = n.Parent()
	}
	return false
}

// duplicateElem returns the second element of an aggregate target
// that names the same object or subelement as an earlier one: a simple
// name, or a name whose suffixes are literals, written the same way
// up to case and spacing.
func duplicateElem(elems []cst.Node, declAt func(int) *Decl,
) (cst.Node, bool) {
	seen := map[elemID]bool{}
	for _, e := range elems {
		k, ok := elemKey(e, declAt)
		if !ok {
			continue
		}
		if seen[k] {
			return e, true
		}
		seen[k] = true
	}
	return cst.Node{}, false
}

type elemID struct {
	d    *Decl
	rest string
}

// elemKey identifies a target element written as an object name
// followed by indices or selections that are literals or element
// names.
func elemKey(e cst.Node, declAt func(int) *Decl) (elemID, bool) {
	if !(cst.Name{}).Accepts(e.Kind()) {
		return elemID{}, false
	}
	first := true
	var id elemID
	var b strings.Builder
	for tok := range e.DescendantTokens() {
		if first {
			first = false
			d := declAt(tok.Span().Start)
			if tok.Kind() != token.Identifier || d == nil ||
				d.Kind == DeclAlias || objectClass(d) == "" {
				return elemID{}, false
			}
			id.d = d
			continue
		}
		switch tok.Kind() {
		case token.IntegerLiteral, token.CharacterLiteral,
			token.LParen, token.RParen, token.Comma, token.Dot:
		case token.Identifier:
			// Only a record element name after a dot is static text.
			if d := declAt(tok.Span().Start); d == nil ||
				d.Kind != DeclElement {
				return elemID{}, false
			}
		default:
			return elemID{}, false
		}
		t := tok.Text()
		if tok.Kind() == token.Identifier {
			t = strings.ToLower(t)
		}
		b.WriteString(t)
		b.WriteByte(' ')
	}
	id.rest = b.String()
	return id, true
}

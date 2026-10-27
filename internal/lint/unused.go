package lint

import (
	"cmp"
	"slices"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// use is how a signal is used anywhere in the design.
type use struct{ read, written bool }

// unusedSignals reports lint/unused-signal: a signal of an architecture
// that is never read or never assigned, an in port never read, an out
// port never assigned, and an inout or buffer port never used.
//
// A name in a port map or a procedure call's arguments counts as both
// a read and a write: the formal's mode is not consulted, so a finding
// is never caused by an association. A use through an alias is a use
// of its object; the alias declaration itself uses nothing.
// shortcut: package signals are not checked, add them once the whole
// design's readers are known to be in the analysis.
func unusedSignals(res *sema.Result, m *meter, sev diag.Severity,
) []finding {
	bad := errorFiles(res)
	uses := map[*sema.Decl]*use{}
	type name struct{ lib, entity sema.Key }
	archs := map[name][]*sema.Unit{}
	for _, u := range res.Units {
		if u.Kind == sema.UnitArchitecture {
			k := name{u.Library, u.Of}
			archs[k] = append(archs[k], u)
		}
		var root cst.Node
		aliases := aliasSpans(u)
		m.spend(u, len(u.Decls))
		for _, r := range u.Refs {
			if !m.spend(u, 1) {
				break
			}
			d := object(r.Decl)
			if d == nil || d.Kind != sema.DeclSignal &&
				d.Kind != sema.DeclPort {
				continue
			}
			for len(aliases) > 0 && aliases[0].End <= r.Span.Start {
				aliases = aliases[1:]
			}
			if len(aliases) > 0 && aliases[0].Start <= r.Span.Start {
				continue // the name an alias declaration aliases
			}
			x := uses[d]
			if x == nil {
				x = &use{}
				uses[d] = x
			}
			if root.IsZero() {
				root = r.File.Root()
			}
			switch r.Kind {
			case sema.RefWrite:
				x.written = true
			case sema.RefSelect: // the prefix of a selected name
				if targeted(root, r.Span) {
					x.written = true
					continue
				}
				fallthrough
			case sema.RefRead:
				x.read = true
				if associated(root, r.Span) {
					x.written = true
				}
			}
		}
	}
	var out []finding
	var u *sema.Unit
	report := func(d *sema.Decl, what, how string) {
		f := diag.New(diag.LintUnusedSignal, what, d.Text, how)
		f.Severity = sev
		out = append(out, finding{u, at(d.File, d.Span, f)})
	}
	for _, u = range res.Units {
		if !trusted(u, bad) || m.over(u) {
			continue
		}
		for _, d := range u.Decls {
			if !m.spend(u, 1) {
				break
			}
			x := uses[d]
			if x == nil {
				x = &use{}
			}
			switch {
			case d.Kind == sema.DeclSignal &&
				u.Kind == sema.UnitArchitecture:
				switch {
				case !x.read && !x.written:
					report(d, "signal", "used")
				case !x.read:
					report(d, "signal", "read")
				case !x.written:
					report(d, "signal", "assigned")
				}
			case d.Kind == sema.DeclPort && d.Class == sema.ClassSignal &&
				u.Kind == sema.UnitEntity && d.Parent == u.Region:
				as := archs[name{u.Library, u.Name}]
				if len(as) == 0 {
					continue // no body to read or drive it
				}
				ok := true
				for _, a := range as {
					ok = ok && trusted(a, bad) && !m.over(a)
				}
				if !ok {
					continue
				}
				switch d.Mode {
				case sema.ModeNone, sema.ModeIn:
					if !x.read {
						report(d, "port", "read")
					}
				case sema.ModeOut:
					if !x.written {
						report(d, "port", "assigned")
					}
				case sema.ModeInout, sema.ModeBuffer:
					if !x.read && !x.written {
						report(d, "port", "used")
					}
				}
			}
		}
	}
	return out
}

// aliasSpans returns the spans of u's alias declarations in source
// order.
func aliasSpans(u *sema.Unit) []cst.Range {
	var out []cst.Range
	for _, d := range u.Decls {
		if d.Kind == sema.DeclAlias && d.File == u.File {
			out = append(out, d.Full)
		}
	}
	slices.SortFunc(out, func(a, b cst.Range) int {
		return cmp.Compare(a.Start, b.Start)
	})
	return out
}

// associated reports whether the name at rg is inside an actual of a
// port map or a procedure call, where it may be driven.
func associated(root cst.Node, rg cst.Range) bool {
	inAssoc := false
	for n := range root.Covering(rg.Start, rg.End).Ancestors() {
		switch n.Kind() {
		case cst.KindAssociationElement:
			inAssoc = true
		case cst.KindPortMapAspect, cst.KindProcedureCallStmt,
			cst.KindConcurrentProcedureCallStmt:
			return inAssoc
		}
	}
	return false
}

// targeted reports whether the name at rg begins an assignment
// target (or an element of an aggregate target): the prefix of a
// written record element, not a name read in the target's index.
func targeted(root cst.Node, rg cst.Range) bool {
	n := root.Covering(rg.Start, rg.End)
	for p := range n.Ancestors() {
		switch p.Kind() {
		case cst.KindTarget:
			return true
		case cst.KindAggregate, cst.KindElementAssociation:
			continue
		}
		if p.Span().Start != rg.Start {
			return false
		}
	}
	return false
}

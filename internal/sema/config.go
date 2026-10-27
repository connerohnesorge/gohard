package sema

// Configuration specification rules (#34, LRM 7.3): what the
// instantiation list and the binding indication of
// `for i1, i2 : comp use entity lib.e(a);` may name.

import (
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/token"
)

// configSpecRules checks a configuration specification of the current
// declarative part; comp is the component it names (nil when the name
// does not denote one).
func (a *analyzer) configSpecRules(cs cst.ConfigurationSpec, comp *Decl) {
	if a.quiet || cs.IsZero() {
		return
	}
	spec := cs.Spec()
	cn := spec.Component().Node
	if comp == nil && !cn.IsZero() {
		a.notComponent(cn)
	}
	if b := cs.Binding(); b.IsZero() || b.Entity().IsZero() {
		// LRM 7.3.2.1: only the binding indication of a component
		// configuration may omit the entity aspect (incremental
		// binding); a configuration specification is primary binding.
		a.semaError(cs.Node.FirstToken().Span(), "the binding "+
			"indication of a configuration specification must have an "+
			"entity aspect (LRM 7.3.2.1)")
	} else {
		a.entityAspectRules(b.Entity())
	}
	owner := specOwner(cs.Node)
	if owner.IsZero() || comp == nil {
		return
	}
	insts := directInstances(owner)
	bound := boundBefore(cs.Node.Span().Start, owner, insts)
	want := lastIdent(cn)
	list := spec.Instances()
	if !list.Node.Token(token.KwAll, 0).IsZero() ||
		!list.Node.Token(token.KwOthers, 0).IsZero() {
		if list.Node.Token(token.KwAll, 0).IsZero() {
			return // others: the instances not yet bound
		}
		for _, k := range sortedKeys(insts) {
			if st := insts[k]; instComponent(st) == want && bound[k] {
				a.semaError(list.Node.Span(), "instance "+
					instLabel(st)+" of "+a.text(cn)+" is already bound by "+
					"a configuration specification (LRM 7.3.1)")
				return
			}
		}
		return
	}
	for tok := range list.Labels() {
		k := string(TokenKey(tok))
		st, ok := insts[Key(k)]
		switch {
		case !ok:
			a.semaError(tok.Span(), "no component instantiation "+
				"statement labelled "+tok.Text()+" in this region "+
				"(LRM 7.3.1)")
		case instComponent(st) == "":
			a.semaError(tok.Span(), "a configuration specification "+
				"does not apply to the direct instantiation "+tok.Text()+
				" (LRM 7.3.1)")
		case instComponent(st) != want:
			a.semaError(tok.Span(), "instance "+tok.Text()+" is a "+
				"component "+string(instComponent(st))+", not "+
				a.text(cn)+" (LRM 7.3.1)")
		case bound[Key(k)]:
			a.semaError(tok.Span(), "instance "+tok.Text()+" is "+
				"already bound by a configuration specification "+
				"(LRM 7.3.1)")
		}
	}
}

// notComponent reports a component name of a configuration
// specification that denotes something else (LRM 7.3.1).
func (a *analyzer) notComponent(n cst.Node) {
	k := lastIdent(n)
	if k == "" || n.Kind() != cst.KindSimpleName {
		return
	}
	ds := a.lookup(k).Decls
	if len(ds) == 0 {
		a.undefined(n.FirstToken())
		return
	}
	dk := ds[0].Denotes().Kind
	a.semaError(n.Span(), a.text(n)+" is "+article(dk)+" "+dk.String()+
		", not a component "+
		"(LRM 7.3.1)")
}

// entityAspectRules reports `use entity L.U` where U is a unit of L
// that is not an entity, and `use configuration L.U` where it is not a
// configuration (LRM 7.3.2.2).
func (a *analyzer) entityAspectRules(ea cst.EntityAspect) {
	n := ea.Unit().Node
	if c, ok := cst.As[cst.CallOrIndexName](n); ok {
		n = c.Prefix().Node
	}
	sel, ok := cst.As[cst.SelectedName](n)
	if !ok {
		return
	}
	lib := simpleKey(sel.Prefix().Node)
	if lib == "" {
		return
	}
	if _, declared := libraryNames(a.u.Node)[lib]; !declared &&
		lib != Work && !a.s.inheritsLibrary(a.u, lib) {
		return
	}
	l, ok := a.s.library(a.u, lib)
	if !ok {
		return
	}
	u, ok := l.UnitFrom(TokenKey(sel.Suffix()), a.u)
	if !ok {
		return // reported as library/missing-unit
	}
	want, what := UnitEntity, "an entity"
	if !ea.Node.Token(token.KwConfiguration, 0).IsZero() {
		want, what = UnitConfiguration, "a configuration"
	} else if ea.Node.Token(token.KwEntity, 0).IsZero() {
		return // open
	}
	if u.Kind != want {
		a.semaError(sel.Suffix().Span(), u.Label()+" is "+
			withArticle(u.Kind.String())+", not "+what+" (LRM 7.3.2.2)")
	}
}

// inheritsLibrary reports whether the primary unit of secondary unit u
// declares library k (its context clause applies to u, LRM 13.1).
func (s *session) inheritsLibrary(u *Unit, k Key) bool {
	if u.Kind.Primary() {
		return false
	}
	l, ok := s.library(u, Work)
	if !ok {
		return false
	}
	p, ok := l.UnitFrom(u.Of, u)
	return ok && libraryNames(p.Node)[k]
}

// boundBefore returns the labels of insts that a configuration
// specification of owner's declarative part before offset end binds.
func boundBefore(end int, owner cst.Node, insts map[Key]cst.Node,
) map[Key]bool {
	out := map[Key]bool{}
	for x := range owner.Descendants() {
		if x.Span().Start >= end {
			break
		}
		prev, ok := cst.As[cst.ConfigurationSpec](x)
		if !ok || !specOwner(x).Equal(owner) {
			continue
		}
		spec := prev.Spec()
		comp := lastIdent(spec.Component().Node)
		list := spec.Instances()
		all := !list.Node.Token(token.KwAll, 0).IsZero()
		others := !list.Node.Token(token.KwOthers, 0).IsZero()
		if others {
			// others binds the instances no specification names, so
			// a later one naming one of them is no second binding.
			continue
		}
		if all {
			for _, k := range sortedKeys(insts) {
				if instComponent(insts[k]) == comp {
					out[k] = true
				}
			}
			continue
		}
		for tok := range list.Labels() {
			k := TokenKey(tok)
			if st, ok := insts[k]; ok && instComponent(st) == comp {
				out[k] = true
			}
		}
	}
	return out
}

// specOwner returns the architecture body, block statement or generate
// body whose declarative part holds n.
func specOwner(n cst.Node) cst.Node {
	for x := range n.Ancestors() {
		switch x.Kind() {
		case cst.KindArchitectureBody, cst.KindBlockStmt,
			cst.KindGenerateBody:
			return x
		case cst.KindPackageDecl, cst.KindPackageBody,
			cst.KindSubprogramBody, cst.KindProcessStmt:
			return cst.Node{}
		}
	}
	return cst.Node{}
}

// directInstances returns the instantiation statements of owner's own
// statement part (not those of nested blocks or generates) by label.
func directInstances(owner cst.Node) map[Key]cst.Node {
	out := map[Key]cst.Node{}
	for x := range owner.Descendants() {
		ci, ok := cst.As[cst.ComponentInstantiationStmt](x)
		if !ok || x.Inactive() {
			continue
		}
		if !instOwner(x).Equal(owner) {
			continue
		}
		if k := TokenKey(ci.Label().Name()); k != "" {
			out[k] = x
		}
	}
	return out
}

// instOwner is the region node of a concurrent statement.
func instOwner(n cst.Node) cst.Node {
	for x := range n.Ancestors() {
		switch x.Kind() {
		case cst.KindArchitectureBody, cst.KindBlockStmt,
			cst.KindGenerateBody, cst.KindForGenerateStmt,
			cst.KindIfGenerateStmt, cst.KindCaseGenerateStmt:
			return x
		}
	}
	return cst.Node{}
}

// instLabel is the label of an instantiation statement.
func instLabel(st cst.Node) string {
	return cst.ComponentInstantiationStmt{Node: st}.Label().Name().Text()
}

// instComponent returns the component an instantiation statement
// names, or "" for an entity or configuration instantiation.
func instComponent(st cst.Node) Key {
	iu := cst.ComponentInstantiationStmt{Node: st}.Unit()
	if !iu.Node.Token(token.KwEntity, 0).IsZero() ||
		!iu.Node.Token(token.KwConfiguration, 0).IsZero() {
		return ""
	}
	return lastIdent(iu.Name().Node)
}

// lastIdent is the key of the last identifier of a name.
func lastIdent(n cst.Node) Key {
	var k Key
	for t := range n.DescendantTokens() {
		if t.Kind() == token.Identifier ||
			t.Kind() == token.ExtendedIdentifier {
			k = TokenKey(t)
		}
	}
	return k
}

// withArticle prefixes a noun with "a" or "an".
func withArticle(w string) string {
	if w != "" && strings.ContainsRune("aeiou", rune(w[0])) {
		return "an " + w
	}
	return "a " + w
}

// configurationRules checks the block and component configurations of
// every configuration declaration against the architecture its block
// configuration names (LRM 3.4.2, 3.4.3): each block configuration
// names a block or generate statement of the enclosing block, each
// component configuration component instantiations of it. A block
// whose architecture is missing is not checked (its name is reported).
func (s *session) configurationRules() {
	for _, u := range s.units {
		if u.Kind != UnitConfiguration || u.Duplicate || u.File == nil ||
			u.File.Bundled {
			continue
		}
		cd, ok := cst.As[cst.ConfigurationDecl](u.Lib)
		if !ok {
			continue
		}
		bc := cd.Block()
		arch := simpleKey(bc.Spec().Node)
		l, ok := s.libs[u.Library]
		if !ok || arch == "" {
			continue
		}
		for _, x := range l.Secondary(u.Of) {
			if x.Kind == UnitArchitecture && x.Name == arch &&
				!x.Duplicate && !x.Lib.IsZero() {
				s.blockConfigRules(u, bc, x.Lib)
				break
			}
		}
	}
}

// blockConfigRules checks the items of block configuration bc against
// the statements of owner (an architecture body, block statement or
// generate body).
func (s *session) blockConfigRules(u *Unit, bc cst.BlockConfiguration,
	owner cst.Node,
) {
	report := func(tok cst.Token, msg string) {
		s.note(u, at(u.File, tok.Span(), diag.New(diag.SemaError, msg)))
	}
	where := ownerName(owner)
	insts := directInstances(owner)
	for item := range bc.Items() {
		switch item.Kind() {
		case cst.KindBlockConfiguration:
			nb := cst.BlockConfiguration{Node: item.Node}
			tok := nb.Spec().Node.FirstToken()
			k := TokenKey(tok)
			if k == "" {
				continue
			}
			st, ok := directStatements(owner)[k]
			switch {
			case !ok:
				report(tok, "no block or generate statement labelled "+
					tok.Text()+" in "+where+" (LRM 3.4.2)")
			case st.Kind() == cst.KindBlockStmt:
				s.blockConfigRules(u, nb, st)
			case st.Kind() == cst.KindForGenerateStmt:
				body := cst.ForGenerateStmt{Node: st}.Body().Node
				if !body.IsZero() {
					s.blockConfigRules(u, nb, body)
				}
			case st.Kind() == cst.KindIfGenerateStmt,
				st.Kind() == cst.KindCaseGenerateStmt:
			default:
				report(tok, tok.Text()+" is not a block or generate "+
					"statement of "+where+" (LRM 3.4.2)")
			}
		case cst.KindComponentConfiguration:
			cc := cst.ComponentConfiguration{Node: item.Node}
			spec := cc.Spec()
			want := lastIdent(spec.Component().Node)
			s.fullyBound(u, cc, owner, insts)
			s.nestedBlockSpec(u, cc) // forms.go
			for tok := range spec.Instances().Labels() {
				st, ok := insts[TokenKey(tok)]
				switch {
				case !ok:
					report(tok, "no component instantiation statement "+
						"labelled "+tok.Text()+" in "+where+
						" (LRM 3.4.3)")
				case instComponent(st) == "":
					report(tok, "a component configuration does not "+
						"apply to the direct instantiation "+tok.Text()+
						" (LRM 3.4.3)")
				case want != "" && instComponent(st) != want:
					report(tok, "instance "+tok.Text()+" is a component "+
						string(instComponent(st))+", not "+string(want)+
						" (LRM 3.4.3)")
				}
			}
		}
	}
}

// directStatements returns the labelled concurrent statements of
// owner's own statement part by label.
func directStatements(owner cst.Node) map[Key]cst.Node {
	out := map[Key]cst.Node{}
	for x := range owner.Descendants() {
		if !(cst.ConcStmt{}).Accepts(x.Kind()) || x.Inactive() ||
			!instOwner(x).Equal(owner) {
			continue
		}
		if l := x.Child[cst.Label](0); !l.IsZero() {
			if k := TokenKey(l.Name()); k != "" {
				out[k] = x
			}
		}
	}
	return out
}

// ownerName names a block for messages.
func ownerName(owner cst.Node) string {
	switch owner.Kind() {
	case cst.KindArchitectureBody:
		return "architecture " + cst.ArchitectureBody{Node: owner}.Name().
			Text()
	case cst.KindBlockStmt:
		return "block " + cst.BlockStmt{Node: owner}.Label().Name().Text()
	}
	return "the generate statement"
}

// fullyBound reports a component configuration holding a block
// configuration whose instances no binding indication binds to an
// entity: the block configuration then configures no architecture
// (LRM 3.4.3: the corresponding components shall be fully bound).
func (s *session) fullyBound(u *Unit, cc cst.ComponentConfiguration,
	owner cst.Node, insts map[Key]cst.Node,
) {
	nb := cc.Block()
	if nb.IsZero() {
		return
	}
	if b := cc.Binding(); !b.IsZero() && !b.Entity().IsZero() {
		return
	}
	bound := boundBefore(owner.Span().End, owner, insts)
	spec := cc.Spec()
	want := lastIdent(spec.Component().Node)
	list := spec.Instances()
	for _, k := range sortedKeys(insts) {
		st := insts[k]
		if instComponent(st) != want {
			continue
		}
		named := !list.Node.Token(token.KwAll, 0).IsZero() ||
			!list.Node.Token(token.KwOthers, 0).IsZero()
		for tok := range list.Labels() {
			named = named || TokenKey(tok) == k
		}
		if named && !bound[k] {
			tok := nb.Spec().Node.FirstToken()
			s.note(u, at(u.File, tok.Span(),
				diag.New(diag.SemaError, "instance "+instLabel(st)+
					" is not fully bound (no entity aspect binds it), "+
					"so it cannot hold the block configuration "+
					tok.Text()+" (LRM 3.4.3)")))
			return
		}
	}
}

// disconnectRules checks a signal name n of a disconnection
// specification, resolved to r: it denotes a guarded signal, one
// declared with a signal kind (bus or register; LRM 7.4, 6.4.2.3).
func (a *analyzer) disconnectRules(n cst.Node, r named) {
	if a.quiet || r.unknown {
		return
	}
	if c := n.Parent().Token(token.Colon, 0); !c.IsZero() &&
		n.Span().Start > c.Span().Start {
		return // the type mark
	}
	var d *Decl
	if len(r.decls) == 1 {
		d = r.decls[0].Denotes()
	}
	if d == nil || d.Kind != DeclSignal && d.Kind != DeclPort {
		a.semaError(n.Span(), a.text(n)+" is not a signal: a "+
			"disconnection specification names guarded signals "+
			"(LRM 7.4)")
		return
	}
	if d.Node.IsZero() {
		return
	}
	for t := range d.Node.Tokens() {
		if t.Kind() == token.KwBus || t.Kind() == token.KwRegister {
			return
		}
	}
	a.semaError(n.Span(), "signal "+a.text(n)+" in a disconnection "+
		"specification is not a guarded signal (LRM 7.4)")
}

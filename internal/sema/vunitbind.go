package sema

// vunitbind.go binds PSL verification units through verification unit
// binding indications (#66, LRM 2008 3.4.1, 3.4.3, 7.3.2, 7.3.4):
// `use vunit v, w;` in a configuration declaration binds the units to
// the design entity it configures (its entity and the architecture its
// block configuration names); in a component configuration or a
// configuration specification, to the design entity the binding
// indication binds the instances to (its entity aspect, a
// configuration's entity, or the default binding: the entity named
// like the component, with the architecture of the component
// configuration's block configuration). A component configuration
// without an entity aspect (an incremental binding, LRM 7.3.2.1) binds
// to the design entity of the configuration specification that binds
// the first of its instances in the configured block, and to the
// default binding when none does.
//
// A unit named there has no binding of its own (LRM 7.3.4: one bound
// in its declaration is an error). Its names are looked up in the
// bound design entity, as those of a unit bound in its declaration
// (#110). Sema analyzes each unit once, so an unbound unit is
// analyzed against the first indication that names it, in canonical
// order (units by Index, indications in source order); elaboration
// reports a binding to another design entity as unsupported/construct.
// A unit no indication names keeps its own names only, as GHDL 6.0.0
// analyzes it (sema/undefined for the design's names).
//
// The indications are read from the syntax tree before the unit jobs
// run (linkDeps), so the unit graph holds the bound entity and
// architecture as Deps of the unit (and the named units as Deps of
// the unit holding the indication): an edit that changes a binding
// changes the graph, and the incremental analysis starts over.

import (
	"slices"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// vunitBinder is the design entity a verification unit binding
// indication binds its units to: the library and entity, and the
// architecture when the indication's context names one.
type vunitBinder struct {
	lib, entity, arch Key
}

// vunitSite is one verification unit binding indication of a unit's
// tree, read without resolving anything: the units it names (lib ""
// for a simple name), and its design entity, as the entity aspect
// names it (lib Work when the name has no library prefix) or, when
// config is set, as the configuration lib.config does. open is a
// binding to open, which binds nothing. cc is the component
// configuration of an incremental binding (no entity aspect): a
// configuration specification of the configured block may bind its
// instances to another design entity (incrementalSite).
type vunitSite struct {
	names                     []unitRef
	lib, entity, arch, config Key
	open                      bool
	cc                        cst.ComponentConfiguration
}

// vunitSites lists the verification unit binding indications of the
// tree of u, in source order: only configuration declarations and
// architectures (configuration specifications) hold them. It is a
// pure function of the tree.
func vunitSites(u *Unit) []vunitSite {
	var out []vunitSite
	if u.Node.IsZero() || u.Kind != UnitConfiguration &&
		u.Kind != UnitArchitecture {
		return nil
	}
	for n := range u.Node.Descendants() {
		if n.Kind() != cst.KindVerificationUnitBinding || n.Inactive() {
			continue
		}
		site := vunitSite{lib: Work}
		for name := range (cst.VerificationUnitBinding{Node: n}).Names() {
			if r, ok := vunitListName(name.Node); ok {
				site.names = append(site.names, r)
			}
		}
		p := n.Parent()
		switch p.Kind() {
		case cst.KindConfigurationDecl:
			cd := cst.ConfigurationDecl{Node: p}
			site.entity = simpleKey(cd.Entity().Node)
			site.arch = blockArch(cd.Block())
		case cst.KindComponentConfiguration:
			cc := cst.ComponentConfiguration{Node: p}
			site.aspect(cc.Binding(), cc.Spec())
			if cc.Binding().Entity().IsZero() {
				site.cc = cc
			}
			if site.arch == "" && site.config == "" {
				site.arch = blockArch(cc.Block())
			}
		case cst.KindConfigurationSpec:
			cs := cst.ConfigurationSpec{Node: p}
			site.aspect(cs.Binding(), cs.Spec())
		default:
			continue
		}
		out = append(out, site)
	}
	return out
}

// vunitListName reads the name of a unit in a verification unit list: v
// (lib "") or lib.v.
func vunitListName(n cst.Node) (unitRef, bool) {
	switch n.Kind() {
	case cst.KindSimpleName:
		k := TokenKey(cst.SimpleName{Node: n}.Ident())
		return unitRef{unit: k, node: n}, k != ""
	case cst.KindSelectedName:
		sel := cst.SelectedName{Node: n}
		lib, k := simpleKey(sel.Prefix().Node), TokenKey(sel.Suffix())
		return unitRef{lib: lib, unit: k, node: n}, lib != "" && k != ""
	}
	return unitRef{}, false
}

// blockArch returns the architecture a block configuration names, ""
// when it names none.
func blockArch(bc cst.BlockConfiguration) Key {
	if bc.IsZero() {
		return ""
	}
	if sp := bc.Spec(); !sp.IsZero() && sp.Kind() == cst.KindSimpleName {
		return TokenKey(sp.FirstToken())
	}
	return ""
}

// aspect reads the design entity of the binding indication bi of a
// component specification spec: its entity aspect, else the default
// binding (the entity named like the component, LRM 7.3.3).
func (site *vunitSite) aspect(bi cst.BindingIndication,
	spec cst.ComponentSpecification,
) {
	ea := bi.Entity()
	switch {
	case ea.IsZero():
		site.entity = lastIdent(spec.Component().Node)
		return
	case !ea.Node.Token(token.KwOpen, 0).IsZero():
		site.open = true
		return
	}
	n := ea.Unit().Node
	var arch cst.Token
	if c, ok := cst.As[cst.CallOrIndexName](n); ok {
		n = c.Prefix().Node
		for item := range c.Args().Items() {
			arch = leftmostToken(item.Actual().Node)
			break
		}
	}
	if arch.IsZero() {
		arch = ea.Node.TokenIn(nameTokens, 0)
	}
	var k Key
	switch n.Kind() {
	case cst.KindSimpleName:
		k = simpleKey(n)
	case cst.KindSelectedName:
		sel := cst.SelectedName{Node: n}
		site.lib, k = simpleKey(sel.Prefix().Node), TokenKey(sel.Suffix())
	}
	if !ea.Node.Token(token.KwConfiguration, 0).IsZero() {
		site.config = k
		return
	}
	site.entity, site.arch = k, TokenKey(arch)
}

// findVunitBinders binds every unbound verification unit that a
// verification unit binding indication names to the design entity of
// the first such indication, in canonical order.
func (s *session) findVunitBinders() map[*Unit]vunitBinder {
	out := map[*Unit]vunitBinder{}
	for _, u := range s.units {
		if u.Duplicate || u.File == nil || u.File.Bundled {
			continue
		}
		for _, site := range memoized(u, "vunitSites", func() []vunitSite {
			return vunitSites(u)
		}) {
			b, ok := s.siteBinder(u, site)
			if !ok {
				continue
			}
			for _, r := range site.names {
				v := s.namedVunit(u, r)
				if v == nil || v.BoundInline() {
					continue
				}
				if _, done := out[v]; !done {
					out[v] = b
				}
			}
		}
	}
	return out
}

// siteBinder returns the design entity of the indication site of unit
// u; false when it binds to open or names an unknown configuration.
func (s *session) siteBinder(u *Unit, site vunitSite) (vunitBinder,
	bool,
) {
	if !site.cc.IsZero() {
		if a, spec, ok := s.incrementalSite(u, site.cc); ok {
			if spec.arch == "" && spec.config == "" {
				spec.arch = site.arch
			}
			return s.siteBinder(a, spec)
		}
	}
	if site.open {
		return vunitBinder{}, false
	}
	lib := site.lib
	if lib == Work {
		lib = u.Library
	}
	if site.config == "" {
		return vunitBinder{lib: lib, entity: site.entity,
			arch: site.arch}, site.entity != ""
	}
	l, ok := s.libs[lib]
	if !ok {
		return vunitBinder{}, false
	}
	c, ok := l.UnitFrom(site.config, u)
	if !ok || c.Kind != UnitConfiguration {
		return vunitBinder{}, false
	}
	cd, ok := cst.As[cst.ConfigurationDecl](c.Lib)
	if !ok {
		return vunitBinder{}, false
	}
	return vunitBinder{lib: c.Library, entity: c.Of,
		arch: blockArch(cd.Block())}, c.Of != ""
}

// incrementalSite returns the configuration specification that binds
// the instances of the component configuration cc of unit u, which
// has no entity aspect (LRM 7.3.2.1: the binding it adds to is that of
// the configuration specification, else the default binding), as an
// indication site, and the architecture a holding it. The
// specification of the first instance cc applies to, in source order,
// counts. ok is false when the configured block or the specification
// is not found.
func (s *session) incrementalSite(u *Unit, cc cst.ComponentConfiguration,
) (a *Unit, spec vunitSite, ok bool) {
	a, owner := s.ccOwner(u, cc)
	if owner.IsZero() {
		return nil, vunitSite{}, false
	}
	comp := lastIdent(cc.Spec().Component().Node)
	insts := directInstances(owner)
	var target cst.Node
	for _, k := range sortedKeys(insts) {
		st := insts[k]
		if instComponent(st) == comp && ccNames(cc, k) &&
			(target.IsZero() || st.Span().Start < target.Span().Start) {
			target = st
		}
	}
	if target.IsZero() {
		return nil, vunitSite{}, false
	}
	label := TokenKey(cst.ComponentInstantiationStmt{Node: target}.Label().
		Name())
	var others cst.ConfigurationSpec
	for x := range owner.Descendants() {
		cs, ok := cst.As[cst.ConfigurationSpec](x)
		if !ok || x.Inactive() || !specOwner(x).Equal(owner) ||
			lastIdent(cs.Spec().Component().Node) != comp {
			continue
		}
		list := cs.Spec().Instances()
		named := !list.Node.Token(token.KwAll, 0).IsZero()
		for tok := range list.Labels() {
			named = named || TokenKey(tok) == label
		}
		if !named {
			if others.IsZero() &&
				!list.Node.Token(token.KwOthers, 0).IsZero() {
				others = cs
			}
			continue
		}
		spec = vunitSite{lib: Work}
		spec.aspect(cs.Binding(), cs.Spec())
		return a, spec, true
	}
	if others.IsZero() {
		return nil, vunitSite{}, false
	}
	spec = vunitSite{lib: Work}
	spec.aspect(others.Binding(), others.Spec())
	return a, spec, true
}

// ccNames reports whether the instantiation list of the component
// configuration cc names the instance labelled k: by its label, all,
// or others when no other component configuration of the same block
// configuration names it.
func ccNames(cc cst.ComponentConfiguration, k Key) bool {
	list := cc.Spec().Instances()
	if !list.Node.Token(token.KwAll, 0).IsZero() {
		return true
	}
	for tok := range list.Labels() {
		if TokenKey(tok) == k {
			return true
		}
	}
	if list.Node.Token(token.KwOthers, 0).IsZero() {
		return false
	}
	comp := lastIdent(cc.Spec().Component().Node)
	for x := range cc.Node.Parent().Nodes() {
		o, ok := cst.As[cst.ComponentConfiguration](x)
		if !ok || x.Equal(cc.Node) ||
			lastIdent(o.Spec().Component().Node) != comp {
			continue
		}
		for tok := range o.Spec().Instances().Labels() {
			if TokenKey(tok) == k {
				return false
			}
		}
	}
	return true
}

// ccOwner returns the architecture body, block statement or generate
// body whose statements the component configuration cc of unit u
// configures, and the architecture a holding it: the block
// configurations around cc name the architecture of the configured
// design entity (that of the configuration declaration, or the design
// entity an enclosing component configuration binds to) and the
// blocks and generate statements inside it. owner is zero when one
// is not found.
func (s *session) ccOwner(u *Unit, cc cst.ComponentConfiguration,
) (a *Unit, owner cst.Node) {
	var chain []cst.BlockConfiguration // innermost first
	p := cc.Node.Parent()
	for ; p.Kind() == cst.KindBlockConfiguration; p = p.Parent() {
		chain = append(chain, cst.BlockConfiguration{Node: p})
	}
	if len(chain) == 0 {
		return nil, cst.Node{}
	}
	top := chain[len(chain)-1]
	var lib, entity, arch Key
	switch p.Kind() {
	case cst.KindConfigurationDecl:
		lib, entity, arch = u.Library, u.Of, blockArch(top)
	case cst.KindComponentConfiguration:
		outer := cst.ComponentConfiguration{Node: p}
		site := vunitSite{lib: Work}
		site.aspect(outer.Binding(), outer.Spec())
		if outer.Binding().Entity().IsZero() {
			site.cc = outer
		}
		if site.arch == "" && site.config == "" {
			site.arch = blockArch(top)
		}
		b, ok := s.siteBinder(u, site)
		if !ok {
			return nil, cst.Node{}
		}
		lib, entity, arch = b.lib, b.entity, b.arch
	default:
		return nil, cst.Node{}
	}
	l, ok := s.libs[lib]
	if !ok || arch == "" {
		return nil, cst.Node{}
	}
	for _, x := range l.Secondary(entity) {
		if x.Kind == UnitArchitecture && x.Name == arch && !x.Duplicate &&
			!x.Lib.IsZero() {
			a, owner = x, x.Lib
			break
		}
	}
	for i := len(chain) - 2; i >= 0 && !owner.IsZero(); i-- {
		owner = nestedOwner(owner, chain[i])
	}
	return a, owner
}

// nestedOwner returns the block statement or generate body of owner
// that the block configuration bc names (an if or case generate's
// alternative by its label, gen(alt), else its first one); zero when
// there is none.
func nestedOwner(owner cst.Node, bc cst.BlockConfiguration) cst.Node {
	st, ok := directStatements(owner)[TokenKey(bc.Spec().Node.
		FirstToken())]
	if !ok {
		return cst.Node{}
	}
	var alt Key
	if l := bc.Node.Child[cst.AssociationList](0); !l.IsZero() {
		for item := range l.Items() {
			alt = simpleKey(item.Actual().Node)
			break
		}
	}
	switch st.Kind() {
	case cst.KindBlockStmt:
		return st
	case cst.KindForGenerateStmt:
		return cst.ForGenerateStmt{Node: st}.Body().Node
	case cst.KindIfGenerateStmt:
		for br := range (cst.IfGenerateStmt{Node: st}).Branches() {
			if alt == "" || TokenKey(br.AltLabel().Name()) == alt {
				return br.Body().Node
			}
		}
	case cst.KindCaseGenerateStmt:
		for c := range (cst.CaseGenerateStmt{Node: st}).Alternatives() {
			if alt == "" || TokenKey(c.AltLabel().Name()) == alt {
				return c.Body().Node
			}
		}
	}
	return cst.Node{}
}

// namedVunit returns the verification unit the name r of an
// indication in unit u denotes: lib.v, or for a simple name the unit
// of u's working library (the name is looked up like the entity name
// of a configuration declaration); nil when it is no verification
// unit.
func (s *session) namedVunit(u *Unit, r unitRef) *Unit {
	lib := r.lib
	if lib == "" {
		lib = Work
	}
	l, ok := s.library(u, lib)
	if !ok {
		return nil
	}
	v, ok := l.UnitFrom(r.unit, u)
	if !ok || v.Kind != UnitVunit || v == u {
		return nil
	}
	return v
}

// namedVunits returns the verification units the indications of u
// name, in source order: u depends on them.
func (s *session) namedVunits(u *Unit) []*Unit {
	if u.File == nil || u.File.Bundled {
		return nil
	}
	var out []*Unit
	for _, site := range memoized(u, "vunitSites", func() []vunitSite {
		return vunitSites(u)
	}) {
		for _, r := range site.names {
			if v := s.namedVunit(u, r); v != nil {
				out = append(out, v)
			}
		}
	}
	return out
}

// vunitBindings resolves the names of the verification unit binding
// indications among the children of n (a configuration declaration, a
// component configuration or a configuration specification).
func (a *analyzer) vunitBindings(n cst.Node) {
	for vb := range n.Children[cst.VerificationUnitBinding]() {
		for name := range vb.Names() {
			a.vunitName(name.Node)
		}
	}
}

// vunitName resolves one name of a verification unit list: it denotes
// a PSL verification unit without a binding of its own (LRM 7.3.4). A
// simple name that names nothing visible denotes the unit of the
// working library. A unit that a.u does not depend on (linkVunitDeps:
// it is bound to a.u's own architecture) is analyzed after a.u, so the
// name is checked against its tree and records no reference
// (Result.NamedVunit finds the unit).
func (a *analyzer) vunitName(n cst.Node) {
	boundInline := func() {
		a.semaError(n.Span(), a.text(n)+" is bound in its "+
			"declaration, and a verification unit binding indication "+
			"names only unbound verification units (LRM 7.3.4)")
	}
	if r, ok := vunitListName(n); ok && (r.lib != "" ||
		len(a.lookup(r.unit).Decls) == 0) {
		if v := a.s.namedVunit(a.u, r); v != nil &&
			!slices.Contains(a.u.Deps, v) {
			if v.BoundInline() {
				boundInline()
			}
			return
		}
	}
	var d *Decl
	if n.Kind() == cst.KindSimpleName {
		tok := cst.SimpleName{Node: n}.Ident()
		k := TokenKey(tok)
		if len(a.lookup(k).Decls) == 0 {
			l, ok := a.s.library(a.u, Work)
			if !ok {
				return
			}
			u, ok := l.UnitFrom(k, a.u)
			switch {
			case ok && u.Kind != UnitVunit:
				a.semaError(n.Span(), a.text(n)+" is "+
					withArticle(u.Kind.String())+", not a verification "+
					"unit (LRM 7.3.4)")
				return
			case ok && u.Decl != nil && a.s.readable(a.u, u):
				d = u.Decl
				a.ref(tok, d, RefRead)
			}
		}
	}
	if d == nil {
		r := a.resolveName(n, RefRead, nil)
		if len(r.decls) == 0 {
			return
		}
		d = r.decls[0].Denotes()
	}
	switch {
	case d.Kind != DeclVunit:
		a.semaError(n.Span(), a.text(n)+" is "+article(d.Kind)+" "+
			d.Kind.String()+", not a verification unit (LRM 7.3.4)")
	case d.Unit != nil && d.Unit.BoundInline():
		boundInline()
	}
}

// NamedVunit returns the PSL verification unit that the name n of a
// verification unit binding indication written in unit u denotes, nil
// when it denotes none: the unit its reference names or, for a unit u
// does not depend on (vunitName), the unit of the library the name
// names.
func (r *Result) NamedVunit(u *Unit, n cst.Node) *Unit {
	if r == nil || u == nil || u.File == nil || n.IsZero() {
		return nil
	}
	if ref, ok := r.RefAt(u.File.Path, n.LastToken().Span().Start); ok &&
		ref.Decl != nil {
		if d := ref.Decl.Denotes(); d.Kind == DeclVunit {
			return d.Unit
		}
		return nil
	}
	name, ok := vunitListName(n)
	if !ok {
		return nil
	}
	lib := name.lib
	if lib == "" || lib == Work {
		lib = u.Library
	}
	l, ok := r.libs[lib]
	if !ok {
		return nil
	}
	v, ok := l.UnitFrom(name.unit, u)
	if !ok || v.Kind != UnitVunit || v == u || v.BoundInline() ||
		slices.Contains(u.Deps, v) {
		return nil
	}
	return v
}

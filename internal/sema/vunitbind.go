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
// configuration's block configuration).
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
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/token"
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
// binding to open, which binds nothing.
type vunitSite struct {
	names                     []unitRef
	lib, entity, arch, config Key
	open                      bool
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
// working library.
func (a *analyzer) vunitName(n cst.Node) {
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
		a.semaError(n.Span(), a.text(n)+" is bound in its "+
			"declaration, and a verification unit binding indication "+
			"names only unbound verification units (LRM 7.3.4)")
	}
}

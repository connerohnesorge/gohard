package hier

// config.go binds component instances (LRM 7.3, 14.5.2): component
// configurations of configuration declarations, configuration
// specifications, and the default binding. The configuration items are
// read from the syntax tree; their names were resolved by sema
// (Result.RefAt) and the actuals of their generic maps typed into the
// unit's expressions.

import (
	"cmp"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// binding is how one instance is bound: the entity and architecture,
// the block configuration that applies inside it, and the generic map
// of the binding indication.
type binding struct {
	entity *sema.Decl
	arch   sema.Key
	block  *blockConfig
	// config is the configuration declaration that supplied the
	// binding (a configuration instantiation, a top, or `use
	// configuration`).
	config *sema.Unit
	// unit holds the binding indication (its references and typed
	// actuals).
	unit *sema.Unit
	gmap []bindAssoc
	// ports is set when the binding has a port map: the ports are not
	// the default by-name association then.
	ports bool
	// aspect is set when the binding indication has an entity aspect.
	aspect bool
	// vunits are the PSL verification units the binding's verification
	// unit binding indications name (#66).
	vunits []*sema.Unit
}

// bindAssoc is one association of a binding's generic map.
type bindAssoc struct {
	formal *sema.Decl
	// name is the formal's name, for an incremental binding whose
	// entity is not known where it is written.
	name   sema.Key
	actual *sema.Expr
}

// generic returns the binding's association of the entity generic g.
func (bd *binding) generic(g *sema.Decl) *bindAssoc {
	if bd == nil {
		return nil
	}
	for i := range bd.gmap {
		f := bd.gmap[i].formal
		if f == g || (f == nil && bd.gmap[i].name == g.Name) {
			return &bd.gmap[i]
		}
	}
	return nil
}

// blockConfig is a block configuration (LRM 3.4.2) and the unit it is
// written in.
type blockConfig struct {
	unit *sema.Unit
	node cst.BlockConfiguration
	// arch is the architecture a top-level block configuration names.
	arch sema.Key
}

// configuration returns the binding a configuration declaration makes
// for its entity.
func (b *builder) configuration(u *sema.Unit) *binding {
	if u == nil || u.Decl == nil || u.Decl.Spec == nil {
		return nil
	}
	cd, ok := cst.As[cst.ConfigurationDecl](u.Lib)
	if !ok {
		return &binding{entity: u.Decl.Spec, config: u}
	}
	bc := &blockConfig{unit: u, node: cd.Block()}
	if sp := bc.node.Spec(); !sp.IsZero() &&
		sp.Kind() == cst.KindSimpleName {
		bc.arch = sema.TokenKey(sp.FirstToken())
	}
	return &binding{entity: u.Decl.Spec, arch: bc.arch, block: bc,
		config: u, vunits: b.vunitsOf(u, cd.Node)}
}

// vunitsOf returns the PSL verification units that the verification
// unit binding indications (`use vunit v, w;`, LRM 7.3.4) among the
// children of n, written in unit u, name, as sema resolved them.
func (b *builder) vunitsOf(u *sema.Unit, n cst.Node) []*sema.Unit {
	if u == nil || u.File == nil {
		return nil
	}
	var out []*sema.Unit
	for vb := range n.Children[cst.VerificationUnitBinding]() {
		for name := range vb.Names() {
			v := b.r.NamedVunit(u, name.Node)
			if v != nil && !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
	}
	return out
}

// inlineVunits returns the PSL verification units of r bound in their
// own declaration (vunit v(e(a))), in canonical order.
func inlineVunits(r *sema.Result) []*sema.Unit {
	var out []*sema.Unit
	for _, u := range r.Units {
		if u.Kind == sema.UnitVunit && u.Decl != nil && !u.Duplicate &&
			!u.Opaque && u.BoundInline() {
			out = append(out, u)
		}
	}
	return out
}

// vunits returns the verification units bound to the entity instance
// in, whose binding is bd (#66): the units bound inline to its design
// entity (sema.Unit.Binds) and those bd's verification unit binding
// indications name, in canonical order, each once.
func (b *builder) vunits(in *Instance, bd *binding) []*sema.Unit {
	var out []*sema.Unit
	for _, u := range b.inline {
		if u.Binds(in.Entity.Unit, in.Arch) {
			out = append(out, u)
		}
	}
	if bd != nil {
		for _, u := range bd.vunits {
			if !slices.Contains(out, u) && !u.Opaque && !u.Duplicate {
				out = append(out, u)
			}
		}
	}
	slices.SortFunc(out, func(x, y *sema.Unit) int {
		return cmp.Compare(x.Index, y.Index)
	})
	return out
}

// nested returns the block configuration inside bc for the block or
// generate statement named lbl; for a for generate, idx is the
// iteration's index (LRM 3.4.2: an index specification picks
// iterations, none means every iteration).
func (bc *blockConfig) nested(lbl string, idx *eval.Value) *blockConfig {
	if bc == nil || bc.node.IsZero() {
		return nil
	}
	for item := range bc.node.Items() {
		if item.Kind() != cst.KindBlockConfiguration {
			continue
		}
		n := cst.BlockConfiguration{Node: item.Node}
		sp := n.Spec().Node
		if string(sema.TokenKey(sp.FirstToken())) != lbl {
			continue
		}
		spec := indexSpec(n)
		if spec.IsZero() || indexMatches(spec, idx) {
			return &blockConfig{unit: bc.unit, node: n}
		}
	}
	return nil
}

// nestedAlt returns the block configuration of alternative alt of the
// if or case generate lbl (VHDL-2008: for lbl(alt)).
func (bc *blockConfig) nestedAlt(lbl, alt string) *blockConfig {
	if bc == nil || bc.node.IsZero() {
		return nil
	}
	for item := range bc.node.Items() {
		if item.Kind() != cst.KindBlockConfiguration {
			continue
		}
		n := cst.BlockConfiguration{Node: item.Node}
		sp := n.Spec().Node
		if string(sema.TokenKey(sp.FirstToken())) != lbl {
			continue
		}
		arg := indexSpec(n)
		if arg.Kind() == cst.KindAssociationElement {
			arg = cst.AssociationElement{Node: arg}.Actual().Node
		}
		if arg.Kind() == cst.KindSimpleName &&
			string(sema.TokenKey(arg.FirstToken())) == alt {
			return &blockConfig{unit: bc.unit, node: n}
		}
	}
	return nil
}

// indexSpec returns the index specification of a block
// configuration (the node inside the parentheses of gen(3) or
// gen(1 to 3)), or the zero Node.
func indexSpec(n cst.BlockConfiguration) cst.Node {
	l := n.Node.Child[cst.AssociationList](0)
	if l.IsZero() {
		return cst.Node{}
	}
	return inParens(l.Node)
}

// indexMatches reports whether the index specification arg of a block
// configuration selects the iteration idx. Only literal bounds are
// read; an index specification that is not a literal matches every
// iteration.
func indexMatches(arg cst.Node, idx *eval.Value) bool {
	if idx == nil {
		return false
	}
	if arg.Kind() == cst.KindAssociationElement {
		arg = cst.AssociationElement{Node: arg}.Actual().Node
	}
	if arg.Kind() == cst.KindRangeExpr {
		re := cst.RangeExpr{Node: arg}
		l, lok := literal(re.Low().Node)
		h, hok := literal(re.High().Node)
		if !lok || !hok {
			return true
		}
		r := eval.Range{Left: l, Right: h}
		if re.Direction().Kind() == token.KwDownto {
			r.Dir = eval.Downto
		}
		return r.Contains(idx.I)
	}
	v, ok := literal(arg)
	return !ok || v == idx.I
}

// literal reads an integer literal, possibly negated.
func literal(n cst.Node) (int64, bool) {
	text := strings.TrimSpace(n.Text())
	neg := strings.HasPrefix(text, "-")
	v, ok := sema.ParseInteger(strings.TrimSpace(strings.TrimPrefix(text,
		"-")))
	if neg {
		v = -v
	}
	return v, ok
}

// inParens returns the first child node after the first "(" of n.
func inParens(n cst.Node) cst.Node {
	seen := false
	for e := range n.Elements() {
		if e.IsToken() {
			if e.Token.Kind() == token.LParen {
				seen = true
			}
			continue
		}
		if seen {
			return e.Node
		}
	}
	return cst.Node{}
}

// componentBinding binds the component instance s: a component
// configuration of bc, else a configuration specification of the
// declarative part sc, else the default binding. A component
// configuration without an entity aspect (incremental binding) keeps
// the entity of the specification or the default and adds its generic
// map and block configuration.
func (b *builder) componentBinding(s *sema.Stmt, sc scope,
	bc *blockConfig,
) *binding {
	var inc *binding
	if bc != nil && !bc.node.IsZero() {
		var ccs []cst.ComponentConfiguration
		var specs []cst.ComponentSpecification
		for item := range bc.node.Items() {
			if item.Kind() == cst.KindComponentConfiguration {
				cc := cst.ComponentConfiguration{Node: item.Node}
				ccs = append(ccs, cc)
				specs = append(specs, cc.Spec())
			}
		}
		for _, cc := range ccs {
			if !specMatches(cc.Spec(), s, specs) {
				continue
			}
			bd := b.bindingOf(bc.unit, cc.Binding())
			bd.vunits = append(bd.vunits, b.vunitsOf(bc.unit,
				cc.Node)...)
			if blk := cc.Block(); !blk.IsZero() {
				bd.block = &blockConfig{unit: bc.unit, node: blk}
				if sp := blk.Spec(); !sp.IsZero() &&
					sp.Kind() == cst.KindSimpleName && bd.arch == "" {
					bd.arch = sema.TokenKey(sp.FirstToken())
				}
			}
			if bd.entity != nil || bd.open() {
				return bd
			}
			inc = bd
			break
		}
	}
	bd := b.specBinding(s, sc)
	if bd == nil {
		bd = &binding{entity: b.defaultEntity(s)}
	}
	if inc != nil {
		if len(inc.gmap) > 0 {
			bd.gmap, bd.unit = inc.gmap, inc.unit
		}
		if inc.block != nil {
			bd.block = inc.block
		}
		if inc.arch != "" && bd.arch == "" {
			bd.arch = inc.arch
		}
		bd.vunits = append(bd.vunits, inc.vunits...)
	}
	return bd
}

// open reports a binding to open (no entity, LRM 7.3.2.1).
func (bd *binding) open() bool {
	return bd != nil && bd.aspect && bd.entity == nil
}

// specBinding finds the configuration specification of the
// declarative part sc that applies to s.
func (b *builder) specBinding(s *sema.Stmt, sc scope) *binding {
	decls := declsOf(sc.node)
	if decls.IsZero() {
		return nil
	}
	var cfs []cst.ConfigurationSpec
	var specs []cst.ComponentSpecification
	for item := range decls.Nodes() {
		if item.Kind() == cst.KindConfigurationSpec && !item.Inactive() {
			cf := cst.ConfigurationSpec{Node: item}
			cfs = append(cfs, cf)
			specs = append(specs, cf.Spec())
		}
	}
	for _, cf := range cfs {
		if specMatches(cf.Spec(), s, specs) {
			bd := b.bindingOf(sc.unit, cf.Binding())
			if bd.entity == nil && cf.Binding().Entity().IsZero() {
				bd.entity = b.defaultEntity(s)
			}
			bd.vunits = append(bd.vunits, b.vunitsOf(sc.unit,
				cf.Node)...)
			return bd
		}
	}
	return nil
}

// declsOf returns the declarative part of an architecture, block or
// generate body node.
func declsOf(n cst.Node) cst.Node {
	switch n.Kind() {
	case cst.KindArchitectureBody:
		return cst.ArchitectureBody{Node: n}.Decls().Node
	case cst.KindBlockStmt:
		return cst.BlockStmt{Node: n}.Decls().Node
	case cst.KindGenerateBody:
		return cst.GenerateBody{Node: n}.Decls().Node
	}
	return cst.Node{}
}

// specMatches reports whether the component specification sp applies
// to the instance s; all is every specification of the same
// declarative part (for `others`).
func specMatches(sp cst.ComponentSpecification, s *sema.Stmt,
	all []cst.ComponentSpecification,
) bool {
	if s.Component == nil || s.Label == nil {
		return false
	}
	comp := sp.Component().Node
	if comp.IsZero() || sema.TokenKey(comp.LastToken()) != s.Component.Name {
		return false
	}
	insts := sp.Instances()
	if !insts.Node.Token(token.KwAll, 0).IsZero() {
		return true
	}
	if !insts.Node.Token(token.KwOthers, 0).IsZero() {
		for _, o := range all {
			if o.Node.Equal(sp.Node) {
				continue
			}
			oc := o.Component().Node
			if oc.IsZero() ||
				sema.TokenKey(oc.LastToken()) != s.Component.Name {
				continue
			}
			for tok := range o.Instances().Labels() {
				if sema.TokenKey(tok) == s.Label.Name {
					return false
				}
			}
		}
		return true
	}
	for tok := range insts.Labels() {
		if sema.TokenKey(tok) == s.Label.Name {
			return true
		}
	}
	return false
}

// bindingOf reads a binding indication written in unit u.
func (b *builder) bindingOf(u *sema.Unit, bi cst.BindingIndication,
) *binding {
	bd := &binding{unit: u}
	if bi.IsZero() || u == nil || u.File == nil {
		return bd
	}
	ea := bi.Entity()
	bd.aspect = !ea.IsZero()
	if !ea.IsZero() && ea.Node.Token(token.KwOpen, 0).IsZero() {
		n := ea.Unit().Node
		var arch cst.Token
		if c, ok := cst.As[cst.CallOrIndexName](n); ok {
			n = c.Prefix().Node
			for item := range c.Args().Items() {
				arch = item.Actual().Node.FirstToken()
				break
			}
		} else if !ea.Node.Token(token.LParen, 0).IsZero() {
			arch = ea.Node.Token(token.Identifier, 0)
		}
		if ref, ok := b.r.RefAt(u.File.Path,
			n.LastToken().Span().Start); ok && ref.Decl != nil {
			d := ref.Decl.Denotes()
			switch d.Kind {
			case sema.DeclEntity:
				bd.entity = d
				if !arch.IsZero() {
					bd.arch = sema.TokenKey(arch)
				}
			case sema.DeclConfiguration:
				if c := b.configuration(d.Unit); c != nil {
					c.unit, c.aspect = u, true
					bd = c
				}
			}
		}
	}
	bd.ports = !bi.PortMap().IsZero()
	var formals []*sema.Decl
	if bd.entity != nil {
		formals = interfaceDecls(bd.entity.Region, true)
	}
	i := 0
	for item := range bi.GenericMap().List().Items() {
		var f *sema.Decl
		var name sema.Key
		if fn := item.Formal(); !fn.IsZero() {
			tok := fn.FirstToken()
			name = sema.TokenKey(tok)
			if ref, ok := b.r.RefAt(u.File.Path,
				tok.Span().Start); ok && ref.Decl != nil &&
				ref.Decl.Kind == sema.DeclGeneric {
				f = ref.Decl
			}
		} else if i < len(formals) {
			f = formals[i]
		}
		i++
		v := item.Actual().Node
		if (f == nil && name == "") || v.IsZero() ||
			v.Kind() == cst.KindOpenExpr {
			continue
		}
		if e := b.exprAt(u, v); e != nil {
			bd.gmap = append(bd.gmap, bindAssoc{formal: f, name: name,
				actual: e})
		}
	}
	return bd
}

// exprAt returns the typed top-level expression of unit u at node n.
func (b *builder) exprAt(u *sema.Unit, n cst.Node) *sema.Expr {
	idx, ok := b.exprs[u]
	if !ok {
		idx = map[[2]int]*sema.Expr{}
		for _, e := range u.Exprs {
			sp := e.Node.Span()
			k := [2]int{sp.Start, sp.End}
			if _, dup := idx[k]; !dup {
				idx[k] = e
			}
		}
		b.exprs[u] = idx
	}
	sp := n.Span()
	return idx[[2]int{sp.Start, sp.End}]
}

// defaultEntity is the default binding of a component instance (LRM
// 7.3.3): the entity of the same simple name in the instantiating
// unit's library.
func (b *builder) defaultEntity(s *sema.Stmt) *sema.Decl {
	if s.Label == nil || s.Label.Unit == nil || s.Component == nil {
		return nil
	}
	u := s.Label.Unit
	l, ok := b.r.Library(u.Library)
	if !ok {
		return nil
	}
	e, ok := l.Unit(s.Component.Name)
	if !ok || e.Kind != sema.UnitEntity || e.Decl == nil {
		return nil
	}
	return e.Decl
}

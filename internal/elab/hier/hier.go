// Package hier builds the elaborated instance hierarchy of a design
// (#34, #35): every instance of every top-level entity with its path
// (tb.dut.u0, tb.gen(3).u), its architecture and its generic values,
// computed once per instance with the shared evaluator (internal/eval
// through sema.Evaluator). It is what per-instance static checks and
// the elaboration oracle (GHDL --disp-tree, #8) look at, and what the
// simulator's elaboration (#35) extends.
//
// Binding follows LRM 7.3 and 14.5: an entity instantiation names its
// entity (and architecture), a configuration instantiation or top
// applies its block configuration, and a component instance is bound
// by a component configuration of the enclosing block configuration,
// else by a configuration specification of the enclosing declarative
// part, else by the default binding (the entity of the component's
// name in the instantiating unit's library, its most recently analyzed
// architecture). For, if and case generates are unrolled: a for
// iteration's path is label(index), a chosen if or case alternative
// with an alternative label adds it (polarity.inv).
//
// Per-instance static checks (checks.go) evaluate what elaboration
// evaluates under the instance's generics: generic and port map
// actuals (subtype and index ranges, port widths) and the constants and
// signals of the instance's declarative parts. A failure is
// sema/static-error naming the instance and its generics ("width
// mismatch ... in tb.dut.u1 (WIDTH=12)"); reaching the step limit is
// sema/eval-limit and lists the instance in Incomplete.
//
// Build is a pure function of the sema result and the options: tops,
// children and generics are in canonical order, and every evaluation
// runs under the step budget (the editor's 1,000,000 steps unless the
// caller passes the simulation budget). Dump is the stable text form
// the elaboration oracle compares (dump.go).
package hier

import (
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/diag"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
)

// Kind is the kind of an instance.
type Kind uint8

// Instance kinds.
const (
	// KindEntity is a design entity: a top or an instantiated entity
	// with its architecture.
	KindEntity Kind = iota + 1
	// KindBlock is a block statement.
	KindBlock
	// KindGenerate is one iteration of a for generate or the chosen
	// alternative of an if or case generate.
	KindGenerate
	// KindUnbound is a component instance with no entity bound (an
	// open binding, or no entity of the component's name).
	KindUnbound
)

// Generic is one generic of an instance and its value.
type Generic struct {
	Decl *sema.Decl
	// Value is valid when Known; Err says why the value is not known
	// (not static, or the step limit).
	Value eval.Value
	Known bool
	Err   error
	// Type is the actual of a generic type.
	Type *sema.Type
}

// Instance is one node of the hierarchy.
type Instance struct {
	Kind Kind
	// Path is the instance path: labels from the top entity, lower
	// case, generate iterations as label(index), chosen alternatives
	// with an alternative label as label.alt.
	Path string
	// Entity and Arch are the design entity of a KindEntity instance;
	// Component the component of a component instantiation;
	// Configuration the configuration that bound it, if any.
	Entity        *sema.Decl
	Arch          *sema.Unit
	Component     *sema.Decl
	Configuration *sema.Unit
	Generics      []Generic
	Children      []*Instance
	// Vunits are the PSL verification units bound to a KindEntity
	// instance (#66): the units bound in their declaration to its
	// entity, or to its entity and architecture (vunit v(e(a))), and
	// the units the verification unit binding indications (use vunit)
	// of the configuration items that bound it name, in canonical
	// order, each once.
	Vunits []*sema.Unit
	// Stmt is the statement that creates the instance (nil for tops);
	// Alt the chosen alternative of an if or case generate, Param the
	// value of a for generate's parameter.
	Stmt  *sema.Stmt
	Alt   *sema.Alt
	Param eval.Value

	ev     *sema.Evaluator
	parent *Instance
}

// Describe returns the path with the known generic values, as
// per-instance diagnostics name an instance: "tb.dut.u1 (WIDTH=12)".
func (in *Instance) Describe() string {
	var gs []string
	for _, g := range in.Generics {
		if g.Known && g.Decl.Kind == sema.DeclGeneric {
			gs = append(gs, g.Decl.Text+"="+g.Value.Image())
		}
	}
	if len(gs) == 0 {
		return in.Path
	}
	return in.Path + " (" + strings.Join(gs, ", ") + ")"
}

// Evaluator returns the instance's evaluator: generics and generate
// parameters bound to their values.
func (in *Instance) Evaluator() *sema.Evaluator { return in.ev }

// Options configure Build.
type Options struct {
	// Tops names the top-level entities or configurations ("tb",
	// "lib.tb", "cfg"); empty means every project entity no
	// architecture instantiates.
	Tops []string
	// Steps is the budget of one evaluation (0: eval.EditorSteps).
	Steps int64
	// TotalSteps bounds the steps of every per-instance evaluation
	// together (0: 64 times Steps); past it the remaining instances
	// are incomplete.
	TotalSteps int64
	// MaxDepth bounds the nesting of instances (0: 256), so a
	// recursive design whose recursion is not static ends.
	MaxDepth int
	// Natives, if set, evaluates the calls of functions without a
	// body (sema.Evaluator.WithNatives): elaboration's std.env.
	Natives sema.Natives
	// Reporter, if set with Natives, receives the assertion and
	// report statements the elaboration's evaluations run
	// (sema.Evaluator.SetReporter).
	Reporter sema.Reporter
	// NativeBodies runs the ieee functions that have a native Go body
	// natively (sema.Evaluator.SetNativeBodies): elaboration for a
	// simulation (#59).
	NativeBodies bool
}

// Result is the hierarchy and its diagnostics.
type Result struct {
	Tops []*Instance
	// Diagnostics are the per-instance static errors (sema/static-error)
	// and step-limit warnings (sema/eval-limit), sorted.
	Diagnostics []diag.Diagnostic
	// Incomplete lists the instance paths whose evaluation hit the
	// step limit, sorted.
	Incomplete []string
	// Steps counts the steps of the per-instance evaluations.
	Steps int64
}

// Build elaborates the hierarchy of r.
func Build(r *sema.Result, o Options) *Result {
	base := sema.NewEvaluator(r, o.Steps)
	if o.Natives != nil {
		base = base.WithNatives(o.Natives)
		if o.Reporter != nil {
			base.SetReporter(o.Reporter)
		}
	}
	base.SetNativeBodies(o.NativeBodies)
	b := &builder{r: r, o: o, out: &Result{}, inline: inlineVunits(r),
		base:    base,
		exprs:   map[*sema.Unit]map[[2]int]*sema.Expr{},
		indep:   map[*sema.Decl]bool{},
		limited: map[*sema.Decl]bool{},
		asgDone: map[*sema.Assign]bool{}}
	if b.o.MaxDepth <= 0 {
		b.o.MaxDepth = 256
	}
	if b.o.Steps <= 0 {
		b.o.Steps = eval.EditorSteps
	}
	if b.o.TotalSteps <= 0 {
		b.o.TotalSteps = 64 * b.o.Steps
	}
	for _, top := range b.tops() {
		in := &Instance{Kind: KindEntity, Path: string(top.ent.Name),
			Entity: top.ent.Decl, Configuration: top.cfg,
			ev: b.base.Fresh()}
		b.defaults(in)
		b.entity(in, nil, top.bind, 0)
		b.out.Tops = append(b.out.Tops, in)
	}
	b.out.Steps = b.spent
	b.out.Diagnostics = diag.Normalize(b.out.Diagnostics)
	slices.Sort(b.out.Incomplete)
	b.out.Incomplete = slices.Compact(b.out.Incomplete)
	return b.out
}

type builder struct {
	r     *sema.Result
	o     Options
	out   *Result
	base  *sema.Evaluator
	exprs map[*sema.Unit]map[[2]int]*sema.Expr
	// indep marks declarations whose evaluation does not depend on the
	// instance; limited those that exceeded the step limit in some
	// instance; spent counts the steps of per-instance evaluations,
	// capped once it passes Options.TotalSteps (checks.go).
	indep   map[*sema.Decl]bool
	limited map[*sema.Decl]bool
	// asgDone marks assignments that need no further instance: they do
	// not depend on it, or exceeded the step limit once.
	asgDone map[*sema.Assign]bool
	spent   int64
	capped  bool
	// inline are the verification units bound in their declaration
	// (config.go).
	inline []*sema.Unit
}

type top struct {
	ent  *sema.Unit
	cfg  *sema.Unit
	bind *binding
}

// tops returns the top-level entities in canonical order.
func (b *builder) tops() []top {
	var ents, cfgs []*sema.Unit
	for _, u := range b.r.Units {
		if u.Decl == nil || u.Duplicate || u.Opaque {
			continue
		}
		switch u.Kind {
		case sema.UnitEntity:
			ents = append(ents, u)
		case sema.UnitConfiguration:
			cfgs = append(cfgs, u)
		}
	}
	if len(b.o.Tops) > 0 {
		var out []top
		named := func(u *sema.Unit, t string) bool {
			lib, name, ok := strings.Cut(t, ".")
			if !ok {
				lib, name = "", t
			}
			return sema.IdentKey(name) == u.Name && (lib == "" ||
				sema.IdentKey(lib) == u.Library)
		}
		for _, t := range b.o.Tops {
			for _, u := range ents {
				if named(u, t) {
					out = append(out, top{ent: u})
				}
			}
			for _, u := range cfgs {
				if !named(u, t) {
					continue
				}
				if bd := b.configuration(u); bd != nil &&
					bd.entity != nil && bd.entity.Unit != nil {
					out = append(out, top{ent: bd.entity.Unit, cfg: u,
						bind: bd})
				}
			}
		}
		return out
	}
	return defaultTops(b.r, ents)
}

// defaultTops returns the entities no other design instantiates: the
// entities of each strongly connected component of the instantiation
// graph that nothing outside the component instantiates, the first in
// canonical order for each such component. A recursive entity that
// only instantiates itself is a top, as the entity a user elaborates.
func defaultTops(r *sema.Result, ents []*sema.Unit) []top {
	idx := map[*sema.Unit]int{}
	byName := map[sema.Key][]int{}
	byLib := map[[2]sema.Key]int{}
	for i, u := range ents {
		idx[u] = i
		byName[u.Name] = append(byName[u.Name], i)
		byLib[[2]sema.Key{u.Library, u.Name}] = i
	}
	succ := make([][]int, len(ents))
	for _, u := range r.Units {
		var from int
		var ok bool
		switch u.Kind {
		case sema.UnitEntity:
			from, ok = idx[u]
		case sema.UnitArchitecture:
			from, ok = byLib[[2]sema.Key{u.Library, u.Of}]
		}
		if !ok {
			continue
		}
		walkStmts(u.Body, func(s *sema.Stmt) {
			if s.Kind != sema.StmtInstance {
				return
			}
			if s.Entity != nil && s.Entity.Unit != nil {
				if to, ok := idx[s.Entity.Unit]; ok {
					succ[from] = append(succ[from], to)
				}
			}
			if s.Component != nil {
				succ[from] = append(succ[from],
					byName[s.Component.Name]...)
			}
		})
	}
	comp := scc(succ)
	entered := map[int]bool{}
	for from, ts := range succ {
		for _, to := range ts {
			if comp[from] != comp[to] {
				entered[comp[to]] = true
			}
		}
	}
	var out []top
	seen := map[int]bool{}
	for i, u := range ents {
		c := comp[i]
		if !entered[c] && !seen[c] {
			seen[c] = true
			out = append(out, top{ent: u})
		}
	}
	return out
}

// scc returns the strongly connected component number of each node
// of the graph succ (Tarjan's algorithm).
func scc(succ [][]int) []int {
	n := len(succ)
	comp, low, num := make([]int, n), make([]int, n), make([]int, n)
	for i := range comp {
		comp[i], num[i] = -1, -1
	}
	var stack []int
	onStack := make([]bool, n)
	next, ncomp := 0, 0
	var visit func(v int)
	visit = func(v int) {
		num[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range succ[v] {
			switch {
			case num[w] < 0:
				visit(w)
				low[v] = min(low[v], low[w])
			case onStack[w]:
				low[v] = min(low[v], num[w])
			}
		}
		if low[v] != num[v] {
			return
		}
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			comp[w] = ncomp
			if w == v {
				break
			}
		}
		ncomp++
	}
	for v := range n {
		if num[v] < 0 {
			visit(v)
		}
	}
	return comp
}

func walkStmts(ss []*sema.Stmt, f func(*sema.Stmt)) {
	for _, s := range ss {
		f(s)
		walkStmts(s.Body, f)
		for _, a := range s.Alts {
			walkStmts(a.Body, f)
		}
	}
}

// entity elaborates the design entity of in, whose generics are
// bound: its architecture (the binding's, else the default) and the
// declarations and statements of both.
func (b *builder) entity(in *Instance, s *sema.Stmt, bd *binding,
	depth int,
) {
	eu := in.Entity.Unit
	var arch sema.Key
	var bc *blockConfig
	if bd != nil {
		arch, bc = bd.arch, bd.block
	}
	if bc != nil && bc.arch != "" {
		arch = bc.arch
	}
	in.Arch = b.architecture(eu, arch)
	in.Vunits = b.vunits(in, bd)
	if depth >= b.o.MaxDepth {
		// Not the step limit: the design nests deeper than any real
		// hierarchy, so its instantiation recurses without end.
		b.out.Diagnostics = append(b.out.Diagnostics, b.at(s,
			diag.New(diag.SemaError, "instance nesting exceeds "+
				strconv.Itoa(b.o.MaxDepth)+" levels at "+
				shortPath(in.Path)+": the instantiation is recursive "+
				"without end")))
		return
	}
	b.declarations(in, in.Entity.Region)
	b.stmts(in, eu.Body, scope{unit: eu, node: eu.Lib}, nil, depth)
	if in.Arch != nil {
		b.declarations(in, in.Arch.Region)
		b.stmts(in, in.Arch.Body, scope{unit: in.Arch,
			node: in.Arch.Lib}, bc, depth)
	}
}

// architecture picks the architecture of an entity: the one named, or
// the most recently analyzed (the last in canonical order).
func (b *builder) architecture(eu *sema.Unit, name sema.Key) *sema.Unit {
	l, ok := b.r.Library(eu.Library)
	if !ok {
		return nil
	}
	var pick *sema.Unit
	for _, a := range l.Secondary(eu.Name) {
		if a.Kind != sema.UnitArchitecture || a.Decl == nil {
			continue
		}
		if name != "" {
			if a.Name == name {
				return a
			}
			continue
		}
		pick = a
	}
	return pick
}

// interfaceDecls returns the generics (or ports) of a region in order.
func interfaceDecls(r *sema.Region, generics bool) []*sema.Decl {
	if r == nil {
		return nil
	}
	var out []*sema.Decl
	for _, d := range r.Decls {
		switch d.Kind {
		case sema.DeclGeneric, sema.DeclGenericType,
			sema.DeclGenericSubprogram, sema.DeclGenericPackage:
			if generics {
				out = append(out, d)
			}
		case sema.DeclPort:
			if !generics {
				out = append(out, d)
			}
		}
	}
	return out
}

// scope is the declarative part a statement list belongs to: its unit
// and the node whose declarations hold configuration specifications.
type scope struct {
	unit *sema.Unit
	node cst.Node
}

// stmts elaborates the concurrent statements ss inside in.
func (b *builder) stmts(in *Instance, ss []*sema.Stmt, sc scope,
	bc *blockConfig, depth int,
) {
	for _, s := range ss {
		switch s.Kind {
		case sema.StmtInstance:
			b.instance(in, s, sc, bc, depth)
		case sema.StmtBlock:
			c := &Instance{Kind: KindBlock, Path: in.Path + "." +
				label(s), Stmt: s, ev: in.ev, parent: in}
			b.declarations(c, s.Region)
			b.stmts(c, s.Body, scope{unit: sc.unit, node: s.Node},
				bc.nested(label(s), nil), depth+1)
			in.Children = append(in.Children, c)
		case sema.StmtForGenerate:
			b.forGenerate(in, s, sc, bc, depth)
		case sema.StmtIfGenerate:
			b.ifGenerate(in, s, sc, bc, depth)
		case sema.StmtCaseGenerate:
			b.caseGenerate(in, s, sc, bc, depth)
		}
	}
}

func label(s *sema.Stmt) string {
	if s.Label != nil {
		return string(s.Label.Name)
	}
	return "_"
}

// instance elaborates a component, entity or configuration
// instantiation.
func (b *builder) instance(in *Instance, s *sema.Stmt, sc scope,
	bc *blockConfig, depth int,
) {
	c := &Instance{Path: in.Path + "." + label(s), Stmt: s,
		Component: s.Component, Entity: s.Entity, parent: in}
	var bd *binding
	switch {
	case s.Config != nil && s.Config.Unit != nil:
		bd = b.configuration(s.Config.Unit)
		c.Configuration = s.Config.Unit
	case s.Entity != nil:
		bd = &binding{entity: s.Entity, arch: s.Arch}
	case s.Component != nil:
		bd = b.componentBinding(s, sc, bc)
		if bd != nil && bd.config != nil {
			c.Configuration = bd.config
		}
	}
	if bd != nil {
		c.Entity = bd.entity
	}
	if c.Entity == nil || c.Entity.Unit == nil {
		c.Kind = KindUnbound
		c.ev = in.ev
		in.Children = append(in.Children, c)
		return
	}
	c.Kind = KindEntity
	b.generics(c, s, bd, in.ev)
	b.ports(c, s, bd, in.ev)
	b.entity(c, s, bd, depth+1)
	in.Children = append(in.Children, c)
}

func (b *builder) forGenerate(in *Instance, s *sema.Stmt, sc scope,
	bc *blockConfig, depth int,
) {
	r, err := in.ev.Range(s.Range)
	if err != nil {
		b.limit(in, s, err, "the range of "+in.Path+"."+label(s))
		return
	}
	pt, perr := in.ev.Layout(s.Param.Type)
	const most = 1 << 16
	if r.Len() > most {
		b.out.Diagnostics = append(b.out.Diagnostics, b.at(s,
			diag.New(diag.SemaEvalLimit, "the "+itoa(r.Len())+
				" iterations of "+in.Path+"."+label(s))))
		b.out.Incomplete = append(b.out.Incomplete, in.Path)
		return
	}
	body := cst.ForGenerateStmt{Node: s.Node}.Body().Node
	for i := range r.Len() {
		v := eval.Int(eval.UniversalInteger, r.At(i))
		if perr == nil {
			v.T = pt
		}
		c := &Instance{Kind: KindGenerate, Stmt: s, Param: v, parent: in,
			Path: in.Path + "." + label(s) + "(" + v.Image() + ")",
			ev:   in.ev.Bind(s.Param, v)}
		b.declarations(c, s.Region)
		b.stmts(c, s.Body, scope{unit: sc.unit, node: body},
			bc.nested(label(s), &v), depth+1)
		in.Children = append(in.Children, c)
	}
}

func (b *builder) ifGenerate(in *Instance, s *sema.Stmt, sc scope,
	bc *blockConfig, depth int,
) {
	for i, alt := range s.Alts {
		if alt.Cond != nil {
			v, err := in.ev.Value(alt.Cond)
			if err != nil {
				b.limit(in, s, err, "the condition of "+in.Path+"."+
					label(s))
				return
			}
			if v.I == 0 {
				continue
			}
		}
		b.alternative(in, s, i, sc, bc, depth)
		return
	}
}

func (b *builder) caseGenerate(in *Instance, s *sema.Stmt, sc scope,
	bc *blockConfig, depth int,
) {
	sel, err := in.ev.Value(s.Selector)
	if err != nil {
		b.limit(in, s, err, "the selector of "+in.Path+"."+label(s))
		return
	}
	for i, alt := range s.Alts {
		hit := alt.Others
		for _, ch := range alt.Choices {
			if ch.Kind == sema.ExprRange {
				r, err := in.ev.Range(ch.Range)
				if err == nil && r.Contains(sel.I) {
					hit = true
				}
				continue
			}
			v, err := in.ev.Value(ch)
			if err == nil && v.Equal(sel) {
				hit = true
			}
		}
		if hit {
			b.alternative(in, s, i, sc, bc, depth)
			return
		}
	}
}

// alternative elaborates the chosen alternative i of an if or case
// generate.
func (b *builder) alternative(in *Instance, s *sema.Stmt, i int,
	sc scope, bc *blockConfig, depth int,
) {
	alt := &s.Alts[i]
	path := in.Path + "." + label(s)
	var altLabel string
	if alt.Label != nil {
		altLabel = string(alt.Label.Name)
		path += "." + altLabel
	}
	c := &Instance{Kind: KindGenerate, Path: path, Stmt: s, Alt: alt,
		ev: in.ev, parent: in}
	b.declarations(c, alt.Region)
	nb := bc.nested(label(s), nil)
	if altLabel != "" {
		if a := bc.nestedAlt(label(s), altLabel); a != nil {
			nb = a
		}
	}
	b.stmts(c, alt.Body, scope{unit: sc.unit, node: altBody(s, i)},
		nb, depth+1)
	in.Children = append(in.Children, c)
}

// altBody returns the generate body node of alternative i.
func altBody(s *sema.Stmt, i int) cst.Node {
	n := 0
	switch s.Kind {
	case sema.StmtIfGenerate:
		for br := range (cst.IfGenerateStmt{Node: s.Node}).Branches() {
			if n == i {
				return br.Body().Node
			}
			n++
		}
	case sema.StmtCaseGenerate:
		for a := range (cst.CaseGenerateStmt{Node: s.Node}).Alternatives() {
			if n == i {
				return a.Body().Node
			}
			n++
		}
	}
	return cst.Node{}
}

// limit reports a generate whose range or condition could not be
// evaluated because of the step limit, or failed with a design error;
// other failures (not static) are silent.
func (b *builder) limit(in *Instance, s *sema.Stmt, err error,
	what string,
) {
	switch c := eval.CodeOf(err); {
	case c == eval.ErrCodeBudget:
		b.out.Diagnostics = append(b.out.Diagnostics, b.at(s,
			diag.New(diag.SemaEvalLimit, what)))
		b.out.Incomplete = append(b.out.Incomplete, in.Path)
	case sema.DesignError(c):
		b.out.Diagnostics = append(b.out.Diagnostics, b.at(s,
			diag.New(diag.SemaStaticError, err.Error(), in.Describe())))
	}
}

// at locates d at statement s (a top's diagnostics have no position).
func (b *builder) at(s *sema.Stmt, d diag.Diagnostic) diag.Diagnostic {
	if s == nil {
		return d
	}
	return atNode(s, s.Node, d)
}

func atNode(s *sema.Stmt, n cst.Node, d diag.Diagnostic) diag.Diagnostic {
	if s == nil || s.Label == nil || s.Label.File == nil {
		return d
	}
	return inFile(s.Label.File, n, d)
}

// inFile locates d at node n of file f.
func inFile(f *sema.File, n cst.Node, d diag.Diagnostic) diag.Diagnostic {
	if f == nil || n.IsZero() {
		return d
	}
	sp := n.Span()
	l, c := f.Lines.LineCol(sp.Start)
	el, ec := f.Lines.LineCol(sp.End)
	return d.At(f.Path, l, c).To(el, ec)
}

// Walk calls f for every instance in preorder, tops in order.
func (r *Result) Walk(f func(*Instance)) {
	var walk func(in *Instance)
	walk = func(in *Instance) {
		f(in)
		for _, c := range in.Children {
			walk(c)
		}
	}
	for _, t := range r.Tops {
		walk(t)
	}
}

// shortPath abbreviates a deep instance path to its first three and
// last two names.
func shortPath(p string) string {
	parts := strings.Split(p, ".")
	if len(parts) <= 6 {
		return p
	}
	return strings.Join(parts[:3], ".") + "...." +
		strings.Join(parts[len(parts)-2:], ".") + " (" +
		strconv.Itoa(len(parts)) + " names)"
}

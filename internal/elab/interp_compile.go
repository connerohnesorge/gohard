package elab

// interp_compile.go lowers sema's statement trees into the
// interpreter's nodes: the sequential statements with what the typed
// tree does not carry (waveforms, delay mechanisms, wait sensitivity
// clauses, found from the syntax tree and the unit's typed
// expressions), and every concurrent statement as its equivalent
// process (LRM 11.4-11.7).

import (
	"errors"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/token"
)

// node is one executable statement.
type node struct {
	s   *sema.SeqStmt
	src sim.SrcRef
	// bundled marks a statement of a bundled std/ieee file, whose
	// src.Path is its bundle path (debugSrc).
	bundled bool
	// Children, shaped like s's: if branches, else, case alternatives,
	// loop and block bodies.
	branches [][]*node
	els      []*node
	alts     [][]*node
	body     []*node
	sig      *sigStmt
	wait     *waitStmt
}

// sigForm is the form of a signal assignment.
type sigForm uint8

const (
	sigSimple sigForm = iota
	sigCond
	sigSelected
	sigForce
	sigRelease
)

// sigStmt is a signal assignment (LRM 10.5).
type sigStmt struct {
	form     sigForm
	target   *sema.Expr
	delay    delayMech
	waves    [][]wave // one per branch or alternative
	unaff    []bool
	conds    []*sema.Expr
	selector *sema.Expr
	choices  [][]*sema.Expr
	others   []bool
	matching bool
	value    *sema.Expr // force
	out      bool       // force/release mode out
	hasMode  bool
}

type delayMech struct {
	transport bool
	reject    *sema.Expr
}

// wave is one waveform element.
type wave struct {
	value *sema.Expr
	after *sema.Expr
	null  bool
}

type waitStmt struct {
	on      []*sema.Expr
	hasOn   bool
	cond    *sema.Expr
	timeout *sema.Expr
}

// program is the code of one process.
type program struct {
	path   string
	inst   *hier.Instance
	region *sema.Region
	body   []*node
	// sensitive marks a process with a sensitivity list (or a
	// concurrent statement): sens is waited on at the end of body.
	sensitive bool
	sens      []sim.Target
	// implicit are the implicit signals ('stable, 'quiet, 'delayed,
	// 'transaction) of the attribute names in the process.
	implicit map[*sema.Expr]sim.SigID // lookup only
}

// progCache holds what every process of a design shares.
type progCache struct {
	d     *Design
	index map[*sema.File]map[nodeKey]*sema.Expr // lookup only
	nodes map[*sema.SeqStmt]*node               // lookup only
	// externals are the resolved external names; lookup only.
	externals map[extKey]External
}

type nodeKey struct {
	kind       cst.Kind
	start, end int
}

func newProgCache(d *Design) *progCache {
	return &progCache{d: d,
		index:     map[*sema.File]map[nodeKey]*sema.Expr{},
		nodes:     map[*sema.SeqStmt]*node{},
		externals: map[extKey]External{}}
}

func keyOf(n cst.Node) nodeKey {
	sp := n.Span()
	return nodeKey{n.Kind(), sp.Start, sp.End}
}

// expr returns the typed expression of syntax node n of file f.
func (pc *progCache) expr(f *sema.File, n cst.Node) *sema.Expr {
	if n.IsZero() || f == nil {
		return nil
	}
	idx, ok := pc.index[f]
	if !ok {
		idx = map[nodeKey]*sema.Expr{}
		for _, u := range f.Units {
			for _, e := range u.Exprs {
				if e == nil || e.Node.IsZero() {
					continue
				}
				k := keyOf(e.Node)
				if _, dup := idx[k]; !dup {
					idx[k] = e
				}
			}
		}
		pc.index[f] = idx
	}
	for range 16 {
		if e := idx[keyOf(n)]; e != nil {
			return e
		}
		// A parenthesized expression is the expression it encloses
		// (LRM 9.1): sema types the inner node.
		if n.Kind() != cst.KindParenExpr {
			return nil
		}
		n = cst.ParenExpr{Node: n}.Inner().Node
		if n.IsZero() {
			return nil
		}
	}
	return nil
}

// errNoExpr reports syntax the typed tree has no expression for.
func errNoExpr(n cst.Node) error {
	return errors.New("no typed expression for " + n.Text())
}

func (pc *progCache) mustExpr(f *sema.File, n cst.Node) (*sema.Expr,
	error,
) {
	if e := pc.expr(f, n); e != nil {
		return e, nil
	}
	return nil, errNoExpr(n)
}

// seq lowers a statement list of file f.
func (pc *progCache) seq(f *sema.File, ss []*sema.SeqStmt) ([]*node,
	error,
) {
	out := make([]*node, 0, len(ss))
	for _, s := range ss {
		n, err := pc.node(f, s)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func (pc *progCache) node(f *sema.File, s *sema.SeqStmt) (*node, error) {
	if n, ok := pc.nodes[s]; ok {
		return n, nil
	}
	n := &node{s: s, src: srcOf(f, s.Node.Span()),
		bundled: f != nil && f.Bundled}
	pc.nodes[s] = n
	var err error
	for _, br := range s.Branches {
		b, e := pc.seq(f, br.Body)
		err = errors.Join(err, e)
		n.branches = append(n.branches, b)
	}
	for _, a := range s.Alts {
		b, e := pc.seq(f, a.Body)
		err = errors.Join(err, e)
		n.alts = append(n.alts, b)
	}
	var e1, e2 error
	n.els, e1 = pc.seq(f, s.Else)
	n.body, e2 = pc.seq(f, s.Body)
	err = errors.Join(err, e1, e2)
	switch s.Kind {
	case sema.SeqSignal:
		n.sig, e1 = pc.sigStmt(f, s.Node, s.Target)
		err = errors.Join(err, e1)
	case sema.SeqWait:
		n.wait, e1 = pc.waitStmt(f, s)
		err = errors.Join(err, e1)
	case sema.SeqOther:
		err = errors.Join(err, sema.NewUnsupported("simulating the "+
			"statement "+firstLine(s.Node.Text()), nil))
	}
	if err != nil {
		delete(pc.nodes, s)
		return nil, err
	}
	return n, nil
}

func firstLine(s string) string {
	for i := range len(s) {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

func (pc *progCache) waitStmt(f *sema.File, s *sema.SeqStmt) (*waitStmt,
	error,
) {
	w := &waitStmt{cond: s.Cond, timeout: s.Value}
	ws := cst.WaitStmt{Node: s.Node}
	if sc := ws.Sensitivity(); !sc.IsZero() {
		w.hasOn = true
		for nm := range sc.List().Names() {
			e, err := pc.mustExpr(f, nm.Node)
			if err != nil {
				return nil, err
			}
			w.on = append(w.on, e)
		}
	}
	return w, nil
}

// sigStmt lowers the signal assignment statement st (sequential or
// concurrent) whose typed target is target.
func (pc *progCache) sigStmt(f *sema.File, st cst.Node,
	target *sema.Expr,
) (*sigStmt, error) {
	g := &sigStmt{target: target}
	var err error
	delay := func(d cst.DelayMechanism) {
		if d.IsZero() {
			return
		}
		g.delay.transport = !d.Node.Token(token.KwTransport, 0).IsZero()
		if r := d.Reject(); !r.IsZero() {
			g.delay.reject = pc.expr(f, r.Node)
			if g.delay.reject == nil {
				err = errors.Join(err, errNoExpr(r.Node))
			}
		}
	}
	whens := func(brs func(func(cst.WhenBranch) bool)) {
		for br := range brs {
			ws, un, e := pc.whenValue(f, br.Value())
			err = errors.Join(err, e)
			g.waves, g.unaff = append(g.waves, ws), append(g.unaff, un)
			var c *sema.Expr
			if cn := childAfter(br.Node, token.KwWhen); !cn.IsZero() {
				c = pc.expr(f, cn)
				if c == nil {
					err = errors.Join(err, errNoExpr(cn))
				}
			}
			g.conds = append(g.conds, c)
		}
	}
	selects := func(sel cst.Expr, brs func(func(cst.SelectBranch) bool)) {
		g.selector = pc.expr(f, sel.Node)
		if g.selector == nil {
			err = errors.Join(err, errNoExpr(sel.Node))
		}
		g.matching = !st.Token(token.Question, 0).IsZero()
		for br := range brs {
			ws, un, e := pc.whenValue(f, br.Value())
			err = errors.Join(err, e)
			g.waves, g.unaff = append(g.waves, ws), append(g.unaff, un)
			var cs []*sema.Expr
			others := false
			for ch := range br.Choices().Items() {
				if ch.Kind() == cst.KindOthersChoice {
					others = true
					continue
				}
				c := pc.expr(f, ch.Node)
				if c == nil {
					err = errors.Join(err, sema.NewUnsupported(
						"the choice "+ch.Node.Text()+" of a "+
							"selected signal assignment", nil))
					continue
				}
				cs = append(cs, c)
			}
			g.choices, g.others = append(g.choices, cs),
				append(g.others, others)
		}
	}
	targetOf := func(t cst.Target) {
		if g.target != nil {
			return
		}
		g.target = pc.expr(f, t.Target().Node)
		if g.target == nil {
			err = errors.Join(err, errNoExpr(t.Target().Node))
		}
	}
	switch st.Kind() {
	case cst.KindSignalAssignStmt:
		s := cst.SignalAssignStmt{Node: st}
		targetOf(s.Target())
		delay(s.Delay())
		ws, e := pc.waveform(f, s.Waveform())
		err = errors.Join(err, e)
		g.waves, g.unaff = [][]wave{ws}, []bool{false}
		g.conds = []*sema.Expr{nil}
	case cst.KindConcurrentSimpleSignalAssign:
		s := cst.ConcurrentSimpleSignalAssign{Node: st}
		targetOf(s.Target())
		delay(s.Delay())
		ws, e := pc.waveform(f, s.Waveform())
		err = errors.Join(err, e)
		g.waves, g.unaff = [][]wave{ws}, []bool{false}
		g.conds = []*sema.Expr{nil}
	case cst.KindConditionalSignalAssignStmt:
		s := cst.ConditionalSignalAssignStmt{Node: st}
		g.form = sigCond
		targetOf(s.Target())
		delay(s.Delay())
		whens(s.Waveforms().Branches())
	case cst.KindConcurrentConditionalSignalAssign:
		s := cst.ConcurrentConditionalSignalAssign{Node: st}
		g.form = sigCond
		targetOf(s.Target())
		delay(s.Delay())
		whens(s.Waveforms().Branches())
	case cst.KindSelectedSignalAssignStmt:
		s := cst.SelectedSignalAssignStmt{Node: st}
		g.form = sigSelected
		targetOf(s.Target())
		delay(s.Delay())
		selects(s.Selector(), s.Waveforms().Branches())
	case cst.KindConcurrentSelectedSignalAssign:
		s := cst.ConcurrentSelectedSignalAssign{Node: st}
		g.form = sigSelected
		targetOf(s.Target())
		delay(s.Delay())
		selects(s.Selector(), s.Waveforms().Branches())
	case cst.KindForceAssignStmt:
		s := cst.ForceAssignStmt{Node: st}
		g.form = sigForce
		targetOf(s.Target())
		if v := s.Value(); !v.IsZero() {
			g.value = pc.expr(f, v.Node)
			if g.value == nil {
				err = errors.Join(err, errNoExpr(v.Node))
			}
		}
		g.mode(s.Mode())
	case cst.KindReleaseAssignStmt:
		s := cst.ReleaseAssignStmt{Node: st}
		g.form = sigRelease
		targetOf(s.Target())
		g.mode(s.Mode())
	default:
		return nil, sema.NewUnsupported("simulating the signal "+
			"assignment "+firstLine(st.Text()), nil)
	}
	if !st.Token(token.KwGuarded, 0).IsZero() {
		err = errors.Join(err, sema.NewUnsupported(
			"guarded signal assignments", nil))
	}
	return g, err
}

func (g *sigStmt) mode(t cst.Token) {
	if t.IsZero() {
		return
	}
	g.hasMode = true
	g.out = t.Kind() == token.KwOut
}

// whenValue lowers the value of a conditional or selected branch: a
// waveform, an expression (one element after 0 ns) or unaffected.
func (pc *progCache) whenValue(f *sema.File, v cst.WhenValue) ([]wave,
	bool, error,
) {
	switch {
	case v.IsZero() || v.Kind() == cst.KindUnaffectedExpr:
		return nil, true, nil
	case v.Kind() == cst.KindWaveform:
		ws, err := pc.waveform(f, cst.Waveform{Node: v.Node})
		return ws, false, err
	}
	e, err := pc.mustExpr(f, v.Node)
	if err != nil {
		return nil, false, err
	}
	return []wave{{value: e, null: isNull(e)}}, false, nil
}

func (pc *progCache) waveform(f *sema.File, w cst.Waveform) ([]wave,
	error,
) {
	var out []wave
	for el := range w.WaveformElements() {
		var x wave
		if v := el.Value(); !v.IsZero() {
			e, err := pc.mustExpr(f, v.Node)
			if err != nil {
				return nil, err
			}
			x.value, x.null = e, isNull(e)
		} else {
			x.null = true
		}
		if a := el.After(); !a.IsZero() {
			e, err := pc.mustExpr(f, a.Node)
			if err != nil {
				return nil, err
			}
			x.after = e
		}
		out = append(out, x)
	}
	return out, nil
}

func isNull(e *sema.Expr) bool {
	return e != nil && e.Kind == sema.ExprLiteral && !e.Lit.IsZero() &&
		e.Lit.Kind() == token.KwNull
}

// childAfter returns the first child node after the first token of
// kind k.
func childAfter(n cst.Node, k token.Kind) cst.Node {
	seen := false
	for e := range n.Elements() {
		if e.IsToken() {
			if e.Token.Kind() == k {
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

// program builds the code of process p (nil for statements the
// interpreter does not run).
func (w *world) program(p *Proc) (*program, error) {
	pc := w.prog
	s := p.Stmt
	prog := &program{path: p.Path, inst: p.Inst,
		implicit: map[*sema.Expr]sim.SigID{}}
	switch {
	case s.Kind == sema.StmtProcess:
		prog.region = s.Region
		body, err := pc.seq(p.File, s.Seq)
		if err != nil {
			return nil, err
		}
		prog.body = body
		ps := cst.ProcessStmt{Node: s.Node}.Sensitivity()
		switch {
		case ps.IsZero():
		case !ps.All().IsZero():
			prog.sensitive = true
			prog.sens = pc.reads(p, body, nil)
		default:
			prog.sensitive = true
			for nm := range ps.Names() {
				e, err := pc.mustExpr(p.File, nm.Node)
				if err != nil {
					return nil, err
				}
				t, ok := pc.d.staticTarget(p.Inst, e, p.Ev)
				if !ok && e.Kind == sema.ExprAttr {
					// S'TRANSACTION, S'STABLE(T), ...: an implicit
					// signal.
					id, found, err := pc.implicitOf(p, e)
					if err != nil {
						return nil, err
					}
					if found {
						prog.implicit[e] = id
						t, ok = sim.Target{Sig: id, N: leaves(
							pc.d.Model.Signals[id].Init)}, true
					}
				}
				if !ok {
					return nil, errors.New("the sensitivity list " +
						"names " + nm.Node.Text() + ", not a static " +
						"signal name")
				}
				prog.sens = appendTarget(prog.sens, t)
			}
		}
	default:
		n, err := pc.concurrent(p)
		if err != nil || n == nil {
			return nil, err
		}
		prog.body = []*node{n}
		prog.sensitive = true
		prog.sens = pc.reads(p, prog.body, nil)
		if n.sig != nil && n.sig.target != nil {
			// The driver exists from initialization (LRM 14.7.2).
			for _, t := range targetsOf(n.sig.target) {
				if tg, ok := pc.d.staticTarget(p.Inst, t, p.Ev); ok {
					pr := &pc.d.Model.Processes[p.Index]
					pr.Drivers = appendTarget(pr.Drivers, tg)
				}
			}
		}
	}
	if err := pc.implicitSignals(p, prog); err != nil {
		return nil, err
	}
	return prog, nil
}

// targetsOf returns the names of a target: the name, or the elements
// of an aggregate target.
func targetsOf(e *sema.Expr) []*sema.Expr {
	if e == nil {
		return nil
	}
	if e.Kind != sema.ExprAggregate {
		return []*sema.Expr{e}
	}
	var out []*sema.Expr
	for _, a := range e.Assocs {
		out = append(out, targetsOf(a.Value)...)
	}
	return out
}

func appendTarget(ts []sim.Target, t sim.Target) []sim.Target {
	for _, x := range ts {
		if x == t {
			return ts
		}
	}
	return append(ts, t)
}

// concurrent lowers a concurrent statement to the statement of its
// equivalent process.
func (pc *progCache) concurrent(p *Proc) (*node, error) {
	st := p.Stmt.Node
	f := p.File
	n := &node{src: srcOf(f, st.Span())}
	switch st.Kind() {
	case cst.KindConcurrentSimpleSignalAssign,
		cst.KindConcurrentConditionalSignalAssign,
		cst.KindConcurrentSelectedSignalAssign:
		g, err := pc.sigStmt(f, st, nil)
		if err != nil {
			return nil, err
		}
		n.s = &sema.SeqStmt{Kind: sema.SeqSignal, Node: st,
			Target: g.target}
		n.sig = g
	case cst.KindConcurrentAssertionStmt:
		s := cst.ConcurrentAssertionStmt{Node: st}
		q := &sema.SeqStmt{Kind: sema.SeqAssert, Node: st}
		var err error
		if q.Cond, err = pc.mustExpr(f, s.Condition().Node); err != nil {
			return nil, err
		}
		if r := childAfter(st, token.KwReport); !r.IsZero() {
			if q.Report, err = pc.mustExpr(f, r); err != nil {
				return nil, err
			}
		}
		if sv := childAfter(st, token.KwSeverity); !sv.IsZero() {
			if q.Severity, err = pc.mustExpr(f, sv); err != nil {
				return nil, err
			}
		}
		n.s = q
	case cst.KindConcurrentProcedureCallStmt:
		c := cst.ConcurrentProcedureCallStmt{Node: st}.Call()
		e, err := pc.mustExpr(f, c.Node)
		if err != nil {
			return nil, err
		}
		n.s = &sema.SeqStmt{Kind: sema.SeqCall, Node: st, Call: e}
	default:
		return nil, nil // PSL and others: another hook's
	}
	return n, nil
}

// implicitSignals declares the implicit signals of the attribute
// names of prog (S'STABLE(T) and the others): the model must have
// them before the kernel starts.
func (pc *progCache) implicitSignals(p *Proc, prog *program) error {
	var err error
	walkProgram(prog.body, func(e *sema.Expr) {
		if e.Kind != sema.ExprAttr || err != nil {
			return
		}
		id, ok, ierr := pc.implicitOf(p, e)
		if ierr != nil {
			err = ierr
		} else if ok {
			prog.implicit[e] = id
		}
	})
	return err
}

// implicitOf returns the implicit signal of the attribute name e
// (found false for another attribute, or a prefix that is no static
// signal name: a signal parameter, an error if it runs).
func (pc *progCache) implicitOf(p *Proc, e *sema.Expr) (sim.SigID, bool,
	error,
) {
	var kind sim.ImplicitKind
	switch e.Attr {
	case "stable":
		kind = sim.ImplicitStable
	case "quiet":
		kind = sim.ImplicitQuiet
	case "transaction":
		kind = sim.ImplicitTransaction
	case "delayed":
		kind = sim.ImplicitDelayed
	default:
		return 0, false, nil
	}
	t, _, terr := pc.d.Target(p.Inst, e.Prefix, p.Ev)
	if terr != nil {
		return 0, false, nil
	}
	if t.First != 0 || t.N != leaves(pc.d.Model.Signals[t.Sig].Init) {
		return 0, false, sema.NewUnsupported("the attribute '"+
			string(e.Attr)+" of a part of a signal", nil)
	}
	var delay int64
	if len(e.Args) > 0 {
		v, err := p.Ev.Value(e.Args[0])
		if err != nil {
			return 0, false, errors.New("the delay of '" +
				string(e.Attr) + " must be static: " + err.Error())
		}
		delay = v.I
	}
	if delay < 0 {
		return 0, false, errors.New("negative delay in '" +
			string(e.Attr))
	}
	// A port collapsed onto its whole actual is the actual's net: the
	// implicit signal starts from the actual's initial value.
	sig := t.Sig
	for range 64 {
		s := &pc.d.Model.Signals[sig]
		if !s.Collapsed || s.Offset != 0 ||
			leaves(pc.d.Model.Signals[s.Of].Init) != leaves(s.Init) {
			break
		}
		sig = s.Of
	}
	return pc.d.Model.Implicit(kind, sig, delay), true, nil
}

// reads returns the signals read by the statements ns (LRM 10.2, the
// sensitivity set of process(all) and of concurrent statements): the
// longest static prefix of every signal name read.
func (pc *progCache) reads(p *Proc, ns []*node, out []sim.Target,
) []sim.Target {
	walkReads(ns, func(e *sema.Expr) bool {
		if e.Kind == sema.ExprAttr {
			id, ok, err := pc.implicitOf(p, e)
			if err != nil || !ok {
				return false
			}
			out = appendTarget(out, sim.Target{Sig: id,
				N: leaves(pc.d.Model.Signals[id].Init)})
			return true
		}
		if t, ok := pc.d.staticTarget(p.Inst, e, p.Ev); ok {
			out = appendTarget(out, t)
			return true
		}
		return false
	})
	return out
}

// walkProgram calls f on every expression (and subexpression) of ns.
func walkProgram(ns []*node, f func(*sema.Expr)) {
	var expr func(e *sema.Expr)
	expr = func(e *sema.Expr) {
		if e == nil {
			return
		}
		f(e)
		expr(e.Prefix)
		for _, a := range e.Args {
			expr(a)
		}
		for _, a := range e.Assocs {
			for _, c := range a.Choices {
				expr(c)
			}
			expr(a.Value)
		}
		walkRange(e.Range, expr)
	}
	forNodes(ns, func(n *node) { nodeExprs(n, expr, expr) })
}

// walkReads calls name on every name read by ns, outermost first; when
// name returns true, the name's prefixes are not visited (index
// expressions still are).
func walkReads(ns []*node, name func(*sema.Expr) bool) {
	var read func(e *sema.Expr)
	read = func(e *sema.Expr) {
		if e == nil {
			return
		}
		switch e.Kind {
		case sema.ExprName, sema.ExprIndex, sema.ExprSlice,
			sema.ExprSelect:
			if name(e) {
				indices(e, read)
				return
			}
		case sema.ExprAttr:
			// An attribute that is a signal ('delayed, 'stable,
			// 'quiet, 'transaction) is read itself; 'event and the
			// others read their prefix ('length and the other array
			// attributes do not, but including it is harmless).
			if name(e) {
				for _, a := range e.Args {
					read(a)
				}
				return
			}
		}
		read(e.Prefix)
		for _, a := range e.Args {
			read(a)
		}
		for _, a := range e.Assocs {
			for _, c := range a.Choices {
				read(c)
			}
			read(a.Value)
		}
		walkRange(e.Range, read)
	}
	// A target is written, not read; its indices are read.
	written := func(e *sema.Expr) {
		for _, t := range targetsOf(e) {
			indices(t, read)
		}
	}
	forNodes(ns, func(n *node) { nodeExprs(n, read, written) })
}

// indices calls f on the index and slice expressions of name e.
func indices(e *sema.Expr, f func(*sema.Expr)) {
	for e != nil {
		switch e.Kind {
		case sema.ExprIndex:
			for _, a := range e.Args {
				f(a)
			}
		case sema.ExprSlice:
			walkRange(e.Range, f)
		case sema.ExprSelect:
		default:
			return
		}
		e = e.Prefix
	}
}

func walkRange(rc *sema.RangeCons, f func(*sema.Expr)) {
	if rc == nil {
		return
	}
	f(rc.Left)
	f(rc.Right)
	f(rc.Attr)
}

func forNodes(ns []*node, f func(*node)) {
	for _, n := range ns {
		f(n)
		for _, b := range n.branches {
			forNodes(b, f)
		}
		forNodes(n.els, f)
		for _, a := range n.alts {
			forNodes(a, f)
		}
		forNodes(n.body, f)
	}
}

// nodeExprs calls read on the expressions node n reads and target on
// its assignment targets.
func nodeExprs(n *node, read, target func(*sema.Expr)) {
	s := n.s
	if s == nil {
		return
	}
	switch s.Kind {
	case sema.SeqVarAssign, sema.SeqCondVarAssign, sema.SeqSelVarAssign:
		target(s.Target)
	case sema.SeqSignal:
	default:
		read(s.Target)
	}
	read(s.Value)
	read(s.Cond)
	read(s.Selector)
	callExprs(s.Call, read, target)
	read(s.Report)
	read(s.Severity)
	walkRange(s.Range, read)
	for _, b := range s.Branches {
		read(b.Cond)
		read(b.Value)
	}
	for _, a := range s.Alts {
		for _, c := range a.Choices {
			read(c)
		}
		read(a.Value)
	}
	if g := n.sig; g != nil {
		target(g.target)
		read(g.delay.reject)
		for _, ws := range g.waves {
			for _, w := range ws {
				read(w.value)
				read(w.after)
			}
		}
		for _, c := range g.conds {
			read(c)
		}
		read(g.selector)
		for _, cs := range g.choices {
			for _, c := range cs {
				read(c)
			}
		}
		read(g.value)
	}
	if w := n.wait; w != nil {
		for _, e := range w.on {
			read(e)
		}
	}
}

// callExprs splits the actuals of a procedure call: those of out
// parameters are written, the others read.
func callExprs(e *sema.Expr, read, target func(*sema.Expr)) {
	if e == nil || e.Decl == nil {
		read(e)
		return
	}
	x := e.Decl.Denotes()
	acts, err := actualsOf(e, x)
	if err != nil {
		read(e)
		return
	}
	read(e.Prefix)
	for i, a := range acts {
		if a == nil || i < len(x.Params) && a == x.Params[i].Value {
			continue
		}
		if i < len(x.Params) && x.Params[i].Mode == sema.ModeOut {
			target(a)
			continue
		}
		read(a)
	}
}

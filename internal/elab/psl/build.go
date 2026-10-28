package psl

import (
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/elab"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/sim"
	"github.com/connerohnesorge/gohard/internal/trace"
)

// CodeFunc builds a process's behaviour (elab.Options.Code).
type CodeFunc = func(*elab.Design, *elab.Proc) (sim.Code, error)

// Wrap returns a CodeFunc that runs PSL directives itself and gives
// every other process to next (the sequential interpreter; nil gives
// those processes no code).
func Wrap(next CodeFunc) CodeFunc {
	return func(d *elab.Design, p *elab.Proc) (sim.Code, error) {
		if c, ok, err := Code(d, p); ok {
			return c, err
		}
		if next == nil {
			return nil, nil
		}
		return next(d, p)
	}
}

// IsDirective reports whether n is a PSL verification directive.
func IsDirective(n cst.Node) bool {
	switch n.Kind() {
	case cst.KindPSLAssert, cst.KindPSLCover, cst.KindPSLAssume,
		cst.KindPSLAssumeGuarantee, cst.KindPSLRestrict,
		cst.KindPSLRestrictGuarantee, cst.KindPSLFairness:
		return true
	}
	return false
}

// Code builds the behaviour of p when p is a PSL directive (ok false
// otherwise): a process that samples the directive's booleans at every
// tick of its clock.
func Code(d *elab.Design, p *elab.Proc) (sim.Code, bool, error) {
	if p == nil || p.Stmt == nil || !IsDirective(p.Stmt.Node) {
		return nil, false, nil
	}
	dir := compile(d, p)
	return &runner{dir: dir}, true, nil
}

type dKind uint8

const (
	dIgnore dKind = iota // assume, restrict, fairness: no effect
	dAssert
	dCover
)

// directive is the compiled, immutable part of one PSL directive.
type directive struct {
	kind     dKind
	label    string
	src      sim.SrcRef
	prop     *prop // assert
	seq      *sere // cover
	bools    []*sema.Expr
	clock    clockSpec
	sigs     []sigRef
	message  string
	hasMsg   bool
	severity trace.Severity
	// formal names the construct that needs formal verification;
	// unsup a construct the simulator does not run.
	formal string
	unsup  string
	ev     *sema.Evaluator
	// ante is the antecedent of an assert whose property is an
	// implication (under always or not): the assert is covered
	// (PslIsAssertCovered) once ante matches. anteAll starts a match
	// at every tick (under always), else at the first tick only. An
	// assert without an antecedent is covered at its first tick.
	ante    *sere
	anteAll bool
	// builtins are the built-in function calls of bools, inner calls
	// first (builtin.go).
	builtins []*builtin
}

type sigRef struct {
	decl *sema.Decl
	id   sim.SigID
}

type edge uint8

const (
	level edge = iota // tick when cond holds and a signal changed
	rise              // rising_edge(sig)
	fall              // falling_edge(sig)
	event             // sig'event and cond
)

type clockSpec struct {
	on   []sim.SigID
	edge edge
	sig  sim.SigID
	cond *sema.Expr
}

type builder struct {
	d     *elab.Design
	p     *elab.Proc
	f     *sema.File
	dir   *directive
	bools map[cst.Range]int
	calls map[*sema.Expr]bool // the built-in calls compiled
}

func compile(d *elab.Design, p *elab.Proc) *directive {
	n := p.Stmt.Node
	dir := &directive{src: d.Model.Processes[p.Index].Src, ev: p.Ev,
		severity: trace.Error}
	dir.label = p.Path[strings.LastIndexByte(p.Path, '.')+1:]
	if p.Stmt.Label != nil {
		dir.label = string(p.Stmt.Label.Name)
	}
	b := &builder{d: d, p: p, f: p.File, dir: dir,
		bools: map[cst.Range]int{}, calls: map[*sema.Expr]bool{}}
	switch n.Kind() {
	case cst.KindPSLAssert:
		dir.kind = dAssert
	case cst.KindPSLCover:
		dir.kind = dCover
	default:
		return dir
	}
	var body, clk cst.Node
	for c := range n.Nodes() {
		switch c.Kind() {
		case cst.KindLabel:
		case cst.KindPSLReport:
			b.report(c)
		default:
			if body.IsZero() {
				body = c
			}
		}
	}
	if body.Kind() == cst.KindPSLClockedProp {
		kids := slices.Collect(body.Nodes())
		if len(kids) == 2 {
			body, clk = kids[0], kids[1]
		}
	}
	if f := firstOf(
		body,
		cst.KindPSLOBEUnary,
		cst.KindPSLOBEUntil,
	); !f.IsZero() {
		dir.formal = "the branching-time operator " +
			strings.ToUpper(firstText(f))
		return dir
	}
	if clk.IsZero() {
		clk = defaultClock(n)
	}
	if clk.IsZero() {
		word := "assert"
		if dir.kind == dCover {
			word = "cover"
		}
		dir.formal = "the unclocked " + word + " " + dir.label
		return dir
	}
	if dir.kind == dAssert {
		dir.prop = b.prop(body)
		dir.ante, dir.anteAll = b.antecedent(body)
	} else {
		dir.seq = b.sere(body)
	}
	b.clock(clk)
	return dir
}

// antecedent returns the antecedent sequence of an assert's property
// (nil when it is not an implication) and whether the implication is
// under always: `always {s} |-> p`, `always (b -> p)`, `{s} |=> p`.
// Its booleans are the property's own (b.boolean memoizes them).
func (b *builder) antecedent(n cst.Node) (*sere, bool) {
	always := false
	for {
		switch n.Kind() {
		case cst.KindPSLParenProp, cst.KindPSLAlways:
			always = always || n.Kind() == cst.KindPSLAlways
			n = firstKid(n)
			continue
		case cst.KindPSLSuffixImplication:
			return b.sere(firstKid(n)), always
		case cst.KindPSLImplication:
			l := firstKid(n)
			if has(n, "<->") || !b.isBoolean(l) {
				return nil, false
			}
			return boolS(b.boolean(l), false), always
		}
		return nil, false
	}
}

// firstKid is n's first child node (zero when it has none).
func firstKid(n cst.Node) cst.Node {
	for c := range n.Nodes() {
		return c
	}
	return cst.Node{}
}

// firstOf returns the first node of n's subtree of one of the kinds.
func firstOf(n cst.Node, ks ...cst.Kind) cst.Node {
	if n.IsZero() {
		return cst.Node{}
	}
	if slices.Contains(ks, n.Kind()) {
		return n
	}
	for c := range n.Descendants() {
		if slices.Contains(ks, c.Kind()) {
			return c
		}
	}
	return cst.Node{}
}

func firstText(n cst.Node) string {
	for t := range n.Tokens() {
		return t.Text()
	}
	return ""
}

// words returns n's own tokens in lower case.
func words(n cst.Node) []string {
	var out []string
	for t := range n.Tokens() {
		out = append(out, strings.ToLower(t.Text()))
	}
	return out
}

// defaultClock finds the default clock declaration of the innermost
// region enclosing the directive n. Only a declaration written before n
// counts: a `-- psl default clock` in the statement part (psl.comments)
// clocks the later directives only, as GHDL and NVC read it (#109).
func defaultClock(n cst.Node) cst.Node {
	at := n.Span().Start
	for a := range n.Ancestors() {
		for c := range a.Nodes() {
			if c.Span().Start >= at {
				break
			}
			if c.Kind() == cst.KindPSLDefaultClock {
				return clockExpr(c)
			}
			for g := range c.Nodes() {
				if g.Kind() == cst.KindPSLDefaultClock &&
					g.Span().Start < at {
					return clockExpr(g)
				}
			}
		}
		if a.Kind() == cst.KindArchitectureBody {
			break
		}
	}
	return cst.Node{}
}

func clockExpr(dc cst.Node) cst.Node {
	return cst.PSLDefaultClock{Node: dc}.Clock().Node
}

func (b *builder) unsupported(what string) {
	if b.dir.unsup == "" {
		b.dir.unsup = what + " in a PSL directive"
	}
}

// report reads the report and severity of the directive.
func (b *builder) report(n cst.Node) {
	r := cst.PSLReport{Node: n}
	if m := r.Report().Node; !m.IsZero() {
		b.dir.message, b.dir.hasMsg = b.text(m), true
	}
	if s := r.Severity().Node; !s.IsZero() {
		if e := b.f.PSLExpr(s); e != nil {
			if v, err := b.p.Ev.Value(e); err == nil &&
				v.T != nil && v.T.Kind == eval.KindEnum {
				b.dir.severity = trace.Severity(v.I + 1)
			}
		}
	}
}

// text is the value of a static string expression (a literal when the
// evaluator cannot type it).
func (b *builder) text(n cst.Node) string {
	if e := b.f.PSLExpr(n); e != nil {
		if v, err := b.p.Ev.Value(e); err == nil {
			if s, ok := stringOf(v); ok {
				return s
			}
		}
	}
	s := strings.TrimSpace(n.Text())
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.ReplaceAll(s[1:len(s)-1], `""`, `"`)
	}
	return s
}

func stringOf(v eval.Value) (string, bool) {
	if v.T == nil || v.T.Kind != eval.KindArray || v.T.Elem == nil ||
		v.T.Elem.Kind != eval.KindEnum {
		return "", false
	}
	var sb strings.Builder
	for _, e := range v.E {
		lits := v.T.Elem.Literals
		if e.I < 0 || int(e.I) >= len(lits) {
			return "", false
		}
		l := lits[e.I]
		if len(l) == 3 && l[0] == '\'' {
			sb.WriteByte(l[1])
			continue
		}
		return "", false
	}
	return sb.String(), true
}

// expr returns the typed expression of n (a PSL boolean or a VHDL
// expression in PSL code).
func (b *builder) expr(n cst.Node) *sema.Expr {
	if e := b.f.PSLExpr(n); e != nil {
		return e
	}
	for c := range n.Nodes() {
		if e := b.f.PSLExpr(c); e != nil {
			return e
		}
	}
	return nil
}

// isBoolean reports whether the PSL operand n is a Boolean: a
// PSLBoolean, or a call of a built-in function sema typed.
func (b *builder) isBoolean(n cst.Node) bool {
	switch n.Kind() {
	case cst.KindPSLBoolean:
		return true
	case cst.KindPSLBuiltinCall:
		return sema.PSLBuiltin(b.f.PSLExpr(n)) != ""
	}
	return false
}

// boolean returns the index of the boolean n in the directive's
// letter, adding it on first use.
func (b *builder) boolean(n cst.Node) int {
	if i, ok := b.bools[n.Span()]; ok {
		return i
	}
	e := b.expr(n)
	if e == nil {
		b.unsupported("the expression " + strings.TrimSpace(n.Text()))
	}
	b.builtins(e)
	b.signals(e)
	i := len(b.dir.bools)
	b.dir.bools = append(b.dir.bools, e)
	b.bools[n.Span()] = i
	return i
}

// signals records the signals e reads.
func (b *builder) signals(e *sema.Expr) {
	if e == nil {
		return
	}
	if e.Kind == sema.ExprName && e.Decl != nil {
		x := e.Decl.Denotes()
		if x.Kind == sema.DeclSignal || x.Kind == sema.DeclPort {
			if id, ok := b.d.Signal(b.p.Inst, x); ok &&
				!slices.ContainsFunc(b.dir.sigs, func(s sigRef) bool {
					return s.decl == x
				}) {
				b.dir.sigs = append(b.dir.sigs, sigRef{x, id})
			}
		}
	}
	b.signals(e.Prefix)
	for _, a := range e.Args {
		b.signals(a)
	}
	for _, a := range e.Assocs {
		for _, c := range a.Choices {
			b.signals(c)
		}
		b.signals(a.Value)
	}
}

// signalOf returns the signal the name e denotes.
func (b *builder) signalOf(e *sema.Expr) (sim.SigID, bool) {
	if e == nil || e.Kind != sema.ExprName || e.Decl == nil {
		return 0, false
	}
	return b.d.Signal(b.p.Inst, e.Decl.Denotes())
}

// clock compiles the clock expression n.
func (b *builder) clock(n cst.Node) {
	e := b.expr(n)
	if e == nil {
		b.unsupported("the clock expression " + strings.TrimSpace(n.Text()))
		return
	}
	if name := firstBuiltin(e); name != "" {
		b.unsupported("the built-in function " + name +
			" in a clock expression")
		return
	}
	c := &b.dir.clock
	name := ""
	if e.Kind == sema.ExprCall && e.Decl != nil {
		name = strings.ToLower(strings.Trim(e.Decl.Text, `"`))
	}
	switch {
	case (name == "rising_edge" || name == "falling_edge") &&
		len(e.Args) == 1:
		if s, ok := b.signalOf(e.Args[0]); ok {
			c.edge, c.sig, c.on = rise, s, []sim.SigID{s}
			if name == "falling_edge" {
				c.edge = fall
			}
			return
		}
	case name == "and" && len(e.Args) == 2:
		for i, a := range e.Args {
			if a.Kind == sema.ExprAttr && a.Prefix != nil &&
				strings.EqualFold(string(a.Attr), "event") {
				if s, ok := b.signalOf(a.Prefix); ok {
					c.edge, c.sig, c.cond = event, s, e.Args[1-i]
					b.signals(c.cond)
					c.on = b.on(c.cond, s)
					return
				}
			}
		}
	}
	c.edge, c.cond = level, e
	b.signals(e)
	c.on = b.on(e, -1)
	if len(c.on) == 0 {
		b.unsupported("a clock expression that reads no signal")
	}
}

// on is the sensitivity of a clock: s (when >= 0) and the signals of e.
func (b *builder) on(e *sema.Expr, s sim.SigID) []sim.SigID {
	var out []sim.SigID
	if s >= 0 {
		out = append(out, s)
	}
	var walk func(e *sema.Expr)
	walk = func(e *sema.Expr) {
		if e == nil {
			return
		}
		if id, ok := b.signalOf(e); ok && !slices.Contains(out, id) {
			out = append(out, id)
		}
		walk(e.Prefix)
		for _, a := range e.Args {
			walk(a)
		}
	}
	walk(e)
	return out
}

// count evaluates a PSL count: [n] or [lo to hi]; hi < 0 is inf.
func (b *builder) count(n cst.Node) (lo, hi int) {
	kids := slices.Collect(n.Nodes())
	val := func(c cst.Node) int {
		if strings.EqualFold(strings.TrimSpace(c.Text()), "inf") {
			return -1
		}
		e := b.expr(c)
		if e == nil {
			b.unsupported("the count " + strings.TrimSpace(c.Text()))
			return 0
		}
		v, err := b.p.Ev.Value(e)
		if err != nil || v.I < 0 || v.I > maxCount {
			b.unsupported("the count " + strings.TrimSpace(c.Text()))
			return 0
		}
		return int(v.I)
	}
	inf := slices.Contains(words(n), "inf")
	switch {
	case len(kids) >= 2:
		return val(kids[0]), val(kids[1])
	case len(kids) == 1 && inf:
		return val(kids[0]), -1
	case len(kids) == 1:
		v := val(kids[0])
		return v, v
	}
	b.unsupported("an empty count")
	return 0, 0
}

// maxCount bounds the repetition and next counts the simulator
// expands, and the samples a prev call keeps.
const maxCount = 1 << 12

// operand returns the child of n that is not a count.
func operand(n cst.Node) (op, count cst.Node) {
	for c := range n.Nodes() {
		if c.Kind() == cst.KindPSLCount {
			count = c
		} else if op.IsZero() {
			op = c
		}
	}
	return op, count
}

func has(n cst.Node, w string) bool { return slices.Contains(words(n), w) }

// prop compiles a property.
func (b *builder) prop(n cst.Node) *prop {
	kids := slices.Collect(n.Nodes())
	kid := func(i int) cst.Node {
		if i < len(kids) {
			return kids[i]
		}
		return cst.Node{}
	}
	switch n.Kind() {
	case cst.KindPSLParenProp:
		return b.prop(kid(0))
	case cst.KindPSLBoolean:
		return boolP(b.boolean(n))
	case cst.KindPSLBuiltinCall:
		if b.isBoolean(n) {
			return boolP(b.boolean(n))
		}
		b.unsupported(kindWord(n))
	case cst.KindPSLAlways:
		return alwaysP(b.prop(kid(0)))
	case cst.KindPSLNever:
		return neverP(b.sere(kid(0)), nil)
	case cst.KindPSLEventually:
		return eventuallyP(b.prop(kid(0)))
	case cst.KindPSLNext:
		op, cnt := operand(n)
		k := 1
		if !cnt.IsZero() {
			k, _ = b.count(cnt)
		}
		return nextP(k, has(n, "!") || has(n, "next!"), b.prop(op))
	case cst.KindPSLNextA, cst.KindPSLNextE:
		op, cnt := operand(n)
		lo, hi := 1, 1
		if !cnt.IsZero() {
			lo, hi = b.count(cnt)
		}
		if hi < lo {
			b.unsupported("an infinite next_a or next_e range")
			return pT
		}
		strong := has(n, "!") || has(n, "next_a!") || has(n, "next_e!")
		p := b.prop(op)
		var xs []*prop
		for k := lo; k <= hi; k++ {
			xs = append(xs, nextP(k, strong, p))
		}
		if n.Kind() == cst.KindPSLNextE {
			return orP(xs...)
		}
		return andP(xs...)
	case cst.KindPSLUntil, cst.KindPSLBefore:
		ws := words(n)
		strong := slices.ContainsFunc(ws, func(w string) bool {
			return strings.Contains(w, "!")
		})
		incl := slices.ContainsFunc(ws, func(w string) bool {
			return strings.Contains(w, "_")
		})
		l, r := b.prop(kid(0)), b.prop(kid(1))
		if n.Kind() == cst.KindPSLUntil {
			return untilP(l, r, strong, incl)
		}
		return beforeP(l, r, strong, incl)
	case cst.KindPSLAbort:
		return abortP(b.prop(kid(0)), b.boolean(kid(1)))
	case cst.KindPSLImplication:
		l, r := b.prop(kid(0)), b.prop(kid(1))
		if has(n, "<->") {
			return andP(orP(notP(l), r), orP(l, notP(r)))
		}
		return orP(notP(l), r)
	case cst.KindPSLSuffixImplication:
		s := b.sere(kid(0))
		return suffixP([]*sere{s}, has(n, "|->"), b.prop(kid(1)))
	case cst.KindPSLLogicalProp:
		if len(kids) == 1 {
			return notP(b.prop(kid(0)))
		}
		l, r := b.prop(kid(0)), b.prop(kid(1))
		if has(n, "or") {
			return orP(l, r)
		}
		return andP(l, r)
	case cst.KindPSLBracedSere, cst.KindPSLRepetition:
		return sereP(states([]*sere{b.sere(n)}), has(n, "!"))
	case cst.KindPSLClockedProp:
		b.unsupported("a nested clock")
	case cst.KindPSLOBEUnary, cst.KindPSLOBEUntil:
		b.unsupported("a branching-time operator")
	default:
		b.unsupported(kindWord(n))
	}
	return pT
}

func kindWord(n cst.Node) string {
	switch n.Kind() {
	case cst.KindPSLForall:
		return "forall"
	case cst.KindPSLPropertyInstance:
		return "a property instance"
	case cst.KindPSLSequenceInstance:
		return "a sequence instance"
	case cst.KindPSLBuiltinCall:
		return "the built-in function " + firstText(n)
	case cst.KindPSLNextEvent, cst.KindPSLNextEventA,
		cst.KindPSLNextEventE:
		return "next_event"
	}
	return "the PSL construct " + strings.TrimSpace(n.Text())
}

// sere compiles a sequence.
func (b *builder) sere(n cst.Node) *sere {
	kids := slices.Collect(n.Nodes())
	kid := func(i int) cst.Node {
		if i < len(kids) {
			return kids[i]
		}
		return cst.Node{}
	}
	switch n.Kind() {
	case cst.KindPSLBracedSere, cst.KindPSLParenProp:
		return b.sere(kid(0))
	case cst.KindPSLBoolean:
		return boolS(b.boolean(n), false)
	case cst.KindPSLBuiltinCall:
		if b.isBoolean(n) {
			return boolS(b.boolean(n), false)
		}
	case cst.KindPSLSereConcat:
		return cat(b.sere(kid(0)), b.sere(kid(1)))
	case cst.KindPSLSereFusion:
		return fuse(b.sere(kid(0)), b.sere(kid(1)))
	case cst.KindPSLSereOr:
		return or(b.sere(kid(0)), b.sere(kid(1)))
	case cst.KindPSLSereAnd:
		if has(n, "&&") {
			return and(b.sere(kid(0)), b.sere(kid(1)))
		}
		return andNL(b.sere(kid(0)), b.sere(kid(1)))
	case cst.KindPSLSereWithin:
		return within(b.sere(kid(0)), b.sere(kid(1)))
	case cst.KindPSLRepetition:
		return b.repetition(n)
	}
	b.unsupported(kindWord(n))
	return none
}

func (b *builder) repetition(n cst.Node) *sere {
	op, cnt := operand(n)
	ws := words(n)
	lo, hi := 0, -1
	if !cnt.IsZero() {
		lo, hi = b.count(cnt)
	}
	first := ""
	if len(ws) > 0 {
		first = ws[0]
	}
	switch {
	case strings.HasPrefix(first, "[+"):
		s := anyL
		if !op.IsZero() {
			s = b.sere(op)
		}
		return cat(s, star(s))
	case strings.HasPrefix(first, "[*"):
		s := anyL
		if !op.IsZero() {
			s = b.sere(op)
		}
		return rep(s, lo, hi)
	case strings.HasPrefix(first, "[->"), strings.HasPrefix(first, "[="):
		if op.Kind() != cst.KindPSLBoolean {
			b.unsupported("a goto or non-consecutive repetition of a " +
				"sequence")
			return none
		}
		if cnt.IsZero() {
			lo, hi = 1, 1
		}
		if hi >= 0 && hi < lo {
			return none
		}
		x := b.boolean(op)
		if strings.HasPrefix(first, "[->") {
			return gotoRep(x, lo, hi)
		}
		return nonConsec(x, lo, hi)
	}
	b.unsupported("the repetition " + strings.TrimSpace(n.Text()))
	return none
}

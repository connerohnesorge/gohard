package ide

import (
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Testbench periods (#57): the stimulus runs this many clock periods,
// or tbTime without a clock, before it ends the run.
const (
	tbPeriods = "100"
	tbPeriod  = "10 ns"
	tbTime    = "100 ns"
)

// testbench returns the text of the testbench of entity e under the
// standard std, before formatting: e's context clause, the portless
// entity <name>_tb and its architecture sim, which declares a constant
// for every generic (its default, or a placeholder), a signal for
// every port (the port's subtype and default), the instance u_<name>
// mapping each formal to the object of its name, a clock process for
// every clock port (isClock) with the period CLK_PERIOD, and the
// stimulus process. The stimulus waits 100 clock periods, or 100 ns
// without a clock, then calls std.env.finish; under VHDL-93 and 2002,
// which have no std.env, it sets the signal done, which stops the
// clocks, and every process ends with `wait;`.
//
// ok is false when e has no ports (it is a testbench itself), a
// generic that is not a constant, a generic without a default whose
// subtype has no placeholder, or a port that is not a signal (a
// VHDL-2019 shared variable port), of a mode view or of a subtype that
// is not fully constrained.
func testbench(e *sema.Unit, std token.Standard) (string, bool) {
	gs, ps := formals(e.Region)
	if len(ps) == 0 {
		return "", false
	}
	taken := map[sema.Key]bool{e.Name: true}
	for _, d := range gs {
		taken[d.Name] = true
	}
	for _, d := range ps {
		taken[d.Name] = true
	}
	fresh := func(base string) string {
		s := base
		for i := 2; taken[sema.IdentKey(s)]; i++ {
			s = base + "_" + strconv.Itoa(i)
		}
		taken[sema.IdentKey(s)] = true
		return s
	}
	var decls, gmap, pmap []string
	for _, g := range gs {
		if g.Kind != sema.DeclGeneric ||
			g.Node.Kind() != cst.KindInterfaceObjectDecl {
			return "", false
		}
		io := cst.InterfaceObjectDecl{Node: g.Node}
		st := io.Subtype()
		if st.IsZero() {
			return "", false
		}
		val := ""
		if def := defaultOf(g.Node); !def.IsZero() {
			val = flat(def)
		} else if v, ok := placeholder(g.Type, st); ok {
			val = v
		} else {
			return "", false
		}
		decls = append(decls, "constant "+g.Text+" : "+flat(st.Node)+
			" := "+val+";")
		gmap = append(gmap, g.Text+" => "+g.Text)
	}
	var clocks []string
	var sigs []string
	for _, p := range ps {
		if p.Node.Kind() != cst.KindInterfaceObjectDecl ||
			p.Class != sema.ClassSignal && p.Class != sema.ClassNone ||
			p.Mode == sema.ModeView || p.Type.Unknown() ||
			!p.Type.FullyConstrained() {
			return "", false
		}
		io := cst.InterfaceObjectDecl{Node: p.Node}
		st := io.Subtype()
		if st.IsZero() {
			return "", false
		}
		s := "signal " + p.Text + " : " + flat(st.Node)
		if def := defaultOf(p.Node); !def.IsZero() {
			s += " := " + flat(def)
		}
		sigs = append(sigs, s+";")
		pmap = append(pmap, p.Text+" => "+p.Text)
		if isClock(p) {
			clocks = append(clocks, p.Text)
		}
	}
	old := std < token.Std2008
	period, done := "", ""
	if len(clocks) > 0 {
		period = fresh("CLK_PERIOD")
		decls = append(decls, "constant "+period+" : time := "+
			tbPeriod+";")
		if old {
			done = fresh("done")
		}
	}
	decls = append(decls, sigs...)
	if done != "" {
		decls = append(decls, "signal "+done+" : boolean := false;")
	}
	name := e.Decl.Text
	inst := fresh("u_" + name)
	var procs []string
	for _, c := range clocks {
		label := fresh(c + "_gen")
		body := []string{
			c + " <= '0';",
			"wait for " + period + " / 2;",
			c + " <= '1';",
			"wait for " + period + " / 2;",
		}
		if old {
			body = append(append([]string{"while not " + done + " loop"},
				indent(body)...), "end loop;", "wait;")
		}
		procs = append(procs, process(label, body))
	}
	wait := "wait for " + tbTime + ";"
	if period != "" {
		wait = "wait for " + tbPeriods + " * " + period + ";"
	}
	stim := []string{wait, "std.env.finish;"}
	if old {
		stim = []string{wait, "wait;"}
		if done != "" {
			stim = []string{wait, done + " <= true;", "wait;"}
		}
	}
	procs = append(procs, process(fresh("stimulus"), stim))

	var b strings.Builder
	for c := range e.Node.Nodes() {
		if c.Kind() != cst.KindContextClause {
			continue
		}
		for it := range (cst.ContextClause{Node: c}).Items() {
			b.WriteString(flat(it.Node) + "\n")
		}
		b.WriteString("\n")
	}
	tb := name + "_tb"
	b.WriteString("entity " + tb + " is\nend entity " + tb + ";\n\n")
	b.WriteString("architecture sim of " + tb + " is\n")
	for _, d := range decls {
		b.WriteString("  " + d + "\n")
	}
	b.WriteString("begin\n")
	for _, l := range strings.Split(instanceText(inst, "work", name,
		gmap, pmap), "\n") {
		b.WriteString("  " + l + "\n")
	}
	for _, p := range procs {
		b.WriteString("\n" + p)
	}
	b.WriteString("end architecture sim;\n")
	return b.String(), true
}

// defaultOf returns the default expression of the interface object
// declaration n: the node after its `:=`.
func defaultOf(n cst.Node) cst.Node {
	as := n.Token(token.VarAssign, 0)
	if as.IsZero() {
		return cst.Node{}
	}
	for c := range n.Nodes() {
		if c.Span().Start >= as.Span().End {
			return c
		}
	}
	return cst.Node{}
}

// process is a process statement with the given body lines.
func process(label string, body []string) string {
	var b strings.Builder
	b.WriteString("  " + label + " : process\n  begin\n")
	for _, l := range body {
		b.WriteString("    " + l + "\n")
	}
	b.WriteString("  end process " + label + ";\n")
	return b.String()
}

func indent(ls []string) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = "  " + l
	}
	return out
}

// isClock reports whether the port p looks like a clock: of mode in,
// of type std_logic or std_ulogic (ieee.std_logic_1164), and named,
// in any letter case, clk or clock, or starting with clk_ or ending
// with _clk.
func isClock(p *sema.Decl) bool {
	n := strings.ToLower(p.Text)
	if p.Mode != sema.ModeIn || !(n == "clk" || n == "clock" ||
		strings.HasPrefix(n, "clk_") || strings.HasSuffix(n, "_clk")) {
		return false
	}
	t := p.Type
	if t == nil || t.Decl == nil || t.Decl.Unit == nil {
		return false
	}
	u := t.Decl.Unit
	return (t.Decl.Name == "std_logic" || t.Decl.Name == "std_ulogic") &&
		u.Library == "ieee" && u.Name == "std_logic_1164"
}

// placeholder returns the value the testbench gives a generic without
// a default whose subtype indication st is a plain type mark T: for
// an integer or physical subtype whose range is static, the value in
// it nearest to 0 (in primary units); for a floating one 0.0 when it
// is in the range; else T'left for a scalar; "" for an unconstrained
// one-dimensional array of a character type (string,
// std_logic_vector).
func placeholder(t *sema.Type, st cst.SubtypeIndication) (string, bool) {
	tm := st.TypeMark()
	if t.Unknown() || tm.IsZero() || !st.Resolution().IsZero() ||
		!st.Constraint().IsZero() {
		return "", false
	}
	if t.IsScalar() {
		if ev := t.Eval; ev != nil {
			r := ev.Range
			v := strconv.FormatInt(min(max(0, r.Low()), r.High()), 10)
			switch {
			case ev.Kind == eval.KindInteger && r.Low() <= r.High():
				return v, true
			case ev.Kind == eval.KindPhysical && r.Low() <= r.High() &&
				len(ev.Units) > 0:
				return v + " " + ev.Units[0].Name, true
			case ev.Kind == eval.KindFloating &&
				ev.Real.Low() <= 0 && 0 <= ev.Real.High():
				return "0.0", true
			}
		}
		return flat(tm.Node) + "'left", true
	}
	b := t.Base()
	if b.Class != sema.TypeArray || t.Dims() != 1 ||
		t.FullyConstrained() || b.Elem == nil {
		return "", false
	}
	for _, l := range b.Elem.Base().Literals {
		if l.Name.IsCharacter() {
			return `""`, true
		}
	}
	return "", false
}

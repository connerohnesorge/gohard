// Package vhdlgen generates small, legal, self-contained VHDL designs
// (#58): entities with generics and ports, processes of four shapes
// (combinational, wait loops, clocked, bounded stimulus), every wait
// form, signal assignments (delayed, transport, conditional,
// selected), resolved signals (std_logic and an integer subtype with a
// user resolution function), direct and component instances, and
// if-generate statements, under a portless testbench gen_tb.
//
// Generate is a pure function of (seed, data, options): every decision
// reads the next byte of data and, once data is used up, a SplitMix64
// stream keyed by the seed, so a fuzzer mutates designs through data
// and a property test enumerates them through the seed. Seed 0 with
// empty data decides 0 everywhere, the smallest design; small choice
// values pick edge values (0, 255, 1, 128) first (P5).
//
// Every design is bounded and well defined by construction:
//
//   - The clock and the stimulus processes run bounded loops; every
//     other process and concurrent statement reads only signals
//     declared before everything it drives (or, when clocked, runs
//     only on clock edges), so the design settles in finitely many
//     deltas and ends on its own.
//   - Integer values stay in 0..255 (every operation is wrapped in
//     mod 256, every integer object starts in range, and the
//     resolution function sums modulo 256), so no evaluation fails.
//   - Unresolved signals have exactly one driver.
//   - Processes share no variable unless Options.Racy is set, so the
//     trace is the same at every seed (Design.RaceFree). With Racy,
//     processes may bump the shared variable sv at initialization and
//     report its value, which then depends on the in-delta order.
//
// #94 extends this grammar with synthesisable constructs and #96
// reuses it for formal property tests.
package vhdlgen

import (
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/env"
)

// Standards are the VHDL standards a design can be generated for.
var Standards = []string{"1993", "2002", "2008", "2019"}

// Top is the name of every design's testbench entity.
const Top = "gen_tb"

// Options bound a design.
type Options struct {
	// Standard is the design's standard (one of Standards); "" lets
	// the input choose.
	Standard string
	// Racy lets processes bump and report a shared variable, so the
	// trace may depend on the seed.
	Racy bool
	// Units is the most entities below the testbench (0: 3).
	Units int
	// Signals is the most signals an architecture declares (0: 5).
	Signals int
	// Statements is the most concurrent statements of an architecture
	// besides the testbench's clock and checker (0: 5).
	Statements int
}

// File is one generated source file.
type File struct {
	Path string
	Text []byte
}

// Design is one generated design.
type Design struct {
	Standard string
	// Top is the testbench to run (always Top).
	Top   string
	Files []File
	// RaceFree reports that no process but the checker touches the
	// shared variable: the trace does not depend on the in-delta order
	// (seed).
	RaceFree bool
}

// Text returns every file of d in order, each after a comment line
// naming it: the form failure messages print.
func (d Design) Text() string {
	var b strings.Builder
	for _, f := range d.Files {
		b.WriteString("-- file " + f.Path + "\n")
		b.Write(f.Text)
	}
	return b.String()
}

// choices are the generator's decisions: the bytes of data first,
// then a SplitMix64 stream keyed by the seed. With seed 0 every
// decision past data is 0.
type choices struct {
	data []byte
	r    env.SplitMix64
	zero bool
}

// n returns a decision in [0, k).
func (c *choices) n(k int) int {
	if k <= 1 {
		return 0
	}
	if len(c.data) > 0 {
		b := int(c.data[0])
		c.data = c.data[1:]
		return b % k
	}
	if c.zero {
		return 0
	}
	return env.Intn(&c.r, k)
}

// typ is a signal type.
type typ int

const (
	tInt typ = iota // integer, unresolved
	tSL             // std_logic, resolved
	tSum            // sum_int: integer resolved by sum_res
)

func (t typ) name() string {
	switch t {
	case tSL:
		return "std_logic"
	case tSum:
		return "sum_int"
	}
	return "integer"
}

func (t typ) isInt() bool { return t != tSL }

// mode is what a signal is to its architecture.
type mode int

const (
	mSignal mode = iota
	mIn
	mOut
	mClk // the clock: an in port, or the testbench's clock signal
)

type sig struct {
	name    string
	t       typ
	m       mode
	drivers int
	init    string // the initial value or port default; "" none
}

func (s *sig) readable() bool { return s.m != mOut }

func (s *sig) drivable() bool {
	return (s.m == mSignal || s.m == mOut) &&
		(s.t != tInt || s.drivers == 0)
}

type generic struct {
	name string
	time bool
	def  string // "" no default
}

// entity is a generated entity below the testbench.
type entity struct {
	name     string
	generics []generic
	clk      bool
	ports    []*sig // in ports, then out ports; clk not included
}

type gen struct {
	c      choices
	o      Options
	std    string
	v2008  bool
	ents   []*entity
	racy   bool
	labels int
}

// Generate returns the design of (seed, data) under o.
func Generate(seed uint64, data []byte, o Options) Design {
	if o.Units <= 0 {
		o.Units = 3
	}
	if o.Signals <= 0 {
		o.Signals = 5
	}
	if o.Statements <= 0 {
		o.Statements = 5
	}
	g := &gen{c: choices{data: data, r: env.SplitMix64{State: seed},
		zero: seed == 0}, o: o}
	g.std = o.Standard
	if g.std == "" {
		// 2008 first: the most constructs.
		g.std = []string{"2008", "1993", "2002", "2019"}[g.c.n(4)]
	}
	g.v2008 = g.std >= "2008"
	var units []string
	units = append(units, g.pkg())
	n := g.c.n(o.Units + 1)
	for i := range n {
		units = append(units, g.entity(i))
	}
	units = append(units, g.testbench())
	d := Design{Standard: g.std, Top: Top, RaceFree: !g.racy}
	if g.c.n(2) == 0 {
		var b strings.Builder
		for _, u := range units {
			b.WriteString(u)
		}
		d.Files = []File{{Path: "gen.vhd", Text: []byte(b.String())}}
		return d
	}
	for i, u := range units {
		d.Files = append(d.Files, File{
			Path: "gen" + strconv.Itoa(i) + ".vhd", Text: []byte(u)})
	}
	return d
}

// pkg returns the packages gen_pkg and gen_sv. gen_pkg declares
// sum_int, an integer subtype resolved by summing its drivers modulo
// 256, so the order of the drivers never matters. For a racy design
// gen_sv declares the shared variable sv (of the protected type
// counter from VHDL-2002 on), whose value depends on the order of the
// processes that bump it.
func (g *gen) pkg() string {
	decl, body, sv := "", "", ""
	if g.o.Racy {
		if g.std == "1993" {
			sv = "  shared variable sv : integer := 0;\n"
		} else {
			decl, body = pkgCounter, pkgCounterBody
			sv = "  shared variable sv : counter;\n"
		}
	}
	return "package gen_pkg is\n" +
		"  type int_list is array (natural range <>) of integer;\n" +
		"  function sum_res (v : int_list) return integer;\n" +
		"  subtype sum_int is sum_res integer;\n" + decl +
		"end package gen_pkg;\n\n" +
		"package body gen_pkg is\n" +
		"  function sum_res (v : int_list) return integer is\n" +
		"    variable s : integer := 0;\n" +
		"  begin\n" +
		"    for i in v'range loop\n" +
		"      s := (s + v(i)) mod 256;\n" +
		"    end loop;\n" +
		"    return s;\n" +
		"  end function sum_res;\n" + body +
		"end package body gen_pkg;\n\n" +
		// A shared variable of a protected type must follow the
		// elaboration of the type's body.
		"use work.gen_pkg.all;\n\n" +
		"package gen_sv is\n" + sv + "end package gen_sv;\n\n"
}

const pkgCounter = `  type counter is protected
    procedure bump (n : integer);
    impure function get return integer;
  end protected counter;
`

const pkgCounterBody = `  type counter is protected body
    variable v : integer := 0;
    procedure bump (n : integer) is
    begin
      v := (v * 3 + n) mod 256;
    end procedure bump;
    impure function get return integer is
    begin
      return v;
    end function get;
  end protected body counter;
`

// racyReport bumps sv by a number of its own and reports its value:
// the reported value depends on the order of the bumps.
func (g *gen) racyReport(ind string) string {
	l := g.label("r")
	n := l[1:]
	if g.std == "1993" {
		return ind + "sv := (sv * 3 + " + n + ") mod 256;\n" + ind +
			"report \"" + l + " \" & integer'image(sv);\n"
	}
	return ind + "sv.bump(" + n + ");\n" + ind + "report \"" + l +
		" \" & integer'image(sv.get);\n"
}

const header = "library ieee;\nuse ieee.std_logic_1164.all;\n" +
	"use work.gen_pkg.all;\nuse work.gen_sv.all;\n\n"

// constInt returns an integer literal in 0..255, edge values first.
func (g *gen) constInt() string {
	switch k := g.c.n(8); k {
	case 0, 1, 2, 3:
		return []string{"0", "255", "1", "128"}[k]
	}
	return strconv.Itoa(g.c.n(256))
}

// slLit returns a std_logic literal, '0' and '1' first.
func (g *gen) slLit() string {
	switch k := g.c.n(4); k {
	case 0, 1, 2:
		return []string{"'0'", "'1'", "'Z'"}[k]
	}
	return "'" + string("UXWLH-"[g.c.n(6)]) + "'"
}

func (g *gen) typ() typ { return typ(g.c.n(3)) }

func (g *gen) initOf(t typ) string {
	if t == tSL {
		if g.c.n(2) == 0 {
			return "'0'"
		}
		return "" // starts at 'U'
	}
	return g.constInt()
}

func (g *gen) label(prefix string) string {
	g.labels++
	return prefix + strconv.Itoa(g.labels)
}

// entity generates entity e<i> and its architecture.
func (g *gen) entity(i int) string {
	e := &entity{name: "e" + strconv.Itoa(i), clk: g.c.n(2) == 1}
	for k := range g.c.n(3) {
		gn := generic{name: "g" + strconv.Itoa(k)}
		if g.c.n(3) != 2 {
			gn.def = g.constInt()
		}
		e.generics = append(e.generics, gn)
	}
	if g.c.n(3) == 2 {
		e.generics = append(e.generics, generic{name: "gd", time: true,
			def: strconv.Itoa(g.c.n(6)) + " ns"})
	}
	for k := range g.c.n(3) {
		t := g.typ()
		e.ports = append(e.ports, &sig{name: "i" + strconv.Itoa(k), t: t,
			m: mIn, init: g.portDefault(t)})
	}
	for k := range 1 + g.c.n(2) {
		t := g.typ()
		e.ports = append(e.ports, &sig{name: "o" + strconv.Itoa(k), t: t,
			m: mOut, init: g.portDefault(t)})
	}
	u := &unit{g: g, ent: e}
	if e.clk {
		u.sigs = append(u.sigs, &sig{name: "clk", t: tSL, m: mClk})
	}
	ins, outs := splitPorts(e.ports)
	u.sigs = append(u.sigs, ins...)
	u.internals()
	for _, p := range outs {
		// Fresh copies: the driver counts are this architecture's.
		c := *p
		u.sigs = append(u.sigs, &c)
	}
	u.constants()
	for range 1 + g.c.n(g.o.Statements) {
		u.statement(2)
	}
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("entity " + e.name + " is\n")
	writeInterface(&b, "  ", e)
	b.WriteString("end entity " + e.name + ";\n\n")
	b.WriteString("architecture rtl of " + e.name + " is\n")
	u.writeBody(&b)
	b.WriteString("end architecture rtl;\n\n")
	g.ents = append(g.ents, e)
	return b.String()
}

func (g *gen) portDefault(t typ) string {
	if t == tSL {
		return "'0'"
	}
	return g.constInt()
}

func splitPorts(ps []*sig) (ins, outs []*sig) {
	for _, p := range ps {
		if p.m == mIn {
			ins = append(ins, p)
		} else {
			outs = append(outs, p)
		}
	}
	return ins, outs
}

// writeInterface writes the generic and port clauses of e (an entity
// or a component).
func writeInterface(b *strings.Builder, ind string, e *entity) {
	if len(e.generics) > 0 {
		var gs []string
		for _, gn := range e.generics {
			s := gn.name + " : integer"
			if gn.time {
				s = gn.name + " : time"
			}
			if gn.def != "" {
				s += " := " + gn.def
			}
			gs = append(gs, s)
		}
		b.WriteString(ind + "generic (" + strings.Join(gs, "; ") + ");\n")
	}
	var ps []string
	if e.clk {
		ps = append(ps, "clk : in std_logic := '0'")
	}
	for _, p := range e.ports {
		dir := "in"
		if p.m == mOut {
			dir = "out"
		}
		ps = append(ps, p.name+" : "+dir+" "+p.t.name()+" := "+p.init)
	}
	if len(ps) > 0 {
		b.WriteString(ind + "port (" + strings.Join(ps, "; ") + ");\n")
	}
}

// testbench generates gen_tb: optionally a clock, the statements, and
// the checker process that reports every signal.
func (g *gen) testbench() string {
	u := &unit{g: g}
	clk := g.c.n(2) == 1
	if clk {
		u.sigs = append(u.sigs, &sig{name: "clk", t: tSL, m: mClk,
			init: "'0'"})
	}
	u.internals()
	u.constants()
	if clk {
		u.stmts = append(u.stmts, "  clock : process\n  begin\n"+
			"    for i in 1 to "+strconv.Itoa(2+2*g.c.n(8))+" loop\n"+
			"      wait for "+strconv.Itoa(1+g.c.n(5))+" ns;\n"+
			"      clk <= not clk;\n    end loop;\n    wait;\n"+
			"  end process clock;\n")
	}
	for range 1 + g.c.n(g.o.Statements) {
		u.statement(2)
	}
	u.stmts = append(u.stmts, u.checker())
	var b strings.Builder
	b.WriteString(header)
	b.WriteString("entity " + Top + " is\nend entity " + Top + ";\n\n")
	b.WriteString("architecture sim of " + Top + " is\n")
	u.writeBody(&b)
	b.WriteString("end architecture sim;\n")
	return b.String()
}

// checker reports every signal of the testbench a few times and, from
// VHDL-2008 on, may end the run with std.env.finish.
func (u *unit) checker() string {
	g := u.g
	var parts []string
	for _, s := range u.sigs {
		if s.t == tSL {
			parts = append(parts, "\""+s.name+"=\" & std_logic'image("+
				s.name+")")
		} else {
			parts = append(parts, "\""+s.name+"=\" & integer'image("+
				s.name+")")
		}
	}
	msg := "\"end\""
	if len(parts) > 0 {
		msg = strings.Join(parts, " & \" \" & ")
	}
	var b strings.Builder
	b.WriteString("  checker : process\n  begin\n")
	if g.o.Racy {
		b.WriteString(g.racyReport("    "))
	}
	b.WriteString("    for k in 1 to " + strconv.Itoa(1+g.c.n(4)) +
		" loop\n")
	b.WriteString("      wait for " + strconv.Itoa(g.c.n(40)) + " ns;\n")
	b.WriteString("      report " + msg + ";\n    end loop;\n")
	if g.c.n(3) == 1 {
		b.WriteString("    assert " + u.scope(len(u.sigs), false).cond(1) +
			" report \"check\" severity " +
			[]string{"note", "warning"}[g.c.n(2)] + ";\n")
	}
	if g.v2008 && g.c.n(2) == 1 {
		b.WriteString("    std.env.finish;\n")
	}
	b.WriteString("    wait;\n  end process checker;\n")
	return b.String()
}

// unit is one architecture being generated.
type unit struct {
	g      *gen
	ent    *entity // nil for the testbench
	sigs   []*sig  // in layer order: clk, in ports, signals, out ports
	consts []string
	decls  []string
	comps  []string // entities with a component declaration
	stmts  []string
}

func (u *unit) internals() {
	g := u.g
	for k := range 1 + g.c.n(g.o.Signals) {
		t := g.typ()
		u.sigs = append(u.sigs, &sig{name: "s" + strconv.Itoa(k), t: t,
			m: mSignal, init: g.initOf(t)})
	}
}

// constants declares up to one constant computed from the generics.
func (u *unit) constants() {
	if u.g.c.n(2) == 0 {
		return
	}
	v := u.scope(0, false).intExpr(2)
	u.consts = append(u.consts, "c0")
	u.decls = append(u.decls, "  constant c0 : integer := "+v+";\n")
}

func (u *unit) intGenerics() []string {
	if u.ent == nil {
		return nil
	}
	var out []string
	for _, gn := range u.ent.generics {
		if !gn.time {
			out = append(out, gn.name)
		}
	}
	return out
}

func (u *unit) timeGeneric() string {
	if u.ent == nil {
		return ""
	}
	for _, gn := range u.ent.generics {
		if gn.time {
			return gn.name
		}
	}
	return ""
}

func (u *unit) hasClk() bool { return len(u.sigs) > 0 && u.sigs[0].m == mClk }

func (u *unit) writeBody(b *strings.Builder) {
	for _, s := range u.sigs {
		if s.m != mSignal && !(s.m == mClk && u.ent == nil) {
			continue
		}
		b.WriteString("  signal " + s.name + " : " + s.t.name())
		if s.init != "" {
			b.WriteString(" := " + s.init)
		}
		b.WriteString(";\n")
	}
	for _, d := range u.decls {
		b.WriteString(d)
	}
	b.WriteString("begin\n")
	for _, s := range u.stmts {
		b.WriteString(s)
	}
}

// statement adds one concurrent statement.
func (u *unit) statement(depth int) {
	switch u.g.c.n(5) {
	case 1:
		u.stmts = append(u.stmts, u.assign("  "))
	case 2:
		if len(u.g.ents) > 0 {
			u.stmts = append(u.stmts, u.instance("  "))
			return
		}
		u.stmts = append(u.stmts, u.process("  "))
	case 3:
		u.stmts = append(u.stmts, u.ifGenerate(depth))
	default:
		u.stmts = append(u.stmts, u.process("  "))
	}
}

// level picks a layer boundary: reads come from signals before it,
// targets from signals at or after it.
func (u *unit) level() int {
	if len(u.sigs) == 0 {
		return 0
	}
	return 1 + u.g.c.n(len(u.sigs))
}

// scope is the expression context of signals below level (all
// readable ones when free).
func (u *unit) scope(level int, free bool) *scope {
	s := &scope{u: u}
	for i, x := range u.sigs {
		if x.readable() && (free || i < level) {
			s.reads = append(s.reads, x)
		}
		if x.drivable() && (free || i >= level) {
			s.targets = append(s.targets, x)
		}
	}
	return s
}

// commit counts a driver for every signal s assigned.
func (s *scope) commit() {
	for _, t := range s.assigned {
		t.drivers++
	}
}

// assign is a concurrent signal assignment: simple, delayed,
// conditional or selected.
func (u *unit) assign(ind string) string {
	g := u.g
	s := u.scope(u.level(), false)
	if len(s.targets) == 0 {
		return u.process(ind)
	}
	t := s.targets[g.c.n(len(s.targets))]
	s.assigned = append(s.assigned, t)
	defer s.commit()
	lbl := g.label("a")
	switch g.c.n(4) {
	case 1:
		return ind + lbl + " : " + t.name + " <= " + s.value(t, 2) + " when " +
			s.cond(1) + " else " + s.value(t, 2) + ";\n"
	case 2:
		if x := s.pick(func(x *sig) bool { return true }); x != nil {
			var c [2]string
			if x.t == tSL {
				c = [2]string{"'0'", "'1'"}
			} else {
				c = [2]string{"0", "1 | 2"}
			}
			return ind + lbl + " : with " + x.name + " select " + t.name +
				" <= " + s.value(t, 1) + " when " + c[0] + ", " +
				s.value(t, 1) + " when " + c[1] + ", " + s.value(t, 1) +
				" when others;\n"
		}
	case 3:
		return ind + lbl + " : " + t.name + " <= " + s.delay(t) + ";\n"
	}
	return ind + lbl + " : " + t.name + " <= " + s.value(t, 2) + ";\n"
}

// ifGenerate wraps a concurrent statement in an if-generate on the
// generics and constants.
func (u *unit) ifGenerate(depth int) string {
	if depth == 0 {
		return u.process("  ")
	}
	lbl := u.g.label("gen")
	s := u.scope(0, false)
	cond := s.cond(1)
	saved := u.stmts
	u.stmts = nil
	u.statement(depth - 1)
	inner := u.stmts
	u.stmts = saved
	var b strings.Builder
	b.WriteString("  " + lbl + " : if " + cond + " generate\n")
	for _, x := range inner {
		b.WriteString(indent(x))
	}
	b.WriteString("  end generate " + lbl + ";\n")
	return b.String()
}

func indent(s string) string {
	lines := strings.SplitAfter(s, "\n")
	var b strings.Builder
	for _, l := range lines {
		if l != "" {
			b.WriteString("  " + l)
		}
	}
	return b.String()
}

// instance instantiates an earlier entity, directly or through a
// component declaration, with its inputs below a level and its
// outputs at or after it.
func (u *unit) instance(ind string) string {
	g := u.g
	e := g.ents[g.c.n(len(g.ents))]
	s := u.scope(u.level(), false)
	var gm []string
	gs := u.scope(0, false)
	for _, gn := range e.generics {
		if gn.def != "" && g.c.n(2) == 0 {
			continue
		}
		v := gs.intExpr(1)
		if gn.time {
			v = strconv.Itoa(g.c.n(6)) + " ns"
			if tg := u.timeGeneric(); tg != "" && g.c.n(2) == 1 {
				v = tg
			}
		}
		gm = append(gm, gn.name+" => "+v)
	}
	var pm []string
	if e.clk {
		switch {
		case u.hasClk():
			pm = append(pm, "clk => clk")
		default:
			if x := s.pick(func(x *sig) bool { return x.t == tSL }); x != nil {
				pm = append(pm, "clk => "+x.name)
			}
		}
	}
	for _, p := range e.ports {
		want := func(x *sig) bool { return x.t == p.t }
		if p.m == mIn {
			if g.c.n(4) == 0 {
				pm = append(pm, p.name+" => open")
				continue
			}
			if x := s.pick(want); x != nil {
				pm = append(pm, p.name+" => "+x.name)
			}
			continue
		}
		x := s.pickTarget(want)
		if x == nil || g.c.n(4) == 0 {
			pm = append(pm, p.name+" => open")
			continue
		}
		x.drivers++
		pm = append(pm, p.name+" => "+x.name)
	}
	lbl := g.label("u")
	head := ind + lbl + " : entity work." + e.name
	if g.c.n(2) == 1 {
		head = ind + lbl + " : " + e.name
		u.component(e)
	}
	var b strings.Builder
	b.WriteString(head)
	if len(gm) > 0 {
		b.WriteString("\n" + ind + "  generic map (" +
			strings.Join(gm, ", ") + ")")
	}
	if len(pm) > 0 {
		b.WriteString("\n" + ind + "  port map (" +
			strings.Join(pm, ", ") + ")")
	}
	b.WriteString(";\n")
	return b.String()
}

// component declares e as a component of u once.
func (u *unit) component(e *entity) {
	for _, c := range u.comps {
		if c == e.name {
			return
		}
	}
	u.comps = append(u.comps, e.name)
	var b strings.Builder
	b.WriteString("  component " + e.name + "\n")
	writeInterface(&b, "    ", e)
	b.WriteString("  end component;\n")
	u.decls = append(u.decls, b.String())
}

// Process shapes.
const (
	pComb     = iota // sensitivity list, no waits
	pWaitLoop        // wait on / wait until, no other waits
	pClocked         // runs on clock edges, reads anything
	pStimulus        // bounded waits, then wait forever
)

// process generates one process statement.
func (u *unit) process(ind string) string {
	g := u.g
	kind := g.c.n(4)
	if kind == pClocked && !u.hasClk() {
		kind = pStimulus
	}
	var s *scope
	if kind == pComb || kind == pWaitLoop {
		s = u.scope(u.level(), false)
	} else {
		s = u.scope(0, true)
	}
	s.waits = kind == pStimulus
	s.report = g.o.Racy && g.c.n(3) == 0
	if s.report {
		g.racy = true
	}
	var decls []string
	for k := range g.c.n(3) {
		v := "v" + strconv.Itoa(k)
		s.vars = append(s.vars, v)
		decls = append(decls, ind+"  variable "+v+" : integer := "+
			g.constInt()+";\n")
	}
	var body strings.Builder
	lead := ind + "  "
	pre := ""
	if s.report {
		// At initialization, where every reporting process and the
		// checker run in the same delta.
		pre = g.racyReport(lead)
	}
	var head, tail string
	switch kind {
	case pClocked:
		switch g.c.n(3) {
		case 0:
			head = " (clk)"
			lead = ind + "    "
			s.block(&body, lead, 1+g.c.n(3), 2)
			body2 := lead[:len(lead)-2] + "if rising_edge(clk) then\n" +
				body.String() + lead[:len(lead)-2] + "end if;\n"
			body.Reset()
			body.WriteString(body2)
		case 1:
			head = " (clk)"
			lead = ind + "    "
			s.block(&body, lead, 1+g.c.n(3), 2)
			body2 := lead[:len(lead)-2] +
				"if clk'event and clk = '1' then\n" + body.String() +
				lead[:len(lead)-2] + "end if;\n"
			body.Reset()
			body.WriteString(body2)
		default:
			body.WriteString(lead + "wait until rising_edge(clk);\n")
			s.block(&body, lead, 1+g.c.n(3), 2)
		}
	case pStimulus:
		s.block(&body, lead, 1+g.c.n(4), 2)
		tail = lead + "wait;\n"
	case pWaitLoop:
		if g.c.n(2) == 1 && len(s.reads) > 0 {
			body.WriteString(lead + "wait until " + s.cond(1) + ";\n")
			s.block(&body, lead, 1+g.c.n(3), 2)
		} else {
			s.block(&body, lead, 1+g.c.n(3), 2)
			if len(s.read) == 0 {
				tail = lead + "wait;\n"
			} else {
				tail = lead + "wait on " + names(s.read) + ";\n"
			}
		}
	default:
		s.block(&body, lead, 1+g.c.n(3), 2)
		switch {
		case len(s.read) == 0:
			tail = lead + "wait;\n"
		case g.v2008 && g.c.n(2) == 1:
			head = " (all)"
		default:
			head = " (" + names(s.read) + ")"
		}
	}
	s.commit()
	lbl := g.label("p")
	var b strings.Builder
	b.WriteString(ind + lbl + " : process" + head + "\n")
	for _, d := range decls {
		b.WriteString(d)
	}
	b.WriteString(ind + "begin\n")
	b.WriteString(pre)
	b.WriteString(body.String())
	b.WriteString(tail)
	b.WriteString(ind + "end process " + lbl + ";\n")
	return b.String()
}

func names(xs []*sig) string {
	var out []string
	for _, x := range xs {
		out = append(out, x.name)
	}
	return strings.Join(out, ", ")
}

// scope is the context of expressions and sequential statements.
type scope struct {
	u        *unit
	reads    []*sig // signals it may read
	targets  []*sig // signals it may drive
	read     []*sig // signals read so far, in first-read order
	assigned []*sig // signals driven so far
	vars     []string
	loops    []string
	waits    bool
	report   bool
}

func (s *scope) c() *choices { return &s.u.g.c }

// pick returns a readable signal satisfying ok, nil if none, and
// records the read.
func (s *scope) pick(ok func(*sig) bool) *sig {
	var xs []*sig
	for _, x := range s.reads {
		if ok(x) {
			xs = append(xs, x)
		}
	}
	if len(xs) == 0 {
		return nil
	}
	x := xs[s.c().n(len(xs))]
	s.noteRead(x)
	return x
}

func (s *scope) noteRead(x *sig) {
	for _, r := range s.read {
		if r == x {
			return
		}
	}
	s.read = append(s.read, x)
}

// pickTarget returns a drivable signal satisfying ok, nil if none.
func (s *scope) pickTarget(ok func(*sig) bool) *sig {
	var xs []*sig
	for _, x := range s.targets {
		if x.drivable() && ok(x) {
			xs = append(xs, x)
		}
	}
	if len(xs) == 0 {
		return nil
	}
	return xs[s.c().n(len(xs))]
}

// intNames are the integer names in scope besides signals.
func (s *scope) intNames() []string {
	out := append([]string(nil), s.u.intGenerics()...)
	out = append(out, s.u.consts...)
	out = append(out, s.vars...)
	return append(out, s.loops...)
}

// intExpr returns an integer expression with a value in 0..255.
func (s *scope) intExpr(d int) string {
	c := s.c()
	switch c.n(6) {
	case 1:
		if x := s.pick(func(x *sig) bool { return x.t.isInt() }); x != nil {
			return x.name
		}
	case 2:
		if ns := s.intNames(); len(ns) > 0 {
			return ns[c.n(len(ns))]
		}
	case 3, 4:
		if d > 0 {
			op := []string{" + ", " - ", " * "}[c.n(3)]
			return "(" + s.intExpr(d-1) + op + s.intExpr(d-1) + ") mod 256"
		}
	case 5:
		if d > 0 {
			if x := s.pick(func(x *sig) bool { return x.t == tSL }); x != nil {
				return "std_logic'pos(" + x.name + ")"
			}
			return "boolean'pos(" + s.cond(d-1) + ")"
		}
	}
	return s.u.g.constInt()
}

// slExpr returns a std_logic expression; a bare literal only when top
// (the type then comes from the context).
func (s *scope) slExpr(d int, top bool) string {
	c := s.c()
	switch c.n(4) {
	case 1:
		if x := s.pick(func(x *sig) bool { return x.t == tSL }); x != nil {
			return x.name
		}
	case 2:
		if d > 0 {
			return "(not " + s.slExpr(d-1, false) + ")"
		}
	case 3:
		if d > 0 {
			op := []string{" and ", " or ", " xor ", " nand ", " nor "}[c.n(5)]
			return "(" + s.slExpr(d-1, false) + op + s.slExpr(d-1, false) +
				")"
		}
	}
	if top {
		return s.u.g.slLit()
	}
	return "std_logic'(" + s.u.g.slLit() + ")"
}

// cond returns a boolean expression.
func (s *scope) cond(d int) string {
	c := s.c()
	switch c.n(5) {
	case 1:
		return "(" + s.slExpr(d, false) + " = " + s.u.g.slLit() + ")"
	case 2:
		if d > 0 {
			op := []string{" and ", " or ", " xor "}[c.n(3)]
			return "(" + s.cond(d-1) + op + s.cond(d-1) + ")"
		}
	case 3:
		if d > 0 {
			return "(not " + s.cond(d-1) + ")"
		}
	}
	op := []string{" = ", " /= ", " < ", " <= ", " > ", " >= "}[c.n(6)]
	return "(" + s.intExpr(d) + op + s.intExpr(d) + ")"
}

// value returns an expression of t's type.
func (s *scope) value(t *sig, d int) string {
	if t.t == tSL {
		return s.slExpr(d, true)
	}
	return s.intExpr(d)
}

// delay returns a value with an after clause, transport or inertial.
func (s *scope) delay(t *sig) string {
	c := s.c()
	after := strconv.Itoa(1+c.n(10)) + " ns"
	if tg := s.u.timeGeneric(); tg != "" && c.n(3) == 0 {
		after = tg
	}
	v := s.value(t, 2) + " after " + after
	if c.n(3) == 0 {
		return "transport " + v
	}
	return v
}

// block writes n sequential statements nested at most d deep.
func (s *scope) block(b *strings.Builder, ind string, n, d int) {
	for range n {
		s.stmt(b, ind, d)
	}
}

func (s *scope) stmt(b *strings.Builder, ind string, d int) {
	c := s.c()
	switch c.n(8) {
	case 1:
		if len(s.vars) > 0 {
			b.WriteString(ind + s.vars[c.n(len(s.vars))] + " := " +
				s.intExpr(2) + ";\n")
			return
		}
	case 2:
		if d > 0 {
			b.WriteString(ind + "if " + s.cond(1) + " then\n")
			s.block(b, ind+"  ", 1+c.n(2), d-1)
			if c.n(2) == 1 {
				b.WriteString(ind + "elsif " + s.cond(1) + " then\n")
				s.block(b, ind+"  ", 1, d-1)
			}
			if c.n(2) == 1 {
				b.WriteString(ind + "else\n")
				s.block(b, ind+"  ", 1, d-1)
			}
			b.WriteString(ind + "end if;\n")
			return
		}
	case 3:
		if d > 0 {
			l := "l" + strconv.Itoa(len(s.loops))
			b.WriteString(ind + "for " + l + " in 0 to " +
				strconv.Itoa(c.n(4)) + " loop\n")
			s.loops = append(s.loops, l)
			s.block(b, ind+"  ", 1+c.n(2), d-1)
			s.loops = s.loops[:len(s.loops)-1]
			b.WriteString(ind + "end loop;\n")
			return
		}
	case 4:
		if d > 0 {
			if sel := s.caseSelector(); sel != "" {
				b.WriteString(ind + "case " + sel + " is\n")
				b.WriteString(ind + "  when 0 =>\n")
				s.block(b, ind+"    ", 1, d-1)
				b.WriteString(ind + "  when 1 | 2 =>\n")
				s.block(b, ind+"    ", 1, d-1)
				b.WriteString(ind + "  when others =>\n")
				s.block(b, ind+"    ", 1, d-1)
				b.WriteString(ind + "end case;\n")
				return
			}
		}
	case 5, 6:
		if s.waits {
			b.WriteString(ind + s.wait() + ";\n")
			return
		}
	case 7:
		if s.report {
			b.WriteString(ind + "report \"" + s.u.g.label("r") + " \" & " +
				"integer'image(" + s.intExpr(1) + ");\n")
			return
		}
	}
	t := s.pickTarget(func(*sig) bool { return true })
	if t == nil {
		b.WriteString(ind + "null;\n")
		return
	}
	s.noteAssigned(t)
	v := s.value(t, 2)
	if c.n(3) == 0 {
		v = s.delay(t)
	}
	b.WriteString(ind + t.name + " <= " + v + ";\n")
}

func (s *scope) noteAssigned(t *sig) {
	for _, x := range s.assigned {
		if x == t {
			return
		}
	}
	s.assigned = append(s.assigned, t)
}

// caseSelector returns an integer variable or signal to case on, ""
// if none.
func (s *scope) caseSelector() string {
	if len(s.vars) > 0 && s.c().n(2) == 0 {
		return s.vars[s.c().n(len(s.vars))]
	}
	if x := s.pick(func(x *sig) bool { return x.t.isInt() }); x != nil {
		return x.name
	}
	return ""
}

// wait returns a bounded wait statement (without the semicolon).
func (s *scope) wait() string {
	c := s.c()
	t := strconv.Itoa(c.n(11)) + " ns"
	switch c.n(5) {
	case 1:
		if x := s.pick(func(*sig) bool { return true }); x != nil {
			return "wait on " + x.name + " for " + t
		}
	case 2:
		return "wait until " + s.cond(1) + " for " + t
	case 3:
		if x := s.pick(func(*sig) bool { return true }); x != nil {
			return "wait on " + x.name
		}
	case 4:
		return "wait until " + s.cond(1)
	}
	return "wait for " + t
}

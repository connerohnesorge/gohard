package elab

import (
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// The property: random process bodies over the integer variables a, b
// and c (assignments, if, case, for loops, procedure calls, waits in
// loops and inside procedures) end with the values a direct Go
// evaluation of the same program gives.

type pexpr struct {
	op   byte // 0 leaf, '+', '-', '*'
	v    int  // leaf: 0..2 a..c, 3+k loop parameter k, -1 constant
	c    int64
	l, r *pexpr
}

type pstmt struct {
	kind  int // 0 assign, 1 wait, 2 bump, 3 bumpw, 4 if, 5 for, 6 case
	v     int
	e     *pexpr
	k     int64
	hi    int
	param int
	body  [][]*pstmt
}

type pgen struct {
	r     env.SplitMix64
	depth int
	loops int
}

func (g *pgen) n(k int) int { return env.Intn(&g.r, k) }

func (g *pgen) leaf() *pexpr {
	switch g.n(3) {
	case 0:
		return &pexpr{v: g.n(3)}
	case 1:
		if g.loops > 0 {
			return &pexpr{v: 3 + g.n(g.loops)}
		}
	}
	return &pexpr{v: -1, c: int64(g.n(21))}
}

func (g *pgen) expr() *pexpr {
	l := g.leaf()
	switch g.n(4) {
	case 0:
		return l
	case 1:
		return &pexpr{op: '*', l: l, r: g.leaf()}
	case 2:
		return &pexpr{op: '-', l: l, r: g.leaf()}
	}
	return &pexpr{op: '+', l: l, r: g.leaf()}
}

func (g *pgen) list(n int) []*pstmt {
	out := make([]*pstmt, n)
	for i := range out {
		out[i] = g.stmt()
	}
	return out
}

func (g *pgen) stmt() *pstmt {
	k := g.n(7)
	if g.depth >= 2 && k >= 4 {
		k = g.n(4)
	}
	s := &pstmt{kind: k, v: g.n(3), e: g.expr()}
	g.depth++
	defer func() { g.depth-- }()
	switch k {
	case 4:
		s.k = int64(g.n(500))
		s.body = [][]*pstmt{g.list(1 + g.n(2)), g.list(1 + g.n(2))}
	case 5:
		s.hi, s.param = g.n(4), g.loops
		g.loops++
		s.body = [][]*pstmt{g.list(1 + g.n(2))}
		g.loops--
	case 6:
		s.body = [][]*pstmt{g.list(1), g.list(1), g.list(1)}
	}
	return s
}

func vmod(x int64) int64 { return ((x % 1009) + 1009) % 1009 }

type pstate struct {
	vars  [3]int64
	loops []int64
}

func (st *pstate) eval(e *pexpr) int64 {
	if e.op == 0 {
		switch {
		case e.v < 0:
			return e.c
		case e.v < 3:
			return st.vars[e.v]
		}
		return st.loops[e.v-3]
	}
	l, r := st.eval(e.l), st.eval(e.r)
	switch e.op {
	case '+':
		return l + r
	case '-':
		return l - r
	}
	return l * r
}

func (st *pstate) run(ss []*pstmt) {
	for _, s := range ss {
		switch s.kind {
		case 0:
			st.vars[s.v] = vmod(st.eval(s.e))
		case 2, 3:
			st.vars[s.v] = vmod(st.vars[s.v] + st.eval(s.e))
		case 4:
			if st.eval(s.e) > s.k {
				st.run(s.body[0])
			} else {
				st.run(s.body[1])
			}
		case 5:
			st.loops = append(st.loops, 0)
			for i := 0; i <= s.hi; i++ {
				st.loops[s.param] = int64(i)
				st.run(s.body[0])
			}
			st.loops = st.loops[:len(st.loops)-1]
		case 6:
			st.run(s.body[((st.eval(s.e)%3)+3)%3])
		}
	}
}

func exprText(e *pexpr) string {
	if e.op == 0 {
		switch {
		case e.v < 0:
			return strconv.FormatInt(e.c, 10)
		case e.v < 3:
			return string(rune('a' + e.v))
		}
		return "i" + strconv.Itoa(e.v-3)
	}
	return "(" + exprText(e.l) + " " + string(e.op) + " " +
		exprText(e.r) + ")"
}

func writeStmts(sb *strings.Builder, ss []*pstmt, ind int) {
	pad := strings.Repeat("  ", ind)
	for _, s := range ss {
		v := string(rune('a' + s.v))
		e := exprText(s.e)
		switch s.kind {
		case 0:
			sb.WriteString(pad + v + " := " + e + " mod 1009;\n")
		case 1:
			sb.WriteString(pad + "wait for 1 ns;\n")
		case 2:
			sb.WriteString(pad + "bump(" + v + ", " + e + ");\n")
		case 3:
			sb.WriteString(pad + "bumpw(" + v + ", " + e + ");\n")
		case 4:
			sb.WriteString(pad + "if " + e + " > " +
				strconv.FormatInt(s.k, 10) + " then\n")
			writeStmts(sb, s.body[0], ind+1)
			sb.WriteString(pad + "else\n")
			writeStmts(sb, s.body[1], ind+1)
			sb.WriteString(pad + "end if;\n")
		case 5:
			sb.WriteString(pad + "for i" + strconv.Itoa(s.param) +
				" in 0 to " + strconv.Itoa(s.hi) + " loop\n")
			writeStmts(sb, s.body[0], ind+1)
			sb.WriteString(pad + "end loop;\n")
		case 6:
			sb.WriteString(pad + "case " + e + " mod 3 is\n")
			for i, w := range []string{"0", "1", "others"} {
				sb.WriteString(pad + "  when " + w + " =>\n")
				writeStmts(sb, s.body[i], ind+2)
			}
			sb.WriteString(pad + "end case;\n")
		}
	}
}

func TestInterpRandomPrograms(t *testing.T) {
	for seed := range uint64(100) {
		g := &pgen{r: env.SplitMix64{State: seed}}
		var st pstate
		for i := range st.vars {
			st.vars[i] = int64(g.n(100))
		}
		init := st.vars
		prog := g.list(10)
		st.run(prog)
		var sb strings.Builder
		writeStmts(&sb, prog, 2)
		src := prelude + `
entity tb is end entity;
architecture a of tb is
  procedure bump(variable x : inout integer; d : integer) is
  begin
    x := (x + d) mod 1009;
  end procedure;
  procedure bumpw(variable x : inout integer; d : integer) is
  begin
    wait for 2 ns;
    x := (x + d) mod 1009;
  end procedure;
begin
  p : process
    variable a : integer := ` + strconv.FormatInt(init[0], 10) + `;
    variable b : integer := ` + strconv.FormatInt(init[1], 10) + `;
    variable c : integer := ` + strconv.FormatInt(init[2], 10) + `;
    variable l : line;
  begin
` + sb.String() + `
    write(l, a); write(l, ' '); write(l, b); write(l, ' '); write(l, c);
    writeline(output, l);
    wait;
  end process;
end architecture;
`
		r := simulate(t, src, "tb", runOpts{})
		r.clean(t)
		want := strconv.FormatInt(st.vars[0], 10) + " " +
			strconv.FormatInt(st.vars[1], 10) + " " +
			strconv.FormatInt(st.vars[2], 10)
		if len(r.out) != 1 || r.out[0] != want {
			t.Errorf("seed %d: OUTPUT %q, want %q\n%s", seed, r.out, want,
				sb.String())
		}
	}
}

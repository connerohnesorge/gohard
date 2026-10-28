package ghw

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
	"github.com/connerohnesorge/gohard/v2/internal/trace"
	"github.com/connerohnesorge/gohard/v2/internal/trace/tracetest"
)

// dump renders f the way GHDL's `ghwdump -t -H -s` (or, without
// values, `ghwdump -t -H -T`) prints it, following ghw_disp_types,
// ghw_disp_hie and ghw_disp_values in libghw.c.
func dump(f *ghwFile, values bool) string {
	var b strings.Builder
	for i := range f.types {
		if f.types[i].name != "" {
			dispType(&b, f, i+1)
		}
	}
	b.WriteString("design\n")
	dispHie(&b, f, f.top, "")
	show := func(vals []rval) {
		if !values {
			return
		}
		for n := 1; n <= f.nbasic; n++ {
			fmt.Fprintf(&b, "#%d: %s\n", n,
				dispValue(f, f.sigType[n], vals[n]))
		}
	}
	fmt.Fprintf(&b, "Time is %d fs\n", f.snapTime)
	cur := append([]rval(nil), f.snap...)
	show(cur)
	for _, c := range f.cycles {
		for k, n := range c.nums {
			cur[n] = c.vals[k]
		}
		fmt.Fprintf(&b, "Time is %d fs\n", c.time)
		show(cur)
	}
	return b.String()
}

// cg is C's printf %g.
func cg(x float64) string {
	switch {
	case math.IsNaN(x):
		if math.Signbit(x) {
			return "-nan"
		}
		return "nan"
	case math.IsInf(x, 1):
		return "inf"
	case math.IsInf(x, -1):
		return "-inf"
	}
	return strconv.FormatFloat(x, 'g', 6, 64)
}

func lit(t *rtype, e int64) string {
	if e < 0 || e >= int64(len(t.lits)) {
		return "??"
	}
	return t.lits[e]
}

func dispValue(f *ghwFile, id int, v rval) string {
	t := f.typ(f.baseOf(id))
	switch t.kind {
	case rtikB2, rtikE8:
		return fmt.Sprintf("%s (%d)", lit(t, v.i), v.i)
	case rtikI32, rtikP64:
		return strconv.FormatInt(v.i, 10)
	case rtikF64:
		return cg(v.f)
	}
	return fmt.Sprintf("<ghwdump exits on kind %d>", t.kind)
}

func dir(downto bool) string {
	if downto {
		return "downto"
	}
	return "to"
}

func dispRange(f *ghwFile, id int, g rrange) string {
	switch g.kind {
	case rtikB2, rtikE8:
		t := f.typ(id)
		return lit(t, g.lo) + " " + dir(g.downto) + " " + lit(t, g.hi)
	case rtikF64:
		return cg(g.flo) + " " + dir(g.downto) + " " + cg(g.fhi)
	}
	return fmt.Sprintf("%d %s %d", g.lo, dir(g.downto), g.hi)
}

func subtypeDef(f *ghwFile, id int) string {
	t := f.typ(id)
	switch t.kind {
	case rtikSubScalar:
		return f.typ(t.base).name + " range " +
			dispRange(f, t.base, t.rng)
	case rtikSubArray:
		b := f.typ(t.base)
		return b.name + " (" + dispRange(f, b.dims[0], t.rng) + ")"
	}
	return fmt.Sprintf("ghw_disp_subtype_definition: unhandled type "+
		"kind %d\n", t.kind)
}

// typeName is ghw_disp_typename: string 0 reads "<anon>".
func typeName(f *ghwFile, id int) string {
	if n := f.typ(id).name; n != "" {
		return n
	}
	return "<anon>"
}

func subtypeInd(f *ghwFile, id int) string {
	if t := f.typ(id); t.name != "" {
		return t.name
	}
	return subtypeDef(f, id)
}

func dispType(b *strings.Builder, f *ghwFile, id int) {
	t := f.typ(id)
	switch t.kind {
	case rtikB2, rtikE8:
		fmt.Fprintf(b, "type %s is (%s);", t.name,
			strings.Join(t.lits, ", "))
		if t.wkt != 0 {
			fmt.Fprintf(b, "  -- WKT:%d", t.wkt)
		}
		b.WriteString("\n")
	case rtikI32, rtikF64:
		fmt.Fprintf(b, "type %s is range <>;\n", t.name)
	case rtikP64:
		fmt.Fprintf(b, "type %s is range <> units\n", t.name)
		for _, u := range t.units {
			fmt.Fprintf(b, "  %s = %d %s;\n", u[0], u[1], t.units[0][0])
		}
		b.WriteString("end units;\n")
	case rtikArray:
		var ds []string
		for _, d := range t.dims {
			ds = append(ds, typeName(f, d)+" range <>")
		}
		fmt.Fprintf(b, "type %s is array (%s) of %s;\n", t.name,
			strings.Join(ds, ", "), subtypeInd(f, t.elem))
	case rtikRecord:
		fmt.Fprintf(b, "type %s is record\n", t.name)
		for _, fl := range t.fields {
			fmt.Fprintf(b, "  %s: %s;\n", fl.name, subtypeInd(f, fl.typ))
		}
		b.WriteString("end record;\n")
	case rtikSubScalar, rtikSubArray:
		fmt.Fprintf(b, "subtype %s is %s;\n", t.name, subtypeDef(f, id))
	default:
		fmt.Fprintf(b, "ghw_disp_type: unhandled type kind %d\n", t.kind)
	}
}

var hieNames = map[byte]string{
	hieBlock: "block", hieIfGen: "generate-if", hieForGen: "generate-for",
	hieInstance: "instance", hiePackage: "package", hieProcess: "process",
	hieSignal: "signal", hiePortIn: "port-in", hiePortOut: "port-out",
	hiePortInout: "port-inout", hiePortBuf: "port-buffer",
	hiePortLink: "port-linkage",
}

func dispHie(b *strings.Builder, f *ghwFile, hs []*rhie, prefix string) {
	for _, h := range hs {
		name := h.name
		if h.kind == hieForGen {
			name += "(" + dispValue(f, h.iterType, h.iter) + ")"
		}
		path := prefix + "/" + name
		fmt.Fprintf(b, "%s %s: ", hieNames[h.kind], path)
		switch h.kind {
		case hieProcess:
		case hieBlock, hieIfGen, hieForGen, hieInstance, hiePackage:
			if h.kind == hieForGen {
				fmt.Fprintf(b, "(%s)", dispValue(f, h.iterType, h.iter))
			}
		default:
			b.WriteString(subtypeInd(f, h.typ) + ":")
			for k := 0; k < len(h.sigs); {
				fmt.Fprintf(b, " #%d", h.sigs[k])
				n := 1
				for k+n < len(h.sigs) && h.sigs[k+n] == h.sigs[k+n-1]+1 {
					n++
				}
				if n > 1 {
					fmt.Fprintf(b, "-#%d", h.sigs[k+n-1])
				}
				k += n
			}
		}
		b.WriteString("\n")
		dispHie(b, f, h.kids, path)
	}
}

// ghwdumpExports are the traces TestGhwdump runs ghwdump on: the
// sample, the shapes trace, random traces and every corpus golden.
func ghwdumpExports(t *testing.T) map[string]*trace.Trace {
	out := map[string]*trace.Trace{
		"sample": tracetest.Sample(),
		"shapes": shapes(t),
	}
	for seed := range uint64(10) {
		out[fmt.Sprintf("random-%d", seed)] = randomTrace(t, seed)
	}
	for _, g := range corpusGoldens(t) {
		out["corpus-"+g.name] = g.tr
	}
	return out
}

// TestGhwdump runs GHDL's ghwdump (when it is on PATH, as in the dev
// shell) on every export and compares its output with dump of this
// package's reader: GHDL's reader and this one agree on every file,
// and roundTrip ties this one to the trace. With
// GOHARD_REQUIRE_GHWDUMP=1 (scripts/ci/ghwdump.sh) a missing ghwdump
// fails instead of skipping.
func TestGhwdump(t *testing.T) {
	e := realenv.New(realenv.Options{Dir: t.TempDir()})
	bin, err := e.Executor.LookPath("ghwdump")
	if err != nil {
		if os.Getenv("GOHARD_REQUIRE_GHWDUMP") == "1" {
			t.Fatalf("ghwdump is not on PATH: %v", err)
		}
		t.Skip("ghwdump is not on PATH; run in nix develop")
	}
	dir := t.TempDir()
	for name, tr := range ghwdumpExports(t) {
		f, data := check(t, tr)
		file := filepath.Join(dir, name+".ghw")
		if err := os.WriteFile(file, data, 0o644); err != nil {
			t.Fatal(err)
		}
		// -s prints every signal at every cycle: big traces get -T.
		values := f.nbasic*(len(f.cycles)+1) <= 1_000_000
		args := []string{"-t", "-H", "-T"}
		if values {
			args = []string{"-t", "-H", "-s"}
		}
		res, err := e.Executor.Run(context.Background(), env.Command{
			Path: bin, Args: append(args, file)})
		if err != nil {
			t.Fatal(err)
		}
		if res.ExitCode != 0 || len(res.Stderr) > 0 {
			t.Fatalf("%s: ghwdump %v exit %d: %s", name, args,
				res.ExitCode, res.Stderr)
		}
		want := dump(f, values)
		if n, a, b, ok := trace.FirstDiff([]byte(want), res.Stdout); ok {
			t.Fatalf("%s: ghwdump %v line %d: ghwdump %q, reader %q",
				name, args, n, b, a)
		}
	}
}

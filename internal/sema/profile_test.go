package sema

import (
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
)

// TestProfileAttributesCalls: the steps of a numeric_std call are
// attributed to ieee.numeric_std, every step charged to the budget is
// attributed exactly once, and two identical runs give identical
// profiles.
func TestProfileAttributesCalls(t *testing.T) {
	run := func() ([]UnitSteps, int64) {
		cfg := config.Defaults()
		cfg.Standard = "2008"
		src := "library ieee;\nuse ieee.numeric_std.all;\n" +
			"entity e is end entity;\n"
		r := Analyze(Input{Config: cfg, Jobs: env.Sequential{},
			Files: []Source{{Path: "e.vhd", Text: []byte(src)}}})
		lib, _ := r.Library("ieee")
		u, ok := lib.Unit("numeric_std")
		if !ok {
			t.Fatal("no numeric_std")
		}
		var fn *Decl
		for _, d := range u.Region.Decls {
			if d.Kind == DeclFunction && !d.Implicit &&
				d.Text == "TO_01" && len(d.Params) == 2 {
				fn = d
				break
			}
		}
		if fn == nil {
			t.Fatal("no TO_01")
		}
		ev := NewEvaluator(r, 0)
		p := NewProfile()
		at, err := ev.Layout(fn.Params[0].Type)
		if err != nil {
			t.Fatal(err)
		}
		rg := eval.Range{Left: 7, Right: 0, Dir: eval.Downto}
		el := make([]eval.Value, 8)
		for i := range el {
			el[i] = eval.Enum(at.Elem, int64(2+i%2))
		}
		lt, err := ev.Layout(fn.Params[1].Type)
		if err != nil {
			t.Fatal(err)
		}
		b := eval.NewBudget(0)
		p.Attach(b)
		_, err = ev.Call(fn, []eval.Value{
			eval.Array(at.ArraySubtype(rg), []eval.Range{rg}, el),
			eval.Enum(lt, 2)}, b)
		if err != nil {
			t.Fatal(err)
		}
		return p.Units(), b.Used()
	}
	us, used := run()
	var total int64
	found := false
	for _, u := range us {
		total += u.Steps
		found = found || u.Unit == "ieee.numeric_std" && u.Steps > 0
	}
	// Every step charged is attributed, exactly once.
	if !found || total != used {
		t.Fatalf("profile %v, budget used %d", us, used)
	}
	us2, used2 := run()
	if used2 != used || len(us2) != len(us) {
		t.Fatalf("profiles differ: %v %v", us, us2)
	}
	for i := range us {
		if us[i] != us2[i] {
			t.Fatalf("profiles differ: %v %v", us, us2)
		}
	}
}

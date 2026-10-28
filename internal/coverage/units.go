package coverage

import (
	"cmp"
	"path"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/elab/hier"
	"github.com/connerohnesorge/gohard/v2/internal/repro"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

// Units returns the coverable units of the design elaborated from top
// (elab.Design.Top) in the analysis r, in unit order: the entity and
// architecture of every entity instance and the PSL verification units
// bound to it (#66), every unit those depend on (Unit.Deps,
// transitively) and the body of every package among them, except the
// bundled std/ieee units, opaque units and duplicates.
func Units(r *sema.Result, top *hier.Instance) []*sema.Unit {
	seen := map[*sema.Unit]bool{} // lookup only
	var out, stack []*sema.Unit
	push := func(u *sema.Unit) {
		if u != nil && !seen[u] {
			seen[u] = true
			stack = append(stack, u)
		}
	}
	insts := []*hier.Instance{top}
	for len(insts) > 0 {
		in := insts[len(insts)-1]
		insts = insts[:len(insts)-1]
		if in == nil {
			continue
		}
		if in.Kind == hier.KindEntity {
			if in.Entity != nil {
				push(in.Entity.Unit)
			}
			push(in.Arch)
			for _, v := range in.Vunits {
				push(v)
			}
		}
		insts = append(insts, in.Children...)
	}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		out = append(out, u)
		for _, d := range u.Deps {
			push(d)
		}
		if u.Kind != sema.UnitPackage || r == nil {
			continue
		}
		if l, ok := r.Library(u.Library); ok {
			for _, b := range l.Secondary(u.Name) {
				if b.Kind == sema.UnitPackageBody {
					push(b)
				}
			}
		}
	}
	keep := out[:0]
	for _, u := range out {
		if u.File != nil && !u.File.Bundled && !u.Opaque && !u.Duplicate {
			keep = append(keep, u)
		}
	}
	slices.SortFunc(keep, func(a, b *sema.Unit) int {
		return cmp.Compare(a.Index, b.Index)
	})
	return keep
}

// Dir is the folder of the coverage files, relative to the project
// root.
const Dir = ".gohard/coverage"

// Merged is the slash name, relative to the project root, of the
// coverage of every testbench of a run.
const Merged = Dir + "/lcov.info"

// FileName is the slash name, relative to the project root, of the
// coverage of testbench tb at seed: .gohard/coverage/<tb>-<seed>.info,
// with tb escaped like trace and repro file names.
func FileName(tb string, seed uint64) string {
	return Dir + "/" + strings.TrimSuffix(path.Base(repro.FileName(tb,
		seed)), ".repro.json") + ".info"
}

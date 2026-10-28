package ide

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/resolve"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/server"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// navStats counts what checkNav proved.
type navStats struct {
	occurrences, bundled, crossFile, hovers, overloads, undecided int
}

// checkNav checks the navigation features on every name occurrence of
// the analysis that denotes a declaration with a source position
// (project or bundled std/ieee):
//
//   - definition(occurrence) is the declaration's location, and it is
//     in references(definition) (includeDeclaration), as is the
//     occurrence itself;
//   - hover there names the declaration (resolve.Summary: its
//     one-line description, or a type's declaration text);
//   - at an occurrence whose overload is not decided, hover shows the
//     message of the diagnostic the overload result gives, and that
//     diagnostic is the one published at the occurrence when sema
//     reported it (#34).
func checkNav(t *testing.T, an *Analysis) navStats {
	t.Helper()
	var st navStats
	refsAt := map[server.Location][]server.Location{}
	published := map[string]bool{}
	for _, fd := range an.Diagnostics {
		for _, d := range fd.Diagnostics {
			published[fmt.Sprint(fd.URI, d.Line, d.Col, d.Message)] = true
		}
	}
	for _, f := range an.Result.Files {
		furi, ok := an.URI(f.Path)
		if !ok {
			continue
		}
		for _, u := range f.Units {
			for _, ref := range u.Refs {
				q := server.Query{Snap: an.Snap, URI: furi,
					Offset: ref.Span.Start}
				if o := ref.Overload; o != nil && ref.Decl == nil {
					d, ok := o.Explain()
					if !ok {
						continue
					}
					h := an.HoverAt(q)
					if h == nil || !strings.Contains(h.Markdown, d.Message) {
						t.Fatalf("%s:%d: hover %v lacks %q", f.Path,
							ref.Span.Start, h, d.Message)
					}
					for _, c := range o.Sorted() {
						if !strings.Contains(h.Markdown,
							resolve.Describe(c.Decl)) {
							t.Fatalf("%s:%d: hover lacks candidate %s",
								f.Path, ref.Span.Start,
								resolve.Describe(c.Decl))
						}
					}
					checkUndecidedDefinition(t, an, q, ref, &st)
					l, c := f.Lines.LineCol(ref.Span.Start)
					if published[fmt.Sprint(furi, l, c, d.Message)] {
						st.overloads++
					}
					continue
				}
				d := ref.Decl
				dl, ok := resolve.LocationOf(d)
				if !ok || d.Implicit {
					continue
				}
				tg, ok := an.At(q)
				if !ok || !tg.IsRef || tg.Decl != d ||
					tg.Span != Span(ref.Span) {
					continue // a declaration sharing the position
				}
				st.occurrences++
				want, ok := an.LocationAll(dl)
				if !ok {
					t.Fatalf("no location for %s", dl.Path)
				}
				if dl.Bundled {
					st.bundled++
					src, ok := BundledSource(want.URI)
					if !ok || want.Lines == nil || !strings.EqualFold(
						string(src[want.Span.Start:want.Span.End]),
						d.Text) {
						t.Fatalf("bundled location %v of %s", want, d.Text)
					}
				} else if dl.Path != f.Path {
					st.crossFile++
				}
				def := an.LocationsAll(an.DefinitionAll(q))
				if !contains(def, want) {
					t.Fatalf("%s:%d %s: definition %v, want %v", f.Path,
						ref.Span.Start, d.Text, def, want)
				}
				all, ok := refsAt[want]
				if !ok {
					all = an.ReferencesAll(server.Query{Snap: an.Snap,
						URI: want.URI, Offset: want.Span.Start}, true)
					if dl.Bundled {
						// A bundled declaration is not a snapshot
						// document: ask from the occurrence.
						all = an.ReferencesAll(q, true)
					}
					refsAt[want] = all
				}
				self := server.Location{URI: furi, Span: Span(ref.Span)}
				if !contains(all, want) || !contains(all, self) {
					t.Fatalf("%s:%d %s: references(definition) miss "+
						"the definition or the occurrence", f.Path,
						ref.Span.Start, d.Text)
				}
				h := an.HoverAt(q)
				if h == nil || !strings.Contains(h.Markdown,
					resolve.Summary(d)) || *h.Span != Span(ref.Span) {
					t.Fatalf("%s:%d: hover %v", f.Path, ref.Span.Start, h)
				}
				st.hovers++
			}
		}
	}
	return st
}

// checkUndecidedDefinition: definition at an undecided call lists
// exactly the candidates that fit (an ambiguous call), or every
// candidate when none fits, each once (#25, #34).
func checkUndecidedDefinition(t *testing.T, an *Analysis,
	q server.Query, ref sema.Ref, st *navStats,
) {
	t.Helper()
	tg, ok := an.At(q)
	if !ok || !tg.IsRef || tg.Ref.Overload != ref.Overload {
		return // another occurrence shares the position
	}
	fits := 0
	for _, c := range ref.Overload.Candidates {
		if c.Fits {
			fits++
		}
	}
	want := map[server.Location]bool{}
	for _, c := range ref.Overload.Candidates {
		if fits > 0 && !c.Fits {
			continue
		}
		if dl, ok := resolve.LocationOf(c.Decl); ok {
			if l, ok := an.LocationAll(dl); ok {
				want[l] = true
			}
		}
	}
	got := an.LocationsAll(an.DefinitionAll(q))
	seen := map[server.Location]bool{}
	for _, l := range got {
		if !want[l] || seen[l] {
			t.Fatalf("%s:%d: definition %v, want %v", q.URI, q.Offset,
				got, want)
		}
		seen[l] = true
	}
	if len(seen) != len(want) {
		t.Fatalf("%s:%d: definition %v, want %v", q.URI, q.Offset, got,
			want)
	}
	st.undecided++
}

// TestNavCorpus: the navigation invariants on every corpus design.
func TestNavCorpus(t *testing.T) {
	ps := corpus(t)
	var total navStats
	for _, name := range slices.Sorted(maps.Keys(ps)) {
		st := checkNav(t, analyze(t, snapshotOf(t, ps[name], nil)))
		total.occurrences += st.occurrences
		total.bundled += st.bundled
		total.crossFile += st.crossFile
		total.hovers += st.hovers
		total.overloads += st.overloads
		total.undecided += st.undecided
	}
	if total.occurrences < 1000 || total.bundled < 100 ||
		total.crossFile < 50 {
		t.Fatalf("too little coverage: %+v", total)
	}
	t.Logf("corpus: %+v", total)
}

func TestNavOSVVM(t *testing.T) {
	st := checkNav(t, analyze(t, snapshotOf(t, osvvm(t), nil)))
	if st.crossFile < 1000 {
		t.Fatalf("too little coverage: %+v", st)
	}
	t.Logf("%+v", st)
}

func TestNavUVVM(t *testing.T) {
	st := checkNav(t, analyze(t, snapshotOf(t, uvvm(t), nil)))
	if st.crossFile < 1000 {
		t.Fatalf("too little coverage: %+v", st)
	}
	t.Logf("%+v", st)
}

func TestNavNEORV32(t *testing.T) {
	if testing.Short() {
		t.Skip("large project")
	}
	st := checkNav(t, analyze(t, snapshotOf(t, neorv(t), nil)))
	if st.crossFile < 500 {
		t.Fatalf("too little coverage: %+v", st)
	}
	t.Logf("%+v", st)
}

// TestNavOverloadFixture: the #34 overload fixture of
// testdata/lsp/hover: hover on an ambiguous and on a failed call
// shows the diagnostic published there and every candidate.
func TestNavOverloadFixture(t *testing.T) {
	p := project{"amb.vhd": readFixture(t,
		"../../testdata/lsp/hover/ambiguous-overload/in/amb.vhd")}
	an := analyze(t, snapshotOf(t, p, nil))
	st := checkNav(t, an)
	if st.overloads != 2 {
		t.Fatalf("%d overload diagnostics shown by hover, want 2",
			st.overloads)
	}
	if st.undecided != 2 {
		t.Fatalf("%d undecided definitions checked, want 2",
			st.undecided)
	}
}

// renameKey identifies a diagnostic across a rename: the file, line
// and code (a renamed name changes columns and messages only).
func diagKeys(an *Analysis) map[string]int {
	out := map[string]int{}
	for _, fd := range an.Diagnostics {
		for _, d := range fd.Diagnostics {
			out[fmt.Sprint(fd.URI, ":", d.Line, " ", d.Code)]++
		}
	}
	return out
}

// applyRename returns p with the rename's edits applied.
func applyRename(t *testing.T, p project, r server.Renamed) project {
	t.Helper()
	out := maps.Clone(p)
	for _, fe := range r.Edits {
		rel := strings.TrimPrefix(fe.URI, "file:///work/")
		text := slices.Clone(out[rel])
		es := slices.Clone(fe.Edits)
		slices.SortFunc(es, func(a, b server.Edit) int {
			return b.Span.Start - a.Span.Start
		})
		for _, e := range es {
			text = slices.Concat(text[:e.Span.Start], []byte(e.NewText),
				text[e.Span.End:])
		}
		out[rel] = text
	}
	return out
}

// checkRenames renames every stride-th renamable project declaration
// reached from an occurrence to a fresh name and checks that the
// re-analysis has no new diagnostic and that every edited site now
// denotes a declaration of the new name.
func checkRenames(t *testing.T, p project, stride int) int {
	t.Helper()
	an := analyze(t, snapshotOf(t, p, nil))
	before := diagKeys(an)
	seen := map[*sema.Decl]bool{}
	n, i := 0, 0
	for _, f := range an.Result.Files {
		furi, ok := an.URI(f.Path)
		if !ok {
			continue
		}
		for _, u := range f.Units {
			for _, ref := range u.Refs {
				d := ref.Decl
				if d == nil || seen[d] || d.File == nil || d.File.Bundled {
					continue
				}
				seen[d] = true
				q := server.Query{Snap: an.Snap, URI: furi,
					Offset: ref.Span.Start}
				if an.PrepareRenameAt(q) == nil || bindsByName(an, d) {
					continue
				}
				if i++; i%stride != 0 {
					continue
				}
				name := fmt.Sprintf("zq_renamed_%d", n)
				r := an.RenameAt(q, name)
				if r.Refused != "" || len(r.Edits) == 0 {
					t.Fatalf("%s:%d rename %s: %+v", f.Path,
						ref.Span.Start, d.Text, r)
				}
				after := analyze(t, snapshotOf(t, applyRename(t, p, r),
					nil))
				for k, c := range diagKeys(after) {
					if c > before[k] {
						t.Fatalf("renaming %s (%s) at %s:%d to %s: new "+
							"diagnostic %s\n%v", d.Text, d.Kind, f.Path,
							ref.Span.Start, name, k, after.Diagnostics)
					}
				}
				checkRenamed(t, after, r, name, d)
				n++
			}
		}
	}
	return n
}

// bindsByName reports an architecture whose entity has several: which
// one a default binding picks follows the names, so a rename may
// legitimately change the design.
func bindsByName(an *Analysis, d *sema.Decl) bool {
	if d.Kind != sema.DeclArchitecture {
		return false
	}
	n := 0
	for _, u := range an.Result.Units {
		if u.Decl != nil && u.Decl.Kind == sema.DeclArchitecture &&
			u.Decl.Spec != nil && d.Spec != nil &&
			u.Decl.Spec.Name == d.Spec.Name {
			n++
		}
	}
	return n > 1
}

// checkRenamed checks that every edited site of r denotes a
// declaration spelled name in the renamed analysis.
func checkRenamed(t *testing.T, an *Analysis, r server.Renamed,
	name string, old *sema.Decl,
) {
	t.Helper()
	for _, fe := range r.Edits {
		shift := 0
		for _, e := range fe.Edits {
			off := e.Span.Start + shift
			shift += len(e.NewText) - (e.Span.End - e.Span.Start)
			tg, ok := an.At(server.Query{Snap: an.Snap, URI: fe.URI,
				Offset: off})
			if !ok || tg.Decl == nil ||
				!strings.EqualFold(tg.Decl.Text, name) {
				t.Fatalf("renamed %s (%s): %s at %d denotes %+v",
					old.Text, old.Kind, fe.URI, off, tg.Decl)
			}
		}
	}
}

// TestRenameCorpus: rename every renamable declaration of the corpus
// that an occurrence reaches; re-analysis gives no new diagnostic.
func TestRenameCorpus(t *testing.T) {
	ps := corpus(t)
	n := 0
	for _, name := range slices.Sorted(maps.Keys(ps)) {
		n += checkRenames(t, ps[name], 1)
	}
	if n < 300 {
		t.Fatalf("only %d renames", n)
	}
	t.Logf("%d renames", n)
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRenameLibraries: sampled renames in OSVVM, UVVM (across its
// three libraries) and NEORV32 give no new diagnostic.
func TestRenameLibraries(t *testing.T) {
	if testing.Short() {
		t.Skip("large projects")
	}
	for _, c := range []struct {
		name   string
		p      func(testing.TB) project
		stride int
	}{{"osvvm", osvvm, 2400}, {"uvvm", uvvm, 2000},
		{"neorv32", neorv, 1100}} {
		t.Run(c.name, func(t *testing.T) {
			n := checkRenames(t, c.p(t), c.stride)
			if n < 3 {
				t.Fatalf("only %d renames", n)
			}
			t.Logf("%d renames", n)
		})
	}
}

// TestRenameToVisibleNameCorpus: renaming a declaration to the name of
// another project declaration visible at it or in its region is either
// refused as a conflict, or gives no new diagnostic and every edited
// site still denotes the renamed entity.
func TestRenameToVisibleNameCorpus(t *testing.T) {
	ps := corpus(t)
	refused, applied := 0, 0
	for _, pname := range slices.Sorted(maps.Keys(ps)) {
		p := ps[pname]
		an := analyze(t, snapshotOf(t, p, nil))
		before := diagKeys(an)
		seen := map[*sema.Decl]bool{}
		for _, f := range an.Result.Files {
			furi, ok := an.URI(f.Path)
			if !ok {
				continue
			}
			for _, u := range f.Units {
				for _, ref := range u.Refs {
					d := ref.Decl
					if d == nil || seen[d] || d.File == nil ||
						d.File.Bundled {
						continue
					}
					seen[d] = true
					q := server.Query{Snap: an.Snap, URI: furi,
						Offset: ref.Span.Start}
					if an.PrepareRenameAt(q) == nil ||
						bindsByName(an, d) {
						continue
					}
					for _, name := range clashNames(an, d) {
						r := an.RenameAt(q, name)
						if r.Refused != "" {
							if !strings.Contains(r.Refused,
								"already in use") {
								t.Fatalf("%s to %s: %s", d.Text, name,
									r.Refused)
							}
							refused++
							continue
						}
						after := analyze(t, snapshotOf(t,
							applyRename(t, p, r), nil))
						for k, c := range diagKeys(after) {
							if c > before[k] {
								t.Fatalf("renaming %s (%s) to %s: "+
									"new diagnostic %s", d.Text, d.Kind,
									name, k)
							}
						}
						checkRenamed(t, after, r, name, d)
						applied++
					}
				}
			}
		}
	}
	if refused < 100 {
		t.Fatalf("only %d conflicts refused", refused)
	}
	t.Logf("%d conflicts refused, %d accepted renames", refused,
		applied)
}

// clashNames returns up to three names of other project declarations
// of d's region or visible at d, in a fixed order.
func clashNames(an *Analysis, d *sema.Decl) []string {
	var out []string
	add := func(x *sema.Decl) {
		if len(out) >= 3 || x == d || x.File == nil || x.File.Bundled ||
			x.Implicit || x.Kind == sema.DeclLibrary ||
			x.Name == d.Name || strings.HasPrefix(x.Text, "\\") ||
			slices.Contains(out, x.Text) {
			return
		}
		k := sema.KeyOf(token.Identifier, x.Text)
		if k.IsOperator() || k.IsCharacter() {
			return
		}
		out = append(out, x.Text)
	}
	if d.Parent != nil {
		for _, x := range d.Parent.Decls {
			add(x)
		}
	}
	for _, x := range an.Result.VisibleAt(d.File.Path, d.Span.Start) {
		add(x)
	}
	return out
}

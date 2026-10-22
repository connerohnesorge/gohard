package main

import (
	"fmt"
	"path"
	"strings"

	"github.com/connerohnesorge/gohard/internal/corpus"
	"github.com/connerohnesorge/gohard/internal/elab/hier"
	"github.com/connerohnesorge/gohard/internal/sema"
	"github.com/connerohnesorge/gohard/internal/token"
)

// The tree section (#8): the elaboration oracle. For designs with
// hierarchy_oracle: ghdl it records GHDL's --disp-tree=inst output as
// golden/ghdl.tree.txt and checks that GoHard's hierarchy
// (internal/elab/hier) has the same instances, generate iterations
// and blocks in the same order. For "none, hand-checked" designs the
// golden is a hand-written golden/hierarchy.txt in hier's Dump form
// (generic values included), compared byte for byte after its
// leading '#' comment lines.

const treeMagic = "gohard-tree 1"

func treeFile(d design) string {
	if d.HierarchyOracle == corpus.HandChecked {
		return "hierarchy.txt"
	}
	return "ghdl.tree.txt"
}

var treeSection = &section{
	name: "tree",
	targets: func(x *ctx) ([]target, error) {
		var ts []target
		for _, d := range x.man.Designs {
			if d.HierarchyOracle == "" {
				continue
			}
			if d.HierarchyOracle != "ghdl" &&
				d.HierarchyOracle != corpus.HandChecked {
				return nil, fmt.Errorf("%s: hierarchy_oracle %q", d.Name,
					d.HierarchyOracle)
			}
			ts = append(ts, target{
				Ref: entryRef{List: "designs", Name: d.Name,
					Field: fieldOracle, After: "trace_sha256"},
				Dir:   path.Join(d.dir(), "golden"),
				Files: []string{treeFile(d)},
			})
		}
		return ts, nil
	},
	record: recordTree,
	check:  checkTree,
}

func recordTree(x *ctx, tl *toolset, t target) ([]goldenFile, error) {
	d, _ := x.design(t.Ref.Name)
	if d.HierarchyOracle == corpus.HandChecked {
		// Hand-written; the record step only keeps it.
		rel := path.Join(t.Dir, "hierarchy.txt")
		b, err := readRepo(x.root, rel)
		if err != nil {
			return nil, fmt.Errorf("%s is hand-written (hierarchy_oracle"+
				" %q); write it first", rel, d.HierarchyOracle)
		}
		return []goldenFile{{Name: "hierarchy.txt", Data: b}}, nil
	}
	tt, err := tl.get("ghdl")
	if err != nil {
		return nil, err
	}
	order, files, err := toolSources(x, d)
	if err != nil {
		return nil, err
	}
	std, _ := token.ParseStandard(d.Standard)
	w, err := newWorkDir(files, order)
	if err != nil {
		return nil, err
	}
	defer w.close()
	a := append([]string{"-a", "--std=" + ghdlStd(std)}, order...)
	if out, code, err := tt.run(w.dir, a...); err != nil || code != 0 {
		return nil, fmt.Errorf("ghdl -a: exit %d %v\n%s", code, err, out)
	}
	out, _, err := tt.run(w.dir, "--elab-run", "--std="+ghdlStd(std),
		d.Testbench, "--disp-tree=inst", "--stop-time=0fs")
	if err != nil {
		return nil, err
	}
	src, err := sourcesHash(x, d)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\ntool ghdl %s\nsources %s\n", treeMagic,
		tt.version, src)
	for _, l := range strings.Split(out, "\n") {
		if strings.HasSuffix(strings.TrimSpace(l), "]") &&
			!strings.Contains(l, "(report") &&
			!strings.Contains(l, "(assertion") {
			b.WriteString(strings.TrimRight(l, " ") + "\n")
		}
	}
	return []goldenFile{{Name: "ghdl.tree.txt",
		Data: []byte(b.String())}}, nil
}

// TreeNode is one hierarchy level the elaboration oracle compares.
type treeNode struct {
	path string
	kind string // instance, block, for-generate, if-generate
}

// parseGHDLTree reads the instances of the testbench tree out of a
// --disp-tree=inst listing: one node per line, two columns per level,
// "name [kind]". Entities and architectures do not add a level to the
// path; packages after the testbench tree are dropped.
func parseGHDLTree(text, tb string) ([]treeNode, error) {
	var out []treeNode
	type level struct {
		depth int
		path  string
	}
	var stack []level
	for i, l := range strings.Split(text, "\n") {
		o := strings.IndexByte(l, '[')
		if o < 0 || !strings.HasSuffix(l, "]") {
			continue
		}
		kind := strings.Fields(l[o+1 : len(l)-1])
		lead := strings.TrimLeft(l[:o], " |`+-")
		name := strings.ToLower(strings.TrimSpace(lead))
		depth := (o - len(lead)) / 2
		if depth == 0 {
			if len(out) > 0 || stack != nil {
				break // the packages
			}
			if name != strings.ToLower(tb) {
				return nil, fmt.Errorf("line %d: top %q, want %q", i+1,
					name, tb)
			}
			stack = []level{{0, name}}
			out = append(out, treeNode{name, "instance"})
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].depth >= depth {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			return nil, fmt.Errorf("line %d: bad nesting", i+1)
		}
		parent := stack[len(stack)-1].path
		k := kind[0]
		switch k {
		case "instance", "block", "for-generate", "if-generate",
			"case-generate":
			p := parent + "." + name
			stack = append(stack, level{depth, p})
			out = append(out, treeNode{p, k})
		case "entity", "arch":
			stack = append(stack, level{depth, parent})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no tree for %s", tb)
	}
	return out, nil
}

// hierNodes projects GoHard's hierarchy onto what --disp-tree shows:
// the alternative label of an if or case generate is dropped from
// the path (GHDL prints only the generate label).
func hierNodes(r *hier.Result) []treeNode {
	var out []treeNode
	var walk func(in *hier.Instance, parentHier, parentCmp string)
	walk = func(in *hier.Instance, parentHier, parentCmp string) {
		seg := strings.TrimPrefix(in.Path, parentHier)
		if in.Alt != nil && in.Alt.Label != nil {
			seg = strings.TrimSuffix(seg, "."+string(in.Alt.Label.Name))
		}
		p := parentCmp + seg
		k := "instance"
		switch in.Kind {
		case hier.KindBlock:
			k = "block"
		case hier.KindGenerate:
			switch in.Stmt.Kind {
			case sema.StmtForGenerate:
				k = "for-generate"
			case sema.StmtIfGenerate:
				k = "if-generate"
			default:
				k = "case-generate"
			}
		}
		out = append(out, treeNode{p, k})
		for _, c := range in.Children {
			walk(c, in.Path, p)
		}
	}
	for _, t := range r.Tops {
		walk(t, "", "")
	}
	return out
}

func checkTree(x *ctx, t target, golden []goldenFile) []string {
	d, _ := x.design(t.Ref.Name)
	m, err := corpus.ParseManifest(x.src)
	if err != nil {
		return []string{err.Error()}
	}
	cd, _ := m.Design(d.Name)
	h, err := corpus.Hierarchy(cd.Runs()[0], func(rel string) ([]byte,
		error) {
		return readRepo(x.root, rel)
	})
	if err != nil {
		return []string{err.Error()}
	}
	text := string(golden[0].Data)
	if d.HierarchyOracle == corpus.HandChecked {
		if !corpus.Reached(d.Milestone) {
			// The golden is the answer at the design's milestone,
			// which corpus.Release has not reached (corpus.ParseManifest
			// rejects such a design; kept for the next milestone).
			return nil
		}
		var want strings.Builder
		for _, l := range strings.SplitAfter(text, "\n") {
			if !strings.HasPrefix(l, "#") {
				want.WriteString(l)
			}
		}
		return diffLines("hierarchy.txt", want.String(), h.Dump())
	}
	lines := strings.SplitN(text, "\n", 4)
	src, err := sourcesHash(x, d)
	if err != nil {
		return []string{err.Error()}
	}
	if len(lines) < 4 || lines[0] != treeMagic ||
		lines[2] != "sources "+src {
		return []string{"ghdl.tree.txt is stale: " + rerun}
	}
	want, err := parseGHDLTree(lines[3], d.Testbench)
	if err != nil {
		return []string{"ghdl.tree.txt: " + err.Error()}
	}
	var a, b strings.Builder
	for _, n := range want {
		fmt.Fprintf(&a, "%s %s\n", n.path, n.kind)
	}
	for _, n := range hierNodes(h) {
		fmt.Fprintf(&b, "%s %s\n", n.path, n.kind)
	}
	return diffLines("GHDL --disp-tree", a.String(), b.String())
}

// diffLines reports the first line where got differs from want.
func diffLines(what, want, got string) []string {
	if want == got {
		return nil
	}
	w := strings.Split(want, "\n")
	g := strings.Split(got, "\n")
	i := firstDiff(w, g)
	return []string{fmt.Sprintf("%s line %d: want %q, GoHard has %q",
		what, i+1, at(w, i), at(g, i))}
}

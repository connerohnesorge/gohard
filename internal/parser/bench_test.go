package parser

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// BenchmarkParse measures full reparse for #22: wall time per 1k lines
// on every corpus file (testdata/corpus/**/*.vhd) and on the largest
// NEORV32 file, plus CST size. It is separate from the determinism
// checks and never fails on timing; scripts/ci/bench.sh records it.
//
// Metrics per sub-benchmark:
//   - ms/1klines: wall time of one full parse per 1000 source lines.
//   - nodes: CST elements (nodes and tokens) in the tree, counting
//     every occurrence of a shared green.
//   - retainedB/node: bytes of the tree's distinct greens, child slots
//     and trivia (arena chunk slack and token text excluded), per node.
//
// Allocated bytes per node is B/op (from -benchmem) divided by nodes.
func BenchmarkParse(b *testing.B) {
	type set struct {
		name string
		srcs [][]byte
	}
	var corpus [][]byte
	var each []set
	const root = "../../testdata/corpus"
	for _, f := range benchCorpusFiles(b, root) {
		src, err := os.ReadFile(f)
		if err != nil {
			b.Fatal(err)
		}
		rel, _ := filepath.Rel(root, f)
		corpus = append(corpus, src)
		each = append(each, set{"file/" + filepath.ToSlash(rel),
			[][]byte{src}})
	}
	sets := append([]set{{"corpus", corpus}}, each...)
	if f := largestVHDL("../../testdata/neorv32"); f != "" {
		if src, err := os.ReadFile(f); err == nil {
			sets = append(sets, set{"neorv32/" + filepath.Base(f),
				[][]byte{src}})
		}
	}
	opts := Options{Standard: token.Std2019}
	for _, set := range sets {
		b.Run(set.name, func(b *testing.B) {
			lines, n, nodes, retained := 0, 0, 0, 0
			for _, src := range set.srcs {
				lines += bytes.Count(src, []byte("\n"))
				n += len(src)
				c, r := treeSize(Parse(src, opts).Root)
				nodes += c
				retained += r
			}
			b.SetBytes(int64(n))
			b.ReportAllocs()
			for b.Loop() {
				for _, src := range set.srcs {
					Parse(src, opts)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/
				float64(b.N)/float64(lines)*1000/1e6, "ms/1klines")
			b.ReportMetric(float64(nodes), "nodes")
			b.ReportMetric(float64(retained)/float64(nodes),
				"retainedB/node")
		})
	}
}

// benchCorpusFiles lists every .vhd file under root, at any depth, in
// lexical order.
func benchCorpusFiles(b *testing.B, root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".vhd") {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		b.Fatal("no corpus files under " + root)
	}
	slices.Sort(files)
	return files
}

// largestVHDL returns the .vhd file under root with the most lines, or
// "" if there is none (the NEORV32 submodule is not checked out).
func largestVHDL(root string) string {
	best, most := "", -1
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".vhd") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if n := bytes.Count(src, []byte("\n")); n > most {
			best, most = p, n
		}
		return nil
	})
	return best
}

var (
	greenSize  = int(reflect.TypeFor[cst.Green]().Size())
	slotSize   = int(reflect.TypeFor[*cst.Green]().Size())
	triviaSize = int(reflect.TypeFor[cst.Trivia]().Size())
)

// treeSize returns the number of CST elements in the tree under g
// (every occurrence) and the bytes held by its distinct greens.
func treeSize(g *cst.Green) (count, retained int) {
	seen := map[*cst.Green]bool{}
	var walk func(g *cst.Green)
	walk = func(g *cst.Green) {
		count++
		if !seen[g] {
			seen[g] = true
			retained += greenSize + g.NumChildren()*slotSize +
				(len(g.Leading())+len(g.Trailing()))*triviaSize
		}
		for i := range g.NumChildren() {
			walk(g.Child(i))
		}
	}
	walk(g)
	return count, retained
}

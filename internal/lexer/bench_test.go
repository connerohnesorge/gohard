package lexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/connerohnesorge/gohard/internal/token"
)

// BenchmarkLex measures lexing the corpus files.
func BenchmarkLex(b *testing.B) {
	files, _ := filepath.Glob("../../testdata/corpus/*/*.vhd")
	var srcs [][]byte
	n := 0
	for _, f := range files {
		src, _ := os.ReadFile(f)
		srcs = append(srcs, src)
		n += len(src)
	}
	b.SetBytes(int64(n))
	for b.Loop() {
		for _, src := range srcs {
			l := New(src, Options{Standard: token.Std2019})
			st := Initial(VHDL)
			off := 0
			for {
				t, next := l.Lex(off, st)
				l.Reset()
				if t.Kind == token.EOF {
					break
				}
				off, st = t.FullEnd, next
			}
		}
	}
}

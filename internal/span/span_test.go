package span

import (
	"fmt"
	"testing"
)

// pieces are the alphabet of the generator: every kind of line
// terminator, runes of 1 to 4 bytes, and invalid or truncated UTF-8.
var pieces = []string{
	"a", " ", "\t", "\n", "\r", "\r\n", "é", "€", "😀", "𝄞",
	"\xff", "\xe2\x82", "\xed\xa0\x80", "\xf0\x9f", "\x00",
}

// edges are the edge-value inputs tried before the random ones (P4).
var edges = []string{
	"", "\n", "\r", "\r\n", "\n\r", "\r\r\n\n", "a", "a\r\nb",
	"€", "😀", "a😀b\n😀", "\xff", "\xe2\x82", "\xe2\x82\n",
	"x\r", "\r\nx\r\n", "𝄞\r\n𝄞",
}

// rng is a splitmix64 generator so the tests are reproducible from a
// printed seed without math/rand.
type rng struct{ s uint64 }

func (r *rng) next() uint64 {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *rng) intn(n int) int { return int(r.next() % uint64(n)) }

func inputs(seed uint64, n int) []string {
	out := append([]string(nil), edges...)
	r := &rng{s: seed}
	for range n {
		var b []byte
		for range r.intn(40) {
			b = append(b, pieces[r.intn(len(pieces))]...)
		}
		out = append(out, string(b))
	}
	return out
}

// point is one rune boundary in the reference model.
type point struct {
	off  int
	line int
	col  [4]int // indexed by Encoding
}

// model is the obviously-correct reference: it walks the text rune by
// rune with `range` over a string and records every boundary.
func model(src string) (pts []point, lines int) {
	line := 0
	var col [4]int
	emit := func(off int) {
		pts = append(pts, point{off: off, line: line, col: col})
	}
	for i := 0; i < len(src); {
		if src[i] == '\n' || src[i] == '\r' {
			emit(i)
			if src[i] == '\r' && i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			i++
			line++
			col = [4]int{}
			continue
		}
		emit(i)
		var r rune
		var size int
		for j, rr := range src[i:] {
			if j > 0 {
				size = j
				break
			}
			r = rr
			size = len(src) - i
		}
		col[UTF8] += size
		col[UTF16]++
		if r >= 0x10000 {
			col[UTF16]++
		}
		col[UTF32]++
		i += size
	}
	emit(len(src))
	return pts, line + 1
}

var encodings = []Encoding{UTF8, UTF16, UTF32}

func TestIndexMatchesModel(t *testing.T) {
	const seed = 1
	for n, src := range inputs(seed, 2000) {
		x := NewIndex([]byte(src))
		pts, lines := model(src)
		if x.LineCount() != lines {
			t.Fatalf("seed %d case %d %q: LineCount = %d, want %d",
				seed, n, src, x.LineCount(), lines)
		}
		for _, p := range pts {
			for _, enc := range encodings {
				got := x.Position(p.off, enc)
				want := Position{Line: p.line, Col: p.col[enc]}
				if got != want {
					t.Fatalf("seed %d case %d %q off %d %v: "+
						"Position = %v, want %v",
						seed, n, src, p.off, enc, got, want)
				}
				off, ok := x.Offset(want, enc)
				if !ok || off != p.off {
					t.Fatalf("seed %d case %d %q %v %v: "+
						"Offset = %d,%v, want %d",
						seed, n, src, want, enc, off, ok, p.off)
				}
			}
		}
	}
}

func TestPositionMonotonicAndTotal(t *testing.T) {
	for n, src := range inputs(2, 500) {
		x := NewIndex([]byte(src))
		for _, enc := range encodings {
			prev := Position{}
			for off := -2; off <= len(src)+2; off++ {
				p := x.Position(off, enc)
				if p.Line < prev.Line ||
					p.Line == prev.Line && p.Col < prev.Col {
					t.Fatalf("case %d %q %v: Position(%d) = %v "+
						"before %v", n, src, enc, off, p, prev)
				}
				prev = p
				back, ok := x.Offset(p, enc)
				if !ok || back > max(0, off) ||
					back < x.LineStart(p.Line) ||
					back > x.LineEnd(p.Line) {
					t.Fatalf("case %d %q %v: Offset(%v) = %d,%v "+
						"for off %d", n, src, enc, p, back, ok, off)
				}
			}
		}
	}
}

func TestOffsetClampsAndRejects(t *testing.T) {
	r := &rng{s: 3}
	for n, src := range inputs(3, 500) {
		x := NewIndex([]byte(src))
		for range 50 {
			p := Position{Line: r.intn(8) - 2, Col: r.intn(80) - 4}
			for _, enc := range encodings {
				off, ok := x.Offset(p, enc)
				valid := p.Line >= 0 && p.Line < x.LineCount() &&
					p.Col >= 0
				if ok != valid {
					t.Fatalf("case %d %q: Offset(%v) ok = %v",
						n, src, p, ok)
				}
				if !ok {
					continue
				}
				if off < x.LineStart(p.Line) ||
					off > x.LineEnd(p.Line) {
					t.Fatalf("case %d %q: Offset(%v) = %d outside "+
						"line", n, src, p, off)
				}
				// Clamped columns never point past the request.
				if q := x.Position(off, enc); q.Col > p.Col {
					t.Fatalf("case %d %q: column grew %v -> %v",
						n, src, p, q)
				}
			}
		}
	}
}

func TestConvertRoundTrip(t *testing.T) {
	for n, src := range inputs(4, 500) {
		x := NewIndex([]byte(src))
		pts, _ := model(src)
		for _, pt := range pts {
			p8 := Position{Line: pt.line, Col: pt.col[UTF8]}
			p16, ok := x.Convert(p8, UTF8, UTF16)
			if !ok || p16.Col != pt.col[UTF16] {
				t.Fatalf("case %d %q: %v -> %v", n, src, p8, p16)
			}
			back, ok := x.Convert(p16, UTF16, UTF8)
			if !ok || back != p8 {
				t.Fatalf("case %d %q: %v -> %v -> %v",
					n, src, p8, p16, back)
			}
		}
	}
}

func TestLineColOneBased(t *testing.T) {
	x := NewIndex([]byte("ab\r\n€x\n"))
	tests := []struct{ off, line, col int }{
		{0, 1, 1}, {2, 1, 3}, {3, 1, 3}, {4, 2, 1},
		{5, 2, 1}, {7, 2, 4}, {8, 2, 5}, {9, 3, 1}, {99, 3, 1},
	}
	for _, tt := range tests {
		l, c := x.LineCol(tt.off)
		if l != tt.line || c != tt.col {
			t.Errorf("LineCol(%d) = %d:%d, want %d:%d",
				tt.off, l, c, tt.line, tt.col)
		}
	}
	if off, ok := x.OffsetOf(2, 4); !ok || off != 7 {
		t.Errorf("OffsetOf(2,4) = %d,%v, want 7", off, ok)
	}
	if _, ok := x.OffsetOf(0, 1); ok {
		t.Error("OffsetOf(0,1) accepted line 0")
	}
}

func TestFileSet(t *testing.T) {
	var fs FileSet
	if fs.Path(NoFile) != "" || fs.Path(7) != "" {
		t.Fatal("unknown IDs must have no path")
	}
	for i, p := range []string{"a.vhd", "b/c.vhd", "a.vhd", "d.vhd"} {
		id := fs.Add(p)
		if fs.Path(id) != p {
			t.Fatalf("step %d: Path(Add(%q)) = %q", i, p, fs.Path(id))
		}
	}
	if fs.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (duplicates count once)", fs.Len())
	}
	if id, ok := fs.Lookup("d.vhd"); !ok || id != 3 {
		t.Fatalf("Lookup(d.vhd) = %d,%v, want 3", id, ok)
	}
}

func TestSpan(t *testing.T) {
	s := Span{File: 1, Start: 3, End: 7}
	if s.Len() != 4 || !s.Contains(3) || s.Contains(7) {
		t.Fatal(fmt.Sprint(s))
	}
	if e := (Span{Start: 5, End: 5}); !e.Contains(5) || e.Len() != 0 {
		t.Fatal("empty span must contain its start")
	}
	if c := s.Cover(Span{Start: 1, End: 4}); c != (Span{1, 1, 7}) {
		t.Fatalf("Cover = %v", c)
	}
	if (Span{Start: 5, End: 2}).Len() != 0 {
		t.Fatal("inverted span has length 0")
	}
}

func FuzzIndex(f *testing.F) {
	for _, s := range edges {
		f.Add([]byte(s), 0, 0)
	}
	f.Fuzz(func(t *testing.T, src []byte, line, col int) {
		x := NewIndex(src)
		for _, enc := range encodings {
			off, ok := x.Offset(Position{line, col}, enc)
			if ok {
				p := x.Position(off, enc)
				if back, _ := x.Offset(p, enc); back != off {
					t.Fatalf("%v: %d -> %v -> %d", enc, off, p, back)
				}
			}
		}
	})
}

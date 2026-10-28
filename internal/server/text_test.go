package server

import (
	"bytes"
	"path/filepath"
	"slices"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/connerohnesorge/gohard/v2/internal/span"
)

// TestQueueOrder: whatever the push order, events pop sorted by (time,
// priority, push sequence), and a Push never changes an older queue.
func TestQueueOrder(t *testing.T) {
	for seed := range uint64(200) {
		r := &rng{}
		r.State = seed
		var q Queue
		var pushed []Event
		for i := range 50 {
			ev := Event{At: time.Duration(r.n(5)), Pri: Priority(r.n(3)),
				Body: Work{ID: uint64(i)}}
			old := q
			oldLen := old.Len()
			q = q.Push(ev)
			if old.Len() != oldLen {
				t.Fatal("Push changed the older queue")
			}
			ev.Seq = uint64(i + 1)
			pushed = append(pushed, ev)
		}
		slices.SortStableFunc(pushed, func(a, b Event) int {
			if a.At != b.At {
				return int(a.At - b.At)
			}
			return int(a.Pri) - int(b.Pri)
		})
		for i, want := range pushed {
			var got Event
			var ok bool
			q, got, ok = q.Pop()
			if !ok || got.Seq != want.Seq {
				t.Fatalf("seed %d: pop %d = %+v, want %+v", seed, i, got,
					want)
			}
		}
	}
}

// TestApplyChange is the property of incremental sync: an LSP edit
// whose range was computed from byte offsets in any encoding gives the
// same text as splicing the bytes directly (the oracle), including
// line ends "\r\n" and "\r" and characters outside the BMP.
func TestApplyChange(t *testing.T) {
	pieces := []string{"a", "é", "😀", "\n", "\r\n", "\r", " ", "Z",
		"中"}
	for seed := range uint64(2000) {
		r := &rng{}
		r.State = seed
		var b bytes.Buffer
		for range r.n(30) {
			b.WriteString(pieces[r.n(len(pieces))])
		}
		text := b.Bytes()
		x := span.NewIndex(text)
		// rune-start offsets that are not inside a "\r\n" pair
		var cuts []int
		for i := 0; i <= len(text); i++ {
			if i < len(text) && !utf8.RuneStart(text[i]) {
				continue
			}
			if i > 0 && i < len(text) && text[i-1] == '\r' &&
				text[i] == '\n' {
				continue
			}
			cuts = append(cuts, i)
		}
		a, c := cuts[r.n(len(cuts))], cuts[r.n(len(cuts))]
		if a > c {
			a, c = c, a
		}
		repl := pieces[r.n(len(pieces))]
		want := slices.Concat(text[:a], []byte(repl), text[c:])
		for _, enc := range []span.Encoding{span.UTF8, span.UTF16,
			span.UTF32} {
			rg := toRange(x, Span{a, c}, enc)
			got := applyChange(text, x, rg, enc, repl)
			if !bytes.Equal(got, want) {
				t.Fatalf("seed %d %s: %q edit %v %q = %q, want %q", seed,
					enc, text, rg, repl, got, want)
			}
		}
	}
}

// TestOffsetClamping: positions past a line end or past the last line
// clamp like LSP says, and negative ones go to the start.
func TestOffsetClamping(t *testing.T) {
	x := span.NewIndex([]byte("ab\ncd"))
	for _, c := range []struct {
		p    Position
		want int
	}{
		{Position{0, 99}, 2}, {Position{1, 1}, 4}, {Position{5, 0}, 5},
		{Position{-1, 3}, 0}, {Position{1, -4}, 3},
	} {
		if got := offset(x, c.p, span.UTF16); got != c.want {
			t.Errorf("offset(%v) = %d, want %d", c.p, got, c.want)
		}
	}
}

func TestURIs(t *testing.T) {
	for _, c := range []struct{ uri, path string }{
		{"file:///work/a.vhd", "/work/a.vhd"},
		{"file:///work/a%20b.vhd", "/work/a b.vhd"},
		{"file:///work/x/../a.vhd", "/work/a.vhd"},
	} {
		p, ok := URIToPath(c.uri)
		if !ok || p != filepath.Clean(filepath.FromSlash(c.path)) {
			t.Errorf("URIToPath(%q) = %q, %v", c.uri, p, ok)
		}
		if back, _ := URIToPath(PathToURI(p)); back != p {
			t.Errorf("round trip of %q gives %q", p, back)
		}
	}
	for _, bad := range []string{"untitled:1", "http://x/y", "file:",
		"%"} {
		if _, ok := URIToPath(bad); ok {
			t.Errorf("URIToPath(%q) accepted", bad)
		}
	}
	if docKey("file:///work/a.vhd") != docKey("file:///work/./a.vhd") {
		t.Error("two spellings of one file are two documents")
	}
}

// TestNegotiate: utf-8 when offered, else utf-16.
func TestNegotiate(t *testing.T) {
	for _, c := range []struct {
		offered []string
		want    span.Encoding
	}{
		{nil, span.UTF16}, {[]string{"utf-16"}, span.UTF16},
		{[]string{"utf-32", "utf-16", "utf-8"}, span.UTF8},
		{[]string{"utf-32"}, span.UTF16},
	} {
		if got := Negotiate(c.offered); got != c.want {
			t.Errorf("Negotiate(%v) = %s", c.offered, got)
		}
	}
}

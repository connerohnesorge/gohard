package server

import (
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/span"
)

// decoded is one decoded token: a byte span and its type and
// modifiers.
type decoded struct {
	start, end int
	typ        int
	mods       uint32
}

// decodeTokens turns token data in encoding enc back into byte spans
// of the text with line index x.
func decodeTokens(t *testing.T, x *span.Index, data []uint32,
	enc span.Encoding,
) []decoded {
	t.Helper()
	if len(data)%5 != 0 {
		t.Fatalf("data length %d is not a multiple of 5", len(data))
	}
	var out []decoded
	line, col := 0, 0
	for i := 0; i < len(data); i += 5 {
		if data[i] > 0 {
			line, col = line+int(data[i]), int(data[i+1])
		} else {
			col += int(data[i+1])
		}
		a, ok1 := x.Offset(span.Position{Line: line, Col: col}, enc)
		b, ok2 := x.Offset(span.Position{Line: line,
			Col: col + int(data[i+2])}, enc)
		if !ok1 || !ok2 {
			t.Fatalf("token %d at %d:%d+%d is outside the text", i/5,
				line, col, data[i+2])
		}
		out = append(out, decoded{a, b, int(data[i+3]), data[i+4]})
	}
	return out
}

// TestEncodeTokens: tokens are split per line without terminators,
// positions and lengths count the encoding's units, an overlapping
// token is dropped, and decoding gives the same byte spans under every
// encoding.
func TestEncodeTokens(t *testing.T) {
	text := []byte("a /* é😀\r\nxy */ b\n\nc\n")
	x := span.NewIndex(text)
	ts := []SemToken{
		{Span{0, 1}, 1, 0},
		{Span{2, 18}, 2, 1}, // the block comment, two lines
		{Span{4, 6}, 3, 0},  // overlaps it: dropped
		{Span{19, 20}, 1, 2},
		{Span{20, 23}, 4, 0}, // "\n\nc": only c
	}
	want := []decoded{{0, 1, 1, 0}, {2, 11, 2, 1}, {13, 18, 2, 1},
		{19, 20, 1, 2}, {22, 23, 4, 0}}
	for _, enc := range []span.Encoding{span.UTF8, span.UTF16,
		span.UTF32} {
		data := EncodeTokens(x, ts, nil, enc)
		if got := decodeTokens(t, x, data, enc); !slices.Equal(got,
			want) {
			t.Errorf("%s: %v, want %v", enc, got, want)
		}
	}
	// The first line of the comment is "/* é😀": 3+2+4 bytes, 3+1+2
	// UTF-16 units, 3+1+1 runes.
	for _, c := range []struct {
		enc span.Encoding
		n   uint32
	}{{span.UTF8, 9}, {span.UTF16, 6}, {span.UTF32, 5}} {
		if got := EncodeTokens(x, ts, nil, c.enc)[7]; got != c.n {
			t.Errorf("%s: comment length %d, want %d", c.enc, got, c.n)
		}
	}
	// A range keeps the pieces that overlap it, whole.
	r := Span{13, 20}
	got := decodeTokens(t, x, EncodeTokens(x, ts, &r, span.UTF16),
		span.UTF16)
	if w := want[2:4]; !slices.Equal(got, w) {
		t.Errorf("range: %v, want %v", got, w)
	}
	if n := len(EncodeTokens(x, nil, nil, span.UTF8)); n != 0 {
		t.Errorf("no tokens: %d values", n)
	}
}

// TestDiffTokens: applying the edits to the old data gives the new.
func TestDiffTokens(t *testing.T) {
	rnd := &env.SplitMix64{State: 55}
	for i := range 2000 {
		gen := func() []uint32 {
			n := int(rnd.Uint64() % 12)
			out := make([]uint32, n)
			for j := range out {
				out[j] = uint32(rnd.Uint64() % 3)
			}
			return out
		}
		old, new := gen(), gen()
		es := diffTokens(old, new)
		if slices.Equal(old, new) != (len(es) == 0) {
			t.Fatalf("%d: %v -> %v: %d edits", i, old, new, len(es))
		}
		got := slices.Clone(old)
		for _, e := range es {
			got = slices.Concat(got[:e.Start], e.Data,
				got[e.Start+e.DeleteCount:])
		}
		if !slices.Equal(got, new) {
			t.Fatalf("%d: %v -> %v: edits %+v give %v", i, old, new,
				es, got)
		}
	}
}

package fst

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/trace"
	"github.com/connerohnesorge/gohard/v2/internal/trace/tracetest"
)

var debugVC = false

var update = flag.Bool("update", false, "rewrite golden files")

// The rest of this file is a small FST reader written from the reader
// side of fstapi.c (fstReaderInit, fstReaderIterBlocks2,
// fstReaderIterateHier), used to check what the writer produced.

type file struct {
	start, end      uint64
	scopes, nvars   uint64
	maxHandle       uint64
	sections        uint64
	timescale       int8
	version, date   string
	fileType        byte
	lens            []uint32 // from the geometry block; 0 means varying
	isReal          []bool
	hier            []hierEntry
	blocks          int
	vc              []vchange
	frameAfterFirst [][]byte // frames of the blocks after the first
}

type hierEntry struct {
	kind string // "scope", "upscope", "attr", "var"
	typ  byte
	sub  byte
	name string
	comp string
	arg  uint64
	len  uint64
}

type vchange struct {
	time   uint64
	handle int
	val    string
}

type rd struct {
	b   []byte
	err error
}

func (r *rd) take(n int) []byte {
	if r.err != nil || n < 0 || n > len(r.b) {
		r.err = errors.New("short read")
		return make([]byte, max(n, 0))
	}
	out := r.b[:n]
	r.b = r.b[n:]
	return out
}

func (r *rd) u8() byte    { return r.take(1)[0] }
func (r *rd) u64() uint64 { return binary.BigEndian.Uint64(r.take(8)) }
func (r *rd) cstr() string {
	i := bytes.IndexByte(r.b, 0)
	if i < 0 {
		r.err = errors.New("unterminated string")
		return ""
	}
	s := string(r.take(i))
	r.take(1)
	return s
}

func (r *rd) varint() uint64 {
	var v uint64
	for shift := 0; ; shift += 7 {
		c := r.u8()
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 || r.err != nil {
			return v
		}
	}
}

func (r *rd) svarint() int64 {
	var v int64
	shift := 0
	var c byte
	for {
		c = r.u8()
		v |= int64(c&0x7f) << shift
		shift += 7
		if c&0x80 == 0 || r.err != nil {
			break
		}
	}
	if shift < 64 && c&0x40 != 0 {
		v |= -1 << shift
	}
	return v
}

func parse(data []byte) (*file, error) {
	f := &file{}
	r := &rd{b: data}
	var vcBlocks [][]byte
	for len(r.b) > 0 && r.err == nil {
		tag := r.u8()
		seclen := r.u64()
		body := r.take(int(seclen) - 8)
		br := &rd{b: body}
		switch tag {
		case blHdr:
			f.start, f.end = br.u64(), br.u64()
			endian := br.take(8)
			if binary.LittleEndian.Uint64(endian) !=
				0x4005bf0a8b145769 {
				return nil, fmt.Errorf("endian test %x", endian)
			}
			br.u64() // memory used
			f.scopes, f.nvars = br.u64(), br.u64()
			f.maxHandle, f.sections = br.u64(), br.u64()
			f.timescale = int8(br.u8())
			f.version, _, _ = strings.Cut(string(br.take(128)), "\x00")
			f.date, _, _ = strings.Cut(string(br.take(119)), "\x00")
			f.fileType = br.u8()
			br.u64()
		case blVCDynAlias:
			vcBlocks = append(vcBlocks, body)
		case blGeom:
			uclen := br.u64()
			if uint64(len(body))-16 != uclen {
				return nil, errors.New("compressed geometry")
			}
			n := br.u64()
			for range n {
				v := br.varint()
				switch v {
				case 0:
					f.lens = append(f.lens, 8)
					f.isReal = append(f.isReal, true)
				case 0xffffffff:
					f.lens = append(f.lens, 0)
					f.isReal = append(f.isReal, false)
				default:
					f.lens = append(f.lens, uint32(v))
					f.isReal = append(f.isReal, false)
				}
			}
		case blHier:
			uclen := br.u64()
			z, err := gzip.NewReader(bytes.NewReader(br.b))
			if err != nil {
				return nil, err
			}
			h, err := io.ReadAll(z)
			if err != nil {
				return nil, err
			}
			if uint64(len(h)) != uclen {
				return nil, errors.New("hierarchy length")
			}
			if err := f.parseHier(h); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unexpected block %d", tag)
		}
		if br.err != nil {
			return nil, fmt.Errorf("block %d: %w", tag, br.err)
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	for i, b := range vcBlocks {
		if err := f.parseVC(b, i); err != nil {
			return nil, fmt.Errorf("block %d: %w", i, err)
		}
	}
	f.blocks = len(vcBlocks)
	return f, nil
}

func (f *file) parseHier(h []byte) error {
	r := &rd{b: h}
	for len(r.b) > 0 && r.err == nil {
		switch tag := r.u8(); tag {
		case stVCDScope:
			typ := r.u8()
			f.hier = append(f.hier, hierEntry{kind: "scope", typ: typ,
				name: r.cstr(), comp: r.cstr()})
		case stVCDUpscope:
			f.hier = append(f.hier, hierEntry{kind: "upscope"})
		case stAttrBegin:
			typ, sub := r.u8(), r.u8()
			name := r.cstr()
			f.hier = append(f.hier, hierEntry{kind: "attr", typ: typ,
				sub: sub, name: name, arg: r.varint()})
		default:
			dir := r.u8()
			name := r.cstr()
			l := r.varint()
			if alias := r.varint(); alias != 0 {
				return errors.New("unexpected alias")
			}
			f.hier = append(f.hier, hierEntry{kind: "var", typ: tag,
				sub: dir, name: name, len: l})
		}
	}
	return r.err
}

func (f *file) parseVC(b []byte, n int) error {
	r := &rd{b: b}
	beg := r.u64()
	r.u64() // end
	mem := r.u64()
	frameLen := r.varint()
	if r.varint() != frameLen {
		return errors.New("compressed frame")
	}
	if r.varint() != f.maxHandle {
		return errors.New("frame max handle")
	}
	frame := r.take(int(frameLen))
	if n > 0 {
		f.frameAfterFirst = append(f.frameAfterFirst, frame)
	}
	if r.varint() != f.maxHandle {
		return errors.New("vc max handle")
	}
	vcStart := len(b) - len(r.b)
	if r.u8() != 'Z' {
		return errors.New("pack type")
	}
	// Trailer: time table and index, from the end.
	tail := b[len(b)-24:]
	tlen := binary.BigEndian.Uint64(tail[0:])
	tclen := binary.BigEndian.Uint64(tail[8:])
	items := binary.BigEndian.Uint64(tail[16:])
	if tlen != tclen {
		return errors.New("compressed time table")
	}
	tt := &rd{b: b[len(b)-24-int(tlen) : len(b)-24]}
	var times []uint64
	var at uint64
	for range items {
		at += tt.varint()
		times = append(times, at)
	}
	if tt.err != nil || len(tt.b) != 0 || times[0] != beg {
		return errors.New("time table")
	}
	ip := len(b) - 24 - int(tlen) - 8
	ilen := int(binary.BigEndian.Uint64(b[ip:]))
	idx := &rd{b: b[ip-ilen : ip]}
	pos := make([]int, 0, f.maxHandle)
	prev := 0
	for len(idx.b) > 0 && idx.err == nil {
		if idx.b[0]&1 != 0 {
			v := idx.svarint() >> 1
			if v <= 0 {
				return errors.New("alias in index")
			}
			prev += int(v)
			pos = append(pos, prev)
		} else {
			for range idx.varint() >> 1 {
				pos = append(pos, 0)
			}
		}
	}
	if len(pos) != int(f.maxHandle) {
		return fmt.Errorf("index has %d entries", len(pos))
	}
	if debugVC {
		fmt.Println("block", n, "times", times, "pos", pos)
	}
	chainsEnd := ip - ilen - vcStart
	total := 0
	for h, p := range pos {
		if p == 0 {
			continue
		}
		end := chainsEnd
		for _, q := range pos[h+1:] {
			if q != 0 {
				end = q
				break
			}
		}
		cr := &rd{b: b[vcStart+p : vcStart+end]}
		if cr.varint() != 0 {
			return errors.New("compressed chain")
		}
		total += len(cr.b)
		if err := f.parseChain(cr, h, times); err != nil {
			return fmt.Errorf("handle %d: %w", h+1, err)
		}
	}
	if uint64(total) != mem {
		return fmt.Errorf("traversal memory %d, chains %d", mem, total)
	}
	return nil
}

func (f *file) parseChain(r *rd, h int, times []uint64) error {
	ti := 0
	n := int(f.lens[h])
	for len(r.b) > 0 && r.err == nil {
		v := r.varint()
		var val string
		switch {
		case n == 0:
			ti += int(v >> 1)
			val = string(r.take(int(r.varint())))
		case n == 1 && !f.isReal[h]:
			if v&1 == 0 {
				val = string(rune('0' + v>>1&1))
				ti += int(v >> 2)
			} else {
				val = string("xzhuwl-?"[v>>1&7])
				ti += int(v >> 4)
			}
		default:
			ti += int(v >> 1)
			if v&1 == 1 {
				val = string(r.take(n))
				break
			}
			packed := r.take((n + 7) / 8)
			s := make([]byte, n)
			for k := range n {
				s[k] = '0' + packed[k/8]>>(7-k%8)&1
			}
			val = string(s)
		}
		if ti >= len(times) {
			return errors.New("time index out of range")
		}
		f.vc = append(f.vc, vchange{time: times[ti], handle: h + 1,
			val: val})
	}
	return r.err
}

// expected returns the value changes the writer must produce for t:
// every settled value of every variable that differs from the one
// before, one-bit values in the reader's lower-case form.
func expected(t *trace.Trace) []vchange {
	_, enums := enumTables(t)
	vs := vars(t, enums)
	last := make([]string, len(vs))
	set := make([]bool, len(vs))
	var out []vchange
	for _, st := range trace.Settle(t) {
		for _, c := range st.Changes {
			for i, v := range vs {
				if v.sig != c.Signal {
					continue
				}
				x := c.Value
				if v.elem >= 0 {
					x = x.Elems[v.elem]
				}
				val := string(v.enc(x))
				if set[i] && val == last[i] {
					continue
				}
				last[i], set[i] = val, true
				if v.length == 1 {
					val = strings.ToLower(val)
				}
				out = append(out, vchange{time: uint64(st.Time),
					handle: i + 1, val: val})
			}
		}
	}
	return out
}

func sortVC(vc []vchange) {
	slices.SortStableFunc(vc, func(a, b vchange) int {
		if a.time != b.time {
			if a.time < b.time {
				return -1
			}
			return 1
		}
		return a.handle - b.handle
	})
}

func check(t *testing.T, tr *trace.Trace, blockChanges int) *file {
	t.Helper()
	data, err := Exporter{BlockChanges: blockChanges}.Export(tr)
	if err != nil {
		t.Fatal(err)
	}
	f, err := parse(data)
	if err != nil {
		t.Fatal(err)
	}
	got := slices.Clone(f.vc)
	sortVC(got)
	// Drop the restatements of handle 1 at the start of later blocks:
	// the same value at the time it already had it.
	cur := map[int]string{}
	kept := got[:0]
	for _, c := range got {
		if v, ok := cur[c.handle]; ok && v == c.val && c.handle == 1 {
			continue
		}
		cur[c.handle] = c.val
		kept = append(kept, c)
	}
	got = kept
	want := expected(tr)
	sortVC(want)
	if !slices.Equal(got, want) {
		t.Fatalf("value changes differ:\n got %v\nwant %v", got, want)
	}
	if f.version != Version || f.date != Date || f.timescale != -15 ||
		f.fileType != fileTypeVHDL {
		t.Fatalf("header %q %q %d %d", f.version, f.date, f.timescale,
			f.fileType)
	}
	if f.sections != uint64(f.blocks) || f.nvars != f.maxHandle ||
		uint64(len(f.lens)) != f.maxHandle && f.maxHandle > 0 {
		t.Fatalf("counts: %d sections, %d blocks, %d vars, %d handles",
			f.sections, f.blocks, f.nvars, f.maxHandle)
	}
	nv, ns := 0, 0
	for _, h := range f.hier {
		switch h.kind {
		case "var":
			nv++
			if f.lens[nv-1] != uint32(h.len) && !f.isReal[nv-1] {
				t.Fatalf("var %s: hierarchy length %d, geometry %d",
					h.name, h.len, f.lens[nv-1])
			}
		case "scope":
			ns++
		}
	}
	if uint64(nv) != f.maxHandle || uint64(ns) != f.scopes {
		t.Fatalf("hierarchy has %d vars and %d scopes", nv, ns)
	}
	return f
}

func TestSampleDecodes(t *testing.T) {
	tr := tracetest.Sample()
	f := check(t, tr, 0)
	if f.blocks != 1 || f.start != 0 || f.end != uint64(tr.Header.End) {
		t.Errorf("blocks %d, start %d, end %d", f.blocks, f.start, f.end)
	}
	var names []string
	for _, h := range f.hier {
		if h.kind == "var" {
			names = append(names, h.name)
		}
	}
	want := []string{"clk", "addr[3:0]", "valid", "clk", "count_q[7:0]",
		"delay", "flag", "hist[0]", "hist[1]", "hist[2]", "n", "ratio",
		"state", "mode[1]", "mode[2]", "dout[3:0]", "rst", "x"}
	if !slices.Equal(names, want) {
		t.Errorf("vars %q", names)
	}
}

// TestSmallBlocks: with a tiny block break policy the same values come
// back from many blocks, and every later block's frame holds the values
// at its start.
func TestSmallBlocks(t *testing.T) {
	tr := tracetest.Sample()
	for _, n := range []int{1, 2, 3, 7, 50} {
		f := check(t, tr, n)
		if n < 10 && f.blocks < 3 {
			t.Errorf("policy %d: only %d blocks", n, f.blocks)
		}
		if len(f.frameAfterFirst) != f.blocks-1 {
			t.Errorf("policy %d: frames", n)
		}
	}
}

func TestRandomTracesDecode(t *testing.T) {
	for seed := range uint64(150) {
		r := trace.NewRecorder(nil)
		if err := tracetest.Random(seed, 8, 12).Feed(r); err != nil {
			t.Fatal(err)
		}
		tr, err := r.Trace()
		if err != nil {
			t.Fatal(err)
		}
		check(t, tr, 1+int(seed%5)*4)
	}
}

func TestHierarchyAttributes(t *testing.T) {
	f := check(t, tracetest.Sample(), 0)
	var attrs []string
	for _, h := range f.hier {
		switch h.kind {
		case "attr":
			attrs = append(attrs, fmt.Sprintf("attr %d %q %d", h.sub,
				h.name, h.arg))
		case "scope":
			attrs = append(attrs, fmt.Sprintf("scope %d %s %s", h.typ,
				h.name, h.comp))
		case "var":
			attrs = append(attrs, fmt.Sprintf("var %d %d %s %d", h.typ,
				h.sub, h.name, h.len))
		case "upscope":
			attrs = append(attrs, "upscope")
		}
	}
	golden := strings.Join(attrs, "\n") + "\n"
	path := "testdata/sample.hier"
	if *update {
		os.WriteFile(path, []byte(golden), 0o644)
		return
	}
	want, _ := os.ReadFile(path)
	if n, a, b, ok := trace.FirstDiff(want, []byte(golden)); ok {
		t.Fatalf("hierarchy line %d: want %q, got %q", n, a, b)
	}
}

// TestGolden pins the sample export byte for byte and by SHA-256: the
// date, version and block policy are fixed, so the bytes never change
// unless the writer does (#36).
func TestGolden(t *testing.T) {
	data, _ := Exporter{}.Export(tracetest.Sample())
	again, _ := Exporter{}.Export(tracetest.Sample())
	if !bytes.Equal(data, again) {
		t.Fatal("two exports differ")
	}
	sum := fmt.Sprintf("%x\n", sha256.Sum256(data))
	if *update {
		os.WriteFile("testdata/sample.fst", data, 0o644)
		os.WriteFile("testdata/sample.fst.sha256", []byte(sum), 0o644)
		return
	}
	want, err := os.ReadFile("testdata/sample.fst")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, data) {
		t.Fatal("export differs from testdata/sample.fst")
	}
	if h, _ := os.ReadFile("testdata/sample.fst.sha256"); string(h) != sum {
		t.Fatalf("sha256 %s, want %s", sum, h)
	}
}

func TestStable(t *testing.T) {
	for seed := range uint64(40) {
		r := trace.NewRecorder(nil)
		tracetest.Random(seed, 6, 8).Feed(r)
		tr, _ := r.Trace()
		back, err := trace.Decode(trace.Encode(tr))
		if err != nil {
			t.Fatal(err)
		}
		a, _ := Exporter{BlockChanges: 4}.Export(tr)
		b, _ := Exporter{BlockChanges: 4}.Export(back)
		if !bytes.Equal(a, b) {
			t.Fatalf("seed %d: export not stable", seed)
		}
	}
}

func TestEmpty(t *testing.T) {
	r := trace.NewRecorder(nil)
	r.Begin(trace.Header{Testbench: "tb"}, trace.Design{})
	r.End(5)
	tr, err := r.Trace()
	if err != nil {
		t.Fatal(err)
	}
	f := check(t, tr, 0)
	if f.blocks != 0 || f.maxHandle != 0 {
		t.Errorf("empty trace: %d blocks, %d vars", f.blocks, f.maxHandle)
	}
	// Signals without any change still get one block.
	r = trace.NewRecorder(nil)
	r.Begin(trace.Header{Testbench: "tb"}, trace.Design{
		Types:   []trace.Type{{Name: "std_logic", Kind: trace.Logic}},
		Signals: []trace.Signal{{Path: "tb.a", Type: "std_logic", Dir: 1}},
	})
	r.End(5)
	tr, _ = r.Trace()
	if f := check(t, tr, 0); f.blocks != 1 || f.end != 5 {
		t.Errorf("no changes: %d blocks, end %d", f.blocks, f.end)
	}
}

func TestEncodings(t *testing.T) {
	if got := svarint(nil, 63); !bytes.Equal(got, []byte{63}) {
		t.Errorf("svarint(63) = %x", got)
	}
	if got := svarint(nil, 64); !bytes.Equal(got, []byte{0xc0, 0}) {
		t.Errorf("svarint(64) = %x", got)
	}
	if got := varint(nil, 300); !bytes.Equal(got, []byte{0xac, 2}) {
		t.Errorf("varint(300) = %x", got)
	}
	if got := escape(`'a' "\?`); got != `\'a\'\040\"\\\?` {
		t.Errorf("escape = %s", got)
	}
	units := []trace.Unit{{Name: "fs", Mult: 1}, {Name: "ps", Mult: 1000},
		{Name: "ns", Mult: 1000000}}
	for n, want := range map[int64]string{0: "0 fs", 1500: "1500 fs",
		2000: "2 ps", 3000000: "3 ns", -7000000: "-7 ns"} {
		if got := physical(n, units); got != want {
			t.Errorf("physical(%d) = %q, want %q", n, got, want)
		}
	}
	z, err := gzip.NewReader(bytes.NewReader(
		gzipTrailer(gzipStored(bytes.Repeat([]byte("ab"), 70000)),
			bytes.Repeat([]byte("ab"), 70000))))
	if err != nil {
		t.Fatal(err)
	}
	if b, err := io.ReadAll(z); err != nil || len(b) != 140000 {
		t.Errorf("stored gzip: %d bytes, %v", len(b), err)
	}
}

// blockPins are the exports whose bytes pin the block break policy
// (#36): the sample cut every 3 changes, and a large seeded trace that
// needs more than one block under DefaultBlockChanges. The golden file
// holds each export's block count and SHA-256, so changing the policy
// constant or the break condition fails here.
var blockPins = []struct {
	name   string
	trace  func(t *testing.T) *trace.Trace
	policy int
}{
	{"sample-3", func(*testing.T) *trace.Trace {
		return tracetest.Sample()
	}, 3},
	{"random-default", largeTrace, 0},
}

// largeTrace is a seeded random trace with more value changes than
// DefaultBlockChanges.
func largeTrace(t *testing.T) *trace.Trace {
	t.Helper()
	r := trace.NewRecorder(nil)
	if err := tracetest.Random(11, 24, 30000).Feed(r); err != nil {
		t.Fatal(err)
	}
	tr, err := r.Trace()
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestBlockPolicyPinned(t *testing.T) {
	if DefaultBlockChanges != 1<<16 {
		t.Fatalf("DefaultBlockChanges = %d; the block break policy "+
			"is fixed at 65536 (#36)", DefaultBlockChanges)
	}
	path := "testdata/blocks.sha256"
	var got strings.Builder
	for _, p := range blockPins {
		tr := p.trace(t)
		f := check(t, tr, p.policy)
		data, err := Exporter{BlockChanges: p.policy}.Export(tr)
		if err != nil {
			t.Fatal(err)
		}
		if p.name == "random-default" {
			n := 0
			for _, s := range trace.Settle(tr) {
				n += len(s.Changes)
			}
			if n <= DefaultBlockChanges || f.blocks < 2 {
				t.Fatalf("%s: %d settled changes in %d blocks; "+
					"want more than one block", p.name, n, f.blocks)
			}
		}
		fmt.Fprintf(&got, "%s %d %x\n", p.name, f.blocks,
			sha256.Sum256(data))
	}
	if *update {
		os.WriteFile(path, []byte(got.String()), 0o644)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if n, a, b, ok := trace.FirstDiff(want, []byte(got.String())); ok {
		t.Fatalf("%s line %d: want %q, got %q", path, n, a, b)
	}
}

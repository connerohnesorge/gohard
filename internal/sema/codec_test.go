package sema

import (
	"bytes"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/connerohnesorge/gohard/internal/eval"
	"github.com/connerohnesorge/gohard/internal/stdlib"
)

// TestStartupNoAnalysis: the bundle a run starts with is the one
// analyzed at build time and embedded (#33 Done when): Bundle decodes
// it and analyzes nothing. A stale prebuilt.bin fails here (rerun go
// generate ./internal/sema).
func TestStartupNoAnalysis(t *testing.T) {
	if len(stdlib.Prebuilt()) == 0 {
		t.Fatal("internal/stdlib/prebuilt.bin is empty: " +
			"run go generate ./internal/sema")
	}
	if _, err := unpackBundle(stdlib.Prebuilt()); err != nil {
		t.Fatalf("embedded bundle: %v: run go generate ./internal/sema",
			err)
	}
	if got := BundleSource(); got != "prebuilt" {
		t.Fatalf("Bundle came from %q, want prebuilt (no analysis at "+
			"startup)", got)
	}
}

// TestPrebuiltUpToDate: the embedded bytes are exactly what analyzing
// the bundled text and encoding it gives now, so the stored bundle is
// the fresh analysis (the gen-check gate fails the same staleness).
func TestPrebuiltUpToDate(t *testing.T) {
	if !bytes.Equal(PrebuildBundle(), stdlib.Prebuilt()) {
		t.Fatal("internal/stdlib/prebuilt.bin is stale: run go generate " +
			"./internal/sema")
	}
}

// TestCodecComplete: decoding the stored bundle gives a graph
// isomorphic to a fresh analysis: every unit, declaration, region,
// type, expression, green and red node, every field value, nil-ness,
// slice and map length, and every pointer identity (two fields share
// an object after decoding exactly when they share one in the fresh
// analysis). The only objects the decoded graph shares with the fresh
// one are the seeded globals, and they are shared by both.
func TestCodecComplete(t *testing.T) {
	fresh := analyzeBundle()
	dec, err := decodeBundle(encodeBundle(fresh))
	if err != nil {
		t.Fatal(err)
	}
	g := globals()
	c := newIso(func(p ptrKey, same bool) error {
		if same != g[p] {
			return fmt.Errorf("shared=%v but global=%v", same, g[p])
		}
		return nil
	})
	if err := c.run(reflect.ValueOf(fresh), reflect.ValueOf(dec)); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d objects compared", len(c.fwd))
}

// TestCodecGlobals: the only objects two independent analyses of the
// bundle share are the package-level ones the codec seeds, so no
// shared object is copied by decoding.
func TestCodecGlobals(t *testing.T) {
	a, b := analyzeBundle(), analyzeBundle()
	g := globals()
	var missing []string
	c := newIso(func(p ptrKey, same bool) error {
		if same && !g[p] {
			missing = append(missing, p.t.String())
		}
		return nil
	})
	if err := c.run(reflect.ValueOf(a), reflect.ValueOf(b)); err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Fatalf("shared objects the codec does not seed: %v", missing)
	}
}

// TestCodecDeterministic: encoding is a pure function of the analysis
// (map order, addresses and the job runner do not reach the bytes),
// and re-encoding a decoded bundle gives the same bytes.
func TestCodecDeterministic(t *testing.T) {
	a := encodeBundle(analyzeBundle())
	if b := encodeBundle(analyzeBundle()); !bytes.Equal(a, b) {
		t.Fatal("two analyses encode differently")
	}
	r, err := decodeBundle(a)
	if err != nil {
		t.Fatal(err)
	}
	if b := encodeBundle(r); !bytes.Equal(a, b) {
		t.Fatal("decode then encode changes the bytes")
	}
}

// TestCodecRejects: a blob of another schema, other sources, or one
// truncated or damaged anywhere is rejected (or decodes to a graph that
// re-encodes to the same bytes), and decoding never panics.
func TestCodecRejects(t *testing.T) {
	b := encodeBundle(Bundle())
	if _, err := decodeBundle(nil); err == nil {
		t.Error("empty blob accepted")
	}
	other := bytes.Replace(bytes.Clone(b), []byte(codecSchema),
		[]byte(codecSchema[:len(codecSchema)-1]+"x"), 1)
	if _, err := decodeBundle(other); err == nil {
		t.Error("other schema accepted")
	}
	other = bytes.Replace(bytes.Clone(b), []byte(sourcesHash()),
		[]byte(sourcesHash()[:63]+"x"), 1)
	if _, err := decodeBundle(other); err == nil {
		t.Error("other sources accepted")
	}
	for _, n := range []int{len(codecMagic) + 1, 200, len(b) / 3,
		len(b) - 1} {
		if _, err := decodeBundle(b[:n]); err == nil {
			t.Errorf("truncated at %d accepted", n)
		}
	}
	// Damage one byte at a spread of offsets past the header.
	for i := range 16 {
		off := 200 + i*(len(b)-201)/16
		bad := bytes.Clone(b)
		bad[off] ^= 0x5a
		r, err := decodeBundle(bad)
		if err == nil && !bytes.Equal(encodeBundle(r), bad) {
			t.Errorf("damage at %d accepted as a different bundle", off)
		}
	}
}

// BenchmarkBundleDecode is the startup cost of the bundle; compare
// BenchmarkBundleAnalyze.
func BenchmarkBundleUnpack(b *testing.B) {
	for b.Loop() {
		if _, err := unpackBundle(stdlib.Prebuilt()); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBundleAnalyze(b *testing.B) {
	for b.Loop() {
		analyzeBundle()
	}
}

// ptrKey identifies an object: its type and address (a struct and its
// first field share an address).
type ptrKey struct {
	t reflect.Type
	p uintptr
}

// globals returns the objects seedTables seeds.
func globals() map[ptrKey]bool {
	g := map[ptrKey]bool{}
	seedTables(func(ts []*Type, es []*eval.Type) {
		for _, p := range ts {
			g[ptrKey{reflect.TypeOf(p), reflect.ValueOf(p).Pointer()}] = true
		}
		for _, p := range es {
			g[ptrKey{reflect.TypeOf(p), reflect.ValueOf(p).Pointer()}] = true
		}
	})
	return g
}

// iso checks that two graphs are isomorphic, reading every field
// (exported or not) through reflection. Pointers are paired by a
// bijection; maps are compared once their pointer keys are paired.
type iso struct {
	fwd, bwd map[ptrKey]uintptr
	work     []pair
	maps     []pair
	shared   func(p ptrKey, same bool) error
}

type pair struct {
	a, b reflect.Value
	path string
}

func newIso(shared func(ptrKey, bool) error) *iso {
	return &iso{fwd: map[ptrKey]uintptr{}, bwd: map[ptrKey]uintptr{},
		shared: shared}
}

func (c *iso) run(a, b reflect.Value) error {
	c.work = append(c.work, pair{a, b, "Result"})
	for len(c.work) > 0 || len(c.maps) > 0 {
		for len(c.work) > 0 {
			p := c.work[len(c.work)-1]
			c.work = c.work[:len(c.work)-1]
			if err := c.cmp(p); err != nil {
				return err
			}
		}
		if len(c.maps) > 0 {
			p := c.maps[0]
			c.maps = c.maps[1:]
			if err := c.cmpMap(p); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *iso) cmp(p pair) error {
	a, b := p.a, p.b
	bad := func(f string, args ...any) error {
		return fmt.Errorf("%s: %s", p.path, fmt.Sprintf(f, args...))
	}
	if a.Type() != b.Type() {
		return bad("type %s vs %s", a.Type(), b.Type())
	}
	switch a.Kind() {
	case reflect.Bool:
		if a.Bool() != b.Bool() {
			return bad("%v vs %v", a.Bool(), b.Bool())
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
		reflect.Int64:
		if a.Int() != b.Int() {
			return bad("%d vs %d", a.Int(), b.Int())
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32,
		reflect.Uint64:
		if a.Uint() != b.Uint() {
			return bad("%d vs %d", a.Uint(), b.Uint())
		}
	case reflect.Float64:
		if math.Float64bits(a.Float()) != math.Float64bits(b.Float()) {
			return bad("%v vs %v", a.Float(), b.Float())
		}
	case reflect.String:
		if a.String() != b.String() {
			return bad("%q vs %q", a.String(), b.String())
		}
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			if a.IsNil() != b.IsNil() {
				return bad("nil %v vs %v", a.IsNil(), b.IsNil())
			}
			return nil
		}
		ka := ptrKey{a.Type(), a.Pointer()}
		kb := ptrKey{b.Type(), b.Pointer()}
		if q, ok := c.fwd[ka]; ok {
			if q != kb.p {
				return bad("pointer identity differs")
			}
			return nil
		}
		if _, ok := c.bwd[kb]; ok {
			return bad("pointer identity differs (reverse)")
		}
		c.fwd[ka], c.bwd[kb] = kb.p, ka.p
		if err := c.shared(ka, ka == kb); err != nil {
			return bad("%s: %v", a.Type(), err)
		}
		c.work = append(c.work, pair{a.Elem(), b.Elem(), p.path + ".*"})
	case reflect.Struct:
		for i := range a.NumField() {
			c.work = append(c.work, pair{a.Field(i), b.Field(i),
				p.path + "." + a.Type().Field(i).Name})
		}
	case reflect.Slice:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return bad("slice nil %v/%v len %d/%d", a.IsNil(), b.IsNil(),
				a.Len(), b.Len())
		}
		for i := range a.Len() {
			c.work = append(c.work, pair{a.Index(i), b.Index(i),
				p.path + "[" + strconv.Itoa(i) + "]"})
		}
	case reflect.Map:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return bad("map nil %v/%v len %d/%d", a.IsNil(), b.IsNil(),
				a.Len(), b.Len())
		}
		c.maps = append(c.maps, p)
	default:
		return bad("unsupported kind %s", a.Kind())
	}
	return nil
}

// cmpMap pairs the entries of two maps by key: equal values for basic
// and struct keys, paired objects for pointer keys.
func (c *iso) cmpMap(p pair) error {
	key := func(k reflect.Value, side map[ptrKey]uintptr) (string, bool) {
		switch k.Kind() {
		case reflect.Pointer:
			if side == nil {
				return strconv.FormatUint(uint64(k.Pointer()), 16), true
			}
			q, ok := side[ptrKey{k.Type(), k.Pointer()}]
			return strconv.FormatUint(uint64(q), 16), ok
		case reflect.String:
			return k.String(), true
		case reflect.Struct:
			s := ""
			for i := range k.NumField() {
				s += strconv.FormatInt(k.Field(i).Int(), 10) + ","
			}
			return s, true
		}
		return strconv.FormatInt(k.Int(), 10), true
	}
	bs := map[string]pair{}
	for it := p.b.MapRange(); it.Next(); {
		k, _ := key(it.Key(), nil)
		bs[k] = pair{it.Key(), it.Value(), ""}
	}
	for it := p.a.MapRange(); it.Next(); {
		k, ok := key(it.Key(), c.fwd)
		if !ok {
			return fmt.Errorf("%s: map key not reached elsewhere", p.path)
		}
		e, ok := bs[k]
		if !ok {
			return fmt.Errorf("%s: map key %s missing", p.path, k)
		}
		c.work = append(c.work, pair{it.Key(), e.a, p.path + "{key}"},
			pair{it.Value(), e.b, p.path + "[" + k + "]"})
	}
	return nil
}

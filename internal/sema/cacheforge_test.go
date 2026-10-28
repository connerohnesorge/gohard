package sema

import (
	"bytes"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
)

// mapCache is a Cache with no checks of its own: whatever bytes a test
// stores in it are handed to Analyze as they are, as if they had
// passed the store's checksum.
type mapCache struct {
	mu      sync.Mutex
	e       map[string][]byte
	discard int
}

func (m *mapCache) Get(kind string, key [32]byte) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.e[kind+string(key[:])]
	return b, ok
}

func (m *mapCache) Put(kind, _ string, key [32]byte, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.e[kind+string(key[:])] = data
}

func (m *mapCache) Discard() {
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.e)
	m.discard++
}

// forge returns damaged copies of entry b that a checksum written
// over them would accept: single bytes changed, cut short, a stretch
// moved. seed picks the positions.
func forge(b []byte, seed uint64) [][]byte {
	if len(b) == 0 {
		return nil
	}
	x := seed*0x9e3779b97f4a7c15 + 1
	next := func() int {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		return int(x % uint64(len(b)))
	}
	var out [][]byte
	for range 4 {
		c := bytes.Clone(b)
		i := next()
		c[i] ^= byte(1 << (x % 8))
		out = append(out, c)
		c = bytes.Clone(b)
		c[i] = 0xff
		out = append(out, c)
	}
	out = append(out, b[:next()])
	i, j := next(), next()
	if i > j {
		i, j = j, i
	}
	sp := append(bytes.Clone(b[:i]), b[j:]...)
	sp = append(sp, b[i:j]...)
	out = append(out, sp)
	return out
}

// TestCacheForgedSyntax (#37): a syntax entry that passed the store's
// checksum but does not spell its file (a writer bug, another tool
// rewriting the file, a checksum collision) is thrown away, never
// trusted: the run gives exactly the uncached result and never
// crashes. A damaged semantic entry never crashes the run either.
func TestCacheForgedSyntax(t *testing.T) {
	src := map[string]string{
		"p.vhd": "package p is\n  -- the width\n  constant w : " +
			"natural := 8;\n  function f(a : integer) return integer;" +
			"\nend;\npackage body p is\n  function f(a : integer) " +
			"return integer is\n  begin\n    return a + w;\n  end;\nend;\n",
		"e.vhd": "use work.p.all;\nentity e is\n" +
			"  port (x : in bit_vector(w - 1 downto 0));\nend;\n" +
			"architecture a of e is\n  signal s : integer := f(3);\n" +
			"begin\n  s <= nosuch + 1;\nend;\n",
	}
	in := func(c Cache) Input {
		var srcs []Source
		for _, p := range slices.Sorted(maps.Keys(src)) {
			srcs = append(srcs, Source{Path: p, Text: []byte(src[p])})
		}
		return Input{Files: srcs, Config: config.Defaults(), Cache: c}
	}
	want := graphBytes(t, Analyze(in(nil)))
	cold := &mapCache{e: map[string][]byte{}}
	Analyze(in(cold))
	keys := slices.Sorted(maps.Keys(cold.e))
	tried := 0
	for ki, k := range keys {
		syntax := strings.HasPrefix(k, CacheSyntax)
		for fi, b := range forge(cold.e[k], uint64(ki)) {
			c := &mapCache{e: maps.Clone(cold.e)}
			c.e[k] = b
			var got []byte
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("entry %d forgery %d: panic %v", ki, fi,
							r)
					}
				}()
				got = graphBytes(t, Analyze(in(c)))
			}()
			if syntax && !bytes.Equal(got, want) {
				t.Errorf("syntax entry %d forgery %d: result differs "+
					"from the uncached run", ki, fi)
			}
			tried++
		}
	}
	if tried == 0 {
		t.Fatal("no entries")
	}
}

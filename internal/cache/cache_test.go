package cache

import (
	"crypto/sha256"
	"fmt"
	"path"
	"slices"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/realenv"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
)

const dir = "/p/.gohard/cache"

func testKey() Key {
	return NewKey("1.0.0\x00abc\x00", "schema", config.Defaults())
}

func key(s string) [32]byte { return sha256.Sum256([]byte(s)) }

func newSim(o simenv.Options) *simenv.Sim { return simenv.New(o) }

// files returns every file under the cache folder.
func files(t *testing.T, fs *simenv.FS) []string {
	t.Helper()
	var out []string
	var walk func(d string)
	walk = func(d string) {
		es, err := fs.ReadDir(d)
		if err != nil {
			return
		}
		for _, e := range es {
			p := path.Join(d, e.Name())
			if e.IsDir() {
				walk(p)
			} else {
				out = append(out, p)
			}
		}
	}
	walk(dir)
	return out
}

func TestRoundTrip(t *testing.T) {
	sim := newSim(simenv.Options{})
	s := Open(sim.FS, dir, testKey())
	if !s.Enabled() {
		t.Fatal("store disabled")
	}
	if _, ok := s.Get("sema", key("a")); ok {
		t.Fatal("hit in an empty cache")
	}
	s.Put("sema", "u", key("a"), []byte("payload a"))
	s.Put("syntax", "f", key("a"), []byte("syntax a"))
	s.Put("sema", "v", key("b"), nil)
	again := Open(sim.FS, dir, testKey())
	for _, c := range []struct {
		kind, k, want string
	}{{"sema", "a", "payload a"}, {"syntax", "a", "syntax a"},
		{"sema", "b", ""}} {
		got, ok := again.Get(c.kind, key(c.k))
		if !ok || string(got) != c.want {
			t.Errorf("Get(%s, %s) = %q, %v; want %q", c.kind, c.k, got,
				ok, c.want)
		}
	}
	if _, ok := again.Get("other", key("a")); ok {
		t.Error("an unknown kind hit")
	}
	if st := again.Stats(); st.Hits != 3 || st.Wipes != 0 {
		t.Errorf("stats %+v", st)
	}
}

// TestKeyComponents (#37): each component of the whole-cache key
// invalidates the cache: opened with a key that differs in it, the
// cache is empty and every old entry file is gone.
func TestKeyComponents(t *testing.T) {
	base := testKey()
	psl := config.Defaults()
	psl.PSL.Comments = !psl.PSL.Comments
	std := config.Defaults()
	std.Standard = "1993"
	cfg := config.Defaults()
	cfg.Defines = append(cfg.Defines, config.Define{Name: "X",
		Value: "1"})
	def := config.Defaults()
	const v = "1.0.0\x00abc\x00"
	changed := map[string]Key{
		"version":  NewKey("1.0.1\x00abc\x00", "schema", def),
		"build":    NewKey("1.0.0\x00abc-dirty\x00", "schema", def),
		"binary":   NewKey("1.0.0\x00abc\x0012-34", "schema", def),
		"schema":   NewKey(v, "schema2", def),
		"standard": NewKey(v, "schema", std),
		"config":   NewKey(v, "schema", cfg),
		"psl":      NewKey(v, "schema", psl),
	}
	// Only where a key is written (diagnostics point there).
	moved, _ := config.Parse("gohard.yaml", []byte("\nlibraries: {}\n"))
	at, _ := config.Parse("gohard.yaml", []byte("libraries: {}\n"))
	changed["config-position"] = NewKey(v, "schema", moved)
	if NewKey(v, "schema", at) == changed["config-position"] {
		t.Error("moving a gohard.yaml key keeps the cache key")
	}
	kinds := base
	kinds.Kinds = "other kinds.yaml"
	changed["kinds"] = kinds
	if base.Standard == std.Standard || base.PSL == changed["psl"].PSL ||
		base.Config == changed["config"].Config {
		t.Fatal("the test keys do not differ where they should")
	}
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range changed {
			if !yield(k) {
				return
			}
		}
	}) {
		k := changed[name]
		if k.Sum() == base.Sum() {
			t.Fatalf("%s: same key", name)
		}
		sim := newSim(simenv.Options{})
		s := Open(sim.FS, dir, base)
		s.Put("sema", "u", key("a"), []byte("x"))
		s.Put("syntax", "f", key("b"), []byte("y"))
		o := Open(sim.FS, dir, k)
		if _, ok := o.Get("sema", key("a")); ok {
			t.Errorf("%s: an entry of the old key was read", name)
		}
		if fs := files(t, sim.FS); len(fs) != 1 ||
			path.Base(fs[0]) != "index" {
			t.Errorf("%s: files after the change: %q", name, fs)
		}
		// The same key again: the cache is the new one.
		o.Put("sema", "u", key("a"), []byte("z"))
		if b, ok := Open(sim.FS, dir, k).Get("sema", key("a")); !ok ||
			string(b) != "z" {
			t.Errorf("%s: rebuilt cache: %q %v", name, b, ok)
		}
	}
}

// damage returns the ways a file can be corrupted.
func damage(b []byte) map[string][]byte {
	out := map[string][]byte{"empty": {}, "truncated": b[:len(b)/2],
		"short-by-one": b[:len(b)-1], "longer": append(slices.Clone(b),
			0)}
	for _, i := range []int{0, 5, len(entryMagic) + 2, len(b) / 2,
		len(b) - 5, len(b) - 1} {
		c := slices.Clone(b)
		c[i] ^= 0x01
		out[fmt.Sprintf("bit@%d", i)] = c
	}
	return out
}

// TestCorruptEntry (#37): a damaged entry file is never returned; the
// whole cache is thrown away, silently, and works again at once.
func TestCorruptEntry(t *testing.T) {
	sim := newSim(simenv.Options{})
	s := Open(sim.FS, dir, testKey())
	s.Put("sema", "u", key("a"), []byte("the payload of a"))
	name := path.Join(dir, "sema", fmt.Sprintf("%x", key("a")))
	good, err := sim.FS.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	for how, bad := range damage(good) {
		sim := newSim(simenv.Options{})
		s := Open(sim.FS, dir, testKey())
		s.Put("sema", "u", key("a"), []byte("the payload of a"))
		s.Put("syntax", "f", key("c"), []byte("another entry"))
		if err := sim.FS.WriteFile(name, bad, 0o644); err != nil {
			t.Fatal(err)
		}
		o := Open(sim.FS, dir, testKey())
		if b, ok := o.Get("sema", key("a")); ok {
			t.Errorf("%s: damaged entry read: %q", how, b)
		}
		if fs := files(t, sim.FS); len(fs) != 1 {
			t.Errorf("%s: the cache was not thrown away: %q", how, fs)
		}
		if _, ok := o.Get("syntax", key("c")); ok {
			t.Errorf("%s: an entry survived the wipe", how)
		}
		o.Put("sema", "u", key("a"), []byte("new"))
		if b, ok := o.Get("sema", key("a")); !ok || string(b) != "new" {
			t.Errorf("%s: after the wipe: %q %v", how, b, ok)
		}
	}
	// An entry moved to another key's file is damage too.
	sim = newSim(simenv.Options{})
	s = Open(sim.FS, dir, testKey())
	s.Put("sema", "u", key("a"), []byte("a"))
	b, _ := sim.FS.ReadFile(name)
	other := path.Join(dir, "sema", fmt.Sprintf("%x", key("b")))
	if err := sim.FS.WriteFile(other, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Open(sim.FS, dir, testKey()).Get("sema",
		key("b")); ok {
		t.Error("an entry under another key's name was read")
	}
}

// TestCorruptIndex: a damaged or missing index throws the entries away.
func TestCorruptIndex(t *testing.T) {
	sim := newSim(simenv.Options{})
	s := Open(sim.FS, dir, testKey())
	idx, err := sim.FS.ReadFile(path.Join(dir, "index"))
	if err != nil {
		t.Fatal(err)
	}
	_ = s
	for how, bad := range damage(idx) {
		sim := newSim(simenv.Options{})
		s := Open(sim.FS, dir, testKey())
		s.Put("sema", "u", key("a"), []byte("a"))
		if err := sim.FS.WriteFile(path.Join(dir, "index"), bad,
			0o644); err != nil {
			t.Fatal(err)
		}
		o := Open(sim.FS, dir, testKey())
		if _, ok := o.Get("sema", key("a")); ok {
			t.Errorf("%s: entry read under a damaged index", how)
		}
		if !o.Enabled() {
			t.Errorf("%s: store disabled", how)
		}
	}
	sim = newSim(simenv.Options{})
	Open(sim.FS, dir, testKey()).Put("sema", "u", key("a"), []byte("a"))
	if err := sim.FS.Remove(path.Join(dir, "index")); err != nil {
		t.Fatal(err)
	}
	if _, ok := Open(sim.FS, dir, testKey()).Get("sema", key("a")); ok {
		t.Error("entry read without an index")
	}
}

// TestUnwritable (#37): a cache folder GoHard cannot write to is no
// cache: the store is disabled, reads miss and writes are dropped,
// and nothing fails.
func TestUnwritable(t *testing.T) {
	for _, op := range []simenv.Op{simenv.OpMkdir, simenv.OpWrite,
		simenv.OpRename} {
		sim := newSim(simenv.Options{})
		var name string
		switch op {
		case simenv.OpMkdir:
			name = path.Join(dir, "syntax")
		case simenv.OpWrite:
			name = dir // WriteTemp's name is unknown: fail every write
		case simenv.OpRename:
			name = dir
		}
		var s *Store
		if op == simenv.OpMkdir {
			sim.FS.FailNext(op, name, nil)
			s = Open(sim.FS, dir, testKey())
		} else {
			faulty := simenv.New(simenv.Options{Faults: simenv.Faults{
				WriteError: simenv.Always}})
			sim = faulty
			s = Open(sim.FS, dir, testKey())
		}
		if s.Enabled() {
			t.Errorf("%s: store enabled", op)
		}
		s.Put("sema", "u", key("a"), []byte("a"))
		if _, ok := s.Get("sema", key("a")); ok {
			t.Errorf("%s: disabled store hit", op)
		}
		for _, f := range files(t, sim.FS) {
			t.Errorf("%s: file written: %s", op, f)
		}
	}
	// Writes that start failing after the open are dropped.
	sim := newSim(simenv.Options{})
	s := Open(sim.FS, dir, testKey())
	sim.FS.FailNext(simenv.OpRename, path.Join(dir, "sema", ".put-1.tmp"),
		nil)
	for i := range 3 {
		sim.FS.FailNext(simenv.OpWrite, path.Join(dir, "sema",
			fmt.Sprintf(".put-%d.tmp", i+2)), nil)
	}
	s.Put("sema", "u", key("a"), []byte("a"))
	if _, ok := s.Get("sema", key("a")); ok {
		t.Error("a failed write was read")
	}
	for _, f := range files(t, sim.FS) {
		if path.Base(f) != "index" {
			t.Errorf("file left behind: %s", f)
		}
	}
}

// TestSlots: a store keeps one entry per slot it writes, so an editing
// session does not pile up entries; another slot's entry stays.
func TestSlots(t *testing.T) {
	sim := newSim(simenv.Options{})
	s := Open(sim.FS, dir, testKey())
	s.Put("sema", "u", key("1"), []byte("1"))
	s.Put("sema", "v", key("v"), []byte("v"))
	s.Put("sema", "u", key("2"), []byte("2"))
	s.Put("syntax", "u", key("3"), []byte("3"))
	if _, ok := s.Get("sema", key("1")); ok {
		t.Error("the replaced entry of slot u is still there")
	}
	for _, k := range []string{"2", "v"} {
		if _, ok := s.Get("sema", key(k)); !ok {
			t.Errorf("entry %s is gone", k)
		}
	}
	if _, ok := s.Get("syntax", key("3")); !ok {
		t.Error("a slot of another kind replaced an entry")
	}
}

// TestPrune: past MaxEntries entries of a kind, opening the cache
// removes the older half (and stray temp files); the newest stay.
func TestPrune(t *testing.T) {
	sim := newSim(simenv.Options{})
	s := Open(sim.FS, dir, testKey())
	for i := range MaxEntries + 10 {
		s.Put("syntax", fmt.Sprint(i), key(fmt.Sprint(i)), []byte("x"))
		sim.Clock.Advance(1)
	}
	tmp := path.Join(dir, "sema", ".put-left.tmp")
	if err := sim.FS.WriteFile(tmp, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := Open(sim.FS, dir, testKey())
	n := len(files(t, sim.FS)) - 1 // the index
	if n != MaxEntries/2 {
		t.Errorf("%d entries after pruning, want %d", n, MaxEntries/2)
	}
	if _, ok := o.Get("syntax", key(fmt.Sprint(MaxEntries+9))); !ok {
		t.Error("the newest entry was pruned")
	}
	if _, ok := o.Get("syntax", key("0")); ok {
		t.Error("the oldest entry was kept")
	}
	if _, err := sim.FS.Stat(tmp); err == nil {
		t.Error("stray temp file kept")
	}
}

// TestModel: under seeded read and write faults, a store never returns
// anything but the payload of a key (entries are content-addressed:
// one key, one payload), and goes on working: a property over 64
// seeds of random operation sequences.
func TestModel(t *testing.T) {
	payload := func(k string) string { return "payload of " + k }
	for seed := range uint64(64) {
		sim := newSim(simenv.Options{Seed: seed, Faults: simenv.Faults{
			ReadError: 50, WriteError: 100}})
		r := &env.SplitMix64{State: seed}
		s := Open(sim.FS, dir, testKey())
		hits := 0
		for step := range 300 {
			k := fmt.Sprint(env.Intn(r, 8))
			switch env.Intn(r, 6) {
			case 0, 1:
				s.Put("sema", k, key(k), []byte(payload(k)))
			case 2:
				s = Open(sim.FS, dir, testKey())
			default:
				b, ok := s.Get("sema", key(k))
				if ok && string(b) != payload(k) {
					t.Fatalf("seed %d step %d: Get(%s) = %q", seed,
						step, k, b)
				}
				if ok {
					hits++
				}
			}
		}
		if hits == 0 {
			t.Errorf("seed %d: no hits", seed)
		}
	}
}

// TestConcurrent: Get and Put from parallel jobs (go test -race).
func TestConcurrent(t *testing.T) {
	sim := newSim(simenv.Options{})
	s := Open(sim.FS, dir, testKey())
	realenv.New(realenv.Options{}).Jobs.Run(8, 8, func(g int) {
		for i := range 50 {
			k := key(fmt.Sprint(g, i%5))
			s.Put("syntax", fmt.Sprint(g, i%5), k, []byte{byte(i)})
			if b, ok := s.Get("syntax", k); ok && len(b) != 1 {
				t.Errorf("bad payload %q", b)
			}
		}
	})
	if st := s.Stats(); st.Puts != 400 || st.Wipes != 0 {
		t.Errorf("stats %+v", st)
	}
}

// Package cache is GoHard's on-disk analysis cache (#37): one folder,
// <root>/.gohard/cache, holding syntax entries (a parsed file) and
// semantic entries (an analyzed design unit), which sema stores and
// restores (sema.Cache). This package owns the shared machinery: the
// whole-cache key, the file format and its versioning, and recovery.
//
// The rules (#37, moved from #20):
//
//   - The cache only ever changes speed, never results: sema keys every
//     entry by everything its result depends on.
//   - The whole-cache key is the GoHard build, the entry schema, the
//     VHDL standard, the project config hash, the `-- psl` setting and
//     the kinds.yaml hash. On a mismatch, or any corrupted file, the
//     whole cache is thrown away and rebuilt, silently.
//   - A cache folder GoHard cannot write to: the run continues without
//     the cache.
//   - The cache prints nothing.
//
// Every file access goes through env.FS. Files are written to a temp
// file and renamed, so a reader never sees a half-written entry; a
// file that does not check out (a crash, a full disk, another tool) is
// corruption.
//
// Layout:
//
//	.gohard/cache/index           magic, format, whole-cache key
//	.gohard/cache/syntax/<key>    syntax entries
//	.gohard/cache/sema/<key>      semantic entries
//
// An entry file is the magic, the format version, the whole-cache key,
// the entry kind and key, the payload length, the payload, and a
// CRC-32C of everything before it.
package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/cst"
	"github.com/connerohnesorge/gohard/internal/env"
)

// Dir is the cache folder under a project root.
func Dir(root string) string {
	return filepath.Join(root, ".gohard", "cache")
}

// Format is the version of the file format below. A cache of another
// format is thrown away.
const Format = 1

const (
	indexMagic = "gohard-cache-index\x00"
	entryMagic = "gohard-cache-entry\x00"
)

// Kinds are the entry kinds, each a subfolder.
var Kinds = []string{"syntax", "sema"}

// MaxEntries bounds the entries of one kind: past it the oldest half
// is removed when a cache is opened (an editing session writes a new
// entry for each changed unit).
const MaxEntries = 4096

// Key is the whole-cache key (#37).
type Key struct {
	// Version identifies the GoHard build: the version, the commit,
	// and something that changes with the code of every build
	// (cmd/gohard passes a SHA-256 of the executable's content).
	Version string
	// Schema is the stored form of the entries (sema.CacheSchema).
	Schema string
	// Standard is the selected VHDL standard.
	Standard string
	// Config is the hash of the project configuration sema sees.
	Config string
	// PSL is psl.comments, the `-- psl` setting.
	PSL bool
	// Kinds is the kinds.yaml hash (cst.KindsHash).
	Kinds string
}

// NewKey returns the key of a cache for the configuration cfg (as sema
// sees it: library folders root-relative) under build version and
// entry schema.
func NewKey(version, schema string, cfg config.Config) Key {
	sum := sha256.Sum256(cfg.Identity())
	return Key{Version: version, Schema: schema, Standard: cfg.Standard,
		Config: hex.EncodeToString(sum[:]), PSL: cfg.PSL.Comments,
		Kinds: cst.KindsHash}
}

// Sum returns the key's hash.
func (k Key) Sum() [32]byte {
	h := sha256.New()
	for _, p := range []string{k.Version, k.Schema, k.Standard, k.Config,
		boolString(k.PSL), k.Kinds} {
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// Store is one opened cache. It is safe for concurrent use. A disabled
// store (the folder cannot be written) has no entries and drops every
// Put.
type Store struct {
	fs  env.FS
	dir string
	key [32]byte

	mu      sync.Mutex
	on      bool
	gen     uint64            // bumped by every wipe
	written map[string]string // slot -> file this store last wrote
	stats   Stats
}

// Stats count what a store did, for tests and the #19 benchmark.
type Stats struct {
	Hits, Misses, Puts, Wipes int
}

// Open opens the cache in dir for key k, creating it when it is
// missing and throwing it away when it belongs to another key, another
// format, or is damaged. It never fails: a folder it cannot write
// gives a disabled store.
func Open(fsys env.FS, dir string, k Key) *Store {
	s := &Store{fs: fsys, dir: dir, key: k.Sum(),
		written: map[string]string{}}
	for _, kind := range Kinds {
		if fsys.MkdirAll(filepath.Join(dir, kind), 0o755) != nil {
			return s
		}
	}
	b, err := fsys.ReadFile(s.indexPath())
	if err != nil || !bytes.Equal(b, s.index()) {
		s.wipe()
		if err != nil && errors.Is(err, fs.ErrNotExist) {
			s.stats.Wipes-- // a new cache: nothing was thrown away
		}
		if !s.writeFile(s.indexPath(), s.index()) {
			return s
		}
	}
	s.on = true
	s.prune()
	return s
}

// Enabled reports whether the store reads and writes entries.
func (s *Store) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.on
}

// Stats returns the counts so far.
func (s *Store) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

func (s *Store) indexPath() string { return filepath.Join(s.dir, "index") }

func (s *Store) index() []byte {
	b := []byte(indexMagic)
	b = binary.LittleEndian.AppendUint32(b, Format)
	b = append(b, s.key[:]...)
	return binary.LittleEndian.AppendUint32(b, crc(b))
}

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

func crc(b []byte) uint32 { return crc32.Checksum(b, castagnoli) }

func validKind(kind string) bool { return slices.Contains(Kinds, kind) }

func (s *Store) entryPath(kind string, key [32]byte) string {
	return filepath.Join(s.dir, kind, hex.EncodeToString(key[:]))
}

// encode returns the file of an entry.
func (s *Store) encode(kind string, key [32]byte, data []byte) []byte {
	b := make([]byte, 0, len(entryMagic)+4+32+1+len(kind)+32+8+
		len(data)+4)
	b = append(b, entryMagic...)
	b = binary.LittleEndian.AppendUint32(b, Format)
	b = append(b, s.key[:]...)
	b = append(b, byte(len(kind)))
	b = append(b, kind...)
	b = append(b, key[:]...)
	b = binary.LittleEndian.AppendUint64(b, uint64(len(data)))
	b = append(b, data...)
	return binary.LittleEndian.AppendUint32(b, crc(b))
}

// decode returns the payload of entry file b, or false when b is not
// an intact entry of this cache for (kind, key).
func (s *Store) decode(kind string, key [32]byte, b []byte) ([]byte,
	bool,
) {
	head := len(entryMagic) + 4 + 32 + 1 + len(kind) + 32 + 8
	if len(b) < head+4 {
		return nil, false
	}
	body, sum := b[:len(b)-4], binary.LittleEndian.Uint32(b[len(b)-4:])
	if crc(body) != sum {
		return nil, false
	}
	want := s.encode(kind, key, nil)[:head-8]
	if !bytes.Equal(body[:head-8], want) {
		return nil, false
	}
	n := binary.LittleEndian.Uint64(body[head-8 : head])
	if n != uint64(len(body)-head) {
		return nil, false
	}
	return body[head:], true
}

// Get returns the payload stored under (kind, key). A damaged entry
// throws the whole cache away.
func (s *Store) Get(kind string, key [32]byte) ([]byte, bool) {
	s.mu.Lock()
	on, gen := s.on, s.gen
	s.mu.Unlock()
	if !on || !validKind(kind) {
		return nil, false
	}
	b, err := s.fs.ReadFile(s.entryPath(kind, key))
	if err != nil {
		s.count(func(st *Stats) { st.Misses++ })
		if !errors.Is(err, fs.ErrNotExist) {
			s.discard(gen) // unreadable: treat as damaged
		}
		return nil, false
	}
	data, ok := s.decode(kind, key, b)
	if !ok {
		s.count(func(st *Stats) { st.Misses++ })
		s.discard(gen)
		return nil, false
	}
	s.count(func(st *Stats) { st.Hits++ })
	return data, true
}

func (s *Store) count(f func(*Stats)) {
	s.mu.Lock()
	f(&s.stats)
	s.mu.Unlock()
}

// Put stores data under (kind, key). slot names what the entry is for;
// the entry this store wrote last for the same slot is removed, so an
// editing session keeps one entry per file and unit.
func (s *Store) Put(kind, slot string, key [32]byte, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.on || !validKind(kind) {
		return
	}
	p := s.entryPath(kind, key)
	if !s.writeFile(p, s.encode(kind, key, data)) {
		return
	}
	s.stats.Puts++
	ws := kind + "\x00" + slot
	if old, ok := s.written[ws]; ok && old != p {
		_ = s.fs.Remove(old)
	}
	s.written[ws] = p
}

// writeFile writes a file through a temp file and a rename.
func (s *Store) writeFile(p string, data []byte) bool {
	dir := filepath.Dir(p)
	tmp, err := s.fs.WriteTemp(dir, ".put-", data)
	if err != nil {
		return false
	}
	if err := s.fs.Rename(tmp, p); err != nil {
		_ = s.fs.Remove(tmp)
		return false
	}
	return true
}

// Discard throws the whole cache away and starts it again empty.
func (s *Store) Discard() {
	s.mu.Lock()
	gen := s.gen
	s.mu.Unlock()
	s.discard(gen)
}

// discard wipes the cache unless it was wiped since generation gen.
func (s *Store) discard(gen uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.on || s.gen != gen {
		return
	}
	s.wipe()
	if !s.writeFile(s.indexPath(), s.index()) {
		s.on = false
	}
}

// wipe removes the index and every entry and temp file.
func (s *Store) wipe() {
	s.gen++
	s.stats.Wipes++
	clear(s.written)
	_ = s.fs.Remove(s.indexPath())
	for _, kind := range Kinds {
		d := filepath.Join(s.dir, kind)
		es, err := s.fs.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range es {
			if !e.IsDir() {
				_ = s.fs.Remove(filepath.Join(d, e.Name()))
			}
		}
	}
}

// prune removes stray temp files and, past MaxEntries entries of a
// kind, the older half of them (by modification time, then name).
func (s *Store) prune() {
	for _, kind := range Kinds {
		d := filepath.Join(s.dir, kind)
		es, err := s.fs.ReadDir(d)
		if err != nil {
			continue
		}
		type entry struct {
			name string
			mod  int64
		}
		var keep []entry
		for _, e := range es {
			p := filepath.Join(d, e.Name())
			switch {
			case e.IsDir():
			case strings.HasPrefix(e.Name(), ".put-"):
				_ = s.fs.Remove(p)
			default:
				var mod int64
				if fi, err := e.Info(); err == nil {
					mod = fi.ModTime().UnixNano()
				}
				keep = append(keep, entry{e.Name(), mod})
			}
		}
		if len(keep) <= MaxEntries {
			continue
		}
		slices.SortFunc(keep, func(a, b entry) int {
			if a.mod != b.mod {
				if a.mod < b.mod {
					return -1
				}
				return 1
			}
			return strings.Compare(a.name, b.name)
		})
		for _, e := range keep[:len(keep)-MaxEntries/2] {
			_ = s.fs.Remove(filepath.Join(d, e.name))
		}
	}
}

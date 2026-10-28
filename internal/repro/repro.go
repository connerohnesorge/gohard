// Package repro reads and writes the replay headers of #29: the
// schema-1 simulation repro file `.gohard/traces/<tb>-<seed>.repro.json`
// and the language-server session file `<n>.jsonl`, whose first line
// carries the same header.
//
// A header names everything a run depends on: the gohard version,
// commit and Go toolchain, the bundled IEEE library, the standard, the testbench, the seed, the defines, the budgets and
// models in effect, the sha256 of every source file and of
// gohard.yaml, and the std.env environment (epoch, variables and
// --real-env, #18). Encoding is canonical (fixed key order, sorted maps and
// sources), so the same run always writes the same bytes, and reading
// then writing a file reproduces it byte for byte.
//
// The package is pure apart from the env.FS it is handed.
package repro

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// Schema is the only header schema this version reads and writes.
const Schema = 1

// ConfigFile is the project configuration whose hash a header records.
const ConfigFile = "gohard.yaml"

// TraceDir holds the repro files and traces, relative to the project
// root (#28: there is no key for it).
const TraceDir = ".gohard/traces"

// Header is the repro header. Field order is the JSON key order.
type Header struct {
	// Schema is always 1; Marshal sets it.
	Schema int `json:"schema"`
	// Version is the gohard version that recorded the run.
	Version string `json:"version"`
	// Commit is the commit the gohard binary was built from, with a
	// "-dirty" suffix for a modified tree, or "unknown" (#39).
	Commit string `json:"commit"`
	// IEEE is the bundled IEEE library, "<tag> <commit>" (stdlib.IEEE),
	// or "unknown".
	IEEE string `json:"ieee"`
	// Go is the Go toolchain that built the binary, such as "go1.27.2",
	// or "unknown".
	Go string `json:"go"`
	// Standard is the VHDL standard, for example "2008".
	Standard string `json:"standard"`
	// Testbench is the simulated top unit; empty in an LSP session
	// that ran no simulation.
	Testbench string `json:"testbench"`
	// Seed is the run's seed (0 is declaration order).
	Seed uint64 `json:"seed"`
	// Defines are the conditional-analysis defines in effect, after
	// --define overrides.
	Defines map[string]string `json:"defines"`
	// Budgets are the simulation budgets in effect.
	Budgets Budgets `json:"budgets"`
	// Models map `lib.unit` to the stand-in file used for it.
	Models map[string]string `json:"models"`
	// Config is the hash of gohard.yaml, or nil when the project has
	// none.
	Config *File `json:"config"`
	// Sources are the hashes of every source file, sorted by path.
	Sources []File `json:"sources"`
	// Env is the std.env environment of the run (#18): the epoch, the
	// variables GETENV saw and whether --real-env was given. Every
	// v1.1 recording writes it; a file recorded before v1.1 has no
	// "env" key and replays with the defaults.
	Env *Env `json:"env,omitempty"`
	// Inputs are the project files the design read while it ran (#18),
	// sorted by path, with the hash of what it read; an empty SHA256
	// records a file it looked for and did not find. A replay whose
	// inputs differ is repro/source-changed, like a changed source.
	// The key is omitted when the design read no file, so a v1.0 file
	// reads and writes back byte for byte.
	Inputs []File `json:"inputs,omitempty"`
	// Listings are the project directories the design listed while it
	// ran (std.env DIR_*, #18), sorted by path ("." the root), with
	// env.ListingSHA256 of the entries it saw; an empty SHA256 records
	// a directory it looked for and did not find. A replay whose
	// listings differ is repro/source-changed. The key is omitted when
	// the design listed no directory.
	Listings []File `json:"listings,omitempty"`
}

// Env is the recorded std.env environment. Field order is the JSON key
// order.
type Env struct {
	// Epoch is simulation.env.epoch, RFC 3339 UTC.
	Epoch string `json:"epoch"`
	// Vars are simulation.env.vars after --env overrides.
	Vars map[string]string `json:"vars"`
	// Real is --real-env: the run read the real clock and
	// environment, so it cannot be replayed (repro/non-reproducible).
	Real bool `json:"real"`
}

// NewEnv records the environment of a run from the simulation.env.*
// values in effect (after --env) and --real-env.
func NewEnv(e config.SimEnv, real bool) *Env {
	out := &Env{Epoch: config.FormatTimestamp(e.Epoch),
		Vars: map[string]string{}, Real: real}
	for _, v := range e.Vars {
		out.Vars[v.Name] = v.Value
	}
	return out
}

// Apply returns s with the recorded epoch and variables, the
// environment a --repro replays. A nil Env is the pre-v1.1 default:
// s is returned unchanged.
func (e *Env) Apply(s config.SimEnv) config.SimEnv {
	if e == nil {
		return s
	}
	if t, ok := config.ParseTimestamp(e.Epoch); ok {
		s.Epoch = t
	}
	s.Vars = make([]config.Var, 0, len(e.Vars))
	for _, k := range slices.Sorted(maps.Keys(e.Vars)) {
		s.Vars = append(s.Vars, config.Var{Name: k, Value: e.Vars[k]})
	}
	return s
}

// RunDirPrefix holds the run directories, relative to the project
// root: a run's design writes land in RunDirPrefix/<hash>/ (#18).
const RunDirPrefix = ".gohard/run"

// RunDir is the run directory of the run h records, relative to the
// project root and slash-separated: RunDirPrefix/<hash>, where <hash>
// is the first 16 hex digits of the sha256 of the run's identity: the
// standard, testbench, seed, defines, budgets, models, the hashes of
// gohard.yaml and every source, and the environment. The gohard
// build (version, commit, IEEE, Go) is not part of it, so a --repro
// replay by another version writes to the same directory.
func RunDir(h Header) string {
	h = canonical(h)
	id := h
	id.Schema, id.Version, id.Commit, id.IEEE, id.Go = 0, "", "", "", ""
	// The inputs are known only once the run ended, and the run
	// directory is needed before it starts; the directory is replaced
	// whole by every run, so it never mixes the output of two runs.
	id.Inputs, id.Listings = nil, nil
	data, err := encodeJSON(id, "")
	if err != nil {
		panic(err) // plain strings, numbers and maps always encode
	}
	return RunDirPrefix + "/" + Hash(data)[:16]
}

// Budgets are the `simulation.*_budget` values of #28.
type Budgets struct {
	Time  string `json:"time"`  // VHDL time, for example "1 ms"
	Delta int64  `json:"delta"` // deltas per time step
	Step  int64  `json:"step"`  // kernel steps
}

// File is one hashed file. Path is relative to the project root and
// slash-separated.
type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// InputFiles are the Inputs of a header for the files a run read
// (env.Overlay.Inputs), in the same order.
func InputFiles(in []env.Input) []File {
	if len(in) == 0 {
		return nil
	}
	out := make([]File, len(in))
	for i, f := range in {
		out[i] = File{Path: f.Path, SHA256: f.SHA256}
	}
	return out
}

// Hash returns the lowercase hex sha256 of data.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Marshal encodes h canonically: two-space indented JSON with a final
// newline. Sources are sorted by path; nil maps and slices are written
// as {} and []. It fails if h is invalid (see Validate).
func Marshal(h Header) ([]byte, error) { return encode(h, "  ") }

// MarshalLine encodes h canonically on one line with a final newline,
// the header line of a session file.
func MarshalLine(h Header) ([]byte, error) { return encode(h, "") }

func encode(h Header, indent string) ([]byte, error) {
	h = canonical(h)
	if err := Validate(h); err != nil {
		return nil, err
	}
	return encodeJSON(h, indent)
}

func encodeJSON(v any, indent string) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Unknown is the value recorded for a build fact the binary does not
// know (#39).
const Unknown = "unknown"

// canonical returns h with its schema set, unknown build facts written
// as Unknown, empty collections non-nil and sources sorted. It never
// modifies the caller's slices.
func canonical(h Header) Header {
	h.Schema = Schema
	for _, f := range []*string{&h.Commit, &h.IEEE, &h.Go} {
		if *f == "" {
			*f = Unknown
		}
	}
	if h.Defines == nil {
		h.Defines = map[string]string{}
	}
	if h.Models == nil {
		h.Models = map[string]string{}
	}
	h.Sources = slices.Clone(h.Sources)
	if h.Sources == nil {
		h.Sources = []File{}
	}
	slices.SortFunc(h.Sources, func(a, b File) int {
		return cmp.Compare(a.Path, b.Path)
	})
	if len(h.Inputs) == 0 {
		h.Inputs = nil // the key is omitted
	} else {
		h.Inputs = slices.Clone(h.Inputs)
		slices.SortFunc(h.Inputs, func(a, b File) int {
			return cmp.Compare(a.Path, b.Path)
		})
	}
	if len(h.Listings) == 0 {
		h.Listings = nil // the key is omitted
	} else {
		h.Listings = slices.Clone(h.Listings)
		slices.SortFunc(h.Listings, func(a, b File) int {
			return cmp.Compare(a.Path, b.Path)
		})
	}
	if h.Env != nil {
		e := *h.Env
		if e.Vars == nil {
			e.Vars = map[string]string{}
		}
		h.Env = &e
	}
	return h
}

// Validate checks the invariants of a header: schema 1, file paths
// that are clean, relative and slash-separated, well-formed hashes, and
// no source listed twice.
func Validate(h Header) error {
	if h.Schema != Schema {
		return fmt.Errorf("unsupported schema %d (this gohard reads "+
			"schema %d)", h.Schema, Schema)
	}
	if h.Standard != "" && !slices.Contains(standards(), h.Standard) {
		return fmt.Errorf("standard %q is not one of %s", h.Standard,
			strings.Join(standards(), ", "))
	}
	if _, ok := config.ParseTime(h.Budgets.Time); !ok {
		return fmt.Errorf("budgets.time %q is not a VHDL time such as "+
			"\"1 ms\"", h.Budgets.Time)
	}
	if h.Budgets.Delta < 1 || h.Budgets.Step < 1 {
		return fmt.Errorf("budgets.delta %d and budgets.step %d must be "+
			"at least 1", h.Budgets.Delta, h.Budgets.Step)
	}
	for _, name := range slices.Sorted(maps.Keys(h.Defines)) {
		if !config.IsIdentifier(name) {
			return fmt.Errorf("define %q is not a VHDL identifier", name)
		}
	}
	if h.Config != nil {
		if err := validFile(*h.Config); err != nil {
			return fmt.Errorf("config: %w", err)
		}
		// Verify looks for a newly added gohard.yaml only when Config
		// is nil, so any other path would hide one.
		if h.Config.Path != ConfigFile {
			return fmt.Errorf("config: path %q must be %s",
				h.Config.Path, ConfigFile)
		}
	}
	if e := h.Env; e != nil {
		if _, ok := config.ParseTimestamp(e.Epoch); !ok {
			return fmt.Errorf("env.epoch %q is not an RFC 3339 UTC "+
				"time", e.Epoch)
		}
		for _, name := range slices.Sorted(maps.Keys(e.Vars)) {
			if !config.IsVarName(name) {
				return fmt.Errorf("env.vars: %q is not a variable name",
					name)
			}
		}
	}
	seen := make(map[string]bool, len(h.Sources))
	for _, f := range h.Sources {
		if err := validFile(f); err != nil {
			return fmt.Errorf("source: %w", err)
		}
		if seen[f.Path] {
			return fmt.Errorf("source %s is listed twice", f.Path)
		}
		seen[f.Path] = true
	}
	inputs := make(map[string]bool, len(h.Inputs))
	for _, f := range h.Inputs {
		g := f
		if g.SHA256 == "" {
			g.SHA256 = Hash(nil) // absent: only the path to check
		}
		if err := validFile(g); err != nil {
			return fmt.Errorf("input: %w", err)
		}
		if strings.HasPrefix(f.Path, "../") {
			return fmt.Errorf("input %s is outside the project", f.Path)
		}
		if inputs[f.Path] {
			return fmt.Errorf("input %s is listed twice", f.Path)
		}
		inputs[f.Path] = true
	}
	lists := make(map[string]bool, len(h.Listings))
	for _, f := range h.Listings {
		g := f
		if g.SHA256 == "" {
			g.SHA256 = Hash(nil)
		}
		if g.Path == "." {
			g.Path = "x" // the root
		}
		if err := validFile(g); err != nil {
			return fmt.Errorf("listing: %w", err)
		}
		if strings.HasPrefix(f.Path, "../") {
			return fmt.Errorf("listing %s is outside the project", f.Path)
		}
		if lists[f.Path] {
			return fmt.Errorf("listing %s is listed twice", f.Path)
		}
		lists[f.Path] = true
	}
	return nil
}

// standards are the values of the standard key of gohard.yaml (#28).
func standards() []string {
	k, _ := config.LookupKey("standard")
	return k.Enum
}

func validFile(f File) error {
	if err := validPath(f.Path); err != nil {
		return err
	}
	if len(f.SHA256) != 2*sha256.Size ||
		strings.Trim(f.SHA256, "0123456789abcdef") != "" {
		return fmt.Errorf("%s: sha256 %q is not 64 lowercase hex digits",
			f.Path, f.SHA256)
	}
	return nil
}

// validPath accepts a clean slash path relative to the project root
// naming a file. It may start with ../ segments: a file outside the root
// named on the command line is reported as ../x.vhd by the workspace,
// and Verify resolves it against the root the same way (#29).
func validPath(p string) error {
	rest := p
	for strings.HasPrefix(rest, "../") {
		rest = rest[len("../"):]
	}
	if rest == "" || rest == "." || rest == ".." || path.IsAbs(p) ||
		strings.Contains(p, `\`) || path.Clean(p) != p ||
		strings.HasPrefix(rest, "..") && path.Clean(rest) != rest ||
		(len(p) >= 2 && p[1] == ':') {
		return fmt.Errorf("path %q is not a clean relative slash path "+
			"to a file", p)
	}
	return nil
}

// Unmarshal decodes a header written by Marshal or MarshalLine. It is
// strict: unknown, missing, duplicate or differently cased keys,
// trailing data, another schema and invalid headers are errors, so the
// values a replay uses are exactly the ones a reader of the file sees.
func Unmarshal(data []byte) (Header, error) {
	var probe struct {
		Schema *int `json:"schema"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Header{}, err
	}
	if probe.Schema == nil {
		return Header{}, errors.New("missing \"schema\"")
	}
	if *probe.Schema != Schema {
		return Header{}, fmt.Errorf("unsupported schema %d (this gohard "+
			"reads schema %d)", *probe.Schema, Schema)
	}
	var h Header
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&h); err != nil {
		return Header{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Header{}, errors.New("trailing data after the header")
	}
	if err := uniqueKeys(data); err != nil {
		return Header{}, err
	}
	// encoding/json matches keys case-insensitively and fills absent
	// keys with zero values; re-encoding what was decoded and comparing
	// the generic trees rejects both (and nulls for strings).
	again, err := encodeJSON(h, "")
	if err != nil {
		return Header{}, err
	}
	in, err1 := generic(data)
	out, err2 := generic(again)
	if err := errors.Join(err1, err2); err != nil {
		return Header{}, err
	}
	if !reflect.DeepEqual(in, out) {
		return Header{}, errors.New("keys must be exactly the schema " +
			"1 keys, each once, in lower case, with no nulls for values")
	}
	h = canonical(h)
	return h, Validate(h)
}

// generic decodes JSON into maps, slices and json.Number values.
func generic(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

// uniqueKeys reports an error if any JSON object in data repeats a key
// (encoding/json keeps the last one silently).
func uniqueKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	type frame struct {
		obj  bool
		key  bool // the next token in this object is a key
		seen map[string]bool
	}
	var stack []frame
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		top := len(stack) - 1
		if top >= 0 && stack[top].obj && stack[top].key {
			if k, ok := tok.(string); ok {
				if stack[top].seen[k] {
					return fmt.Errorf("key %q appears twice", k)
				}
				stack[top].seen[k] = true
				stack[top].key = false
				continue
			}
		}
		switch tok {
		case json.Delim('{'):
			stack = append(stack, frame{obj: true, key: true,
				seen: map[string]bool{}})
			continue
		case json.Delim('['):
			stack = append(stack, frame{})
			continue
		case json.Delim('}'), json.Delim(']'):
			stack = stack[:top]
		}
		// A value ended: the enclosing object expects a key next.
		if n := len(stack) - 1; n >= 0 && stack[n].obj {
			stack[n].key = true
		}
	}
}

// FileName is the repro file of a run, relative to the project root:
// `.gohard/traces/<tb>-<seed>.repro.json`. Bytes of tb outside
// [a-z0-9_.-] are written as %XX, so any testbench name (an extended
// identifier, a `lib.unit`) gives one safe file name. Upper-case
// letters are escaped too: extended identifiers \Foo\ and \foo\ are
// different units, and on a case-insensitive file system (macOS,
// Windows) their names must still differ.
func FileName(tb string, seed uint64) string {
	return TraceDir + "/" + escape(tb) + "-" +
		strconv.FormatUint(seed, 10) + ".repro.json"
}

func escape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '_',
			c == '-',
			c == '.' && i > 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// Write stores h under root at FileName(h.Testbench, h.Seed), creating
// the trace directory, through a temporary file and a rename so a
// reader never sees half a file. It returns the name relative to root.
func Write(fsys env.FS, root string, h Header) (string, error) {
	data, err := Marshal(h)
	if err != nil {
		return "", err
	}
	name := FileName(h.Testbench, h.Seed)
	if err := writeAtomic(fsys, filepath.Join(root,
		filepath.FromSlash(name)), data); err != nil {
		return "", err
	}
	return name, nil
}

func writeAtomic(fsys env.FS, name string, data []byte) error {
	dir := filepath.Dir(name)
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := fsys.WriteTemp(dir, "."+filepath.Base(name)+".", data)
	if err != nil {
		return err
	}
	if err := fsys.Rename(tmp, name); err != nil {
		fsys.Remove(tmp)
		return err
	}
	return nil
}

// Read reads and decodes the repro file name.
func Read(fsys env.FS, name string) (Header, error) {
	data, err := fsys.ReadFile(name)
	if err != nil {
		return Header{}, err
	}
	return Unmarshal(data)
}

// HashFiles hashes the files at paths (relative to root,
// slash-separated) and returns them sorted by path.
func HashFiles(fsys env.FS, root string, paths []string) ([]File, error) {
	out := make([]File, 0, len(paths))
	for _, p := range paths {
		if err := validPath(p); err != nil {
			return nil, err
		}
		data, err := readRegular(fsys, filepath.Join(root,
			filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		out = append(out, File{Path: p, SHA256: Hash(data)})
	}
	slices.SortFunc(out, func(a, b File) int {
		return cmp.Compare(a.Path, b.Path)
	})
	return out, nil
}

// HashConfig hashes root/gohard.yaml; it returns nil when the file does
// not exist.
func HashConfig(fsys env.FS, root string) (*File, error) {
	data, err := readRegular(fsys, filepath.Join(root, ConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &File{Path: ConfigFile, SHA256: Hash(data)}, nil
}

// CopyCommand is the command the "Copy repro" action copies: `gohard
// check --repro <file>`, with the file quoted for a POSIX shell when it
// needs it.
func CopyCommand(file string) string {
	return "gohard check --repro " + shellQuote(file)
}

func shellQuote(s string) string {
	safe := s != ""
	for i := range len(s) {
		c := s[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' ||
			'0' <= c && c <= '9' || strings.IndexByte("_-./%+=:,@", c) >= 0) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

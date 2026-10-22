package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ManifestPath is corpus.yaml, relative to the repository root.
const ManifestPath = "testdata/corpus/corpus.yaml"

// Dir is the corpus directory, relative to the repository root.
const Dir = "testdata/corpus"

// Manifest is the part of corpus.yaml the harness reads.
type Manifest struct {
	Schema     int          `yaml:"schema"`
	Designs    []Design     `yaml:"designs"`
	Regression []Regression `yaml:"regression"`
}

// Design is one corpus design entry.
type Design struct {
	Name            string            `yaml:"name"`
	Standard        string            `yaml:"standard"`
	Milestone       string            `yaml:"milestone"`
	Files           []File            `yaml:"files"`
	Top             string            `yaml:"top"`
	Testbench       string            `yaml:"testbench"`
	Defines         map[string]string `yaml:"defines"`
	Models          map[string]string `yaml:"models"`
	Config          string            `yaml:"config"`
	Expected        Expected          `yaml:"expected"`
	ExpectedV10     *Expected         `yaml:"expected_v1_0"`
	Invariants      []string          `yaml:"invariants"`
	Oracle          string            `yaml:"oracle"`
	HierarchyOracle string            `yaml:"hierarchy_oracle"`
	GHDL            Tool              `yaml:"ghdl"`
	NVC             Tool              `yaml:"nvc"`
	Variants        []Variant         `yaml:"variants"`
	TraceSHA256     string            `yaml:"trace_sha256"`
	OracleSHA256    string            `yaml:"oracle_sha256"`
}

// File is one source file of a design or variant.
type File struct {
	Path     string `yaml:"path"`
	Standard string `yaml:"standard"`
}

// Expected is an expected outcome (expected, expected_v1_0).
type Expected struct {
	Exit     int       `yaml:"exit"`
	Status   string    `yaml:"status"`
	StopTime string    `yaml:"stop_time"`
	Messages []Message `yaml:"messages"`
}

// Message is one expected diagnostic.
type Message struct {
	Severity string `yaml:"severity"`
	Text     string `yaml:"text"`
	Code     string `yaml:"code"`
	Time     string `yaml:"time"`
	Path     string `yaml:"path"`
	Line     int    `yaml:"line"`
}

// Variant is an extra run of a design with other files or standard.
type Variant struct {
	Name     string   `yaml:"name"`
	Standard string   `yaml:"standard"`
	Files    []File   `yaml:"files"`
	Expected Expected `yaml:"expected"`
	GHDL     Tool     `yaml:"ghdl"`
	NVC      Tool     `yaml:"nvc"`
}

// Tool is what GHDL or NVC did on a design.
type Tool struct {
	Version  string `yaml:"version"`
	Command  string `yaml:"command"`
	Result   string `yaml:"result"`
	Exit     int    `yaml:"exit"`
	StopTime string `yaml:"stop_time"`
}

// Regression is one parser-regression submodule entry.
type Regression struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// HandChecked is the oracle value of hand-written goldens.
const HandChecked = "none, hand-checked"

// ParseManifest parses corpus.yaml.
func ParseManifest(src []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(src, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}
	if len(m.Designs) == 0 {
		return nil, fmt.Errorf("%s: no designs", ManifestPath)
	}
	for i := range m.Designs {
		d := &m.Designs[i]
		if d.Name == "" || d.Testbench == "" || len(d.Files) == 0 {
			return nil, fmt.Errorf("%s: design %d: name, testbench "+
				"and files are required", ManifestPath, i)
		}
	}
	return &m, nil
}

// Design returns the design named name.
func (m *Manifest) Design(name string) (Design, bool) {
	i := slices.IndexFunc(m.Designs, func(d Design) bool {
		return d.Name == name
	})
	if i < 0 {
		return Design{}, false
	}
	return m.Designs[i], true
}

// DirOf returns the design directory, repository-relative.
func (d Design) DirOf() string { return path.Join(Dir, d.Name) }

// GoldenDir returns the design's golden directory.
func (d Design) GoldenDir() string { return path.Join(d.DirOf(), "golden") }

// ModelPaths returns the stand-in model files in key order.
func (d Design) ModelPaths() []string {
	keys := make([]string, 0, len(d.Models))
	for k := range d.Models {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, d.Models[k])
	}
	return out
}

// Has reports whether d claims the invariant id.
func (d Design) Has(id string) bool { return slices.Contains(d.Invariants, id) }

// Run is one run of a design: the main files or a variant.
type Run struct {
	Design   Design
	Variant  string // "" for the main run
	Standard string
	// Paths are the analysis files, design-relative: stand-in models
	// first (main run only), then the files in order.
	Paths    []string
	Expected Expected
}

// Name is "design" or "design/variant".
func (r Run) Name() string {
	if r.Variant == "" {
		return r.Design.Name
	}
	return r.Design.Name + "/" + r.Variant
}

// Runs returns the main run and every variant run of d. Release is
// the milestone under test: a design of a later milestone runs
// against its expected_v1_0 outcome.
func (d Design) Runs() []Run {
	exp := d.Expected
	if !Reached(d.Milestone) && d.ExpectedV10 != nil {
		exp = *d.ExpectedV10
	}
	main := Run{Design: d, Standard: d.Standard, Expected: exp,
		Paths: d.ModelPaths()}
	for _, f := range d.Files {
		main.Paths = append(main.Paths, f.Path)
	}
	out := []Run{main}
	for _, v := range d.Variants {
		r := Run{Design: d, Variant: v.Name, Standard: v.Standard,
			Expected: v.Expected}
		for _, f := range v.Files {
			r.Paths = append(r.Paths, f.Path)
		}
		out = append(out, r)
	}
	return out
}

// ReadFunc reads a repository-relative slash path.
type ReadFunc func(rel string) ([]byte, error)

// SubmoduleHint is appended to every missing-file error: corpus and
// regression sources are in the repository or pinned submodules, and
// a missing one is a failure, never a skip (#8).
const SubmoduleHint = "run git submodule update --init"

// Missing wraps a read error of a corpus or submodule file.
func Missing(rel string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s is missing (%s)", rel, SubmoduleHint)
	}
	return fmt.Errorf("%s: %w", rel, err)
}

// Sources reads the files of r (design-relative path to text).
func (r Run) Sources(read ReadFunc) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, p := range r.Paths {
		rel := path.Join(r.Design.DirOf(), p)
		b, err := read(rel)
		if err != nil {
			return nil, Missing(rel, err)
		}
		out[p] = b
	}
	if c := r.Design.Config; c != "" {
		rel := path.Join(r.Design.DirOf(), c)
		b, err := read(rel)
		if err != nil {
			return nil, Missing(rel, err)
		}
		out[configKey] = b
	}
	return out, nil
}

// configKey holds the design config among the sources; no VHDL path
// can start with a NUL byte.
const configKey = "\x00config"

// isVHDL reports whether a sources key is a VHDL file.
func isVHDL(p string) bool { return !strings.HasPrefix(p, "\x00") }

// FieldHash is a corpus.yaml hash field over golden files (name to
// bytes): sha256 over the files in name order, each as its name, a
// newline, its decimal length, a newline and its bytes.
func FieldHash(files map[string][]byte) string {
	h := sha256.New()
	for _, n := range slices.Sorted(maps.Keys(files)) {
		fmt.Fprintf(h, "%s\n%d\n", n, len(files[n]))
		h.Write(files[n])
	}
	return hex.EncodeToString(h.Sum(nil))
}

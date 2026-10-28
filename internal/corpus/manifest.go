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
	Vocabulary Vocabulary   `yaml:"vocabulary"`
	Designs    []Design     `yaml:"designs"`
	Regression []Regression `yaml:"regression"`
}

// Vocabulary holds the fixed feature and invariant ids a design may
// name.
type Vocabulary struct {
	Features   []struct{ ID string } `yaml:"features"`
	Invariants []struct{ ID string } `yaml:"invariants"`
}

// Design is one corpus design entry. Inputs are the checked-in data
// files the design reads while it runs (textio), design-relative: the
// project files of the run.
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
	Inputs          []string          `yaml:"inputs"`
	Expected        Expected          `yaml:"expected"`
	Features        []string          `yaml:"features"`
	Invariants      []string          `yaml:"invariants"`
	Oracle          string            `yaml:"oracle"`
	HierarchyOracle string            `yaml:"hierarchy_oracle"`
	GHDL            Tool              `yaml:"ghdl"`
	NVC             Tool              `yaml:"nvc"`
	Variants        []Variant         `yaml:"variants"`
	TraceSHA256     string            `yaml:"trace_sha256"`
	CoverageSHA256  string            `yaml:"coverage_sha256"`
	OracleSHA256    string            `yaml:"oracle_sha256"`

	// RetiredV10 is the v1.0 outcome key expected_v1_0, retired with
	// the v1.1 release (#18, #27): a manifest that still has it is an
	// error, since nothing would check it.
	RetiredV10 *yaml.Node `yaml:"expected_v1_0"`
}

// File is one source file of a design or variant.
type File struct {
	Path     string `yaml:"path"`
	Standard string `yaml:"standard"`
}

// Expected is an expected outcome.
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
// Defines are the run's --define NAME=VALUE arguments: they override
// the design config's defines: as on the command line.
type Variant struct {
	Name     string            `yaml:"name"`
	Standard string            `yaml:"standard"`
	Defines  map[string]string `yaml:"defines"`
	Files    []File            `yaml:"files"`
	Expected Expected          `yaml:"expected"`
	GHDL     Tool              `yaml:"ghdl"`
	NVC      Tool              `yaml:"nvc"`
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
		if !Reached(d.Milestone) {
			return nil, fmt.Errorf("%s: %s: milestone %q is not a "+
				"milestone at or before the release %s", ManifestPath,
				d.Name, d.Milestone, Release)
		}
		if d.RetiredV10 != nil {
			return nil, fmt.Errorf("%s: %s: expected_v1_0 is retired "+
				"since the %s release; the design runs against "+
				"expected", ManifestPath, d.Name, Release)
		}
	}
	if err := m.checkVocabulary(); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}
	return &m, nil
}

// checkVocabulary requires every feature and invariant a design names
// to be in the vocabulary, and every vocabulary feature to be named by
// at least one design.
func (m *Manifest) checkVocabulary() error {
	ids := func(xs []struct{ ID string }) map[string]bool {
		out := map[string]bool{}
		for _, x := range xs {
			out[x.ID] = true
		}
		return out
	}
	features, invariants := ids(m.Vocabulary.Features),
		ids(m.Vocabulary.Invariants)
	used := map[string]bool{}
	for _, d := range m.Designs {
		for _, f := range d.Features {
			if !features[f] {
				return fmt.Errorf("%s: feature %q is not in "+
					"vocabulary.features", d.Name, f)
			}
			used[f] = true
		}
		for _, i := range d.Invariants {
			if !invariants[i] {
				return fmt.Errorf("%s: invariant %q is not in "+
					"vocabulary.invariants", d.Name, i)
			}
		}
	}
	for _, f := range m.Vocabulary.Features {
		if !used[f.ID] {
			return fmt.Errorf("feature %q is named by no design", f.ID)
		}
	}
	return nil
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
	// Defines are the run's --define arguments (variants only).
	Defines map[string]string
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

// Runs returns the main run and every variant run of d. Every design
// of a parsed manifest is of a milestone the release reached, so each
// runs against its expected outcome.
func (d Design) Runs() []Run {
	main := Run{Design: d, Standard: d.Standard, Expected: d.Expected,
		Paths: d.ModelPaths()}
	for _, f := range d.Files {
		main.Paths = append(main.Paths, f.Path)
	}
	out := []Run{main}
	for _, v := range d.Variants {
		r := Run{Design: d, Variant: v.Name, Standard: v.Standard,
			Defines: v.Defines, Expected: v.Expected}
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
	for _, p := range r.Design.Inputs {
		rel := path.Join(r.Design.DirOf(), p)
		b, err := read(rel)
		if err != nil {
			return nil, Missing(rel, err)
		}
		out[inputKey+p] = b
	}
	return out, nil
}

// configKey holds the design config among the sources, and inputKey
// prefixes each input file; no VHDL path can start with a NUL byte.
const (
	configKey = "\x00config"
	inputKey  = "\x00input/"
)

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

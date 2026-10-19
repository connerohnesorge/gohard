package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// manifestPath is corpus.yaml, relative to the repository root.
const manifestPath = "testdata/corpus/corpus.yaml"

// manifest is the part of corpus.yaml the oracle reads. Unknown keys
// are ignored here; the corpus gate owns the schema.
type manifest struct {
	Designs    []design   `yaml:"designs"`
	Regression []regEntry `yaml:"regression"`
}

type design struct {
	Name     string            `yaml:"name"`
	Standard string            `yaml:"standard"`
	Files    []fileEntry       `yaml:"files"`
	Defines  map[string]string `yaml:"defines"`
	Models   map[string]string `yaml:"models"`
	Config   string            `yaml:"config"`
	Variants []variant         `yaml:"variants"`
	GHDL     toolEntry         `yaml:"ghdl"`
	NVC      toolEntry         `yaml:"nvc"`
}

type fileEntry struct {
	Path     string `yaml:"path"`
	Standard string `yaml:"standard"`
}

type variant struct {
	Name     string      `yaml:"name"`
	Standard string      `yaml:"standard"`
	Files    []fileEntry `yaml:"files"`
}

type toolEntry struct {
	Version string `yaml:"version"`
}

type regEntry struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// corpusDir is the directory of the corpus designs.
const corpusDir = "testdata/corpus"

// dir returns the design directory, repository-relative.
func (d design) dir() string { return path.Join(corpusDir, d.Name) }

// modelPaths returns the stand-in model files in key order.
func (d design) modelPaths() []string {
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

func parseManifest(src []byte) (*manifest, error) {
	var m manifest
	if err := yaml.Unmarshal(src, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, err)
	}
	if len(m.Designs) == 0 {
		return nil, fmt.Errorf("%s: no designs", manifestPath)
	}
	return &m, nil
}

// readRepo reads a repository-relative file.
func readRepo(root, rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
}

// entryRef names one hash field of one corpus.yaml entry.
type entryRef struct {
	List  string // "designs" or "regression"
	Name  string // the entry's name
	Field string // the hash field, for example format_oracle_sha256
	// After is the field the hash line is inserted after when the
	// entry does not have Field yet.
	After string
}

func (r entryRef) String() string {
	return r.List + "[" + r.Name + "]." + r.Field
}

// goldenFile is one recorded file: its name in the golden directory
// and its bytes.
type goldenFile struct {
	Name string
	Data []byte
}

// goldenHash is the hash-field value of a set of golden files: sha256
// over the files in name order, each as name, "\n", decimal length,
// "\n", bytes.
func goldenHash(files []goldenFile) string {
	fs := slices.Clone(files)
	slices.SortFunc(fs, func(a, b goldenFile) int {
		return strings.Compare(a.Name, b.Name)
	})
	h := sha256.New()
	for _, f := range fs {
		fmt.Fprintf(h, "%s\n%d\n", f.Name, len(f.Data))
		h.Write(f.Data)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fieldValue returns the scalar value of ref in src and whether the
// field exists.
func fieldValue(src []byte, ref entryRef) (string, bool, error) {
	_, val, err := locate(src, ref)
	if err != nil || val == nil {
		return "", false, err
	}
	return val.Value, true, nil
}

// setField returns src with ref set to value. Only the bytes of the
// old scalar change; a missing field is added as one line after the
// ref.After field, at that field's indentation. It reports whether the
// text changed.
func setField(src []byte, ref entryRef, value string) ([]byte, bool,
	error) {
	item, val, err := locate(src, ref)
	if err != nil {
		return nil, false, err
	}
	lines := lineStarts(src)
	if val != nil {
		if val.Value == value {
			return src, false, nil
		}
		start, end, err := scalarExtent(src, lines, val)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", ref, err)
		}
		out := slices.Concat(src[:start], []byte(value), src[end:])
		return out, true, nil
	}
	key, anchor := mapGet(item, ref.After)
	if anchor == nil {
		return nil, false, fmt.Errorf("%s: no %s field to insert after",
			ref, ref.After)
	}
	_, end, err := scalarExtent(src, lines, anchor)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", ref, err)
	}
	eol := bytes.IndexByte(src[end:], '\n')
	if eol < 0 {
		return nil, false, fmt.Errorf("%s: %s is on the last line", ref,
			ref.After)
	}
	at := end + eol + 1
	line := strings.Repeat(" ", key.Column-1) + ref.Field + ": " + value +
		"\n"
	return slices.Concat(src[:at], []byte(line), src[at:]), true, nil
}

// locate finds the mapping of ref's entry and the value node of
// ref.Field in it (nil when absent).
func locate(src []byte, ref entryRef) (*yaml.Node, *yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("%s: not a mapping", manifestPath)
	}
	_, list := mapGet(doc.Content[0], ref.List)
	if list == nil || list.Kind != yaml.SequenceNode {
		return nil, nil, fmt.Errorf("%s: no %s list", manifestPath,
			ref.List)
	}
	for _, item := range list.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		if _, n := mapGet(item, "name"); n == nil || n.Value != ref.Name {
			continue
		}
		_, val := mapGet(item, ref.Field)
		if val != nil && val.Kind != yaml.ScalarNode {
			return nil, nil, fmt.Errorf("%s: not a scalar", ref)
		}
		return item, val, nil
	}
	return nil, nil, fmt.Errorf("%s: no entry", ref)
}

// mapGet returns the key and value nodes of key in mapping m.
func mapGet(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

func lineStarts(src []byte) []int {
	out := []int{0}
	for i, b := range src {
		if b == '\n' {
			out = append(out, i+1)
		}
	}
	return out
}

// scalarExtent returns the byte range of a one-line scalar in src: from
// its position to the end of the line, without a trailing comment or
// spaces. The raw text must spell the node's value (plain or quoted).
func scalarExtent(src []byte, lines []int, n *yaml.Node) (int, int,
	error) {
	if n.Line < 1 || n.Line > len(lines) {
		return 0, 0, fmt.Errorf("line %d out of range", n.Line)
	}
	start := lines[n.Line-1] + n.Column - 1
	end := len(src)
	if n.Line < len(lines) {
		end = lines[n.Line] - 1
	}
	if start > end {
		return 0, 0, fmt.Errorf("line %d: bad column", n.Line)
	}
	raw := string(src[start:end])
	if i := strings.Index(raw, " #"); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.TrimRight(raw, " \t\r")
	ok := raw == n.Value
	if !ok && len(raw) >= 2 {
		if u, err := strconv.Unquote(raw); err == nil && u == n.Value {
			ok = true
		}
		if raw[0] == '\'' && raw[len(raw)-1] == '\'' &&
			raw[1:len(raw)-1] == n.Value {
			ok = true
		}
	}
	if !ok {
		return 0, 0, fmt.Errorf("line %d: %q is not a one-line scalar",
			n.Line, raw)
	}
	return start, start + len(raw), nil
}

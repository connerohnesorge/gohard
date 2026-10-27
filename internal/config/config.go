// Package config loads gohard.yaml (#28): it finds the project root,
// decodes the file into a yaml.Node tree, builds typed values from the
// nodes, and reports every problem with the line and column of the
// offending key or value. No VHDL is parsed here.
//
// The key table (Keys) is the single definition of every key: its name,
// type, default, constraints and description. The loader, the editor
// settings merge, the JSON Schema (schema/gohard.schema.json) and
// docs/config.md are all driven by it; `go generate` rewrites the two
// generated files (tools/genconfig) and a test fails when they are stale.
//
// Loading is a pure function of the input bytes (P1): the same bytes give
// the same Config and the same diagnostics in the same order, and it
// never panics. Files are read only through env.FS.
package config

import (
	"slices"
	"strings"
	"time"
)

//go:generate go run ../../tools/genconfig -root ../..

// FileName is the name of the configuration file at the project root.
const FileName = "gohard.yaml"

// Pos is a one-based line and one-based UTF-8 byte column in the file the
// value came from. The zero Pos means the value did not come from a file
// (a default, or a --define on the command line).
type Pos struct {
	Line int
	Col  int
}

// Config is a loaded gohard.yaml. Every field holds its default unless
// the file set it. Map-valued keys are kept as slices sorted by key, so
// iteration order is always the same (P1).
type Config struct {
	// Standard is the VHDL standard: "1993", "2002", "2008" or "2019".
	Standard string
	Lenient  bool
	PSL      PSL
	// Libraries maps library names to source folders, sorted by name.
	Libraries []Library
	// AllowOpaque lists lib.unit names whose encrypted IP is accepted.
	AllowOpaque []string
	// Defines are user conditional-analysis identifiers, sorted by name.
	Defines    []Define
	Format     Format
	Lint       Lint
	Simulation Simulation

	// set records the position of every key the file set validly.
	set map[string]Pos
}

// PSL holds the psl.* keys.
type PSL struct {
	Comments       bool
	StrictKeywords bool
}

// Format holds the format.* keys (#32).
type Format struct {
	Indent        int64
	KeywordCase   string // "lower", "upper" or "preserve"
	AlignPorts    bool
	MaxLineLength int64
}

// Lint holds the lint.* keys (#54): each rule's severity, or "off".
type Lint struct {
	ClockEdgeStyle  string
	Latch           string
	SensitivityList string
	UnusedSignal    string
}

// Simulation holds the simulation.* keys (#35).
type Simulation struct {
	AutoRunOnSave bool
	Testbenches   []string
	Seed          uint64
	TimeBudget    Time
	DeltaBudget   int64
	StepBudget    int64
	// StopLevel is "note", "warning", "error" or "failure".
	StopLevel string
	DumpScope []string
	// Models maps lib.unit names to stand-in files, sorted by unit.
	Models []Model
	Env    SimEnv
}

// SimEnv holds the simulation.env.* keys (validated since v1.0, in
// effect since v1.1, #18).
type SimEnv struct {
	Epoch time.Time // always UTC
	// Vars are the variables GETENV sees, sorted by name.
	Vars []Var
}

// Library is one entry of libraries:.
type Library struct {
	Name    string
	Folders []Folder
	Pos     // of the library name
}

// Folder is one source folder of a library, as written (relative paths
// are relative to the project root).
type Folder struct {
	Path string
	Pos
}

// Define is one user conditional-analysis identifier.
type Define struct {
	Name  string
	Value string
	Pos   // of the name; zero for a --define
}

// Model is one entry of simulation.models.
type Model struct {
	Unit string // lib.unit
	Path string
	Pos
}

// Var is one entry of simulation.env.vars.
type Var struct {
	Name  string
	Value string
}

// Defaults returns the configuration used when gohard.yaml is missing:
// every key at its default.
func Defaults() Config {
	return Config{
		Standard: "2008",
		Format: Format{
			Indent:        2,
			KeywordCase:   "lower",
			AlignPorts:    true,
			MaxLineLength: 80,
		},
		Lint: Lint{
			ClockEdgeStyle:  "off",
			Latch:           "warning",
			SensitivityList: "warning",
			UnusedSignal:    "warning",
		},
		Simulation: Simulation{
			TimeBudget:  Millisecond,
			DeltaBudget: 10000,
			StepBudget:  50000000,
			StopLevel:   "error",
			Env: SimEnv{
				Epoch: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}
}

// IsSet reports whether the source set key (a dotted name from Keys)
// to a valid value.
func (c *Config) IsSet(key string) bool {
	_, ok := c.set[key]
	return ok
}

// Position returns where the source set key, or the zero Pos.
func (c *Config) Position(key string) Pos { return c.set[key] }

// SetKeys returns the names of the keys the source set, in table order.
func (c *Config) SetKeys() []string {
	var out []string
	for _, k := range keys {
		if c.IsSet(k.Name) {
			out = append(out, k.Name)
		}
	}
	return out
}

func (c *Config) mark(key string, p Pos) {
	if c.set == nil {
		c.set = map[string]Pos{}
	}
	c.set[key] = p
}

// Define returns the value of the user identifier name, compared
// case-insensitively like every VHDL identifier.
func (c *Config) Define(name string) (string, bool) {
	for _, d := range c.Defines {
		if strings.EqualFold(d.Name, name) {
			return d.Value, true
		}
	}
	return "", false
}

// Library returns the library called name (case-insensitive).
func (c *Config) Library(name string) (Library, bool) {
	for _, l := range c.Libraries {
		if strings.EqualFold(l.Name, name) {
			return l, true
		}
	}
	return Library{}, false
}

// Builtins are the built-in conditional-analysis identifiers (#18, #31).
// Defining one in defines: or with --define is a setup error (exit 2).
var Builtins = []string{
	"TOOL_EDITION", "TOOL_NAME", "TOOL_TYPE", "TOOL_VENDOR",
	"TOOL_VERSION", "VHDL_VERSION",
}

// IsBuiltin reports whether name is a built-in conditional-analysis
// identifier (case-insensitive).
func IsBuiltin(name string) bool {
	return slices.Contains(Builtins, strings.ToUpper(name))
}

// identLess orders identifiers case-insensitively, then by spelling.
func identLess(a, b string) int {
	if c := strings.Compare(strings.ToLower(a), strings.ToLower(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

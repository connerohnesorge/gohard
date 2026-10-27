package config

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/connerohnesorge/gohard/internal/diag"
)

// Type is the kind of value a key holds.
type Type uint8

// Key types.
const (
	TypeBool      Type = iota + 1
	TypeInt            // decimal integer >= Min
	TypeUint64         // decimal integer in [0, 2^64-1]
	TypeEnum           // one of Enum
	TypeTime           // VHDL time string such as "1 ms"
	TypeTimestamp      // RFC 3339 UTC time
	TypeList           // list of Items
	TypeMap            // mapping from MapKeys to Items
)

// Item is what a list element, map key or map value must be.
type Item uint8

// Items.
const (
	ItemString     Item = iota + 1 // any string
	ItemNonEmpty                   // a non-empty string
	ItemIdentifier                 // a VHDL basic identifier
	ItemUnit                       // lib.unit
	ItemFolders                    // a list of non-empty folder or file paths
	ItemVarName                    // non-empty, without '=' or NUL
	ItemDefine                     // an identifier that is not built in
)

// Key is one row of the key table: the single definition of a
// gohard.yaml key, from which the loader, the JSON Schema and
// docs/config.md are built.
type Key struct {
	// Name is the dotted key, such as "format.indent".
	Name string
	Type Type
	// Min is the smallest allowed value of a TypeInt key.
	Min int64
	// Enum lists the allowed values of a TypeEnum key.
	Enum []string
	// Items are the list elements or map values; MapKeys the map keys.
	Items   Item
	MapKeys Item
	// Default is the default value written as YAML.
	Default string
	// TypeDoc is the type as shown in docs/config.md.
	TypeDoc string
	// Doc is the description (Markdown, one paragraph).
	Doc string

	acc access
}

// access connects a key to its Config field.
type access struct {
	decode    func(p *parser, key string, n *yaml.Node, c *Config) bool
	isDefault func(c *Config) bool
	copy      func(dst, src *Config)
	get       func(c *Config) any
}

func bind[T any](f func(*Config) *T, eq func(a, b T) bool,
	dec func(p *parser, key string, n *yaml.Node) (T, bool),
) access {
	defaults := Defaults()
	def := *f(&defaults)
	return access{
		decode: func(p *parser, key string, n *yaml.Node,
			c *Config,
		) bool {
			v, ok := dec(p, key, n)
			if ok {
				*f(c) = v
			}
			return ok
		},
		isDefault: func(c *Config) bool { return eq(*f(c), def) },
		copy:      func(dst, src *Config) { *f(dst) = *f(src) },
		get:       func(c *Config) any { return *f(c) },
	}
}

func same[T comparable](a, b T) bool { return a == b }

func empty[T any](a, _ []T) bool { return len(a) == 0 }

// Value returns the value of key in c (a bool, int64, uint64, string,
// Time, time.Time, []string, []Library, []Define, []Model or []Var), or
// nil for an unknown key.
func (c *Config) Value(key string) any {
	k, ok := lookupKey(key)
	if !ok {
		return nil
	}
	return k.acc.get(c)
}

// Keys returns a copy of the key table in documentation order.
func Keys() []Key { return slices.Clone(keys) }

// LookupKey returns the key with the dotted name.
func LookupKey(name string) (Key, bool) { return lookupKey(name) }

func lookupKey(name string) (Key, bool) {
	for _, k := range keys {
		if k.Name == name {
			return k, true
		}
	}
	return Key{}, false
}

// isGroup reports whether name is a key group such as "simulation.env".
func isGroup(name string) bool {
	for _, k := range keys {
		if strings.HasPrefix(k.Name, name+".") {
			return true
		}
	}
	return false
}

// Groups returns every key group (a dotted prefix of a key) in table
// order, such as "psl", "format", "simulation" and "simulation.env".
func Groups() []string {
	var out []string
	for _, k := range keys {
		parts := strings.Split(k.Name, ".")
		for i := 1; i < len(parts); i++ {
			g := strings.Join(parts[:i], ".")
			if !slices.Contains(out, g) {
				out = append(out, g)
			}
		}
	}
	return out
}

// lintLevels are the values of a lint.* key: lint findings never fail
// a run except under --strict (#20).
var lintLevels = []string{"off", "hint", "warning"}

var standards = []string{"1993", "2002", "2008", "2019"}

func boolKey(name, def, doc string, f func(*Config) *bool) Key {
	return Key{
		Name: name, Type: TypeBool, Default: def, TypeDoc: "bool",
		Doc: doc,
		acc: bind(f, same[bool], (*parser).boolean),
	}
}

func intKey(name string, min int64, def, doc string,
	f func(*Config) *int64,
) Key {
	bound := "at least " + strconv.FormatInt(min, 10)
	return Key{
		Name: name, Type: TypeInt, Min: min, Default: def,
		TypeDoc: "int >= " + strconv.FormatInt(min, 10), Doc: doc,
		acc: bind(f, same[int64], func(p *parser, key string,
			n *yaml.Node,
		) (int64, bool) {
			v, ok := p.integer(key, n, uint64(min), math.MaxInt64,
				bound)
			return int64(v), ok
		}),
	}
}

func enumKey(name string, enum []string, def, doc string,
	f func(*Config) *string,
) Key {
	shown := slices.Clone(enum)
	if strings.HasPrefix(def, `"`) {
		for i, e := range shown {
			shown[i] = strconv.Quote(e)
		}
	}
	return Key{
		Name: name, Type: TypeEnum, Enum: enum, Default: def,
		TypeDoc: strings.Join(shown, "|"), Doc: doc,
		acc: bind(f, same[string], func(p *parser, key string,
			n *yaml.Node,
		) (string, bool) {
			return p.enum(key, enum, n)
		}),
	}
}

func listKey(name string, item Item, typeDoc, doc string,
	f func(*Config) *[]string,
) Key {
	return Key{
		Name: name, Type: TypeList, Items: item, Default: "[]",
		TypeDoc: typeDoc, Doc: doc,
		acc: bind(f, empty[string], func(p *parser, key string,
			n *yaml.Node,
		) ([]string, bool) {
			out := []string{}
			ok := p.list(key, n, func(key string, e *yaml.Node) bool {
				s, ok := p.item(key, item, e)
				out = append(out, s)
				return ok
			})
			return out, ok
		}),
	}
}

// item decodes one string-valued item.
func (p *parser) item(key string, item Item, n *yaml.Node) (string,
	bool,
) {
	switch item {
	case ItemNonEmpty:
		return p.checked(key, "a non-empty string", n, nonEmpty)
	case ItemIdentifier:
		return p.checked(key, "a VHDL identifier", n, IsIdentifier)
	case ItemUnit:
		return p.checked(key, "a lib.unit name", n, IsUnitName)
	}
	return p.str(key, n)
}

func nonEmpty(s string) bool { return s != "" }

// IsVarName reports whether s is a valid simulation.env.vars (and
// --env) name: non-empty, without '=' or NUL.
func IsVarName(s string) bool { return isVarName(s) }

func isVarName(s string) bool {
	return s != "" && !strings.ContainsAny(s, "=\x00")
}

// mapKeyWant is the description of a map key kind for messages.
func mapKeyWant(it Item) (string, func(string) bool) {
	switch it {
	case ItemUnit:
		return "a lib.unit name", IsUnitName
	case ItemVarName:
		return "a variable name without '='", isVarName
	}
	return "a VHDL identifier", IsIdentifier
}

var keys = []Key{
	enumKey("standard", standards, `"2008"`,
		"The VHDL standard files are analyzed under. It must be a "+
			"quoted string: `standard: \"2008\"`.",
		func(c *Config) *string { return &c.Standard }),
	boolKey("lenient", "false",
		"Relax analysis rules that other VHDL tools commonly "+
			"relax. Under `lenient: true`: from VHDL-2002 on a shared "+
			"variable need not be of a protected type, reported as a "+
			"`sema/relaxed` warning (GHDL `-frelaxed`, NVC "+
			"`--relaxed`); a subprogram body may add or omit "+
			"`constant` on a parameter that names no mode (GHDL "+
			"`-frelaxed`, NVC `--relaxed`); a subelement of a formal "+
			"associated individually may be associated with `open` "+
			"(GHDL `-frelaxed`, NVC `--relaxed`); a pure function may "+
			"declare a file (GHDL `-frelaxed`, NVC `--relaxed`) and "+
			"name an external signal or variable (GHDL `-frelaxed`); "+
			"an `others` aggregate assigned to an unconstrained port, "+
			"parameter or alias takes the target's index range (GHDL "+
			"`-frelaxed`, NVC `--relaxed`); a name used in its own "+
			"declaration denotes the outer homograph (NVC "+
			"`--relaxed`; GHDL accepts it with a `-Whide` warning in "+
			"every mode); an alias of a subprogram or enumeration "+
			"literal may omit the signature and denotes the first "+
			"visible one (NVC in every mode; GHDL always requires the "+
			"signature); a component instantiation may name an entity "+
			"without the reserved word `entity` (neither GHDL nor NVC "+
			"accepts this). Only the shared variable rule is reported; "+
			"the others are accepted silently.",
		func(c *Config) *bool { return &c.Lenient }),
	boolKey("psl.comments", "false",
		"Parse `-- psl` comment lines as PSL.",
		func(c *Config) *bool { return &c.PSL.Comments }),
	boolKey("psl.strict_keywords", "false",
		"Report PSL-only words used as identifiers as errors "+
			"(GHDL-style).",
		func(c *Config) *bool { return &c.PSL.StrictKeywords }),
	{
		Name: "libraries", Type: TypeMap, MapKeys: ItemIdentifier,
		Items: ItemFolders, Default: "{}",
		TypeDoc: "map: library name to list of folders or files",
		Doc: "Maps each library name to the folders holding its " +
			"sources, relative to the project root. An entry may " +
			"also name one `.vhd` or `.vhdl` file, as `gohard " +
			"import` writes them; a file entry wins over a folder " +
			"holding the file. Files in no listed folder go to " +
			"`work`. A missing or unreadable folder or file is a " +
			"setup error (exit 2).",
		acc: bind(func(c *Config) *[]Library { return &c.Libraries },
			empty[Library], (*parser).libraries),
	},
	listKey("allow_opaque", ItemUnit, "list of `lib.unit`",
		"Encrypted units that may stay opaque. It downgrades "+
			"encrypted IP only; unsupported constructs stay exit 2. "+
			"`gohard libs detect --write` fills it in.",
		func(c *Config) *[]string { return &c.AllowOpaque }),
	{
		Name: "defines", Type: TypeMap, MapKeys: ItemDefine,
		Items: ItemString, Default: "{}",
		TypeDoc: "map: identifier to string",
		Doc: "User conditional-analysis identifiers. `--define " +
			"NAME=VALUE` overrides an entry with the same name. " +
			"Defining a built-in identifier (`VHDL_VERSION`, " +
			"`TOOL_TYPE`, `TOOL_VENDOR`, `TOOL_NAME`, " +
			"`TOOL_EDITION`, `TOOL_VERSION`) is a setup error " +
			"(exit 2).",
		acc: bind(func(c *Config) *[]Define { return &c.Defines },
			empty[Define], (*parser).defines),
	},
	intKey("format.indent", 1, "2",
		"Spaces per indentation level.",
		func(c *Config) *int64 { return &c.Format.Indent }),
	enumKey("format.keyword_case",
		[]string{"lower", "upper", "preserve"}, "lower",
		"Case of reserved words.",
		func(c *Config) *string { return &c.Format.KeywordCase }),
	boolKey("format.align_ports", "true",
		"Align port, generic and association lists.",
		func(c *Config) *bool { return &c.Format.AlignPorts }),
	intKey("format.max_line_length", 40, "80",
		"Lines longer than this are wrapped.",
		func(c *Config) *int64 { return &c.Format.MaxLineLength }),
	enumKey("lint.clock_edge_style", lintLevels, "off",
		"A clk'event edge test in a design that also uses rising_edge "+
			"or falling_edge (`lint/clock-edge-style`).",
		func(c *Config) *string { return &c.Lint.ClockEdgeStyle }),
	enumKey("lint.latch", lintLevels, "warning",
		"A combinational process does not assign a signal on every "+
			"path, so it infers a latch (`lint/latch`).",
		func(c *Config) *string { return &c.Lint.Latch }),
	enumKey("lint.sensitivity_list", lintLevels, "warning",
		"A combinational process reads a signal its sensitivity list "+
			"does not name (`lint/sensitivity-list`).",
		func(c *Config) *string { return &c.Lint.SensitivityList }),
	enumKey("lint.unused_signal", lintLevels, "warning",
		"A signal never read or never assigned, an in port never read, "+
			"an out port never assigned (`lint/unused-signal`).",
		func(c *Config) *string { return &c.Lint.UnusedSignal }),
	boolKey("inlay_hints.parameter_names", "false",
		"Show the formal name before each positional actual of a "+
			"subprogram call, generic map or port map.",
		func(c *Config) *bool { return &c.InlayHints.ParameterNames }),
	boolKey("inlay_hints.types", "false",
		"Show the type of an object alias declared without a subtype "+
			"indication and of a loop parameter.",
		func(c *Config) *bool { return &c.InlayHints.Types }),
	boolKey("inlay_hints.values", "false",
		"Show the static value of a constant or a generic default when "+
			"static evaluation succeeds within the editor's limit of "+
			"1,000,000 steps.",
		func(c *Config) *bool { return &c.InlayHints.Values }),
	boolKey("simulation.auto_run_on_save", "false",
		"When on, testbench candidates whose dependencies changed "+
			"run after a short debounce on save. The code lens and "+
			"the CLI always run.",
		func(c *Config) *bool { return &c.Simulation.AutoRunOnSave }),
	listKey("simulation.testbenches", ItemIdentifier,
		"list of entity names",
		"Entities to treat as testbenches, in addition to every "+
			"entity without ports.",
		func(c *Config) *[]string { return &c.Simulation.Testbenches }),
	{
		Name: "simulation.seed", Type: TypeUint64, Default: "0",
		TypeDoc: "uint64",
		Doc: "The simulation seed: it fixes the in-delta process " +
			"order. 0 keeps declaration order.",
		acc: bind(func(c *Config) *uint64 { return &c.Simulation.Seed },
			same[uint64], func(p *parser, key string,
				n *yaml.Node,
			) (uint64, bool) {
				return p.integer(key, n, 0, math.MaxUint64,
					"between 0 and "+
						strconv.FormatUint(math.MaxUint64, 10))
			}),
	},
	{
		Name: "simulation.time_budget", Type: TypeTime,
		Default: `"1 ms"`, TypeDoc: "VHDL time string",
		Doc: "Simulated time after which a run stops with " +
			"`sim/budget-exceeded`.",
		acc: bind(func(c *Config) *Time {
			return &c.Simulation.TimeBudget
		}, same[Time], (*parser).vhdlTime),
	},
	intKey("simulation.delta_budget", 1, "10000",
		"Delta cycles allowed per time step.",
		func(c *Config) *int64 { return &c.Simulation.DeltaBudget }),
	intKey("simulation.step_budget", 1, "50000000",
		"Kernel steps allowed per run.",
		func(c *Config) *int64 { return &c.Simulation.StepBudget }),
	enumKey("simulation.stop_level",
		[]string{"note", "warning", "error", "failure"}, "error",
		"The assertion severity that stops a run. It does not "+
			"change severities or exit codes.",
		func(c *Config) *string { return &c.Simulation.StopLevel }),
	listKey("simulation.dump_scope", ItemNonEmpty,
		"list of instance-path prefixes",
		"Only signals under these instance paths are traced. Empty "+
			"traces every signal. Traces always go to "+
			"`.gohard/traces/`.",
		func(c *Config) *[]string { return &c.Simulation.DumpScope }),
	{
		Name: "simulation.models", Type: TypeMap, MapKeys: ItemUnit,
		Items: ItemNonEmpty, Default: "{}",
		TypeDoc: "map: `lib.unit` to stand-in file path",
		Doc: "Maps an opaque unit to the user's own plain VHDL " +
			"model. GoHard checks that the stand-in's ports match " +
			"the real component's.",
		acc: bind(func(c *Config) *[]Model {
			return &c.Simulation.Models
		}, empty[Model], (*parser).models),
	},
	{
		Name: "simulation.env.epoch", Type: TypeTimestamp,
		Default: `"2000-01-01T00:00:00Z"`,
		TypeDoc: "RFC 3339 UTC time",
		Doc: "The wall-clock time of simulated time 0: `EPOCH`, " +
			"`GMTIME` and `LOCALTIME` report it plus the simulated " +
			"time, in UTC, unless `--real-env` is given (in " +
			"effect since v1.1).",
		acc: bind(func(c *Config) *time.Time {
			return &c.Simulation.Env.Epoch
		}, time.Time.Equal, (*parser).timestamp),
	},
	{
		Name: "simulation.env.vars", Type: TypeMap,
		MapKeys: ItemVarName, Items: ItemString, Default: "{}",
		TypeDoc: "map: name to string",
		Doc: "The only variables `GETENV` sees (in effect since " +
			"v1.1); `--env " +
			"NAME=VALUE` overrides an entry. `--real-env` reads the " +
			"real environment instead.",
		acc: bind(func(c *Config) *[]Var { return &c.Simulation.Env.Vars },
			empty[Var], (*parser).vars),
	},
}

func (p *parser) timestamp(key string, n *yaml.Node) (time.Time, bool) {
	if !p.scalar(key, "an RFC 3339 UTC time", n) {
		return time.Time{}, false
	}
	t, ok := ParseTimestamp(n.Value)
	if !ok {
		p.report(diag.New(diag.ConfigBadTimestamp, key, display(n)), n)
	}
	return t, ok
}

func (p *parser) libraries(key string, n *yaml.Node) ([]Library, bool) {
	out := []Library{}
	want, valid := mapKeyWant(ItemIdentifier)
	ok := p.entries(key, n, want, valid, true,
		func(k string, kp Pos, vn *yaml.Node) bool {
			lib := Library{Name: k, Pos: kp, Folders: []Folder{}}
			ok := p.list(key+"."+k, vn, func(key string,
				e *yaml.Node,
			) bool {
				s, ok := p.item(key, ItemNonEmpty, e)
				lib.Folders = append(lib.Folders,
					Folder{Path: s, Pos: p.pos(e)})
				return ok
			})
			out = append(out, lib)
			return ok
		})
	slices.SortStableFunc(out, func(a, b Library) int {
		return identLess(a.Name, b.Name)
	})
	return out, ok
}

func (p *parser) defines(key string, n *yaml.Node) ([]Define, bool) {
	out := []Define{}
	want, valid := mapKeyWant(ItemIdentifier)
	ok := p.entries(key, n, want, valid, true,
		func(k string, kp Pos, vn *yaml.Node) bool {
			if IsBuiltin(k) {
				p.reportAt(diag.New(diag.ConfigBuiltinDefine, k), kp)
				return false
			}
			v, ok := p.str(key+"."+k, vn)
			out = append(out, Define{Name: k, Value: v, Pos: kp})
			return ok
		})
	slices.SortStableFunc(out, func(a, b Define) int {
		return identLess(a.Name, b.Name)
	})
	return out, ok
}

func (p *parser) models(key string, n *yaml.Node) ([]Model, bool) {
	out := []Model{}
	want, valid := mapKeyWant(ItemUnit)
	ok := p.entries(key, n, want, valid, true,
		func(k string, kp Pos, vn *yaml.Node) bool {
			v, ok := p.item(key+"."+k, ItemNonEmpty, vn)
			out = append(out, Model{Unit: k, Path: v, Pos: kp})
			return ok
		})
	slices.SortStableFunc(out, func(a, b Model) int {
		return identLess(a.Unit, b.Unit)
	})
	return out, ok
}

func (p *parser) vars(key string, n *yaml.Node) ([]Var, bool) {
	out := []Var{}
	want, valid := mapKeyWant(ItemVarName)
	ok := p.entries(key, n, want, valid, false,
		func(k string, _ Pos, vn *yaml.Node) bool {
			v, ok := p.str(key+"."+k, vn)
			out = append(out, Var{Name: k, Value: v})
			return ok
		})
	slices.SortStableFunc(out, func(a, b Var) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out, ok
}

const inlayHintsDoc = "Inlay hints in the language server (#56), each " +
	"kind off by default. Editors can set them through " +
	"`initializationOptions` or `workspace/didChangeConfiguration`."

// groupDocs describes each key group, in Groups order.
var groupDocs = map[string]string{
	"psl":            "PSL parsing.",
	"format":         "The formatter (`gohard fmt`, #32).",
	"lint":           "Lint rules (#54): `off`, `hint` or `warning` each.",
	"inlay_hints":    inlayHintsDoc,
	"simulation":     "Elaboration and simulation (#35).",
	"simulation.env": "The deterministic `std.env` environment (v1.1).",
}

// GroupDoc returns the description of a key group.
func GroupDoc(group string) string { return groupDocs[group] }

package diag

import (
	"slices"
	"strings"
)

// Info is one row of the diagnostic table.
type Info struct {
	Code     Code
	Severity Severity // default severity
	Class    Class
	// Owner is the GitHub issue whose spec defines the code.
	Owner int
	// Summary is a one-line description (SARIF rule text, docs).
	Summary string
	// Message is the template. {name} is a parameter; Format fills the
	// parameters in order of first appearance.
	Message string
	// Variants are alternative templates with the same parameters,
	// chosen by name with NewVariant.
	Variants []Variant
}

// Variant is a named alternative message template.
type Variant struct {
	Name    string
	Message string
}

// Unsupported-construct message variants (#20).
const (
	// PlannedV11 is for constructs planned for milestone v1.1 (#18, #27).
	PlannedV11 = "v1.1"
	// PleaseReport is for every other unimplemented construct.
	PleaseReport = "report"
)

// Diagnostic codes. Keep each group sorted; the table below is the source
// of truth for severity, class and wording.
const (
	// cli: the command line itself (#20).
	CLIInterrupted    Code = "cli/interrupted"
	CLIMissingPath    Code = "cli/missing-path"
	CLINoFiles        Code = "cli/no-files"
	CLINotImplemented Code = "cli/not-implemented"
	CLIOutputFailed   Code = "cli/output-failed"
	CLIUsage          Code = "cli/usage"

	// config: gohard.yaml (#28).
	ConfigBadBool          Code = "config/bad-bool"
	ConfigBadTime          Code = "config/bad-time"
	ConfigBadTimestamp     Code = "config/bad-timestamp"
	ConfigBuiltinDefine    Code = "config/builtin-define"
	ConfigDuplicateKey     Code = "config/duplicate-key"
	ConfigEnum             Code = "config/enum"
	ConfigRange            Code = "config/range"
	ConfigRedundantDefault Code = "config/redundant-default"
	ConfigSyntax           Code = "config/syntax"
	ConfigType             Code = "config/type"
	ConfigUnknownKey       Code = "config/unknown-key"
	ConfigUnquotedBool     Code = "config/unquoted-bool"
	ConfigUnquotedStandard Code = "config/unquoted-standard"
	ConfigUnreadable       Code = "config/unreadable"

	// workspace: file discovery and limits (#20, #31).
	WorkspaceFileTooLarge Code = "workspace/file-too-large"
	WorkspaceSymlinkLoop  Code = "workspace/symlink-loop"
	WorkspaceUnreadable   Code = "workspace/unreadable"

	// syntax: lexer, parser, conditional analysis (#31).
	SyntaxDirectiveError     Code = "syntax/directive-error"
	SyntaxDirectiveWarning   Code = "syntax/directive-warning"
	SyntaxError              Code = "syntax/error"
	SyntaxInactive           Code = "syntax/inactive"
	SyntaxPSLIdentifier      Code = "syntax/psl-identifier"
	SyntaxPSLKeyword         Code = "syntax/psl-keyword"
	SyntaxStandard           Code = "syntax/standard"
	SyntaxUndefinedCondition Code = "syntax/undefined-condition"
	SyntaxUTF8Comment        Code = "syntax/utf8-comment"

	// library: library mapping and units (#20, #34).
	LibraryDuplicateUnit Code = "library/duplicate-unit"
	LibraryFolderMissing Code = "library/folder-missing"
	LibraryImportCycle   Code = "library/import-cycle"
	LibraryMissingUnit   Code = "library/missing-unit"
	LibraryUnmapped      Code = "library/unmapped"

	// stdlib: bundled std/ieee (#33).
	StdlibIntegrity Code = "stdlib/integrity"
	StdlibInternal  Code = "stdlib/internal-error"

	// sema: names, types, overloads, static evaluation (#34).
	SemaAmbiguousOverload Code = "sema/ambiguous-overload"
	SemaEncryptedUnit     Code = "sema/encrypted-unit"
	SemaError             Code = "sema/error"
	SemaEvalLimit         Code = "sema/eval-limit"
	SemaNoOverload        Code = "sema/no-overload"
	SemaOpaqueAllowed     Code = "sema/opaque-allowed"
	SemaRelaxed           Code = "sema/relaxed"
	SemaStaticError       Code = "sema/static-error"
	SemaTypeMismatch      Code = "sema/type-mismatch"
	SemaUnchecked         Code = "sema/unchecked-unit"
	SemaUndefined         Code = "sema/undefined"

	// unsupported: legal VHDL GoHard does not implement (#20).
	UnsupportedConstruct Code = "unsupported/construct"

	// format: gohard fmt (#32).
	FormatNotParsed   Code = "format/not-parsed"
	FormatWouldChange Code = "format/would-change"
	FormatWriteFailed Code = "format/write-failed"

	// sim: elaboration and simulation (#35, #29).
	SimAssertError      Code = "sim/assert-error"
	SimAssertNote       Code = "sim/assert-note"
	SimAssertWarning    Code = "sim/assert-warning"
	SimBudgetExceeded   Code = "sim/budget-exceeded"
	SimCoverHit         Code = "sim/cover-hit"
	SimCoverMiss        Code = "sim/cover-miss"
	SimGlitch           Code = "sim/glitch"
	SimModelPorts       Code = "sim/model-ports"
	SimNeedsFormal      Code = "sim/needs-formal"
	SimRace             Code = "sim/race"
	SimRuntimeError     Code = "sim/runtime-error"
	SimStopStatus       Code = "sim/stop-status"
	SimUnknownTestbench Code = "sim/unknown-testbench"

	// repro: --repro replay (#29).
	ReproInvalid         Code = "repro/invalid"
	ReproSourceChanged   Code = "repro/source-changed"
	ReproVersionMismatch Code = "repro/version-mismatch"

	// trace: trace files and exports (#36).
	TraceInvalid       Code = "trace/invalid"
	TraceUnknownFormat Code = "trace/unknown-format"

	// lsp: `gohard lsp` actions and replay (#26).
	LSPReplayMismatch Code = "lsp/replay-mismatch"

	// libs: `gohard libs detect` (#1).
	LibsUnreadable Code = "libs/unreadable"

	// internal: GoHard's own failures (#20).
	InternalCrash       Code = "internal/crash"
	InternalUnknownCode Code = "internal/unknown-code"
)

// table is every diagnostic code GoHard can emit, sorted by code.
var table = []Info{
	{
		Code: CLIInterrupted, Severity: Error, Class: Tool, Owner: 20,
		Summary: "the run was interrupted by a signal",
		Message: "interrupted by {signal}",
	},
	{
		Code: CLIMissingPath, Severity: Error, Class: Tool, Owner: 20,
		Summary: "a path on the command line does not exist",
		Message: "{path} does not exist",
	},
	{
		Code: CLINoFiles, Severity: Error, Class: Tool, Owner: 20,
		Summary: "no VHDL files were found",
		Message: "no VHDL files found under {path}; " +
			"pass --allow-empty to accept an empty project",
	},
	{
		Code: CLINotImplemented, Severity: Error, Class: Tool,
		Owner:   20,
		Summary: "the command is not built into this binary yet",
		Message: "gohard {command} is not implemented in this build",
	},
	{
		Code: CLIOutputFailed, Severity: Error, Class: Tool, Owner: 20,
		Summary: "the --output file could not be written",
		Message: "cannot write {path}: {reason}",
	},
	{
		Code: CLIUsage, Severity: Error, Class: Tool, Owner: 20,
		Summary: "bad command-line usage",
		Message: "{problem}; run 'gohard help' for usage",
	},
	{
		Code: ConfigBadBool, Severity: Error, Class: Tool, Owner: 28,
		Summary: "a bool key holds something other than true or false",
		Message: "{key} must be true or false, not {value}",
	},
	{
		Code: ConfigBadTime, Severity: Error, Class: Tool, Owner: 28,
		Summary: "a malformed VHDL time string",
		Message: "{key}: {value} is not a VHDL time such as \"1 ms\"",
	},
	{
		Code: ConfigBadTimestamp, Severity: Error, Class: Tool,
		Owner:   28,
		Summary: "a malformed RFC 3339 UTC time",
		Message: "{key}: {value} is not an RFC 3339 UTC time such as " +
			"\"2000-01-01T00:00:00Z\"",
	},
	{
		Code: ConfigBuiltinDefine, Severity: Error, Class: Tool,
		Owner:   28,
		Summary: "a built-in conditional-analysis identifier is defined",
		Message: "{name} is a built-in conditional-analysis " +
			"identifier and cannot be defined",
	},
	{
		Code: ConfigDuplicateKey, Severity: Error, Class: Tool,
		Owner:   28,
		Summary: "a key appears twice in one mapping",
		Message: "duplicate key {key} (first set on line {line})",
	},
	{
		Code: ConfigEnum, Severity: Error, Class: Tool, Owner: 28,
		Summary: "a value outside the key's allowed set",
		Message: "{key} must be one of {allowed}, not {value}",
	},
	{
		Code: ConfigRange, Severity: Error, Class: Tool, Owner: 28,
		Summary: "a number outside the key's range",
		Message: "{key} must be {bound}, not {value}",
	},
	{
		Code: ConfigRedundantDefault, Severity: Warning, Class: Design,
		Owner:   28,
		Summary: "a key is set to its default value",
		Message: "{key} is set to its default value {value}",
	},
	{
		Code: ConfigSyntax, Severity: Error, Class: Tool, Owner: 28,
		Summary: "gohard.yaml is not valid YAML",
		Message: "invalid YAML: {reason}",
	},
	{
		Code: ConfigType, Severity: Error, Class: Tool, Owner: 28,
		Summary: "a value of the wrong type",
		Message: "{key} must be {want}, not {got}",
	},
	{
		Code: ConfigUnknownKey, Severity: Error, Class: Tool,
		Owner:   28,
		Summary: "a key that gohard.yaml does not define",
		Message: "unknown key {key}",
	},
	{
		Code: ConfigUnquotedBool, Severity: Error, Class: Tool,
		Owner:   28,
		Summary: "an unquoted YAML 1.1 boolean in a string key",
		Message: "{key}: {value} reads as a YAML 1.1 boolean; " +
			"quote it: \"{value}\"",
	},
	{
		Code: ConfigUnquotedStandard, Severity: Error, Class: Tool,
		Owner:   28,
		Summary: "standard is an unquoted number",
		Message: "standard must be a quoted string: " +
			"standard: \"{value}\"",
	},
	{
		Code: ConfigUnreadable, Severity: Error, Class: Tool,
		Owner:   28,
		Summary: "gohard.yaml exists but cannot be read",
		Message: "cannot read {path}: {reason}",
	},
	{
		Code: FormatNotParsed, Severity: Error, Class: Design,
		Owner:   32,
		Summary: "a file does not parse, so it was not formatted",
		Message: "file does not parse; it was not formatted",
	},
	{
		Code: FormatWouldChange, Severity: Error, Class: Design,
		Owner:   32,
		Summary: "fmt --check: the file is not formatted",
		Message: "file is not formatted; run gohard fmt",
	},
	{
		Code: FormatWriteFailed, Severity: Error, Class: Tool,
		Owner:   32,
		Summary: "the formatted file could not be written",
		Message: "cannot write formatted file: {reason}",
	},
	{
		Code: InternalCrash, Severity: Error, Class: Tool, Owner: 20,
		Summary: "GoHard crashed",
		Message: "internal error: {reason}; " +
			"crash report written to {file}",
	},
	{
		Code: InternalUnknownCode, Severity: Error, Class: Tool,
		Owner:   20,
		Summary: "GoHard emitted a code missing from the table",
		Message: "internal error: unknown diagnostic code {code}",
	},
	{
		Code: LibraryDuplicateUnit, Severity: Error, Class: Design,
		Owner:   34,
		Summary: "two files declare the same unit in one library",
		Message: "unit {unit} is declared twice in library " +
			"{library}: {first} and {second}",
	},
	{
		Code: LibraryFolderMissing, Severity: Error, Class: Tool,
		Owner:   34,
		Summary: "a library folder is missing or unreadable",
		Message: "library {library}: folder {folder} is missing " +
			"or unreadable",
	},
	{
		Code: LibraryImportCycle, Severity: Error, Class: Design,
		Owner:   34,
		Summary: "units depend on each other in a cycle",
		Message: "import cycle: {cycle}",
	},
	{
		Code: LibraryMissingUnit, Severity: Error, Class: Design,
		Owner:   34,
		Summary: "a mapped library lacks the unit",
		Message: "library {library} has no unit {unit} " +
			"(scanned {scanned})",
	},
	{
		Code: LibraryUnmapped, Severity: Error, Class: Design,
		Owner:   34,
		Summary: "a use clause names an unmapped library",
		Message: "library {library} is not mapped; add it under " +
			"libraries: in gohard.yaml",
	},
	{
		Code: LibsUnreadable, Severity: Warning, Class: Design,
		Owner:   1,
		Summary: "a detected library cannot be read as VHDL source",
		Message: "{library} at {path} is {reason} and cannot be read",
	},
	{
		Code: LSPReplayMismatch, Severity: Error, Class: Tool,
		Owner:   26,
		Summary: "a replayed session diverged from its recording",
		Message: "replay differs from the recording at message {n}",
	},
	{
		Code: ReproInvalid, Severity: Error, Class: Tool, Owner: 29,
		Summary: "the repro file cannot be read",
		Message: "invalid repro file {path}: {reason}",
	},
	{
		Code: ReproSourceChanged, Severity: Error, Class: Tool,
		Owner:   29,
		Summary: "sources changed since the repro was recorded",
		Message: "sources changed since the recorded run: {files}",
	},
	{
		Code: ReproVersionMismatch, Severity: Warning, Class: Design,
		Owner:   29,
		Summary: "the repro was recorded by another gohard version",
		Message: "recorded with gohard {recorded}, running " +
			"{current}; rerunning and diffing the trace",
	},
	{
		Code: SemaAmbiguousOverload, Severity: Error, Class: Design,
		Owner:   34,
		Summary: "more than one overload fits",
		Message: "ambiguous {name}: {candidates}",
	},
	{
		Code: SemaEncryptedUnit, Severity: Error, Class: Tool,
		Owner:   34,
		Summary: "encrypted IP makes the unit opaque",
		Message: "{unit} is encrypted IP and cannot be analyzed; " +
			"list it under allow_opaque or model it under " +
			"simulation.models",
	},
	{
		Code: SemaError, Severity: Error, Class: Design, Owner: 34,
		Summary: "a semantic error",
		Message: "{problem}",
	},
	{
		Code: SemaEvalLimit, Severity: Warning, Class: Design,
		Owner:   34,
		Summary: "static evaluation hit the 1,000,000-step limit",
		Message: "{what} could not be computed within 1,000,000 " +
			"evaluation steps",
	},
	{
		Code: SemaNoOverload, Severity: Error, Class: Design,
		Owner:   34,
		Summary: "no overload fits",
		Message: "no {name} matches: {reasons}",
	},
	{
		Code: SemaOpaqueAllowed, Severity: Hint, Class: Design,
		Owner:   34,
		Summary: "encrypted IP allowed by allow_opaque",
		Message: "{unit} is encrypted IP, allowed by allow_opaque",
	},
	{
		Code: SemaRelaxed, Severity: Warning, Class: Design,
		Owner:   28,
		Summary: "code that breaks a rule lenient mode relaxes",
		Message: "{problem} (allowed by lenient)",
	},
	{
		Code: SemaStaticError, Severity: Error, Class: Design,
		Owner:   34,
		Summary: "a static value is out of range or mismatched",
		Message: "{problem} in {instance}",
	},
	{
		Code: SemaTypeMismatch, Severity: Error, Class: Design,
		Owner:   34,
		Summary: "an expression has the wrong type",
		Message: "expected {want}, found {got}",
	},
	{
		Code: SemaUnchecked, Severity: Warning, Class: Unchecked,
		Owner:   20,
		Summary: "a unit depends on an opaque unit",
		Message: "{unit} was not checked: it depends on opaque " +
			"unit {opaque}",
	},
	{
		Code: SemaUndefined, Severity: Error, Class: Design, Owner: 34,
		Summary: "a name is not declared or not visible",
		Message: "{name} is not declared",
	},
	{
		Code: SimAssertError, Severity: Error, Class: Design,
		Owner:   35,
		Summary: "an assert or report of severity error or failure",
		Message: "{kind} {severity}: {message}",
	},
	{
		Code: SimAssertNote, Severity: Hint, Class: Design, Owner: 35,
		Summary: "an assert or report of severity note",
		Message: "{kind} note: {message}",
	},
	{
		Code: SimAssertWarning, Severity: Warning, Class: Design,
		Owner:   35,
		Summary: "an assert or report of severity warning",
		Message: "{kind} warning: {message}",
	},
	{
		Code: SimBudgetExceeded, Severity: Error, Class: Design,
		Owner:   35,
		Summary: "a simulation budget ran out",
		Message: "{budget} budget of {limit} exceeded at {time}",
	},
	{
		Code: SimCoverHit, Severity: Hint, Class: Design, Owner: 35,
		Summary: "a PSL cover directive was hit",
		Message: "cover {name} hit {count} times",
	},
	{
		Code: SimCoverMiss, Severity: Warning, Class: Design,
		Owner:   35,
		Summary: "a PSL cover directive was never hit",
		Message: "cover {name} was never hit",
	},
	{
		Code: SimGlitch, Severity: Warning, Class: Design, Owner: 35,
		Summary: "a zero-width glitch",
		Message: "zero-width glitch on {signal} at {time}",
	},
	{
		Code: SimModelPorts, Severity: Error, Class: Design,
		Owner:   35,
		Summary: "a stand-in model's ports do not match",
		Message: "stand-in {model} for {unit}: {problem}",
	},
	{
		Code: SimNeedsFormal, Severity: Warning, Class: Design,
		Owner:   35,
		Summary: "a PSL construct needs formal verification",
		Message: "{construct} needs formal verification and does " +
			"not run in simulation",
	},
	{
		Code: SimRace, Severity: Warning, Class: Design, Owner: 29,
		Summary: "traces differ between seeds",
		Message: "race between {process_a} and {process_b}: traces " +
			"differ between seed {seed_a} and seed {seed_b}",
	},
	{
		Code: SimRuntimeError, Severity: Error, Class: Design,
		Owner:   35,
		Summary: "a runtime check failed during simulation",
		Message: "{problem} in {instance} at {time}",
	},
	{
		Code: SimStopStatus, Severity: Error, Class: Design,
		Owner:   35,
		Summary: "std.env.stop or finish with a non-zero status",
		Message: "std.env.{procedure} called with status {status}",
	},
	{
		Code: SimUnknownTestbench, Severity: Error, Class: Tool,
		Owner:   35,
		Summary: "--tb names no testbench candidate",
		Message: "no testbench named {name}; candidates: {candidates}",
	},
	{
		Code: StdlibIntegrity, Severity: Error, Class: Tool, Owner: 33,
		Summary: "a bundled std/ieee file failed its integrity check",
		Message: "bundled file {file} failed its integrity check",
	},
	{
		Code: StdlibInternal, Severity: Error, Class: Tool, Owner: 33,
		Summary: "an error inside the bundled std/ieee packages",
		Message: "error inside bundled {library}: {problem}",
	},
	{
		Code: SyntaxDirectiveError, Severity: Error, Class: Design,
		Owner:   31,
		Summary: "an `error conditional-analysis directive",
		Message: "`error: {message}",
	},
	{
		Code: SyntaxDirectiveWarning, Severity: Warning, Class: Design,
		Owner:   31,
		Summary: "a `warning conditional-analysis directive",
		Message: "`warning: {message}",
	},
	{
		Code: SyntaxError, Severity: Error, Class: Design, Owner: 31,
		Summary: "a lexical or syntax error",
		Message: "{problem}",
	},
	{
		Code: SyntaxInactive, Severity: Hint, Class: Design, Owner: 31,
		Summary: "a syntax error inside an inactive branch",
		Message: "{problem} (inactive conditional-analysis branch)",
	},
	{
		Code: SyntaxPSLIdentifier, Severity: Hint, Class: Design,
		Owner:   31,
		Summary: "a PSL-only word used as an identifier",
		Message: "{word} is a PSL keyword; GHDL rejects it as an " +
			"identifier",
	},
	{
		Code: SyntaxPSLKeyword, Severity: Error, Class: Design,
		Owner: 31,
		Summary: "a PSL-only word used as an identifier " +
			"(psl.strict_keywords)",
		Message: "{word} is reserved (psl.strict_keywords: true)",
	},
	{
		Code: SyntaxStandard, Severity: Error, Class: Design,
		Owner:   31,
		Summary: "a construct the selected standard does not allow",
		Message: "{construct} requires VHDL-{first} or later " +
			"(standard is {selected})",
	},
	{
		Code: SyntaxUndefinedCondition, Severity: Error,
		Class: Design, Owner: 31,
		Summary: "a conditional-analysis identifier is not defined",
		Message: "{name} is not a built-in conditional-analysis " +
			"identifier and is not defined; define it under " +
			"defines: in gohard.yaml or with --define {name}=VALUE",
	},
	{
		Code: SyntaxUTF8Comment, Severity: Hint, Class: Design,
		Owner:   20,
		Summary: "a UTF-8 comment under VHDL-1993",
		Message: "non-ASCII text in a comment is not portable " +
			"under VHDL-1993",
	},
	{
		Code: TraceInvalid, Severity: Error, Class: Tool, Owner: 36,
		Summary: "a trace file cannot be read",
		Message: "invalid trace {path}: {reason}",
	},
	{
		Code: TraceUnknownFormat, Severity: Error, Class: Tool,
		Owner:   36,
		Summary: "an unknown trace export format",
		Message: "unknown trace format {format}; supported: vcd, fst",
	},
	{
		Code: UnsupportedConstruct, Severity: Error, Class: Tool,
		Owner: 20,
		Summary: "legal VHDL GoHard does not implement; the unit " +
			"is opaque",
		Message: "{construct} is not supported yet, please report",
		Variants: []Variant{
			{
				Name: PlannedV11,
				Message: "{construct} is not supported yet " +
					"(planned for v1.1)",
			},
			{
				Name:    PleaseReport,
				Message: "{construct} is not supported yet, please report",
			},
		},
	},
	{
		Code: WorkspaceFileTooLarge, Severity: Warning, Class: Design,
		Owner:   31,
		Summary: "a file over 10 MB or 1,000,000 CST nodes",
		Message: "file not parsed: {what} exceeds the limit of " +
			"{limit}",
	},
	{
		Code: WorkspaceSymlinkLoop, Severity: Error, Class: Tool,
		Owner:   20,
		Summary: "a symbolic link loop",
		Message: "symbolic link loop at {path}",
	},
	{
		Code: WorkspaceUnreadable, Severity: Error, Class: Tool,
		Owner:   20,
		Summary: "a file or folder cannot be read",
		Message: "cannot read {path}: {reason}",
	},
}

var index = func() map[Code]int {
	m := make(map[Code]int, len(table))
	for i, info := range table {
		m[info.Code] = i
	}
	return m
}()

// Table returns a copy of every table row, sorted by code.
func Table() []Info {
	out := make([]Info, len(table))
	copy(out, table)
	return out
}

// Params returns the parameter names of the template in order of first
// appearance.
func (i Info) Params() []string { return params(i.Message) }

// Format fills the template with args in parameter order. A missing
// argument leaves its {name} in place and extra arguments are ignored,
// so formatting never fails; tests check producers pass the right count.
func (i Info) Format(args ...string) string {
	return fill(i.Message, args)
}

// FormatVariant is Format with the named variant's template; an unknown
// variant uses the default template.
func (i Info) FormatVariant(name string, args ...string) string {
	for _, v := range i.Variants {
		if v.Name == name {
			return fill(v.Message, args)
		}
	}
	return fill(i.Message, args)
}

func params(tmpl string) []string {
	var out []string
	for rest := tmpl; ; {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			return out
		}
		end := strings.IndexByte(rest[open:], '}')
		if end < 0 {
			return out
		}
		name := rest[open+1 : open+end]
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
		rest = rest[open+end+1:]
	}
}

func fill(tmpl string, args []string) string {
	names := params(tmpl)
	pairs := make([]string, 0, 2*len(names))
	for i, n := range names {
		if i < len(args) {
			pairs = append(pairs, "{"+n+"}", args[i])
		}
	}
	return strings.NewReplacer(pairs...).Replace(tmpl)
}

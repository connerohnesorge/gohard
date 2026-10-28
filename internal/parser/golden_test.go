package parser

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/connerohnesorge/gohard/v2/internal/condanal"
	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/cst"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// Per-grammar-rule goldens (#31): testdata/parse/<rule>/ holds
//
//	valid.vhd     a file that uses the rule and parses without errors
//	valid.tree    its committed cst.Dump
//	invalid.vhd   a file that breaks the rule
//	invalid.err   its committed diagnostics in the #20 text form; the
//	              first error line is the committed error position
//
// A rule named after a node kind in snake case (entity_decl for
// EntityDecl) must produce that kind in its valid tree. The first line
// of a .vhd may be a header comment with parse options:
//
//	-- test: std=2008 psl.comments psl.strict define=NAME=VALUE
//	--       allow=code,code kind=Kind,Kind|none
//
// The default standard is 2019. allow lists the error and warning codes
// a valid file may report (hints are always allowed); kind replaces the
// implied node kind (none: no kind check).
//
// `go test ./internal/parser -run Golden -update` writes the goldens of
// every rule whose valid file parses cleanly. A rule whose valid file
// does not parse cleanly yet and has no golden is pending. Every grammar
// builder has landed, so a pending rule fails (plain go test, and so the
// PR gate); -allow-pending skips them instead, for work in progress.
var (
	goldUpdate = flag.Bool("update", false,
		"rewrite the testdata/parse goldens")
	goldAllowPending = flag.Bool("allow-pending", false,
		"skip testdata/parse rules whose goldens are pending")
)

const goldDir = "../../testdata/parse"

// goldenToolVersion is TOOL_VERSION in every golden parse.
const goldenToolVersion = "0.0.0-test"

// goldHeader is the parsed `-- test:` line of a golden input.
type goldHeader struct {
	opts  Options
	allow []diag.Code
	kinds []cst.Kind
	// explicit reports a kind= option (kind=none included).
	explicit bool
}

// goldSnakeKinds maps the snake-case name of every node kind to the kind.
var goldSnakeKinds = func() map[string]cst.Kind {
	m := map[string]cst.Kind{}
	for _, k := range cst.Kinds() {
		m[kindSnake(k.String())] = k
	}
	return m
}()

// kindSnake turns a kind name into a rule directory name: PSLOBEUnary
// becomes psl_obe_unary and EntityDecl entity_decl.
func kindSnake(s string) string {
	if rest, ok := strings.CutPrefix(s, "PSL"); ok && rest != "" {
		return "psl_" + kindSnake(rest)
	}
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		if i > 0 && unicode.IsUpper(c) &&
			(unicode.IsLower(r[i-1]) ||
				i+1 < len(r) && unicode.IsLower(r[i+1])) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(c))
	}
	return b.String()
}

func goldKindByName(name string) (cst.Kind, bool) {
	for _, k := range cst.Kinds() {
		if k.String() == name {
			return k, true
		}
	}
	return 0, false
}

// goldParseHeader reads the options of a golden input of rule.
func goldParseHeader(rule string, src []byte) (goldHeader, error) {
	h := goldHeader{opts: Options{Standard: token.Std2019,
		ToolVersion: goldenToolVersion}}
	line, _, _ := bytes.Cut(src, []byte("\n"))
	rest, ok := strings.CutPrefix(string(line), "-- test:")
	if ok {
		for f := range strings.FieldsSeq(rest) {
			if err := h.option(f); err != nil {
				return h, err
			}
		}
	}
	if !h.explicit {
		if k, ok := goldSnakeKinds[rule]; ok {
			h.kinds = []cst.Kind{k}
		}
	}
	return h, nil
}

func (h *goldHeader) option(f string) error {
	key, val, _ := strings.Cut(f, "=")
	switch key {
	case "std":
		std, ok := token.ParseStandard(val)
		if !ok {
			return fmt.Errorf("bad standard %q", val)
		}
		h.opts.Standard = std
	case "psl.comments":
		h.opts.PSLComments = true
	case "psl.strict":
		h.opts.PSLStrictKeywords = true
	case "define":
		n, v, ok := strings.Cut(val, "=")
		if !ok {
			return fmt.Errorf("bad define %q", val)
		}
		h.opts.Defines = append(h.opts.Defines,
			condanal.Define{Name: n, Value: v})
	case "allow":
		for c := range strings.SplitSeq(val, ",") {
			if _, ok := diag.Code(c).Info(); !ok {
				return fmt.Errorf("unknown code %q", c)
			}
			h.allow = append(h.allow, diag.Code(c))
		}
	case "kind":
		h.explicit = true
		if val == "none" {
			return nil
		}
		for n := range strings.SplitSeq(val, ",") {
			k, ok := goldKindByName(n)
			if !ok {
				return fmt.Errorf("unknown kind %q", n)
			}
			h.kinds = append(h.kinds, k)
		}
	default:
		return fmt.Errorf("unknown option %q", f)
	}
	return nil
}

// ruleDirs lists the rule directories in name order; names starting
// with _ hold other goldens.
func goldRuleDirs(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(goldDir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if e.IsDir() && !strings.HasPrefix(e.Name(), "_") {
			out = append(out, e.Name())
		}
	}
	return out
}

// renderDiags is the #20 text form of r's diagnostics for file name.
func goldDiags(r *Result, name string) string {
	return string(diag.RenderText(&diag.Report{
		Diagnostics: r.Diagnostics(name), Files: 1}))
}

// unclean returns why the valid parse r does not count as clean: an
// error or warning not allowed by h, an ErrorNode, a missing token, a
// stub message, or a missing required kind. Empty means clean.
func goldUnclean(r *Result, h goldHeader) []string {
	var why []string
	for _, d := range r.Diagnostics("valid.vhd") {
		if d.Severity != diag.Hint && !slices.Contains(h.allow, d.Code) ||
			strings.Contains(d.Message, "not parsed yet") {
			why = append(why, fmt.Sprintf("%d:%d %s %s", d.Line, d.Col,
				d.Code, d.Message))
		}
	}
	seen := map[cst.Kind]bool{}
	for n := range r.Tree().Descendants() {
		seen[n.Kind()] = true
		if n.Kind() == cst.KindErrorNode && !n.Inactive() {
			why = append(why, fmt.Sprintf("ErrorNode at %d",
				n.FullSpan().Start))
		}
	}
	for tk := range r.Tree().DescendantTokens() {
		if tk.Missing() && !tk.Inactive() {
			why = append(why, fmt.Sprintf("missing %v at %d", tk.Kind(),
				tk.Span().Start))
		}
	}
	for _, k := range h.kinds {
		if !seen[k] {
			why = append(why, "no "+k.String()+" node")
		}
	}
	return why
}

// golden compares got with the file at path, or writes it with -update.
// It returns false when the golden is missing.
func goldCompare(t *testing.T, path, got string) bool {
	t.Helper()
	if *goldUpdate {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return true
	}
	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("%s differs (run with -update and review):\n%s", path,
			goldFirstDiff(string(want), got))
	}
	return true
}

// firstDiff shows the first differing line of two texts.
func goldFirstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(w), len(g)) {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d:\nwant %q\n got %q", i+1, a, b)
		}
	}
	return "(trailing newline)"
}

func goldPending(t *testing.T, format string, args ...any) {
	t.Helper()
	if !*goldAllowPending {
		t.Errorf("pending: "+format, args...)
		return
	}
	t.Skipf("pending: "+format, args...)
}

// TestParseGoldens parses every rule's valid and invalid file and
// checks the goldens.
func TestParseGoldens(t *testing.T) {
	for _, rule := range goldRuleDirs(t) {
		t.Run(rule, func(t *testing.T) { goldCheckRule(t, rule) })
	}
}

func goldCheckRule(t *testing.T, rule string) {
	dir := filepath.Join(goldDir, rule)
	read := func(name string) ([]byte, goldHeader) {
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		h, err := goldParseHeader(rule, src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return src, h
	}
	vsrc, vh := read("valid.vhd")
	isrc, ih := read("invalid.vhd")
	vr := checkInvariants(t, rule+"/valid.vhd", vsrc, vh.opts)
	ir := checkInvariants(t, rule+"/invalid.vhd", isrc, ih.opts)

	why := goldUnclean(vr, vh)
	treePath := filepath.Join(dir, "valid.tree")
	errPath := filepath.Join(dir, "invalid.err")
	if len(why) > 0 {
		if _, err := os.Stat(treePath); err == nil {
			t.Fatalf("valid.vhd no longer parses cleanly:\n%s",
				strings.Join(why, "\n"))
		}
		goldPending(t, "valid.vhd does not parse cleanly yet: %s", why[0])
		return
	}
	if !goldCompare(t, treePath, cst.Dump(vr.Tree())) {
		goldPending(t, "no valid.tree; run with -update and review it")
	}

	errs := 0
	for _, d := range ir.Diagnostics("invalid.vhd") {
		if d.Severity == diag.Error {
			errs++
		}
		if strings.Contains(d.Message, "not parsed yet") {
			goldPending(t, "invalid.vhd hits a stub: %s", d.Message)
			return
		}
	}
	if errs == 0 {
		t.Errorf("invalid.vhd reports no error:\n%s",
			goldDiags(ir, "invalid.vhd"))
		return
	}
	if !goldCompare(t, errPath, goldDiags(ir, "invalid.vhd")) {
		goldPending(t, "no invalid.err; run with -update and review it")
	}
}

// TestParseRulesCoverKinds: every node kind but ErrorNode has its rule
// directory, every directory has both inputs, and a directory not named
// after a kind declares its kinds, so a misspelt name cannot silently
// skip the kind check.
func TestParseRulesCoverKinds(t *testing.T) {
	dirs := goldRuleDirs(t)
	for _, k := range cst.Kinds() {
		if k == cst.KindErrorNode {
			continue
		}
		if !slices.Contains(dirs, kindSnake(k.String())) {
			t.Errorf("no testdata/parse/%s for %v", kindSnake(k.String()), k)
		}
	}
	for _, rule := range dirs {
		for _, name := range []string{"valid.vhd", "invalid.vhd"} {
			src, err := os.ReadFile(filepath.Join(goldDir, rule, name))
			if err != nil {
				t.Errorf("%s: %v", rule, err)
				continue
			}
			h, err := goldParseHeader(rule, src)
			if err != nil {
				t.Errorf("%s/%s: %v", rule, name, err)
			}
			_, named := goldSnakeKinds[rule]
			if name == "valid.vhd" && !named && !h.explicit {
				t.Errorf("%s/valid.vhd: not a kind name and no kind= "+
					"option", rule)
			}
		}
	}
	if n := len(dirs); n < len(cst.Kinds())-1 {
		t.Errorf("%d rules for %d kinds", n, len(cst.Kinds()))
	}
}

// TestParseRulesDirectives: the conditional-analysis rules (#31 Done
// when) exist, and the valid inputs of the directive rules really use
// their directive.
func TestParseRulesDirectives(t *testing.T) {
	for rule, directive := range map[string]string{
		"directive_if": "`if ", "directive_elsif": "`elsif ",
		"directive_else": "`else", "directive_end": "`end",
		"directive_warning": "`warning ", "directive_error": "`error ",
		"builtin_identifier": "VHDL_VERSION", "user_identifier": "PROFILE",
		"inactive_branch": "`if ", "converse_attribute": "'converse",
		"mode_view_decl": "view ",
	} {
		src, err := os.ReadFile(filepath.Join(goldDir, rule, "valid.vhd"))
		if err != nil {
			t.Errorf("%s: %v", rule, err)
			continue
		}
		if !bytes.Contains(src, []byte(directive)) {
			t.Errorf("%s/valid.vhd does not use %q", rule, directive)
		}
	}
}

// TestParseRulesDirectiveSemantics checks the directive rules through
// the parser now, whatever the grammar builders' progress: which
// branches are inactive, and which directive diagnostics fire.
func TestParseRulesDirectiveSemantics(t *testing.T) {
	cases := []struct {
		rule, file string
		inactive   int      // inactive regions
		codes      []string // directive and condition codes
	}{
		{"directive_if", "valid.vhd", 0, nil},
		{"directive_elsif", "valid.vhd", 1, nil},
		{"directive_else", "valid.vhd", 1, nil},
		{"directive_end", "valid.vhd", 0, nil},
		{"directive_end", "invalid.vhd", 0, []string{"syntax/error"}},
		{"directive_warning", "valid.vhd", 1,
			[]string{"syntax/directive-warning"}},
		{"directive_error", "valid.vhd", 0,
			[]string{"syntax/directive-error"}},
		{"builtin_identifier", "valid.vhd", 0, nil},
		{"user_identifier", "valid.vhd", 1, nil},
		{"user_identifier", "invalid.vhd", 1,
			[]string{"syntax/undefined-condition"}},
		{"inactive_branch", "valid.vhd", 1, nil},
	}
	for _, c := range cases {
		src, err := os.ReadFile(filepath.Join(goldDir, c.rule, c.file))
		if err != nil {
			t.Fatal(err)
		}
		h, err := goldParseHeader(c.rule, src)
		if err != nil {
			t.Fatal(err)
		}
		r := checkInvariants(t, c.rule, src, h.opts)
		if len(r.Inactive) != c.inactive {
			t.Errorf("%s/%s: %d inactive regions, want %d", c.rule, c.file,
				len(r.Inactive), c.inactive)
		}
		var codes []string
		for _, d := range r.Diagnostics(c.file) {
			if d.Code != diag.SyntaxInactive &&
				!strings.Contains(d.Message, "not parsed yet") {
				codes = append(codes, string(d.Code))
			}
		}
		if !slices.Equal(codes, c.codes) {
			t.Errorf("%s/%s: codes %v, want %v", c.rule, c.file, codes,
				c.codes)
		}
	}
}

// TestModeViewsGolden: the mode_views corpus package, parsed with the
// defines of its gohard.yaml, keeps its directives as trivia, marks its
// inactive branches in the dump, and matches the committed dump
// testdata/parse/_corpus/mode_views_pkg.tree once it parses cleanly.
func TestModeViewsGolden(t *testing.T) {
	const dir = "../../testdata/corpus/mode_views/"
	cfgSrc, err := os.ReadFile(dir + config.FileName)
	if err != nil {
		t.Fatal(err)
	}
	c, ds := config.Parse(config.FileName, cfgSrc)
	if len(ds) > 0 {
		t.Fatalf("config: %v", ds)
	}
	opts := FromConfig(c, goldenToolVersion)
	opts.Standard = token.Std2019
	src, err := os.ReadFile(dir + "mode_views_pkg.vhd")
	if err != nil {
		t.Fatal(err)
	}
	r := checkInvariants(t, "mode_views_pkg.vhd", src, opts)
	dump := cst.Dump(r.Tree())
	if n := strings.Count(dump, "Directive "); n != len(r.Directives) ||
		n == 0 {
		t.Errorf("%d Directive trivia, %d directives", n,
			len(r.Directives))
	}
	for _, d := range r.Directives {
		text := strconv.Quote(string(src[d.Start:d.End]))
		if !strings.Contains(dump, "Directive "+text) {
			t.Errorf("directive %d..%d not in the dump", d.Start, d.End)
		}
	}
	if !strings.Contains(dump, "InactiveBranch ") {
		t.Errorf("no InactiveBranch in the dump")
	}
	builtin, user := false, false
	for _, d := range r.Directives {
		text := string(src[d.Start:d.End])
		builtin = builtin || strings.Contains(text, "VHDL_VERSION")
		user = user || strings.Contains(text, "MV_PROFILE")
	}
	if !builtin || !user {
		t.Errorf("directives use built-in %v, user %v", builtin, user)
	}
	why := goldUnclean(r, goldHeader{})
	path := filepath.Join(goldDir, "_corpus", "mode_views_pkg.tree")
	if len(why) > 0 {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("no longer parses cleanly: %v", why)
		}
		goldPending(t, "mode_views_pkg.vhd does not parse cleanly yet: %s",
			why[0])
		return
	}
	if *goldUpdate {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if !goldCompare(t, path, dump) {
		goldPending(t, "no %s; run with -update and review it", path)
	}
}

// branchActive parses `if cond then entity yes ... `else entity no and
// reports whether the yes branch is active.
func goldBranchActive(t *testing.T, cond string, opts Options) bool {
	t.Helper()
	src := "`if " + cond + " then\nentity yes is end;\n`else\n" +
		"entity no is end;\n`end if\n"
	r := checkInvariants(t, cond, []byte(src), opts)
	for _, p := range r.Problems {
		if p.Code == diag.SyntaxUndefinedCondition {
			t.Errorf("%s: undefined condition %v", cond, p.Args)
		}
	}
	yes := strings.Index(src, "entity yes")
	no := strings.Index(src, "entity no")
	in := func(off int) bool {
		for _, g := range r.Inactive {
			if g.Start <= off && off < g.End {
				return true
			}
		}
		return false
	}
	if in(yes) == in(no) {
		t.Errorf("%s: both branches %v", cond, in(yes))
	}
	return !in(yes)
}

// TestBuiltinIdentifierValues: through the parser, every built-in
// identifier has its #31 value under each standard (the directives are
// gated before 2019 but still select the branch), compares equal to
// that value only, and the comparison is case-sensitive in the value.
func TestBuiltinIdentifierValues(t *testing.T) {
	for _, std := range token.Standards {
		want := map[string]string{
			"VHDL_VERSION": std.String(),
			"TOOL_TYPE":    "SIMULATION",
			"TOOL_VENDOR":  "gohard",
			"TOOL_NAME":    "gohard",
			"TOOL_EDITION": "community",
			"TOOL_VERSION": goldenToolVersion,
		}
		if len(want) != len(config.Builtins) {
			t.Fatalf("built-ins %v", config.Builtins)
		}
		opts := Options{Standard: std, ToolVersion: goldenToolVersion}
		for _, name := range config.Builtins {
			v, ok := want[name]
			if !ok {
				t.Fatalf("no expected value for %s", name)
			}
			if !goldBranchActive(t, name+` = "`+v+`"`, opts) {
				t.Errorf("%v: %s /= %q", std, name, v)
			}
			if goldBranchActive(t, name+` /= "`+v+`"`, opts) {
				t.Errorf("%v: %s /= %q holds", std, name, v)
			}
			if goldBranchActive(t, name+` = "`+v+`x"`, opts) {
				t.Errorf("%v: %s = %q", std, name, v+"x")
			}
			if strings.ToUpper(v) != v && goldBranchActive(t,
				name+` = "`+strings.ToUpper(v)+`"`, opts) {
				t.Errorf("%v: %s compares case-insensitively", std, name)
			}
			lower := strings.ToLower(name)
			if !goldBranchActive(t, lower+` = "`+v+`"`, opts) {
				t.Errorf("%v: %s is case-sensitive", std, lower)
			}
		}
	}
}

// TestDefineOverridesDefinesParse: a --define replaces the defines:
// entry of the same name in what the parser sees, and leaves the others.
func TestDefineOverridesDefinesParse(t *testing.T) {
	yaml := []byte("defines:\n  MODE: \"slow\"\n  WIDTH: \"8\"\n")
	c, ds := config.Parse(config.FileName, yaml)
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	opts := FromConfig(c, goldenToolVersion)
	opts.Standard = token.Std2019
	if !goldBranchActive(t, `MODE = "slow" and WIDTH = "8"`, opts) {
		t.Errorf("defines: not applied")
	}
	c2, logs, ds := config.ApplyDefines(c, []string{"mode=fast"})
	if len(ds) > 0 || len(logs) != 1 {
		t.Fatalf("ApplyDefines: %v %v", logs, ds)
	}
	opts = FromConfig(c2, goldenToolVersion)
	opts.Standard = token.Std2019
	if !goldBranchActive(t, `MODE = "fast" and WIDTH = "8"`, opts) {
		t.Errorf("--define did not override defines:")
	}
	if goldBranchActive(t, `MODE = "slow"`, opts) {
		t.Errorf("the defines: value survived --define")
	}
	c3, _, ds := config.ApplyDefines(c, []string{"EXTRA=1", "EXTRA=2"})
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	opts = FromConfig(c3, goldenToolVersion)
	opts.Standard = token.Std2019
	if !goldBranchActive(t, `EXTRA = "2" and MODE = "slow"`, opts) {
		t.Errorf("the last --define does not win")
	}
}

// TestEverywhereReservedRules: each everywhere-reserved 2008 word (a
// VHDL keyword first reserved by 2008) has its reserved_2008_<word>
// rule, which is an identifier before 2008 and an error from 2008 on.
func TestEverywhereReservedRules(t *testing.T) {
	var words []string
	for _, kw := range token.Keywords() {
		if kw.Class == token.ClassVHDL && kw.First == token.Std2008 {
			words = append(words, kw.Word)
		}
	}
	want := []string{"context", "cover", "default", "force", "parameter",
		"release"}
	if slices.Sort(words); !slices.Equal(words, want) {
		t.Errorf("everywhere-reserved 2008 words %v, want %v", words, want)
	}
	for _, w := range words {
		dir := filepath.Join(goldDir, "reserved_2008_"+w)
		for _, c := range []struct {
			file string
			bad  bool
		}{{"valid.vhd", false}, {"invalid.vhd", true}} {
			src, err := os.ReadFile(filepath.Join(dir, c.file))
			if err != nil {
				t.Errorf("%s: %v", w, err)
				continue
			}
			if !bytes.Contains(src, []byte("signal "+w+" :")) {
				t.Errorf("%s/%s does not declare %s", dir, c.file, w)
			}
			h, err := goldParseHeader("", src)
			if err != nil {
				t.Fatal(err)
			}
			if got := h.opts.Standard >= token.Std2008; got != c.bad {
				t.Errorf("%s/%s: standard %v", w, c.file, h.opts.Standard)
			}
			// The word is checked at the lexer level here; the full
			// diagnostics are the rule's goldens.
			k, _ := token.Lookup([]byte(w), h.opts.Standard, false, false)
			if (k != token.Identifier) != c.bad {
				t.Errorf("%s under %v lexes as %v", w, h.opts.Standard, k)
			}
		}
	}
}

// TestSnake pins the kind-to-directory mapping.
func TestSnake(t *testing.T) {
	for in, want := range map[string]string{
		"EntityDecl": "entity_decl", "PSLOBEUnary": "psl_obe_unary",
		"PSLVunit": "psl_vunit", "DesignFile": "design_file",
		"PSLNextEventA": "psl_next_event_a", "Label": "label",
	} {
		if got := kindSnake(in); got != want {
			t.Errorf("kindSnake(%q) = %q, want %q", in, got, want)
		}
	}
	if len(goldSnakeKinds) != len(cst.Kinds()) {
		t.Errorf("snake names collide: %d for %d kinds", len(goldSnakeKinds),
			len(cst.Kinds()))
	}
}

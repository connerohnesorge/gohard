package analyzer_test

import (
	"fmt"
	"go/types"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
	"golang.org/x/tools/go/packages"

	"github.com/connerohnesorge/gohard/tools/detlint/analyzer"
)

const mod = analyzer.Module

// TestTestdata runs the hand-written cases: every banned item in a core
// package and in a package outside the module, the _test.go policy,
// the exempt packages and the map-order rule.
func TestTestdata(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), analyzer.Analyzer,
		mod+"/internal/core",
		mod+"/internal/maporder",
		mod+"/cmd/tool",
		mod+"/internal/env/realenv/...",
		mod+"/tools/gen",
		mod+"/internal/jsonrpc2",
		"outside",
	)
}

func TestRules(t *testing.T) {
	tests := []struct {
		path string
		test bool
		want analyzer.Rule
	}{
		{mod + "/cmd", false, 0},
		{mod + "/cmd/gohard", false, 0},
		{mod + "/cmd/gohard", true, 0},
		{mod + "/internal/env/realenv", false, 0},
		{mod + "/internal/env/realenv_test", true, 0},
		{mod + "/internal/env/realenv/x", false, 0},
		{mod + "/tools/detlint", false, analyzer.MapOrder},
		{mod + "/tools/detlint/analyzer_test", true, 0},
		{mod + "/internal/env", false, analyzer.All &^ analyzer.Rooted},
		{mod + "/internal/env/simenv", false, analyzer.All},
		{mod + "/internal/env/realenvx", false, analyzer.All},
		{mod + "/cmdx", false, analyzer.All},
		{mod + "/internal/cmd", false, analyzer.All},
		{mod + "/internal/tools", false, analyzer.All},
		{mod, false, analyzer.All},
		{"example.com/cmd/x", false, analyzer.All},
		{mod + "/internal/jsonrpc2", false,
			analyzer.All &^ analyzer.Go &^ analyzer.Select},
		{mod + "/internal/jsonrpc2_test", true,
			analyzer.All &^ analyzer.TestAllowed &^ analyzer.Go &^
				analyzer.Select},
		{mod + "/internal/jsonrpc2/sub", false, analyzer.All},
		{mod + "/internal/jsonrpc2x", false, analyzer.All},
		{mod + "/internal/server", false, analyzer.All},
		{mod + "/internal/cli", true,
			analyzer.Clock | analyzer.Rand | analyzer.Net |
				analyzer.Process | analyzer.Go | analyzer.Select |
				analyzer.Linkname | analyzer.Address},
	}
	for _, tt := range tests {
		if got := analyzer.Rules(tt.path, tt.test); got != tt.want {
			t.Errorf("Rules(%q, %v) = %v, want %v",
				tt.path, tt.test, got, tt.want)
		}
	}
}

// TestRulesLaws checks the scope laws over many paths: a test file
// never gets more rules than its package, core packages get every rule,
// the exempt trees get none, and tools only get maporder.
func TestRulesLaws(t *testing.T) {
	segs := []string{"cmd", "internal", "env", "realenv", "tools", "x",
		"cmdx", "realenv_test", "gohard", "jsonrpc2"}
	roots := []string{mod, "example.com/m", "outside"}
	n := 0
	for _, root := range roots {
		for a := range segs {
			for b := range len(segs) + 1 {
				for c := range len(segs) + 1 {
					path := root + "/" + segs[a]
					if b < len(segs) {
						path += "/" + segs[b]
						if c < len(segs) {
							path += "/" + segs[c]
						}
					}
					checkLaws(t, path)
					n++
				}
			}
		}
	}
	if n < 500 {
		t.Fatalf("only %d paths checked", n)
	}
}

func checkLaws(t *testing.T, path string) {
	t.Helper()
	pkg, file := analyzer.Rules(path, false), analyzer.Rules(path, true)
	if file&^pkg != 0 {
		t.Errorf("%s: test rules %v exceed package rules %v",
			path, file, pkg)
	}
	if file&analyzer.TestAllowed != 0 {
		t.Errorf("%s: test file gets lifted rules %v", path, file)
	}
	if !strings.HasSuffix(path, "_test") &&
		analyzer.Rules(path+"_test", true) != file {
		t.Errorf("%s: external test package differs", path)
	}
	base := strings.TrimSuffix(path, "_test")
	in := func(dir string) bool {
		p := mod + "/" + dir
		return base == p || strings.HasPrefix(base, p+"/")
	}
	want := analyzer.All
	switch {
	case in("cmd"), in("internal/env/realenv"):
		want = 0
	case in("tools"):
		want = analyzer.MapOrder
	case base == analyzer.StdioPackage:
		want = analyzer.All &^ analyzer.StdioAllowed
	case base == analyzer.EnvPackage:
		want = analyzer.All &^ analyzer.Rooted
	}
	if pkg != want {
		t.Errorf("Rules(%q) = %v, want %v", path, pkg, want)
	}
}

func TestRuleString(t *testing.T) {
	if s := analyzer.Rule(0).String(); s != "none" {
		t.Errorf("Rule(0) = %q", s)
	}
	all := analyzer.All.String()
	if strings.Count(all, "|") != 13 {
		t.Errorf("All = %q, want 14 names", all)
	}
	for i := range 14 {
		s := analyzer.Rule(1 << i).String()
		if s == "" || strings.Contains(s, "|") {
			t.Errorf("rule %d has name %q", i, s)
		}
	}
}

// pkgScope loads the exported package-level functions and variables of
// a standard package, in sorted order.
func pkgScope(t *testing.T, path string) []string {
	t.Helper()
	pkgs, err := packages.Load(&packages.Config{Mode: packages.NeedTypes},
		path)
	if err != nil || len(pkgs) != 1 || pkgs[0].Types == nil {
		t.Fatalf("load %s: %v", path, err)
	}
	scope := pkgs[0].Types.Scope()
	var names []string
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch o := obj.(type) {
		case *types.Var:
			names = append(names, name)
		case *types.Func:
			if sig, ok := o.Type().(*types.Signature); ok &&
				sig.TypeParams().Len() == 0 {
				names = append(names, name)
			}
		}
	}
	return names
}

type use struct {
	pkg, name string
	rule      analyzer.Rule // 0: allowed
}

// expected pins the classification of the package-level os, time,
// context, log, log/slog, fmt and path/filepath names. It is written
// independently of the analyzer tables.
func expected(pkg, name string) analyzer.Rule {
	switch pkg {
	case "os":
		switch {
		case strings.HasPrefix(name, "Err"),
			strings.HasPrefix(name, "Is"),
			name == "NewSyscallError", name == "SameFile",
			name == "Expand":
			return 0
		case strings.Contains(name, "env") ||
			strings.Contains(name, "Env"):
			return analyzer.Getenv
		case name == "Stdin", name == "Stdout", name == "Stderr":
			return analyzer.Stdio
		case name == "Args", name == "Exit", name == "Executable",
			name == "Hostname", name == "Getwd", name == "Chdir",
			name == "StartProcess", name == "FindProcess",
			name == "Getpagesize", name == "Interrupt", name == "Kill",
			strings.HasPrefix(name, "Get") && strings.HasSuffix(name, "id"),
			name == "Getgroups":
			return analyzer.Process
		}
		return analyzer.FS
	case "time":
		switch name {
		case "Now", "Sleep", "After", "AfterFunc", "NewTimer",
			"NewTicker", "Tick", "Since", "Until", "LoadLocation",
			"Local", "Unix", "UnixMilli", "UnixMicro":
			return analyzer.Clock
		}
	case "context":
		switch {
		case name == "AfterFunc":
			return analyzer.Go
		case strings.HasPrefix(name, "WithTimeout"),
			strings.HasPrefix(name, "WithDeadline"):
			return analyzer.Clock
		}
	case "log":
		if name != "New" {
			return analyzer.Stdio
		}
	case "log/slog":
		switch name {
		case "Debug", "DebugContext", "Info", "InfoContext", "Warn",
			"WarnContext", "Error", "ErrorContext", "Log", "LogAttrs",
			"Default", "SetDefault", "SetLogLoggerLevel":
			return analyzer.Stdio
		}
	case "fmt":
		if strings.HasPrefix(name, "Print") ||
			strings.HasPrefix(name, "Scan") {
			return analyzer.Stdio
		}
	case "path/filepath":
		switch name {
		case "Abs", "EvalSymlinks", "Glob", "Walk", "WalkDir":
			return analyzer.FS
		case "IsAbs":
			return analyzer.Rooted
		}
	}
	return 0
}

var importAs = map[string]string{
	"os": "os", "time": "time", "log": "log", "log/slog": "slog",
	"fmt": "fmt", "path/filepath": "filepath", "context": "context",
}

var stdPkgs = []string{"os", "time", "context", "log", "log/slog", "fmt",
	"path/filepath"}

var bannedImports = []struct {
	path string
	rule analyzer.Rule
}{
	{"math/rand", analyzer.Rand}, {"math/rand/v2", analyzer.Rand},
	{"crypto/rand", analyzer.Rand}, {"io/ioutil", analyzer.FS},
	{"os/exec", analyzer.Process}, {"os/signal", analyzer.Process},
	{"os/user", analyzer.Process}, {"syscall", analyzer.Process},
	{"unsafe", analyzer.Address}, {"weak", analyzer.Go},
	{"flag", analyzer.Getenv},
	{"net", analyzer.Net}, {"net/http", analyzer.Net},
	{"net/http/httptest", analyzer.Net}, {"net/rpc", analyzer.Net},
	{"net/smtp", analyzer.Net}, {"net/mail", analyzer.Net},
	{"net/textproto", analyzer.Net},
	{"net/url", 0}, {"net/netip", 0}, {"strings", 0}, {"io/fs", 0},
	{mod + "/internal/env/realenv", analyzer.RealEnv},
}

// genFiles builds one package per scope that uses every exported
// function and variable of the classified standard packages and imports
// every listed package; the core copy carries a want comment for each
// banned use and each banned import.
func genFiles(t *testing.T) (map[string]string, int) {
	var uses []use
	for _, p := range stdPkgs {
		for _, name := range pkgScope(t, p) {
			uses = append(uses, use{p, name, expected(p, name)})
		}
	}
	files := map[string]string{
		mod + "/internal/env/realenv/realenv.go": "package realenv\n",
	}
	scopes := []struct {
		dir  string
		test bool
		want analyzer.Rule
	}{
		{"internal/gen", false, analyzer.All},
		{"cmd/gen", false, 0},
		{"internal/env/realenv/gen", false, 0},
		{"tools/gen", false, analyzer.MapOrder},
		{"internal/gentest", true, analyzer.All &^ analyzer.TestAllowed},
	}
	wants := 0
	for _, sc := range scopes {
		var b strings.Builder
		fmt.Fprintf(&b, "package gen\n\nimport (\n")
		for _, p := range stdPkgs {
			fmt.Fprintf(&b, "\t%s %q\n", importAs[p], p)
		}
		for _, im := range bannedImports {
			fmt.Fprintf(&b, "\t_ %q", im.path)
			if im.rule&sc.want != 0 {
				fmt.Fprintf(&b, " // want `import of %s: `",
					regexp.QuoteMeta(strings.TrimPrefix(im.path, mod+"/")))
				wants++
			}
			b.WriteString("\n")
		}
		b.WriteString(")\n\nvar (\n")
		for _, u := range uses {
			fmt.Fprintf(&b, "\t_ = %s.%s", importAs[u.pkg], u.name)
			if u.rule&sc.want != 0 {
				fmt.Fprintf(&b, " // want `^use of %s: `",
					regexp.QuoteMeta(u.pkg+"."+u.name))
				wants++
			}
			b.WriteString("\n")
		}
		b.WriteString(")\n")
		name := mod + "/" + sc.dir + "/gen.go"
		if sc.test {
			name = mod + "/" + sc.dir + "/gen_test.go"
			files[mod+"/"+sc.dir+"/doc.go"] = "package gen\n"
		}
		files[name] = b.String()
	}
	return files, wants
}

// TestEveryName proves the analyzer classifies every exported function
// and variable of os, time, context, log, log/slog, fmt and
// path/filepath, and
// every banned import, as the independent expectation says, in each
// scope.
func TestEveryName(t *testing.T) {
	files, wants := genFiles(t)
	if wants < 150 {
		t.Fatalf("only %d expectations generated", wants)
	}
	dir, cleanup, err := analysistest.WriteFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	analysistest.Run(t, dir, analyzer.Analyzer,
		mod+"/internal/gen", mod+"/cmd/gen",
		mod+"/internal/env/realenv/gen", mod+"/tools/gen",
		mod+"/internal/gentest")
}

func TestAnalyzerMeta(t *testing.T) {
	a := analyzer.Analyzer
	if a.Name != "detlint" || a.Doc == "" || a.Run == nil {
		t.Fatalf("bad analyzer %+v", a)
	}
	if err := a.Flags.Parse(nil); err != nil {
		t.Fatal(err)
	}
}

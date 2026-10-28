package analyzer

import (
	"strings"
)

// Module is the import path of the GoHard module. Scopes are decided
// relative to it.
const Module = "github.com/connerohnesorge/gohard/v2"

// Rule is a set of detlint rules, one bit per rule.
type Rule uint16

// The rules. See the package documentation for what each one reports.
const (
	Clock Rule = 1 << iota
	Rand
	Getenv
	FS
	Stdio
	Net
	Process
	Go
	Select
	RealEnv
	MapOrder
	Linkname
	Address
	Rooted
)

// World is every rule that guards an env.Env door.
const World = Clock | Rand | Getenv | FS | Stdio | Net | Process | Go |
	Select | RealEnv | Linkname | Address

// All is every rule.
const All = World | MapOrder | Rooted

// TestAllowed is the set of rules lifted in _test.go files of core
// packages.
const TestAllowed = Getenv | FS | Stdio | RealEnv | MapOrder | Rooted

// Stdio goroutines: internal/jsonrpc2 (the package itself, not its
// subpackages) runs the stdio reader and writer, the only goroutines
// in GoHard (#4 P8, #25), so only it is exempt from the go and select
// rules. Every other rule still applies there.
const (
	StdioPackage = Module + "/internal/jsonrpc2"
	StdioAllowed = Go | Select
)

// EnvPackage defines env.IsRooted, the one place that may call
// filepath.IsAbs (the rooted rule).
const EnvPackage = Module + "/internal/env"

var ruleNames = [...]string{
	"clock", "rand", "getenv", "fs", "stdio", "net", "process", "go",
	"select", "realenv", "maporder", "linkname", "address", "rooted",
}

// String returns the rule names joined by "|", or "none".
func (r Rule) String() string {
	var names []string
	for i, n := range ruleNames {
		if r&(1<<i) != 0 {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, "|")
}

// door tells the reader where the capability must come from instead.
func (r Rule) door() string {
	switch r {
	case Clock:
		return "take time from the Clock door of env.Env"
	case Rand:
		return "take randomness from the Rand door of env.Env"
	case Getenv:
		return "read the environment through the Getenv door of env.Env"
	case FS:
		return "reach files through the FS door of env.Env"
	case Stdio:
		return "write through the Transport door of env.Env"
	case Net:
		return "the network is not an env.Env door; use Transport"
	case Process:
		return "use the Executor door of env.Env or return an exit code"
	case Go, Select:
		return "run parallel work through the Jobs door of env.Env"
	case RealEnv:
		return "only cmd/ builds realenv; take an env.Env value"
	case Linkname:
		return "it reaches runtime internals past every env.Env door"
	case Address:
		return "memory addresses differ between runs; " +
			"order and print by value or ID"
	case Rooted:
		return "use env.IsRooted: on Windows /x and \\x are rooted " +
			"but not filepath.IsAbs"
	}
	return "range over slices.Sorted(maps.Keys(m)) or sort what you collect"
}

func within(path, dir string) bool {
	p := Module + "/" + dir
	return path == p || strings.HasPrefix(path, p+"/")
}

// Rules returns the rules that apply to a file of the package with
// import path pkgPath; test reports whether the file is a _test.go
// file. A trailing "_test" (external test package) is ignored.
func Rules(pkgPath string, test bool) Rule {
	pkgPath = strings.TrimSuffix(pkgPath, "_test")
	switch {
	case pkgPath == StdioPackage && test:
		return All &^ TestAllowed &^ StdioAllowed
	case pkgPath == StdioPackage:
		return All &^ StdioAllowed
	case within(pkgPath, "cmd"), within(pkgPath, "internal/env/realenv"):
		return 0
	case within(pkgPath, "tools"):
		if test {
			return 0
		}
		return MapOrder
	case test:
		return All &^ TestAllowed
	case pkgPath == EnvPackage:
		return All &^ Rooted
	}
	return All
}

type ban struct {
	rule Rule
	what string
}

// bannedImport reports whether importing path is banned.
func bannedImport(path string) (ban, bool) {
	switch path {
	case "math/rand", "math/rand/v2", "crypto/rand", "hash/maphash":
		return ban{Rand, "import of " + path}, true
	case "io/ioutil":
		return ban{FS, "import of io/ioutil"}, true
	case "os/exec", "os/signal", "os/user", "syscall", "plugin":
		return ban{Process, "import of " + path}, true
	case "flag":
		// It reads os.Args, the process's command line; core code
		// gets its arguments from cli.Run.
		return ban{Getenv, "import of flag"}, true
	case "unsafe":
		return ban{Address, "import of unsafe"}, true
	case "weak":
		// weak pointers go nil when the GC runs.
		return ban{Go, "import of weak"}, true
	case "net/url", "net/netip":
		return ban{}, false
	case Module + "/internal/env/realenv":
		return ban{RealEnv, "import of internal/env/realenv"}, true
	}
	if path == "net" || strings.HasPrefix(path, "net/") {
		return ban{Net, "import of " + path}, true
	}
	return ban{}, false
}

// bannedObjects maps "pkgpath.Name" of package-level objects to the
// rule that bans them. Package os is handled by osBan.
var bannedObjects = map[string]Rule{
	"time.Now":          Clock,
	"time.Sleep":        Clock,
	"time.After":        Clock,
	"time.AfterFunc":    Clock,
	"time.NewTimer":     Clock,
	"time.NewTicker":    Clock,
	"time.Tick":         Clock,
	"time.Since":        Clock,
	"time.Until":        Clock,
	"time.LoadLocation": Clock,
	"time.Local":        Clock,
	// These return times in the process's zone (TZ, /etc/localtime).
	"time.Unix":      Clock,
	"time.UnixMilli": Clock,
	"time.UnixMicro": Clock,

	// Callbacks the GC runs on its own goroutine, and machine facts.
	"runtime.SetFinalizer": Go,
	"runtime.AddCleanup":   Go,
	"runtime.NumCPU":       Process,
	"runtime.NumGoroutine": Process,
	"runtime.GOMAXPROCS":   Process,
	"runtime.ReadMemStats": Process,

	// Real deadlines and timers, and a goroutine on cancel.
	"context.WithTimeout":       Clock,
	"context.WithTimeoutCause":  Clock,
	"context.WithDeadline":      Clock,
	"context.WithDeadlineCause": Clock,
	"context.AfterFunc":         Go,

	"path/filepath.Abs":          FS,
	"path/filepath.IsAbs":        Rooted,
	"path/filepath.EvalSymlinks": FS,
	"path/filepath.Glob":         FS,
	"path/filepath.Walk":         FS,
	"path/filepath.WalkDir":      FS,

	"fmt.Print":   Stdio,
	"fmt.Printf":  Stdio,
	"fmt.Println": Stdio,
	"fmt.Scan":    Stdio,
	"fmt.Scanf":   Stdio,
	"fmt.Scanln":  Stdio,

	"log/slog.Debug":             Stdio,
	"log/slog.DebugContext":      Stdio,
	"log/slog.Info":              Stdio,
	"log/slog.InfoContext":       Stdio,
	"log/slog.Warn":              Stdio,
	"log/slog.WarnContext":       Stdio,
	"log/slog.Error":             Stdio,
	"log/slog.ErrorContext":      Stdio,
	"log/slog.Log":               Stdio,
	"log/slog.LogAttrs":          Stdio,
	"log/slog.Default":           Stdio,
	"log/slog.SetDefault":        Stdio,
	"log/slog.SetLogLoggerLevel": Stdio,
}

// bannedMethods maps "pkgpath.Type.Method" of methods to the rule that
// bans them: a goroutine without a go statement, and iteration in an
// order the runtime picks at random.
var bannedMethods = map[string]Rule{
	"sync.WaitGroup.Go":      Go,
	"time.Time.Local":        Clock,
	"sync.Map.Range":         MapOrder,
	"reflect.Value.MapKeys":  MapOrder,
	"reflect.Value.MapRange": MapOrder,
	"reflect.Value.Seq":      MapOrder,
	"reflect.Value.Seq2":     MapOrder,
}

// bannedBuiltins are the builtins that write to standard error.
var bannedBuiltins = map[string]Rule{"print": Stdio, "println": Stdio}

// osPure lists the package-level os functions that touch nothing.
var osPure = map[string]bool{
	"IsExist":         true,
	"IsNotExist":      true,
	"IsPermission":    true,
	"IsTimeout":       true,
	"IsPathSeparator": true,
	"NewSyscallError": true,
	"SameFile":        true,
	"Expand":          true,
}

var osEnv = map[string]bool{
	"Getenv": true, "LookupEnv": true, "Environ": true, "Setenv": true,
	"Unsetenv": true, "Clearenv": true, "ExpandEnv": true,
}

var osProcess = map[string]bool{
	"Args": true, "Exit": true, "Executable": true, "Hostname": true,
	"Getpid": true, "Getppid": true, "Getuid": true, "Geteuid": true,
	"Getgid": true, "Getegid": true, "Getgroups": true, "Getwd": true,
	"Chdir": true, "StartProcess": true, "FindProcess": true,
	"Getpagesize": true, "Interrupt": true, "Kill": true,
}

// osBan classifies a package-level os function or variable. Types,
// constants and the Err* values never reach it.
func osBan(name string) (Rule, bool) {
	switch {
	case osPure[name]:
		return 0, false
	case osEnv[name]:
		return Getenv, true
	case name == "Stdin" || name == "Stdout" || name == "Stderr":
		return Stdio, true
	case osProcess[name]:
		return Process, true
	}
	return FS, true
}

// logBan reports whether a package-level function of package log is
// banned: all of them write to the shared default logger except New.
func logBan(name string) bool { return name != "New" }

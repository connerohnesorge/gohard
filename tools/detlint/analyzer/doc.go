// Package analyzer implements detlint, the go/analysis pass that keeps
// GoHard's core packages deterministic (#4 P1, P2, P5; #29; #30).
//
// The outside world reaches GoHard only through the doors of one
// env.Env value: Clock, Rand, FS, Transport, Executor, Getenv, Jobs and
// Order. detlint reports every other way in.
//
// # Rules
//
// Each finding belongs to one [Rule]; its name is the diagnostic
// category.
//
//   - clock: time.Now, time.Sleep, the timers (After, AfterFunc,
//     NewTimer, NewTicker, Tick), time.Since, time.Until,
//     time.LoadLocation and time.Local (which reads TZ), time.Unix,
//     UnixMilli and UnixMicro (which return times in the process's
//     zone; allowed when converted straight away, time.Unix(s,
//     ns).UTC()), the (time.Time).Local method, and the real deadlines
//     of context.WithTimeout, WithTimeoutCause, WithDeadline and
//     WithDeadlineCause.
//   - rand: importing math/rand, math/rand/v2, crypto/rand or
//     hash/maphash (process-random seeds).
//   - getenv: os.Getenv, LookupEnv, Environ, Setenv, Unsetenv,
//     Clearenv and ExpandEnv, and importing flag, which reads os.Args
//     (allowed in tests, for -update).
//   - fs: every package-level function or variable of os that is not
//     on the pure allowlist (types, constants, the Err* values,
//     IsExist, IsNotExist, IsPermission, IsTimeout, IsPathSeparator,
//     NewSyscallError, SameFile, Expand), importing io/ioutil, and the
//     file-system walkers of path/filepath (Abs, EvalSymlinks, Glob,
//     Walk, WalkDir).
//   - stdio: os.Stdin, os.Stdout, os.Stderr, fmt.Print*, fmt.Scan*,
//     the package-level log functions other than log.New, and the
//     package-level log/slog logging functions, and the print and
//     println builtins.
//   - net: importing net or any net/... package except the pure
//     parsers net/url and net/netip.
//   - process: importing os/exec, os/signal, os/user, syscall or
//     plugin, and the process parts of os (Args, Exit, Executable,
//     Hostname, Getpid and the other Get*id calls, Getwd, Chdir,
//     StartProcess, FindProcess, Getpagesize), and the machine facts
//     runtime.NumCPU, NumGoroutine, GOMAXPROCS and ReadMemStats.
//   - go: go statements, (*sync.WaitGroup).Go and context.AfterFunc,
//     which start goroutines without one, and runtime.SetFinalizer,
//     runtime.AddCleanup and importing weak, whose callbacks and nil
//     pointers follow the garbage collector. Parallel pure work runs
//     through env.Jobs.
//   - select: select statements.
//   - realenv: importing internal/env/realenv. Only cmd/ builds the
//     real Env; everything else takes an env.Env.
//   - maporder: map iteration whose order can reach output (below),
//     and the iterations whose order the runtime randomizes outside a
//     range statement: (*sync.Map).Range and reflect.Value's MapKeys,
//     MapRange, Seq and Seq2.
//   - linkname: //go:linkname directives, which bind to runtime
//     internals such as runtime.nanotime past every door.
//   - address: importing unsafe (pointer-to-integer conversions order
//     or hash by address) and a constant fmt format with a %p verb.
//     Addresses change from run to run. Not covered: %v of a pointer
//     to a non-struct value, which also prints an address.
//   - rooted: filepath.IsAbs, which is false on Windows for a name
//     rooted without a volume (/work in simenv, \x on the OS). Use
//     env.IsRooted, which treats such names as env.Abs does. Only
//     internal/env itself (that package, not its subpackages) may call
//     filepath.IsAbs.
//
// Uses are found through type information, so aliases, dot imports
// and function values (f := time.Now) are caught as well as calls.
//
// # The map-order rule
//
// A range over a map (including a type parameter whose type set holds
// only maps) or over maps.Keys, maps.Values or maps.All is reported
// unless its body is order-independent. A body is order-independent
// when every statement in it is one of:
//
//   - s = append(s, x...) where s is a local variable that is sorted
//     totally before anything can observe it. The sort must be a
//     statement of the list holding the loop, or of an enclosing list
//     reached by falling out of if, switch, block and loop statements
//     (leaving a loop body only when nothing else in that loop
//     mentions s). Every statement between the loop and the sort may
//     only append more to s; none may read, copy or output s, return,
//     break, continue, goto or carry a label. The sort takes s itself
//     (not s[:n]) and is total: sort.Strings, Ints, Float64s,
//     slices.Sort; sort.Sort or Stable of sort.StringSlice, IntSlice or
//     Float64Slice of s (optionally under sort.Reverse);
//     slices.SortFunc or SortStableFunc with cmp.Compare,
//     strings.Compare, or func(a, b T) int { return X } where X is a
//     Compare of a and b or a cmp.Or of Compares of fields that
//     together cover every field of T; sort.Slice or SliceStable with
//     func(i, j int) bool { return s[i] < s[j] } (or >). A comparator
//     that can tie distinct elements (one field, a length, return 0)
//     keeps their map order and is not accepted. A slice declared
//     inside the loop body needs no sort.
//   - a write into an index that is the range's own key: m2[k] = x.
//     Keys are distinct, so every iteration writes its own slot. Any
//     other index (out[0] = v, inv[v] = k) may be written twice and
//     keep the last value, so it is reported unless it stores a
//     constant.
//   - a commutative aggregate: x += y, x -= y, x *= y, x |= y,
//     x &= y, x ^= y, x &^= y on integers; x++ and x-- on numbers;
//     x = min(x, y) and x = max(x, y). Each target may receive one
//     kind of update only (+= with -= and ++, &= with &^=); mixing
//     kinds (x += v; x *= 3, or max then min) does not commute.
//     Floating-point += is not associative and string += is
//     concatenation, so both are reported.
//   - an idempotent store of a constant (found = true), the same
//     constant for every store to that target, or any assignment to a
//     variable declared inside the loop body. Writes through a
//     pointer, or to a field or element reached through a pointer,
//     slice or map held by a loop variable (p := &x; *p = v) may alias
//     outer state and are not loop-local.
//   - delete(m, k); an unlabeled continue; return whose results are
//     all constants or nil.
//   - if, switch, for and range statements (and blocks) whose parts
//     follow these rules; inside them an unlabeled break is allowed
//     when it leaves that inner statement. A nested map range is
//     judged on its own.
//
// Every expression in such a body must be pure: no channel receive,
// no function literal, and calls only to builtins other than append,
// conversions, String() and Error() methods, and the pure standard
// functions listed in pureFuncs (strings, strconv, unicode, math, cmp,
// path, errors, bytes, the non-mutating slices and maps functions,
// fmt.Sprint*, fmt.Errorf and the lexical path/filepath functions).
// User functions count as impure because detlint cannot see their
// effects; the fix is the idiom the rule pushes toward anyway:
//
//	for _, k := range slices.Sorted(maps.Keys(m)) { ... }
//
// Outside a range, a maps.Keys, maps.Values or maps.All call must be
// the argument of slices.Sorted, slices.SortedFunc,
// slices.SortedStableFunc, maps.Collect or maps.Insert, or the
// argument of a slices.Collect whose result is assigned to a variable
// that is then sorted under the same rule as appends. Using one of the
// three as a function value (k := maps.Keys[M]) is reported, since
// calls through the value cannot be judged.
//
// Not covered: user iterators that range over a map internally, and a
// body that reads an aggregate it is still building (x += v; if x > 9
// {...}). Appends to fields (s.f = append...) are reported
// conservatively.
//
// # Scopes
//
// Rules depend on the package and the file ([Rules]):
//
//   - cmd/... and internal/env/realenv/... of the GoHard module: no
//     rules. They are where the real world is wired in (#29, #30).
//   - internal/jsonrpc2 of the GoHard module (that package only, not
//     its subpackages): every rule except go and select, in its _test.go
//     files too. It owns the stdio reader and writer goroutines, the
//     only goroutines in GoHard (#4 P8, #25); the single-threaded
//     server waits on input and timers through it, so no other package
//     needs a select.
//   - tools/... of the GoHard module: only maporder. Tools are dev-time
//     programs (generators, the GHDL/NVC oracle) that may touch the
//     world, but their output is committed and checked by the
//     generated-file sync gate, so it must not depend on map order.
//   - _test.go files in core packages: getenv, fs, stdio, realenv,
//     maporder and rooted are allowed, so tests may read and update
//     golden files, use realenv for fixture harnesses, assert over
//     maps (only failure messages could be reordered) and check OS
//     paths. clock, rand, net,
//     process, go, select, linkname and address stay banned: a test
//     must not sleep, roll dice, touch the network, spawn programs or
//     race. The one select a test
//     may use is a non-blocking poll of a single channel (one case
//     plus default), which can neither block nor choose at random.
//   - everything else, including packages outside the GoHard module:
//     every rule.
package analyzer

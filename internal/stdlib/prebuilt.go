package stdlib

// prebuilt is the analyzed form of the bundled libraries, embedded at
// build time (#33: std and ieee are analyzed when GoHard is built, so
// neither the editor nor `gohard check` analyzes them at startup). It
// is empty until analysis exists: the end of S4b (#34) adds the
// generator (`go generate ./internal/stdlib` analyzing Files in order
// and writing prebuilt.bin), a `//go:embed prebuilt.bin` on this
// variable, a sync check in scripts/ci, and the test that startup
// performs no analysis of std or ieee.
var prebuilt []byte

// Prebuilt returns the embedded analyzed libraries, or nil when the
// build has none yet (callers then analyze Files themselves). The bytes
// are shared: callers must not modify them.
func Prebuilt() []byte { return prebuilt }

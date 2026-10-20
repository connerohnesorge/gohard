package stdlib

import _ "embed"

// prebuilt is the analyzed form of the bundled libraries, written at
// build time (#33: std and ieee are analyzed when GoHard is built, so
// neither the editor nor `gohard check` analyzes them at startup). go
// generate ./internal/sema writes it (tools/prebuild, with the codec of
// internal/sema); the generated-file sync check fails a stale copy.
//
//go:embed prebuilt.bin
var prebuilt []byte

// Prebuilt returns the embedded analyzed libraries (decoded by
// sema.Bundle). The bytes are shared: callers must not modify them.
func Prebuilt() []byte { return prebuilt }

// Command prebuild writes internal/stdlib/prebuilt.bin: the std/ieee
// bundle analyzed at build time and stored with the codec of
// internal/sema/codec.go (#33). The binary embeds the file, so neither
// the editor nor `gohard check` analyzes std or ieee at startup.
//
//	go generate ./internal/sema    # runs gencodec, then prebuild
//
// The output is a pure function of the bundled sources and the sema
// code, so the generated-file sync check (#30) fails when either
// changes and the file is not regenerated.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/connerohnesorge/gohard/v2/internal/sema"
)

const outPath = "internal/stdlib/prebuilt.bin"

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	b := sema.PrebuildBundle()
	name := filepath.Join(*root, filepath.FromSlash(outPath))
	if old, err := os.ReadFile(name); err == nil && bytes.Equal(old, b) {
		return
	}
	if err := os.WriteFile(name, b, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "prebuild:", err)
		os.Exit(1)
	}
}

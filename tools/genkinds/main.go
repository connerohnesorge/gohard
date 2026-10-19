// Command genkinds writes the files generated from internal/cst/kinds.yaml
// and internal/token/keywords.yaml (#10, #31): the token kind enum and
// keyword table, the CST node kind enum with names, introducing standards
// and groups, the typed CST wrappers, and the generated test inputs (the
// keyword edge values for the lexer and the edit-replay texts for the
// parser). It never writes parsing code.
//
// It runs from `go generate ./internal/cst`; CI reruns it and fails when
// a committed file differs (#30).
//
// Usage:
//
//	go run ./tools/genkinds [-root DIR] [-check]
//
// -root is the repository root (default "."). -check writes nothing and
// exits 1 when a file is stale.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false, "report stale files, write nothing")
	flag.Parse()
	files, err := Generate(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "genkinds:", err)
		os.Exit(2)
	}
	stale := false
	for _, f := range files {
		path := filepath.Join(*root, filepath.FromSlash(f.Path))
		old, _ := os.ReadFile(path)
		if bytes.Equal(old, f.Data) {
			continue
		}
		if *check {
			fmt.Fprintln(os.Stderr, "genkinds: stale:", f.Path)
			stale = true
			continue
		}
		if err := os.WriteFile(path, f.Data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "genkinds:", err)
			os.Exit(2)
		}
	}
	if stale {
		os.Exit(1)
	}
}

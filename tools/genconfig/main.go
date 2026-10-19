// Command genconfig writes the files generated from the gohard.yaml key
// table in internal/config (#28): schema/gohard.schema.json (JSON Schema
// 2020-12) and docs/config.md. It runs from `go generate
// ./internal/config`; CI reruns it and fails when a committed file
// differs (#30), and internal/config's tests check the same thing.
//
// Usage:
//
//	go run ./tools/genconfig [-root DIR] [-check]
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

	"github.com/connerohnesorge/gohard/internal/config"
)

// Outputs maps each generated file (slash path from the repository root)
// to its content.
func outputs() []struct {
	path string
	data []byte
} {
	return []struct {
		path string
		data []byte
	}{
		{"schema/gohard.schema.json", config.Schema()},
		{"docs/config.md", config.Markdown()},
	}
}

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false,
		"write nothing; exit 1 if a generated file is stale")
	flag.Parse()
	stale := false
	for _, o := range outputs() {
		name := filepath.Join(*root, filepath.FromSlash(o.path))
		old, err := os.ReadFile(name)
		if err == nil && bytes.Equal(old, o.data) {
			continue
		}
		if *check {
			fmt.Fprintf(os.Stderr, "genconfig: %s is stale; run go "+
				"generate ./internal/config\n", o.path)
			stale = true
			continue
		}
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(name, o.data, 0o644); err != nil {
			fail(err)
		}
	}
	if stale {
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genconfig:", err)
	os.Exit(1)
}

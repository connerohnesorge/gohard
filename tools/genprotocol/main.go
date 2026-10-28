// Command genprotocol writes internal/protocol/protocol_gen.go, the LSP
// 3.17 types GoHard's server uses, from the vendored metaModel.json
// (#25). It runs from `go generate ./internal/protocol`; CI reruns it
// and fails when the committed file differs (#30), and
// internal/protocol's tests check the same thing.
//
// metaModel.json is the LSP 3.17 meta model from
// microsoft/vscode-languageserver-node, tag release/protocol/3.17.5,
// file protocol/metaModel.json (MIT, see LICENSE in this directory).
// See README.md for its hash and how to update it.
//
// Usage:
//
//	go run ./tools/genprotocol [-root DIR] [-check]
//
// -root is the repository root (default "."). -check writes nothing
// and exits 1 when the output is stale.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/connerohnesorge/gohard/v2/tools/genprotocol/gen"
)

func main() {
	root := flag.String("root", ".", "repository root")
	check := flag.Bool("check", false,
		"write nothing; exit 1 if the output is stale")
	flag.Parse()
	model := filepath.Join(*root, "tools", "genprotocol", "metaModel.json")
	out := filepath.Join(*root, "internal", "protocol", "protocol_gen.go")
	data, err := os.ReadFile(model)
	if err != nil {
		fail(err)
	}
	src, err := gen.Generate(data)
	if err != nil {
		fail(err)
	}
	old, err := os.ReadFile(out)
	if err == nil && bytes.Equal(old, src) {
		return
	}
	if *check {
		fmt.Fprintf(os.Stderr, "genprotocol: %s is stale; run go "+
			"generate ./internal/protocol\n", out)
		os.Exit(1)
	}
	if err := os.WriteFile(out, src, 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genprotocol:", err)
	os.Exit(1)
}

// Command detlint runs the detlint analyzer, which keeps GoHard's core
// packages deterministic (#30). Usage:
//
//	go run ./tools/detlint ./...
//
// See package analyzer for the rules and scopes.
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/connerohnesorge/gohard/v2/tools/detlint/analyzer"
)

func main() { singlechecker.Main(analyzer.Analyzer) }

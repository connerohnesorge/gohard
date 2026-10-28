package sema

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
)

// analyze runs Analyze on files (path -> text) with the default
// configuration changed by cfg.
func analyze(t testing.TB, files map[string]string,
	cfg func(*config.Config),
) *Result {
	t.Helper()
	c := config.Defaults()
	if cfg != nil {
		cfg(&c)
	}
	var srcs []Source
	for _, p := range slices.Sorted(mapsKeys(files)) {
		srcs = append(srcs, Source{Path: p, Text: []byte(files[p])})
	}
	return Analyze(Input{Files: srcs, Config: c})
}

func mapsKeys(m map[string]string) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// diagText renders diagnostics one per line as path:line:col: code:
// message.
func diagText(r *Result) string {
	var b strings.Builder
	for _, d := range r.Diagnostics {
		fmt.Fprintf(&b, "%s:%d:%d: %s: %s\n", d.Path, d.Line, d.Col,
			d.Code, d.Message)
	}
	return b.String()
}

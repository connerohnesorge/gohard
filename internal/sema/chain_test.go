package sema

import (
	"strings"
	"testing"
)

// TestLongChains: a generated constant with a 100,000-term operator
// chain (a left-nested tree as deep as it has terms) analyzes without
// errors in linear time. The unsupported-construct walk used to
// ask every node whether it is inactive by walking its ancestors,
// which took minutes on this input; the package test timeout bounds
// the run.
func TestLongChains(t *testing.T) {
	if testing.Short() {
		t.Skip("long input")
	}
	const n = 100_000
	sum := strings.Repeat("1 + ", n-1) + "1"
	cat := strings.Repeat(`"ab" & `, n-1) + `"ab"`
	src := "package p is\n  constant d : integer := " + sum + ";\n" +
		"  constant s : string := " + cat + ";\nend;\n"
	r := analyze(t, map[string]string{"p.vhd": src}, nil)
	// Evaluating the 200,000-character string by repeated copying
	// exceeds the step budget: that is the step-limit warning, not an
	// error.
	for _, d := range r.Diagnostics {
		if d.Code != "sema/eval-limit" {
			t.Errorf("%d:%d: %s: %.200s", d.Line, d.Col, d.Code,
				d.Message)
		}
	}
}

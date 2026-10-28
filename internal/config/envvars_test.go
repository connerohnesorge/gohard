package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// TestApplyEnvVars: --env overrides simulation.env.vars by exact name,
// a later --env wins, every override is logged, the result stays
// sorted and c is not modified.
func TestApplyEnvVars(t *testing.T) {
	c := Defaults()
	c.Simulation.Env.Vars = []Var{{Name: "HOME", Value: "/h"},
		{Name: "home", Value: "lower"}}
	got, logs, ds := ApplyEnvVars(c, []string{"Z=1", "HOME=/x",
		"A==b", "Z=2", "EMPTY="})
	if len(ds) != 0 {
		t.Fatalf("diagnostics %v", ds)
	}
	want := []Var{{"A", "=b"}, {"EMPTY", ""}, {"HOME", "/x"},
		{"Z", "2"}, {"home", "lower"}}
	if !slices.Equal(got.Simulation.Env.Vars, want) {
		t.Errorf("vars %v, want %v", got.Simulation.Env.Vars, want)
	}
	wantLogs := []string{
		`--env HOME=/x overrides simulation.env.vars.HOME: "/h"`,
		"--env Z=2 overrides --env Z=1"}
	if !slices.Equal(logs, wantLogs) {
		t.Errorf("logs %q, want %q", logs, wantLogs)
	}
	if c.Simulation.Env.Vars[0].Value != "/h" {
		t.Error("ApplyEnvVars modified its argument")
	}
}

// TestApplyEnvVarsNoAlias: the caller's vars (unsorted, with spare
// capacity) are neither overridden nor sorted in place.
func TestApplyEnvVarsNoAlias(t *testing.T) {
	in := make([]Var, 0, 8)
	in = append(in, Var{"Z", "z"}, Var{"B", "b"}, Var{"A", "a"})
	keep := slices.Clone(in)
	c := Defaults()
	c.Simulation.Env.Vars = in
	got, _, _ := ApplyEnvVars(c, []string{"B=new", "Z=new"})
	if !slices.Equal(in, keep) || !slices.Equal(c.Simulation.Env.Vars,
		keep) {
		t.Errorf("input changed to %v, want %v", in, keep)
	}
	want := []Var{{"A", "a"}, {"B", "new"}, {"Z", "new"}}
	if !slices.Equal(got.Simulation.Env.Vars, want) {
		t.Errorf("vars %v, want %v", got.Simulation.Env.Vars, want)
	}
}

func TestApplyEnvVarsUsage(t *testing.T) {
	for _, arg := range []string{"NOEQUALS", "=v", "A\x00=v"} {
		_, _, ds := ApplyEnvVars(Defaults(), []string{arg})
		if len(ds) != 1 || ds[0].Code != diag.CLIUsage ||
			!strings.Contains(ds[0].Message, "--env") {
			t.Errorf("--env %q: %v", arg, ds)
		}
	}
}

// TestApplyEnvVarsOrder: the result depends only on the last --env per
// name, never on how the distinct names are ordered.
func TestApplyEnvVarsOrder(t *testing.T) {
	args := []string{"B=2", "A=1", "C=3", "D=4", "E=5"}
	ref, _, _ := ApplyEnvVars(Defaults(), args)
	for seed := range uint64(30) {
		r := &env.SplitMix64{State: seed}
		p := slices.Clone(args)
		for i := len(p) - 1; i > 0; i-- {
			j := env.Intn(r, i+1)
			p[i], p[j] = p[j], p[i]
		}
		got, _, _ := ApplyEnvVars(Defaults(), p)
		if !slices.Equal(got.Simulation.Env.Vars,
			ref.Simulation.Env.Vars) {
			t.Errorf("order %q gave %v", p, got.Simulation.Env.Vars)
		}
	}
}

func TestFormatTimestampRoundTrip(t *testing.T) {
	for _, s := range []string{"2000-01-01T00:00:00Z",
		"2026-10-21T12:00:00.5Z", "1969-12-31T23:59:59.999999999Z"} {
		ts, ok := ParseTimestamp(s)
		if !ok || FormatTimestamp(ts) != s {
			t.Errorf("%s -> %v %v -> %s", s, ts, ok, FormatTimestamp(ts))
		}
	}
}

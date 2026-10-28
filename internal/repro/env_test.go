package repro

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
)

// TestPreV11HeaderStable: a v1.0 file (no "env" key) still reads, and
// writes back byte for byte without one; a v1.1 header always has it.
func TestPreV11HeaderStable(t *testing.T) {
	old, err := Marshal(sample())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(old, []byte(`"env"`)) {
		t.Fatalf("a header without Env wrote an env key:\n%s", old)
	}
	h, err := Unmarshal(old)
	if err != nil || h.Env != nil {
		t.Fatalf("v1.0 header: %v, env %+v", err, h.Env)
	}
	again, _ := Marshal(h)
	if !bytes.Equal(old, again) {
		t.Errorf("v1.0 header not byte-stable:\n%s\n%s", old, again)
	}
	h.Env = NewEnv(config.Defaults().Simulation.Env, false)
	b, err := Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	want := `"env": {
    "epoch": "2000-01-01T00:00:00Z",
    "vars": {},
    "real": false
  }`
	if !bytes.Contains(b, []byte(want)) {
		t.Errorf("default env not recorded as\n%s\ngot\n%s", want, b)
	}
	if _, err := Unmarshal(bytes.Replace(b, []byte(`"real": false`),
		[]byte(`"real": null`), 1)); err == nil {
		t.Error("a null env.real was accepted")
	}
}

// TestEnvApply: NewEnv then Apply gives back the simulation.env values,
// and a nil Env leaves them alone.
func TestEnvApply(t *testing.T) {
	s := config.SimEnv{Epoch: time.Date(2026, 10, 21, 1, 2, 3, 4,
		time.UTC), Vars: []config.Var{{Name: "A", Value: "1"},
		{Name: "b", Value: ""}}}
	e := NewEnv(s, true)
	if !e.Real || e.Epoch != "2026-10-21T01:02:03.000000004Z" {
		t.Errorf("recorded %+v", e)
	}
	got := e.Apply(config.SimEnv{})
	if !got.Epoch.Equal(s.Epoch) || len(got.Vars) != 2 ||
		got.Vars[0] != s.Vars[0] || got.Vars[1] != s.Vars[1] {
		t.Errorf("applied %+v, want %+v", got, s)
	}
	var none *Env
	if r := none.Apply(s); !r.Epoch.Equal(s.Epoch) || len(r.Vars) != 2 {
		t.Errorf("nil Env changed the values: %+v", r)
	}
}

// TestEnvValidate rejects a bad epoch and bad variable names.
func TestEnvValidate(t *testing.T) {
	for _, e := range []Env{{Epoch: "yesterday"},
		{Epoch: "2000-01-01T00:00:00+01:00"},
		{Epoch: "2000-01-01T00:00:00Z", Vars: map[string]string{"": "x"}},
		{Epoch: "2000-01-01T00:00:00Z",
			Vars: map[string]string{"A=B": "x"}}} {
		h := sample()
		h.Env = &e
		if _, err := Marshal(h); err == nil {
			t.Errorf("env %+v accepted", e)
		}
	}
}

// TestRunDir: the run directory is a function of the run's identity
// only: the build facts never change it; the seed, testbench, sources,
// config and environment always do.
func TestRunDir(t *testing.T) {
	base := sample()
	base.Env = NewEnv(config.Defaults().Simulation.Env, false)
	d := RunDir(base)
	if !strings.HasPrefix(d, RunDirPrefix+"/") ||
		len(d) != len(RunDirPrefix)+1+16 ||
		strings.Trim(d[len(RunDirPrefix)+1:], "0123456789abcdef") != "" {
		t.Fatalf("RunDir = %q", d)
	}
	same := base
	same.Version, same.Commit, same.IEEE, same.Go = "9", "c", "i", "g"
	same.Sources = []File{base.Sources[1], base.Sources[0]}
	if RunDir(same) != d {
		t.Error("the build or the source order changed the run dir")
	}
	changes := []func(*Header){
		func(h *Header) { h.Seed++ },
		func(h *Header) { h.Testbench = "other" },
		func(h *Header) { h.Standard = "2019" },
		func(h *Header) { h.Defines = map[string]string{"X": "1"} },
		func(h *Header) { h.Budgets.Step++ },
		func(h *Header) { h.Config = nil },
		func(h *Header) { h.Sources = h.Sources[:1] },
		func(h *Header) {
			h.Env = &Env{Epoch: "2001-01-01T00:00:00Z"}
		},
		func(h *Header) {
			h.Env = &Env{Epoch: base.Env.Epoch,
				Vars: map[string]string{"K": "V"}}
		},
		func(h *Header) { h.Env = &Env{Epoch: base.Env.Epoch, Real: true} },
	}
	for i, f := range changes {
		h := base
		h.Sources = append([]File(nil), base.Sources...)
		f(&h)
		if RunDir(h) == d {
			t.Errorf("change %d kept the run dir", i)
		}
	}
	if RunDir(base) != d {
		t.Error("RunDir changed its argument")
	}
}

// TestVerifyRealEnv: a --real-env recording is never replayed
// (repro/non-reproducible, tool class), even with unchanged sources.
func TestVerifyRealEnv(t *testing.T) {
	s := simenv.New(simenv.Options{Dir: "/proj",
		Files: map[string][]byte{"src/a.vhd": []byte("entity a is end;")}})
	var fsys env.FS = s.Env().FS
	srcs, err := HashFiles(fsys, "/proj", []string{"src/a.vhd"})
	if err != nil {
		t.Fatal(err)
	}
	h := sample()
	h.Sources, h.Config = srcs, nil
	h.Env = NewEnv(config.Defaults().Simulation.Env, true)
	c, err := Verify(fsys, "/proj", h, h.Version)
	if err != nil {
		t.Fatal(err)
	}
	ds := c.Diagnostics("r.json")
	if c.OK() || len(ds) != 1 ||
		ds[0].Code != diag.ReproNonReproducible ||
		ds[0].Class() != diag.Tool {
		t.Errorf("ok %v, diagnostics %+v", c.OK(), ds)
	}
	h.Env.Real = false
	if c, _ := Verify(fsys, "/proj", h, h.Version); !c.OK() {
		t.Errorf("a reproducible recording was refused: %+v", c)
	}
}

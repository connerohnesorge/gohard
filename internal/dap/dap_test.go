package dap_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/dap"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/sema"
	"github.com/connerohnesorge/gohard/v2/internal/simdbg"
	"github.com/connerohnesorge/gohard/v2/internal/simrun"
	"github.com/connerohnesorge/gohard/v2/internal/stdlib"
)

// handle sends one request and returns the messages, decoded.
func handle(t *testing.T, s *dap.Server, seq int, command string,
	args any,
) []map[string]any {
	t.Helper()
	b, err := json.Marshal(map[string]any{"seq": seq, "type": "request",
		"command": command, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, m := range s.Handle(b) {
		var v map[string]any
		if err := json.Unmarshal(m, &v); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		out = append(out, v)
	}
	return out
}

// TestProtocol checks the requests that need no session: the
// capabilities, a failed launch, breakpoints before a launch, an
// unknown request, messages that are not requests, and disconnect.
func TestProtocol(t *testing.T) {
	s := dap.New(dap.Options{Launch: func(dap.LaunchArgs) (
		*dap.Launched, error,
	) {
		return nil, errors.New("no testbench nope")
	}})
	ms := handle(t, s, 1, "initialize", map[string]any{
		"adapterID": "gohard", "linesStartAt1": true})
	body, _ := ms[0]["body"].(map[string]any)
	if len(ms) != 1 || ms[0]["success"] != true ||
		body["supportsStepBack"] != true ||
		body["supportsConfigurationDoneRequest"] != true {
		t.Fatalf("initialize: %v", ms)
	}
	ms = handle(t, s, 2, "launch", map[string]any{"testbench": "nope"})
	if len(ms) != 1 || ms[0]["success"] != false ||
		ms[0]["message"] != "no testbench nope" ||
		ms[0]["request_seq"] != 2.0 {
		t.Fatalf("failed launch: %v", ms)
	}
	ms = handle(t, s, 3, "setBreakpoints", map[string]any{
		"source":      map[string]any{"path": "/x/tb.vhd"},
		"breakpoints": []any{map[string]any{"line": 4}}})
	bps, _ := ms[0]["body"].(map[string]any)["breakpoints"].([]any)
	if len(bps) != 1 || bps[0].(map[string]any)["verified"] != false {
		t.Fatalf("breakpoints before launch: %v", ms)
	}
	for i, c := range []string{"next", "threads", "evaluate"} {
		ms = handle(t, s, 4+i, c, map[string]any{})
		if len(ms) != 1 || c == "threads" && ms[0]["success"] != true ||
			c != "threads" && ms[0]["success"] != false {
			t.Fatalf("%s: %v", c, ms)
		}
	}
	msg, _ := ms[0]["message"].(string)
	if !strings.Contains(msg, "not supported") {
		t.Errorf("evaluate: %v", ms)
	}
	if out := s.Handle([]byte(`{"seq":9,"type":"event","event":"x"}`)); out != nil {
		t.Errorf("an event got %q", out)
	}
	if out := s.Handle([]byte(`not json`)); out != nil {
		t.Errorf("a broken body got %q", out)
	}
	ms = handle(t, s, 10, "disconnect", map[string]any{})
	if len(ms) != 1 || ms[0]["success"] != true || !s.Done() {
		t.Fatalf("disconnect: %v", ms)
	}
	ms = handle(t, s, 11, "threads", nil)
	if len(ms) != 1 || ms[0]["success"] != false {
		t.Fatalf("after disconnect: %v", ms)
	}
}

// TestBundledFrame: stepping into a subprogram of a bundled std/ieee
// file (ieee.math_real's UNIFORM, which the interpreter runs) gives a
// frame whose source has no path, which would name a file of the
// project, but a sourceReference; the source request answers it with
// the bundled text, where the frame's line is UNIFORM's first
// statement.
func TestBundledFrame(t *testing.T) {
	const src = `library ieee;
use ieee.math_real.all;

entity tb is
end entity tb;

architecture sim of tb is
begin
  process
    variable s1 : positive := 7;
    variable s2 : positive := 11;
    variable r  : real;
  begin
    uniform(s1, s2, r);
    report "r = " & real'image(r);
    wait;
  end process;
end architecture sim;
`
	cfg := config.Defaults()
	res := sema.Analyze(sema.Input{Files: []sema.Source{{Path: "tb.vhd",
		Text: []byte(src)}}, Config: cfg})
	for _, d := range res.Diagnostics {
		if d.Severity == diag.Error {
			t.Fatalf("%s:%d:%d: %s", d.Path, d.Line, d.Col, d.Message)
		}
	}
	root := filepath.FromSlash(simenv.DefaultDir)
	o := simrun.Options{Result: res, Sim: cfg.Simulation, Testbench: "tb",
		Env: simrun.Env{Root: root,
			FS: simenv.New(simenv.Options{Dir: root}).FS}}
	s := dap.New(dap.Options{Launch: func(dap.LaunchArgs) (
		*dap.Launched, error,
	) {
		return &dap.Launched{Session: simdbg.Start(o), Root: root,
			Report: &diag.Report{}, Testbench: "tb"}, nil
	}})
	tb := filepath.Join(root, "tb.vhd")
	handle(t, s, 1, "initialize", map[string]any{"adapterID": "gohard"})
	handle(t, s, 2, "launch", map[string]any{"testbench": "tb"})
	handle(t, s, 3, "setBreakpoints", map[string]any{
		"source":      map[string]any{"path": tb},
		"breakpoints": []any{map[string]any{"line": 14}}})
	stopped := func(ms []map[string]any, reason string) {
		t.Helper()
		last := ms[len(ms)-1]
		body, _ := last["body"].(map[string]any)
		if last["event"] != "stopped" || body["reason"] != reason {
			t.Fatalf("want a stop for %s: %v", reason, ms)
		}
	}
	stopped(handle(t, s, 4, "configurationDone", map[string]any{}),
		"breakpoint")
	stopped(handle(t, s, 5, "stepIn", map[string]any{"threadId": 1}),
		"step")
	ms := handle(t, s, 6, "stackTrace", map[string]any{"threadId": 1})
	var st struct {
		Body struct {
			StackFrames []struct {
				Name   string
				Line   int
				Source struct {
					Name, Path      string
					SourceReference int
				}
			}
		}
	}
	b, _ := json.Marshal(ms[0])
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	fs := st.Body.StackFrames
	if len(fs) != 2 || fs[0].Name != "UNIFORM" ||
		fs[0].Source.Name != "math_real-body.vhdl" ||
		fs[0].Source.Path != "" || fs[0].Source.SourceReference <= 0 ||
		fs[1].Source.Path != tb || fs[1].Line != 14 ||
		fs[1].Source.SourceReference != 0 {
		t.Fatalf("frames %+v", fs)
	}
	ref := fs[0].Source.SourceReference
	ms = handle(t, s, 7, "source", map[string]any{
		"source":          map[string]any{"sourceReference": ref},
		"sourceReference": ref})
	body, _ := ms[0]["body"].(map[string]any)
	text, _ := body["content"].(string)
	want, _ := stdlib.Source("ieee/math_real-body.vhdl")
	if text != string(want) {
		t.Fatalf("source %d: %v", ref, ms)
	}
	line := strings.TrimSpace(strings.Split(text, "\n")[fs[0].Line-1])
	if !strings.HasPrefix(line, "if SEED1 > 2147483562 then") {
		t.Errorf("frame line %d of the bundled text is %q", fs[0].Line,
			line)
	}
	for i, r := range []int{0, 1000} {
		ms = handle(t, s, 8+i, "source", map[string]any{
			"sourceReference": r})
		if len(ms) != 1 || ms[0]["success"] != false {
			t.Errorf("source %d: %v", r, ms)
		}
	}
}

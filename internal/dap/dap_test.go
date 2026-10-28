package dap_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/internal/dap"
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

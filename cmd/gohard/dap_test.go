package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// dapClient is a scripted Debug Adapter Protocol client over the real
// stdio pipes of `gohard dap`, sending what nvim-dap sends. Events
// that arrive while it waits for a response are kept for event.
type dapClient struct {
	io     *lspClient
	seq    int
	events []map[string]json.RawMessage
}

// next returns the next message within 60 s of wall time.
func (d *dapClient) next(what string) map[string]json.RawMessage {
	d.io.t.Helper()
	select {
	case m := <-d.io.msgs:
		return m
	case err := <-d.io.errs:
		d.io.t.Fatalf("waiting for %s: %v", what, err)
	case <-time.After(60 * time.Second):
		d.io.t.Fatalf("no %s within 60 s", what)
	}
	return nil
}

func str(m map[string]json.RawMessage, key string) string {
	var s string
	_ = json.Unmarshal(m[key], &s)
	return s
}

// call sends a request and returns its response.
func (d *dapClient) call(command string, args any) map[string]json.
	RawMessage {
	d.io.t.Helper()
	d.seq++
	seq := d.seq
	d.io.write(map[string]any{"seq": seq, "type": "request",
		"command": command, "arguments": args})
	for {
		m := d.next(command + " response")
		if str(m, "type") == "event" {
			d.events = append(d.events, m)
			continue
		}
		if str(m, "type") == "response" &&
			string(m["request_seq"]) == strconv.Itoa(seq) {
			return m
		}
		d.io.t.Fatalf("unexpected message %v", m)
	}
}

// request sends a request that must succeed and decodes its body
// into body (when not nil).
func (d *dapClient) request(command string, args, body any) {
	d.io.t.Helper()
	m := d.call(command, args)
	if string(m["success"]) != "true" {
		d.io.t.Fatalf("%s failed: %s", command, m["message"])
	}
	if body != nil {
		if err := json.Unmarshal(m["body"], body); err != nil {
			d.io.t.Fatalf("%s body: %v: %s", command, err, m["body"])
		}
	}
}

// event returns the next event called name, failing on any other
// event but output; the output text seen on the way is appended to
// *output when output is not nil.
func (d *dapClient) event(name string, output *string) map[string]json.
	RawMessage {
	d.io.t.Helper()
	for {
		var m map[string]json.RawMessage
		if len(d.events) > 0 {
			m, d.events = d.events[0], d.events[1:]
		} else {
			m = d.next(name + " event")
		}
		ev := str(m, "event")
		if ev == name {
			return m
		}
		if ev != "output" {
			d.io.t.Fatalf("event %s while waiting for %s: %s", ev, name,
				m["body"])
		}
		if output != nil {
			var b struct{ Output string }
			_ = json.Unmarshal(m["body"], &b)
			*output += b.Output
		}
	}
}

type dapStopped struct {
	Reason, Description string
	ThreadID            int
}

func (d *dapClient) stopped(reason string) dapStopped {
	d.io.t.Helper()
	var s dapStopped
	m := d.event("stopped", nil)
	if err := json.Unmarshal(m["body"], &s); err != nil {
		d.io.t.Fatal(err)
	}
	if s.Reason != reason {
		d.io.t.Fatalf("stopped for %s (%s), want %s", s.Reason,
			s.Description, reason)
	}
	return s
}

type dapFrame struct {
	ID     int
	Name   string
	Line   int
	Source struct{ Path string }
}

// frames returns the call stack of thread, innermost first.
func (d *dapClient) frames(thread int) []dapFrame {
	d.io.t.Helper()
	var b struct{ StackFrames []dapFrame }
	d.request("stackTrace", map[string]any{"threadId": thread,
		"startFrame": 0, "levels": 20}, &b)
	if len(b.StackFrames) == 0 {
		d.io.t.Fatalf("thread %d has no frames", thread)
	}
	return b.StackFrames
}

// vars returns the variables of frame's scope ("Locals" or
// "Signals") by name.
func (d *dapClient) vars(frame int, scope string) map[string]string {
	d.io.t.Helper()
	var sc struct {
		Scopes []struct {
			Name               string
			VariablesReference int
		}
	}
	d.request("scopes", map[string]any{"frameId": frame}, &sc)
	for _, s := range sc.Scopes {
		if s.Name != scope {
			continue
		}
		var vs struct {
			Variables []struct{ Name, Value string }
		}
		d.request("variables", map[string]any{
			"variablesReference": s.VariablesReference}, &vs)
		out := map[string]string{}
		for _, v := range vs.Variables {
			out[v.Name] = v.Value
		}
		return out
	}
	d.io.t.Fatalf("frame %d has no scope %s: %+v", frame, scope, sc)
	return nil
}

// lineOf returns the 1-based line of path holding text.
func lineOf(t *testing.T, path, text string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, text) {
			return i + 1
		}
	}
	t.Fatalf("%s has no line with %q", path, text)
	return 0
}

// startDAP starts `gohard dap` in a copy of the corpus design dir over
// real OS pipes, runs nvim-dap's initialize handshake, launches args
// and sets the breakpoints of file (relative to the design's folder)
// at lines, which must all verify. It returns the client, the copied
// folder and a function that disconnects and checks that gohard
// exits 0.
func startDAP(t *testing.T, bin, design string, args map[string]any,
	file string, lines []int,
) (*dapClient, string, func()) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "corpus", design)
	for _, ext := range []string{"*.vhd", "*.txt"} {
		ms, _ := filepath.Glob(filepath.Join(src, ext))
		for _, m := range ms {
			b, err := os.ReadFile(m)
			if err != nil {
				t.Fatal(err)
			}
			err = os.WriteFile(filepath.Join(dir, filepath.Base(m)), b,
				0o644)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	cmd := exec.Command(bin, "dap")
	cmd.Dir = dir
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	outp, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	c := &lspClient{t: t, in: in, out: bufio.NewReader(outp),
		msgs: make(chan map[string]json.RawMessage, 64),
		errs: make(chan error, 1)}
	go func() {
		for {
			m, err := c.read()
			if err != nil {
				c.errs <- err
				return
			}
			c.msgs <- m
		}
	}()
	d := &dapClient{io: c}
	var caps struct {
		SupportsConfigurationDoneRequest, SupportsStepBack bool
	}
	d.request("initialize", map[string]any{"clientID": "neovim",
		"clientName": "neovim", "adapterID": "gohard",
		"pathFormat": "path", "linesStartAt1": true,
		"columnsStartAt1": true, "supportsRunInTerminalRequest": true,
		"supportsVariableType": true, "locale": "en_US"}, &caps)
	if !caps.SupportsConfigurationDoneRequest || !caps.SupportsStepBack {
		t.Fatalf("capabilities %+v", caps)
	}
	d.request("launch", args, nil)
	d.event("initialized", nil)
	path := filepath.Join(dir, file)
	var bps struct {
		Breakpoints []struct {
			Verified bool
			Line     int
			Message  string
		}
	}
	var want []map[string]any
	for _, l := range lines {
		want = append(want, map[string]any{"line": l})
	}
	d.request("setBreakpoints", map[string]any{
		"source":      map[string]any{"name": file, "path": path},
		"breakpoints": want, "sourceModified": false}, &bps)
	for i, b := range bps.Breakpoints {
		if !b.Verified || b.Line != lines[i] {
			t.Fatalf("breakpoint %d: %+v", lines[i], b)
		}
	}
	d.request("setExceptionBreakpoints", map[string]any{
		"filters": []string{}}, nil)
	d.request("configurationDone", map[string]any{}, nil)
	done := func() {
		t.Helper()
		d.request("disconnect", map[string]any{"restart": false,
			"terminateDebuggee": true}, nil)
		_ = in.Close()
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		select {
		case err := <-waited:
			if err != nil {
				t.Fatalf("gohard dap: %v\n%s", err, stderr.Bytes())
			}
		case <-time.After(30 * time.Second):
			t.Fatal("gohard dap did not exit after disconnect")
		}
	}
	return d, path, done
}

// ended waits for the end of the run and returns its exit code and
// output.
func (d *dapClient) ended() (int, string) {
	d.io.t.Helper()
	var out string
	m := d.event("exited", &out)
	var b struct{ ExitCode int }
	if err := json.Unmarshal(m["body"], &b); err != nil {
		d.io.t.Fatal(err)
	}
	d.event("terminated", &out)
	return b.ExitCode, out
}

// TestDAPStdio is the stdio test of #68 in the shape of TestHelixStdio:
// the built gohard binary runs `gohard dap` over real OS pipes, and a
// scripted client sends the requests nvim-dap sends (initialize,
// launch, setBreakpoints, setExceptionBreakpoints, configurationDone,
// threads, stackTrace, scopes, variables, continue, next, stepIn,
// stepOut, stepBack, disconnect) while debugging the wait_forms and
// assert_fail corpus testbenches, and checks every response: where
// each move stops, the call stack, the variables and signals there,
// and the end of the run with the exit code `gohard check --sim` has.
func TestDAPStdio(t *testing.T) {
	bin := buildGohard(t)
	t.Run("wait_forms", func(t *testing.T) {
		tb := filepath.Join("..", "..", "testdata", "corpus",
			"wait_forms", "wait_forms_tb.vhd")
		call := lineOf(t, tb, `expect_time("wait on a", i, t);`)
		wait := lineOf(t, tb, "      wait on a;")
		d, path, done := startDAP(t, bin, "wait_forms", map[string]any{
			"type": "gohard", "request": "launch", "name": "wait_forms",
			"testbench": "wait_forms_tb", "seed": 0},
			"wait_forms_tb.vhd", []int{call})
		st := d.stopped("breakpoint")
		if !strings.Contains(st.Description, "at 10 ns") {
			t.Errorf("stopped %s, want at 10 ns", st.Description)
		}
		var th struct {
			Threads []struct {
				ID   int
				Name string
			}
		}
		d.request("threads", nil, &th)
		name := ""
		for _, x := range th.Threads {
			if x.ID == st.ThreadID {
				name = x.Name
			}
		}
		if name != "wait_forms_tb.on_one" || len(th.Threads) < 20 {
			t.Fatalf("stopped thread %d %q of %+v", st.ThreadID, name,
				th.Threads)
		}
		fs := d.frames(st.ThreadID)
		if f := fs[0]; f.Name != "on_one" || f.Line != call ||
			f.Source.Path != path {
			t.Fatalf("frame %+v, want on_one at %s:%d", f, path, call)
		}
		if v := d.vars(fs[0].ID, "Locals"); v["i"] != "0" ||
			v["k"] != "0" {
			t.Errorf("locals %v, want i = 0, k = 0", v)
		}
		if v := d.vars(fs[0].ID, "Signals"); v["a"] != "'1'" ||
			v["n_on"] != "0" || v["dut.x"] != "" {
			t.Errorf("signals %v, want a = '1', n_on = 0, no dut.x", v)
		}

		// Into expect_time and out again.
		d.request("stepIn", map[string]any{"threadId": st.ThreadID}, nil)
		d.stopped("step")
		fs = d.frames(st.ThreadID)
		if len(fs) != 2 || fs[0].Name != "expect_time" ||
			fs[1].Name != "on_one" || fs[1].Line != call {
			t.Fatalf("frames after stepIn %+v", fs)
		}
		if v := d.vars(fs[0].ID, "Locals"); v["what"] != `"wait on a"` ||
			v["i"] != "0" {
			t.Errorf("expect_time locals %v", v)
		}
		d.request("stepOut", map[string]any{"threadId": st.ThreadID},
			nil)
		d.stopped("step")
		fs = d.frames(st.ThreadID)
		if len(fs) != 1 || fs[0].Line != call+1 {
			t.Fatalf("frames after stepOut %+v, want line %d", fs, call+1)
		}
		if v := d.vars(fs[0].ID, "Locals"); v["i"] != "1" {
			t.Errorf("locals after stepOut %v, want i = 1", v)
		}

		// Over the loop's end to the wait, and over the wait to the
		// next resumption at 30 ns.
		d.request("next", map[string]any{"threadId": st.ThreadID}, nil)
		d.stopped("step")
		if fs = d.frames(st.ThreadID); fs[0].Line != wait {
			t.Fatalf("next stopped at %+v, want line %d", fs[0], wait)
		}
		d.request("next", map[string]any{"threadId": st.ThreadID}, nil)
		st = d.stopped("step")
		if fs = d.frames(st.ThreadID); fs[0].Line != call ||
			!strings.Contains(st.Description, "at 30 ns") {
			t.Fatalf("next stopped %s at %+v, want 30 ns line %d",
				st.Description, fs[0], call)
		}
		if v := d.vars(fs[0].ID, "Signals"); v["a"] != "'0'" {
			t.Errorf("signals at 30 ns %v, want a = '0'", v)
		}

		// Back over the wait to 10 ns.
		d.request("stepBack", map[string]any{"threadId": st.ThreadID},
			nil)
		st = d.stopped("step")
		if fs = d.frames(st.ThreadID); fs[0].Line != wait ||
			!strings.Contains(st.Description, "at 10 ns") {
			t.Fatalf("stepBack stopped %s at %+v, want 10 ns line %d",
				st.Description, fs[0], wait)
		}

		// The breakpoint at 30 ns and 60 ns, then the end.
		for _, at := range []string{"30 ns", "60 ns"} {
			d.request("continue", map[string]any{
				"threadId": st.ThreadID}, nil)
			st = d.stopped("breakpoint")
			if !strings.Contains(st.Description, "at "+at) {
				t.Fatalf("continue stopped %s, want at %s",
					st.Description, at)
			}
		}
		d.request("continue", map[string]any{"threadId": st.ThreadID},
			nil)
		code, out := d.ended()
		if code != 0 || !strings.Contains(out, "summary: 0 errors") {
			t.Fatalf("exit %d, output:\n%s", code, out)
		}
		done()
	})
	t.Run("assert_fail", func(t *testing.T) {
		tb := filepath.Join("..", "..", "testdata", "corpus",
			"assert_fail", "assert_fail_tb.vhd")
		check := lineOf(t, tb, "assert s > a")
		next := lineOf(t, tb, "a <= to_unsigned(5, 4);")
		d, _, done := startDAP(t, bin, "assert_fail", map[string]any{
			"type": "gohard", "request": "launch", "name": "assert_fail",
			"testbench": "assert_fail_tb"}, "assert_fail_tb.vhd",
			[]int{check})
		st := d.stopped("breakpoint")
		if !strings.Contains(st.Description, "at 20 ns") {
			t.Errorf("stopped %s, want at 20 ns", st.Description)
		}
		fs := d.frames(st.ThreadID)
		if fs[0].Name != "check" || fs[0].Line != check {
			t.Fatalf("frame %+v, want check at line %d", fs[0], check)
		}
		// 9 + 9 wraps to 2 in 4 bits.
		if v := d.vars(fs[0].ID, "Signals"); v["a"] != `"1001"` ||
			v["b"] != `"1001"` || v["s"] != `"0010"` {
			t.Errorf("signals %v, want a = b = 1001, s = 0010", v)
		}
		d.request("next", map[string]any{"threadId": st.ThreadID}, nil)
		d.stopped("step")
		if fs = d.frames(st.ThreadID); fs[0].Line != next {
			t.Fatalf("next stopped at %+v, want line %d", fs[0], next)
		}
		d.request("stepBack", map[string]any{"threadId": st.ThreadID},
			nil)
		d.stopped("step")
		if fs = d.frames(st.ThreadID); fs[0].Line != check {
			t.Fatalf("stepBack stopped at %+v, want line %d", fs[0],
				check)
		}
		d.request("continue", map[string]any{"threadId": st.ThreadID},
			nil)
		code, out := d.ended()
		for _, want := range []string{"assert_fail: sum wrapped to 2",
			"assert_fail: expected 12, got 11", "halted at 30 ns",
			"summary: 1 errors, 1 warnings, 1 hints"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		}
		if code != 1 {
			t.Errorf("exit %d, want 1 (gohard check --sim)", code)
		}
		done()
	})
}

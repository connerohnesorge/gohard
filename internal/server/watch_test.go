package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/cli"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/ide"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/v2/internal/repro"
	"github.com/connerohnesorge/gohard/v2/internal/server"
)

// TestServeRecordsDiskChanges: a session in which a file changes on
// disk (reported by workspace/didChangeWatchedFiles) replays byte for
// byte (#29): the reread is in the recording, so the replay needs
// neither the file as it was at the start nor as it was left.
func TestServeRecordsDiskChanges(t *testing.T) {
	const pkg = "/work/a.vhd"
	old := []byte("package p is\nend package;\n") // no K: b fails
	fixed := []byte("package p is\n  constant K : integer := 1;\n" +
		"end package;\n")
	use := []byte("use work.p.all;\nentity b is end;\n" +
		"architecture a of b is\n  constant C : integer := K;\n" +
		"begin\nend;\n")
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
			`{"rootUri":"file:///work","capabilities":{}}}`,
		`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
		`{"jsonrpc":"2.0","method":"workspace/didChangeWatchedFiles",` +
			`"params":{"changes":[{"uri":"file:///work/a.vhd",` +
			`"type":2}]}}`,
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`,
		`{"jsonrpc":"2.0","method":"exit"}`,
	}
	parts := make([][]byte, 3)
	for i, m := range msgs {
		k := []int{0, 0, 1, 2, 2}[i]
		parts[k] = jsonrpc2.AppendFrame(parts[k], []byte(m))
	}
	files := map[string][]byte{pkg: old, "/work/b.vhd": use}
	sim := simenv.New(simenv.Options{Dir: workDir, Files: files})
	e := sim.Env()
	h, err := server.SessionHeader(e, workDir, cli.Version)
	if err != nil {
		t.Fatal(err)
	}
	opts := server.Options{Version: cli.Version,
		Analyzer: ide.New(ide.Options{ToolVersion: cli.Version,
			Jobs: e.Jobs, Limit: 1})}
	gates := []chan struct{}{make(chan struct{}), make(chan struct{})}
	out := &gatedWriter{gates: gates, trigger: []byte(
		`"uri":"file:///work/b.vhd","diagnostics"`)}
	e.Transport.Stdin = &stagedReader{parts: parts, gates: gates,
		before: map[int]func(){1: func() {
			if err := sim.FS.WriteFile(pkg, fixed, 0o644); err != nil {
				panic(err)
			}
		}}}
	e.Transport.Stdout = out
	var lines []json.RawMessage
	code := server.Serve(context.Background(), e, opts, 1,
		func(a server.Arrival) error {
			if l := a.Line(); l != nil {
				lines = append(lines, l)
			}
			return nil
		})
	live := out.buf.Bytes()
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if n := bytes.Count(live, []byte("sema/undefined")); n != 1 {
		t.Fatalf("live: %d sema/undefined, want 1 (before the "+
			"change only):\n%s", n, live)
	}
	if !bytes.Contains(live, []byte(`"uri":"file:///work/b.vhd",`+
		`"diagnostics":[]`)) {
		t.Fatalf("live: the reread did not clear b.vhd:\n%s", live)
	}
	sess := repro.Session{Header: h, Messages: lines}
	replay := func(data []byte, s repro.Session) server.Replayed {
		t.Helper()
		fs := map[string][]byte{pkg: data, "/work/b.vhd": use}
		got, err := server.Replay(s, simenv.New(simenv.Options{
			Dir: "/elsewhere", Files: fs}).Env().FS, opts, "live")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := replay(old, sess)
	if len(got.Diagnostics) > 0 {
		t.Fatal(got.Diagnostics)
	}
	if !bytes.Equal(live, got.Output) {
		t.Fatalf("live output differs from its replay:\n%s\n---\n%s",
			live, got.Output)
	}
	var stripped []json.RawMessage
	for _, l := range lines {
		if !bytes.HasPrefix(l, []byte(`{"$gohard":"workspace"`)) {
			stripped = append(stripped, l)
		}
	}
	if len(stripped) == len(lines) {
		t.Fatal("no workspace reread was recorded")
	}
	sess.Messages = stripped
	if got := replay(old, sess); bytes.Equal(live, got.Output) {
		t.Fatal("a replay without the recorded reread matched")
	}
}

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// helixCapabilities are the client capabilities Helix (25.07) sends in
// initialize (helix-lsp/src/client.rs), so the handshake below is the
// one a real Helix runs against `gohard lsp` (#38: Helix connects over
// stdio with no extension; CI does not launch Helix itself).
const helixCapabilities = `{
 "workspace": {
  "configuration": true,
  "didChangeConfiguration": {"dynamicRegistration": false},
  "workspaceFolders": true,
  "applyEdit": true,
  "symbol": {"dynamicRegistration": false},
  "executeCommand": {"dynamicRegistration": false},
  "inlayHint": {"refreshSupport": false},
  "workspaceEdit": {"documentChanges": true,
   "resourceOperations": ["create", "rename", "delete"],
   "failureHandling": "abort", "normalizesLineEndings": false,
   "changeAnnotationSupport": null},
  "didChangeWatchedFiles": {"dynamicRegistration": true,
   "relativePatternSupport": false},
  "fileOperations": {"willRename": true, "didRename": true},
  "diagnostic": {"refreshSupport": true}
 },
 "textDocument": {
  "completion": {"completionItem": {"snippetSupport": true,
    "resolveSupport": {"properties": ["documentation", "detail",
     "additionalTextEdits"]},
    "insertReplaceSupport": true, "deprecatedSupport": true,
    "tagSupport": {"valueSet": [1]}},
   "completionItemKind": {}},
  "hover": {"contentFormat": ["markdown"]},
  "signatureHelp": {"signatureInformation": {
    "documentationFormat": ["markdown"],
    "parameterInformation": {"labelOffsetSupport": true},
    "activeParameterSupport": true}},
  "rename": {"dynamicRegistration": false, "prepareSupport": true,
   "prepareSupportDefaultBehavior": 1, "honorsChangeAnnotations": false},
  "codeAction": {"codeActionLiteralSupport": {"codeActionKind": {
    "valueSet": ["", "quickfix", "refactor", "refactor.extract",
     "refactor.inline", "refactor.rewrite", "source",
     "source.organizeImports"]}},
   "isPreferredSupport": true, "disabledSupport": true,
   "dataSupport": true,
   "resolveSupport": {"properties": ["edit", "command"]}},
  "publishDiagnostics": {"versionSupport": true,
   "tagSupport": {"valueSet": [1, 2]}},
  "inlayHint": {"dynamicRegistration": false},
  "diagnostic": {"dynamicRegistration": false,
   "relatedDocumentSupport": true}
 },
 "window": {"workDoneProgress": true, "showDocument": {"support": true}},
 "general": {"positionEncodings": ["utf-8", "utf-32", "utf-16"]}
}`

// lspClient is a scripted LSP client over the real stdio pipes of a
// gohard process. It answers the server's own requests the way Helix
// does (null configuration, accepted progress tokens).
type lspClient struct {
	t    *testing.T
	in   io.WriteCloser
	out  *bufio.Reader
	msgs chan map[string]json.RawMessage
	errs chan error
	next int
}

func (c *lspClient) write(v any) {
	c.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		c.t.Fatal(err)
	}
	_, err = fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(b), b)
	if err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

// read decodes one framed message; it runs in the reader goroutine.
func (c *lspClient) read() (map[string]json.RawMessage, error) {
	n := -1
	for {
		line, err := c.out.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			if n, err = strconv.Atoi(v); err != nil {
				return nil, err
			}
		}
	}
	if n < 0 {
		return nil, fmt.Errorf("no Content-Length header")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(c.out, body); err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	return m, json.Unmarshal(body, &m)
}

// await returns messages until match accepts one, answering server
// requests on the way; it fails after timeout of wall time.
func (c *lspClient) await(what string, match func(
	map[string]json.RawMessage) bool,
) map[string]json.RawMessage {
	c.t.Helper()
	deadline := time.After(60 * time.Second)
	for {
		select {
		case m := <-c.msgs:
			if _, isReq := m["method"]; isReq && m["id"] != nil {
				// A server request: Helix answers configuration with
				// one null per item and accepts progress tokens.
				var res any
				var method string
				_ = json.Unmarshal(m["method"], &method)
				if method == "workspace/configuration" {
					var p struct{ Items []any }
					_ = json.Unmarshal(m["params"], &p)
					res = make([]any, len(p.Items))
				}
				c.write(map[string]any{"jsonrpc": "2.0",
					"id": m["id"], "result": res})
			}
			if match(m) {
				return m
			}
		case err := <-c.errs:
			c.t.Fatalf("waiting for %s: %v", what, err)
		case <-deadline:
			c.t.Fatalf("no %s within 60 s", what)
		}
	}
}

func (c *lspClient) request(method string, params any) json.RawMessage {
	c.t.Helper()
	c.next++
	id := c.next
	c.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method,
		"params": params})
	m := c.await(method+" response", func(m map[string]json.RawMessage,
	) bool {
		_, hasMethod := m["method"]
		return !hasMethod && string(m["id"]) == strconv.Itoa(id)
	})
	if e := m["error"]; e != nil {
		c.t.Fatalf("%s: %s", method, e)
	}
	return m["result"]
}

func (c *lspClient) notify(method string, params any) {
	c.write(map[string]any{"jsonrpc": "2.0", "method": method,
		"params": params})
}

// buildGohard builds cmd/gohard into a temporary directory and returns
// the binary's path.
func buildGohard(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the gohard binary")
	}
	bin := filepath.Join(t.TempDir(), "gohard")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	gobin := filepath.Join(runtime.GOROOT(), "bin", "go")
	cmd := exec.Command(gobin, "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// copyTree copies the files under src to dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry,
		err error,
	) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func fileURI(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // C:/x -> /C:/x
	}
	return "file://" + p
}

// TestHelixStdio is the Helix connection test of #38: the built gohard
// binary runs as `gohard lsp` over real OS pipes, a scripted client
// runs Helix's initialize handshake with Helix's client capabilities,
// opens a file of a two-file project, gets its diagnostics (equal to
// `gohard check` on the same project), then shuts down and exits 0.
func TestHelixStdio(t *testing.T) {
	bin := buildGohard(t)
	dir := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "testdata", "lsp",
		"diagnostics", "cross-file", "in"), dir)

	// What gohard check says about the same project.
	chk := exec.Command(bin, "check", "--format", "json")
	chk.Dir = dir
	chkOut, _ := chk.Output()
	var want struct {
		Diagnostics []struct {
			Path      string
			Line, Col int
			Code      string
			Message   string
		}
	}
	if err := json.Unmarshal(chkOut, &want); err != nil {
		t.Fatalf("check: %v\n%s", err, chkOut)
	}
	var wantTop []string
	for _, d := range want.Diagnostics {
		if d.Path == "top.vhd" {
			wantTop = append(wantTop, fmt.Sprintf("%d:%d %s %s",
				d.Line-1, d.Col-1, d.Code, d.Message))
		}
	}
	if len(wantTop) == 0 {
		t.Fatalf("check reports nothing for top.vhd:\n%s", chkOut)
	}

	cmd := exec.Command(bin, "lsp")
	cmd.Dir = dir
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	c := &lspClient{t: t, in: in, out: bufio.NewReader(out),
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

	root := fileURI(dir)
	var caps any
	if err := json.Unmarshal([]byte(helixCapabilities), &caps); err != nil {
		t.Fatal(err)
	}
	res := c.request("initialize", map[string]any{
		"processId": os.Getpid(), "rootPath": dir, "rootUri": root,
		"workspaceFolders": []any{map[string]any{"uri": root,
			"name": filepath.Base(dir)}},
		"initializationOptions": nil, "capabilities": caps,
		"clientInfo": map[string]any{"name": "helix",
			"version": "25.07.1"}})
	var init struct {
		Capabilities struct {
			PositionEncoding string
			TextDocumentSync json.RawMessage
		}
		ServerInfo struct{ Name string }
	}
	if err := json.Unmarshal(res, &init); err != nil {
		t.Fatal(err)
	}
	if init.Capabilities.PositionEncoding != "utf-8" {
		t.Errorf("position encoding %q, want utf-8 (Helix offers it)",
			init.Capabilities.PositionEncoding)
	}
	if len(init.Capabilities.TextDocumentSync) == 0 {
		t.Error("no textDocumentSync capability")
	}
	c.notify("initialized", map[string]any{})
	// Helix sends its language-server config (none here) next.
	c.notify("workspace/didChangeConfiguration",
		map[string]any{"settings": nil})

	top := filepath.Join(dir, "top.vhd")
	text, err := os.ReadFile(top)
	if err != nil {
		t.Fatal(err)
	}
	uri := fileURI(top)
	c.notify(
		"textDocument/didOpen",
		map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": "vhdl", "version": 0,
			"text": string(text)}},
	)
	m := c.await("diagnostics for top.vhd", func(
		m map[string]json.RawMessage,
	) bool {
		var method string
		_ = json.Unmarshal(m["method"], &method)
		if method != "textDocument/publishDiagnostics" {
			return false
		}
		var p struct{ URI string }
		_ = json.Unmarshal(m["params"], &p)
		return strings.EqualFold(p.URI, uri)
	})
	var pub struct {
		Diagnostics []struct {
			Range struct {
				Start struct{ Line, Character int }
			}
			Code, Message string
		}
	}
	if err := json.Unmarshal(m["params"], &pub); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range pub.Diagnostics {
		got = append(got, fmt.Sprintf("%d:%d %s %s", d.Range.Start.Line,
			d.Range.Start.Character, d.Code, d.Message))
	}
	if !slices.Equal(got, wantTop) {
		t.Fatalf("diagnostics\n%s\nwant (gohard check)\n%s",
			strings.Join(got, "\n"), strings.Join(wantTop, "\n"))
	}

	if r := c.request("shutdown", nil); string(r) != "null" {
		t.Errorf("shutdown result %s, want null", r)
	}
	c.notify("exit", nil)
	_ = in.Close()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("gohard lsp: %v\n%s", err, stderr.Bytes())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("gohard lsp did not exit after exit")
	}
}

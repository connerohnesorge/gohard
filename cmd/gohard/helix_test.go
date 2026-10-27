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
	"regexp"
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

// stdioSession is how an editor starts `gohard lsp` and talks to it,
// up to the diagnostics of the first file it opens.
type stdioSession struct {
	args []string // the arguments after the gohard binary
	dir  string   // the server's working directory, the project folder
	// initialize holds the initialize params.
	initialize map[string]any
	// settings, when not nil, is sent in a
	// workspace/didChangeConfiguration after initialized.
	settings   json.RawMessage
	open       string // the file opened, a path in dir
	languageID string
	encoding   string // the position encoding the server must choose
}

// checkDiagnostics runs `gohard check` in dir and returns its
// diagnostics for the file rel as 0-based "line:col code message".
func checkDiagnostics(t *testing.T, bin, dir, rel string) []string {
	t.Helper()
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
	var out []string
	for _, d := range want.Diagnostics {
		if d.Path == rel {
			out = append(out, fmt.Sprintf("%d:%d %s %s",
				d.Line-1, d.Col-1, d.Code, d.Message))
		}
	}
	if len(out) == 0 {
		t.Fatalf("check reports nothing for %s:\n%s", rel, chkOut)
	}
	return out
}

// runStdio starts bin with s.args over real OS pipes, runs the
// session's handshake, opens s.open and checks that its diagnostics
// equal `gohard check` in s.dir. Then it shuts the server down and
// checks that it exits 0.
func runStdio(t *testing.T, bin string, s stdioSession) {
	t.Helper()
	rel, err := filepath.Rel(s.dir, s.open)
	if err != nil {
		t.Fatal(err)
	}
	wantDiags := checkDiagnostics(t, bin, s.dir, filepath.ToSlash(rel))

	cmd := exec.Command(bin, s.args...)
	cmd.Dir = s.dir
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

	res := c.request("initialize", s.initialize)
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
	if init.Capabilities.PositionEncoding != s.encoding {
		t.Errorf("position encoding %q, want %s",
			init.Capabilities.PositionEncoding, s.encoding)
	}
	if len(init.Capabilities.TextDocumentSync) == 0 {
		t.Error("no textDocumentSync capability")
	}
	c.notify("initialized", map[string]any{})
	if s.settings != nil {
		c.notify("workspace/didChangeConfiguration",
			map[string]any{"settings": s.settings})
	}

	text, err := os.ReadFile(s.open)
	if err != nil {
		t.Fatal(err)
	}
	uri := fileURI(s.open)
	c.notify(
		"textDocument/didOpen",
		map[string]any{"textDocument": map[string]any{
			"uri": uri, "languageId": s.languageID, "version": 0,
			"text": string(text)}},
	)
	m := c.await("diagnostics for "+rel, func(
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
	if !slices.Equal(got, wantDiags) {
		t.Fatalf("diagnostics\n%s\nwant (gohard check)\n%s",
			strings.Join(got, "\n"), strings.Join(wantDiags, "\n"))
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

// upstream is the folder of the configs proposed to the editors'
// own repositories (#65).
var upstream = filepath.Join("..", "..", "editors", "upstream")

// helixEntry returns the command and arguments of the gohard language
// server that editors/upstream/helix/gohard.diff adds to Helix's
// languages.toml. It fails the test if the entry sets a config (the
// test would have to send it) or the vhdl language sets roots (the
// test sends the root Helix picks without them).
func helixEntry(t *testing.T) (string, []string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(upstream, "helix", "gohard.diff"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\+gohard = \{ command = ("[^"]+")` +
		`, args = \[([^\]]*)\] \}$`).FindSubmatch(b)
	if m == nil {
		t.Fatalf("gohard.diff adds no line of the form "+
			"gohard = { command = \"...\", args = [...] }:\n%s", b)
	}
	command, err := strconv.Unquote(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	for a := range strings.SplitSeq(string(m[2]), ",") {
		if a = strings.TrimSpace(a); a == "" {
			continue
		}
		u, err := strconv.Unquote(a)
		if err != nil {
			t.Fatalf("argument %s: %v", a, err)
		}
		args = append(args, u)
	}
	if !regexp.MustCompile(`(?m)^\+language-servers = \[.*"gohard".*\]$`).
		Match(b) {
		t.Error("gohard.diff does not add gohard to the vhdl language")
	}
	if regexp.MustCompile(`(?m)^[ +]roots = `).Match(b) {
		t.Error("gohard.diff sets roots; send the root Helix picks " +
			"from them")
	}
	return command, args
}

// TestHelixStdio is the Helix connection test of #38 and #65: the
// built gohard binary runs over real OS pipes with the command line of
// the upstream Helix entry (editors/upstream/helix/gohard.diff), a
// scripted client runs Helix's initialize handshake with Helix's
// client capabilities, opens a file of a two-file project, gets its
// diagnostics (equal to `gohard check` on the same project), then
// shuts down and exits 0. The vhdl language has no roots, so Helix
// sends the folder holding .git as the root; with no .git it sends no
// root and no workspace folder, only rootPath, the folder it was
// started in. Helix sends no initialization options and no
// didChangeConfiguration when the entry has no config.
func TestHelixStdio(t *testing.T) {
	command, args := helixEntry(t)
	if command != "gohard" {
		t.Fatalf("the Helix entry runs %q, want gohard", command)
	}
	bin := buildGohard(t)
	var caps any
	if err := json.Unmarshal([]byte(helixCapabilities), &caps); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		git  bool // the folder holds .git
	}{{"git", true}, {"cwd", false}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			copyTree(t, filepath.Join("..", "..", "testdata", "lsp",
				"diagnostics", "cross-file", "in"), dir)
			init := map[string]any{
				"processId": os.Getpid(), "rootPath": dir,
				"rootUri": nil, "workspaceFolders": []any{},
				"capabilities": caps,
				"clientInfo": map[string]any{"name": "helix",
					"version": "25.07.1"}}
			if tc.git {
				if err := os.Mkdir(filepath.Join(dir, ".git"),
					0o755); err != nil {
					t.Fatal(err)
				}
				root := fileURI(dir)
				init["rootUri"] = root
				init["workspaceFolders"] = []any{map[string]any{
					"uri": root, "name": filepath.Base(dir)}}
			}
			runStdio(t, bin, stdioSession{
				args: args, dir: dir, initialize: init,
				open:       filepath.Join(dir, "top.vhd"),
				languageID: "vhdl", encoding: "utf-8",
			})
		})
	}
}

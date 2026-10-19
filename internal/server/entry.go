package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
)

// A recorded session (#29) is a header line and then one line per
// client input. A line is either a plain client message, a compact
// JSON-RPC body that the shell queued while the core was idle, or an
// entry: a JSON object with the key "$gohard" naming its kind.
//
//	{"$gohard":"message","step":N,"message":BODY}
//	    BODY arrived after the core had run N events, while it was
//	    busy (no "step": once it was idle). Also used for a body that
//	    is itself an object with a "$gohard" key.
//	{"$gohard":"raw","step":N,"raw":BASE64}
//	    a body that is not JSON, byte for byte (no "step": idle).
//	{"$gohard":"rejected","step":N,"id":ID,"reason":TEXT}
//	    a request the transport skipped unread (over the size limit
//	    or not UTF-8), answered with InvalidRequest (no "step": idle).
//	{"$gohard":"config","step":N,"path":PATH,"data":BASE64}
//	    one entry per LoadConfig the live server performed (after N
//	    events), in order, with the gohard.yaml it read (PATH relative to the session folder,
//	    "missing":true when there was none). Replay feeds them back,
//	    so a session that saves gohard.yaml replays byte for byte.
//	{"$gohard":"end","step":N}
//	    the client's input ended after N events, while the core was
//	    busy; an end at idle is not written.
//	{"$gohard":"file","path":PATH,"data":BASE64}
//	    a project file, PATH relative to the session folder in slash
//	    form. A session with files is self-contained: Replay builds
//	    the project from them instead of reading it (failing harness
//	    sessions, #25).
//	{"$gohard":"analyzer","name":NAME}
//	    the analyzer the session ran with, for harness sessions that
//	    use a test analyzer (see SessionAnalyzer).
//
// Plain lines keep a hand-written or editor-recorded session readable;
// entries make every live session replay byte for byte, including
// input that arrived while work was running.
const entryKey = "$gohard"

// Entry kinds.
const (
	entryMessage  = "message"
	entryRaw      = "raw"
	entryEnd      = "end"
	entryRejected = "rejected"
	entryConfig   = "config"
	entryFile     = "file"
	entryAnalyzer = "analyzer"
)

type entry struct {
	Kind    string          `json:"$gohard"`
	Step    *uint64         `json:"step,omitempty"`
	Message json.RawMessage `json:"message,omitempty"`
	Raw     []byte          `json:"raw,omitempty"`
	Path    string          `json:"path,omitempty"`
	Data    []byte          `json:"data,omitempty"`
	Name    string          `json:"name,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Reason  string          `json:"reason,omitempty"`
	Missing bool            `json:"missing,omitempty"`
}

// Arrival is one client input of a live session as the shell queued
// it, for `gohard lsp --record`.
type Arrival struct {
	// Body is the message body as received; nil is the end of the
	// input.
	Body []byte
	// Step is the number of events the core had run when the input
	// was queued.
	Step uint64
	// Busy reports that the core had queued events then, so Replay
	// must queue the input after exactly Step events.
	Busy bool
	// Rejected, when set, is a request the transport skipped unread
	// (Body is nil then).
	Rejected *Rejected
	// Config, when set, is not client input but a gohard.yaml read the
	// server performed after Step events (Body is nil then).
	Config *ConfigRead
}

// ConfigRead is one read of gohard.yaml by the live server.
type ConfigRead struct {
	// Path is the file read, relative to the session folder in slash
	// form; "" when Missing.
	Path string
	// Data is the file's content.
	Data []byte
	// Missing reports that no gohard.yaml was found.
	Missing bool
}

// configRead describes what config.Load read from folder.
func configRead(folder string, l config.Loaded) *ConfigRead {
	if !l.Present {
		return &ConfigRead{Missing: true}
	}
	p, err := filepath.Rel(folder, filepath.Join(l.Root, config.FileName))
	if err != nil {
		return &ConfigRead{Missing: true}
	}
	return &ConfigRead{Path: filepath.ToSlash(p), Data: l.Source}
}

// Line returns a's session line, or nil when nothing needs to be
// written (the end of the input while the core was idle).
func (a Arrival) Line() []byte {
	var step *uint64
	if a.Busy {
		step = &a.Step
	}
	switch {
	case a.Config != nil:
		return marshalEntry(entry{Kind: entryConfig, Step: &a.Step,
			Path: a.Config.Path, Data: a.Config.Data,
			Missing: a.Config.Missing})
	case a.Rejected != nil:
		id, _ := a.Rejected.ID.MarshalJSON()
		return marshalEntry(entry{Kind: entryRejected, Step: step,
			ID: id, Reason: a.Rejected.Reason})
	case a.Body == nil && step == nil:
		return nil
	case a.Body == nil:
		return marshalEntry(entry{Kind: entryEnd, Step: step})
	case !json.Valid(a.Body):
		return marshalEntry(entry{Kind: entryRaw, Step: step,
			Raw: a.Body})
	}
	var b bytes.Buffer
	_ = json.Compact(&b, a.Body)
	if step == nil && !isEntry(b.Bytes()) {
		return b.Bytes()
	}
	return marshalEntry(entry{Kind: entryMessage, Step: step,
		Message: b.Bytes()})
}

// FileLine returns the session line that embeds a project file; path
// is relative to the session folder.
func FileLine(path string, data []byte) []byte {
	return marshalEntry(entry{Kind: entryFile,
		Path: filepath.ToSlash(path), Data: data})
}

// AnalyzerLine returns the session line that names the analyzer.
func AnalyzerLine(name string) []byte {
	return marshalEntry(entry{Kind: entryAnalyzer, Name: name})
}

func marshalEntry(e entry) []byte {
	b, err := json.Marshal(e)
	if err != nil {
		panic(err) // every field is plain data
	}
	return b
}

// isEntry reports whether line is a JSON object with the key
// "$gohard" (matched exactly, unlike encoding/json's field matching).
func isEntry(line []byte) bool {
	if t := bytes.TrimSpace(line); len(t) == 0 || t[0] != '{' {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(line, &m) != nil {
		return false
	}
	_, ok := m[entryKey]
	return ok
}

// cleanRel reports whether p is a clean relative slash path naming a
// file, with leading ../ segments only when up is set. The rule is the
// same on every OS (a session recorded on one replays on all): no
// leading '/', no backslash, no drive letter, no "." or ".." elsewhere.
func cleanRel(p string, up bool) bool {
	rest := p
	for up && strings.HasPrefix(rest, "../") {
		rest = rest[len("../"):]
	}
	return rest != "" && rest != "." && rest != ".." &&
		!strings.HasPrefix(rest, "../") && path.Clean(p) == p &&
		!path.IsAbs(p) && !strings.Contains(p, `\`) &&
		!(len(rest) >= 2 && rest[1] == ':')
}

// input is one client input of a session.
type input struct {
	body   []byte // nil: the end of the input (unless reject is set)
	reject *Rejected
	step   uint64
	timed  bool // queue after exactly step events
}

// project is what a session's lines say besides the inputs.
type project struct {
	files    map[string][]byte // slash path -> data
	analyzer string
	configs  []ConfigRead // the live server's gohard.yaml reads
}

// parseLines splits a session's lines into inputs and project data.
func parseLines(lines []json.RawMessage) ([]input, project, error) {
	var ins []input
	p := project{files: map[string][]byte{}}
	for i, line := range lines {
		if !isEntry(line) {
			ins = append(ins, input{body: line})
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, p, fmt.Errorf("message %d: %v", i+1, err)
		}
		in := input{}
		if e.Step != nil {
			in.step, in.timed = *e.Step, true
		}
		switch e.Kind {
		case entryMessage:
			if len(e.Message) == 0 {
				return nil, p, fmt.Errorf("message %d: no message", i+1)
			}
			in.body = e.Message
		case entryRaw:
			if e.Raw == nil {
				e.Raw = []byte{}
			}
			in.body = e.Raw
		case entryEnd:
		case entryRejected:
			var id jsonrpc2.ID
			if id.UnmarshalJSON(e.ID) != nil || id.IsNull() {
				return nil, p, fmt.Errorf("message %d: rejected "+
					"entry without a request ID", i+1)
			}
			in.reject = &Rejected{ID: id, Reason: e.Reason}
		case entryConfig:
			c := ConfigRead{Path: e.Path, Data: e.Data,
				Missing: e.Missing}
			if c.Missing != (c.Path == "") || (!c.Missing &&
				(!cleanRel(c.Path, true) ||
					path.Base(c.Path) != config.FileName)) {
				return nil, p, fmt.Errorf("message %d: config "+
					"entry path %s is not a clean relative path to "+
					"%s", i+1, strconv.Quote(c.Path), config.FileName)
			}
			p.configs = append(p.configs, c)
			continue
		case entryFile:
			if !cleanRel(e.Path, false) {
				return nil, p, fmt.Errorf("message %d: file path %s "+
					"is not a clean relative path", i+1,
					strconv.Quote(e.Path))
			}
			if _, dup := p.files[e.Path]; dup {
				return nil, p, fmt.Errorf("message %d: file %s "+
					"repeats", i+1, e.Path)
			}
			p.files[e.Path] = append([]byte{}, e.Data...)
			continue
		case entryAnalyzer:
			p.analyzer = e.Name
			continue
		default:
			return nil, p, fmt.Errorf("message %d: unknown entry %s",
				i+1, strconv.Quote(e.Kind))
		}
		ins = append(ins, in)
	}
	return ins, p, nil
}

package repro

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// Session is a recorded language-server session (#29): the header,
// then the client messages in arrival order, each a compact JSON value.
type Session struct {
	Header   Header
	Messages []json.RawMessage
}

// Marshal encodes s as JSON lines: the header, then one message per
// line. Messages are compacted, so the encoding is canonical.
func (s Session) Marshal() ([]byte, error) {
	var b bytes.Buffer
	r, err := NewRecorder(&b, s.Header)
	if err != nil {
		return nil, err
	}
	for _, m := range s.Messages {
		if err := r.Record(m); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

// ParseSession decodes a session file. Every line, the last included,
// ends in a newline; empty lines are errors.
func ParseSession(data []byte) (Session, error) {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return Session{}, errors.New("a session file must end in a " +
			"newline")
	}
	lines := bytes.Split(data[:len(data)-1], []byte("\n"))
	h, err := Unmarshal(lines[0])
	if err != nil {
		return Session{}, fmt.Errorf("line 1: %w", err)
	}
	s := Session{Header: h, Messages: []json.RawMessage{}}
	for i, line := range lines[1:] {
		m, err := compact(line)
		if err != nil {
			return Session{}, fmt.Errorf("line %d: %w", i+2, err)
		}
		s.Messages = append(s.Messages, m)
	}
	return s, nil
}

// ReadSession reads and decodes the session file name.
func ReadSession(fsys env.FS, name string) (Session, error) {
	data, err := fsys.ReadFile(name)
	if err != nil {
		return Session{}, err
	}
	return ParseSession(data)
}

func compact(m []byte) (json.RawMessage, error) {
	var b bytes.Buffer
	if err := json.Compact(&b, m); err != nil {
		return nil, err
	}
	if b.Len() == 0 {
		return nil, errors.New("empty message")
	}
	return b.Bytes(), nil
}

// Recorder writes a session as it happens: the header line first, then
// one line per Record call.
type Recorder struct {
	w   io.Writer
	err error
}

// NewRecorder writes h's header line to w.
func NewRecorder(w io.Writer, h Header) (*Recorder, error) {
	line, err := MarshalLine(h)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(line); err != nil {
		return nil, err
	}
	return &Recorder{w: w}, nil
}

// Record appends one client message, compacted onto one line. After a
// failed write every later call returns the same error.
func (r *Recorder) Record(msg []byte) error {
	if r.err != nil {
		return r.err
	}
	m, err := compact(msg)
	if err != nil {
		return err
	}
	if _, err := r.w.Write(append(m, '\n')); err != nil {
		r.err = err
	}
	return r.err
}

// SessionName is the n-th session file in dir: `dir/<n>.jsonl`.
func SessionName(dir string, n int) string {
	return filepath.Join(dir, strconv.Itoa(n)+".jsonl")
}

// NextSession returns the number of the next session file in dir: one
// more than the largest existing `<n>.jsonl`, counting from 1. A
// missing dir gives 1.
func NextSession(fsys env.FS, dir string) (int, error) {
	ents, err := fsys.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	next := 1
	for _, e := range ents {
		num, ok := strings.CutSuffix(e.Name(), ".jsonl")
		n, err := strconv.Atoi(num)
		if !ok || err != nil || n < 1 || strconv.Itoa(n) != num {
			continue
		}
		next = max(next, n+1)
	}
	return next, nil
}

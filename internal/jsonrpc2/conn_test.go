package jsonrpc2

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestPipeEcho runs a server loop on a real OS pipe pair, the way
// `gohard lsp` runs on stdio. A client goroutine writes requests and
// notifications of random sizes (some larger than the pipe buffer)
// while another reads the responses; the server answers every request
// in order from one goroutine. CI runs it under -race (#25, #30).
func TestPipeEcho(t *testing.T) {
	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	const n = 300
	g := newGen(7)
	var frames [][]byte
	var wantIDs []ID
	for i := range n {
		pad := strings.Repeat("x", g.intn(200000))
		if i%10 != 0 {
			pad = pad[:len(pad)%300]
		}
		params, _ := json.Marshal(map[string]any{"i": i, "pad": pad})
		var m Message = &Request{ID: IntID(int64(i)), Method: "echo",
			Params: params}
		if i%3 == 0 {
			m = &Notification{Method: "note", Params: params}
		} else {
			wantIDs = append(wantIDs, IntID(int64(i)))
		}
		body, _ := Encode(m)
		frames = append(frames, AppendFrame(nil, body))
	}
	frames = append(frames, AppendFrame(nil,
		[]byte(`{"jsonrpc":"2.0","id":"bad","method":3}`)))

	writeErr := make(chan error, 1)
	go func() {
		for _, f := range frames {
			if _, err := inW.Write(f); err != nil {
				writeErr <- err
				return
			}
		}
		writeErr <- inW.Close()
	}()
	type reply struct {
		ids []ID
		err error
	}
	replies := make(chan reply, 1)
	go func() {
		r := NewReader(outR, 0)
		var ids []ID
		for {
			body, err := r.ReadFrame()
			if err == io.EOF {
				replies <- reply{ids: ids}
				return
			}
			if err != nil {
				replies <- reply{err: err}
				return
			}
			m, err := Decode(body)
			if err != nil {
				replies <- reply{err: err}
				return
			}
			ids = append(ids, m.(*Response).ID)
		}
	}()

	c := NewConn(inR, outW, Options{Buffer: 2})
	notes := 0
loop:
	for {
		ev := c.Next(nil, nil)
		switch ev.Kind {
		case EventClosed:
			break loop
		case EventMessage:
		default:
			t.Fatalf("unexpected event %v", ev.Kind)
		}
		var de *DecodeError
		switch m := ev.In.Msg.(type) {
		case *Request:
			err = c.Send(&Response{ID: m.ID, Result: m.Params})
		case *Notification:
			notes++
		case nil:
			switch {
			case errors.As(ev.In.Err, &de):
				err = c.Send(de.Response())
			case ev.In.Err != io.EOF:
				t.Fatalf("read: %v", ev.In.Err)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := outW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	got := <-replies
	if got.err != nil {
		t.Fatal(got.err)
	}
	wantIDs = append(wantIDs, StringID("bad"))
	if len(got.ids) != len(wantIDs) || notes != n/3 {
		t.Fatalf("%d replies, %d notes; want %d, %d", len(got.ids),
			notes, len(wantIDs), n/3)
	}
	for i := range wantIDs {
		if got.ids[i] != wantIDs[i] {
			t.Fatalf("reply %d has id %v, want %v", i, got.ids[i],
				wantIDs[i])
		}
	}
}

// TestSendNeverBlocks: with nobody reading the output, Send still
// returns at once; the queue drains when the reader shows up.
func TestSendNeverBlocks(t *testing.T) {
	outR, outW := io.Pipe()
	c := NewConn(strings.NewReader(""), outW, Options{})
	const n = 500
	for i := range n {
		m := &Notification{Method: "m" + strconv.Itoa(i)}
		if err := c.Send(m); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	r := NewReader(outR, 0)
	for i := range n {
		body, err := r.ReadFrame()
		if err != nil {
			t.Fatal(err)
		}
		m, _ := Decode(body)
		if m.(*Notification).Method != "m"+strconv.Itoa(i) {
			t.Fatalf("frame %d out of order: %s", i, body)
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := c.Send(&Notification{Method: "late"}); err != ErrClosed {
		t.Fatalf("send after close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }

// TestWriteErrorSticky: the first write error is returned by Close and
// by every later Send.
func TestWriteErrorSticky(t *testing.T) {
	boom := errors.New("boom")
	c := NewConn(strings.NewReader(""), failWriter{boom}, Options{})
	if err := c.Send(&Notification{Method: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != boom {
		t.Fatalf("close: %v", err)
	}
	if err := c.Send(&Notification{Method: "b"}); err != boom {
		t.Fatalf("send: %v", err)
	}
	if err := c.Send(&Request{Method: "no id"}); err == nil {
		t.Fatal("encoding error not reported")
	}
}

func TestNextEvents(t *testing.T) {
	inR, inW := io.Pipe()
	c := NewConn(inR, io.Discard, Options{})
	defer c.Close()

	wake := make(chan time.Time, 1)
	at := time.Date(2000, 1, 1, 0, 0, 1, 0, time.UTC)
	wake <- at
	if ev := c.Next(wake, nil); ev.Kind != EventWake || !ev.Time.Equal(at) {
		t.Fatalf("wake: %+v", ev)
	}
	done := make(chan struct{})
	close(done)
	wake <- at
	if ev := c.Next(wake, done); ev.Kind != EventDone {
		t.Fatalf("done first: %+v", ev)
	}

	frame := AppendFrame(nil, []byte(`{"jsonrpc":"2.0","method":"x"}`))
	frame = append(frame, "Content-Length: 1\r\n\r\n{"...)
	go func() {
		inW.Write(frame)
		inW.Close()
	}()
	ev := c.Next(nil, nil)
	if ev.Kind != EventMessage || ev.In.Msg.(*Notification).Method != "x" ||
		string(ev.In.Raw) != `{"jsonrpc":"2.0","method":"x"}` {
		t.Fatalf("message: %+v", ev)
	}
	ev = c.Next(nil, nil)
	var de *DecodeError
	if !errors.As(ev.In.Err, &de) || de.Err.Code != CodeParseError ||
		string(ev.In.Raw) != "{" || ev.In.Terminal() {
		t.Fatalf("parse error: %+v", ev)
	}
	ev = c.Next(nil, nil)
	if ev.In.Err != io.EOF || !ev.In.Terminal() {
		t.Fatalf("eof: %+v", ev)
	}
	if ev := c.Next(nil, nil); ev.Kind != EventClosed {
		t.Fatalf("closed: %+v", ev)
	}
}

// TestCloseStopsWaitingReader: a reader blocked on delivering to a full
// buffer exits on Close instead of leaking.
func TestCloseStopsWaitingReader(t *testing.T) {
	var stream []byte
	for range 10 {
		stream = AppendFrame(stream, []byte(`{"jsonrpc":"2.0","method":"m"}`))
	}
	c := NewConn(strings.NewReader(string(stream)), io.Discard,
		Options{Buffer: 1})
	if ev := c.Next(nil, nil); ev.Kind != EventMessage {
		t.Fatalf("%+v", ev)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; ; i++ {
		ev := c.Next(nil, nil)
		if ev.Kind == EventClosed {
			break
		}
		if i > 10 {
			t.Fatal("reader kept delivering after Close")
		}
	}
}

package jsonrpc2

import (
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
)

// header returns a random but valid header block for a body of n
// bytes: any letter case, padding, bare "\n" line ends, extra and
// Content-Type headers, a repeated equal Content-Length.
func (g *gen) header(n int) string {
	var lines []string
	name := []string{"Content-Length", "content-length",
		"CONTENT-LENGTH", "Content-length"}[g.intn(4)]
	pad := []string{"", " ", "  ", "\t", " \t "}
	length := name + ":" + pad[g.intn(len(pad))] + strconv.Itoa(n) +
		pad[g.intn(2)]
	lines = append(lines, length)
	if g.bool() {
		lines = append(lines, []string{
			"Content-Type: application/vscode-jsonrpc; charset=utf-8",
			"content-type:application/vscode-jsonrpc;charset=\"UTF8\"",
			"Content-Type: application/json",
			"X-Extra: whatever: with colons",
			"X-Empty:",
		}[g.intn(5)])
	}
	if g.intn(4) == 0 {
		lines = append(lines, length)
	}
	if g.bool() {
		lines[0], lines[len(lines)-1] = lines[len(lines)-1], lines[0]
	}
	eol := "\r\n"
	if g.intn(4) == 0 {
		eol = "\n"
	}
	lead := ""
	if g.intn(5) == 0 {
		lead = "\r\n"
	}
	return lead + strings.Join(lines, eol) + eol + eol
}

// chunks splits b at random points, sometimes into single bytes.
func (g *gen) chunks(b []byte) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		n := 1 + g.intn(len(b))
		if g.intn(3) == 0 {
			n = 1
		}
		out = append(out, b[:n])
		b = b[n:]
	}
	return out
}

func readAll(t *testing.T, r *Reader) [][]byte {
	t.Helper()
	var got [][]byte
	for i := 0; ; i++ {
		body, err := r.ReadFrame()
		if err == io.EOF {
			return got
		}
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		got = append(got, body)
		if i > 1000 {
			t.Fatal("no end of input")
		}
	}
}

// TestFramingRoundTrip: frames written with any valid header variant
// and read through any split into short reads give back exactly the
// bodies, in order, then io.EOF.
func TestFramingRoundTrip(t *testing.T) {
	for seed := range uint64(1500) {
		g := newGen(seed)
		var stream []byte
		var want [][]byte
		for range g.intn(6) {
			body, _ := Encode(g.message())
			want = append(want, body)
			if g.bool() {
				stream = AppendFrame(stream, body)
			} else {
				stream = append(stream, g.header(len(body))...)
				stream = append(stream, body...)
			}
		}
		r := NewReader(simenv.NewScript(g.chunks(stream)...), 0)
		got := readAll(t, r)
		if len(got) != len(want) {
			t.Fatalf("seed %d: %d frames, want %d", seed, len(got),
				len(want))
		}
		for i := range got {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("seed %d frame %d: %q, want %q", seed, i,
					got[i], want[i])
			}
			if _, err := Decode(got[i]); err != nil {
				t.Fatalf("seed %d frame %d: %v", seed, i, err)
			}
		}
		if _, err := r.ReadFrame(); err != io.EOF {
			t.Fatalf("seed %d: after EOF got %v", seed, err)
		}
	}
}

// TestFramingShortReads reads frames from a simulated file whose reads
// are cut short at seeded points (P6).
func TestFramingShortReads(t *testing.T) {
	for seed := range uint64(50) {
		g := newGen(seed)
		var stream []byte
		var want [][]byte
		for range 1 + g.intn(20) {
			body, _ := Encode(g.message())
			want = append(want, body)
			stream = AppendFrame(stream, body)
		}
		s := simenv.New(simenv.Options{Seed: seed,
			Files:  map[string][]byte{"in": stream},
			Faults: simenv.Faults{ShortRead: 900}})
		f, err := s.Env().FS.Open("in")
		if err != nil {
			t.Fatal(err)
		}
		got := readAll(t, NewReader(f, 0))
		if len(got) != len(want) {
			t.Fatalf("seed %d: %d frames, want %d", seed, len(got),
				len(want))
		}
		for i := range got {
			if !bytes.Equal(got[i], want[i]) {
				t.Fatalf("seed %d frame %d differs", seed, i)
			}
		}
	}
}

func TestMalformedHeaders(t *testing.T) {
	long := "X: " + strings.Repeat("a", MaxHeaderLine) + "\r\n"
	many := strings.Repeat("X: y\r\n", MaxHeaders+1)
	tests := []struct {
		in  string
		eof bool // want io.ErrUnexpectedEOF instead of a HeaderError
	}{
		{in: "Content-Length 5\r\n\r\nhello"},
		{in: "Content-Length: \r\n\r\n"},
		{in: "Content-Length: -1\r\n\r\n"},
		{in: "Content-Length: +5\r\n\r\nhello"},
		{in: "Content-Length: 5 5\r\n\r\nhello"},
		{in: "Content-Length: 1e3\r\n\r\n"},
		{in: "Content-Length: 0x10\r\n\r\n"},
		{in: "Content-Length: 1234567890123456789\r\n\r\n"},
		{in: "Content-Length: ５\r\n\r\nhello"},
		{in: "Content-Length: 5\r\nContent-Length: 6\r\n\r\nhello!"},
		{in: "Content-Type: application/json\r\n\r\n{}"},
		{in: " Content-Length: 2\r\n\r\n{}"},
		{in: "Content Length: 2\r\n\r\n{}"},
		{in: ": 2\r\n\r\n{}"},
		{in: "Content-Length: 2\r\n" + long + "\r\n{}"},
		{in: "Content-Length: 2\r\n" + many + "\r\n{}"},
		{in: "Content-Length: 2\r\n", eof: true},
		{in: "Content-Length: 2", eof: true},
		{in: "Content-Length: 2\r\n\r\n{", eof: true},
		{in: "Content-Length: 10\r\n\r\n", eof: true},
		{in: "C", eof: true},
	}
	for _, tt := range tests {
		r := NewReader(strings.NewReader(tt.in), 0)
		body, err := r.ReadFrame()
		var he *HeaderError
		switch {
		case tt.eof && err != io.ErrUnexpectedEOF,
			!tt.eof && !errors.As(err, &he):
			t.Errorf("%q: got %q, %v", tt.in, body, err)
			continue
		}
		// Terminal errors are sticky.
		if _, again := r.ReadFrame(); again != err {
			t.Errorf("%q: second read %v, want %v", tt.in, again, err)
		}
	}
}

// TestSkippedFrames: an oversized or non-UTF-8 body is skipped and the
// stream stays in sync.
func TestSkippedFrames(t *testing.T) {
	stream := "Content-Length: 11\r\n\r\n12345678901" +
		"Content-Length: 2\r\nContent-Type: text/x; charset=latin1" +
		"\r\n\r\n{}" +
		"Content-Length: 2\r\n\r\nok"
	r := NewReader(strings.NewReader(stream), 10)
	var se *SkippedError
	if _, err := r.ReadFrame(); !errors.As(err, &se) || se.Size != 11 {
		t.Fatalf("oversized: %v", err)
	}
	if _, err := r.ReadFrame(); !errors.As(err, &se) || se.Size != 2 {
		t.Fatalf("latin1: %v", err)
	}
	if b, err := r.ReadFrame(); err != nil || string(b) != "ok" {
		t.Fatalf("after skips: %q, %v", b, err)
	}
	if _, err := r.ReadFrame(); err != io.EOF {
		t.Fatalf("end: %v", err)
	}
	// An oversized frame cut short is unexpected EOF, not a skip.
	r = NewReader(strings.NewReader("Content-Length: 99\r\n\r\nabc"), 10)
	if _, err := r.ReadFrame(); err != io.ErrUnexpectedEOF {
		t.Fatalf("truncated oversized: %v", err)
	}
}

// TestReaderNeverPanics: random and mutated streams end in a bounded
// number of reads with io.EOF or a terminal error, never a panic.
func TestReaderNeverPanics(t *testing.T) {
	for seed := range uint64(3000) {
		g := newGen(seed)
		var stream []byte
		for range g.intn(4) {
			body, _ := Encode(g.message())
			stream = append(stream, g.header(len(body))...)
			stream = append(stream, body...)
		}
		for range 1 + g.intn(4) {
			if len(stream) == 0 {
				stream = append(stream, byte(g.intn(256)))
				continue
			}
			i := g.intn(len(stream))
			switch g.intn(3) {
			case 0:
				stream[i] = byte(g.intn(256))
			case 1:
				stream = stream[:i]
			default:
				stream = append(stream[:i:i],
					append([]byte("\r\n:0"[g.intn(4):][:1]),
						stream[i:]...)...)
			}
		}
		r := NewReader(simenv.NewScript(g.chunks(stream)...), 1<<16)
		for reads := 0; ; reads++ {
			if reads > len(stream)+2 {
				t.Fatalf("seed %d: reader does not terminate", seed)
			}
			_, err := r.ReadFrame()
			var se *SkippedError
			if err != nil && !errors.As(err, &se) {
				break
			}
		}
	}
}

func TestWriteFrame(t *testing.T) {
	var w countWriter
	if err := WriteFrame(&w, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if w.calls != 1 || w.buf.String() != "Content-Length: 2\r\n\r\n{}" {
		t.Fatalf("%d calls, %q", w.calls, w.buf.String())
	}
	if err := WriteFrame(shortWriter{}, []byte(`{}`)); err !=
		io.ErrShortWrite {
		t.Fatalf("short write: %v", err)
	}
}

type countWriter struct {
	buf   bytes.Buffer
	calls int
}

func (w *countWriter) Write(p []byte) (int, error) {
	w.calls++
	return w.buf.Write(p)
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }

// TestSkippedRequestID: a skipped frame keeps the ID of a request, so
// the server can still answer it (#25): from the whole body for a
// charset skip, from the first SkipPeek bytes of an oversized one.
func TestSkippedRequestID(t *testing.T) {
	big := `{"jsonrpc":"2.0","id":"r7","method":"x","params":"` +
		strings.Repeat("a", 3*SkipPeek) + `"}`
	late := `{"jsonrpc":"2.0","params":"` + strings.Repeat("a", SkipPeek) +
		`","id":9,"method":"x"}`
	note := `{"jsonrpc":"2.0","method":"x"}`
	resp := `{"jsonrpc":"2.0","id":3,"result":null}`
	latin := func(b string) string {
		return "Content-Length: " + strconv.Itoa(len(b)) +
			"\r\nContent-Type: a/b; charset=latin1\r\n\r\n" + b
	}
	plain := func(b string) string {
		return "Content-Length: " + strconv.Itoa(len(b)) + "\r\n\r\n" + b
	}
	tests := []struct {
		frame string
		id    ID
		req   bool
	}{
		{latin(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`),
			IntID(2), true},
		{latin(note), ID{}, false},
		{latin(resp), ID{}, false},
		{latin(`{"jsonrpc":"2.0","id":null,"method":"x"}`), ID{}, false},
		{latin(`not json`), ID{}, false},
		{plain(big), StringID("r7"), true},
		{plain(late), ID{}, false}, // the ID is past the peek window
	}
	for i, tt := range tests {
		r := NewReader(strings.NewReader(tt.frame+plain("ok")), SkipPeek)
		_, err := r.ReadFrame()
		var se *SkippedError
		if !errors.As(err, &se) {
			t.Fatalf("%d: %v, want a skip", i, err)
		}
		if se.Request != tt.req || se.ID != tt.id {
			t.Errorf("%d: got %v %v, want %v %v", i, se.Request, se.ID,
				tt.req, tt.id)
		}
		if b, err := r.ReadFrame(); err != nil || string(b) != "ok" {
			t.Fatalf("%d: after the skip: %q, %v", i, b, err)
		}
	}
}

// TestPeekRequestIDPrefixes: for every prefix of random messages,
// peekRequestID reports either nothing or the request's own ID, never
// a wrong one, and the whole body of a request yields its ID.
func TestPeekRequestIDPrefixes(t *testing.T) {
	for seed := range uint64(500) {
		m := newGen(seed).message()
		body, err := Encode(m)
		if err != nil {
			t.Fatal(err)
		}
		req, isReq := m.(*Request)
		for n := 0; n <= len(body); n++ {
			id, ok := peekRequestID(body[:n])
			if ok && (!isReq || id != req.ID) {
				t.Fatalf("seed %d prefix %d: got %v for %s", seed, n, id,
					body)
			}
		}
		if id, ok := peekRequestID(body); isReq && !req.ID.IsNull() &&
			(!ok || id != req.ID) {
			t.Fatalf("seed %d: whole body gave %v %v for %s", seed, id,
				ok, body)
		}
	}
}

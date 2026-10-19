package jsonrpc2

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// DefaultMaxMessage is the default size limit of one message body:
// 64 MiB, room for the largest file GoHard parses (10 MB, #20) even if
// every byte is escaped in JSON.
const DefaultMaxMessage = 64 << 20

// Header limits. A header line longer than MaxHeaderLine or a frame
// with more than MaxHeaders header lines is a [*HeaderError].
const (
	MaxHeaderLine = 4096
	MaxHeaders    = 32
)

// HeaderError is a malformed frame header. The stream has lost sync,
// so the [Reader] returns the same error from then on.
type HeaderError struct {
	// Line is the 1-based header line of the frame, 0 for errors about
	// the header block as a whole.
	Line int
	Msg  string
}

// Error implements error.
func (e *HeaderError) Error() string {
	if e.Line == 0 {
		return "jsonrpc2: bad header: " + e.Msg
	}
	return "jsonrpc2: bad header line " + strconv.Itoa(e.Line) + ": " +
		e.Msg
}

// SkippedError is a frame whose body was read and discarded: it was
// over the size limit or its Content-Type named a charset other than
// UTF-8. (A body that is not valid UTF-8 is not skipped: [Decode]
// rejects it with ParseError, so the client gets an answer.) The
// stream is still in sync and the next [Reader.ReadFrame] reads the
// next frame.
//
// When the skipped body was a request whose ID could be read (from the
// whole body for a charset skip, from its first [SkipPeek] bytes for
// an oversized one), Request is true and ID holds it, so the server
// can still give the request its one error response (#25).
type SkippedError struct {
	Size    int64
	Reason  string
	ID      ID
	Request bool
}

// SkipPeek is how many bytes of an oversized body the Reader keeps to
// find the request ID of a skipped frame.
const SkipPeek = 4096

// Error implements error.
func (e *SkippedError) Error() string {
	return "jsonrpc2: skipped a " + strconv.FormatInt(e.Size, 10) +
		"-byte message: " + e.Reason
}

// Reader reads LSP base-protocol frames. It is not safe for
// concurrent use.
type Reader struct {
	r   *bufio.Reader
	max int64
	err error // sticky terminal error
}

// NewReader returns a Reader over r that accepts bodies of at most max
// bytes; max <= 0 means [DefaultMaxMessage].
func NewReader(r io.Reader, max int64) *Reader {
	if max <= 0 {
		max = DefaultMaxMessage
	}
	return &Reader{r: bufio.NewReaderSize(r, MaxHeaderLine+2), max: max}
}

// ReadFrame returns the body of the next frame. At a clean end of input
// (no byte of a new frame read) it returns io.EOF. A *SkippedError
// leaves the Reader usable. Every other error is terminal and is
// returned again by later calls: a *HeaderError, io.ErrUnexpectedEOF
// for input that ends inside a frame, or the underlying read error.
func (r *Reader) ReadFrame() ([]byte, error) {
	if r.err != nil {
		return nil, r.err
	}
	body, err := r.readFrame()
	var skip *SkippedError
	if err != nil && !errors.As(err, &skip) {
		r.err = err
	}
	return body, err
}

type header struct {
	length  int64
	hasLen  bool
	charset string
}

func (r *Reader) readFrame() ([]byte, error) {
	h, err := r.readHeader()
	if err != nil {
		return nil, err
	}
	if h.length > r.max {
		peek := make([]byte, min(h.length, SkipPeek))
		if _, err := io.ReadFull(r.r, peek); err != nil {
			return nil, unexpected(err)
		}
		if err := r.discard(h.length - int64(len(peek))); err != nil {
			return nil, err
		}
		se := &SkippedError{Size: h.length, Reason: fmt.Sprintf(
			"over the %d-byte limit", r.max)}
		se.ID, se.Request = peekRequestID(peek)
		return nil, se
	}
	body := make([]byte, h.length)
	if _, err := io.ReadFull(r.r, body); err != nil {
		return nil, unexpected(err)
	}
	if h.charset != "" && !utf8Charset(h.charset) {
		se := &SkippedError{Size: h.length,
			Reason: "charset " + strconv.Quote(h.charset) +
				" is not utf-8"}
		se.ID, se.Request = peekRequestID(body)
		return nil, se
	}
	return body, nil
}

// peekRequestID reads the top-level "id" and "method" members of a
// JSON object that may be cut short. It reports a request only when
// both were read and the ID is not null; a cut-off body never yields a
// half-read ID.
func peekRequestID(body []byte) (ID, bool) {
	dec := json.NewDecoder(bytes.NewReader(body))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return ID{}, false
	}
	var id ID
	hasID, hasMethod := false, false
	for dec.More() && !(hasID && hasMethod) {
		t, err := dec.Token()
		key, ok := t.(string)
		if err != nil || !ok {
			return ID{}, false
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return ID{}, false
		}
		// A number at the very end of a cut body may be cut too.
		rest := body[dec.InputOffset():]
		if len(bytes.TrimLeft(rest, " \t\r\n")) == 0 {
			return ID{}, false
		}
		switch key {
		case "id":
			if id.UnmarshalJSON(v) != nil {
				return ID{}, false
			}
			hasID = true
		case "method":
			hasMethod = true
		case "result", "error":
			return ID{}, false // a response
		}
	}
	if !hasID || !hasMethod || id.IsNull() {
		return ID{}, false
	}
	return id, true
}

func (r *Reader) discard(n int64) error {
	if _, err := io.CopyN(io.Discard, r.r, n); err != nil {
		return unexpected(err)
	}
	return nil
}

func unexpected(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}

func (r *Reader) readHeader() (header, error) {
	var h header
	lines := 0
	for {
		line, err := r.readLine(lines > 0)
		if err != nil {
			return h, err
		}
		if len(line) == 0 {
			if lines == 0 {
				continue // tolerate blank lines between frames
			}
			break
		}
		lines++
		if lines > MaxHeaders {
			return h, &HeaderError{Line: lines, Msg: fmt.Sprintf(
				"more than %d header lines", MaxHeaders)}
		}
		if err := h.field(lines, line); err != nil {
			return h, err
		}
	}
	if !h.hasLen {
		return h, &HeaderError{Msg: "no Content-Length"}
	}
	return h, nil
}

// readLine returns the next line without its "\r\n" or "\n". inFrame
// reports whether a header line of this frame was already read, which
// decides between io.EOF and io.ErrUnexpectedEOF at the end of input.
func (r *Reader) readLine(inFrame bool) ([]byte, error) {
	line, err := r.r.ReadSlice('\n')
	switch {
	case err == bufio.ErrBufferFull:
		return nil, &HeaderError{Msg: fmt.Sprintf(
			"header line longer than %d bytes", MaxHeaderLine)}
	case err == io.EOF && len(line) == 0 && !inFrame:
		return nil, io.EOF
	case err != nil:
		return nil, unexpected(err)
	}
	line = line[:len(line)-1]
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	if len(line) > MaxHeaderLine {
		return nil, &HeaderError{Msg: fmt.Sprintf(
			"header line longer than %d bytes", MaxHeaderLine)}
	}
	return line, nil
}

func (h *header) field(n int, line []byte) error {
	name, value, ok := bytes.Cut(line, []byte(":"))
	if !ok {
		return &HeaderError{Line: n, Msg: "no colon"}
	}
	if !isToken(name) {
		return &HeaderError{Line: n, Msg: "invalid header name " +
			strconv.Quote(string(name))}
	}
	value = bytes.Trim(value, " \t")
	switch strings.ToLower(string(name)) {
	case "content-length":
		v, err := parseLength(value)
		if err != nil {
			return &HeaderError{Line: n, Msg: err.Error()}
		}
		if h.hasLen && v != h.length {
			return &HeaderError{Line: n,
				Msg: "conflicting Content-Length headers"}
		}
		h.length, h.hasLen = v, true
	case "content-type":
		h.charset = charset(string(value))
	}
	return nil
}

// parseLength accepts 1 to 18 ASCII digits, so the value never
// overflows int64.
func parseLength(v []byte) (int64, error) {
	if len(v) == 0 || len(v) > 18 {
		return 0, errors.New("invalid Content-Length " +
			strconv.Quote(string(v)))
	}
	var n int64
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid Content-Length " +
				strconv.Quote(string(v)))
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

// isToken reports whether b is a non-empty RFC 7230 token.
func isToken(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// charset returns the charset parameter of a Content-Type value, or ""
// when there is none.
func charset(v string) string {
	for _, p := range strings.Split(v, ";")[1:] {
		k, val, ok := strings.Cut(p, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), "charset") {
			return strings.Trim(strings.TrimSpace(val), `"`)
		}
	}
	return ""
}

// utf8Charset accepts "utf-8" and the legacy "utf8" that LSP allows.
func utf8Charset(cs string) bool {
	return strings.EqualFold(cs, "utf-8") || strings.EqualFold(cs, "utf8")
}

// AppendFrame appends the frame for body to dst: a Content-Length
// header, a blank line and the body.
func AppendFrame(dst, body []byte) []byte {
	dst = append(dst, "Content-Length: "...)
	dst = strconv.AppendInt(dst, int64(len(body)), 10)
	dst = append(dst, "\r\n\r\n"...)
	return append(dst, body...)
}

// WriteFrame writes the frame for body to w in a single Write call.
func WriteFrame(w io.Writer, body []byte) error {
	frame := AppendFrame(make([]byte, 0, len(body)+32), body)
	n, err := w.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	return err
}

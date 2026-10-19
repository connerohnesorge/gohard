package jsonrpc2

import (
	"errors"
	"io"
	"sync"
	"time"
)

// Options configure [NewConn].
type Options struct {
	// MaxMessage is the body size limit; <= 0 means
	// [DefaultMaxMessage].
	MaxMessage int64
	// Buffer is how many received messages may wait for [Conn.Next]
	// before the reader stops reading; <= 0 means 64. Unread input
	// stays in the pipe, so the client sees back-pressure, never loss.
	Buffer int
}

// Incoming is one result of the reader goroutine.
type Incoming struct {
	// Msg is the decoded message, nil when Err is set.
	Msg Message
	// Raw is the frame body as received (also for undecodable bodies),
	// for session recording (#29). It is nil for frame errors.
	Raw []byte
	// Err is nil, a *DecodeError (reply with its Response), a
	// *SkippedError (the frame was dropped), or a terminal error: io.EOF
	// at a clean end of input, or a *HeaderError, io.ErrUnexpectedEOF
	// or read error. After a terminal error nothing more arrives.
	Err error
}

// Terminal reports whether in ends the input stream.
func (in Incoming) Terminal() bool {
	if in.Err == nil {
		return false
	}
	var de *DecodeError
	var se *SkippedError
	return !errors.As(in.Err, &de) && !errors.As(in.Err, &se)
}

// Readier is implemented by an input that can tell whether its next
// Read returns without waiting for the client: the bytes (or the end of
// the input) it returns have already arrived. A simulated transport
// (simenv.Script) is always ready. [Conn] treats any other input as
// one whose Read may wait.
type Readier interface {
	Ready() bool
}

// EventKind says what [Conn.Next] returned.
type EventKind uint8

// The events of Next.
const (
	// EventMessage: Event.In holds a received message or error.
	EventMessage EventKind = iota + 1
	// EventWake: the wake channel delivered Event.Time.
	EventWake
	// EventDone: the done channel was closed.
	EventDone
	// EventClosed: the input has ended; the terminal Incoming was
	// already returned.
	EventClosed
)

// Event is what [Conn.Next] waited for.
type Event struct {
	Kind EventKind
	In   Incoming
	Time time.Time
}

// ErrClosed is returned by [Conn.Send] after [Conn.Close].
var ErrClosed = errors.New("jsonrpc2: connection closed")

// Conn runs the two stdio goroutines of the language server: a reader
// that frames and decodes input and a writer that drains an unbounded
// output queue. Next and Send may be called from one goroutine (the
// server's event loop); Send, Close and Err are also safe to call
// concurrently.
type Conn struct {
	in   chan Incoming
	quit chan struct{} // closed by Close; stops a blocked reader

	// rmu guards waiting: a channel that is closed while the reader
	// goroutine waits for the client (in a Read that may block) or has
	// exited, and open while it reads, decodes or delivers input that
	// has already arrived.
	rmu     sync.Mutex
	waiting chan struct{}
	parked  bool

	mu     sync.Mutex
	cond   *sync.Cond
	queue  [][]byte
	closed bool
	werr   error         // first write error; sticky
	wdone  chan struct{} // closed when the writer exits
	once   sync.Once
	closeE error
}

// NewConn starts the reader goroutine on r and the writer goroutine on
// w. Nothing else in GoHard starts a goroutine.
func NewConn(r io.Reader, w io.Writer, o Options) *Conn {
	buf := o.Buffer
	if buf <= 0 {
		buf = 64
	}
	c := &Conn{
		in:      make(chan Incoming, buf),
		quit:    make(chan struct{}),
		wdone:   make(chan struct{}),
		waiting: make(chan struct{}),
	}
	c.cond = sync.NewCond(&c.mu)
	ready, _ := r.(Readier)
	go c.read(NewReader(&input{r: r, ready: ready, c: c}, o.MaxMessage))
	go c.write(w)
	return c
}

func (c *Conn) read(fr *Reader) {
	defer c.park()
	defer close(c.in)
	for {
		in := receive(fr)
		select {
		case c.in <- in:
		case <-c.quit:
			return
		}
		if in.Terminal() {
			return
		}
	}
}

// input is the transport as the reader goroutine sees it: a Read that
// may wait for the client parks the reader for its duration, so
// [Conn.Next] knows that everything that has arrived was delivered.
type input struct {
	r     io.Reader
	ready Readier // nil: every Read may wait
	c     *Conn
}

func (in *input) Read(p []byte) (int, error) {
	if in.ready == nil || !in.ready.Ready() {
		in.c.park()
		defer in.c.unpark()
	}
	return in.r.Read(p)
}

// park marks the reader as waiting for the client (or gone).
func (c *Conn) park() {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if !c.parked {
		c.parked = true
		close(c.waiting)
	}
}

// unpark marks the reader as busy with input that has arrived.
func (c *Conn) unpark() {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if c.parked {
		c.parked = false
		c.waiting = make(chan struct{})
	}
}

// receive reads and decodes one frame.
func receive(fr *Reader) Incoming {
	body, err := fr.ReadFrame()
	if err != nil {
		return Incoming{Err: err}
	}
	msg, err := Decode(body)
	return Incoming{Msg: msg, Raw: body, Err: err}
}

func (c *Conn) write(w io.Writer) {
	defer close(c.wdone)
	for {
		c.mu.Lock()
		for len(c.queue) == 0 && !c.closed {
			c.cond.Wait()
		}
		if len(c.queue) == 0 { // closed and drained
			c.mu.Unlock()
			return
		}
		frame := c.queue[0]
		c.queue[0] = nil
		c.queue = c.queue[1:]
		c.mu.Unlock()
		n, err := w.Write(frame)
		if err == nil && n != len(frame) {
			err = io.ErrShortWrite
		}
		if err != nil {
			c.mu.Lock()
			c.werr = err
			c.queue = nil
			c.mu.Unlock()
			return
		}
	}
}

// Next blocks until a message arrives, wake delivers a time or done is
// closed, and returns which. A nil wake or done never fires, so a
// shell with no pending timer passes nil. When several are ready Next
// prefers done, then input, then wake.
//
// Input that has arrived always comes before wake: while the reader
// goroutine is reading, decoding or delivering bytes it has already
// received (or bytes a [Readier] input has ready), Next waits for that
// message instead of returning EventWake. Only once the reader waits
// for the client can wake fire. So how far the core gets before a
// message reaches it depends on when the client sent it, never on how
// fast the reader goroutine ran; with a transport that never waits
// (simenv) the whole input reaches the core before it runs (P1). The
// order the core sees is the order the shell records (#29), so replay
// does not depend on it either.
func (c *Conn) Next(wake <-chan time.Time, done <-chan struct{}) Event {
	select {
	case <-done:
		return Event{Kind: EventDone}
	default:
	}
	select {
	case in, ok := <-c.in:
		return message(in, ok)
	default:
	}
	if wake != nil {
		c.rmu.Lock()
		waiting := c.waiting
		c.rmu.Unlock()
		select {
		case <-done:
			return Event{Kind: EventDone}
		case in, ok := <-c.in:
			return message(in, ok)
		case <-waiting:
		}
		// The reader delivers a message before it waits again, so
		// one that arrived is in c.in now.
		select {
		case in, ok := <-c.in:
			return message(in, ok)
		default:
		}
	}
	select {
	case <-done:
		return Event{Kind: EventDone}
	case in, ok := <-c.in:
		return message(in, ok)
	case t := <-wake:
		return Event{Kind: EventWake, Time: t}
	}
}

func message(in Incoming, ok bool) Event {
	if !ok {
		return Event{Kind: EventClosed}
	}
	return Event{Kind: EventMessage, In: in}
}

// Send encodes m and queues its frame for the writer. It never waits
// for I/O. It returns an encoding error, the first write error of the
// writer, or ErrClosed after Close.
func (c *Conn) Send(m Message) error {
	body, err := Encode(m)
	if err != nil {
		return err
	}
	frame := AppendFrame(make([]byte, 0, len(body)+32), body)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.werr != nil:
		return c.werr
	case c.closed:
		return ErrClosed
	}
	c.queue = append(c.queue, frame)
	c.cond.Signal()
	return nil
}

// Err returns the first write error, or nil.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.werr
}

// Close stops accepting messages, waits until the writer has written
// every queued frame (or failed) and returns the first write error. It
// also stops the reader if it is waiting to deliver; a reader blocked
// in Read returns when the input ends, which for stdio is process
// exit. Close is idempotent.
func (c *Conn) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cond.Broadcast()
		c.mu.Unlock()
		close(c.quit)
		<-c.wdone
		c.closeE = c.Err()
	})
	return c.closeE
}

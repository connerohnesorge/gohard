package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/env"
	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
)

// Serve runs the stdio language server on e.Transport until the
// client exits, the input ends or ctx is cancelled, and returns the
// exit code: 0 after shutdown and exit, else 1 (LSP), or 0 when ctx
// was cancelled (the caller maps the cancellation cause to 130 or
// 143). record, when non-nil, is called with every client input
// before the core sees it (`gohard lsp --record`); its error stops
// nothing, the caller reports it.
//
// While the core has queued events Serve runs them one at a time and,
// before each, queues every client message that has already arrived,
// so a $/cancelRequest or an edit reaches running work at its next
// slice boundary (#25). "Arrived" means received from the transport,
// not merely decoded: jsonrpc2.Conn.Next hands over every message whose
// bytes are in hand before it lets the core step, so the step a message
// is queued at depends on when the client sent it, never on how fast
// the reader goroutine ran, and a simulated transport (simenv.Script)
// is queued whole before the first step (P1). When the core is idle
// Serve waits for input.
//
// Every gohard.yaml read is passed to record too (Arrival.Config), so
// a session that reloads its configuration replays byte for byte. A
// frame the transport skips is logged on stderr; when it was a request
// the core answers it (Rejected). A fatal framing error (a bad header,
// input cut inside a frame) is logged on stderr before Serve returns 1
// (#20).
//
// The two goroutines are jsonrpc2's reader and writer; the core runs
// on the calling goroutine (#25).
func Serve(ctx context.Context, e env.Env, o Options, jobs int,
	record func(Arrival) error,
) int {
	conn := jsonrpc2.NewConn(e.Transport.Stdin, e.Transport.Stdout,
		jsonrpc2.Options{})
	defer conn.Close()
	d := NewDriver(o, e, jobs, func(s Send) error {
		return conn.Send(s.Msg)
	})
	// ready is a wake channel that is always ready: with it Next
	// returns a waiting message, or EventWake at once when there is
	// none.
	ready := make(chan time.Time)
	close(ready)
	logf := func(format string, args ...any) {
		if e.Transport.Stderr != nil {
			fmt.Fprintf(e.Transport.Stderr, "gohard: lsp: "+format+"\n",
				args...)
		}
	}
	if record != nil {
		d.loadConfig = func(c LoadConfig) config.Loaded {
			l := config.Load(e.FS, c.Folder)
			_ = record(Arrival{Step: d.Steps(),
				Config: configRead(c.Folder, l)})
			return l
		}
		loads := 0
		d.loadFiles = func(l LoadWorkspace) WorkspaceLoaded {
			w := LoadFiles(e, jobs, l.Root)
			if loads > 0 {
				_ = record(Arrival{Step: d.Steps(),
					Workspace: workspaceRead(w)})
			}
			loads++
			return w
		}
	}
	arrive := func(a Arrival) {
		a.Step, a.Busy = d.Steps(), !d.Idle()
		if record != nil {
			_ = record(a)
		}
		switch {
		case a.Rejected != nil:
			d.PushRejected(*a.Rejected)
		case a.Body == nil:
			d.PushClosed()
		default:
			d.Push(a.Body)
		}
	}
	ended := false
	for {
		var wake <-chan time.Time
		if !d.Idle() {
			wake = ready
		}
		var err error
		switch ev := conn.Next(wake, ctx.Done()); ev.Kind {
		case jsonrpc2.EventDone:
			return 0
		case jsonrpc2.EventWake:
			err = d.Step()
		case jsonrpc2.EventClosed:
			if !ended {
				ended = true
				arrive(Arrival{})
			}
			// Nothing more can arrive: finish the session.
			err = d.Run()
		case jsonrpc2.EventMessage:
			var skip *jsonrpc2.SkippedError
			switch in := ev.In; {
			case in.Raw != nil:
				arrive(Arrival{Body: in.Raw})
			case errors.As(in.Err, &skip):
				logf("%v", skip)
				if skip.Request {
					arrive(Arrival{Rejected: &Rejected{ID: skip.ID,
						Reason: skip.Reason}})
				}
			case in.Terminal():
				switch {
				case errors.Is(in.Err, io.ErrUnexpectedEOF):
					logf("the input ended inside a message")
				case !errors.Is(in.Err, io.EOF):
					logf("%v", in.Err)
				}
				ended = true
				arrive(Arrival{})
			}
		}
		if code, ok := d.Exited(); ok {
			if cerr := conn.Close(); cerr != nil {
				return 1
			}
			return code
		}
		if err != nil {
			return 1 // the client stopped reading
		}
	}
}

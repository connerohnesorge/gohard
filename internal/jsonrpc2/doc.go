// Package jsonrpc2 is GoHard's hand-rolled JSON-RPC 2.0 over the LSP
// base protocol (#25): Content-Length framed messages on a byte stream.
//
// The package has three layers:
//
//   - Messages: [Request], [Notification] and [Response], with [ID]
//     (an integer or a string) and [Error] (the JSON-RPC and LSP error
//     codes). [Encode] writes a message with a fixed key order (jsonrpc,
//     id, method, params, result, error), so the same message always
//     gives the same bytes (P1). [Decode] is strict about the JSON-RPC
//     shape and reports problems as a [*DecodeError] that knows the
//     reply to send.
//   - Framing: [Reader] reads one frame body at a time. Header parsing
//     is strict where the stream would otherwise lose sync and tolerant
//     elsewhere: header names are case-insensitive, values may carry
//     spaces, a bare "\n" ends a line like "\r\n", blank lines between
//     frames are skipped, unknown headers and Content-Type are ignored.
//     A missing, malformed or conflicting Content-Length is fatal; a
//     body over the size limit or in another charset is skipped and the
//     stream stays usable. Nothing on any input panics.
//     [WriteFrame] writes a frame with a single Write call.
//   - [Conn]: the stdio shell. It starts exactly two goroutines, one
//     reader and one writer. They are the only goroutines in GoHard
//     (#4 P8, #25), and this is the only package detlint lets use go
//     and select statements (#30). The single-threaded server core
//     receives with [Conn.Next], which waits for a message, a wake-up
//     channel (a timer from the Clock door) or cancellation, so the
//     core itself never needs a select. Input that has arrived always
//     comes before a wake-up (see [Readier]). [Conn.Send] never blocks on
//     I/O: messages go into an unbounded queue drained by the writer,
//     so a client that writes while not reading cannot deadlock the
//     server.
package jsonrpc2

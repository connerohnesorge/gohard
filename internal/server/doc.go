// Package server is the GoHard language server (#25): a pure,
// single-threaded state machine and the thin shell that runs it.
//
// # Core
//
// Step(State, Event) -> (State, []Effect) is the whole server (P3).
// Events are client messages, the end of input, the results of
// effects (ConfigLoaded, WorkspaceLoaded) and slices of background
// work (Work). They wait in a Queue ordered by virtual time, then
// Priority, then sequence number. Effects are Send, LoadConfig,
// LoadWorkspace and Exit. The core never reads a clock: every Step
// advances its virtual time by Tick, so every timing rule is a step
// bound (P7), above all the 5 s no-progress rule of #25 (NoProgress).
// A State is a value: Step never changes what an older State can see,
// so states can be kept, compared and forked.
//
// Documents and snapshots are immutable. Every change (didOpen,
// didChange with ranges in the negotiated encoding, didClose, a new
// configuration or project file set) makes a Snapshot with the next ID.
// A Snapshot holds every project file (open buffers over the files on
// disk), so analyzer queries are workspace queries and resolve across
// files. Work computed for an older snapshot is never published:
// stale diagnostics are dropped and a request whose snapshot changed
// gets ContentModified.
//
// Every response and every publishDiagnostics is followed by the
// notification $/gohard/snapshot (SnapshotMethod) carrying the ID of
// the snapshot it came from. A "$/" notification is ignored by editors,
// so this is the least intrusive LSP-compatible place for it. The only
// exception are error responses before initialize, where LSP allows no
// other notification.
//
// # Protocol rules
//
//   - Position encoding: utf-8 when the client offers it, else utf-16
//     (Negotiate), converted with internal/span.
//   - Text sync is incremental only; stale versions and changes to
//     documents that are not open are logged and ignored (P6).
//   - initialize, initialized, shutdown and exit follow LSP 3.17;
//     exit after shutdown is exit code 0, else 1.
//   - $/cancelRequest ends a running or waiting request at the next
//     slice boundary with RequestCancelled; every request gets exactly
//     one final response. Requests that arrive before the project files
//     are loaded wait for them.
//   - The project root comes from the first workspace folder (else
//     rootUri, else rootPath) through config.Load, the single root rule
//     of #28. A client that sends none gets the folder of the first
//     file it opens (#48), not counting cached bundled std/ieee files
//     (of any cache root, so a replay agrees); until then only open
//     documents are seen, and requests after that didOpen wait for
//     the project load.
//     gohard.yaml wins over the editor settings
//     (initializationOptions and didChangeConfiguration, the "gohard"
//     object or the whole settings object); each overridden key is
//     reported in a window/logMessage (config.MergeEditor).
//   - $/progress: a request's workDoneToken, or for diagnostics a token
//     the server creates with window/workDoneProgress/create once the
//     client supports it.
//
// # Analyzer
//
// Language features come from an Analyzer: pure functions of a
// Snapshot that return sliced Tasks over byte offsets. Later stages
// implement it (#32, #34, #35); Nop provides nothing, and the server
// advertises exactly the features the analyzer reports.
//
// # Shell
//
// Driver feeds events to Step and performs the effects through the
// env doors. Serve runs the queued events one at a time and, before
// each, queues every client message that has already arrived, so a
// $/cancelRequest or an edit reaches running work at its next slice
// boundary. Each recorded input carries the number of core steps run
// before it was queued (Arrival, and the session line format in
// entry.go), and Replay queues it after the same number of steps, so
// the output is a pure function of the session file and the project
// files and a recorded session replays byte for byte (#29). The
// session also records every gohard.yaml read the server performs
// (a save of gohard.yaml reloads it) and every request the transport
// skipped unread, which the core answers with InvalidRequest; the
// session folder is the one the first accepted initialize request
// names (or, when it names none, the folder of the first file opened
// after it), wherever it is in the session. A fatal framing error is
// logged on stderr before Serve returns 1. Serve is
// the stdio shell on jsonrpc2.Conn, whose
// reader and writer are the only goroutines (#4 P8). RunAction runs
// `gohard lsp <action>` as a whole in-memory session, so the CLI and
// the editor share one code path (#26).
package server

// Package trace is the native simulation trace (#36): the lossless
// record of one run that is the source of truth for goldens, value
// lookup in the language server, the race check (#29) and the viewer
// exports in the subpackages vcd and fst.
//
// # Model
//
// A Trace holds a Header (testbench, seed, end time; the seed only
// names the file and is never encoded), the VHDL Types of
// the recorded signals (the nine std_ulogic values, enumerations,
// integers, reals, physical types and one-dimensional arrays of those),
// the hierarchy Scopes (instances, blocks, generates and record
// scopes: a record signal is recorded as one signal per field below a
// record scope), the Signals by hierarchical path (tb.dut.counter_q,
// the path diagnostics use), the Processes that drive them, every value
// Change with its time, delta, driving process and modeled mark, and
// the assert, report and cover Events with their source line.
// Process-local variables are not recorded. Time is in femtoseconds.
//
// The simulation kernel (#35) feeds a run to a Writer; the Recorder is
// the Writer that builds a Trace. It applies simulation.dump_scope
// (InScope), sorts the changes of every delta by signal path, so a
// race-free design gives the same bytes for every seed, and marks the
// values of stand-in model processes as modeled. A trace contains no
// wall time, host name or other run-dependent field: the same design
// and seed always give the same bytes (P1).
//
// # Canonical text form
//
// Encode writes, and Decode reads back, the canonical text form that
// trace files (.gohard/traces/<tb>-<seed>.trace, see FileName) and
// corpus goldens store. It is UTF-8 text, one record per line, every
// line ending in "\n", tokens separated by exactly one space. Decode
// accepts only canonical input, so Encode(Decode(b)) == b.
//
//	trace    = magic header {type} {scope} {signal} {process} {entry}
//	magic    = "gohard-trace 1" NL
//	header   = "testbench" SP tok NL "end" SP uint NL
//	type     = "type" SP name SP typedef NL
//	typedef  = "logic"
//	         | "enum" {SP literal}               (position order)
//	         | "integer" SP int SP int           (low high)
//	         | "real"
//	         | "physical" SP int SP int {SP unit SP int}
//	                                             (low high, units with
//	                                              multipliers, base 1)
//	         | "array" SP name                   (scalar element type)
//	scope    = "scope" SP path SP scopekind [SP tok] NL
//	scopekind= "instance" | "block" | "for-generate" | "if-generate"
//	         | "case-generate" | "record" | "package"
//	signal   = "signal" SP path SP name SP mode [SP int SP dir SP int] NL
//	mode     = "signal" | "in" | "out" | "inout" | "buffer" | "linkage"
//	dir      = "to" | "downto"                   (array signals only)
//	process  = "process" SP id SP path [SP "modeled"] NL
//	entry    = change | event
//	change   = uint SP uint SP path SP value SP proc [SP "modeled"] NL
//	event    = uint SP uint SP "!" SP evkind SP severity SP loc SP proc
//	           SP quoted NL
//	evkind   = "assert" | "report" | "cover-hit" | "cover-miss"
//	severity = "note" | "warning" | "error" | "failure" | "-"
//	loc      = tok                               (FILE:LINE)
//	proc     = "-" | "p" id                      (driving process)
//	value    = logic                             (one of UX01ZWLH-)
//	         | literal                           (enum)
//	         | int                               (integer; physical in
//	                                              base units)
//	         | real                              (Go 'g' shortest form,
//	                                              NaN, +Inf, -Inf)
//	         | tok                               (array of logic: one
//	                                              character per element,
//	                                              left to right)
//	         | "[" {SP value} SP "]"             (any other array)
//	tok      = a run of printable ASCII other than space that does not
//	           start with '"', or else a Go-quoted string
//	quoted   = a Go-quoted string (strconv.Quote)
//	name, path, literal, unit = tok
//
// Order is canonical: types by name; scopes, signals and processes by
// path (byte order); process ids count from 1 in that order. Entries
// are ordered by time, then delta; within one (time, delta) the changes
// come first, sorted by signal path, then the events, ordered by
// process id and, within one process, in emission order. Each signal
// changes at most once per delta and never to the value it already has;
// a change by a modeled process is marked modeled. Time and delta of a
// change are the cycle in which the signal took the value; the changes
// at 0 0 are the initial values.
//
// # Exports
//
// An Exporter (vcd.Exporter, fst.Exporter) converts a Trace into a
// viewer format. Exports keep only the value each signal settles on at
// each time (Settle), so deltas and zero-width glitches do not appear.
package trace

// Package format is GoHard's VHDL formatter (#32). It formats a parsed
// file from its CST and backs `gohard fmt`, `gohard fmt --check` and the
// language server's whole-file and range formatting, all through the
// same functions (Format, Range).
//
// # Model
//
// The formatter never rewrites text: it cuts the file into atoms
// (tokens, comments, other non-blank trivia, and verbatim chunks) and
// only chooses the whitespace between them and the case of reserved
// words. The atoms of the output are the atoms of the input in the same
// order, so literals, extended identifiers, comment contents and
// identifiers are byte-for-byte unchanged by construction, and the
// edits of a run are exactly the changed gaps and keywords.
//
// Verbatim chunks are whole source lines copied unchanged: inactive
// conditional-analysis branches, directive lines, protected data, and
// error regions (ErrorNode, Error and Unparsed tokens).
//
// # Layout
//
// Line breaks are kept as written (only blank-line runs change). Each
// line gets an indentation level from the tree: a construct that starts
// a line inside the body of another one is one level deeper (an end
// clause, elsif, else and generate branch stay at their construct's
// level); a line that continues a construct is one level deeper than the
// construct's first line; inside parentheses a line is one level deeper
// than the line that opened them, and a line that starts with `)` is at
// that line's level. Comment-only lines take the level of the next code
// line, one deeper when that line closes a region (end, begin, elsif,
// else, `)`).
//
// Alignment (align.go) and wrapping (wrap.go) are separate passes over
// the same model; see the hooks in format.go.
package format

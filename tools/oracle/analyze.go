package main

import (
	"regexp"
	"strconv"
	"strings"
)

// toolDiag is one error a tool reported. Line is 0 when the tool gave
// no position; Col is 0 when it gave only a line.
type toolDiag struct {
	File string
	Line int
	Col  int
	Msg  string
}

var (
	ghdlErr     = regexp.MustCompile(`^(\S+?):(\d+):(\d+):\s*error:\s*(.*)$`)
	ghdlBareErr = regexp.MustCompile(`^ghdl[^:\s]*:error:\s*(.*)$`)
	nvcErr      = regexp.MustCompile(`^\*\* (?:Error|Fatal):\s*(.*)$`)
	nvcAt       = regexp.MustCompile(`^\s+> (\S+?):(\d+)(?::\d+)?\s*$`)
	nvcHeader   = regexp.MustCompile(`^\*\* [A-Z][a-z]+:`)
	// posRef is a file position inside a message text.
	posRef = regexp.MustCompile(`(\S+\.vhdl?):\d+(?::\d+)?`)
)

// parseGHDL returns the errors of `ghdl -a file` output.
func parseGHDL(out, file string) []toolDiag {
	var ds []toolDiag
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := ghdlErr.FindStringSubmatch(line); m != nil {
			l, _ := strconv.Atoi(m[2])
			c, _ := strconv.Atoi(m[3])
			ds = append(ds, toolDiag{File: m[1], Line: l, Col: c,
				Msg: m[4]})
		} else if m := ghdlBareErr.FindStringSubmatch(line); m != nil {
			ds = append(ds, toolDiag{File: file, Msg: m[1]})
		}
	}
	return ds
}

// parseNVC returns the errors of `nvc -a file` output: a "** Error:"
// or "** Fatal:" header and the first "> file:line" below it.
func parseNVC(out, file string) []toolDiag {
	var ds []toolDiag
	open := false // the last header was an error without a position
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := nvcErr.FindStringSubmatch(line); m != nil {
			ds = append(ds, toolDiag{File: file, Msg: m[1]})
			open = true
			continue
		}
		if nvcHeader.MatchString(line) {
			open = false
			continue
		}
		if m := nvcAt.FindStringSubmatch(line); m != nil && open {
			l, _ := strconv.Atoi(m[2])
			ds[len(ds)-1].File = m[1]
			ds[len(ds)-1].Line = l
			open = false
		}
	}
	return ds
}

// msgKey is the comparison form of a message: positions inside it
// dropped (they move when lines move) and letters folded (keyword case
// may change).
func msgKey(msg string) string {
	return strings.ToLower(posRef.ReplaceAllString(msg, "$1"))
}

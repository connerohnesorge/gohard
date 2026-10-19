package format

import (
	"bytes"
	"strings"
)

// render writes the doc and returns the text and the edits from the
// source to it.
func (d *doc) render() ([]byte, []Edit) {
	var out []byte
	pos := make([]int, 2*len(d.atoms)) // output start and end per atom
	for li, ln := range d.lines {
		for range ln.blank {
			out = append(out, d.eol...)
		}
		if !d.atoms[ln.first].isChunk() {
			out = append(out, strings.Repeat(" ",
				max(ln.level, 0)*d.opts.Indent)...)
		}
		for i := ln.first; i < ln.last; i++ {
			if i > ln.first {
				out = append(out, d.gap[i]...)
			}
			pos[2*i] = len(out)
			out = append(out, d.atoms[i].text...)
			pos[2*i+1] = len(out)
		}
		if li == len(d.lines)-1 {
			out = append(out, d.eol...) // the file's final newline
		} else {
			out = append(out, d.lineEnd(ln)...)
		}
	}
	return out, d.edits(out, pos)
}

// lineEnd returns the line ending written after line ln: the dominant
// one, except after a verbatim chunk (directive lines, inactive
// branches), whose own line ending is part of the text kept byte for
// byte. The last line gets the file's final newline instead (render),
// and a lone CR the dominant ending: a CR followed by the LF of a blank
// line would read as one CR LF line end.
func (d *doc) lineEnd(ln line) string {
	a := &d.atoms[ln.last-1]
	if !a.isChunk() || a.end >= len(d.src) {
		return d.eol
	}
	switch {
	case d.src[a.end] == '\r' && a.end+1 < len(d.src) &&
		d.src[a.end+1] == '\n':
		return "\r\n"
	case d.src[a.end] == '\n':
		return "\n"
	}
	return d.eol
}

// edits compares the source with out atom by atom: changed gaps and
// changed keywords become edits.
func (d *doc) edits(out []byte, pos []int) []Edit {
	var es []Edit
	prevSrc, prevOut := 0, 0
	for i := range d.atoms {
		a := &d.atoms[i]
		es = gapEdits(es, prevSrc, d.src[prevSrc:a.start],
			out[prevOut:pos[2*i]])
		end := a.end
		if a.isChunk() {
			// A chunk's text is its source text, at the end of the
			// file without the final blank lines and line ending
			// (trimFileEnd): those compare with the final gap.
			end = a.start + len(a.text)
		} else {
			es = appendTrimmed(es, a.start, d.src[a.start:a.end],
				[]byte(a.text))
		}
		prevSrc, prevOut = end, pos[2*i+1]
	}
	return gapEdits(es, prevSrc, d.src[prevSrc:], out[prevOut:])
}

// gapEdits appends the edits that turn the whitespace old (at offset
// off) into new. The first and last lines of a gap are compared on
// their own, so an edit never spans a line break that both texts keep:
// range formatting then can fix one line's indentation alone.
func gapEdits(es []Edit, off int, old, new []byte) []Edit {
	if bytes.Equal(old, new) {
		return es
	}
	oi := bytes.IndexByte(old, '\n')
	ni := bytes.IndexByte(new, '\n')
	if oi < 0 || ni < 0 {
		return appendTrimmed(es, off, old, new)
	}
	oj := bytes.LastIndexByte(old, '\n')
	nj := bytes.LastIndexByte(new, '\n')
	// Head: up to and including the first line end.
	es = appendTrimmed(es, off, old[:oi+1], new[:ni+1])
	if bytes.Count(old, []byte{'\n'}) == bytes.Count(new, []byte{'\n'}) {
		// Same lines: pair them one by one.
		ol := bytes.SplitAfter(old[oi+1:oj+1], []byte{'\n'})
		nl := bytes.SplitAfter(new[ni+1:nj+1], []byte{'\n'})
		o := off + oi + 1
		for k := range ol {
			if k < len(nl) {
				es = appendTrimmed(es, o, ol[k], nl[k])
			}
			o += len(ol[k])
		}
	} else {
		es = appendTrimmed(es, off+oi+1, old[oi+1:oj+1],
			new[ni+1:nj+1])
	}
	// Tail: the indentation of the last line.
	return appendTrimmed(es, off+oj+1, old[oj+1:], new[nj+1:])
}

// appendTrimmed appends one edit for old -> new at off, without their
// common prefix and suffix.
func appendTrimmed(es []Edit, off int, old, new []byte) []Edit {
	p := 0
	for p < len(old) && p < len(new) && old[p] == new[p] {
		p++
	}
	s := 0
	for s < len(old)-p && s < len(new)-p &&
		old[len(old)-1-s] == new[len(new)-1-s] {
		s++
	}
	if p == len(old) && p == len(new) {
		return es
	}
	return append(es, Edit{Start: off + p, End: off + len(old) - s,
		NewText: string(new[p : len(new)-s])})
}

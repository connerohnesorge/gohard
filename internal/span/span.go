// Package span holds source locations: file IDs, half-open byte spans and
// the conversion between byte offsets and line/column positions in UTF-8,
// UTF-16 and UTF-32 code units.
//
// Byte offsets are the only source reference stored by other packages
// (#10, #35); lines and columns are derived on demand through an Index.
// Every function here is pure and never panics on any input, including
// invalid UTF-8 and out-of-range offsets.
package span

import "unicode/utf8"

// FileID names one source file inside a FileSet. The zero value NoFile
// means "no file" (for example a command-line diagnostic).
type FileID uint32

// NoFile is the FileID of a location that is not inside any file.
const NoFile FileID = 0

// Span is a half-open byte range [Start, End) inside one file.
type Span struct {
	File  FileID
	Start int
	End   int
}

// Len returns the number of bytes covered by s, or 0 if s is inverted.
func (s Span) Len() int {
	if s.End < s.Start {
		return 0
	}
	return s.End - s.Start
}

// Contains reports whether off lies inside s. An empty span contains its
// own start offset, so a cursor at an insertion point hits it.
func (s Span) Contains(off int) bool {
	if s.Start == s.End {
		return off == s.Start
	}
	return s.Start <= off && off < s.End
}

// Cover returns the smallest span containing both s and t. The file of s
// is kept.
func (s Span) Cover(t Span) Span {
	out := s
	out.Start = min(s.Start, t.Start)
	out.End = max(s.End, t.End)
	return out
}

// FileSet assigns FileIDs to paths in the order they are added. Callers
// add paths in canonical (sorted) order so IDs are deterministic. IDs
// start at 1.
type FileSet struct {
	paths []string
	ids   map[string]FileID
}

// Add returns the FileID of path, assigning the next ID if path is new.
func (f *FileSet) Add(path string) FileID {
	if id, ok := f.ids[path]; ok {
		return id
	}
	if f.ids == nil {
		f.ids = make(map[string]FileID)
	}
	f.paths = append(f.paths, path)
	id := FileID(len(f.paths))
	f.ids[path] = id
	return id
}

// Lookup returns the FileID of path if it was added.
func (f *FileSet) Lookup(path string) (FileID, bool) {
	id, ok := f.ids[path]
	return id, ok
}

// Path returns the path of id, or "" for NoFile and unknown IDs.
func (f *FileSet) Path(id FileID) string {
	if id == NoFile || int(id) > len(f.paths) {
		return ""
	}
	return f.paths[id-1]
}

// Len returns the number of files in the set.
func (f *FileSet) Len() int { return len(f.paths) }

// Encoding is the code unit used to count columns.
type Encoding uint8

// Column encodings. UTF8 counts bytes, UTF16 counts UTF-16 code units (a
// rune above U+FFFF counts 2) and UTF32 counts runes. An invalid UTF-8
// byte counts as one rune (U+FFFD) in every encoding.
const (
	UTF8 Encoding = iota + 1
	UTF16
	UTF32
)

// String returns the LSP name of the encoding ("utf-8", "utf-16",
// "utf-32").
func (e Encoding) String() string {
	switch e {
	case UTF8:
		return "utf-8"
	case UTF16:
		return "utf-16"
	case UTF32:
		return "utf-32"
	}
	return "unknown"
}

// Position is a zero-based line and a zero-based column counted in the
// code units of some Encoding, as in LSP.
type Position struct {
	Line int
	Col  int
}

// Index maps byte offsets of one immutable text to positions. Lines end
// at "\n", "\r\n" or "\r" (the LSP rule). The text must not be modified
// after NewIndex.
type Index struct {
	src   []byte
	lines []int // lines[i] is the byte offset where line i starts
}

// NewIndex builds the line index of src in one linear pass.
func NewIndex(src []byte) *Index {
	lines := []int{0}
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\n':
			lines = append(lines, i+1)
		case '\r':
			if i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			lines = append(lines, i+1)
		}
	}
	return &Index{src: src, lines: lines}
}

// Len returns the length of the text in bytes.
func (x *Index) Len() int { return len(x.src) }

// LineCount returns the number of lines. Text ending in a line
// terminator has a final empty line, and empty text has one line.
func (x *Index) LineCount() int { return len(x.lines) }

// LineStart returns the offset of the first byte of line (clamped to the
// valid line range).
func (x *Index) LineStart(line int) int {
	return x.lines[x.clampLine(line)]
}

// LineEnd returns the offset just before the terminator of line (clamped
// to the valid line range).
func (x *Index) LineEnd(line int) int {
	line = x.clampLine(line)
	if line+1 == len(x.lines) {
		return len(x.src)
	}
	end := x.lines[line+1]
	if end > 0 && x.src[end-1] == '\n' {
		end--
	}
	if end > x.lines[line] && x.src[end-1] == '\r' {
		end--
	}
	return end
}

func (x *Index) clampLine(line int) int {
	return max(0, min(line, len(x.lines)-1))
}

// Line returns the zero-based line containing off (clamped to [0, Len]).
func (x *Index) Line(off int) int {
	off = max(0, min(off, len(x.src)))
	lo, hi := 0, len(x.lines)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if x.lines[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// Position converts a byte offset to a position in enc. The offset is
// clamped to [0, Len]; an offset inside a multi-byte rune is moved back
// to the start of that rune, and an offset inside a line terminator is
// moved back to the end of the line.
func (x *Index) Position(off int, enc Encoding) Position {
	line := x.Line(off)
	start, end := x.lines[line], x.LineEnd(line)
	off = max(start, min(off, end))
	col := 0
	for i := start; i < off; {
		r, size := utf8.DecodeRune(x.src[i:end])
		if i+size > off {
			break // off is inside this rune: snap back
		}
		col += units(r, size, enc)
		i += size
	}
	return Position{Line: line, Col: col}
}

// Offset converts a position in enc back to a byte offset. A column past
// the end of the line is clamped to the line end, and a column inside a
// rune (such as between the two halves of a UTF-16 surrogate pair) is
// moved back to the start of that rune. ok is false when the line or the
// column is negative or the line does not exist.
func (x *Index) Offset(p Position, enc Encoding) (off int, ok bool) {
	if p.Line < 0 || p.Line >= len(x.lines) || p.Col < 0 {
		return 0, false
	}
	start, end := x.lines[p.Line], x.LineEnd(p.Line)
	col, i := 0, start
	for i < end {
		r, size := utf8.DecodeRune(x.src[i:end])
		n := units(r, size, enc)
		if col+n > p.Col {
			break
		}
		col += n
		i += size
	}
	return i, true
}

// LineCol returns the one-based line and one-based UTF-8 column of off,
// the form used by diagnostics and `gohard lsp --at` (#20, #26).
func (x *Index) LineCol(off int) (line, col int) {
	p := x.Position(off, UTF8)
	return p.Line + 1, p.Col + 1
}

// OffsetOf is the inverse of LineCol: it converts a one-based line and
// one-based UTF-8 column to a byte offset, with the clamping rules of
// Offset.
func (x *Index) OffsetOf(line, col int) (int, bool) {
	return x.Offset(Position{Line: line - 1, Col: col - 1}, UTF8)
}

// Convert re-expresses a column of line from one encoding to another.
func (x *Index) Convert(p Position, from, to Encoding) (Position, bool) {
	off, ok := x.Offset(p, from)
	if !ok {
		return Position{}, false
	}
	return x.Position(off, to), true
}

// units returns the number of code units the decoded rune r (size bytes
// in the source) occupies in enc.
func units(r rune, size int, enc Encoding) int {
	switch enc {
	case UTF16:
		if r >= 0x10000 {
			return 2
		}
		return 1
	case UTF32:
		return 1
	default:
		return size
	}
}

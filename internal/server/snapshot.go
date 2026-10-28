package server

import (
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/connerohnesorge/gohard/v2/internal/config"
	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/span"
)

// Document is one immutable text: an open editor buffer or a project
// file read from disk. A new edit makes a new Document; old ones stay
// valid for every snapshot that holds them.
type Document struct {
	// URI is the document's URI as the client spells it (open
	// documents) or as the server builds it from the path (disk files).
	URI string
	// Path is the OS path for file URIs, else "".
	Path string
	// Rel is Path relative to the project root with '/' separators
	// (with a ../ prefix outside the root), the path `gohard check`
	// prints; "" without a root or a path.
	Rel string
	// Version is the client's version (0 for disk files).
	Version int32
	// Open reports whether the client owns the text (didOpen).
	Open       bool
	LanguageID string

	key   string
	text  []byte
	index *span.Index
}

// Text returns the document's bytes. Callers must not modify them.
func (d *Document) Text() []byte { return d.text }

// Index returns the line index of the text.
func (d *Document) Index() *span.Index { return d.index }

func newDocument(uri string, root string, text []byte) *Document {
	d := &Document{URI: uri, text: text, index: span.NewIndex(text)}
	d.Path, _ = URIToPath(uri)
	d.key = docKey(uri)
	if d.Path != "" && root != "" {
		if rel, err := filepath.Rel(root, d.Path); err == nil {
			d.Rel = filepath.ToSlash(rel)
		}
	}
	return d
}

// with returns a copy of d with new text.
func (d *Document) with(text []byte, version int32) *Document {
	n := *d
	n.text = text
	n.index = span.NewIndex(text)
	n.Version = version
	return &n
}

// Snapshot is an immutable view of the whole workspace at one moment:
// the project root, the merged configuration and every document (open
// buffers over the files on disk). IDs count up from 1 in the order
// snapshots are made; a result computed for an older snapshot is never
// published (P3).
type Snapshot struct {
	ID     uint64
	Root   string
	Config config.Config
	// Discovery are the file discovery diagnostics of the project
	// (workspace/file-too-large, workspace/unreadable, ...), which
	// `gohard check` reports too.
	Discovery []diag.Diagnostic

	docs  []*Document // sorted by URI
	byKey map[string]int
	// config is the gohard.yaml of the root as read from disk (empty
	// text when there is none); see ConfigDocument.
	config        *Document
	configPresent bool
}

// ConfigDocument returns gohard.yaml at the project root: the open
// buffer when the client has it open, else the text the server read
// (empty when the file does not exist; present reports whether it
// does or is open). Quick fixes edit it (#34); it is not a project
// file, so Document and Documents do not list the disk copy.
func (s *Snapshot) ConfigDocument() (d *Document, present bool) {
	if s == nil || s.config == nil {
		return nil, false
	}
	if d, ok := s.Document(s.config.URI); ok {
		return d, true
	}
	return s.config, s.configPresent
}

// lookup is Document with the disk gohard.yaml as a fallback, for
// converting edits of a quick fix.
func (s *Snapshot) lookup(uri string) (*Document, bool) {
	if d, ok := s.Document(uri); ok {
		return d, true
	}
	if s != nil && s.config != nil && docKey(uri) == s.config.key {
		return s.config, true
	}
	return nil, false
}

// Document returns the document for uri. An open buffer wins over the
// file on disk with the same path.
func (s *Snapshot) Document(uri string) (*Document, bool) {
	if s == nil {
		return nil, false
	}
	i, ok := s.byKey[docKey(uri)]
	if !ok {
		return nil, false
	}
	return s.docs[i], true
}

// Documents returns every document sorted by URI. Callers must not
// modify the slice.
func (s *Snapshot) Documents() []*Document {
	if s == nil {
		return nil
	}
	return s.docs
}

// newSnapshot merges open and disk documents into a snapshot.
func newSnapshot(id uint64, root string, cfg config.Config,
	open map[string]*Document, disk []*Document,
) *Snapshot {
	merged := make(map[string]*Document, len(open)+len(disk))
	for _, d := range disk {
		merged[d.key] = d
	}
	for k, d := range open {
		merged[k] = d
	}
	docs := make([]*Document, 0, len(merged))
	for _, k := range slices.Sorted(maps.Keys(merged)) {
		docs = append(docs, merged[k])
	}
	slices.SortFunc(docs, func(a, b *Document) int {
		return strings.Compare(a.URI, b.URI)
	})
	s := &Snapshot{ID: id, Root: root, Config: cfg, docs: docs,
		byKey: make(map[string]int, len(docs))}
	for i, d := range docs {
		s.byKey[d.key] = i
	}
	return s
}

// docKey identifies a document across URI spellings: the cleaned path
// for file URIs, else the URI itself.
func docKey(uri string) string {
	if p, ok := URIToPath(uri); ok {
		return "file:" + p
	}
	return uri
}

// URIToPath converts a file URI to an OS path. ok is false for other
// schemes and malformed URIs. file:///C:/x and file:///c%3A/x both give
// C:\x on Windows-style paths; file:///work/a.vhd gives /work/a.vhd.
func URIToPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Opaque != "" {
		return "", false
	}
	p := u.Path
	if p == "" {
		return "", false
	}
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' && isDriveLetter(p[1]) {
		p = strings.ToUpper(p[1:2]) + p[2:] // /c:/x -> C:/x
	}
	if u.Host != "" && u.Host != "localhost" {
		p = "//" + u.Host + p // UNC share
	}
	return filepath.Clean(filepath.FromSlash(p)), true
}

// PathToURI converts an absolute OS path to a file URI.
func PathToURI(p string) string {
	p = filepath.ToSlash(p)
	if len(p) >= 2 && p[1] == ':' && isDriveLetter(p[0]) {
		p = "/" + strings.ToUpper(p[:1]) + p[1:]
	}
	u := url.URL{Scheme: "file", Path: p}
	if strings.HasPrefix(p, "//") { // UNC share
		host, rest, _ := strings.Cut(p[2:], "/")
		u = url.URL{Scheme: "file", Host: host, Path: "/" + rest}
	}
	return u.String()
}

func isDriveLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// Range is an LSP range in the session's position encoding.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Position is an LSP position: zero-based line and character in the
// session's position encoding.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// offset converts p to a byte offset with the LSP clamping rules: a
// character past the end of its line means the end of the line, and a
// line past the last line means the end of the text.
func offset(x *span.Index, p Position, enc span.Encoding) int {
	if p.Line < 0 {
		return 0
	}
	if p.Line >= x.LineCount() {
		return x.Len()
	}
	off, _ := x.Offset(span.Position{Line: p.Line,
		Col: max(p.Character, 0)}, enc)
	return off
}

// position converts a byte offset to an LSP position.
func position(x *span.Index, off int, enc span.Encoding) Position {
	p := x.Position(off, enc)
	return Position{Line: p.Line, Character: p.Col}
}

func toRange(x *span.Index, s Span, enc span.Encoding) Range {
	return Range{Start: position(x, s.Start, enc),
		End: position(x, max(s.End, s.Start), enc)}
}

func toSpan(x *span.Index, r Range, enc span.Encoding) Span {
	a, b := offset(x, r.Start, enc), offset(x, r.End, enc)
	if b < a {
		a, b = b, a
	}
	return Span{Start: a, End: b}
}

// applyChange replaces the bytes of r in text with repl and returns the
// new text; text is not modified.
func applyChange(text []byte, x *span.Index, r Range, enc span.Encoding,
	repl string,
) []byte {
	s := toSpan(x, r, enc)
	out := make([]byte, 0, len(text)-(s.End-s.Start)+len(repl))
	out = append(out, text[:s.Start]...)
	out = append(out, repl...)
	return append(out, text[s.End:]...)
}

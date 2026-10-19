package server

import (
	"encoding/json"
	"path/filepath"
	"strconv"

	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/span"
)

// Text synchronization is incremental only (#25): didChange carries
// ranges in the negotiated position encoding, applied in order. A
// change without a range replaces the whole text, as LSP allows. A
// change for a document that is not open, or whose version does not
// increase, is a stale edit (P6): it is logged and ignored.

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int32  `json:"version"`
	Text       string `json:"text"`
}

type versionedID struct {
	URI     string `json:"uri"`
	Version int32  `json:"version"`
}

type contentChange struct {
	Range *Range `json:"range"`
	Text  string `json:"text"`
}

func (s *State) didOpen(o *out, params json.RawMessage) {
	var p struct {
		TextDocument textDocumentItem `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) != nil || p.TextDocument.URI == "" {
		s.log(o, LogWarning, "didOpen: invalid params")
		return
	}
	t := p.TextDocument
	d := newDocument(t.URI, s.root, []byte(t.Text))
	d.Version, d.Open, d.LanguageID = t.Version, true, t.LanguageID
	s.open = cloneSet(s.open, d.key, d)
	s.edits++
	s.newSnapshot(o)
}

func (s *State) didChange(o *out, params json.RawMessage) {
	var p struct {
		TextDocument   versionedID     `json:"textDocument"`
		ContentChanges []contentChange `json:"contentChanges"`
	}
	if json.Unmarshal(params, &p) != nil {
		s.log(o, LogWarning, "didChange: invalid params")
		return
	}
	key := docKey(p.TextDocument.URI)
	d, ok := s.open[key]
	switch {
	case !ok:
		s.log(o, LogWarning, "didChange: "+p.TextDocument.URI+
			" is not open; change ignored")
		return
	case p.TextDocument.Version <= d.Version:
		s.log(o, LogWarning, "didChange: stale version "+
			strconv.Itoa(int(p.TextDocument.Version))+" of "+
			p.TextDocument.URI+" (have "+strconv.Itoa(int(d.Version))+
			"); change ignored")
		return
	}
	text, x := d.text, d.index
	for i, c := range p.ContentChanges {
		if c.Range == nil {
			text = []byte(c.Text)
		} else {
			text = applyChange(text, x, *c.Range, s.enc, c.Text)
		}
		if i+1 < len(p.ContentChanges) {
			x = span.NewIndex(text)
		}
	}
	s.open = cloneSet(s.open, key, d.with(text, p.TextDocument.Version))
	s.edits++
	s.newSnapshot(o)
}

func (s *State) didClose(o *out, params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	key := docKey(p.TextDocument.URI)
	if _, ok := s.open[key]; !ok {
		return
	}
	s.open = cloneDelete(s.open, key)
	s.edits++
	s.newSnapshot(o)
}

// didSave of gohard.yaml reloads the configuration; other saves change
// nothing yet (on-save checks arrive with #34 and #35).
func (s *State) didSave(o *out, params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if json.Unmarshal(params, &p) != nil || s.root == "" {
		return
	}
	path, ok := URIToPath(p.TextDocument.URI)
	if ok && path == filepath.Join(s.root, config.FileName) {
		o.do(LoadConfig{Folder: s.folder})
	}
}

package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
)

// JSON value kinds, as a bit mask.
const (
	kNull uint8 = 1 << iota
	kBool
	kNumber
	kString
	kArray
	kObject

	kAny = kNull | kBool | kNumber | kString | kArray | kObject
)

// alt is one alternative of an or type.
type alt struct {
	kinds uint8  // JSON kinds it accepts
	shape *shape // for objects: the keys of the structure
	elem  *alt   // for arrays: the element alternative
	sub   []alt  // for a nested or type: its alternatives
	// decode reads the JSON into a value of the alternative's type.
	decode func([]byte) (any, error)
}

// shape is the key set of a structure. fixed lists key, JSON value
// pairs of string-literal properties (a "kind": "create" tag): an
// object whose key holds another value is not this structure.
type shape struct {
	req, known, fixed []string
}

func as[T any](b []byte) (any, error) {
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func marshalOr(v any) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	return json.Marshal(v)
}

// unmarshalOr decodes b into the best matching alternative; null is
// nil.
func unmarshalOr(b []byte, alts []alt, name string) (any, error) {
	b = bytes.TrimSpace(b)
	if kindOf(b) == kNull {
		return nil, nil
	}
	best, score := -1, -1
	for i := range alts {
		if s, ok := alts[i].match(b); ok && s > score {
			best, score = i, s
		}
	}
	if best < 0 {
		return nil, errors.New("protocol: JSON matches no alternative " +
			"of " + name)
	}
	return alts[best].decode(b)
}

// match reports whether b fits a and how well: the number of the
// object's keys the alternative knows.
func (a *alt) match(b []byte) (int, bool) {
	k := kindOf(b)
	if a.kinds&k == 0 {
		return 0, false
	}
	switch {
	case a.sub != nil:
		best := -1
		for i := range a.sub {
			if s, ok := a.sub[i].match(b); ok && s > best {
				best = s
			}
		}
		return best, best >= 0
	case k == kObject && a.shape != nil:
		return a.shape.score(b)
	case k == kArray && a.elem != nil:
		var elems []json.RawMessage
		if json.Unmarshal(b, &elems) != nil {
			return 0, false
		}
		if len(elems) == 0 {
			return 0, true
		}
		return a.elem.match(bytes.TrimSpace(elems[0]))
	}
	return 0, true
}

func (s *shape) score(b []byte) (int, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(b, &obj) != nil {
		return 0, false
	}
	for _, k := range s.req {
		if _, ok := obj[k]; !ok {
			return 0, false
		}
	}
	for i := 0; i+1 < len(s.fixed); i += 2 {
		v, ok := obj[s.fixed[i]]
		if ok && !jsonEqual(v, s.fixed[i+1]) {
			return 0, false
		}
	}
	n := 0
	for _, k := range s.known {
		if _, ok := obj[k]; ok {
			n++
		}
	}
	return n, true
}

// kindOf classifies trimmed JSON by its first byte; 0 for empty input.
func kindOf(b []byte) uint8 {
	if len(b) == 0 {
		return 0
	}
	switch c := b[0]; {
	case c == 'n':
		return kNull
	case c == 't' || c == 'f':
		return kBool
	case c == '"':
		return kString
	case c == '[':
		return kArray
	case c == '{':
		return kObject
	case c == '-' || (c >= '0' && c <= '9'):
		return kNumber
	}
	return 0
}

// jsonEqual compares a raw JSON string value with a compact JSON
// string, ignoring escapes ("cr\u0065ate" equals "create").
func jsonEqual(raw json.RawMessage, want string) bool {
	var a, b string
	if json.Unmarshal(raw, &a) != nil ||
		json.Unmarshal([]byte(want), &b) != nil {
		return false
	}
	return a == b
}

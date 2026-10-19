package jsonrpc2

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
)

// ID is a request ID: an integer or a string (JSON-RPC 2.0). The zero
// value is the null ID, which only appears in error responses to a
// message whose ID could not be read.
type ID struct {
	kind idKind
	num  int64
	str  string
}

type idKind uint8

const (
	idNull idKind = iota
	idNum
	idStr
)

// IntID returns the integer ID n.
func IntID(n int64) ID { return ID{kind: idNum, num: n} }

// StringID returns the string ID s.
func StringID(s string) ID { return ID{kind: idStr, str: s} }

// IsNull reports whether id is the null ID.
func (id ID) IsNull() bool { return id.kind == idNull }

// Int returns the integer value of an integer ID.
func (id ID) Int() (int64, bool) { return id.num, id.kind == idNum }

// Str returns the value of a string ID.
func (id ID) Str() (string, bool) { return id.str, id.kind == idStr }

// String renders id for logs: digits for an integer, a quoted string
// for a string and null for the null ID, so the forms never collide.
func (id ID) String() string {
	switch id.kind {
	case idNum:
		return strconv.FormatInt(id.num, 10)
	case idStr:
		return strconv.Quote(id.str)
	}
	return "null"
}

// MarshalJSON implements json.Marshaler.
func (id ID) MarshalJSON() ([]byte, error) {
	return id.appendJSON(nil), nil
}

func (id ID) appendJSON(dst []byte) []byte {
	switch id.kind {
	case idNum:
		return strconv.AppendInt(dst, id.num, 10)
	case idStr:
		return appendString(dst, id.str)
	}
	return append(dst, "null"...)
}

var errBadID = errors.New("id must be an integer, a string or null")

// UnmarshalJSON implements json.Unmarshaler. It accepts an integer
// that fits in int64 (no fraction or exponent), a string or null.
func (id *ID) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0:
		return errBadID
	case string(b) == "null":
		*id = ID{}
		return nil
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return errBadID
		}
		*id = StringID(s)
		return nil
	}
	if !isInteger(b) {
		return errBadID
	}
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return errBadID
	}
	*id = IntID(n)
	return nil
}

// isInteger reports whether b is -?[0-9]+.
func isInteger(b []byte) bool {
	if len(b) > 0 && b[0] == '-' {
		b = b[1:]
	}
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// appendString appends s as a JSON string. Invalid UTF-8 becomes
// U+FFFD, as encoding/json does.
func appendString(dst []byte, s string) []byte {
	b, err := json.Marshal(s)
	if err != nil { // unreachable: strings always marshal
		return append(dst, `""`...)
	}
	return append(dst, b...)
}

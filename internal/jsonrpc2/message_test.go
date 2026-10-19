package jsonrpc2

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestIDJSON(t *testing.T) {
	good := map[string]ID{
		"0": IntID(0), "-7": IntID(-7), `"x"`: StringID("x"),
		`""`: StringID(""), "null": {}, " 12 ": IntID(12),
		"9223372036854775807":  IntID(1<<63 - 1),
		"-9223372036854775808": IntID(-1 << 63),
	}
	for in, want := range good {
		var id ID
		if err := json.Unmarshal([]byte(in), &id); err != nil ||
			id != want {
			t.Errorf("%s: got %v, %v; want %v", in, id, err, want)
		}
	}
	for _, in := range []string{"1.0", "1e3", "true", "{}", "[]", "-",
		"9223372036854775808", `"\x"`, ""} {
		var id ID
		if err := id.UnmarshalJSON([]byte(in)); err == nil {
			t.Errorf("%q: accepted as %v", in, id)
		}
	}
	// String never confuses the forms.
	if IntID(1).String() == StringID("1").String() ||
		StringID("null").String() == (ID{}).String() {
		t.Error("ID.String collides")
	}
}

// TestRoundTrip: Decode(Encode(m)) == m and encoding is a fixed point,
// for many generated messages (edge values first).
func TestRoundTrip(t *testing.T) {
	for seed := range uint64(3000) {
		g := newGen(seed)
		m := g.message()
		b, err := Encode(m)
		if err != nil {
			t.Fatalf("seed %d: encode %#v: %v", seed, m, err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("seed %d: decode %s: %v", seed, b, err)
		}
		if !reflect.DeepEqual(norm(got), norm(m)) {
			t.Fatalf("seed %d: round trip\n got %#v\nwant %#v\n%s",
				seed, got, m, b)
		}
		again, _ := Encode(got)
		if !bytes.Equal(again, b) {
			t.Fatalf("seed %d: not a fixed point:\n%s\n%s", seed, b, again)
		}
	}
}

// norm replaces empty raw messages by nil so DeepEqual compares
// meaning.
func norm(m Message) Message {
	z := func(r json.RawMessage) json.RawMessage {
		if len(r) == 0 {
			return nil
		}
		return r
	}
	switch m := m.(type) {
	case *Request:
		c := *m
		c.Params = z(c.Params)
		return &c
	case *Notification:
		c := *m
		c.Params = z(c.Params)
		return &c
	case *Response:
		c := *m
		c.Result = z(c.Result)
		if c.Error != nil {
			e := *c.Error
			e.Data = z(e.Data)
			c.Error = &e
		}
		return &c
	}
	return m
}

func TestEncodeGolden(t *testing.T) {
	tests := []struct {
		m    Message
		want string
	}{
		{&Request{ID: IntID(1), Method: "initialize",
			Params: json.RawMessage(`{ "b" : 1, "a" : [ 2 ] }`)},
			`{"jsonrpc":"2.0","id":1,"method":"initialize",` +
				`"params":{"b":1,"a":[2]}}`},
		{&Notification{Method: "exit"},
			`{"jsonrpc":"2.0","method":"exit"}`},
		{&Response{ID: StringID("a")},
			`{"jsonrpc":"2.0","id":"a","result":null}`},
		{&Response{Error: &Error{Code: CodeParseError, Message: "bad",
			Data: json.RawMessage(` [ ] `)}},
			`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,` +
				`"message":"bad","data":[]}}`},
	}
	for _, tt := range tests {
		got, err := Encode(tt.m)
		if err != nil || string(got) != tt.want {
			t.Errorf("Encode(%#v) = %s, %v\nwant %s", tt.m, got, err,
				tt.want)
		}
	}
}

func TestEncodeErrors(t *testing.T) {
	bad := []Message{
		nil, (*Request)(nil), (*Notification)(nil), (*Response)(nil),
		&Request{Method: "m"}, // null id
		&Request{ID: IntID(1), Method: "m", Params: []byte(`3`)},
		&Notification{Method: "m", Params: []byte(`{`)},
		&Notification{Method: "m", Params: []byte(`"s"`)},
		&Response{ID: IntID(1), Result: []byte(`nul`)},
		&Response{ID: IntID(1), Result: []byte(`1`),
			Error: &Error{Code: 1}},
		&Response{ID: IntID(1), Error: &Error{Data: []byte(`{`)}},
	}
	for _, m := range bad {
		if b, err := Encode(m); err == nil {
			t.Errorf("Encode(%#v) = %s, want error", m, b)
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	tests := []struct {
		in   string
		code int64
		id   ID
		call bool
	}{
		{``, CodeParseError, ID{}, false},
		{`{"jsonrpc":"2.0"`, CodeParseError, ID{}, false},
		{`[{"jsonrpc":"2.0","method":"m"}]`, CodeInvalidRequest, ID{},
			false},
		{`"x"`, CodeInvalidRequest, ID{}, false},
		{`null`, CodeInvalidRequest, ID{}, false},
		{`{"id":3,"method":"m"}`, CodeInvalidRequest, IntID(3), true},
		{`{"jsonrpc":"1.0","id":3,"method":"m"}`, CodeInvalidRequest,
			IntID(3), true},
		{`{"jsonrpc":2,"id":"a","method":"m"}`, CodeInvalidRequest,
			StringID("a"), true},
		{`{"jsonrpc":"2.0","id":1.5,"method":"m"}`, CodeInvalidRequest,
			ID{}, true},
		{`{"jsonrpc":"2.0","id":true}`, CodeInvalidRequest, ID{}, false},
		{`{"jsonrpc":"2.0","id":null,"method":"m"}`, CodeInvalidRequest,
			ID{}, true},
		{`{"jsonrpc":"2.0","id":4,"method":7}`, CodeInvalidRequest,
			IntID(4), true},
		{`{"jsonrpc":"2.0","method":"m","params":"s"}`,
			CodeInvalidRequest, ID{}, true},
		{`{"jsonrpc":"2.0","id":5,"method":"m","params":1}`,
			CodeInvalidRequest, IntID(5), true},
		{`{"jsonrpc":"2.0","id":6}`, CodeInvalidRequest, IntID(6), false},
		{`{"jsonrpc":"2.0","id":6,"result":1,"error":{}}`,
			CodeInvalidRequest, IntID(6), false},
		{`{"jsonrpc":"2.0","id":6,"error":{"code":1.5,"message":""}}`,
			CodeInvalidRequest, IntID(6), false},
		{`{"jsonrpc":"2.0","id":6,"error":{"code":1}}`,
			CodeInvalidRequest, IntID(6), false},
		{`{"jsonrpc":"2.0","id":6,"error":"x"}`, CodeInvalidRequest,
			IntID(6), false},
		{`{"jsonrpc":"2.0","result":1}`, CodeInvalidRequest, ID{}, false},
		// Keys are case-sensitive: "Method" is not "method".
		{`{"jsonrpc":"2.0","Method":"m"}`, CodeInvalidRequest, ID{},
			false},
	}
	for _, tt := range tests {
		m, err := Decode([]byte(tt.in))
		var de *DecodeError
		if !errors.As(err, &de) {
			t.Errorf("%s: got %#v, %v; want a DecodeError", tt.in, m, err)
			continue
		}
		if de.Err.Code != tt.code || de.ID != tt.id || de.Call != tt.call {
			t.Errorf("%s: got code %d id %v call %v; want %d %v %v",
				tt.in, de.Err.Code, de.ID, de.Call, tt.code, tt.id,
				tt.call)
		}
		r := de.Response()
		if r.ID != de.ID || r.Error != de.Err {
			t.Errorf("%s: Response() = %#v", tt.in, r)
		}
		if _, err := Encode(r); err != nil {
			t.Errorf("%s: error response does not encode: %v", tt.in, err)
		}
	}
}

func TestDecodeTolerant(t *testing.T) {
	tests := []struct {
		in   string
		want Message
	}{
		{` {"method":"m","jsonrpc":"2.0","extra":{"x":1}} `,
			&Notification{Method: "m"}},
		{`{"jsonrpc":"2.0","method":"m","params":null,"id":"q"}`,
			&Request{ID: StringID("q"), Method: "m"}},
		{`{"jsonrpc":"2.0","id":2,"result":null}`,
			&Response{ID: IntID(2), Result: json.RawMessage(`null`)}},
		{`{"jsonrpc":"2.0","id":2,"error":{"code":-32801,` +
			`"message":"stale","data":{ "a" : 1 }}}`,
			&Response{ID: IntID(2), Error: &Error{
				Code: CodeContentModified, Message: "stale",
				Data: json.RawMessage(`{"a":1}`)}}},
	}
	for _, tt := range tests {
		got, err := Decode([]byte(tt.in))
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %#v, %v; want %#v", tt.in, got, err,
				tt.want)
		}
	}
}

// TestDecodeNeverPanics feeds mutated valid bodies and random bytes.
// Every result is either a message that re-encodes or a DecodeError.
func TestDecodeNeverPanics(t *testing.T) {
	for seed := range uint64(5000) {
		g := newGen(seed)
		b, _ := Encode(g.message())
		for range g.intn(4) {
			switch i := g.intn(len(b) + 1); g.intn(3) {
			case 0:
				if i < len(b) {
					b[i] = byte(g.intn(256))
				}
			case 1:
				b = b[:i]
			default:
				b = append(b[:i:i], append([]byte{"{}[]\":,0"[g.intn(8)]},
					b[i:]...)...)
			}
		}
		m, err := Decode(b)
		var de *DecodeError
		switch {
		case err == nil:
			if _, err := Encode(m); err != nil {
				t.Fatalf("seed %d: decoded %q does not re-encode: %v",
					seed, b, err)
			}
		case !errors.As(err, &de):
			t.Fatalf("seed %d: %q: error %v is not a DecodeError",
				seed, b, err)
		}
	}
}

// TestDecodeReply: every bad body gets an error response (JSON-RPC
// 2.0 section 5) except one that looks like a response, and a body
// that is not UTF-8 is a parse error rather than text with U+FFFD in
// it.
func TestDecodeReply(t *testing.T) {
	tests := []struct {
		in    string
		reply bool
	}{
		{``, true},
		{`{not json`, true},
		{"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"m\"," +
			"\"params\":{\"s\":\"\xff\xfe\"}}", true},
		{`[]`, true},
		{`[{"jsonrpc":"2.0","id":1,"method":"initialize"}]`, true},
		{`42`, true},
		{`null`, true},
		{`"x"`, true},
		{`{}`, true},
		{`{"jsonrpc":"2.0"}`, true},
		{`{"jsonrpc":"2.0","Method":"m"}`, true},
		{`{"jsonrpc":"1.0","method":"m"}`, true},
		{`{"jsonrpc":"2.0","id":3,"method":7}`, true},
		{`{"jsonrpc":"2.0","id":6}`, false},
		{`{"jsonrpc":"2.0","id":true}`, false},
		{`{"jsonrpc":"2.0","result":1}`, false},
		{`{"jsonrpc":"2.0","error":{"code":1,"message":""}}`, false},
		{`{"jsonrpc":"2.0","id":6,"result":1,"error":{}}`, false},
		{`{"jsonrpc":"1.0","id":6,"result":1}`, false},
	}
	for _, tt := range tests {
		_, err := Decode([]byte(tt.in))
		var de *DecodeError
		if !errors.As(err, &de) {
			t.Errorf("%q: %v; want a DecodeError", tt.in, err)
			continue
		}
		if de.Reply() != tt.reply {
			t.Errorf("%q: Reply() = %v, want %v", tt.in, de.Reply(),
				tt.reply)
		}
	}
	_, err := Decode([]byte("{\"jsonrpc\":\"2.0\",\"method\":\"m\"," +
		"\"params\":{\"s\":\"\xc3\"}}"))
	var de *DecodeError
	if !errors.As(err, &de) || de.Err.Code != CodeParseError {
		t.Errorf("invalid UTF-8: %v; want ParseError", err)
	}
}

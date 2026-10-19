package server

import (
	"bytes"
	"encoding/json"

	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
)

// SnapshotMethod is the notification that follows every response and
// every textDocument/publishDiagnostics with the ID of the snapshot it
// was computed from (#25). Its params are {"snapshot":N,"id":ID} after a
// response and {"snapshot":N,"uri":URI} after a publish. Clients ignore
// unknown "$/" notifications, so editors are unaffected, while tests
// and replays see which snapshot every answer belongs to.
const SnapshotMethod = "$/gohard/snapshot"

// call is a decoded client request or notification.
type call struct {
	id     jsonrpc2.ID
	isReq  bool
	method string
	params json.RawMessage
}

// marshal encodes v as compact JSON without HTML escaping. The same
// value always gives the same bytes (struct fields in declaration
// order, map keys sorted).
func marshal(v any) json.RawMessage {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic("server: cannot encode " + err.Error())
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// Send writes one JSON-RPC message to the client.
type Send struct{ Msg jsonrpc2.Message }

// Body returns the encoded message body (without framing).
func (s Send) Body() []byte {
	b, err := jsonrpc2.Encode(s.Msg)
	if err != nil {
		panic("server: cannot encode a message: " + err.Error())
	}
	return b
}

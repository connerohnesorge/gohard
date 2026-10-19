package jsonrpc2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// Message is a [*Request], a [*Notification] or a [*Response].
type Message interface {
	isMessage()
}

// Request is a call that expects exactly one [Response] with the same
// ID.
type Request struct {
	ID     ID
	Method string
	// Params is a JSON object or array, or nil for none.
	Params json.RawMessage
}

// Notification is a call without an ID; it gets no response.
type Notification struct {
	Method string
	// Params is a JSON object or array, or nil for none.
	Params json.RawMessage
}

// Response answers the [Request] with the same ID. Exactly one of
// Result and Error is meaningful: when Error is nil the response is a
// success and a nil Result is sent as null.
type Response struct {
	ID     ID
	Result json.RawMessage
	Error  *Error
}

func (*Request) isMessage()      {}
func (*Notification) isMessage() {}
func (*Response) isMessage()     {}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int64
	Message string
	// Data is any JSON value, or nil for none.
	Data json.RawMessage
}

// Error implements error.
func (e *Error) Error() string {
	return "jsonrpc2: " + e.Message + " (" +
		strconv.FormatInt(e.Code, 10) + ")"
}

// Errorf returns an *Error with the given code and formatted message.
func Errorf(code int64, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// The JSON-RPC 2.0 error codes and the LSP 3.17 additions.
const (
	CodeParseError     int64 = -32700
	CodeInvalidRequest int64 = -32600
	CodeMethodNotFound int64 = -32601
	CodeInvalidParams  int64 = -32602
	CodeInternalError  int64 = -32603

	// CodeServerNotInitialized: a request other than initialize came
	// before it.
	CodeServerNotInitialized int64 = -32002
	// CodeUnknownErrorCode is reserved by LSP for unknown errors.
	CodeUnknownErrorCode int64 = -32001

	// CodeRequestFailed: the request was valid but failed.
	CodeRequestFailed int64 = -32803
	// CodeServerCancelled: the server cancelled the request itself.
	CodeServerCancelled int64 = -32802
	// CodeContentModified: the document changed under the request, so
	// its result would be stale (#11 invariant 3).
	CodeContentModified int64 = -32801
	// CodeRequestCancelled: the client cancelled the request with
	// $/cancelRequest.
	CodeRequestCancelled int64 = -32800
)

// Encode returns the JSON body of m with the keys in a fixed order:
// jsonrpc, id, method, params for calls and jsonrpc, id, result or
// error for responses. Params, Result and Data are compacted. It fails
// on a nil message, invalid raw JSON, params that are not an object
// or array, a request with the null ID or a response with both a
// result and an error.
func Encode(m Message) ([]byte, error) {
	return AppendEncode(nil, m)
}

// AppendEncode is [Encode] appending to dst.
func AppendEncode(dst []byte, m Message) ([]byte, error) {
	dst = append(dst, `{"jsonrpc":"2.0"`...)
	var err error
	switch m := m.(type) {
	case *Request:
		if m == nil {
			return nil, errNilMessage
		}
		if m.ID.IsNull() {
			return nil, errors.New("jsonrpc2: request with null id")
		}
		dst = append(dst, `,"id":`...)
		dst = m.ID.appendJSON(dst)
		dst, err = appendCall(dst, m.Method, m.Params)
	case *Notification:
		if m == nil {
			return nil, errNilMessage
		}
		dst, err = appendCall(dst, m.Method, m.Params)
	case *Response:
		if m == nil {
			return nil, errNilMessage
		}
		dst = append(dst, `,"id":`...)
		dst = m.ID.appendJSON(dst)
		dst, err = appendReply(dst, m)
	default:
		return nil, errNilMessage
	}
	if err != nil {
		return nil, err
	}
	return append(dst, '}'), nil
}

var errNilMessage = errors.New("jsonrpc2: nil or unknown message")

func appendCall(dst []byte, method string, params []byte) ([]byte, error) {
	dst = append(dst, `,"method":`...)
	dst = appendString(dst, method)
	if len(params) == 0 {
		return dst, nil
	}
	if !structured(params) {
		return nil, errors.New(
			"jsonrpc2: params must be a JSON object or array")
	}
	dst = append(dst, `,"params":`...)
	return appendCompact(dst, params)
}

func appendReply(dst []byte, m *Response) ([]byte, error) {
	if m.Error == nil {
		dst = append(dst, `,"result":`...)
		if len(m.Result) == 0 {
			return append(dst, "null"...), nil
		}
		return appendCompact(dst, m.Result)
	}
	if len(m.Result) != 0 {
		return nil, errors.New(
			"jsonrpc2: response with both result and error")
	}
	dst = append(dst, `,"error":{"code":`...)
	dst = strconv.AppendInt(dst, m.Error.Code, 10)
	dst = append(dst, `,"message":`...)
	dst = appendString(dst, m.Error.Message)
	if len(m.Error.Data) != 0 {
		var err error
		dst = append(dst, `,"data":`...)
		if dst, err = appendCompact(dst, m.Error.Data); err != nil {
			return nil, err
		}
	}
	return append(dst, '}'), nil
}

func appendCompact(dst, raw []byte) ([]byte, error) {
	buf := bytes.NewBuffer(dst)
	if err := json.Compact(buf, raw); err != nil {
		return nil, fmt.Errorf("jsonrpc2: invalid JSON: %w", err)
	}
	return buf.Bytes(), nil
}

// structured reports whether raw starts like a JSON object or array.
// Validity is checked when it is compacted.
func structured(raw []byte) bool {
	raw = bytes.TrimLeft(raw, " \t\r\n")
	return len(raw) > 0 && (raw[0] == '{' || raw[0] == '[')
}

// DecodeError is a message body that is not valid JSON-RPC. The stream
// is still in sync; [DecodeError.Response] is the reply JSON-RPC asks
// for.
type DecodeError struct {
	// ID is the message's ID when it could be read, else null.
	ID ID
	// Call reports whether the body looked like a request or
	// notification (it had a method) rather than a response.
	Call bool
	Err  *Error
	// response reports that the body looked like a response: an
	// object with an "id", "result" or "error" and no "method".
	response bool
}

// Reply reports whether JSON-RPC asks for e's error response: for
// every bad body (invalid JSON or UTF-8, a batch, a scalar, an object
// that is neither a call nor a response, a bad call) except one that
// looks like a response, which must never be answered.
func (e *DecodeError) Reply() bool { return !e.response }

// Error implements error.
func (e *DecodeError) Error() string { return e.Err.Error() }

// Unwrap returns the JSON-RPC error.
func (e *DecodeError) Unwrap() error { return e.Err }

// Response returns the error response for the bad message.
func (e *DecodeError) Response() *Response {
	return &Response{ID: e.ID, Error: e.Err}
}

func parseError(format string, args ...any) *DecodeError {
	return &DecodeError{Err: Errorf(CodeParseError, format, args...)}
}

func invalid(id ID, call bool, format string, args ...any) *DecodeError {
	return &DecodeError{ID: id, Call: call,
		Err: Errorf(CodeInvalidRequest, format, args...)}
}

// Decode parses one message body. Keys match exactly (encoding/json's
// case folding is not used) and unknown keys are ignored. The body must
// be a single JSON object (LSP has no batches) with "jsonrpc":"2.0". A
// message with a string "method" is a request when it has a non-null
// "id" and a notification when it has none; otherwise it must be a
// response with an "id" and exactly one of "result" and "error".
// Params that are null count as absent. The error, if any, is a
// *DecodeError: code ParseError for invalid JSON, InvalidRequest for
// everything else.
func Decode(body []byte) (Message, error) {
	if !utf8.Valid(body) {
		// encoding/json would replace the bad bytes with U+FFFD, so
		// document text would silently differ from the client's.
		return nil, parseError("message is not valid UTF-8")
	}
	if !json.Valid(body) {
		return nil, parseError("invalid JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, invalid(ID{}, false,
			"message must be a JSON object")
	}
	m, err := decodeFields(fields)
	var de *DecodeError
	if errors.As(err, &de) && !de.Call {
		// A malformed response must not be answered; an object
		// that is neither a call nor a response must be.
		_, hasID := fields["id"]
		_, hasResult := fields["result"]
		_, hasErr := fields["error"]
		de.response = hasID || hasResult || hasErr
	}
	return m, err
}

func decodeFields(fields map[string]json.RawMessage) (Message, error) {
	rawID, hasID := fields["id"]
	var id ID
	if hasID && id.UnmarshalJSON(rawID) != nil {
		_, call := fields["method"]
		return nil, invalid(ID{}, call, "%v", errBadID)
	}
	rawMethod, call := fields["method"]
	var version string
	if json.Unmarshal(fields["jsonrpc"], &version) != nil ||
		version != "2.0" {
		return nil, invalid(id, call, `"jsonrpc" must be "2.0"`)
	}
	if call {
		return decodeCall(fields, id, hasID, rawMethod)
	}
	return decodeResponse(fields, id, hasID)
}

func decodeCall(fields map[string]json.RawMessage, id ID, hasID bool,
	rawMethod json.RawMessage) (Message, error) {
	var method string
	if !isString(rawMethod) || json.Unmarshal(rawMethod, &method) != nil {
		return nil, invalid(id, true, `"method" must be a string`)
	}
	if hasID && id.IsNull() {
		return nil, invalid(id, true, "request id must not be null")
	}
	params := fields["params"]
	if isNull(params) {
		params = nil
	}
	if params != nil && !structured(params) {
		return nil, invalid(id, true,
			`"params" must be an object or an array`)
	}
	params = compact(params)
	if !hasID {
		return &Notification{Method: method, Params: params}, nil
	}
	return &Request{ID: id, Method: method, Params: params}, nil
}

func decodeResponse(fields map[string]json.RawMessage, id ID,
	hasID bool) (Message, error) {
	result, hasResult := fields["result"]
	rawErr, hasErr := fields["error"]
	switch {
	case !hasID:
		return nil, invalid(id, false, `message has no "method" or "id"`)
	case hasResult == hasErr:
		return nil, invalid(id, false,
			`response must have exactly one of "result" and "error"`)
	case hasResult:
		return &Response{ID: id, Result: compact(result)}, nil
	}
	e, err := decodeErrorObject(rawErr)
	if err != nil {
		return nil, invalid(id, false, "%v", err)
	}
	return &Response{ID: id, Error: e}, nil
}

func decodeErrorObject(raw json.RawMessage) (*Error, error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, errors.New(`"error" must be an object`)
	}
	var e Error
	code := bytes.TrimSpace(obj["code"])
	if !isInteger(code) {
		return nil, errors.New(`"error.code" must be an integer`)
	}
	var err error
	if e.Code, err = strconv.ParseInt(string(code), 10, 64); err != nil {
		return nil, errors.New(`"error.code" is out of range`)
	}
	msg := obj["message"]
	if !isString(msg) || json.Unmarshal(msg, &e.Message) != nil {
		return nil, errors.New(`"error.message" must be a string`)
	}
	if data, ok := obj["data"]; ok {
		e.Data = compact(data)
	}
	return &e, nil
}

func isNull(raw []byte) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

func isString(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '"'
}

// compact returns raw without insignificant whitespace, as a copy that
// does not alias the decoded body. raw is valid JSON.
func compact(raw []byte) json.RawMessage {
	if raw == nil {
		return nil
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil { // unreachable: raw is valid
		return bytes.Clone(raw)
	}
	return buf.Bytes()
}

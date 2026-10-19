package server_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/internal/server"
)

// checkSession checks the server invariants of #25 on one session,
// given the client message bodies and the server message bodies:
//
//   - every request gets at most one response (a client that reuses an
//     ID gets one per request; errors for messages without a readable
//     ID use the null ID and may repeat), and every request sent
//     before shutdown gets exactly one;
//   - every response after the initialize result, and every
//     publishDiagnostics, is followed by $/gohard/snapshot naming it;
//   - snapshot IDs never decrease;
//   - $/progress for a token is begin, reports, end, in that order;
//   - the server never answers with InternalError.
func checkSession(client, srv []json.RawMessage) []string {
	var probs []string
	bad := func(f string, a ...any) {
		probs = append(probs, fmt.Sprintf(f, a...))
	}
	sent := map[string]int{} // requests per ID the client sent
	mustAnswer := map[string]bool{}
	shutdown := false
	for _, raw := range client {
		m, err := jsonrpc2.Decode(raw)
		var de *jsonrpc2.DecodeError
		if errors.As(err, &de) && de.Call && !de.ID.IsNull() {
			sent[de.ID.String()]++ // answered with an error
			continue
		}
		r, ok := m.(*jsonrpc2.Request)
		if err != nil || !ok {
			continue
		}
		sent[r.ID.String()]++
		if !shutdown {
			mustAnswer[r.ID.String()] = true
		}
		if r.Method == "shutdown" {
			shutdown = true
		}
	}
	if !shutdown {
		mustAnswer = map[string]bool{}
	}
	answered := map[string]int{}
	progress := map[string]string{} // token -> last kind
	var lastSnap uint64
	initDone := false
	expectNote := func(i int, key, val string) {
		if i+1 >= len(srv) {
			bad("message %d: no %s follows it", i+1,
				server.SnapshotMethod)
			return
		}
		n, err := jsonrpc2.Decode(srv[i+1])
		note, ok := n.(*jsonrpc2.Notification)
		if err != nil || !ok || note.Method != server.SnapshotMethod {
			bad("message %d: not followed by %s", i+1,
				server.SnapshotMethod)
			return
		}
		var p map[string]json.RawMessage
		_ = json.Unmarshal(note.Params, &p)
		if string(p[key]) != val {
			bad("message %d: snapshot note has %s %s, want %s", i+2,
				key, p[key], val)
		}
	}
	for i, raw := range srv {
		m, err := jsonrpc2.Decode(raw)
		if err != nil {
			bad("message %d: %v", i+1, err)
			continue
		}
		switch m := m.(type) {
		case *jsonrpc2.Response:
			k := m.ID.String()
			answered[k]++
			if answered[k] > sent[k] && !m.ID.IsNull() {
				bad("message %d: response %d to ID %s, which the "+
					"client sent %d times", i+1, answered[k], k, sent[k])
			}
			if m.Error != nil &&
				m.Error.Code == jsonrpc2.CodeInternalError {
				bad("message %d: internal error: %s", i+1,
					m.Error.Message)
			}
			if initDone || m.Error == nil && m.Result != nil &&
				isInitResult(m.Result) {
				initDone = true
				id, _ := json.Marshal(m.ID)
				expectNote(i, "id", string(id))
			}
		case *jsonrpc2.Notification:
			switch m.Method {
			case "textDocument/publishDiagnostics":
				var p struct {
					URI string `json:"uri"`
				}
				_ = json.Unmarshal(m.Params, &p)
				uri, _ := json.Marshal(p.URI)
				expectNote(i, "uri", string(uri))
			case server.SnapshotMethod:
				var p struct {
					Snapshot uint64 `json:"snapshot"`
				}
				_ = json.Unmarshal(m.Params, &p)
				if p.Snapshot < lastSnap {
					bad("message %d: snapshot %d after %d", i+1,
						p.Snapshot, lastSnap)
				}
				lastSnap = p.Snapshot
			case "$/progress":
				var p struct {
					Token json.RawMessage `json:"token"`
					Value struct {
						Kind string `json:"kind"`
					} `json:"value"`
				}
				_ = json.Unmarshal(m.Params, &p)
				tok, last := string(p.Token), progress[string(p.Token)]
				ok := p.Value.Kind == "begin" && last == "" ||
					p.Value.Kind != "begin" &&
						(last == "begin" || last == "report")
				if !ok {
					bad("message %d: progress %s after %q", i+1,
						p.Value.Kind, last)
				}
				progress[tok] = p.Value.Kind
			}
		}
	}
	for _, k := range slices.Sorted(maps.Keys(mustAnswer)) {
		if answered[k] == 0 {
			bad("request %s sent before shutdown got no response", k)
		}
	}
	return probs
}

func isInitResult(r json.RawMessage) bool {
	var v struct {
		Capabilities json.RawMessage `json:"capabilities"`
	}
	return json.Unmarshal(r, &v) == nil && v.Capabilities != nil
}

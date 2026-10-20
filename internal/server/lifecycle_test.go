package server

import (
	"strconv"
	"testing"

	"github.com/connerohnesorge/gohard/internal/jsonrpc2"
)

// TestEndAnswersEveryRequest: when shutdown or exit arrives, every
// request still running, parked on the workspace load or parked on a
// reread gets exactly one RequestCancelled, and a request after
// shutdown gets InvalidRequest (#25).
func TestEndAnswersEveryRequest(t *testing.T) {
	ends := []struct{ name, msg string }{
		{"shutdown", `{"jsonrpc":"2.0","id":99,"method":"shutdown"}`},
		{"exit", `{"jsonrpc":"2.0","method":"exit"}`},
	}
	for _, park := range []string{"load", "reread"} {
		for _, end := range ends {
			name := end.name
			h := newHarness(t, slow{hover: 1000, diags: 1})
			want := []int{}
			if park == "load" {
				h.hold = true
				h.start(`{}`)
				h.open(uri, "x")
				h.hover(1, uri)
				h.hover(2, uri)
				h.drain()
				want = append(want, 1, 2)
			} else {
				h.start(`{}`)
				h.open(uri, "x")
				h.drain()
				h.hover(1, uri) // running: 1000 slices
				h.step()
				h.step()
				h.hold = true
				h.client(`{"jsonrpc":"2.0","method":` +
					`"workspace/didChangeWatchedFiles","params":` +
					`{"changes":[{"uri":"file:///work/b.vhd","type":1}]}}`)
				h.step()
				h.hover(2, uri) // parked on the reread
				h.step()
				want = append(want, 1, 2)
			}
			h.client("%s", end.msg)
			h.hover(3, uri)
			h.drain()
			got := h.responses()
			for _, id := range want {
				rs := got[strconv.Itoa(id)]
				if len(rs) != 1 || rs[0].Error == nil ||
					rs[0].Error.Code != jsonrpc2.CodeRequestCancelled {
					t.Errorf("%s/%s: request %d: %d responses %+v",
						park, name, id, len(rs), rs)
				}
			}
			rs := got["3"]
			switch name {
			case "shutdown":
				if len(got["99"]) != 1 || got["99"][0].Error != nil {
					t.Errorf("%s/shutdown: shutdown answered %+v",
						park, got["99"])
				}
				if len(rs) != 1 || rs[0].Error == nil ||
					rs[0].Error.Code != jsonrpc2.CodeInvalidRequest {
					t.Errorf("%s/shutdown: request after it: %+v",
						park, rs)
				}
			case "exit":
				if len(rs) != 0 || h.exit != 1 {
					t.Errorf("%s/exit: after exit %+v, code %d",
						park, rs, h.exit)
				}
			}
		}
	}
}

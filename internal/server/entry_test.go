package server

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
	"github.com/connerohnesorge/gohard/v2/internal/env/simenv"
	"github.com/connerohnesorge/gohard/v2/internal/jsonrpc2"
	"github.com/connerohnesorge/gohard/v2/internal/repro"
)

// TestArrivalLineRoundTrip: every client input, whatever its bytes,
// survives the session file: Line, then the session encoding, then
// parseLines gives back the body (compacted when it is JSON), the end
// of the input, and the step when the core was busy.
func TestArrivalLineRoundTrip(t *testing.T) {
	bodies := [][]byte{
		[]byte(`{"jsonrpc":"2.0","method":"initialized","params":{}}`),
		[]byte(`{ "jsonrpc" : "2.0", "id" : 1, "method" : "x" }`),
		[]byte(`{not json`),
		[]byte(""),
		[]byte("\xff\xfe{}"),
		[]byte(`[]`),
		[]byte(`42`),
		[]byte(`"$gohard"`),
		[]byte(`{"$gohard":"end","step":3}`),
		[]byte(`{"$gohard":"file","path":"../x","data":""}`),
		[]byte(`{"$GOHARD":"end"}`),
		nil,
	}
	h, err := SessionHeader(simenv.New(simenv.Options{Dir: "/w"}).Env(),
		"/w", "t")
	if err != nil {
		t.Fatal(err)
	}
	r := &env.SplitMix64{State: 7}
	for i := range 2000 {
		var a Arrival
		if i < len(bodies)*2 {
			a.Body = bodies[i%len(bodies)]
			a.Busy = i >= len(bodies)
		} else {
			n := r.Uint64() % 12
			a.Body = make([]byte, n)
			for k := range a.Body {
				const alphabet = "{}[]\":,0a$\xff \\"
				a.Body[k] = alphabet[r.Uint64()%uint64(len(alphabet))]
			}
			if r.Uint64()%5 == 0 {
				a.Body = nil
			}
			a.Busy = r.Uint64()%2 == 0
		}
		a.Step = r.Uint64() % 100
		line := a.Line()
		if line == nil {
			if a.Body != nil || a.Busy {
				t.Fatalf("%q busy %v: no line", a.Body, a.Busy)
			}
			continue
		}
		sess := repro.Session{Header: h,
			Messages: []json.RawMessage{line}}
		file, err := sess.Marshal()
		if err != nil {
			t.Fatalf("%q: %v", a.Body, err)
		}
		back, err := repro.ParseSession(file)
		if err != nil {
			t.Fatalf("%q: %v", a.Body, err)
		}
		ins, p, err := parseLines(back.Messages)
		if err != nil || len(ins) != 1 || len(p.files) != 0 {
			t.Fatalf("%q: %v, %d inputs", a.Body, err, len(ins))
		}
		in := ins[0]
		want := a.Body
		if json.Valid(want) {
			var b bytes.Buffer
			_ = json.Compact(&b, want)
			want = b.Bytes()
		}
		if (in.body == nil) != (a.Body == nil) ||
			!bytes.Equal(in.body, want) || in.timed != a.Busy ||
			a.Busy && in.step != a.Step {
			t.Fatalf("%q busy %v step %d: got %q timed %v step %d",
				a.Body, a.Busy, a.Step, in.body, in.timed, in.step)
		}
	}
}

// TestParseLinesProject: file and analyzer entries describe the
// project; a file path must be clean and relative, and an unknown
// entry kind is an error.
func TestParseLinesProject(t *testing.T) {
	lines := []json.RawMessage{
		AnalyzerLine("echo"),
		FileLine("rtl/a.vhd", []byte("entity a is end;\n")),
		FileLine("empty.vhd", nil),
		[]byte(`{"jsonrpc":"2.0","method":"exit"}`),
	}
	ins, p, err := parseLines(lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 1 || p.analyzer != "echo" || len(p.files) != 2 ||
		string(p.files["rtl/a.vhd"]) != "entity a is end;\n" ||
		p.files["empty.vhd"] == nil {
		t.Fatalf("got %v %+v", ins, p)
	}
	for _, bad := range []string{
		`{"$gohard":"file","path":"../a.vhd"}`,
		`{"$gohard":"file","path":"/a.vhd"}`,
		`{"$gohard":"file","path":"a/./b.vhd"}`,
		`{"$gohard":"file","path":""}`,
		`{"$gohard":"nope"}`,
		`{"$gohard":"message"}`,
		`{"$gohard":"message","step":-1,"message":{}}`,
	} {
		if _, _, err := parseLines([]json.RawMessage{
			json.RawMessage(bad)}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	dup := []json.RawMessage{FileLine("a.vhd", nil),
		FileLine("a.vhd", nil)}
	if _, _, err := parseLines(dup); err == nil {
		t.Error("a repeated file was accepted")
	}
}

// TestRejectedAndConfigLines: a skipped request and a gohard.yaml read
// round-trip through their session lines; config entries are project
// data, not inputs, and keep their order; bad entries are errors.
func TestRejectedAndConfigLines(t *testing.T) {
	lines := []json.RawMessage{
		Arrival{Step: 2, Config: &ConfigRead{Path: "gohard.yaml",
			Data: []byte("standard: \"2008\"\n")}}.Line(),
		Arrival{Rejected: &Rejected{ID: jsonrpc2.StringID("a"),
			Reason: "too big"}, Step: 4, Busy: true}.Line(),
		Arrival{Rejected: &Rejected{ID: jsonrpc2.IntID(7),
			Reason: "latin1"}}.Line(),
		Arrival{Step: 9, Config: &ConfigRead{Missing: true}}.Line(),
		Arrival{Step: 9, Config: &ConfigRead{Path: "../gohard.yaml",
			Data: []byte{}}}.Line(),
	}
	ins, p, err := parseLines(lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 2 || ins[0].reject == nil ||
		ins[0].reject.ID != jsonrpc2.StringID("a") ||
		ins[0].reject.Reason != "too big" || !ins[0].timed ||
		ins[0].step != 4 || ins[1].reject == nil ||
		ins[1].reject.ID != jsonrpc2.IntID(7) || ins[1].timed {
		t.Fatalf("inputs %+v", ins)
	}
	if len(p.configs) != 3 ||
		string(p.configs[0].Data) != "standard: \"2008\"\n" ||
		!p.configs[1].Missing || p.configs[2].Path != "../gohard.yaml" ||
		len(p.configs[2].Data) != 0 || p.configs[2].Missing {
		t.Fatalf("configs %+v", p.configs)
	}
	for _, bad := range []string{
		`{"$gohard":"rejected","reason":"x"}`,
		`{"$gohard":"rejected","id":null}`,
		`{"$gohard":"config","path":"x.yaml"}`,
		`{"$gohard":"config","path":"/gohard.yaml"}`,
		`{"$gohard":"config","path":"a/../gohard.yaml"}`,
		`{"$gohard":"config","path":"C:/gohard.yaml"}`,
		`{"$gohard":"config","path":"a\\gohard.yaml"}`,
		`{"$gohard":"file","path":"/a.vhd","data":""}`,
		`{"$gohard":"file","path":"../a.vhd","data":""}`,
		`{"$gohard":"file","path":"c:/a.vhd","data":""}`,
		`{"$gohard":"file","path":"rtl\\a.vhd","data":""}`,
		`{"$gohard":"file","path":"./a.vhd","data":""}`,
		`{"$gohard":"config"}`,
		`{"$gohard":"config","path":"gohard.yaml","missing":true}`,
	} {
		if _, _, err := parseLines([]json.RawMessage{
			json.RawMessage(bad)}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

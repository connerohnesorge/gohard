package elab

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/v2/internal/condanal"
	"github.com/connerohnesorge/gohard/v2/internal/eval"
	"github.com/connerohnesorge/gohard/v2/internal/token"
)

// gRand is a small deterministic generator (SplitMix64) for the
// property tests.
type gRand uint64

func (r *gRand) next() uint64 {
	*r += 0x9e3779b97f4a7c15
	z := uint64(*r)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (r *gRand) intn(n int64) int64 { return int64(r.next() % uint64(n)) }

// TestBuiltinOneSource: condanal.Builtin answers every built-in
// identifier in any case with the value of condanal.Builtins, and
// nothing else, under every standard; envFunc reads the same values.
func TestBuiltinOneSource(t *testing.T) {
	for _, std := range token.Standards {
		for _, b := range condanal.Builtins(std, "9.9") {
			for _, n := range []string{b.Name, strings.ToLower(b.Name),
				strings.ToUpper(b.Name[:1]) +
					strings.ToLower(b.Name[1:])} {
				if v, ok := condanal.Builtin(n, std, "9.9"); !ok ||
					v != b.Value {
					t.Errorf("%v %s: %q %v, want %q", std, n, v, ok, b.Value)
				}
			}
		}
		if _, ok := condanal.Builtin("TOOL_X", std, ""); ok {
			t.Errorf("%v: TOOL_X is a built-in", std)
		}
	}
}

// TestTimeRecordRoundTrip: for random times, the TIME_RECORD of
// GMTIME(s) gives back s through EPOCH(TREC) at microsecond
// resolution, its fields agree with the calendar, TO_STRING is ISO
// 8601 with truncated fractions, and TREC + d - d is TREC.
func TestTimeRecordRoundTrip(t *testing.T) {
	// TIME_RECORD as std.env declares it.
	ir := func(lo, hi int64) eval.Field {
		return eval.Field{Type: &eval.Type{Kind: eval.KindInteger,
			Name: "INTEGER", Range: eval.Range{Left: lo, Right: hi}}}
	}
	dow := eval.Field{Type: &eval.Type{Kind: eval.KindEnum,
		Name: "DAYOFWEEK", Range: eval.Range{Left: 0, Right: 6},
		Literals: []string{"sunday", "monday", "tuesday", "wednesday",
			"thursday", "friday", "saturday"}}}
	rt := &eval.Type{Kind: eval.KindRecord, Name: "TIME_RECORD",
		Fields: []eval.Field{ir(0, 999_999), ir(0, 61), ir(0, 59),
			ir(0, 23), ir(1, 31), ir(0, 11), ir(1, 4095), dow, ir(0, 365)}}
	r := gRand(18)
	for range 2000 {
		// Years 1 .. 4095 (TIME_RECORD's range), in microseconds.
		lo := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC).UnixMicro()
		hi := time.Date(4095, 12, 31, 0, 0, 0, 0, time.UTC).UnixMicro()
		us := lo + r.intn(hi-lo)
		s := float64(us) / 1e6
		tm, err := secondsTime(s)
		if err != nil {
			t.Fatal(err)
		}
		v, err := timeRecord(rt, tm)
		if err != nil {
			t.Fatalf("%v: %v", tm, err)
		}
		back, err := recordTime(v)
		if err != nil || !back.Equal(tm) {
			t.Fatalf("%v: back %v %v", tm, back, err)
		}
		if got := unixSeconds(back); math.Abs(got-s) > 1e-6*math.Max(1,
			math.Abs(s)/1e9) {
			t.Fatalf("%v: epoch %v, want %v", tm, got, s)
		}
		e := v.E
		if e[trWeekday].I != int64(tm.Weekday()) ||
			e[trDayOfYear].I != int64(tm.YearDay()-1) ||
			e[trMonth].I != int64(tm.Month()-1) {
			t.Fatalf("%v: fields %v", tm, e)
		}
		digits := int(r.intn(7))
		want := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d", tm.Year(),
			tm.Month(), tm.Day(), tm.Hour(), tm.Minute(), tm.Second())
		if digits > 0 {
			want += "." + fmt.Sprintf("%06d", tm.Nanosecond()/1000)[:digits]
		}
		if got := isoTime(tm, digits); got != want {
			t.Fatalf("TO_STRING(%v, %d) = %q, want %q", tm, digits, got,
				want)
		}
		dl := float64(r.intn(1e9)) / 1e3
		p, err := addSeconds(tm, dl)
		if err != nil {
			t.Fatal(err)
		}
		if q, _ := addSeconds(p, -dl); !q.Equal(tm) {
			t.Fatalf("%v + %v - %v = %v", tm, dl, dl, q)
		}
	}
}

// TestReflectIntegerProperty: for random integer subtypes and values,
// the mirrors agree with the predefined attributes.
func TestReflectIntegerProperty(t *testing.T) {
	r := gRand(7)
	var decls, body strings.Builder
	var want []string
	for i := range 40 {
		a := r.intn(2000) - 1000
		b := a + r.intn(500)
		dir, left, right := "to", a, b
		if r.intn(2) == 0 {
			dir, left, right = "downto", b, a
		}
		v := a + r.intn(b-a+1)
		fmt.Fprintf(&decls, "  subtype s%d is integer range %d %s %d;\n",
			i, left, dir, right)
		fmt.Fprintf(&decls, "  constant c%d : s%d := %d;\n", i, i, v)
		fmt.Fprintf(&body, "    sm := c%d'reflect.to_integer."+
			"get_subtype_mirror;\n", i)
		fmt.Fprintf(&body, "    report sm.simple_name & \" \" & "+
			"sm.left.image & \" \" & sm.right.image & \" \" & "+
			"sm.low.image & \" \" & sm.high.image & \" \" & "+
			"index'image(sm.length) & \" \" & "+
			"boolean'image(sm.ascending) & \" \" & "+
			"c%d'reflect.to_integer.image & \" \" & "+
			"integer'image(c%d'reflect.to_integer.value);\n", i, i)
		want = append(want, fmt.Sprintf("S%d %d %d %d %d %d %v %d %d",
			i, left, right, a, b, b-a+1, dir == "to", v, v))
	}
	src := "use std.reflection.all;\nentity tb is end entity;\n" +
		"architecture a of tb is\n" + decls.String() + "begin\n" +
		"  process\n    variable sm : integer_subtype_mirror;\n" +
		"  begin\n" + body.String() + "    wait;\n  end process;\n" +
		"end architecture;\n"
	gSim(t, []gSource{{"tb.vhd", src}}, "tb", EnvOptions{}).want(t,
		want...)
}

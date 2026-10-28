package coverage

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// genReport returns a random report from seed: a few files with lines
// and branches, edge values (0, NotRun, the largest count, line 1)
// first, duplicates and unsorted order included.
func genReport(seed uint64) Report {
	rng := &env.SplitMix64{State: seed}
	n := func(k uint64) int { return int(rng.Uint64() % k) }
	count := func() int64 {
		switch n(6) {
		case 0:
			return 0
		case 1:
			return math.MaxInt64 - int64(n(3))
		}
		return int64(n(1000))
	}
	paths := []string{"a.vhd", "b.vhd", "lib/c.vhd", "../ext/d.vhd"}
	var r Report
	for range n(4) {
		f := File{Path: paths[n(uint64(len(paths)))]}
		for range n(8) {
			f.Lines = append(f.Lines, Line{Line: 1 + n(20),
				Count: count()})
		}
		for range n(6) {
			b := Branch{Line: 1 + n(20), Block: n(2), Branch: n(3),
				Taken: count()}
			if n(4) == 0 {
				b.Taken = NotRun
			}
			f.Branches = append(f.Branches, b)
		}
		r.Files = append(r.Files, f)
	}
	return r
}

// TestEncodeParse: Parse reads back exactly what Encode wrote, and
// Encode of a parsed file is byte-identical (the canonical form).
func TestEncodeParse(t *testing.T) {
	for seed := range uint64(500) {
		a := Encode(genReport(seed))
		r, err := Parse(a)
		if err != nil {
			t.Fatalf("seed %d: %v\n%s", seed, err, a)
		}
		if b := Encode(r); !bytes.Equal(a, b) {
			t.Fatalf("seed %d: round trip differs:\n%s\n---\n%s", seed,
				a, b)
		}
	}
}

// TestMergeOrder: merging gives the same bytes in every order of its
// inputs and of their files, and in every grouping (the property `gohard
// coverage merge` relies on, #62).
func TestMergeOrder(t *testing.T) {
	for seed := range uint64(300) {
		rs := []Report{genReport(3 * seed), genReport(3*seed + 1),
			genReport(3*seed + 2)}
		want := Encode(Merge(rs...))
		for step := range uint64(6) {
			perm := env.Shuffle(seed, step, len(rs))
			var in []Report
			for _, i := range perm {
				r := rs[i]
				rev := Report{}
				for j := len(r.Files) - 1; j >= 0; j-- {
					rev.Files = append(rev.Files, r.Files[j])
				}
				in = append(in, rev)
			}
			if got := Encode(Merge(in...)); !bytes.Equal(got, want) {
				t.Fatalf("seed %d order %v: merge differs:\n%s\n---\n%s",
					seed, perm, want, got)
			}
		}
		grouped := Merge(Merge(rs[0], rs[1]), rs[2])
		if got := Encode(grouped); !bytes.Equal(got, want) {
			t.Fatalf("seed %d: (a+b)+c differs from a+b+c", seed)
		}
		// Merging the encoded files is merging the reports.
		var parsed []Report
		for _, r := range rs {
			p, err := Parse(Encode(r))
			if err != nil {
				t.Fatal(err)
			}
			parsed = append(parsed, p)
		}
		if got := Encode(Merge(parsed...)); !bytes.Equal(got, want) {
			t.Fatalf("seed %d: merge of the parsed files differs", seed)
		}
	}
}

// TestMergeSums pins the sums: counts add up, a branch not run takes
// the other run's count, and counts saturate.
func TestMergeSums(t *testing.T) {
	a := Report{Files: []File{{Path: "x.vhd",
		Lines: []Line{{3, 2}, {5, 0}},
		Branches: []Branch{{3, 0, 0, 1}, {3, 0, 1, NotRun},
			{3, 0, 2, NotRun}}}}}
	b := Report{Files: []File{
		{Path: "x.vhd", Lines: []Line{{3, math.MaxInt64}, {7, 1}},
			Branches: []Branch{{3, 0, 0, 4}, {3, 0, 1, 0},
				{3, 0, 2, NotRun}}},
		{Path: "a.vhd", Lines: []Line{{1, 1}}},
	}}
	want := "TN:\nSF:a.vhd\nDA:1,1\nLF:1\nLH:1\nend_of_record\n" +
		"TN:\nSF:x.vhd\nBRDA:3,0,0,5\nBRDA:3,0,1,0\nBRDA:3,0,2,-\n" +
		"BRF:3\nBRH:1\nDA:3,9223372036854775807\nDA:5,0\nDA:7,1\n" +
		"LF:3\nLH:2\nend_of_record\n"
	if got := string(Encode(Merge(a, b))); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	tot := Merge(a, b).Totals()
	if s := tot.String(); s != "3 of 4 lines (75.0%), 1 of 3 branches "+
		"(33.3%)" {
		t.Fatalf("totals %q", s)
	}
	if s := (Totals{Lines: 3, LinesHit: 2}).String(); s !=
		"2 of 3 lines (66.6%), 0 of 0 branches" {
		t.Fatalf("totals %q", s)
	}
}

// TestParseErrors: every malformed record is an error naming its line.
func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ in, err string }{
		{"DA:1,1\n", "line 1: expected SF"},
		{"SF:a\nDA:1\nend_of_record\n", "line 2: malformed DA"},
		{"SF:a\nDA:0,1\nend_of_record\n", "line 2: malformed DA"},
		{"SF:a\nDA:1,-1\nend_of_record\n", "line 2: malformed DA"},
		{"SF:a\nDA:1,1,abc\nend_of_record\n", "line 2: malformed DA"},
		{"SF:a\nBRDA:1,0,0\nend_of_record\n", "line 2: malformed BRDA"},
		{"SF:a\nBRDA:1,0,0,x\nend_of_record\n", "line 2: malformed BRDA"},
		{"SF:a\nBRDA:1,0,0,-2\nend_of_record\n",
			"line 2: malformed BRDA"},
		{"SF:a\nFN:1,f\nend_of_record\n", "line 2: unknown record"},
		{"SF:a\nLF:x\nend_of_record\n", "line 2: malformed LF"},
		{"SF:a\nSF:b\n", "line 2: SF inside"},
		{"SF:\n", "line 1: empty SF"},
		{"SF:a\nDA:1,1\n", "has no end_of_record"},
		{"SF:a\nend_of_record:x\n", "line 2: malformed end_of_record"},
	} {
		_, err := Parse([]byte(tc.in))
		if err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("Parse(%q) = %v, want %q", tc.in, err, tc.err)
		}
	}
	r, err := Parse([]byte("TN:x\r\nSF:a\r\nDA:2,1\r\nDA:2,3\r\n" +
		"end_of_record\r\n\r\n"))
	if err != nil || string(Encode(r)) !=
		"TN:\nSF:a\nDA:2,4\nLF:1\nLH:1\nend_of_record\n" {
		t.Errorf("CRLF and duplicate lines: %v %q", err, Encode(r))
	}
}

// TestFileName: per-testbench names escape like trace files.
func TestFileName(t *testing.T) {
	for _, tc := range []struct{ tb, want string }{
		{"tb", ".gohard/coverage/tb-3.info"},
		{`\Foo\`, ".gohard/coverage/%5C%46oo%5C-3.info"},
	} {
		if got := FileName(tc.tb, 3); got != tc.want {
			t.Errorf("FileName(%q) = %q, want %q", tc.tb, got, tc.want)
		}
	}
}

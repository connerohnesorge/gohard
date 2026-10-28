package coverage

import (
	"bytes"
	"cmp"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Report is coverage in lcov terms: per source file the hit count of
// each line and the taken count of each branch. A Report from Encode,
// Parse or Merge is canonical: files sorted by path, lines by number,
// branches by line, block and branch, no duplicates.
type Report struct {
	Files []File
}

// File is the coverage of one source file (an lcov record).
type File struct {
	// Path is the project-relative slash path (SF).
	Path     string
	Lines    []Line
	Branches []Branch
}

// Line is the hit count of one line (DA).
type Line struct {
	Line  int
	Count int64
}

// Branch is one alternative of a decision (BRDA). Block numbers the
// decisions that start on Line in source order; Branch is the
// alternative.
type Branch struct {
	Line, Block, Branch int
	// Taken is the number of times the alternative was taken; NotRun
	// when its decision never ran ("-" in lcov).
	Taken int64
}

// NotRun is the Taken of a branch whose decision never ran.
const NotRun = -1

// Totals are the summary counts of a report.
type Totals struct {
	Lines, LinesHit       int
	Branches, BranchesHit int
}

// Totals counts the instrumented and hit lines and branches of r.
func (r Report) Totals() Totals {
	var t Totals
	for _, f := range r.Files {
		lf, lh, bf, bh := f.counts()
		t.Lines += lf
		t.LinesHit += lh
		t.Branches += bf
		t.BranchesHit += bh
	}
	return t
}

func (f File) counts() (lf, lh, bf, bh int) {
	for _, l := range f.Lines {
		lf++
		if l.Count > 0 {
			lh++
		}
	}
	for _, b := range f.Branches {
		bf++
		if b.Taken > 0 {
			bh++
		}
	}
	return lf, lh, bf, bh
}

// String is the summary line: "40 of 50 lines (80.0%), 3 of 4
// branches (75.0%)".
func (t Totals) String() string {
	return part(t.LinesHit, t.Lines, "lines") + ", " +
		part(t.BranchesHit, t.Branches, "branches")
}

func part(hit, all int, what string) string {
	s := strconv.Itoa(hit) + " of " + strconv.Itoa(all) + " " + what
	if all == 0 {
		return s
	}
	// Rounded down to a tenth, so 99.96% never shows as 100.0%.
	tenths := int64(hit) * 1000 / int64(all)
	return s + " (" + strconv.FormatInt(tenths/10, 10) + "." +
		strconv.FormatInt(tenths%10, 10) + "%)"
}

// Encode writes r in the lcov tracefile format genhtml reads: one
// record per file (TN, SF, BRDA, BRF, BRH, DA, LF, LH,
// end_of_record). The same report always gives the same bytes.
func Encode(r Report) []byte {
	var b bytes.Buffer
	for _, f := range canonical(r).Files {
		lf, lh, bf, bh := f.counts()
		b.WriteString("TN:\nSF:")
		b.WriteString(f.Path)
		b.WriteByte('\n')
		for _, x := range f.Branches {
			taken := "-"
			if x.Taken != NotRun {
				taken = strconv.FormatInt(x.Taken, 10)
			}
			b.WriteString("BRDA:" + strconv.Itoa(x.Line) + "," +
				strconv.Itoa(x.Block) + "," + strconv.Itoa(x.Branch) + "," +
				taken + "\n")
		}
		if bf > 0 {
			b.WriteString("BRF:" + strconv.Itoa(bf) + "\nBRH:" +
				strconv.Itoa(bh) + "\n")
		}
		for _, l := range f.Lines {
			b.WriteString("DA:" + strconv.Itoa(l.Line) + "," +
				strconv.FormatInt(l.Count, 10) + "\n")
		}
		b.WriteString("LF:" + strconv.Itoa(lf) + "\nLH:" +
			strconv.Itoa(lh) + "\nend_of_record\n")
	}
	return b.Bytes()
}

// maxLine bounds line numbers, block and branch numbers read from a
// file.
const maxLine = 1 << 30

// Parse reads an lcov tracefile of the records Encode writes. Test
// names are ignored and the LF, LH, BRF and BRH totals are recomputed;
// any other record, a malformed line or a record without
// end_of_record is an error naming the line. Two records of one file
// are merged.
func Parse(data []byte) (Report, error) {
	var out []File
	var cur *File
	text := string(data)
	n := 0
	for len(text) > 0 {
		n++
		line, rest, _ := strings.Cut(text, "\n")
		text = rest
		line = strings.TrimSuffix(line, "\r")
		fail := func(why string) (Report, error) {
			return Report{}, errors.New("line " + strconv.Itoa(n) + ": " +
				why)
		}
		if line == "" {
			continue
		}
		key, val, _ := strings.Cut(line, ":")
		if cur == nil && key != "TN" && key != "SF" {
			return fail("expected SF before " + strconv.Quote(line))
		}
		switch key {
		case "TN":
		case "SF":
			if cur != nil {
				return fail("SF inside the record of " + cur.Path)
			}
			if val == "" {
				return fail("empty SF")
			}
			out = append(out, File{Path: val})
			cur = &out[len(out)-1]
		case "DA":
			f, ok := ints(val, 2)
			if !ok || f[0] < 1 || f[0] > maxLine || f[1] < 0 {
				return fail("malformed DA " + strconv.Quote(val))
			}
			cur.Lines = append(cur.Lines, Line{Line: int(f[0]),
				Count: f[1]})
		case "BRDA":
			last := strings.LastIndexByte(val, ',')
			f, ok := ints(val[:max(last, 0)], 3)
			if !ok || f[0] < 1 || f[0] > maxLine || f[1] < 0 ||
				f[1] > maxLine || f[2] < 0 || f[2] > maxLine {
				return fail("malformed BRDA " + strconv.Quote(val))
			}
			taken := int64(NotRun)
			if t := val[last+1:]; t != "-" {
				x, err := strconv.ParseInt(t, 10, 64)
				if err != nil || x < 0 {
					return fail("malformed BRDA " + strconv.Quote(val))
				}
				taken = x
			}
			cur.Branches = append(cur.Branches, Branch{Line: int(f[0]),
				Block: int(f[1]), Branch: int(f[2]), Taken: taken})
		case "LF", "LH", "BRF", "BRH":
			if _, ok := ints(val, 1); !ok {
				return fail("malformed " + key + " " + strconv.Quote(val))
			}
		case "end_of_record":
			if line != "end_of_record" {
				return fail("malformed end_of_record")
			}
			cur = nil
		default:
			return fail("unknown record " + strconv.Quote(key))
		}
	}
	if cur != nil {
		return Report{}, errors.New("the record of " + cur.Path +
			" has no end_of_record")
	}
	return Merge(Report{Files: out}), nil
}

// ints parses exactly n comma-separated non-negative decimal
// integers.
func ints(s string, n int) ([]int64, bool) {
	parts := strings.Split(s, ",")
	if len(parts) != n {
		return nil, false
	}
	out := make([]int64, n)
	for i, p := range parts {
		x, err := strconv.ParseInt(p, 10, 64)
		if err != nil || x < 0 {
			return nil, false
		}
		out[i] = x
	}
	return out, true
}

// Merge sums reports: the union of their files, lines and branches,
// with the counts of a line or branch present in several added up (a
// branch not run in one report takes the other's count; saturating
// at the largest int64). The result does not depend on the order of
// rs or of anything inside them.
func Merge(rs ...Report) Report {
	var all Report
	for _, r := range rs {
		all.Files = append(all.Files, r.Files...)
	}
	return canonical(all)
}

// canonical sorts r and sums its duplicate files, lines and branches.
func canonical(r Report) Report {
	files := slices.Clone(r.Files)
	slices.SortStableFunc(files, func(a, b File) int {
		return strings.Compare(a.Path, b.Path)
	})
	var out Report
	for _, f := range files {
		if n := len(out.Files); n > 0 && out.Files[n-1].Path == f.Path {
			g := &out.Files[n-1]
			g.Lines = append(g.Lines, f.Lines...)
			g.Branches = append(g.Branches, f.Branches...)
			continue
		}
		out.Files = append(out.Files, File{Path: f.Path,
			Lines:    slices.Clone(f.Lines),
			Branches: slices.Clone(f.Branches)})
	}
	for i := range out.Files {
		f := &out.Files[i]
		f.Lines = sumLines(f.Lines)
		f.Branches = sumBranches(f.Branches)
	}
	return out
}

func sumLines(ls []Line) []Line {
	slices.SortFunc(ls, func(a, b Line) int {
		return cmp.Compare(a.Line, b.Line)
	})
	var out []Line
	for _, l := range ls {
		if n := len(out); n > 0 && out[n-1].Line == l.Line {
			out[n-1].Count = add(out[n-1].Count, l.Count)
			continue
		}
		out = append(out, l)
	}
	return out
}

func sumBranches(bs []Branch) []Branch {
	slices.SortFunc(bs, func(a, b Branch) int {
		return cmp.Or(cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Block, b.Block), cmp.Compare(a.Branch, b.Branch))
	})
	var out []Branch
	for _, b := range bs {
		n := len(out)
		if n == 0 || out[n-1].Line != b.Line || out[n-1].Block != b.Block ||
			out[n-1].Branch != b.Branch {
			out = append(out, b)
			continue
		}
		p := &out[n-1]
		switch {
		case b.Taken == NotRun:
		case p.Taken == NotRun:
			p.Taken = b.Taken
		default:
			p.Taken = add(p.Taken, b.Taken)
		}
	}
	return out
}

// add is a + b for counts, saturating at the largest int64.
func add(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

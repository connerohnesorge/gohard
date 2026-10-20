package corpus

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The trace oracle (#8, #36): GHDL and NVC dump a run as VCD; the
// dump is converted to the settled-value form of oracle.<tool>.txt,
// with every signal named by its GoHard hierarchical path. A VCD
// keeps only the value each signal settles on at each time, so the
// comparison with GoHard is on the settled projection of the native
// trace (trace.Settle).

// Class is how an oracle signal's values are written.
type Class string

// Oracle signal classes.
const (
	// ClassLogic values are one character per element (std_logic,
	// bit, boolean as 0/1, vectors left to right), upper case.
	ClassLogic Class = "logic"
	// ClassInteger values are signed decimal integers.
	ClassInteger Class = "integer"
	// ClassReal values are reals in Go's shortest 'g' form.
	ClassReal Class = "real"
)

// OracleSignal is one dumped signal.
type OracleSignal struct {
	Path  string
	Class Class
	Width int
}

// OracleChange is the value Signal settles on at Time.
type OracleChange struct {
	Time  uint64
	Path  string
	Value string
}

// Oracle is one tool's settled-value trace of a design.
type Oracle struct {
	Tool, Version string
	// Sources is the hash of the analysed sources (stale check).
	Sources   string
	Testbench string
	Signals   []OracleSignal
	// Changes are the first OracleLines changes; Total and Digest
	// cover all of them (a bounded run such as budget_hang has
	// hundreds of thousands, too many to keep as text).
	Changes []OracleChange
	Total   int
	Digest  string
}

// OracleLines is the number of change lines an oracle file keeps.
const OracleLines = 4096

// digest returns the count and hash of the change text of cs.
func digest(cs []OracleChange) (int, string) {
	h := sha256.Sum256(changeText(cs))
	return len(cs), hex.EncodeToString(h[:])
}

// limit keeps the first OracleLines changes of o and sets the digest.
func (o *Oracle) limit() {
	o.Total, o.Digest = digest(o.Changes)
	if len(o.Changes) > OracleLines {
		o.Changes = o.Changes[:OracleLines]
	}
}

const oracleMagic = "gohard-oracle 1"

// Encode writes the canonical oracle text: header, signals sorted by
// path, changes sorted by time then path.
func (o *Oracle) Encode() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s\ntool %s %s\nsources %s\ntestbench %s\n",
		oracleMagic, o.Tool, o.Version, o.Sources, o.Testbench)
	for _, s := range o.Signals {
		fmt.Fprintf(&b, "signal %s %s %d\n", s.Path, s.Class, s.Width)
	}
	b.Write(changeText(o.Changes))
	fmt.Fprintf(&b, "digest %d %s\n", o.Total, o.Digest)
	return b.Bytes()
}

// DecodeOracle reads oracle text written by Encode.
func DecodeOracle(data []byte) (*Oracle, error) {
	o := &Oracle{}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) < 4 || lines[0] != oracleMagic {
		return nil, fmt.Errorf("oracle: not a %q file", oracleMagic)
	}
	head := []struct {
		key string
		dst []*string
	}{
		{"tool", []*string{&o.Tool, &o.Version}},
		{"sources", []*string{&o.Sources}},
		{"testbench", []*string{&o.Testbench}},
	}
	for i, h := range head {
		f := strings.Fields(lines[i+1])
		if len(f) != len(h.dst)+1 || f[0] != h.key {
			return nil, fmt.Errorf("oracle: line %d: want %s", i+2, h.key)
		}
		for j, d := range h.dst {
			*d = f[j+1]
		}
	}
	last := strings.Fields(lines[len(lines)-1])
	if len(last) != 3 || last[0] != "digest" {
		return nil, fmt.Errorf("oracle: no digest line")
	}
	n, err := strconv.Atoi(last[1])
	if err != nil {
		return nil, fmt.Errorf("oracle: bad digest line")
	}
	o.Total, o.Digest = n, last[2]
	for n, l := range lines[4 : len(lines)-1] {
		f := strings.Fields(l)
		bad := fmt.Errorf("oracle: line %d: %q", n+5, l)
		if len(f) == 4 && f[0] == "signal" {
			w, err := strconv.Atoi(f[3])
			if err != nil || len(o.Changes) > 0 {
				return nil, bad
			}
			o.Signals = append(o.Signals, OracleSignal{Path: f[1],
				Class: Class(f[2]), Width: w})
			continue
		}
		if len(f) != 3 {
			return nil, bad
		}
		t, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			return nil, bad
		}
		o.Changes = append(o.Changes, OracleChange{Time: t, Path: f[1],
			Value: f[2]})
	}
	return o, nil
}

// Signal returns the dumped signal at path.
func (o *Oracle) Signal(path string) (OracleSignal, bool) {
	i, ok := slices.BinarySearchFunc(o.Signals, path,
		func(s OracleSignal, p string) int {
			return strings.Compare(s.Path, p)
		})
	if !ok {
		return OracleSignal{}, false
	}
	return o.Signals[i], true
}

// vcdVar is one $var of a dump.
type vcdVar struct {
	id    string
	path  string
	class Class
	width int
}

// ParseVCD converts a GHDL or NVC VCD dump of testbench tb into the
// settled-value form. Only the variables under the top scope named tb
// (case-insensitive) are kept; package scopes are dropped. Names are
// lower-cased, a trailing [l:r] range is removed and scopes join with
// '.', which gives GoHard's hierarchical paths. Several variables may
// share one VCD id (a port and its actual).
func ParseVCD(data []byte, tb string) (*Oracle, error) {
	tb = strings.ToLower(tb)
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	sc.Split(bufio.ScanWords)
	var scopes []string
	byID := map[string][]int{}
	var vars []vcdVar
	cur := map[int]string{}
	type pend struct {
		v   int
		val string
	}
	var at uint64
	var out []OracleChange
	var step []pend
	flush := func() {
		last := map[int]string{}
		var order []int
		for _, p := range step {
			if _, ok := last[p.v]; !ok {
				order = append(order, p.v)
			}
			last[p.v] = p.val
		}
		for _, v := range order {
			if old, ok := cur[v]; ok && old == last[v] {
				continue
			}
			cur[v] = last[v]
			out = append(out, OracleChange{Time: at, Path: vars[v].path,
				Value: last[v]})
		}
		step = step[:0]
	}
	words := func(stop string) ([]string, error) {
		var ws []string
		for sc.Scan() {
			if sc.Text() == stop {
				return ws, nil
			}
			ws = append(ws, sc.Text())
		}
		return nil, fmt.Errorf("vcd: missing %s", stop)
	}
	set := func(id, raw string) error {
		for _, v := range byID[id] {
			val, err := vcdValue(vars[v], raw)
			if err != nil {
				return err
			}
			step = append(step, pend{v, val})
		}
		return nil
	}
	defs := true
	for sc.Scan() {
		w := sc.Text()
		switch {
		case w == "$scope":
			ws, err := words("$end")
			if err != nil || len(ws) < 2 {
				return nil, fmt.Errorf("vcd: bad $scope")
			}
			scopes = append(scopes, strings.ToLower(ws[1]))
		case w == "$upscope":
			if _, err := words("$end"); err != nil || len(scopes) == 0 {
				return nil, fmt.Errorf("vcd: bad $upscope")
			}
			scopes = scopes[:len(scopes)-1]
		case w == "$var":
			ws, err := words("$end")
			if err != nil || len(ws) < 4 {
				return nil, fmt.Errorf("vcd: bad $var")
			}
			if len(scopes) == 0 || scopes[0] != tb {
				continue
			}
			width, err := strconv.Atoi(ws[1])
			if err != nil {
				return nil, fmt.Errorf("vcd: bad $var width %q", ws[1])
			}
			if width == 0 {
				// A type the tool cannot dump (NVC: strings,
				// booleans in records); never compared.
				continue
			}
			name := strings.ToLower(ws[3])
			if i := strings.IndexByte(name, '['); i > 0 {
				name = name[:i]
			}
			cl := ClassLogic
			switch ws[0] {
			case "integer":
				cl = ClassInteger
			case "real":
				cl = ClassReal
			}
			p := strings.Join(append(slices.Clone(scopes), name), ".")
			byID[ws[2]] = append(byID[ws[2]], len(vars))
			vars = append(vars, vcdVar{id: ws[2], path: p, class: cl,
				width: width})
		case w == "$enddefinitions":
			if _, err := words("$end"); err != nil {
				return nil, err
			}
			defs = false
		case defs || w == "$dumpvars" || w == "$end":
		case strings.HasPrefix(w, "$"):
			if _, err := words("$end"); err != nil {
				return nil, err
			}
		case w[0] == '#':
			t, err := strconv.ParseUint(w[1:], 10, 64)
			if err != nil || t < at {
				return nil, fmt.Errorf("vcd: bad time %q", w)
			}
			flush()
			at = t
		case w[0] == 'b' || w[0] == 'B' || w[0] == 'r' || w[0] == 'R':
			if !sc.Scan() {
				return nil, fmt.Errorf("vcd: value %q without id", w)
			}
			if err := set(sc.Text(), w); err != nil {
				return nil, err
			}
		default:
			if err := set(w[1:], w[:1]); err != nil {
				return nil, err
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()
	o := &Oracle{Testbench: tb}
	seen := map[string]bool{}
	for _, v := range vars {
		if seen[v.path] {
			continue
		}
		seen[v.path] = true
		o.Signals = append(o.Signals, OracleSignal{Path: v.path,
			Class: v.class, Width: v.width})
	}
	slices.SortFunc(o.Signals, func(a, b OracleSignal) int {
		return strings.Compare(a.Path, b.Path)
	})
	o.Changes = sortChanges(out)
	o.limit()
	return o, nil
}

func sortChanges(cs []OracleChange) []OracleChange {
	slices.SortStableFunc(cs, func(a, b OracleChange) int {
		if a.Time != b.Time {
			if a.Time < b.Time {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	return cs
}

// vcdValue normalises one VCD value of v: logic characters upper
// case and padded on the left to the width as VCD extends (0 and 1
// with 0, x and z with themselves), integers in decimal.
func vcdValue(v vcdVar, raw string) (string, error) {
	switch raw[0] {
	case 'r', 'R':
		f, err := strconv.ParseFloat(raw[1:], 64)
		if err != nil {
			return "", fmt.Errorf("vcd: bad real %q", raw)
		}
		return strconv.FormatFloat(f, 'g', -1, 64), nil
	case 'b', 'B':
		raw = raw[1:]
	}
	s := strings.ToUpper(raw)
	if v.class == ClassInteger {
		n, err := strconv.ParseUint(s, 2, 64)
		if err != nil {
			return "", fmt.Errorf("vcd: bad integer %q", raw)
		}
		if v.width > 0 && v.width <= 64 && len(s) == v.width &&
			n>>(v.width-1)&1 == 1 {
			// Two's complement over the dumped width.
			x := int64(n)
			if v.width < 64 {
				x -= int64(1) << v.width
			}
			return strconv.FormatInt(x, 10), nil
		}
		return strconv.FormatUint(n, 10), nil
	}
	if len(s) < v.width {
		pad := "0"
		if s[0] == 'X' || s[0] == 'Z' {
			pad = s[:1]
		}
		s = strings.Repeat(pad, v.width-len(s)) + s
	}
	return s, nil
}

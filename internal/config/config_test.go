package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/connerohnesorge/gohard/v2/internal/diag"
	"github.com/connerohnesorge/gohard/v2/internal/env"
)

// TestKeyTable checks the table's own invariants.
func TestKeyTable(t *testing.T) {
	var names []string
	for _, k := range keys {
		if slices.Contains(names, k.Name) {
			t.Errorf("duplicate key %s", k.Name)
		}
		names = append(names, k.Name)
		if isGroup(k.Name) {
			t.Errorf("%s is both a key and a group", k.Name)
		}
		if k.Doc == "" || k.TypeDoc == "" || k.Default == "" {
			t.Errorf("%s lacks docs or a default", k.Name)
		}
		if k.acc.decode == nil {
			t.Errorf("%s has no decoder", k.Name)
		}
		if k.Type == TypeEnum && !slices.Contains(k.Enum,
			strings.Trim(k.Default, `"`)) {
			t.Errorf("%s: default %s not in enum", k.Name, k.Default)
		}
	}
	for _, g := range Groups() {
		if GroupDoc(g) == "" {
			t.Errorf("group %s has no description", g)
		}
	}
}

// TestDefaultsMatchTable: writing any key's documented default gives
// exactly one config/redundant-default warning and the same value as
// Defaults(), so the table and Defaults can never disagree.
func TestDefaultsMatchTable(t *testing.T) {
	def := Defaults()
	for _, k := range keys {
		c, ds := Parse(FileName, []byte(nest(k.Name, k.Default)))
		if len(ds) != 1 || ds[0].Code != diag.ConfigRedundantDefault {
			t.Errorf("%s: %v", k.Name, ds)
		}
		if !equalConfig(c, def) {
			t.Errorf("%s: default %s decodes to %s, Defaults() has %s",
				k.Name, k.Default, show(c.Value(k.Name)),
				show(def.Value(k.Name)))
		}
		if !c.IsSet(k.Name) || len(c.SetKeys()) != 1 {
			t.Errorf("%s: set keys %v", k.Name, c.SetKeys())
		}
	}
}

// TestGeneratedFilesUpToDate is the staleness check of #28/#30: the
// committed schema and docs equal what the table generates.
func TestGeneratedFilesUpToDate(t *testing.T) {
	for path, want := range map[string][]byte{
		"../../schema/gohard.schema.json": Schema(),
		"../../docs/config.md":            Markdown(),
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale; run go generate ./internal/config",
				path)
		}
	}
}

// TestSchemaShape checks the schema is valid JSON, declares 2020-12,
// and lists exactly the table's keys.
func TestSchemaShape(t *testing.T) {
	s := string(Schema())
	if !strings.Contains(s, `"$schema": "https://json-schema.org/draft/`+
		`2020-12/schema"`) {
		t.Error("schema does not declare 2020-12")
	}
	var v any
	if err := json.Unmarshal(Schema(), &v); err != nil {
		t.Fatal(err)
	}
	var got []string
	var walk func(prefix string, o map[string]any)
	walk = func(prefix string, o map[string]any) {
		if o["additionalProperties"] != false {
			t.Errorf("%q allows additional properties", prefix)
		}
		props := o["properties"].(map[string]any)
		for name, p := range props {
			p := p.(map[string]any)
			full := join(prefix, name)
			if isGroup(full) {
				walk(full, p)
				continue
			}
			got = append(got, full)
		}
	}
	walk("", v.(map[string]any))
	slices.Sort(got)
	var want []string
	for _, k := range keys {
		want = append(want, k.Name)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("schema keys %v, want %v", got, want)
	}
}

// genString returns a random short string over an alphabet that hits
// the edges of identifiers, unit names and time literals.
func genString(r *env.SplitMix64, alphabet string) string {
	n := env.Intn(r, 12)
	var b strings.Builder
	for range n {
		b.WriteByte(alphabet[env.Intn(r, len(alphabet))])
	}
	return b.String()
}

// TestPatternsMatchValidators checks the schema's regular expressions
// against the Go validators on generated strings.
func TestPatternsMatchValidators(t *testing.T) {
	type pair struct {
		name     string
		re       *regexp.Regexp
		ok       func(string) bool
		alphabet string
	}
	timeOK := func(s string) bool { _, e := parseTime(s); return e != 1 }
	pairs := []pair{
		{"identifier", regexp.MustCompile(identPattern), IsIdentifier,
			"aZ9_._"},
		{"unit", regexp.MustCompile(unitPattern), IsUnitName, "aZ9_.."},
		{"var", regexp.MustCompile(varPattern), isVarName, "a=\x00 "},
		{"time", regexp.MustCompile(timePattern()), timeOK,
			"019_.eE+- fmsnupcSMh"},
		{"builtin", regexp.MustCompile(builtinPattern()), IsBuiltin,
			"_"},
	}
	r := env.SplitMix64{State: 7}
	for _, p := range pairs {
		inputs := []string{"", "a", "a_b", "a__b", "1 ms", "1.5 NS",
			"1e3 fs", "1ms", "lib.unit", "TOOL_NAME", "tool_name"}
		for range 20000 {
			inputs = append(inputs, genString(&r, p.alphabet))
		}
		if p.name == "time" {
			for range 20000 {
				inputs = append(inputs, genString(&r, "0123456789_.e")+
					" "+[]string{"fs", "ps", "Ns", "us", "MS", "sec",
					"min", "hr", "s"}[env.Intn(&r, 9)])
			}
		}
		if p.name == "builtin" {
			for _, b := range Builtins {
				inputs = append(inputs, b, strings.ToLower(b), b+"_")
			}
		}
		for _, s := range inputs {
			if p.re.MatchString(s) != p.ok(s) {
				t.Errorf("%s: %q: pattern %v, validator %v", p.name, s,
					p.re.MatchString(s), p.ok(s))
			}
		}
	}
}

// TestTimeRoundTrip: the canonical form of any time parses back to the
// same value, and scaling by a unit never changes the value.
func TestTimeRoundTrip(t *testing.T) {
	r := env.SplitMix64{State: 3}
	edge := []Time{0, 1, 999, 1000, Millisecond, Hour, 1<<63 - 1}
	for range 5000 {
		edge = append(edge, Time(r.Uint64()>>1),
			Time(r.Uint64()>>40)*Nanosecond)
	}
	for _, v := range edge {
		got, ok := ParseTime(v.String())
		if !ok || got != v {
			t.Fatalf("ParseTime(%q) = %d, %v; want %d", v.String(), got,
				ok, v)
		}
	}
	for _, u := range timeUnits {
		for _, n := range []int64{0, 1, 7, 1000} {
			if n > int64((1<<63-1)/u.size) {
				continue
			}
			s := strconv.FormatInt(n, 10) + " " + strings.ToUpper(u.name)
			got, ok := ParseTime(s)
			if !ok || got != Time(n)*u.size {
				t.Errorf("ParseTime(%q) = %d, %v", s, got, ok)
			}
		}
	}
	for s, want := range map[string]Time{
		"1.5 ns": 1500 * Picosecond, "1e3 fs": 1000, "2.5E-3 ps": 3,
		"0.4 fs": 0, "0.5 fs": 1, "1_0 us": 10 * Microsecond,
		"9223372036854775807 fs": 1<<63 - 1, "1e-30 hr": 0,
	} {
		if got, ok := ParseTime(s); !ok || got != want {
			t.Errorf("ParseTime(%q) = %d, %v; want %d", s, got, ok, want)
		}
	}
	for _, s := range []string{"9223372036854775808 fs", "3000 hr",
		"1e25 fs", "1ms", "1  ", " 1 ms", "1. ms", ".5 ms", "1e ms",
		"1e+ ms", "1 sec ", "1 MS2", strings.Repeat("1", 101) + " fs"} {
		if _, ok := ParseTime(s); ok {
			t.Errorf("ParseTime(%q) accepted", s)
		}
	}
}

// TestValidConfigsRoundTrip generates random valid configurations,
// renders them as YAML in random key order and flow or block style,
// and checks that parsing gives no error, sets exactly the generated
// keys, warns exactly for the values equal to the default, and decodes
// every value.
func TestValidConfigsRoundTrip(t *testing.T) {
	r := env.SplitMix64{State: 11}
	defaults := Defaults()
	for iter := range 2000 {
		var picked []Key
		for _, k := range keys {
			if env.Intn(&r, 3) == 0 {
				picked = append(picked, k)
			}
		}
		perm := env.Shuffle(r.Uint64(), 0, len(picked))
		want := map[string]string{}
		vals := map[string]string{}
		var order, redundant []string
		for _, i := range perm {
			k := picked[i]
			text, shown := genValue(&r, k)
			want[k.Name], vals[k.Name] = shown, text
			order = append(order, k.Name)
			if shown == plain(defaults.Value(k.Name)) {
				redundant = append(redundant, k.Name)
			}
		}
		yaml := renderKeys(order, vals)
		c, ds := Parse(FileName, []byte(yaml))
		var warned []string
		for _, d := range ds {
			if d.Code != diag.ConfigRedundantDefault {
				t.Fatalf("iter %d: unexpected %s: %s\n%s", iter, d.Code,
					d.Message, yaml)
			}
			warned = append(warned, strings.Fields(d.Message)[0])
		}
		slices.Sort(warned)
		slices.Sort(redundant)
		if !slices.Equal(warned, redundant) {
			t.Fatalf("iter %d: warned %v, want %v\n%s", iter, warned,
				redundant, yaml)
		}
		for _, k := range picked {
			if !c.IsSet(k.Name) {
				t.Fatalf("iter %d: %s not set\n%s", iter, k.Name, yaml)
			}
			if got := plain(c.Value(k.Name)); got != want[k.Name] {
				t.Fatalf("iter %d: %s = %s, want %s\n%s", iter, k.Name,
					got, want[k.Name], yaml)
			}
		}
		if len(c.SetKeys()) != len(picked) {
			t.Fatalf("iter %d: set %v", iter, c.SetKeys())
		}
	}
}

// plain is show without positions.
func plain(v any) string {
	return posRE.ReplaceAllString(show(v), "")
}

var posRE = regexp.MustCompile(`@\d+:\d+`)

// renderKeys writes the dotted keys in order with their YAML values,
// each group once, its keys in order of first appearance.
func renderKeys(order []string, vals map[string]string) string {
	var b strings.Builder
	var write func(prefix string, depth int)
	write = func(prefix string, depth int) {
		var done []string
		for _, name := range order {
			rest, ok := strings.CutPrefix(name, prefix)
			if !ok {
				continue
			}
			head, _, nested := strings.Cut(rest, ".")
			if slices.Contains(done, head) {
				continue
			}
			done = append(done, head)
			ind := strings.Repeat("  ", depth)
			if nested {
				b.WriteString(ind + head + ":\n")
				write(prefix+head+".", depth+1)
				continue
			}
			b.WriteString(ind + head + ": " + vals[name] + "\n")
		}
	}
	write("", 0)
	return b.String()
}

// genValue returns a valid YAML value for k and the plain form of the
// value it decodes to.
func genValue(r *env.SplitMix64, k Key) (text, shown string) {
	if env.Intn(r, 6) == 0 {
		c, _ := Parse(FileName, []byte(nest(k.Name, k.Default)))
		return k.Default, plain(c.Value(k.Name))
	}
	pick := func(xs ...string) string { return xs[env.Intn(r, len(xs))] }
	ident := func() string {
		return pick("a", "tb", "Lib_1", "x9", "UVVM", "ieee_proposed")
	}
	switch k.Type {
	case TypeBool:
		v := pick("true", "false")
		return v, v
	case TypeInt:
		n := k.Min + int64(env.Intn(r, 1000))
		if env.Intn(r, 10) == 0 {
			n = 1<<63 - 1
		}
		s := strconv.FormatInt(n, 10)
		return s, s
	case TypeUint64:
		n := r.Uint64() >> (env.Intn(r, 64))
		s := strconv.FormatUint(n, 10)
		return s, s
	case TypeEnum:
		v := k.Enum[env.Intn(r, len(k.Enum))]
		q := strconv.Quote(v)
		if k.Name == "standard" || env.Intn(r, 2) == 0 {
			return q, q
		}
		return v, q
	case TypeTime:
		n := int64(env.Intn(r, 100000))
		u := timeUnits[env.Intn(r, len(timeUnits))]
		if Time(n)*u.size/u.size != Time(n) {
			n = 1
		}
		s := strconv.FormatInt(n, 10) + " " + u.name
		return pick(s, strconv.Quote(s)), (Time(n) * u.size).String()
	case TypeTimestamp:
		y := 1970 + env.Intn(r, 100)
		s := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02dZ", y,
			1+env.Intn(r, 12), 1+env.Intn(r, 28), env.Intn(r, 24),
			env.Intn(r, 60), env.Intn(r, 60))
		return pick(s, strconv.Quote(s)), s
	case TypeList:
		n := 1 + env.Intn(r, 3)
		var items, shownItems []string
		for range n {
			var v string
			switch k.Items {
			case ItemUnit:
				v = ident() + "." + ident()
			case ItemIdentifier:
				v = ident()
			default:
				v = pick("tb.dut", "x", "a/b c", "yes!", "1")
			}
			items = append(items, strconv.Quote(v))
			shownItems = append(shownItems, strconv.Quote(v))
		}
		return "[" + strings.Join(items, ", ") + "]",
			"[" + strings.Join(shownItems, ", ") + "]"
	case TypeMap:
		var used []string
		var lines []string
		for range 1 + env.Intn(r, 3) {
			var key, val string
			switch k.MapKeys {
			case ItemUnit:
				key = ident() + "." + ident()
			case ItemVarName:
				key = pick("HOME", "PATH", "x y", "ü")
			default:
				key = ident()
			}
			if slices.ContainsFunc(used, func(u string) bool {
				return strings.EqualFold(u, key)
			}) {
				continue
			}
			used = append(used, key)
			if k.Items == ItemFolders {
				val = "[" + strconv.Quote(pick("rtl", "src/a")) + "]"
			} else {
				val = strconv.Quote(pick("v", "a b", "yes", "1"))
			}
			lines = append(lines, strconv.Quote(key)+": "+val)
		}
		text = "{" + strings.Join(lines, ", ") + "}"
		got, ds := Parse(FileName, []byte(nest(k.Name, text)))
		if len(ds) != 0 {
			panic(fmt.Sprint(ds))
		}
		return text, plain(got.Value(k.Name))
	}
	panic("unreachable")
}

// TestParseTimestamp: RFC 3339 section 5.6 allows "t" and "z" in lower
// case; every accepted spelling means the same instant, and the
// schema's pattern accepts what the loader accepts.
func TestParseTimestamp(t *testing.T) {
	want := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	re := regexp.MustCompile("[Zz]$")
	for _, s := range []string{"2000-01-01T00:00:00Z",
		"2000-01-01t00:00:00z", "2000-01-01T00:00:00z",
		"2000-01-01t00:00:00Z", "2000-01-01T00:00:00.000Z"} {
		got, ok := ParseTimestamp(s)
		if !ok || !got.Equal(want) {
			t.Errorf("ParseTimestamp(%q) = %v, %v", s, got, ok)
		}
		if !re.MatchString(s) {
			t.Errorf("schema pattern rejects %q", s)
		}
	}
	for _, s := range []string{"", "z", "2000-01-01T00:00:00",
		"2000-01-01T00:00:00+01:00", "2000-01-01 00:00:00Z",
		"2000-01-01x00:00:00z", "2000-13-01T00:00:00Z"} {
		if _, ok := ParseTimestamp(s); ok {
			t.Errorf("ParseTimestamp(%q) accepted", s)
		}
	}
}

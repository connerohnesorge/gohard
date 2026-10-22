package config

import (
	"math/big"
	"strconv"
	"strings"
	"time"
)

// Time is a VHDL time value in femtoseconds, the resolution of the
// simulator (#35).
type Time int64

// VHDL time units.
const (
	Femtosecond Time = 1
	Picosecond       = 1000 * Femtosecond
	Nanosecond       = 1000 * Picosecond
	Microsecond      = 1000 * Nanosecond
	Millisecond      = 1000 * Microsecond
	Second           = 1000 * Millisecond
	Minute           = 60 * Second
	Hour             = 60 * Minute
)

// timeUnits are the units of STD.STANDARD.TIME, largest first.
var timeUnits = []struct {
	name string
	size Time
}{
	{"hr", Hour}, {"min", Minute}, {"sec", Second}, {"ms", Millisecond},
	{"us", Microsecond}, {"ns", Nanosecond}, {"ps", Picosecond},
	{"fs", Femtosecond},
}

// String returns the canonical VHDL form: an integer in the largest unit
// that divides the value exactly, such as "1 ms".
func (t Time) String() string {
	for _, u := range timeUnits {
		if t%u.size == 0 && t != 0 {
			return strconv.FormatInt(int64(t/u.size), 10) + " " + u.name
		}
	}
	return strconv.FormatInt(int64(t), 10) + " fs"
}

// timeError says why a time string was rejected.
type timeError uint8

const (
	timeOK timeError = iota
	timeMalformed
	timeOverflow
)

// ParseTime parses a VHDL time string: a decimal literal (digits with
// single underscores between them, an optional fraction and exponent), at
// least one space, and a unit (fs, ps, ns, us, ms, sec, min or hr; any
// case). The value is rounded to the nearest femtosecond. ok is false for
// a malformed string or a value above the TIME range.
func ParseTime(s string) (Time, bool) {
	t, e := parseTime(s)
	return t, e == timeOK
}

func parseTime(s string) (Time, timeError) {
	num, unit, ok := strings.Cut(s, " ")
	if !ok {
		return 0, timeMalformed
	}
	unit = strings.TrimLeft(unit, " ")
	var size Time
	for _, u := range timeUnits {
		if strings.EqualFold(unit, u.name) {
			size = u.size
		}
	}
	if size == 0 {
		return 0, timeMalformed
	}
	r, ok := decimalLiteral(num)
	if !ok {
		return 0, timeMalformed
	}
	r.Mul(r, new(big.Rat).SetInt64(int64(size)))
	// Round half up (the value is never negative).
	q, m := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if new(big.Int).Lsh(m, 1).Cmp(r.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, timeOverflow
	}
	return Time(q.Int64()), timeOK
}

// maxDigits bounds the digits of a time literal so hostile input cannot
// make the exact arithmetic expensive.
const maxDigits = 100

// decimalLiteral parses a VHDL decimal literal without a sign. Values of
// 10^20 or more are returned as 10^20 (they overflow TIME in any unit)
// and values below 10^-20 as 0 (they round to 0 fs in any unit).
func decimalLiteral(s string) (*big.Rat, bool) {
	mant, exp, hasExp := strings.Cut(strings.ToLower(s), "e")
	intPart, frac, hasFrac := strings.Cut(mant, ".")
	digits, ok := vhdlDigits(intPart)
	if !ok {
		return nil, false
	}
	p := 0
	if hasFrac {
		f, ok := vhdlDigits(frac)
		if !ok {
			return nil, false
		}
		digits += f
		p = -len(f)
	}
	if hasExp {
		sign := 1
		switch {
		case strings.HasPrefix(exp, "+"):
			exp = exp[1:]
		case strings.HasPrefix(exp, "-"):
			sign, exp = -1, exp[1:]
		}
		d, ok := vhdlDigits(exp)
		if !ok {
			return nil, false
		}
		d = strings.TrimLeft(d, "0")
		n := 10_000_000 // far beyond either bound below
		if len(d) <= 7 {
			n, _ = strconv.Atoi("0" + d)
		}
		p += sign * n
	}
	if len(digits) > maxDigits {
		return nil, false
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return new(big.Rat), true
	}
	// The value lies in [10^(m-1), 10^m).
	switch m := len(digits) + p; {
	case m > 21:
		return new(big.Rat).SetInt(pow10(20)), true
	case m < -20:
		return new(big.Rat), true
	}
	n, _ := new(big.Int).SetString(digits, 10)
	r := new(big.Rat).SetInt(n)
	if p >= 0 {
		return r.Mul(r, new(big.Rat).SetInt(pow10(p))), true
	}
	return r.Quo(r, new(big.Rat).SetInt(pow10(-p))), true
}

func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// vhdlDigits checks digit { [underline] digit } and returns the digits
// without underscores.
func vhdlDigits(s string) (string, bool) {
	if s == "" || s[0] == '_' || s[len(s)-1] == '_' ||
		strings.Contains(s, "__") {
		return "", false
	}
	var b strings.Builder
	for i := range len(s) {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			b.WriteByte(c)
		case c == '_':
		default:
			return "", false
		}
	}
	return b.String(), true
}

// ParseTimestamp parses an RFC 3339 time in UTC (ending in "Z" or "z").
func ParseTimestamp(s string) (time.Time, bool) {
	// RFC 3339 section 5.6 allows a lower-case "t" and "z"; Go's parser
	// takes only upper case, so normalise those two bytes first.
	if !strings.HasSuffix(s, "Z") && !strings.HasSuffix(s, "z") {
		return time.Time{}, false
	}
	b := []byte(s)
	b[len(b)-1] = 'Z'
	if len(b) > 10 && b[10] == 't' {
		b[10] = 'T'
	}
	t, err := time.Parse(time.RFC3339Nano, string(b))
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// FormatTimestamp is the RFC 3339 UTC text of t that ParseTimestamp
// reads back: the form simulation.env.epoch is recorded in.
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// IsIdentifier reports whether s is a VHDL basic identifier: a letter,
// then letters, digits and single underscores, not ending in one.
func IsIdentifier(s string) bool {
	if s == "" || !isLetter(s[0]) || s[len(s)-1] == '_' ||
		strings.Contains(s, "__") {
		return false
	}
	for i := range len(s) {
		if c := s[i]; !isLetter(c) && !(c >= '0' && c <= '9') &&
			c != '_' {
			return false
		}
	}
	return true
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// IsUnitName reports whether s is lib.unit: two identifiers joined by a
// dot.
func IsUnitName(s string) bool {
	lib, unit, ok := strings.Cut(s, ".")
	return ok && IsIdentifier(lib) && IsIdentifier(unit)
}

// yaml11Bools are every YAML 1.1 boolean spelling (the "Norway problem").
var yaml11Bools = []string{
	"y", "Y", "yes", "Yes", "YES", "n", "N", "no", "No", "NO",
	"true", "True", "TRUE", "false", "False", "FALSE",
	"on", "On", "ON", "off", "Off", "OFF",
}

// YAML11Bools returns every YAML 1.1 boolean spelling. Only true and
// false are accepted in bool keys, and an unquoted spelling in a string
// key is an error.
func YAML11Bools() []string {
	return append([]string(nil), yaml11Bools...)
}

func isYAML11Bool(s string) bool {
	for _, b := range yaml11Bools {
		if s == b {
			return true
		}
	}
	return false
}

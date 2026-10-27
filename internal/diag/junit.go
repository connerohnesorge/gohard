package diag

import (
	"bytes"
	"cmp"
	"slices"
	"strconv"
	"strings"
)

// JUnit XML output (#61), valid against the Jenkins junit-10.xsd
// schema (a copy is in internal/diag/junittest/junit-10.xsd).
//
// The root <testsuites> holds one <testsuite> per run. Its test cases
// are, in order: one per checked file and per other path with a
// diagnostic (classname gohard.check, named by the path; diagnostics
// without a path go to the case named "gohard"), then one per
// testbench run (classname gohard.sim, named by the testbench). Each
// diagnostic of a case is classified by the exit rules: one that
// alone would exit 2 goes into the case's <error>, one that would exit
// 1 (an error, an unchecked unit, a warning under --strict) into its
// <failure>, and the rest into its <system-out>. A case has at most
// one <error> and one <failure>; their type and message are those of
// the most severe diagnostic of that kind (the first in sorted order
// among equals), and their text is the text form of every diagnostic
// of that kind, in sorted order.
//
// junit-10.xsd allows no properties on a test case, so a testbench's
// seed, status, simulated end time and repro path are suite
// properties named "<testbench>.seed" and so on, and also the first
// lines of the case's <system-out>. There is no wall-clock time:
// every time attribute is 0.

// RenderJUnit renders r as JUnit XML. Output depends only on r (P1).
func RenderJUnit(r *Report) []byte {
	ds := Normalize(r.Diagnostics)
	tbs := slices.Clone(r.Testbenches)
	slices.SortFunc(tbs, func(a, b Testbench) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name),
			cmp.Compare(a.Seed, b.Seed))
	})
	var own []Diagnostic
	for _, tb := range tbs {
		own = append(own, tb.Diagnostics...)
	}
	own = Normalize(own)
	var static []Diagnostic
	paths := slices.Clone(r.Checked)
	for _, d := range ds {
		if _, found := slices.BinarySearchFunc(own, d,
			Compare); !found {
			static = append(static, d)
			paths = append(paths, d.Path)
		}
	}
	var cases []junitCase
	for _, p := range sortedUnique(paths) {
		c := junitCase{name: cmp.Or(p, "gohard"), class: "gohard.check"}
		for len(static) > 0 && static[0].Path == p {
			c.add(static[0], r.Strict)
			static = static[1:]
		}
		cases = append(cases, c)
	}
	var props [][2]string
	for _, tb := range tbs {
		c := junitCase{name: tb.Name, class: "gohard.sim"}
		facts := [][2]string{
			{"seed", strconv.FormatUint(tb.Seed, 10)},
			{"status", tb.Status},
			{"end", tb.End},
			{"repro", tb.Repro},
		}
		for _, f := range facts {
			if f[1] == "" {
				continue
			}
			props = append(props, [2]string{tb.Name + "." + f[0], f[1]})
			c.facts = append(c.facts, f[0]+": "+f[1])
		}
		for _, d := range Normalize(tb.Diagnostics) {
			c.add(d, r.Strict)
		}
		cases = append(cases, c)
	}
	tests, failures, errors := len(cases), 0, 0
	for _, c := range cases {
		switch {
		case len(c.errors) > 0:
			errors++
		case len(c.failures) > 0:
			failures++
		}
	}
	counts := ` tests="` + strconv.Itoa(tests) + `" failures="` +
		strconv.Itoa(failures) + `" errors="` + strconv.Itoa(errors) +
		`"`
	name := xmlAttr(cmp.Or(r.Tool, "gohard"))
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<testsuites name="` + name + `"` + counts +
		` time="0">` + "\n")
	b.WriteString(`  <testsuite name="` + name + `"` + counts +
		` skipped="0" time="0">` + "\n")
	if len(props) > 0 {
		b.WriteString("    <properties>\n")
		for _, p := range props {
			b.WriteString(`      <property name="` + xmlAttr(p[0]) +
				`" value="` + xmlAttr(p[1]) + `"/>` + "\n")
		}
		b.WriteString("    </properties>\n")
	}
	for _, c := range cases {
		c.write(&b)
	}
	b.WriteString("  </testsuite>\n</testsuites>\n")
	return b.Bytes()
}

// junitCase is one test case: its diagnostics by kind, and the facts
// that open its <system-out>.
type junitCase struct {
	name, class              string
	facts                    []string
	errors, failures, output []Diagnostic
}

// add files d under the kind its exit class gives it.
func (c *junitCase) add(d Diagnostic, strict bool) {
	switch ExitCode([]Diagnostic{d}, strict, NoSignal) {
	case ExitTool:
		c.errors = append(c.errors, d)
	case ExitProblems:
		c.failures = append(c.failures, d)
	default:
		c.output = append(c.output, d)
	}
}

func (c *junitCase) write(b *bytes.Buffer) {
	b.WriteString(`    <testcase name="` + xmlAttr(c.name) +
		`" classname="` + c.class + `" time="0"`)
	if len(c.errors)+len(c.failures)+len(c.output)+len(c.facts) == 0 {
		b.WriteString("/>\n")
		return
	}
	b.WriteString(">\n")
	writeJUnitResult(b, "error", c.errors)
	writeJUnitResult(b, "failure", c.failures)
	if len(c.output)+len(c.facts) > 0 {
		var text bytes.Buffer
		for _, f := range c.facts {
			text.WriteString(escapeText(f) + "\n")
		}
		for _, d := range c.output {
			writeText(&text, d)
		}
		b.WriteString("      <system-out>" + xmlText(text.String()) +
			"</system-out>\n")
	}
	b.WriteString("    </testcase>\n")
}

// writeJUnitResult writes one <error> or <failure> element for ds, if
// any: the type and message of the most severe, the text of all.
func writeJUnitResult(b *bytes.Buffer, elem string, ds []Diagnostic) {
	if len(ds) == 0 {
		return
	}
	var text bytes.Buffer
	top := ds[0]
	for _, d := range ds {
		writeText(&text, d)
		if d.Severity < top.Severity {
			top = d
		}
	}
	b.WriteString("      <" + elem + ` type="` + xmlAttr(string(top.Code)) +
		`" message="` + xmlAttr(top.Message) + `">` +
		xmlText(text.String()) + "</" + elem + ">\n")
}

// xmlText escapes the text of an element; the final line break is
// dropped so the closing tag ends the last line.
func xmlText(s string) string {
	return xmlEscape(strings.TrimSuffix(s, "\n"))
}

// xmlAttr escapes an attribute value. Control characters and line
// breaks are written as Go-style escapes first, like the text form,
// since an XML parser would turn them into spaces.
func xmlAttr(s string) string {
	return xmlEscape(escapeText(s))
}

// xmlEscape escapes the XML markup characters of s. A character XML 1.0
// does not allow (most control characters, U+FFFE, U+FFFF) and invalid
// UTF-8 become U+FFFD, so the document is always well-formed.
func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '"':
			b.WriteString("&quot;")
		case r == '\'':
			b.WriteString("&apos;")
		case r == '\t' || r == '\n':
			b.WriteRune(r)
		case r < 0x20 || r == 0xfffe || r == 0xffff:
			b.WriteRune(0xfffd)
		default:
			b.WriteRune(r) // invalid UTF-8 decodes as U+FFFD
		}
	}
	return b.String()
}

package corpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The conformance cases (#18 Done when): one VHDL file per case under
// ConformanceDir/<feature>/<case>.vhd with the exact gohard output in
// <case>.want and an optional NVC recording in <case>.nvc.txt. The
// format is described in testdata/conformance/README.md. This file
// holds the pure parts shared by the internal/cli test that runs the
// cases and the tools/oracle conformance section that records NVC.

// ConformanceDir is the repository-relative root of the cases.
const ConformanceDir = "testdata/conformance"

// DefaultConformanceStd is the standard of a case without a std:
// header: the cases are for the VHDL-2019 work of v1.1.
const DefaultConformanceStd = "2019"

// defaultConfigStd is the standard GoHard uses without a gohard.yaml.
const defaultConfigStd = "2008"

// conformanceStds are the standards a std: header may name.
var conformanceStds = []string{"1993", "2000", "2002", "2008", "2019"}

// ConformanceCase is one case, read from the header comments of its
// file: the leading lines that start with "--". A header line of the
// form "-- key: value" with one of the keys below is a directive; any
// other comment line is prose.
//
//	-- std: 2008              the standard (default 2019)
//	-- top: name              the testbench (default the last entity)
//	-- pending: construct     the construct is still unsupported
//	-- nvc-differs: reason    NVC's messages are not compared
type ConformanceCase struct {
	Feature string // the folder under ConformanceDir
	Name    string // the file name without .vhd
	Std     string
	Top     string
	// Pending names the construct, as its unsupported/construct
	// message words it, that keeps the case from running yet.
	Pending string
	// NVCDiffers, when set, is why NVC's report messages differ from
	// GoHard's; the recording is then only checked for freshness.
	NVCDiffers string
}

var entityRE = regexp.MustCompile(`(?im)^[ \t]*entity[ \t]+` +
	`([a-z][a-z0-9_]*)[ \t]+is\b`)

// ParseConformanceCase reads the header of the case feature/name with
// source src.
func ParseConformanceCase(feature, name string, src []byte) (
	ConformanceCase, error,
) {
	c := ConformanceCase{Feature: feature, Name: name}
	seen := map[string]bool{}
	for line := range strings.Lines(string(src)) {
		line = strings.TrimRight(line, "\r\n")
		body, ok := strings.CutPrefix(line, "--")
		if !ok {
			break
		}
		key, val, ok := strings.Cut(strings.TrimSpace(body), ":")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		var dst *string
		switch key {
		case "std":
			dst = &c.Std
		case "top":
			dst = &c.Top
		case "pending":
			dst = &c.Pending
		case "nvc-differs":
			dst = &c.NVCDiffers
		default:
			continue
		}
		if seen[key] {
			return c, fmt.Errorf("%s: duplicate %s: header", c.Path(),
				key)
		}
		seen[key] = true
		if val == "" {
			return c, fmt.Errorf("%s: empty %s: header", c.Path(), key)
		}
		*dst = val
	}
	if c.Std == "" {
		c.Std = DefaultConformanceStd
	}
	if !slices.Contains(conformanceStds, c.Std) {
		return c, fmt.Errorf("%s: std: %q is not one of %s", c.Path(),
			c.Std, strings.Join(conformanceStds, ", "))
	}
	if c.Top == "" {
		m := entityRE.FindAllSubmatch(src, -1)
		if len(m) == 0 {
			return c, fmt.Errorf("%s: no entity and no top: header",
				c.Path())
		}
		c.Top = strings.ToLower(string(m[len(m)-1][1]))
	}
	return c, nil
}

// File is the case's VHDL file name.
func (c ConformanceCase) File() string { return c.Name + ".vhd" }

// Path is the case file's repository-relative slash path.
func (c ConformanceCase) Path() string {
	return ConformanceDir + "/" + c.Feature + "/" + c.File()
}

// WantFile and NVCFile are the names of the expected-output files,
// next to the case file.
func (c ConformanceCase) WantFile() string { return c.Name + ".want" }

// NVCFile is the name of the NVC recording next to the case file.
func (c ConformanceCase) NVCFile() string { return c.Name + ".nvc.txt" }

// CheckArgs is the gohard command line that simulates the case, without
// the leading "gohard".
func (c ConformanceCase) CheckArgs() []string {
	return []string{"check", "--sim", "--seed", "0", "--tb", c.Top}
}

// Config is the gohard.yaml the case runs with: none (nil) for the
// default standard, which a gohard.yaml may not repeat without a
// config/redundant-default warning.
func (c ConformanceCase) Config() []byte {
	if c.Std == defaultConfigStd {
		return nil
	}
	return []byte("standard: \"" + c.Std + "\"\n")
}

// NVCArgs is the NVC command line that analyzes, elaborates and runs
// the case in a folder holding only its file, without the leading
// "nvc".
func (c ConformanceCase) NVCArgs() []string {
	return []string{"--std=" + c.Std, "-a", c.File(), "-e", c.Top, "-r"}
}

// Want rendering. A .want file is
//
//	# gohard check --sim --seed 0 --tb TOP (VHDL-STD)
//	exit N
//	== stdout
//	...stdout bytes...
//	== stderr
//	...stderr bytes...
//
// Streams that do not end in a newline get one, followed by a
// "\ no newline" line, so the file is always line-structured.

const noNewline = "\\ no newline\n"

// RenderWant renders the outcome of the case's run as its .want bytes.
func (c ConformanceCase) RenderWant(exit int, stdout, stderr []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# gohard %s (VHDL-%s)\nexit %d\n",
		strings.Join(c.CheckArgs(), " "), c.Std, exit)
	for _, s := range []struct {
		name string
		data []byte
	}{{"stdout", stdout}, {"stderr", stderr}} {
		b.WriteString("== " + s.name + "\n")
		b.Write(s.data)
		if len(s.data) > 0 && s.data[len(s.data)-1] != '\n' {
			b.WriteString("\n" + noNewline)
		}
	}
	return b.Bytes()
}

// Want is a parsed .want file.
type Want struct {
	Exit           int
	Stdout, Stderr []byte
}

// ParseWant parses .want bytes written by RenderWant.
func ParseWant(b []byte) (Want, error) {
	var w Want
	s := string(b)
	head, rest, ok := strings.Cut(s, "\n== stdout\n")
	if !ok {
		return w, errors.New("no == stdout line")
	}
	out, errs, ok := strings.Cut(rest, "== stderr\n")
	if !ok || !(out == "" || strings.HasSuffix(out, "\n")) {
		return w, errors.New("no == stderr line")
	}
	var exitLine string
	for l := range strings.Lines(head) {
		if !strings.HasPrefix(l, "#") {
			exitLine = strings.TrimSpace(l)
		}
	}
	n, err := strconv.Atoi(strings.TrimPrefix(exitLine, "exit "))
	if err != nil || !strings.HasPrefix(exitLine, "exit ") {
		return w, fmt.Errorf("bad exit line %q", exitLine)
	}
	w.Exit = n
	w.Stdout, w.Stderr = unstream(out), unstream(errs)
	return w, nil
}

// unstream undoes RenderWant's newline marker.
func unstream(s string) []byte {
	if t, ok := strings.CutSuffix(s, "\n"+noNewline); ok {
		return []byte(t)
	}
	return []byte(s)
}

// NVC recordings. A .nvc.txt file is
//
//	# NVC output of testdata/conformance/F/C.vhd
//	# recorded by: go run ./tools/oracle --update conformance
//	# nvc: VERSION
//	# command: nvc ARGS
//	# source sha256: HEX
//	# exit: N
//	...NVC's combined output...

// NVCRecordedBy is the recording command named in every .nvc.txt.
const NVCRecordedBy = "go run ./tools/oracle --update conformance"

// NVCRecording is a parsed .nvc.txt file.
type NVCRecording struct {
	Version, Command, SourceHash string
	Exit                         int
	Output                       string
}

// SourceHash is the lower-case hex sha256 of a case file.
func SourceHash(src []byte) string {
	h := sha256.Sum256(src)
	return hex.EncodeToString(h[:])
}

// RenderNVC renders an NVC run of the case with source src.
func (c ConformanceCase) RenderNVC(version string, src []byte, exit int,
	output string,
) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# NVC output of %s\n# recorded by: %s\n"+
		"# nvc: %s\n# command: nvc %s\n# source sha256: %s\n"+
		"# exit: %d\n", c.Path(), NVCRecordedBy, version,
		strings.Join(c.NVCArgs(), " "), SourceHash(src), exit)
	b.WriteString(output)
	if output != "" && !strings.HasSuffix(output, "\n") {
		b.WriteString("\n")
	}
	return []byte(b.String())
}

var nvcHeader = []string{"NVC output of", "recorded by:", "nvc:",
	"command:", "source sha256:", "exit:"}

// ParseNVC parses .nvc.txt bytes written by RenderNVC.
func ParseNVC(b []byte) (NVCRecording, error) {
	var r NVCRecording
	s := string(b)
	vals := make([]string, len(nvcHeader))
	for i, key := range nvcHeader {
		line, rest, ok := strings.Cut(s, "\n")
		v, has := strings.CutPrefix(line, "# "+key+" ")
		if !ok || !has {
			return r, fmt.Errorf("line %d is not \"# %s ...\"", i+1, key)
		}
		vals[i], s = strings.TrimSpace(v), rest
	}
	if vals[1] != NVCRecordedBy {
		return r, fmt.Errorf("recorded by %q, want %q", vals[1],
			NVCRecordedBy)
	}
	n, err := strconv.Atoi(vals[5])
	if err != nil {
		return r, fmt.Errorf("bad exit %q", vals[5])
	}
	r.Version = vals[2]
	r.Command = strings.TrimPrefix(vals[3], "nvc ")
	r.SourceHash, r.Exit, r.Output = vals[4], n, s
	return r, nil
}

// SimMessage is one simulation report or assertion message: its severity
// in lower case and its text with line breaks written as \n (GoHard's
// diagnostic form).
type SimMessage struct{ Severity, Text string }

func (m SimMessage) String() string { return m.Severity + ": " + m.Text }

var (
	nvcMsgRE = regexp.MustCompile(
		`^\*\* (Note|Warning|Error|Failure): [^ :]+: (.*)$`)
	gohardMsgRE = regexp.MustCompile(`\[sim/(?:assert|report)-` +
		`(note|warning|error|failure)\]: (?:assertion|report) ` +
		`[a-z]+: (.*)$`)
)

// nvcOnly are the notes NVC prints that are no VHDL report.
var nvcOnly = []string{"FINISH called", "STOP called"}

// NVCMessages returns the report and assertion messages of an NVC run,
// sorted. A message's text runs up to the next line that starts with
// "** " or a space (NVC's location lines).
func NVCMessages(output string) []SimMessage {
	var out []SimMessage
	cur := -1
	for line := range strings.Lines(output) {
		line = strings.TrimRight(line, "\r\n")
		if m := nvcMsgRE.FindStringSubmatch(line); m != nil {
			out = append(out, SimMessage{strings.ToLower(m[1]), m[2]})
			cur = len(out) - 1
			continue
		}
		if cur >= 0 && line != "" && !strings.HasPrefix(line, " ") &&
			!strings.HasPrefix(line, "** ") {
			out[cur].Text += `\n` + line
			continue
		}
		cur = -1
	}
	out = slices.DeleteFunc(out, func(m SimMessage) bool {
		return m.Severity == "note" && slices.Contains(nvcOnly, m.Text)
	})
	return sortMessages(out)
}

// GoHardMessages returns the sim/assert-* and sim/report-* messages of
// a gohard check --sim stdout, sorted.
func GoHardMessages(stdout []byte) []SimMessage {
	var out []SimMessage
	for line := range strings.Lines(string(stdout)) {
		line = strings.TrimRight(line, "\r\n")
		if m := gohardMsgRE.FindStringSubmatch(line); m != nil {
			out = append(out, SimMessage{m[1], m[2]})
		}
	}
	return sortMessages(out)
}

func sortMessages(m []SimMessage) []SimMessage {
	slices.SortStableFunc(m, func(a, b SimMessage) int {
		return strings.Compare(a.String(), b.String())
	})
	return m
}

// CheckNVC compares the recording rec of the case (source src) with
// the case and with GoHard's expected output want, without running
// NVC. Each problem is one line. A stale recording (another source,
// command or recorder) is reported without comparing messages; a
// pending case or one with an nvc-differs: header is only checked for
// freshness.
func (c ConformanceCase) CheckNVC(src, rec []byte, want *Want) []string {
	r, err := ParseNVC(rec)
	if err != nil {
		return []string{c.NVCFile() + ": " + err.Error()}
	}
	var probs []string
	if r.SourceHash != SourceHash(src) {
		probs = append(probs, c.NVCFile()+": recorded for another "+
			"version of "+c.File())
	}
	if cmd := strings.Join(c.NVCArgs(), " "); r.Command != cmd {
		probs = append(probs, fmt.Sprintf("%s: command nvc %s, want "+
			"nvc %s", c.NVCFile(), r.Command, cmd))
	}
	if len(probs) > 0 || c.Pending != "" || c.NVCDiffers != "" ||
		want == nil {
		return probs
	}
	got, nvc := GoHardMessages(want.Stdout), NVCMessages(r.Output)
	if !slices.Equal(got, nvc) {
		probs = append(probs, fmt.Sprintf("%s: messages %q, NVC %q "+
			"(add an nvc-differs: header if that is intended)",
			c.WantFile(), got, nvc))
	}
	if (want.Exit != 0) != (r.Exit != 0) {
		probs = append(probs, fmt.Sprintf("%s: exit %d, NVC exit %d",
			c.WantFile(), want.Exit, r.Exit))
	}
	return probs
}

package format

import (
	"github.com/connerohnesorge/gohard/internal/config"
	"github.com/connerohnesorge/gohard/internal/token"
)

// Case is the format.keyword_case setting.
type Case uint8

// Keyword cases.
const (
	// Lower writes reserved words in lower case (the default).
	Lower Case = iota
	// Upper writes reserved words in upper case.
	Upper
	// Preserve keeps reserved words as written.
	Preserve
)

// String returns the gohard.yaml spelling of c.
func (c Case) String() string {
	switch c {
	case Upper:
		return "upper"
	case Preserve:
		return "preserve"
	}
	return "lower"
}

// ParseCase parses "lower", "upper" or "preserve".
func ParseCase(s string) (Case, bool) {
	switch s {
	case "lower":
		return Lower, true
	case "upper":
		return Upper, true
	case "preserve":
		return Preserve, true
	}
	return Lower, false
}

// Limits of the options (#28): an indent of at least 1 and a line
// length of at least 40.
const (
	MinIndent        = 1
	MinMaxLineLength = 40
)

// Options configure the formatter. The zero value is not useful; start
// from Default or FromConfig.
type Options struct {
	// Indent is the number of spaces per nesting level (format.indent).
	Indent int
	// KeywordCase is format.keyword_case.
	KeywordCase Case
	// AlignPorts is format.align_ports.
	AlignPorts bool
	// MaxLineLength is format.max_line_length.
	MaxLineLength int
	// Standard selects the reserved-word table for keyword case.
	Standard token.Standard
}

// Default returns the options of a project without gohard.yaml.
func Default() Options { return FromConfig(config.Defaults()) }

// FromConfig returns the format options of a loaded configuration.
func FromConfig(c config.Config) Options {
	kc, _ := ParseCase(c.Format.KeywordCase)
	std, ok := token.ParseStandard(c.Standard)
	if !ok {
		std = token.Std2008
	}
	return Options{
		Indent:        int(c.Format.Indent),
		KeywordCase:   kc,
		AlignPorts:    c.Format.AlignPorts,
		MaxLineLength: int(c.Format.MaxLineLength),
		Standard:      std,
	}.normalized()
}

// normalized clamps out-of-range values to the nearest valid one, so a
// hand-built Options never makes the formatter misbehave.
func (o Options) normalized() Options {
	o.Indent = max(o.Indent, MinIndent)
	o.MaxLineLength = max(o.MaxLineLength, MinMaxLineLength)
	if o.KeywordCase > Preserve {
		o.KeywordCase = Lower
	}
	if o.Standard == 0 {
		o.Standard = token.Std2008
	}
	return o
}

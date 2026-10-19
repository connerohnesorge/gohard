package cst

import "github.com/connerohnesorge/gohard/internal/token"

// DocComment returns the doc comment of the construct that starts with
// token t (#10): the block of comments in t's leading trivia that ends
// on the line just above t (or on t's own line, before it) with no blank
// line between the block and t, nor inside the block. A comment followed
// by a blank line floats and is not a doc comment. The result lists the
// comment trivia in source order; it is nil when there is none.
// Directives end a block.
func DocComment(t Token) []Trivia {
	lead := t.Leading()
	i := len(lead) - 1
	// Between the block and the token: whitespace and at most one
	// newline.
	newlines := 0
	for ; i >= 0; i-- {
		k := lead[i].Kind
		if k == token.Whitespace {
			continue
		}
		if k == token.Newline {
			newlines++
			if newlines > 1 {
				return nil
			}
			continue
		}
		break
	}
	end := i + 1
	start := end
	newlines = 0
	for ; i >= 0; i-- {
		switch lead[i].Kind {
		case token.LineComment, token.BlockComment:
			start = i
			newlines = 0
		case token.Whitespace:
		case token.Newline:
			newlines++
			if newlines > 1 {
				i = -1 // blank line: the block ends here
			}
		default:
			i = -1
		}
	}
	if start == end {
		return nil
	}
	var out []Trivia
	for _, tr := range lead[start:end] {
		if tr.Kind == token.LineComment || tr.Kind == token.BlockComment {
			out = append(out, tr)
		}
	}
	return out
}

package importer

// The command splitter for the Tcl files gohard import reads (ModelSim
// .do scripts and Quartus .qsf files). It follows the Tcl word rules
// (commands end at a newline or ';', '#' starts a comment where a
// command could start, words are bare, "quoted" or {braced}, a
// backslash-newline is a space) but evaluates nothing: a word that
// would be substituted ($var or [command]) is only marked, and the
// caller skips that command with a hint. Nothing is ever run.

// word is one Tcl word of a command.
type word struct {
	Text string
	Off  int // byte offset of the word's first character
	// Subst reports a $ or [ substitution Tcl would perform, so Text
	// is not the value Tcl would see.
	Subst bool
}

// command is one Tcl command: its words, at least one.
type command struct {
	Words []word
	Off   int // byte offset of the first word
}

// syntaxError is an unterminated brace, quote or bracket.
type syntaxError struct {
	Off    int // the opening character
	Reason string
}

// splitTcl splits src into commands. It stops at the first syntax
// error, returning the commands before it.
func splitTcl(src []byte) ([]command, *syntaxError) {
	s := &tclScanner{src: src}
	var out []command
	for {
		c, ok := s.command()
		if s.err != nil {
			return out, s.err
		}
		if len(c.Words) > 0 {
			out = append(out, c)
		}
		if !ok {
			return out, nil
		}
	}
}

type tclScanner struct {
	src []byte
	pos int
	err *syntaxError
}

func (s *tclScanner) peek() byte {
	if s.pos < len(s.src) {
		return s.src[s.pos]
	}
	return 0
}

func (s *tclScanner) atEOF() bool { return s.pos >= len(s.src) }

// isSpace reports a word separator inside a command.
func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\f' }

// isEnd reports a command terminator.
func isEnd(c byte) bool { return c == '\n' || c == '\r' || c == ';' }

// skipSpace skips separators and backslash-newlines inside a command.
func (s *tclScanner) skipSpace() {
	for !s.atEOF() {
		c := s.peek()
		switch {
		case isSpace(c):
			s.pos++
		case c == '\\' && s.pos+1 < len(s.src) &&
			(s.src[s.pos+1] == '\n' || s.src[s.pos+1] == '\r'):
			s.pos += 2
			if s.src[s.pos-1] == '\r' && s.peek() == '\n' {
				s.pos++
			}
		default:
			return
		}
	}
}

// command reads one command; ok is false at the end of the input.
func (s *tclScanner) command() (command, bool) {
	// Skip blank lines and separators before the command.
	for !s.atEOF() && (isSpace(s.peek()) || isEnd(s.peek())) {
		s.pos++
	}
	if s.atEOF() {
		return command{}, false
	}
	if s.peek() == '#' {
		s.comment()
		return command{}, !s.atEOF()
	}
	c := command{Off: s.pos}
	for {
		s.skipSpace()
		if s.atEOF() {
			return c, false
		}
		if isEnd(s.peek()) {
			s.pos++
			return c, true
		}
		w, ok := s.word()
		if !ok {
			return c, false
		}
		c.Words = append(c.Words, w)
	}
}

// comment skips to the end of the line; a backslash-newline continues
// the comment, as in Tcl.
func (s *tclScanner) comment() {
	for !s.atEOF() {
		c := s.peek()
		if c == '\\' {
			s.pos += 2
			continue
		}
		if c == '\n' || c == '\r' {
			return
		}
		s.pos++
	}
	s.pos = min(s.pos, len(s.src))
}

// word reads one word at s.pos.
func (s *tclScanner) word() (word, bool) {
	switch s.peek() {
	case '{':
		return s.braced()
	case '"':
		return s.quoted()
	}
	return s.bare()
}

// braced reads a {word}: the text between the matching braces,
// literal except that a backslash-newline becomes a space.
func (s *tclScanner) braced() (word, bool) {
	start := s.pos
	s.pos++
	depth := 1
	var b []byte
	for !s.atEOF() {
		c := s.peek()
		switch c {
		case '\\':
			if s.pos+1 < len(s.src) {
				n := s.src[s.pos+1]
				if n == '\n' || n == '\r' {
					s.pos += 2
					s.skipSpace()
					b = append(b, ' ')
					continue
				}
				b = append(b, c, n)
				s.pos += 2
				continue
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				s.pos++
				w := word{Text: string(b), Off: start}
				// Tcl refuses characters right after the closing
				// brace; GoHard reads them as part of the word.
				for !s.atEOF() && !isSpace(s.peek()) &&
					!isEnd(s.peek()) {
					w.Text += string(s.peek())
					s.pos++
				}
				return w, true
			}
		}
		b = append(b, c)
		s.pos++
	}
	s.err = &syntaxError{Off: start, Reason: "unterminated {"}
	return word{}, false
}

// quoted reads a "word" with backslash sequences; $ and [ mark a
// substitution.
func (s *tclScanner) quoted() (word, bool) {
	w := word{Off: s.pos}
	s.pos++
	var b []byte
	for !s.atEOF() {
		c := s.peek()
		switch c {
		case '"':
			s.pos++
			w.Text = string(b)
			return w, true
		case '\\':
			b = s.escape(b)
			continue
		case '$':
			w.Subst = true
		case '[':
			w.Subst = true
			if !s.bracket(&b) {
				return word{}, false
			}
			continue
		}
		b = append(b, c)
		s.pos++
	}
	s.err = &syntaxError{Off: w.Off, Reason: "unterminated \""}
	return word{}, false
}

// bare reads a word up to a separator or terminator.
func (s *tclScanner) bare() (word, bool) {
	w := word{Off: s.pos}
	var b []byte
	for !s.atEOF() {
		c := s.peek()
		if isSpace(c) || isEnd(c) {
			break
		}
		switch c {
		case '\\':
			if s.pos+1 < len(s.src) && (s.src[s.pos+1] == '\n' ||
				s.src[s.pos+1] == '\r') {
				w.Text = string(b)
				return w, true // a backslash-newline ends the word
			}
			b = s.escape(b)
			continue
		case '$':
			w.Subst = true
		case '[':
			w.Subst = true
			if !s.bracket(&b) {
				return word{}, false
			}
			continue
		}
		b = append(b, c)
		s.pos++
	}
	w.Text = string(b)
	return w, true
}

// bracket copies a [command substitution] with its nested brackets,
// braces and quotes, which may hold spaces and newlines.
func (s *tclScanner) bracket(b *[]byte) bool {
	start := s.pos
	depth := 0
	for !s.atEOF() {
		c := s.peek()
		*b = append(*b, c)
		s.pos++
		switch c {
		case '\\':
			if !s.atEOF() {
				*b = append(*b, s.peek())
				s.pos++
			}
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return true
			}
		}
	}
	s.err = &syntaxError{Off: start, Reason: "unterminated ["}
	return false
}

// escape appends the value of the backslash sequence at s.pos.
func (s *tclScanner) escape(b []byte) []byte {
	s.pos++
	if s.atEOF() {
		return append(b, '\\')
	}
	c := s.peek()
	s.pos++
	switch c {
	case 'n':
		return append(b, '\n')
	case 't':
		return append(b, '\t')
	case '\n', '\r':
		return append(b, ' ')
	}
	return append(b, c)
}

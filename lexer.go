package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// stateFn represents the state of the scanner as a function that returns the next state.
type stateFn func(*lexer) stateFn

// lexer holds the state of the scanner. Uses Rob Pike's state-function architecture
// from https://go.dev/talks/2011/lex.slide
type lexer struct {
	name      string    // for error messages
	input     string    // the string being scanned
	start     int       // start position of this item
	pos       int       // current position in the input
	width     int       // width of last rune read
	line      int       // 1-based line number at start
	startLine int       // line number at item start
	items     chan item  // channel of scanned items
	lastType  itemType  // type of the last emitted item (for newline insertion)
	parenDepth int      // nesting depth of (), [], {}
}

// eof is a sentinel rune to signal end of input.
const eof = -1

// lex creates a new scanner for the input string.
func lex(name, input string) *lexer {
	l := &lexer{
		name:  name,
		input: input,
		line:  1,
		items: make(chan item, 2),
	}
	go l.run()
	return l
}

// run lexes the input by executing state functions until the state is nil.
func (l *lexer) run() {
	for state := lexStart; state != nil; {
		state = state(l)
	}
	close(l.items)
}

// next returns the next rune in the input.
func (l *lexer) next() rune {
	if l.pos >= len(l.input) {
		l.width = 0
		return eof
	}
	r, w := utf8.DecodeRuneInString(l.input[l.pos:])
	l.width = w
	l.pos += w
	if r == '\n' {
		l.line++
	}
	return r
}

// peek returns but does not consume the next rune in the input.
func (l *lexer) peek() rune {
	r := l.next()
	l.backup()
	return r
}

// backup steps back one rune.  Can be called only once per call of next.
func (l *lexer) backup() {
	l.pos -= l.width
	if l.width > 0 && l.input[l.pos] == '\n' {
		l.line--
	}
}

// emit passes an item back to the client.
func (l *lexer) emit(t itemType) {
	l.items <- item{t, l.input[l.start:l.pos], l.start, l.startLine}
	l.lastType = t
	l.start = l.pos
	l.startLine = l.line
}

// emitVal passes an item with an explicit value back to the client.
func (l *lexer) emitVal(t itemType, val string) {
	l.items <- item{t, val, l.start, l.startLine}
	l.lastType = t
	l.start = l.pos
	l.startLine = l.line
}

// ignore skips over the pending input before this point.
func (l *lexer) ignore() {
	l.line += strings.Count(l.input[l.start:l.pos], "\n")
	l.start = l.pos
	l.startLine = l.line
}

// accept consumes the next rune if it's from the valid set.
func (l *lexer) accept(valid string) bool {
	if strings.ContainsRune(valid, l.next()) {
		return true
	}
	l.backup()
	return false
}

// acceptRun consumes a run of runes from the valid set.
func (l *lexer) acceptRun(valid string) {
	for strings.ContainsRune(valid, l.next()) {
	}
	l.backup()
}

// errorf returns an error token and terminates the scan.
func (l *lexer) errorf(format string, args ...interface{}) stateFn {
	l.items <- item{itemError, fmt.Sprintf(format, args...), l.start, l.startLine}
	return nil
}

// nextItem returns the next item from the input.
func (l *lexer) nextItem() item {
	return <-l.items
}

// peekNonSpace returns the first non-whitespace character at or after pos,
// without consuming any input. Used for lookahead decisions.
func (l *lexer) peekNonSpace() rune {
	i := l.pos
	for i < len(l.input) {
		r, w := utf8.DecodeRuneInString(l.input[i:])
		if r != ' ' && r != '\t' && r != '\r' {
			return r
		}
		i += w
	}
	return eof
}

// shouldInsertNewline returns true if a significant newline should be emitted
// based on the last emitted token type.
func (l *lexer) shouldInsertNewline() bool {
	switch l.lastType {
	case itemIdent, itemNumber, itemString, itemRegex,
		itemIncr, itemDecr,
		itemRParen, itemRBracket, itemRBrace,
		itemNext, itemNextfile, itemExit,
		itemBreak, itemContinue, itemReturn,
		itemGetline:
		return true
	}
	return false
}

// lexStart is the main dispatch state.
func lexStart(l *lexer) stateFn {
	l.startLine = l.line
	r := l.next()
	switch {
	case r == eof:
		if l.shouldInsertNewline() {
			l.backup()
			l.emit(itemNewline)
		}
		l.emit(itemEOF)
		return nil

	case r == '\n':
		if l.shouldInsertNewline() {
			l.emitVal(itemNewline, "\n")
		} else {
			l.ignore()
		}
		return lexStart

	case r == '\r':
		l.ignore()
		return lexStart

	case r == '\\' && l.peek() == '\n':
		// line continuation: backslash-newline is ignored
		l.next() // consume the newline
		l.ignore()
		return lexStart

	case r == ' ' || r == '\t':
		l.ignore()
		return lexStart

	case r == '#':
		// comment: skip to end of line
		for l.peek() != '\n' && l.peek() != eof {
			l.next()
		}
		l.ignore()
		return lexStart

	case r == '"':
		return lexString

	case r == '/':
		// Could be a regex, division, or //comment (not standard awk but handle /= )
		// Division comes after a "value" token; regex comes after operators, etc.
		if l.isDivision() {
			if l.peek() == '=' {
				l.next()
				l.emit(itemDivAssign)
			} else {
				l.emitVal(itemSlash, "/")
			}
			return lexStart
		}
		return lexRegex

	case r == '`':
		// Not standard AWK; treat as raw string (no escapes)
		return lexRawString

	case isDigit(r) || (r == '.' && isDigit(l.peek())):
		l.backup()
		return lexNumber

	case isAlpha(r):
		l.backup()
		return lexIdentifier

	case r == '+':
		if l.peek() == '+' {
			l.next()
			l.emit(itemIncr)
		} else if l.peek() == '=' {
			l.next()
			l.emit(itemAddAssign)
		} else {
			l.emitVal(itemPlus, "+")
		}
		return lexStart

	case r == '-':
		if l.peek() == '-' {
			l.next()
			l.emit(itemDecr)
		} else if l.peek() == '=' {
			l.next()
			l.emit(itemSubAssign)
		} else {
			l.emitVal(itemMinus, "-")
		}
		return lexStart

	case r == '*':
		if l.peek() == '=' {
			l.next()
			l.emit(itemMulAssign)
		} else if l.peek() == '*' {
			// ** is power in some awks
			l.next()
			if l.peek() == '=' {
				l.next()
				l.emit(itemPowAssign)
			} else {
				l.emit(itemPow)
			}
		} else {
			l.emitVal(itemStar, "*")
		}
		return lexStart

	case r == '%':
		if l.peek() == '=' {
			l.next()
			l.emit(itemModAssign)
		} else {
			l.emitVal(itemPercent, "%")
		}
		return lexStart

	case r == '^':
		if l.peek() == '=' {
			l.next()
			l.emit(itemPowAssign)
		} else {
			l.emit(itemPow)
		}
		return lexStart

	case r == '!':
		if l.peek() == '=' {
			l.next()
			l.emit(itemNE)
		} else if l.peek() == '~' {
			l.next()
			l.emit(itemNoMatch)
		} else {
			l.emitVal(itemBang, "!")
		}
		return lexStart

	case r == '~':
		l.emit(itemMatch)
		return lexStart

	case r == '<':
		if l.peek() == '=' {
			l.next()
			l.emit(itemLE)
		} else {
			l.emitVal(itemLT, "<")
		}
		return lexStart

	case r == '>':
		if l.peek() == '>' {
			l.next()
			l.emit(itemAppend)
		} else if l.peek() == '=' {
			l.next()
			l.emit(itemGE)
		} else {
			l.emitVal(itemGT, ">")
		}
		return lexStart

	case r == '=':
		if l.peek() == '=' {
			l.next()
			l.emit(itemEQ)
		} else {
			l.emitVal(itemType('='), "=")
		}
		return lexStart

	case r == '&':
		if l.peek() == '&' {
			l.next()
			l.emit(itemAnd)
		} else {
			// single & is not standard awk; error or treat as bitwise (gawk)
			l.emitVal(itemType('&'), "&")
		}
		return lexStart

	case r == '|':
		if l.peek() == '|' {
			l.next()
			l.emit(itemOr)
		} else if l.peek() == '&' {
			l.next()
			l.emit(itemPipeAppend)
		} else {
			l.emitVal(itemPipe, "|")
		}
		return lexStart

	case r == '(':
		l.parenDepth++
		l.emitVal(itemLParen, "(")
		return lexStart

	case r == ')':
		l.parenDepth--
		l.emit(itemRParen)
		return lexStart

	case r == '[':
		l.parenDepth++
		l.emitVal(itemLBracket, "[")
		return lexStart

	case r == ']':
		l.parenDepth--
		l.emit(itemRBracket)
		return lexStart

	case r == '{':
		l.parenDepth++
		l.emitVal(itemLBrace, "{")
		return lexStart

	case r == '}':
		l.parenDepth--
		l.emit(itemRBrace)
		return lexStart

	case r == ';':
		l.emit(itemSemicolon)
		return lexStart

	case r == ',':
		l.emitVal(itemComma, ",")
		return lexStart

	case r == '$':
		l.emitVal(itemDollar, "$")
		return lexStart

	case r == '?':
		l.emitVal(itemQuestion, "?")
		return lexStart

	case r == ':':
		l.emitVal(itemColon, ":")
		return lexStart

	default:
		return l.errorf("unexpected character: %q", r)
	}
}

// isDivision returns true if the current context means '/' is division (not regex start).
func (l *lexer) isDivision() bool {
	switch l.lastType {
	case itemIdent, itemNumber, itemString, itemRegex,
		itemRParen, itemRBracket,
		itemIncr, itemDecr:
		return true
	}
	return false
}

// lexIdentifier scans an alphanumeric identifier or keyword.
func lexIdentifier(l *lexer) stateFn {
	l.next() // consume first character (already checked isAlpha)
	for {
		r := l.peek()
		if isAlpha(r) || isDigit(r) {
			l.next()
		} else {
			break
		}
	}
	word := l.input[l.start:l.pos]

	// Check if it's a structural regex keyword (x, y, g, v followed by /)
	if st, ok := structuralKeywords[word]; ok {
		// Look ahead past whitespace for /
		rest := l.input[l.pos:]
		i := 0
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
			i++
		}
		if i < len(rest) && rest[i] == '/' {
			// Check it's not /= (divide-assign) or // (comment)
			if i+1 < len(rest) && rest[i+1] != '=' && rest[i+1] != '/' {
				l.emit(st)
				return lexStart
			}
			if i+1 >= len(rest) {
				// / at end of input: regex
				l.emit(st)
				return lexStart
			}
		}
	}

	// Check keywords
	if kw, ok := keywords[word]; ok {
		l.emit(kw)
		return lexStart
	}

	l.emit(itemIdent)
	return lexStart
}

// lexNumber scans a numeric literal.
func lexNumber(l *lexer) stateFn {
	digits := "0123456789"
	l.accept("+-") // optional sign? No, signs are handled as unary ops in awk

	// Check for hex
	if l.accept("0") && l.accept("xX") {
		digits = "0123456789abcdefABCDEF"
		l.acceptRun(digits)
		if !strings.ContainsAny(l.input[l.start:l.pos], "0123456789abcdefABCDEF") {
			return l.errorf("bad hex constant")
		}
		l.emit(itemNumber)
		return lexStart
	}

	l.acceptRun(digits)
	if l.accept(".") {
		l.acceptRun(digits)
	}
	if l.accept("eE") {
		l.accept("+-")
		l.acceptRun("0123456789")
	}

	if isAlpha(l.peek()) {
		l.next()
		return l.errorf("bad number syntax: %q", l.input[l.start:l.pos])
	}

	l.emit(itemNumber)
	return lexStart
}

// lexString scans a double-quoted string.
func lexString(l *lexer) stateFn {
	// Opening " already consumed.
	var buf strings.Builder
	for {
		r := l.next()
		switch r {
		case eof, '\n':
			return l.errorf("unterminated string")
		case '\\':
			esc := l.next()
			switch esc {
			case 'n':
				buf.WriteByte('\n')
			case 't':
				buf.WriteByte('\t')
			case 'r':
				buf.WriteByte('\r')
			case 'a':
				buf.WriteByte('\a')
			case 'b':
				buf.WriteByte('\b')
			case 'f':
				buf.WriteByte('\f')
			case 'v':
				buf.WriteByte('\v')
			case '\\':
				buf.WriteByte('\\')
			case '"':
				buf.WriteByte('"')
			case '/':
				buf.WriteByte('/')
			case '0', '1', '2', '3', '4', '5', '6', '7':
				// octal escape
				oct := string(esc)
				for i := 0; i < 2; i++ {
					r2 := l.peek()
					if r2 >= '0' && r2 <= '7' {
						oct += string(l.next())
					} else {
						break
					}
				}
				var val int
				fmt.Sscanf(oct, "%o", &val)
				buf.WriteByte(byte(val))
			case 'x':
				// hex escape
				hex := ""
				for i := 0; i < 2; i++ {
					r2 := l.peek()
					if (r2 >= '0' && r2 <= '9') || (r2 >= 'a' && r2 <= 'f') || (r2 >= 'A' && r2 <= 'F') {
						hex += string(l.next())
					} else {
						break
					}
				}
				if hex != "" {
					var val int
					fmt.Sscanf(hex, "%x", &val)
					buf.WriteByte(byte(val))
				} else {
					buf.WriteByte('x')
				}
			default:
				buf.WriteByte('\\')
				buf.WriteRune(esc)
			}
		case '"':
			l.emitVal(itemString, buf.String())
			return lexStart
		default:
			buf.WriteRune(r)
		}
	}
}

// lexRawString scans a backtick-quoted raw string (no escape processing).
func lexRawString(l *lexer) stateFn {
	for {
		r := l.next()
		switch r {
		case eof:
			return l.errorf("unterminated raw string")
		case '`':
			l.emitVal(itemString, l.input[l.start+1:l.pos-1])
			return lexStart
		}
	}
}

// lexRegex scans a regex literal /pattern/.
// The opening / has already been consumed.
func lexRegex(l *lexer) stateFn {
	var buf strings.Builder
	for {
		r := l.next()
		switch r {
		case eof, '\n':
			return l.errorf("unterminated regex")
		case '\\':
			next := l.next()
			if next == '/' {
				buf.WriteByte('/')
			} else {
				buf.WriteByte('\\')
				buf.WriteRune(next)
			}
		case '/':
			l.emitVal(itemRegex, buf.String())
			return lexStart
		default:
			buf.WriteRune(r)
		}
	}
}

func isAlpha(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isDigit(r rune) bool {
	return r >= '0' && r <= '9'
}

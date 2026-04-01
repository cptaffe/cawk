package main

import (
	"bufio"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// stateFn represents the state of the scanner as a function that returns the next state.
type stateFn func(*lexer) stateFn

// lexer holds the state of the scanner.  Follows Rob Pike's state-function
// architecture (https://go.dev/talks/2011/lex.slide).
//
// Input is consumed via a *bufio.Reader.  peek() reads ahead using
// bufio.Reader.Peek + utf8.DecodeRune rather than the consume-then-backup
// idiom, so it never disturbs the reader position.  Consumed runes accumulate
// in buf; emit() drains buf to build the token value.
type lexer struct {
	name       string
	input      *bufio.Reader
	buf        strings.Builder // runes consumed since the last emit/ignore
	width      int             // byte-width of the last rune returned by next()
	line       int             // current 1-based line number
	startLine  int             // line number at the start of the current token
	items      chan item
	lastType   itemType
	parenDepth int
}

// eof is a sentinel rune signalling end of input.
const eof = -1

// lex creates a new scanner for the input string.
func lex(name, input string) *lexer {
	l := &lexer{
		name:  name,
		input: bufio.NewReader(strings.NewReader(input)),
		line:  1,
		items: make(chan item, 2),
	}
	go l.run()
	return l
}

func (l *lexer) run() {
	for state := lexStart; state != nil; {
		state = state(l)
	}
	close(l.items)
}

// next consumes and returns the next rune.
func (l *lexer) next() rune {
	r, w, err := l.input.ReadRune()
	if err != nil {
		l.width = 0
		return eof
	}
	l.width = w
	l.buf.WriteRune(r)
	if r == '\n' {
		l.line++
	}
	return r
}

// peek returns the next rune without consuming it.
// It uses bufio.Reader.Peek + utf8.DecodeRune — no consume-then-backup cycle.
func (l *lexer) peek() rune {
	bs, _ := l.input.Peek(utf8.UTFMax)
	if len(bs) == 0 {
		return eof
	}
	r, _ := utf8.DecodeRune(bs)
	return r
}

// backup steps back one rune.  May only be called once per call to next.
func (l *lexer) backup() {
	if l.width == 0 {
		return
	}
	l.input.UnreadRune()
	s := l.buf.String()
	r, _ := utf8.DecodeLastRuneInString(s)
	if r == '\n' {
		l.line--
	}
	l.buf.Reset()
	l.buf.WriteString(s[:len(s)-l.width])
	l.width = 0
}

// emit passes an item whose value is everything consumed since the last emit.
func (l *lexer) emit(t itemType) {
	l.items <- item{t, l.buf.String(), l.startLine}
	l.lastType = t
	l.buf.Reset()
	l.startLine = l.line
}

// emitVal passes an item with an explicit value (discards buf).
func (l *lexer) emitVal(t itemType, val string) {
	l.items <- item{t, val, l.startLine}
	l.lastType = t
	l.buf.Reset()
	l.startLine = l.line
}

// ignore discards everything consumed since the last emit.
func (l *lexer) ignore() {
	l.buf.Reset()
	l.startLine = l.line
}

// accept consumes the next rune if it is in the valid set.
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

// errorf emits an error token and stops the scan.
func (l *lexer) errorf(format string, args ...interface{}) stateFn {
	l.items <- item{itemError, fmt.Sprintf(format, args...), l.startLine}
	return nil
}

// nextItem returns the next item from the input.
func (l *lexer) nextItem() item {
	return <-l.items
}

// peekNonSpace returns the first non-whitespace rune ahead in the unread
// portion of the input, without consuming anything.
func (l *lexer) peekNonSpace() rune {
	bs, _ := l.input.Peek(256)
	i := 0
	for i < len(bs) {
		r, w := utf8.DecodeRune(bs[i:])
		if r != ' ' && r != '\t' && r != '\r' {
			return r
		}
		i += w
	}
	return eof
}

// shouldInsertNewline reports whether a significant newline should follow the
// last emitted token.
func (l *lexer) shouldInsertNewline() bool {
	switch l.lastType {
	case itemIdent, itemNumber, itemString, itemRegex,
		itemNamedGroupRef,
		itemIncr, itemDecr,
		itemRParen, itemRBracket, itemRBrace,
		itemExit,
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
		l.next() // consume the newline
		l.ignore()
		return lexStart

	case r == ' ' || r == '\t':
		l.ignore()
		return lexStart

	case r == '#':
		for l.peek() != '\n' && l.peek() != eof {
			l.next()
		}
		l.ignore()
		return lexStart

	case r == '"':
		return lexString

	case r == '/':
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

	case r == '*':
		if l.peek() == '=' {
			l.next()
			l.emit(itemMulAssign)
		} else if l.peek() == '*' {
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

	case r == '%':
		if l.peek() == '=' {
			l.next()
			l.emit(itemModAssign)
		} else {
			l.emitVal(itemPercent, "%")
		}

	case r == '^':
		if l.peek() == '=' {
			l.next()
			l.emit(itemPowAssign)
		} else {
			l.emit(itemPow)
		}

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

	case r == '~':
		l.emit(itemMatch)

	case r == '<':
		if l.peek() == '=' {
			l.next()
			l.emit(itemLE)
		} else {
			l.emitVal(itemLT, "<")
		}

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

	case r == '=':
		if l.peek() == '=' {
			l.next()
			l.emit(itemEQ)
		} else {
			l.emitVal(itemType('='), "=")
		}

	case r == '&':
		if l.peek() == '&' {
			l.next()
			l.emit(itemAnd)
		} else {
			l.emitVal(itemType('&'), "&")
		}

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

	case r == '(':
		l.parenDepth++
		l.emitVal(itemLParen, "(")

	case r == ')':
		l.parenDepth--
		l.emit(itemRParen)

	case r == '[':
		l.parenDepth++
		l.emitVal(itemLBracket, "[")

	case r == ']':
		l.parenDepth--
		l.emit(itemRBracket)

	case r == '{':
		l.parenDepth++
		l.emitVal(itemLBrace, "{")

	case r == '}':
		l.parenDepth--
		l.emit(itemRBrace)

	case r == ';':
		l.emit(itemSemicolon)

	case r == ',':
		l.emitVal(itemComma, ",")

	case r == '$':
		if isAlpha(l.peek()) {
			l.next() // consume first letter
			for isAlpha(l.peek()) || isDigit(l.peek()) {
				l.next()
			}
			s := l.buf.String() // "$name"
			l.emitVal(itemNamedGroupRef, s[1:])
		} else {
			l.emitVal(itemDollar, "$")
		}

	case r == '?':
		l.emitVal(itemQuestion, "?")

	case r == ':':
		l.emitVal(itemColon, ":")

	default:
		return l.errorf("unexpected character: %q", r)
	}
	return lexStart
}

// isDivision reports whether '/' in the current context is division rather
// than the start of a regex literal.
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
	l.next() // first character already validated as isAlpha by caller
	for isAlpha(l.peek()) || isDigit(l.peek()) {
		l.next()
	}
	word := l.buf.String()

	// Structural keyword (e.g. "y") only when immediately followed (past
	// optional whitespace) by "/" that is not "/=" or "//".
	if st, ok := structuralKeywords[word]; ok {
		bs, _ := l.input.Peek(256)
		i := 0
		for i < len(bs) && (bs[i] == ' ' || bs[i] == '\t') {
			i++
		}
		if i < len(bs) && bs[i] == '/' &&
			(i+1 >= len(bs) || (bs[i+1] != '=' && bs[i+1] != '/')) {
			l.emit(st)
			return lexStart
		}
	}

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

	if l.accept("0") && l.accept("xX") {
		digits = "0123456789abcdefABCDEF"
		l.acceptRun(digits)
		if !strings.ContainsAny(l.buf.String(), "0123456789abcdefABCDEF") {
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
		return l.errorf("bad number syntax: %q", l.buf.String())
	}
	l.emit(itemNumber)
	return lexStart
}

// lexString scans a double-quoted string, processing escape sequences.
func lexString(l *lexer) stateFn {
	var val strings.Builder
	for {
		r := l.next()
		switch r {
		case eof, '\n':
			return l.errorf("unterminated string")
		case '\\':
			esc := l.next()
			switch esc {
			case 'n':
				val.WriteByte('\n')
			case 't':
				val.WriteByte('\t')
			case 'r':
				val.WriteByte('\r')
			case 'a':
				val.WriteByte('\a')
			case 'b':
				val.WriteByte('\b')
			case 'f':
				val.WriteByte('\f')
			case 'v':
				val.WriteByte('\v')
			case '\\':
				val.WriteByte('\\')
			case '"':
				val.WriteByte('"')
			case '/':
				val.WriteByte('/')
			case '0', '1', '2', '3', '4', '5', '6', '7':
				oct := string(esc)
				for i := 0; i < 2; i++ {
					if r2 := l.peek(); r2 >= '0' && r2 <= '7' {
						oct += string(l.next())
					} else {
						break
					}
				}
				var v int
				fmt.Sscanf(oct, "%o", &v)
				val.WriteByte(byte(v))
			case 'x':
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
					var v int
					fmt.Sscanf(hex, "%x", &v)
					val.WriteByte(byte(v))
				} else {
					val.WriteByte('x')
				}
			default:
				val.WriteByte('\\')
				val.WriteRune(esc)
			}
		case '"':
			l.emitVal(itemString, val.String())
			return lexStart
		default:
			val.WriteRune(r)
		}
	}
}

// lexRawString scans a backtick-quoted raw string (no escape processing).
func lexRawString(l *lexer) stateFn {
	for {
		switch l.next() {
		case eof:
			return l.errorf("unterminated raw string")
		case '`':
			s := l.buf.String() // "`content`"
			l.emitVal(itemString, s[1:len(s)-1])
			return lexStart
		}
	}
}

// lexRegex scans a regex literal /pattern/ (opening / already consumed).
func lexRegex(l *lexer) stateFn {
	var val strings.Builder
	for {
		r := l.next()
		switch r {
		case eof, '\n':
			return l.errorf("unterminated regex")
		case '\\':
			next := l.next()
			if next == '/' {
				val.WriteByte('/')
			} else {
				val.WriteByte('\\')
				val.WriteRune(next)
			}
		case '/':
			l.emitVal(itemRegex, val.String())
			return lexStart
		default:
			val.WriteRune(r)
		}
	}
}

func isAlpha(r rune) bool { return r == '_' || unicode.IsLetter(r) }
func isDigit(r rune) bool { return r >= '0' && r <= '9' }

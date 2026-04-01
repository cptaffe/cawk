package main

import "fmt"

// itemType identifies the type of lexed item (internal to the lexer).
type itemType int

const (
	itemError itemType = 256 + iota // error; val is error text (start above ASCII)
	itemEOF
	itemNewline // significant newline (statement terminator)

	// Literals
	itemNumber
	itemString
	itemRegex
	itemIdent

	// Named capture group reference: $name
	itemNamedGroupRef // $identifier

	// Structural regex keywords (context-sensitive: only when followed by /)
	itemY // y/regex/ — gap iteration

	// Keywords
	itemBegin
	itemEnd
	itemIf
	itemElse
	itemWhile
	itemFor
	itemDo
	itemIn
	itemDelete
	itemPrint
	itemPrintf
	itemGetline
	itemReturn
	itemBreak
	itemContinue
	itemExit
	itemFunction

	// Two-character operators
	itemAnd        // &&
	itemOr         // ||
	itemAppend     // >>
	itemLE         // <=
	itemGE         // >=
	itemEQ         // ==
	itemNE         // !=
	itemMatch      // ~
	itemNoMatch    // !~
	itemIncr       // ++
	itemDecr       // --
	itemPow        // ^ (also **)
	itemAddAssign  // +=
	itemSubAssign  // -=
	itemMulAssign  // *=
	itemDivAssign  // /=
	itemModAssign  // %=
	itemPowAssign  // ^=
	itemPipeAppend // |&

	// Single-character tokens are their ASCII value; multi-char above.
	// These are just symbolic names:
	itemLParen    // (
	itemRParen    // )
	itemLBrace    // {
	itemRBrace    // }
	itemLBracket  // [
	itemRBracket  // ]
	itemSemicolon // ;
	itemComma     // ,
	itemDollar    // $
	itemQuestion  // ?
	itemColon     // :
	itemBang      // !
	itemPlus      // +
	itemMinus     // -
	itemStar      // *
	itemSlash     // /
	itemPercent   // %
	itemLT        // <
	itemGT        // >
	itemPipe      // |
	itemCaret     // ^
)

// item represents a token returned from the scanner.
type item struct {
	typ  itemType // The type of this item.
	val  string   // The value of this item.
	line int      // The line number of this item.
}

func (i item) String() string {
	switch i.typ {
	case itemEOF:
		return "EOF"
	case itemError:
		return fmt.Sprintf("ERROR(%s)", i.val)
	case itemNewline:
		return "NEWLINE"
	}
	if len(i.val) > 20 {
		return fmt.Sprintf("%.20q...", i.val)
	}
	return fmt.Sprintf("%q", i.val)
}

var keywords = map[string]itemType{
	"BEGIN":    itemBegin,
	"END":      itemEnd,
	"if":       itemIf,
	"else":     itemElse,
	"while":    itemWhile,
	"for":      itemFor,
	"do":       itemDo,
	"in":       itemIn,
	"delete":   itemDelete,
	"print":    itemPrint,
	"printf":   itemPrintf,
	"getline":  itemGetline,
	"return":   itemReturn,
	"break":    itemBreak,
	"continue": itemContinue,
	"exit":     itemExit,
	"function": itemFunction,
}

// structuralKeywords: y is the only structural keyword.
// x, g, v are removed (x-semantics are built into /re/ patterns;
// guards use standard AWK if ($0 ~ /re/) idiom).
var structuralKeywords = map[string]itemType{
	"y": itemY,
}

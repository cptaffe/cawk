%{
package main

import (
	"fmt"
	"strconv"
)

// pendingItem stores a pre-computed token for injection.
type pendingItem struct {
	tok int
	val yySymType
}

// yyLex bridges Rob Pike's lexer to goyacc.
type yyLex struct {
	l          *lexer
	prog       *Program
	err        string
	inPrint    bool
	printDepth int
	lastTok    int          // last token returned to parser
	pending    *pendingItem // token saved for next Lex() call
}

// concatAfter: tokens after which CONCAT_OP may be injected.
// We intentionally exclude ')' and ']' to avoid false positives
// after if/for/while conditions.
func concatAfter(tok int) bool {
	switch tok {
	case NUMBER, STRING, REGEX, IDENT:
		return true
	}
	return false
}

// concatBefore: tokens that, when seen, may trigger CONCAT_OP injection.
// We only inject for clear value-starting tokens to avoid binary op ambiguity.
func concatBefore(tok int) bool {
	switch tok {
	case NUMBER, STRING, REGEX, IDENT, int('$'):
		return true
	}
	return false
}

func (yl *yyLex) Lex(lval *yySymType) int {
	// Return pending token if any
	if yl.pending != nil {
		p := yl.pending
		yl.pending = nil
		*lval = p.val
		yl.lastTok = p.tok
		return p.tok
	}
	tok := yl.innerLex(lval)
	return yl.emitTok(tok, lval)
}

func (yl *yyLex) innerLex(lval *yySymType) int {
	for {
		it := yl.l.nextItem()

		switch it.typ {
		case itemEOF:
			yl.inPrint = false
			return 0
		case itemError:
			yl.err = it.val
			return 0
		case itemNewline, itemSemicolon:
			yl.inPrint = false
			yl.printDepth = 0
			return TERM
		case itemNumber:
			lval.val = it.val
			f, _ := strconv.ParseFloat(it.val, 64)
			lval.numval = f
			return NUMBER
		case itemString:
			lval.val = it.val
			return STRING
		case itemRegex:
			lval.val = it.val
			return REGEX
		case itemIdent:
			lval.val = it.val
			return IDENT
		case itemX:
			return X
		case itemY:
			return Y
		case itemG:
			return G
		case itemV:
			return V
		case itemBegin:
			return BEGIN
		case itemEnd:
			return END
		case itemIf:
			return IF
		case itemElse:
			return ELSE
		case itemWhile:
			return WHILE
		case itemFor:
			return FOR
		case itemDo:
			return DO
		case itemIn:
			return IN
		case itemDelete:
			return DELETE
		case itemPrint:
			yl.inPrint = true
			yl.printDepth = 0
			return PRINT
		case itemPrintf:
			yl.inPrint = true
			yl.printDepth = 0
			return PRINTF
		case itemGetline:
			return GETLINE
		case itemReturn:
			return RETURN
		case itemBreak:
			return BREAK
		case itemContinue:
			return CONTINUE
		case itemNext:
			return NEXT
		case itemNextfile:
			return NEXTFILE
		case itemExit:
			return EXIT
		case itemFunction:
			return FUNCTION
		case itemAnd:
			return AND
		case itemOr:
			return OR
		case itemAppend:
			if yl.inPrint && yl.printDepth == 0 {
				yl.inPrint = false
				lval.val = ">>"
				return REDIR
			}
			return APPEND
		case itemPipeAppend:
			return PIPEAPPEND
		case itemLE:
			return LE
		case itemGE:
			return GE
		case itemEQ:
			return EQ
		case itemNE:
			return NE
		case itemMatch:
			return int('~')
		case itemNoMatch:
			return NOMATCH
		case itemIncr:
			return INCR
		case itemDecr:
			return DECR
		case itemPow:
			return POW
		case itemAddAssign:
			return ADD_ASSIGN
		case itemSubAssign:
			return SUB_ASSIGN
		case itemMulAssign:
			return MUL_ASSIGN
		case itemDivAssign:
			return DIV_ASSIGN
		case itemModAssign:
			return MOD_ASSIGN
		case itemPowAssign:
			return POW_ASSIGN
		case itemGT:
			if yl.inPrint && yl.printDepth == 0 {
				yl.inPrint = false
				lval.val = ">"
				return REDIR
			}
			return int('>')
		case itemPipe:
			if yl.inPrint && yl.printDepth == 0 {
				yl.inPrint = false
				lval.val = "|"
				return REDIR
			}
			return int('|')
		case itemPlus:
			return int('+')
		case itemMinus:
			return int('-')
		case itemStar:
			return int('*')
		case itemSlash:
			return int('/')
		case itemPercent:
			return int('%')
		case itemBang:
			return int('!')
		case itemLT:
			return int('<')
		case itemLParen:
			if yl.inPrint {
				yl.printDepth++
			}
			return int('(')
		case itemRParen:
			if yl.inPrint && yl.printDepth > 0 {
				yl.printDepth--
			}
			return int(')')
		case itemLBrace:
			return int('{')
		case itemRBrace:
			return int('}')
		case itemLBracket:
			if yl.inPrint {
				yl.printDepth++
			}
			return int('[')
		case itemRBracket:
			if yl.inPrint && yl.printDepth > 0 {
				yl.printDepth--
			}
			return int(']')
		case itemComma:
			return int(',')
		case itemDollar:
			return int('$')
		case itemQuestion:
			return int('?')
		case itemColon:
			return int(':')
		case itemType('='):
			return int('=')
		default:
			if it.typ < 128 {
				return int(it.typ)
			}
			yl.err = fmt.Sprintf("unexpected token %v", it)
			return 0
		}
	}
}

// emitTok checks if CONCAT_OP should be injected before tok,
// saves tok as pending if so, and returns the actual token to emit.
func (yl *yyLex) emitTok(tok int, lval *yySymType) int {
	if concatAfter(yl.lastTok) && concatBefore(tok) {
		// Inject CONCAT_OP: save tok as pending and return CONCAT_OP
		savedLval := *lval
		yl.pending = &pendingItem{tok: tok, val: savedLval}
		yl.lastTok = CONCAT_OP
		return CONCAT_OP
	}
	yl.lastTok = tok
	return tok
}

func (yl *yyLex) Error(s string) {
	if yl.err == "" {
		yl.err = s
	}
}

func parseProgram(name, src string) (*Program, error) {
	l := lex(name, src)
	prog := &Program{Funcs: make(map[string]*FuncDef)}
	yl := &yyLex{l: l, prog: prog}
	yyParse(yl)
	if yl.err != "" {
		return nil, fmt.Errorf("%s", yl.err)
	}
	return prog, nil
}

// checkLvalue validates that an expression is usable as an lvalue.
func checkLvalue(e Expr) {
	switch e.(type) {
	case *IdentExpr, *FieldExpr, *IndexExpr:
		// ok
	default:
		panic(runtimeError("not an lvalue"))
	}
}

%}

%union {
	val     string
	numval  float64
	expr    Expr
	exprs   []Expr
	stmt    Stmt
	stmts   []Stmt
	strvals []string
	pattern Pattern
	rule    *Rule
	redir   *Redirect
}

%token <val>    IDENT STRING REGEX NUMBER
%token          BEGIN END IF ELSE WHILE FOR DO IN DELETE
%token          PRINT PRINTF GETLINE
%token          RETURN BREAK CONTINUE NEXT NEXTFILE EXIT FUNCTION
%token          X Y G V
%token          AND OR APPEND PIPEAPPEND
%token          LE GE EQ NE NOMATCH
%token          INCR DECR POW
%token          ADD_ASSIGN SUB_ASSIGN MUL_ASSIGN DIV_ASSIGN MOD_ASSIGN POW_ASSIGN
%token          TERM CONCAT_OP
%token <val>    REDIR

%type <expr>    expr opt_expr
%type <exprs>   expr_list nonempty_expr_list subscript opt_expr_list
%type <stmt>    stmt simple_stmt for_init if_stmt
%type <stmts>   stmts block opt_else
%type <strvals> param_list opt_param_list
%type <pattern> pattern
%type <rule>    rule
%type <redir>   opt_output_redir


/* Precedence lowest → highest */
%right '?' ':'
%right '=' ADD_ASSIGN SUB_ASSIGN MUL_ASSIGN DIV_ASSIGN MOD_ASSIGN POW_ASSIGN
%left  OR
%left  AND
%left  IN
%left  '~' NOMATCH
%left  '<' '>' LE GE EQ NE
%left  CONCAT_OP
%left  '+' '-'
%left  '*' '/' '%'
%right UMINUS UPLUS UNOT
%right POW
%left  INCR DECR
%left  '$'
%left  '['

%%

program:
    /* empty */
  | program rule
    {
        yylex.(*yyLex).prog.Rules = append(yylex.(*yyLex).prog.Rules, $2)
    }
  | program func_def
  | program TERM
  ;

rule:
    pattern block        { $$ = &Rule{Pattern: $1, Action: $2} }
  | pattern              { $$ = &Rule{Pattern: $1, Action: nil} }
  | block                { $$ = &Rule{Pattern: nil, Action: $1} }
  ;

func_def:
    FUNCTION IDENT '(' opt_param_list ')' opt_nl block
    {
        yylex.(*yyLex).prog.Funcs[$2] = &FuncDef{Name: $2, Params: $4, Body: $7}
    }
  ;

opt_param_list:
    /* empty */              { $$ = nil }
  | param_list               { $$ = $1 }
  ;

param_list:
    IDENT                        { $$ = []string{$1} }
  | param_list ',' IDENT        { $$ = append($1, $3) }
  | param_list ',' opt_nl IDENT { $$ = append($1, $4) }
  ;

pattern:
    BEGIN                 { $$ = &BeginPattern{} }
  | END                   { $$ = &EndPattern{} }
  | REGEX                 { $$ = &RegexPattern{Regex: $1} }
  | X REGEX               { $$ = &XPattern{Regex: $2} }
  | Y REGEX               { $$ = &YPattern{Regex: $2} }
  | G REGEX               { $$ = &GPattern{Regex: $2} }
  | V REGEX               { $$ = &VPattern{Regex: $2} }
  | expr                  { $$ = &ExprPattern{Expr: $1} }
  | expr ',' opt_nl expr  { $$ = &RangePattern{From: $1, To: $4} }
  ;

block:
    '{' opt_nl stmts '}'  { $$ = $3 }
  ;

opt_nl:
    /* empty */
  | opt_nl TERM
  ;

stmts:
    /* empty */            { $$ = nil }
  | stmts stmt opt_term   { if $2 != nil { $$ = append($1, $2) } else { $$ = $1 } }
  | stmts TERM             { $$ = $1 }
  ;

opt_term:
    /* empty */
  | TERM
  ;

stmt:
    simple_stmt
  | if_stmt
  | WHILE '(' expr ')' opt_nl block
    {
        $$ = &WhileStmt{Cond: $3, Body: $6}
    }
  | WHILE '(' expr ')' opt_nl simple_stmt
    {
        $$ = &WhileStmt{Cond: $3, Body: []Stmt{$6}}
    }
  | DO opt_nl block WHILE '(' expr ')' opt_term
    {
        $$ = &DoStmt{Body: $3, Cond: $6}
    }
  | FOR '(' for_init TERM opt_nl opt_expr TERM opt_nl opt_expr ')' opt_nl block
    {
        $$ = &ForStmt{Init: $3, Cond: $6, Post: $9, Body: $12}
    }
  | FOR '(' for_init TERM opt_nl opt_expr TERM opt_nl opt_expr ')' opt_nl simple_stmt
    {
        $$ = &ForStmt{Init: $3, Cond: $6, Post: $9, Body: []Stmt{$12}}
    }
  | FOR '(' IDENT IN expr ')' opt_nl block
    {
        $$ = &ForInStmt{Var: &IdentExpr{Name: $3}, Array: $5, Body: $8}
    }
  | FOR '(' IDENT IN expr ')' opt_nl simple_stmt
    {
        $$ = &ForInStmt{Var: &IdentExpr{Name: $3}, Array: $5, Body: []Stmt{$8}}
    }
  | X REGEX block  { $$ = &XStmt{Regex: $2, Body: $3} }
  | Y REGEX block  { $$ = &YStmt{Regex: $2, Body: $3} }
  | G REGEX block  { $$ = &GStmt{Regex: $2, Body: $3} }
  | V REGEX block  { $$ = &VStmt{Regex: $2, Body: $3} }
  ;

opt_else:
    /* empty */              { $$ = nil }
  | ELSE opt_nl block        { $$ = $3 }
  | ELSE opt_nl if_stmt      { $$ = []Stmt{$3} }
  | ELSE opt_nl simple_stmt  { $$ = []Stmt{$3} }
  ;

if_stmt:
    IF '(' expr ')' opt_nl block opt_else
    {
        $$ = &IfStmt{Cond: $3, Then: $6, Else: $7}
    }
  | IF '(' expr ')' opt_nl simple_stmt opt_else
    {
        $$ = &IfStmt{Cond: $3, Then: []Stmt{$6}, Else: $7}
    }
  ;

for_init:
    /* empty */  { $$ = nil }
  | expr         { $$ = &ExprStmt{Expr: $1} }
  ;

simple_stmt:
    expr
    {
        $$ = &ExprStmt{Expr: $1}
    }
  | PRINT opt_expr_list opt_output_redir
    {
        $$ = &PrintStmt{Printf: false, Args: $2, Redir: $3}
    }
  | PRINTF nonempty_expr_list opt_output_redir
    {
        $$ = &PrintStmt{Printf: true, Args: $2, Redir: $3}
    }
  | PRINT '(' nonempty_expr_list ')' opt_output_redir
    {
        $$ = &PrintStmt{Printf: false, Args: $3, Redir: $5}
    }
  | PRINTF '(' nonempty_expr_list ')' opt_output_redir
    {
        $$ = &PrintStmt{Printf: true, Args: $3, Redir: $5}
    }
  | DELETE expr
    {
        $$ = &DeleteStmt{Expr: $2}
    }
  | RETURN                  { $$ = &ReturnStmt{} }
  | RETURN expr             { $$ = &ReturnStmt{Value: $2} }
  | BREAK                   { $$ = &BreakStmt{} }
  | CONTINUE                { $$ = &ContinueStmt{} }
  | NEXT                    { $$ = &NextStmt{} }
  | NEXTFILE                { $$ = &NextfileStmt{} }
  | EXIT                    { $$ = &ExitStmt{} }
  | EXIT expr               { $$ = &ExitStmt{Status: $2} }
  ;

opt_expr_list:
    /* empty */          { $$ = nil }
  | nonempty_expr_list   { $$ = $1 }
  ;

opt_output_redir:
    /* empty */  { $$ = nil }
  | REDIR expr   { $$ = &Redirect{Mode: $1, Dest: $2} }
  ;

nonempty_expr_list:
    expr                               { $$ = []Expr{$1} }
  | nonempty_expr_list ',' opt_nl expr { $$ = append($1, $4) }
  ;

expr_list:
    /* empty */          { $$ = nil }
  | nonempty_expr_list   { $$ = $1 }
  ;

opt_expr:
    /* empty */  { $$ = nil }
  | expr         { $$ = $1 }
  ;

subscript:
    expr                 { $$ = []Expr{$1} }
  | subscript ',' expr   { $$ = append($1, $3) }
  ;

expr:
    NUMBER
    {
        $$ = &NumberExpr{Val: $<numval>1}
    }
  | STRING
    {
        $$ = &StringExpr{Val: $1}
    }
  | REGEX
    {
        $$ = &RegexExpr{Pattern: $1}
    }
  | IDENT
    {
        $$ = &IdentExpr{Name: $1}
    }
  | '$' expr %prec '$'
    {
        $$ = &FieldExpr{Index: $2}
    }
  | IDENT '[' subscript ']'
    {
        $$ = &IndexExpr{Array: &IdentExpr{Name: $1}, Indices: $3}
    }
  | '(' expr ')'
    {
        $$ = $2
    }
  | '(' nonempty_expr_list IN expr ')'
    {
        $$ = &InExpr{Key: multiExpr($2), Array: $4}
    }
  | '!' expr %prec UNOT        { $$ = &UnaryExpr{Op: "!", Operand: $2} }
  | '-' expr %prec UMINUS      { $$ = &UnaryExpr{Op: "-", Operand: $2} }
  | '+' expr %prec UPLUS       { $$ = &UnaryExpr{Op: "+", Operand: $2} }
  | expr '+' expr              { $$ = &BinaryExpr{Op: "+", Left: $1, Right: $3} }
  | expr '-' expr              { $$ = &BinaryExpr{Op: "-", Left: $1, Right: $3} }
  | expr '*' expr              { $$ = &BinaryExpr{Op: "*", Left: $1, Right: $3} }
  | expr '/' expr              { $$ = &BinaryExpr{Op: "/", Left: $1, Right: $3} }
  | expr '%' expr              { $$ = &BinaryExpr{Op: "%", Left: $1, Right: $3} }
  | expr POW expr              { $$ = &BinaryExpr{Op: "^", Left: $1, Right: $3} }
  | expr '<' expr              { $$ = &BinaryExpr{Op: "<", Left: $1, Right: $3} }
  | expr '>' expr              { $$ = &BinaryExpr{Op: ">", Left: $1, Right: $3} }
  | expr LE  expr              { $$ = &BinaryExpr{Op: "<=", Left: $1, Right: $3} }
  | expr GE  expr              { $$ = &BinaryExpr{Op: ">=", Left: $1, Right: $3} }
  | expr EQ  expr              { $$ = &BinaryExpr{Op: "==", Left: $1, Right: $3} }
  | expr NE  expr              { $$ = &BinaryExpr{Op: "!=", Left: $1, Right: $3} }
  | expr AND expr              { $$ = &BinaryExpr{Op: "&&", Left: $1, Right: $3} }
  | expr OR  expr              { $$ = &BinaryExpr{Op: "||", Left: $1, Right: $3} }
  | expr '~' expr              { $$ = &BinaryExpr{Op: "~", Left: $1, Right: $3} }
  | expr NOMATCH expr          { $$ = &BinaryExpr{Op: "!~", Left: $1, Right: $3} }
  | expr IN expr               { $$ = &InExpr{Key: $1, Array: $3} }
  | expr '?' expr ':' expr      { $$ = &TernaryExpr{Cond: $1, Then: $3, Else: $5} }
  | expr CONCAT_OP expr         { $$ = &ConcatExpr{Left: $1, Right: $3} }
  | INCR expr %prec INCR        { $$ = &IncrDecrExpr{Pre: true,  Op: "++", Operand: $2} }
  | DECR expr %prec DECR       { $$ = &IncrDecrExpr{Pre: true,  Op: "--", Operand: $2} }
  | expr INCR                  { $$ = &IncrDecrExpr{Pre: false, Op: "++", Operand: $1} }
  | expr DECR                  { $$ = &IncrDecrExpr{Pre: false, Op: "--", Operand: $1} }
  | expr '=' expr
    {
        $$ = &AssignExpr{Op: "=", Left: $1, Right: $3}
    }
  | expr ADD_ASSIGN expr       { $$ = &AssignExpr{Op: "+=", Left: $1, Right: $3} }
  | expr SUB_ASSIGN expr       { $$ = &AssignExpr{Op: "-=", Left: $1, Right: $3} }
  | expr MUL_ASSIGN expr       { $$ = &AssignExpr{Op: "*=", Left: $1, Right: $3} }
  | expr DIV_ASSIGN expr       { $$ = &AssignExpr{Op: "/=", Left: $1, Right: $3} }
  | expr MOD_ASSIGN expr       { $$ = &AssignExpr{Op: "%=", Left: $1, Right: $3} }
  | expr POW_ASSIGN expr       { $$ = &AssignExpr{Op: "^=", Left: $1, Right: $3} }
  | GETLINE                    { $$ = &GetlineExpr{} }
  | GETLINE IDENT              { $$ = &GetlineExpr{Var: &IdentExpr{Name: $2}} }
  | GETLINE '$' expr           { $$ = &GetlineExpr{Var: &FieldExpr{Index: $3}} }
  | GETLINE '<' expr           { $$ = &GetlineExpr{File: $3} }
  | GETLINE IDENT '<' expr     { $$ = &GetlineExpr{Var: &IdentExpr{Name: $2}, File: $4} }
  | GETLINE '$' expr '<' expr  { $$ = &GetlineExpr{Var: &FieldExpr{Index: $3}, File: $5} }
  | expr '|' GETLINE           { $$ = &GetlineExpr{Cmd: $1} }
  | expr '|' GETLINE IDENT     { $$ = &GetlineExpr{Cmd: $1, Var: &IdentExpr{Name: $4}} }
  | expr '|' GETLINE '$' expr  { $$ = &GetlineExpr{Cmd: $1, Var: &FieldExpr{Index: $5}} }
  | IDENT '(' expr_list ')'
    {
        $$ = &CallExpr{Name: $1, Args: $3}
    }
  ;

%%

func multiExpr(exprs []Expr) Expr {
	if len(exprs) == 1 {
		return exprs[0]
	}
	result := exprs[0]
	for _, e := range exprs[1:] {
		result = &ConcatExpr{Left: result, Right: e}
	}
	return result
}

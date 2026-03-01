package main

// Node is the interface for all AST nodes.
type Node interface {
	nodePos() int
}

// Program is the root of the AST.
type Program struct {
	Rules []*Rule
	Funcs map[string]*FuncDef
}

// Rule is a pattern-action pair.
//
// Execution model:
//   - IsBegin/IsEnd: run in the BEGIN/END phases.
//   - Regex != "": x-expression. At each stream position, try anchored match.
//     $0 = matched text, $1/$2/... = capture groups. Advance by len($0).
//   - Regex == "": implicit /[^\n]*\n/ scanner (AWK-compat bare block).
//     $0 = line WITHOUT trailing newline. Advance by len(line)+1.
type Rule struct {
	IsBegin bool
	IsEnd   bool
	Regex   string // "" = implicit line scanner (bare block)
	Action  []Stmt
}

// FuncDef is a user-defined function.
type FuncDef struct {
	Name   string
	Params []string
	Body   []Stmt
	Pos    int
}

// ---- Statements ----

// Stmt is the interface for all statement nodes.
type Stmt interface {
	stmtNode()
	nodePos() int
}

type ExprStmt struct {
	Expr Expr
	Loc  int
}

type PrintStmt struct {
	Printf bool
	Args   []Expr
	Redir  *Redirect
	Loc    int
}

type Redirect struct {
	Mode string // ">", ">>", "|", "|&"
	Dest Expr
}

type IfStmt struct {
	Cond Expr
	Then []Stmt
	Else []Stmt
	Loc  int
}

type WhileStmt struct {
	Cond Expr
	Body []Stmt
	Loc  int
}

type DoStmt struct {
	Body []Stmt
	Cond Expr
	Loc  int
}

type ForStmt struct {
	Init Stmt // ExprStmt or nil
	Cond Expr // nil means forever
	Post Expr // nil
	Body []Stmt
	Loc  int
}

type ForInStmt struct {
	Var   Expr // IdentExpr or IndexExpr
	Array Expr
	Body  []Stmt
	Loc   int
}

type DeleteStmt struct {
	Expr Expr // array or array element
	Loc  int
}

type ReturnStmt struct {
	Value Expr // nil for bare return
	Loc   int
}

type BreakStmt struct{ Loc int }
type ContinueStmt struct{ Loc int }

type ExitStmt struct {
	Status Expr // nil for bare exit
	Loc    int
}

type BlockStmt struct {
	Body []Stmt
	Loc  int
}

// RegexStmt: /re/ { body } inside a block — inner x-expression.
// Iterates over all non-overlapping matches of Regex within the current $0.
// Pushes a new MatchState for each match; pops it after the body runs.
// This is Pike's "nesting" — top-level /re/ scans the stream; nested /re/
// scans the current $0.
type RegexStmt struct {
	Regex  string
	Action []Stmt
	Loc    int
}

// YStmt: y/re/ { body } — iterate over gaps between matches of re in $0.
// (Extension from sam's y command; no AWK idiom exists for gap iteration.)
type YStmt struct {
	Regex string
	Body  []Stmt
	Loc   int
}

func (s *ExprStmt) stmtNode()     {}
func (s *PrintStmt) stmtNode()    {}
func (s *IfStmt) stmtNode()       {}
func (s *WhileStmt) stmtNode()    {}
func (s *DoStmt) stmtNode()       {}
func (s *ForStmt) stmtNode()      {}
func (s *ForInStmt) stmtNode()    {}
func (s *DeleteStmt) stmtNode()   {}
func (s *ReturnStmt) stmtNode()   {}
func (s *BreakStmt) stmtNode()    {}
func (s *ContinueStmt) stmtNode() {}
func (s *ExitStmt) stmtNode()     {}
func (s *BlockStmt) stmtNode()    {}
func (s *RegexStmt) stmtNode()    {}
func (s *YStmt) stmtNode()        {}

func (s *ExprStmt) nodePos() int     { return s.Loc }
func (s *PrintStmt) nodePos() int    { return s.Loc }
func (s *IfStmt) nodePos() int       { return s.Loc }
func (s *WhileStmt) nodePos() int    { return s.Loc }
func (s *DoStmt) nodePos() int       { return s.Loc }
func (s *ForStmt) nodePos() int      { return s.Loc }
func (s *ForInStmt) nodePos() int    { return s.Loc }
func (s *DeleteStmt) nodePos() int   { return s.Loc }
func (s *ReturnStmt) nodePos() int   { return s.Loc }
func (s *BreakStmt) nodePos() int    { return s.Loc }
func (s *ContinueStmt) nodePos() int { return s.Loc }
func (s *ExitStmt) nodePos() int     { return s.Loc }
func (s *BlockStmt) nodePos() int    { return s.Loc }
func (s *RegexStmt) nodePos() int    { return s.Loc }
func (s *YStmt) nodePos() int        { return s.Loc }

// ---- Expressions ----

// Expr is the interface for all expression nodes.
type Expr interface {
	exprNode()
	nodePos() int
}

type NumberExpr struct {
	Val float64
	Loc int
}

type StringExpr struct {
	Val string
	Loc int
}

// RegexExpr: /pattern/ used as an expression — tests match against $0.
type RegexExpr struct {
	Pattern string
	Loc     int
}

// FieldExpr: $n — $0 is the full match, $1/$2/... are capture groups.
type FieldExpr struct {
	Index Expr
	Loc   int
}

// NamedGroupExpr: $name — named capture group from the enclosing /re/ pattern.
// Reads from the top of the match stack's NamedGroups map.
type NamedGroupExpr struct {
	Name string
	Loc  int
}

type IdentExpr struct {
	Name string
	Loc  int
}

type IndexExpr struct {
	Array   Expr
	Indices []Expr
	Loc     int
}

type BinaryExpr struct {
	Op    string
	Left  Expr
	Right Expr
	Loc   int
}

type UnaryExpr struct {
	Op      string
	Operand Expr
	Loc     int
}

type TernaryExpr struct {
	Cond Expr
	Then Expr
	Else Expr
	Loc  int
}

// AssignExpr: lvalue op= rvalue (op can be "", "+", "-", "*", "/", "%", "^")
type AssignExpr struct {
	Op    string
	Left  Expr // lvalue: IdentExpr, FieldExpr, IndexExpr
	Right Expr
	Loc   int
}

// IncrDecrExpr: ++x, --x, x++, x--
type IncrDecrExpr struct {
	Pre     bool
	Op      string // "++" or "--"
	Operand Expr
	Loc     int
}

// CallExpr: function call
type CallExpr struct {
	Name string
	Args []Expr
	Loc  int
}

// GetlineExpr: getline var < file  or  cmd | getline var
// Plain `getline` (from main input) is not supported in stream mode.
type GetlineExpr struct {
	Var  Expr // nil if no variable
	File Expr // nil if no file redirect
	Cmd  Expr // nil if no pipe
	Loc  int
}

// InExpr: (key in array)
type InExpr struct {
	Key   Expr
	Array Expr
	Loc   int
}

// ConcatExpr: string concatenation
type ConcatExpr struct {
	Left  Expr
	Right Expr
	Loc   int
}

// MatchExpr: expr ~ /regex/ or expr !~ /regex/
type MatchExpr struct {
	Negate  bool
	Operand Expr
	Pattern string
	Loc     int
}

func (e *NumberExpr) exprNode()    {}
func (e *StringExpr) exprNode()    {}
func (e *RegexExpr) exprNode()     {}
func (e *FieldExpr) exprNode()     {}
func (e *NamedGroupExpr) exprNode() {}
func (e *IdentExpr) exprNode()     {}
func (e *IndexExpr) exprNode()     {}
func (e *BinaryExpr) exprNode()    {}
func (e *UnaryExpr) exprNode()     {}
func (e *TernaryExpr) exprNode()   {}
func (e *AssignExpr) exprNode()    {}
func (e *IncrDecrExpr) exprNode()  {}
func (e *CallExpr) exprNode()      {}
func (e *GetlineExpr) exprNode()   {}
func (e *InExpr) exprNode()        {}
func (e *ConcatExpr) exprNode()    {}
func (e *MatchExpr) exprNode()     {}

func (e *NumberExpr) nodePos() int    { return e.Loc }
func (e *StringExpr) nodePos() int    { return e.Loc }
func (e *RegexExpr) nodePos() int     { return e.Loc }
func (e *FieldExpr) nodePos() int     { return e.Loc }
func (e *NamedGroupExpr) nodePos() int { return e.Loc }
func (e *IdentExpr) nodePos() int     { return e.Loc }
func (e *IndexExpr) nodePos() int     { return e.Loc }
func (e *BinaryExpr) nodePos() int    { return e.Loc }
func (e *UnaryExpr) nodePos() int     { return e.Loc }
func (e *TernaryExpr) nodePos() int   { return e.Loc }
func (e *AssignExpr) nodePos() int    { return e.Loc }
func (e *IncrDecrExpr) nodePos() int  { return e.Loc }
func (e *CallExpr) nodePos() int      { return e.Loc }
func (e *GetlineExpr) nodePos() int   { return e.Loc }
func (e *InExpr) nodePos() int        { return e.Loc }
func (e *ConcatExpr) nodePos() int    { return e.Loc }
func (e *MatchExpr) nodePos() int     { return e.Loc }

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
type Rule struct {
	Pattern Pattern
	Action  []Stmt // nil means print $0
}

// FuncDef is a user-defined function.
type FuncDef struct {
	Name   string
	Params []string
	Body   []Stmt
	Pos    int
}

// ---- Patterns ----

// Pattern is the interface for all rule patterns.
type Pattern interface {
	patternNode()
	nodePos() int
}

type BeginPattern struct{ Loc int }
type EndPattern struct{ Loc int }

// ExprPattern: `expr { action }` — runs when expr is truthy.
type ExprPattern struct {
	Expr Expr
	Loc  int
}

// RangePattern: `expr, expr { action }` — range pattern.
type RangePattern struct {
	From, To Expr
	Loc      int
}

// RegexPattern: `/regex/ { action }` — structural: runs for each match in $0.
type RegexPattern struct {
	Regex string
	Loc   int
}

// XPattern: `x/regex/ { action }` — structural extract (same as RegexPattern at top level).
type XPattern struct {
	Regex string
	Loc   int
}

// YPattern: `y/regex/ { action }` — structural: gaps between matches.
type YPattern struct {
	Regex string
	Loc   int
}

// GPattern: `g/regex/ { action }` — conditional: run if $0 matches.
type GPattern struct {
	Regex string
	Loc   int
}

// VPattern: `v/regex/ { action }` — conditional: run if $0 does NOT match.
type VPattern struct {
	Regex string
	Loc   int
}

func (p *BeginPattern) patternNode()  {}
func (p *EndPattern) patternNode()    {}
func (p *ExprPattern) patternNode()   {}
func (p *RangePattern) patternNode()  {}
func (p *RegexPattern) patternNode()  {}
func (p *XPattern) patternNode()      {}
func (p *YPattern) patternNode()      {}
func (p *GPattern) patternNode()      {}
func (p *VPattern) patternNode()      {}
func (p *BeginPattern) nodePos() int  { return p.Loc }
func (p *EndPattern) nodePos() int    { return p.Loc }
func (p *ExprPattern) nodePos() int   { return p.Loc }
func (p *RangePattern) nodePos() int  { return p.Loc }
func (p *RegexPattern) nodePos() int  { return p.Loc }
func (p *XPattern) nodePos() int      { return p.Loc }
func (p *YPattern) nodePos() int      { return p.Loc }
func (p *GPattern) nodePos() int      { return p.Loc }
func (p *VPattern) nodePos() int      { return p.Loc }

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

type NextStmt struct{ Loc int }
type NextfileStmt struct{ Loc int }

type ExitStmt struct {
	Status Expr // nil for bare exit
	Loc    int
}

type BlockStmt struct {
	Body []Stmt
	Loc  int
}

// Structural regex statements (sam-style commands)

// XStmt: x/regex/ { body } — extract all matches, run body with $0=match
type XStmt struct {
	Regex string
	Body  []Stmt
	Loc   int
}

// YStmt: y/regex/ { body } — extract gaps between matches, run body with $0=gap
type YStmt struct {
	Regex string
	Body  []Stmt
	Loc   int
}

// GStmt: g/regex/ { body } — run body if current text matches regex
type GStmt struct {
	Regex string
	Body  []Stmt
	Loc   int
}

// VStmt: v/regex/ { body } — run body if current text does NOT match regex
type VStmt struct {
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
func (s *NextStmt) stmtNode()     {}
func (s *NextfileStmt) stmtNode() {}
func (s *ExitStmt) stmtNode()     {}
func (s *BlockStmt) stmtNode()    {}
func (s *XStmt) stmtNode()        {}
func (s *YStmt) stmtNode()        {}
func (s *GStmt) stmtNode()        {}
func (s *VStmt) stmtNode()        {}

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
func (s *NextStmt) nodePos() int     { return s.Loc }
func (s *NextfileStmt) nodePos() int { return s.Loc }
func (s *ExitStmt) nodePos() int     { return s.Loc }
func (s *BlockStmt) nodePos() int    { return s.Loc }
func (s *XStmt) nodePos() int        { return s.Loc }
func (s *YStmt) nodePos() int        { return s.Loc }
func (s *GStmt) nodePos() int        { return s.Loc }
func (s *VStmt) nodePos() int        { return s.Loc }

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

type RegexExpr struct {
	Pattern string
	Loc     int
}

type FieldExpr struct {
	Index Expr
	Loc   int
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
	Op  string
	Operand Expr
	Loc int
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
	Pre  bool
	Op   string // "++" or "--"
	Operand Expr
	Loc  int
}

// CallExpr: function call
type CallExpr struct {
	Name string
	Args []Expr
	Loc  int
}

// GetlineExpr: getline, getline var, getline < file, cmd | getline, cmd | getline var
type GetlineExpr struct {
	Var  Expr   // nil if no variable
	File Expr   // nil if no file redirect (<file)
	Cmd  Expr   // nil if no pipe (cmd | getline)
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

func (e *NumberExpr) exprNode()   {}
func (e *StringExpr) exprNode()   {}
func (e *RegexExpr) exprNode()    {}
func (e *FieldExpr) exprNode()    {}
func (e *IdentExpr) exprNode()    {}
func (e *IndexExpr) exprNode()    {}
func (e *BinaryExpr) exprNode()   {}
func (e *UnaryExpr) exprNode()    {}
func (e *TernaryExpr) exprNode()  {}
func (e *AssignExpr) exprNode()   {}
func (e *IncrDecrExpr) exprNode() {}
func (e *CallExpr) exprNode()     {}
func (e *GetlineExpr) exprNode()  {}
func (e *InExpr) exprNode()       {}
func (e *ConcatExpr) exprNode()   {}
func (e *MatchExpr) exprNode()    {}

func (e *NumberExpr) nodePos() int   { return e.Loc }
func (e *StringExpr) nodePos() int   { return e.Loc }
func (e *RegexExpr) nodePos() int    { return e.Loc }
func (e *FieldExpr) nodePos() int    { return e.Loc }
func (e *IdentExpr) nodePos() int    { return e.Loc }
func (e *IndexExpr) nodePos() int    { return e.Loc }
func (e *BinaryExpr) nodePos() int   { return e.Loc }
func (e *UnaryExpr) nodePos() int    { return e.Loc }
func (e *TernaryExpr) nodePos() int  { return e.Loc }
func (e *AssignExpr) nodePos() int   { return e.Loc }
func (e *IncrDecrExpr) nodePos() int { return e.Loc }
func (e *CallExpr) nodePos() int     { return e.Loc }
func (e *GetlineExpr) nodePos() int  { return e.Loc }
func (e *InExpr) nodePos() int       { return e.Loc }
func (e *ConcatExpr) nodePos() int   { return e.Loc }
func (e *MatchExpr) nodePos() int    { return e.Loc }

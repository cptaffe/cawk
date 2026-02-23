package main

// awk.go.y is named with the .go.y extension so editors apply Go syntax
// highlighting to the yacc grammar (close enough). goyacc reads it as a
// standard yacc file and writes the generated LALR(1) parser to awk.go.
//
//go:generate goyacc -o awk.go awk.go.y

.PHONY: all generate build test clean

all: build

# awk.go.y is named with .go.y extension for Go syntax highlighting in editors.
# goyacc reads it as a yacc grammar and writes the generated parser to awk.go.
generate:
	goyacc -o awk.go awk.go.y

build: generate
	go build -o cawk .

test: generate
	go test ./...

clean:
	rm -f cawk y.output

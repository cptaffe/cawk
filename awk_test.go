// awk_test.go — cawk tests based on busybox awk test suite + plan9port awk
package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// runCawk runs the cawk binary with the given program and stdin, returns stdout.
func runCawk(t *testing.T, prog string, stdin string, args ...string) string {
	t.Helper()
	cmdArgs := []string{"run", "."}
	cmdArgs = append(cmdArgs, args...)
	cmdArgs = append(cmdArgs, prog)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Dir, _ = os.Getwd()
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("cawk error: %v\nstderr: %s", err, ee.Stderr)
		}
		t.Fatalf("cawk failed: %v", err)
	}
	return string(out)
}

func runCawkFS(t *testing.T, prog, fs, stdin string) string {
	t.Helper()
	return runCawk(t, prog, stdin, "-F", fs)
}

func check(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s:\n  got:  %q\n  want: %q", name, got, want)
	}
}

// --- Busybox awk: FS tests ---

func TestFS(t *testing.T) {
	// NF for various inputs with FS=[#]
	check(t, "NF empty", runCawkFS(t, "{print NF}", "[#]", ""), "")
	check(t, "NF newline", runCawkFS(t, "{print NF}", "[#]", "\n"), "0\n")
	check(t, "NF #", runCawkFS(t, "{print NF}", "[#]", "#\n"), "2\n")
	check(t, "NF #abc#", runCawkFS(t, "{print NF}", "[#]", "#abc#\n"), "3\n")
}

// --- Arithmetic ---

func TestArithmetic(t *testing.T) {
	tests := []struct{ name, prog, want string }{
		{"add", "BEGIN{print 4+3}", "7\n"},
		{"sub", "BEGIN{print 4-3}", "1\n"},
		{"mul", "BEGIN{print 2*3}", "6\n"},
		{"div", "BEGIN{print 6/2}", "3\n"},
		{"mod", "BEGIN{print 10%3}", "1\n"},
		{"pow", "BEGIN{print 2^10}", "1024\n"},
		{"unary_neg", "BEGIN{print -5}", "-5\n"},
		{"parens", "BEGIN{print (2+3)*4}", "20\n"},
		{"pow_sub", "BEGIN{print (2^31)-1}", "2147483647\n"},
		{"float", "BEGIN{print 1.5+2.5}", "4\n"},
		{"precedence", "BEGIN{print 2+3*4}", "14\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			check(t, tc.name, runCawk(t, tc.prog, ""), tc.want)
		})
	}
}

// --- String operations ---

func TestStringOps(t *testing.T) {
	check(t, "concat", runCawk(t, `BEGIN{print "hello" " " "world"}`, ""), "hello world\n")
	check(t, "concat_var", runCawk(t, `BEGIN{a="foo"; b="bar"; print a b}`, ""), "foobar\n")
	check(t, "length", runCawk(t, `BEGIN{print length("hello")}`, ""), "5\n")
	check(t, "substr", runCawk(t, `BEGIN{print substr("hello",2,3)}`, ""), "ell\n")
	check(t, "index", runCawk(t, `BEGIN{print index("hello","ll")}`, ""), "3\n")
	check(t, "split", runCawk(t, `BEGIN{n=split("a:b:c",arr,":"); print n, arr[1], arr[2], arr[3]}`, ""), "3 a b c\n")
	check(t, "toupper", runCawk(t, `BEGIN{print toupper("hello")}`, ""), "HELLO\n")
	check(t, "tolower", runCawk(t, `BEGIN{print tolower("WORLD")}`, ""), "world\n")
	check(t, "sprintf", runCawk(t, `BEGIN{print sprintf("%05d", 42)}`, ""), "00042\n")
	check(t, "gsub", runCawk(t, `BEGIN{s="aaa"; gsub(/a/,"b",s); print s}`, ""), "bbb\n")
	check(t, "sub", runCawk(t, `BEGIN{s="aaa"; sub(/a/,"b",s); print s}`, ""), "baa\n")
	check(t, "match", runCawk(t, `BEGIN{print match("hello",/ll/)}`, ""), "3\n")
}

// --- Fields ---

func TestFields(t *testing.T) {
	check(t, "$1", runCawk(t, `{print $1}`, "hello world\n"), "hello\n")
	check(t, "$2", runCawk(t, `{print $2}`, "hello world\n"), "world\n")
	check(t, "NF", runCawk(t, `{print NF}`, "a b c\n"), "3\n")
	check(t, "last_field", runCawk(t, `{print $NF}`, "a b c\n"), "c\n")
	check(t, "FS_tab", runCawkFS(t, `{print $2}`, "\t", "a\tb\tc\n"), "b\n")
	check(t, "FS_single", runCawkFS(t, `{print NF}`, ":", "a:b:c\n"), "3\n")
	check(t, "modify_field", runCawk(t, `{$2="X"; print}`, "a b c\n"), "a X c\n")
	check(t, "NF_reduce", runCawk(t, `{NF=2; print}`, "a b c d\n"), "a b\n")
	check(t, "OFS", runCawk(t, `BEGIN{OFS="-"} {$1=$1; print}`, "a b c\n"), "a-b-c\n")
}

// --- Patterns ---

func TestPatterns(t *testing.T) {
	input := "apple\nbanana\ncherry\n"
	check(t, "regex_pat", runCawk(t, `/an/{print}`, input), "banana\n")
	check(t, "neg_pat", runCawk(t, `!/an/{print}`, input), "apple\ncherry\n")
	check(t, "expr_pat", runCawk(t, `NR==2{print}`, input), "banana\n")
	check(t, "range_pat", runCawk(t, `/apple/,/cherry/{print}`, input), "apple\nbanana\ncherry\n")
}

// --- Control flow ---

func TestControlFlow(t *testing.T) {
	check(t, "if", runCawk(t, `BEGIN{if(1)print "yes"}`, ""), "yes\n")
	check(t, "if_else", runCawk(t, `BEGIN{if(0) print "no" else print "yes"}`, ""), "yes\n")
	check(t, "while", runCawk(t, `BEGIN{i=1;while(i<=3){print i;i++}}`, ""), "1\n2\n3\n")
	check(t, "for", runCawk(t, `BEGIN{for(i=1;i<=3;i++)print i}`, ""), "1\n2\n3\n")
	check(t, "for_in", runCawk(t, `BEGIN{a[1]=1;a[2]=2;n=0;for(k in a)n++;print n}`, ""), "2\n")
	check(t, "do_while", runCawk(t, `BEGIN{i=1;do{print i;i++}while(i<=3)}`, ""), "1\n2\n3\n")
	check(t, "next", runCawk(t, `/b/{next}{print}`, "a\nb\nc\n"), "a\nc\n")
	check(t, "break", runCawk(t, `BEGIN{for(i=1;i<=5;i++){if(i==3)break;print i}}`, ""), "1\n2\n")
	check(t, "continue", runCawk(t, `BEGIN{for(i=1;i<=5;i++){if(i==3)continue;print i}}`, ""), "1\n2\n4\n5\n")
}

// --- Arrays ---

func TestArrays(t *testing.T) {
	check(t, "basic", runCawk(t, `BEGIN{a["x"]=1;a["y"]=2;print a["x"],a["y"]}`, ""), "1 2\n")
	check(t, "numeric_key", runCawk(t, `BEGIN{a[1]=10;a[2]=20;print a[1]+a[2]}`, ""), "30\n")
	check(t, "in_op", runCawk(t, `BEGIN{a["k"]=1;print ("k" in a),("z" in a)}`, ""), "1 0\n")
	check(t, "delete", runCawk(t, `BEGIN{a[1]=1;a[2]=2;delete a[1];print (1 in a),(2 in a)}`, ""), "0 1\n")
	check(t, "multi_dim", runCawk(t, `BEGIN{a[1,2]=42;print a[1,2]}`, ""), "42\n")
	check(t, "asorti", runCawk(t, `BEGIN{a["b"]=2;a["a"]=1;n=asorti(a,b);print n,b[1],b[2]}`, ""), "2 a b\n")
}

// --- Built-in functions ---

func TestBuiltins(t *testing.T) {
	check(t, "sin", runCawk(t, `BEGIN{printf "%.4f\n", sin(0)}`, ""), "0.0000\n")
	check(t, "cos", runCawk(t, `BEGIN{printf "%.4f\n", cos(0)}`, ""), "1.0000\n")
	check(t, "atan2", runCawk(t, `BEGIN{printf "%.4f\n", atan2(1,1)}`, ""), "0.7854\n")
	check(t, "exp", runCawk(t, `BEGIN{printf "%.4f\n", exp(1)}`, ""), "2.7183\n")
	check(t, "log", runCawk(t, `BEGIN{printf "%.4f\n", log(exp(1))}`, ""), "1.0000\n")
	check(t, "sqrt", runCawk(t, `BEGIN{print sqrt(4)}`, ""), "2\n")
	check(t, "int_trunc", runCawk(t, `BEGIN{print int(3.9)}`, ""), "3\n")
	check(t, "or", runCawk(t, `BEGIN{print or(3,5)}`, ""), "7\n")
	check(t, "and", runCawk(t, `BEGIN{print and(3,5)}`, ""), "1\n")
	check(t, "xor", runCawk(t, `BEGIN{print xor(3,5)}`, ""), "6\n")
	check(t, "lshift", runCawk(t, `BEGIN{print lshift(1,3)}`, ""), "8\n")
	check(t, "rshift", runCawk(t, `BEGIN{print rshift(16,2)}`, ""), "4\n")
}

// --- Printf ---

func TestPrintf(t *testing.T) {
	check(t, "%d", runCawk(t, `BEGIN{printf "%d\n", 42}`, ""), "42\n")
	check(t, "%s", runCawk(t, `BEGIN{printf "%s\n", "hello"}`, ""), "hello\n")
	check(t, "%f", runCawk(t, `BEGIN{printf "%.2f\n", 3.14159}`, ""), "3.14\n")
	check(t, "%e", runCawk(t, `BEGIN{printf "%.2e\n", 1234.5}`, ""), "1.23e+03\n")
	check(t, "%g", runCawk(t, `BEGIN{printf "%g\n", 100.0}`, ""), "100\n")
	check(t, "%x", runCawk(t, `BEGIN{printf "%x\n", 255}`, ""), "ff\n")
	check(t, "%o", runCawk(t, `BEGIN{printf "%o\n", 8}`, ""), "10\n")
	check(t, "%%", runCawk(t, `BEGIN{printf "100%%\n"}`, ""), "100%\n")
	check(t, "width", runCawk(t, `BEGIN{printf "%5d\n", 42}`, ""), "   42\n")
	check(t, "left", runCawk(t, `BEGIN{printf "%-5d|\n", 42}`, ""), "42   |\n")
	check(t, "star_width", runCawk(t, `BEGIN{printf "%*d\n", 5, 42}`, ""), "   42\n")
}

// --- NR and FNR ---

func TestNR(t *testing.T) {
	check(t, "NR", runCawk(t, `{print NR}`, "a\nb\nc\n"), "1\n2\n3\n")
	check(t, "END_NR", runCawk(t, `END{print NR}`, "a\nb\n"), "2\n")
}

// --- RS handling ---

func TestRS(t *testing.T) {
	check(t, "RS_single", runCawk(t, `BEGIN{RS=":"} {print NR, $0}`, "a:b:c"), "1 a\n2 b\n3 c\n")
}

// --- Getline ---

func TestGetline(t *testing.T) {
	check(t, "getline_basic", runCawk(t, `{getline line; print line}`, "ignored\nhello\n"), "hello\n")
}

// --- OFS and ORS ---

func TestOutput(t *testing.T) {
	check(t, "OFS", runCawk(t, `BEGIN{OFS=","} {print $1,$2,$3}`, "a b c\n"), "a,b,c\n")
	check(t, "ORS", runCawk(t, `BEGIN{ORS=";"} {print $0}`, "a\nb\n"), "a;b;")
}

// --- User-defined functions ---

func TestFunctions(t *testing.T) {
	prog := `
function max(a,b) { return a>b ? a : b }
BEGIN { print max(3,7), max(10,4) }
`
	check(t, "max", runCawk(t, prog, ""), "7 10\n")

	prog2 := `
function fact(n) { return n<=1 ? 1 : n*fact(n-1) }
BEGIN { print fact(5) }
`
	check(t, "factorial", runCawk(t, prog2, ""), "120\n")
}

// --- String comparison ---

func TestComparisons(t *testing.T) {
	check(t, "str_eq", runCawk(t, `BEGIN{if("a"=="a")print "y"}`, ""), "y\n")
	check(t, "str_lt", runCawk(t, `BEGIN{if("a"<"b")print "y"}`, ""), "y\n")
	check(t, "num_vs_str", runCawk(t, `BEGIN{if(1=="1")print "y"}`, ""), "y\n")
	check(t, "concat_num", runCawk(t, `BEGIN{x=3; print x "px"}`, ""), "3px\n")
}

// --- Regex match / no-match ---

func TestRegex(t *testing.T) {
	check(t, "match_op", runCawk(t, `BEGIN{if("hello"~/ell/)print "y"}`, ""), "y\n")
	check(t, "nomatch_op", runCawk(t, `BEGIN{if("hello"!~/xyz/)print "y"}`, ""), "y\n")
	check(t, "gsub_regex", runCawk(t, `{gsub(/[0-9]+/,"N"); print}`, "abc 123 def 456\n"), "abc N def N\n")
}

// --- Multiple rules ---

func TestMultipleRules(t *testing.T) {
	prog := `BEGIN{print "start"} {print NR, $0} END{print "end"}`
	check(t, "multi", runCawk(t, prog, "a\nb\n"), "start\n1 a\n2 b\nend\n")
}

// --- Ternary ---

func TestTernary(t *testing.T) {
	check(t, "ternary", runCawk(t, `BEGIN{x=5; print (x>3 ? "big" : "small")}`, ""), "big\n")
}

// --- Numeric string comparison ---

func TestNumericStrings(t *testing.T) {
	// Input fields are numeric strings — compare numerically when both look numeric
	check(t, "numstr_cmp", runCawk(t, `{if($1>$2) print "yes" else print "no"}`, "10 9\n"), "yes\n")
}

// --- sub/gsub with & ---

func TestSubgsub(t *testing.T) {
	check(t, "sub_amp", runCawk(t, `BEGIN{s="hello"; sub(/l+/,"[&]",s); print s}`, ""), "he[ll]o\n")
	check(t, "gsub_amp", runCawk(t, `BEGIN{s="aa"; gsub(/a/,"[&]",s); print s}`, ""), "[a][a]\n")
}

// --- ARGV/ARGC ---

func TestARGC(t *testing.T) {
	check(t, "ARGC", runCawk(t, `BEGIN{print ARGC}`, ""), "1\n")
}

// --- Structural regex: x command ---

func TestStructuralX(t *testing.T) {
	// x/regex/ loops over matches in $0
	prog := `{ x/[0-9]+/ { print $0 } }`
	out := runCawk(t, prog, "foo 123 bar 456\n")
	if !strings.Contains(out, "123") || !strings.Contains(out, "456") {
		t.Errorf("x/regex/ failed: got %q", out)
	}
}

// --- Structural regex: g/v commands ---

func TestStructuralGV(t *testing.T) {
	prog := `{g/[0-9]/{print "has digit"}}`
	check(t, "g_cmd", runCawk(t, prog, "abc123\nxyz\n"), "has digit\n")

	prog2 := `{v/[0-9]/{print "no digit"}}`
	check(t, "v_cmd", runCawk(t, prog2, "abc123\nxyz\n"), "no digit\n")
}

// --- Pipe I/O ---

func TestPipe(t *testing.T) {
	check(t, "print_pipe", runCawk(t, `BEGIN{print "hello" | "cat"}`, ""), "hello\n")
}

// --- Deletion ---

func TestDelete(t *testing.T) {
	check(t, "delete_arr", runCawk(t, `BEGIN{a[1]=1;a[2]=2;delete a;print length(a)}`, ""), "0\n")
}

// --- exit ---

func TestExit(t *testing.T) {
	check(t, "exit_begin", runCawk(t, `BEGIN{print "a"; exit} END{print "b"}`, ""), "a\nb\n")
}

// --- Additional busybox awk tests ---

func TestGetlineFile(t *testing.T) {
	// Create temp file
	f, err := os.CreateTemp("", "cawk_test_*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("line1\nline2\nline3\n")
	f.Close()

	prog := `BEGIN{while((getline line < "` + f.Name() + `") > 0) print line}`
	check(t, "getline_file", runCawk(t, prog, ""), "line1\nline2\nline3\n")
}

func TestGetlineCmd(t *testing.T) {
	check(t, "getline_pipe", runCawk(t, `BEGIN{"echo hello" | getline s; print s}`, ""), "hello\n")
}

func TestPrintRedirect(t *testing.T) {
	f, err := os.CreateTemp("", "cawk_out_*.txt")
	if err != nil {
		t.Fatal(err)
	}
	name := f.Name()
	f.Close()
	defer os.Remove(name)

	prog := `BEGIN{print "hello" > "` + name + `"}`
	runCawk(t, prog, "")
	data, _ := os.ReadFile(name)
	check(t, "print_redirect", string(data), "hello\n")
}

func TestPrintAppend(t *testing.T) {
	f, err := os.CreateTemp("", "cawk_app_*.txt")
	if err != nil {
		t.Fatal(err)
	}
	name := f.Name()
	f.WriteString("first\n")
	f.Close()
	defer os.Remove(name)

	prog := `BEGIN{print "second" >> "` + name + `"}`
	runCawk(t, prog, "")
	data, _ := os.ReadFile(name)
	check(t, "print_append", string(data), "first\nsecond\n")
}

func TestFieldAssign(t *testing.T) {
	check(t, "field_extend", runCawk(t, `{$5="x"; print NF,$0}`, "a b c\n"), "5 a b c  x\n")
	check(t, "field_rebuild", runCawk(t, `BEGIN{OFS="-"}{$1=$1; print}`, "a b c\n"), "a-b-c\n")
	check(t, "dollar0_assign", runCawk(t, `{$0="x y z"; print NF,$1}`, "a\n"), "3 x\n")
}

func TestRangePattern(t *testing.T) {
	input := "a\nSTART\nb\nc\nEND\nd\n"
	check(t, "range", runCawk(t, `/START/,/END/{print}`, input), "START\nb\nc\nEND\n")
}

func TestSpecialVars(t *testing.T) {
	check(t, "NF_field", runCawk(t, `{x=$NF; print x}`, "a b c\n"), "c\n")
	check(t, "FILENAME", runCawk(t, `{print FILENAME}`, "x\n"), "-\n")
}

func TestArrayFunctions(t *testing.T) {
	check(t, "length_arr", runCawk(t, `BEGIN{a[1]=1;a[2]=2;a[3]=3; print length(a)}`, ""), "3\n")
	check(t, "multi_idx", runCawk(t, `BEGIN{a["x","y"]=42; print a["x","y"]}`, ""), "42\n")
}

func TestStringConcat(t *testing.T) {
	check(t, "num_str", runCawk(t, `BEGIN{x=3; print x "px"}`, ""), "3px\n")
	check(t, "str_num", runCawk(t, `BEGIN{x="v"; n=42; print x n}`, ""), "v42\n")
	check(t, "multi_concat", runCawk(t, `BEGIN{print "a" "b" "c"}`, ""), "abc\n")
}

func TestMath(t *testing.T) {
	check(t, "rand_range", runCawk(t, `BEGIN{srand(42); x=rand(); print (x>=0 && x<1)}`, ""), "1\n")
	check(t, "int_floor", runCawk(t, `BEGIN{print int(-3.9)}`, ""), "-3\n")
	check(t, "atan2_pi", runCawk(t, `BEGIN{printf "%.6f\n", atan2(0,-1)}`, ""), "3.141593\n")
}

func TestControlFlow2(t *testing.T) {
	// Nested for with break/continue
	prog := `BEGIN{
		for(i=1;i<=3;i++){
			for(j=1;j<=3;j++){
				if(j==2)continue
				if(i==2&&j==3)break
				printf "%d%d\n",i,j
			}
		}
	}`
	out := runCawk(t, prog, "")
	if !strings.Contains(out, "11") || !strings.Contains(out, "13") || !strings.Contains(out, "21") {
		t.Errorf("nested for: got %q", out)
	}
}

func TestPrintFormats(t *testing.T) {
	// Number output format
	check(t, "int_output", runCawk(t, `BEGIN{print 1.0}`, ""), "1\n")
	check(t, "float_output", runCawk(t, `BEGIN{print 1.5}`, ""), "1.5\n")
	check(t, "large_int", runCawk(t, `BEGIN{print 1000000}`, ""), "1000000\n")
}

func TestStructuralY(t *testing.T) {
	// y/regex/ loops over gaps between matches
	prog := `{ y/[0-9]+/ { printf "%s\n", $0 } }`
	out := runCawk(t, prog, "foo 123 bar 456\n")
	if !strings.Contains(out, "foo ") || !strings.Contains(out, " bar ") {
		t.Errorf("y/regex/ failed: got %q", out)
	}
}

func TestDeleteStatement(t *testing.T) {
	// delete whole array
	check(t, "delete_whole", runCawk(t, `BEGIN{a[1]=1;a[2]=2;delete a;print length(a)}`, ""), "0\n")
	// delete element
	check(t, "delete_elem", runCawk(t, `BEGIN{a[1]=1;a[2]=2;delete a[1];print (1 in a),(2 in a)}`, ""), "0 1\n")
}

func TestGsub(t *testing.T) {
	check(t, "gsub_dollar", runCawk(t, `{gsub(/a/,"b"); print}`, "aaa\n"), "bbb\n")
	check(t, "gsub_special", runCawk(t, `BEGIN{s="a.b"; gsub(/\./,"_",s); print s}`, ""), "a_b\n")
}

func TestFunctionRecursion(t *testing.T) {
	prog := `
function fib(n) {
	if(n<=1) return n
	return fib(n-1)+fib(n-2)
}
BEGIN{print fib(10)}
`
	check(t, "fib", runCawk(t, prog, ""), "55\n")
}

func TestMultilineProgram(t *testing.T) {
	prog := `
BEGIN {
	FS = ":"
	count = 0
}
{
	count++
	total += $2
}
END {
	print count, total/count
}
`
	check(t, "multiline", runCawk(t, prog, "a:10\nb:20\nc:30\n"), "3 20\n")
}

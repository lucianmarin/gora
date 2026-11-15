package main

import (
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltInConformance(t *testing.T) {
	if err := runBuiltInTests(); err != nil {
		t.Fatal(err)
	}
}

func TestExamplesRoundTrip(t *testing.T) {
	files, err := filepath.Glob("examples/*.gora")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("expected example .gora files")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			go1, err := GoraToGo(string(raw))
			if err != nil {
				t.Fatalf("GoraToGo(%s): %v", f, err)
			}
			fset := token.NewFileSet()
			if _, err := parser.ParseFile(fset, f+".go", go1, parser.AllErrors); err != nil {
				t.Fatalf("ParseFile(%s): %v\n%s", f, err, go1)
			}
			norm1, err := format.Source([]byte(go1))
			if err != nil {
				t.Fatalf("gofmt1(%s): %v\n%s", f, err, go1)
			}

			goraBack, err := GoToGoraIndent(go1, "    ")
			if err != nil {
				t.Fatalf("GoToGora(%s): %v", f, err)
			}
			go2, err := GoraToGo(goraBack)
			if err != nil {
				t.Fatalf("roundtrip GoraToGo(%s): %v\n%s", f, err, goraBack)
			}
			norm2, err := format.Source([]byte(go2))
			if err != nil {
				t.Fatalf("gofmt2(%s): %v\n%s", f, err, go2)
			}
			if string(norm1) != string(norm2) {
				t.Fatalf("roundtrip mismatch for %s:\n--- Go1 ---\n%s--- GoraBack ---\n%s--- Go2 ---\n%s",
					f, string(norm1), goraBack, string(norm2))
			}
		})
	}
}

func TestFStringAutoFmtImportAndSpecs(t *testing.T) {
	src := "def msg(name: str, pi: f64) -> str:\n    return f\"hi {name}, {pi:.2f} 100% {{ok}}\"\n"
	goCode, err := GoraToGo(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goCode, `import "fmt"`) {
		t.Fatalf("expected auto-inserted fmt import, got:\n%s", goCode)
	}
	if !strings.Contains(goCode, `fmt.Sprintf("hi %v, %.2f 100%% {ok}", name, pi)`) {
		t.Fatalf("unexpected sprintf output:\n%s", goCode)
	}
}

func TestUniversalNewlinesAndExplicitPackage(t *testing.T) {
	src := "package util\r\n\r\ndef add(a: i32, b: i32) -> i32:\r\n    return a + b\r"
	goCode, err := GoraToGo(src)
	if err != nil {
		t.Fatal(err)
	}
	want := "package util\n\nfunc add(a int32, b int32) int32 {\n    return a + b\n}\n"
	if goCode != want {
		t.Fatalf("want:\n%s\ngot:\n%s", want, goCode)
	}
}

func TestLambdaAsArgumentAndGroupedGoParams(t *testing.T) {
	goSrc := "package main\n\nfunc apply(x, y int32, fn func(int32, int32) int32) int32 {\n    return fn(x, y)\n}\n"
	goraSrc, err := GoToGora(goSrc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goraSrc, "def apply(x: i32, y: i32, fn: fn(i32, i32) -> i32) -> i32:") {
		t.Fatalf("unexpected GoToGora output:\n%s", goraSrc)
	}
	goBack, err := GoraToGo(goraSrc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goBack, "func apply(x int32, y int32, fn func(int32, int32) int32) int32 {") {
		t.Fatalf("unexpected GoraToGo output:\n%s", goBack)
	}
}

func TestTryRequiresErrorReturn(t *testing.T) {
	src := "def bad() -> i32:\n    x := try something()\n    return x\n"
	if _, err := GoraToGo(src); err == nil {
		t.Fatal("expected error when 'try' is used without error return type")
	}
}

func TestRejectUTF8BOM(t *testing.T) {
	src := "\xEF\xBB\xBFdef main():\n    pass\n"
	if _, err := GoraToGo(src); err == nil {
		t.Fatal("expected UTF-8 BOM to be rejected")
	}
}

func TestChannelPutAndGet(t *testing.T) {
	src := "def run(ch: chan[i32], getCh: fn() -> chan[i32]):\n    ch.put(1)\n    x := ch.get()\n    getCh().put(x)\n    y := getCh().get()\n    _ = y\n"
	goCode, err := GoraToGo(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goCode, "ch <- 1\n    x := <-ch\n    getCh() <- x\n    y := <-getCh()") {
		t.Fatalf("unexpected Go output:\n%s", goCode)
	}
	goraBack, err := GoToGora(goCode)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goraBack, "ch.put(1)\n    x := ch.get()\n    getCh().put(x)\n    y := getCh().get()") {
		t.Fatalf("unexpected GoToGora output:\n%s", goraBack)
	}
}

func TestMembershipNegationAndNestedIn(t *testing.T) {
	src := "import fmt\n\ndef check(m: dict[str, i32]):\n    if not \"a\" in m:\n        fmt.Println(\"a\" in m)\n    ok := not \"b\" in m and (\"c\" in m or \"d\" not in m)\n    _ = ok\n"
	goCode, err := GoraToGo(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goCode, `if _, ok := m["a"]; !ok {`) {
		t.Fatalf("expected negated map check in if condition, got:\n%s", goCode)
	}
	if !strings.Contains(goCode, `fmt.Println(_goraIn(m, "a"))`) {
		t.Fatalf("expected _goraIn inside call argument, got:\n%s", goCode)
	}
	if !strings.Contains(goCode, `!_goraIn(m, "b") && (_goraIn(m, "c") || !_goraIn(m, "d"))`) {
		t.Fatalf("expected compound _goraIn expression, got:\n%s", goCode)
	}
}

func TestRangeBlankAndMultiVarStructRoundTrip(t *testing.T) {
	goSrc := "package main\n\ntype Pt struct {\n\tx, y float64\n}\n\nfunc run(n int) Pt {\n\tvar a, b int32 = 1, 2\n\t_ = a + b\n\tfor _i := 2; _i < n; _i++ {\n\t}\n\treturn Pt{}\n}\n"
	goraSrc, err := GoToGora(goSrc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goraSrc, "x, y: f64") || !strings.Contains(goraSrc, "a, b: i32 = 1, 2") {
		t.Fatalf("expected multi-name struct/var declarations, got:\n%s", goraSrc)
	}
	if !strings.Contains(goraSrc, "for _ in range(2, n):") {
		t.Fatalf("expected _ loop variable restored for range(2, n), got:\n%s", goraSrc)
	}
	if !strings.Contains(goraSrc, "return Pt{}") {
		t.Fatalf("expected empty composite literal Pt{} preserved, got:\n%s", goraSrc)
	}
	goBack, err := GoraToGo(goraSrc)
	if err != nil {
		t.Fatalf("GoraToGo failed: %v\n%s", err, goraSrc)
	}
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "out.go", goBack, parser.AllErrors); err != nil {
		t.Fatalf("invalid Go output: %v\n%s", err, goBack)
	}
}

func TestExtendedPythonicFeatures(t *testing.T) {
	src := `def build(n: isize) -> list[isize]:
    return [
        x + y
        for x in range(n)
        for y in range(2)
        if x + y > 0
    ]

def main():
    doc := """hello
world"""
    nums := build(3)
    tag := "big" if nums[-1] > 2 else "small"
    var p: addr[isize] = nil
    ok := 0 < nums[0] < 10 and not 1 == 2 and p is nil
    fval := -7 // 2
    pval := 2 ** 3
    m := dict[str, isize]{"a": 1, "b": 2}
    del m["a"]
    total := 0
    for a, b in zip(nums, nums):
        total += a + b
    for r in reversed(nums):
        total += r
    if ok and isinstance(any("hi"), str):
        print(f'doc={len(doc)} tag={tag} f={fval} p={pval} total={total} m={len(m)}')
`
	goCode, err := GoraToGo(src)
	if err != nil {
		t.Fatalf("GoraToGo failed: %v", err)
	}
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "py.go", goCode, parser.AllErrors); err != nil {
		t.Fatalf("invalid Go output: %v\n%s", err, goCode)
	}
	if _, err := format.Source([]byte(goCode)); err != nil {
		t.Fatalf("gofmt failed: %v\n%s", err, goCode)
	}
	if !strings.Contains(goCode, `import "fmt"`) || !strings.Contains(goCode, `import "math"`) {
		t.Fatalf("expected auto fmt and math imports, got:\n%s", goCode)
	}
}



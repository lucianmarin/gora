package main

import (
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func printUsage(prog string) {
	fmt.Fprintf(os.Stderr,
		"Usage:\n"+
			"  %s <file.gora>                      Transpile Gora to Go on stdout\n"+
			"  %s --gora [--indent <N|tab>] <f>    Format/canonicalize Gora\n"+
			"  %s --from-go [--indent <N|tab>] <f> Convert Go source to Gora\n"+
			"  %s --compile <file.gora> [-o <bin>] Transpile and compile via go build\n"+
			"  %s --run <file.gora>                Transpile and execute via go run\n"+
			"  %s --check <file.gora>              Validate Gora syntax & Go AST\n"+
			"  %s --test                           Run built-in conformance test suite\n"+
			"  %s --bench [file.gora]              Run transpilation throughput benchmark\n",
		prog, prog, prog, prog, prog, prog, prog, prog)
}

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Args[0])
		os.Exit(1)
	}

	modeGora := false
	modeFromGo := false
	modeCheck := false
	modeCompile := false
	modeRun := false
	indentOpt := "    "
	inputPath := ""
	outputBin := ""

	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch arg {
		case "--test":
			if err := runBuiltInTests(); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			return
		case "--bench":
			bpath := ""
			if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				i++
				bpath = os.Args[i]
			}
			if err := runBench(bpath); err != nil {
				fmt.Fprintf(os.Stderr, "gora bench error: %v\n", err)
				os.Exit(1)
			}
			return
		case "--gora":
			modeGora = true
		case "--from-go":
			modeFromGo = true
		case "--check":
			modeCheck = true
		case "--compile":
			modeCompile = true
		case "--run":
			modeRun = true
		case "-o":
			if i+1 >= len(os.Args) {
				fmt.Fprintln(os.Stderr, "gora error: missing argument for -o")
				os.Exit(1)
			}
			i++
			outputBin = os.Args[i]
		case "--indent":
			if i+1 >= len(os.Args) {
				fmt.Fprintln(os.Stderr, "gora error: missing argument for --indent")
				os.Exit(1)
			}
			i++
			val := os.Args[i]
			if val == "tab" || val == `\t` {
				indentOpt = "\t"
			} else {
				n, err := strconv.Atoi(val)
				if err != nil || n <= 0 || n >= 64 {
					fmt.Fprintln(os.Stderr, "gora error: --indent must be 1..63 or 'tab'")
					os.Exit(1)
				}
				indentOpt = strings.Repeat(" ", n)
			}
		case "--help", "-h":
			printUsage(os.Args[0])
			return
		default:
			if strings.HasPrefix(arg, "-") {
				fmt.Fprintf(os.Stderr, "gora error: unknown option '%s'\n", arg)
				os.Exit(1)
			}
			inputPath = arg
		}
	}

	if inputPath == "" {
		fmt.Fprintln(os.Stderr, "gora error: missing input file")
		os.Exit(1)
	}

	srcBytes, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gora error: cannot read '%s': %v\n", inputPath, err)
		os.Exit(1)
	}
	src := string(srcBytes)

	if modeFromGo {
		out, err := GoToGoraIndent(src, indentOpt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gora error:%v\n", err)
			os.Exit(1)
		}
		fmt.Print(out)
		return
	}

	if modeGora {
		out, err := FormatGoraIndent(src, indentOpt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gora error:%v\n", err)
			os.Exit(1)
		}
		fmt.Print(out)
		return
	}

	goCode, err := GoraToGo(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gora error:%v\n", err)
		os.Exit(1)
	}

	if modeCheck {
		fset := token.NewFileSet()
		if _, perr := parser.ParseFile(fset, inputPath, goCode, parser.AllErrors); perr != nil {
			fmt.Fprintf(os.Stderr, "gora check error: %v\n", perr)
			os.Exit(1)
		}
		return
	}

	if modeCompile || modeRun {
		tmpFile, err := os.CreateTemp("", "gora_*.go")
		if err != nil {
			fmt.Fprintf(os.Stderr, "gora error: failed to create temporary Go file: %v\n", err)
			os.Exit(1)
		}
		tmpPath := tmpFile.Name()
		if _, err := tmpFile.WriteString(goCode); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			fmt.Fprintf(os.Stderr, "gora error: failed to write temporary Go file: %v\n", err)
			os.Exit(1)
		}
		tmpFile.Close()
		defer os.Remove(tmpPath)

		if modeCompile {
			binPath := outputBin
			if binPath == "" {
				binPath = "a.out"
			}
			cmd := exec.Command("go", "build", "-o", binPath, tmpPath)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				os.Exit(1)
			}
			return
		}

		cmd := exec.Command("go", "run", tmpPath)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			os.Exit(1)
		}
		return
	}

	fmt.Print(goCode)
}

func runBuiltInTests() error {
	passed := 0
	for _, tc := range conformanceRoundTripCases() {
		go1, err := GoraToGo(tc.gora)
		if err != nil {
			return fmt.Errorf("FAIL [%s GoraToGo]: %w", tc.name, err)
		}
		if tc.wantGo != "" && go1 != tc.wantGo {
			return fmt.Errorf("FAIL [%s Go mismatch]\n--- Want ---\n%s--- Got ---\n%s", tc.name, tc.wantGo, go1)
		}
		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, tc.name+".go", go1, parser.AllErrors); err != nil {
			return fmt.Errorf("FAIL [%s invalid Go]: %w\n%s", tc.name, err, go1)
		}
		norm1, err := format.Source([]byte(go1))
		if err != nil {
			return fmt.Errorf("FAIL [%s gofmt1]: %w\n%s", tc.name, err, go1)
		}
		for _, indent := range []string{"    ", "\t"} {
			goraBack, err := GoToGoraIndent(go1, indent)
			if err != nil {
				return fmt.Errorf("FAIL [%s GoToGora]: %w", tc.name, err)
			}
			go2, err := GoraToGo(goraBack)
			if err != nil {
				return fmt.Errorf("FAIL [%s roundtrip GoraToGo]: %w\n%s", tc.name, err, goraBack)
			}
			norm2, err := format.Source([]byte(go2))
			if err != nil {
				return fmt.Errorf("FAIL [%s gofmt2]: %w\n%s", tc.name, err, go2)
			}
			if string(norm1) != string(norm2) {
				return fmt.Errorf("FAIL [%s roundtrip mismatch]\n--- Go1 ---\n%s--- GoraBack ---\n%s--- Go2 ---\n%s",
					tc.name, string(norm1), goraBack, string(norm2))
			}
		}
		passed++
	}

	invalidCases := []string{
		"    x: i32 = 1\n",
		"def foo():\n    x: i32 = 1\n  y: i32 = 2\n",
		"def foo():\n\tx: i32 = 1\n    y: i32 = 2\n",
		"def foo():\n \tx: i32 = 1\n",
		"def dangling():\n",
		"def dangling():\nx: i32 = 1\n",
		"x: i32 = 1   \n",
		"def bad():\n    try something()\n",
	}
	for i, inv := range invalidCases {
		if out, err := GoraToGo(inv); err == nil {
			return fmt.Errorf("FAIL [invalid %d accepted]: %q -> %q", i, inv, out)
		}
		passed++
	}

	fmt.Printf("Gora: all %d conformance tests passed.\n", passed)
	return nil
}

type roundTripCase struct {
	name   string
	gora    string
	wantGo string
}

func conformanceRoundTripCases() []roundTripCase {
	return []roundTripCase{
		{
			name: "hello_and_fstring",
			gora: "import fmt\n\nconst MAX_GREETINGS = 3\n\ndef greet(name: str, count: i32) -> i32:\n" +
				"    if name == \"\" or count <= 0:\n        return 0\n" +
				"    for i in range(count):\n        fmt.Println(f\"[{i + 1}] Hello from Gora, {name}!\")\n" +
				"    return count\n\ndef main():\n    sent := greet(\"World\", MAX_GREETINGS)\n" +
				"    if sent > 0 and not (sent > 10):\n        fmt.Println(sent)\n",
		},
		{
			name: "structs_methods_interfaces_match",
			gora: "struct Vec2:\n    x: f64\n    y: f64\n\n" +
				"interface Shape:\n    area() -> f64\n\n" +
				"def (v: addr[Vec2]) translate(dx: f64, dy: f64):\n    v.x += dx\n    v.y += dy\n\n" +
				"def classify(n: i32) -> str:\n    match n:\n        case 0:\n            return \"zero\"\n" +
				"        case 1, 2:\n            return \"small\"\n        default:\n            return \"many\"\n",
		},
		{
			name: "comprehensions_and_membership",
			gora: "def demo():\n" +
				"    squares: list[i32] = [i * i for i in range(6) if i % 2 == 0]\n" +
				"    uniq: set[i32] = {x for x in squares}\n" +
				"    mapping: dict[i32, i32] = {x: x + 1 for x in uniq if x < 100}\n" +
				"    if 4 in mapping:\n        _ = mapping[4]\n" +
				"    if 5 not in uniq:\n        _ = len(squares)\n",
		},
		{
			name: "try_ternary_lambda_tuple",
			gora: "import errors\n\ndef parsePos(n: i32) -> i32, error:\n" +
				"    if n < 0:\n        return 0, errors.New(\"neg\")\n" +
				"    return n * 2, nil\n\n" +
				"def run(n: i32) -> i32, error:\n" +
				"    v := try parsePos(n)\n" +
				"    inc := lambda x: i32 -> i32: x + 1\n" +
				"    res: i32 = inc(v) if v > 10 else 0\n" +
				"    return res, nil\n",
		},
		{
			name: "while_loop_channels_select",
			gora: "def worker(ch: out_chan[i32], done: in_chan[bool]):\n" +
				"    i := 0\n" +
				"    while i < 3:\n        ch.put(i)\n        i++\n" +
				"    loop:\n        select:\n            case done.get():\n                return\n            default:\n                return\n",
		},
	}
}

func runBench(path string) error {
	var gora4 string
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		gora4 = string(b)
	} else {
		var sb strings.Builder
		sb.WriteString("import:\n    fmt\n\n")
		sb.WriteString("struct Node:\n    id: i32\n    weight: f64\n    active: bool\n    label: str\n\n")
		for i := 0; i < 250; i++ {
			fmt.Fprintf(&sb,
				"def computeLayer%d(nodes: list[Node], threshold: f64) -> f64:\n"+
					"    acc: f64 = 0.0\n"+
					"    for n in nodes:\n"+
					"        if n.active and n.weight > threshold:\n"+
					"            acc += n.weight * 1.25\n"+
					"        elif not n.active or n.id == %d:\n"+
					"            acc -= 0.5\n"+
					"        else:\n"+
					"            acc += 0.1\n"+
					"    return acc\n\n", i, i)
		}
		gora4 = sb.String()
	}

	goCode, err := GoraToGo(gora4)
	if err != nil {
		return err
	}
	goraTab, err := GoToGoraIndent(goCode, "\t")
	if err != nil {
		return err
	}

	iters := 200
	t0 := time.Now()
	for i := 0; i < iters; i++ {
		if _, err := GoraToGo(gora4); err != nil {
			return err
		}
	}
	dt4 := time.Since(t0).Seconds()

	t0 = time.Now()
	for i := 0; i < iters; i++ {
		if _, err := GoraToGo(goraTab); err != nil {
			return err
		}
	}
	dtTab := time.Since(t0).Seconds()

	t0 = time.Now()
	for i := 0; i < iters; i++ {
		if _, err := GoToGoraIndent(goCode, "    "); err != nil {
			return err
		}
	}
	dtFromGo := time.Since(t0).Seconds()

	mib4 := (float64(len(gora4)*iters) / (1024.0 * 1024.0)) / dt4
	mibTab := (float64(len(goraTab)*iters) / (1024.0 * 1024.0)) / dtTab
	mibGo := (float64(len(goCode)*iters) / (1024.0 * 1024.0)) / dtFromGo

	fmt.Printf("Gora Benchmark (250 functions, %d iterations):\n", iters)
	fmt.Printf("  Go source size         : %d bytes (%.1f KiB)\n", len(goCode), float64(len(goCode))/1024.0)
	fmt.Printf("  Gora 4-space size       : %d bytes (%.1f KiB, %+.1f%% vs Go)\n",
		len(gora4), float64(len(gora4))/1024.0, 100.0*float64(len(gora4)-len(goCode))/float64(len(goCode)))
	fmt.Printf("  Gora tab (\\t) size      : %d bytes (%.1f KiB, %+.1f%% vs Go)\n",
		len(goraTab), float64(len(goraTab))/1024.0, 100.0*float64(len(goraTab)-len(goCode))/float64(len(goCode)))
	fmt.Printf("  Gora (4-sp) -> Go       : %.0f files/s (%.1f MiB/s)\n", float64(iters)/dt4, mib4)
	fmt.Printf("  Gora (tab)  -> Go       : %.0f files/s (%.1f MiB/s)\n", float64(iters)/dtTab, mibTab)
	fmt.Printf("  Go -> Gora (4-sp)       : %.0f files/s (%.1f MiB/s)\n", float64(iters)/dtFromGo, mibGo)
	return nil
}

type GoraError struct {
	Message string
	Line    int
	Col     int
}

func (e *GoraError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%d:%d: %s", e.Line, e.Col, e.Message)
	}
	return e.Message
}

var goraToGoTypeMap = map[string]string{
	"i8":    "int8",
	"i16":   "int16",
	"i32":   "int32",
	"i64":   "int64",
	"u8":    "uint8",
	"u16":   "uint16",
	"u32":   "uint32",
	"u64":   "uint64",
	"f32":   "float32",
	"f64":   "float64",
	"isize": "int",
	"usize": "uint",
	"str":   "string",
}

var goToGoraTypeMap = map[string]string{
	"int8":    "i8",
	"int16":   "i16",
	"int32":   "i32",
	"int64":   "i64",
	"uint8":   "u8",
	"uint16":  "u16",
	"uint32":  "u32",
	"uint64":  "u64",
	"float32": "f32",
	"float64": "f64",
	"string":  "str",
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// findMatchingDelim finds the matching closing delimiter starting at openIdx.
// Respects double quotes, single quotes, and backtick raw strings.
func findMatchingDelim(s string, openIdx int, openCh, closeCh byte) int {
	depth := 0
	var quote byte
	for i := openIdx; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
		} else if c == openCh {
			depth++
		} else if c == closeCh {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// findTopLevelSep finds the first occurrence of sep at bracket depth 0 outside strings.
func findTopLevelSep(s string, sep byte) int {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
		} else if c == '(' || c == '[' || c == '{' {
			depth++
		} else if c == ')' || c == ']' || c == '}' {
			if depth > 0 {
				depth--
			}
		} else if c == sep && depth == 0 {
			return i
		}
	}
	return -1
}

// findTopLevelKeyword finds `kw` surrounded by token boundaries at bracket depth 0 outside strings.
func findTopLevelKeyword(s string, kw string) int {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '(' || c == '[' || c == '{' {
			depth++
			continue
		}
		if c == ')' || c == ']' || c == '}' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 && i+len(kw) <= len(s) && s[i:i+len(kw)] == kw {
			leftOK := i == 0 || (!isIdentCont(s[i-1]) && s[i-1] != '.')
			rightOK := i+len(kw) == len(s) || !isIdentCont(s[i+len(kw)])
			if leftOK && rightOK {
				return i
			}
		}
	}
	return -1
}

func splitTopLevel(s string, sep byte) []string {
	var parts []string
	start := 0
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
		} else if c == '(' || c == '[' || c == '{' {
			depth++
		} else if c == ')' || c == ']' || c == '}' {
			if depth > 0 {
				depth--
			}
		} else if c == sep && depth == 0 {
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, strings.TrimSpace(s[start:]))
	return parts
}

func translateTypeGoraToGo(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "...") {
		return "..." + translateTypeGoraToGo(s[3:])
	}
	if strings.HasPrefix(s, "addr[") && findMatchingDelim(s, 4, '[', ']') == len(s)-1 {
		return "*" + translateTypeGoraToGo(s[5:len(s)-1])
	}
	if strings.HasPrefix(s, "list[") && findMatchingDelim(s, 4, '[', ']') == len(s)-1 {
		return "[]" + translateTypeGoraToGo(s[5:len(s)-1])
	}
	if strings.HasPrefix(s, "set[") && findMatchingDelim(s, 3, '[', ']') == len(s)-1 {
		return "map[" + translateTypeGoraToGo(s[4:len(s)-1]) + "]bool"
	}
	if strings.HasPrefix(s, "dict[") && findMatchingDelim(s, 4, '[', ']') == len(s)-1 {
		inner := s[5 : len(s)-1]
		comma := findTopLevelSep(inner, ',')
		if comma >= 0 {
			k := translateTypeGoraToGo(inner[:comma])
			v := translateTypeGoraToGo(inner[comma+1:])
			return "map[" + k + "]" + v
		}
	}
	if strings.HasPrefix(s, "chan[") && findMatchingDelim(s, 4, '[', ']') == len(s)-1 {
		return "chan " + translateTypeGoraToGo(s[5:len(s)-1])
	}
	if strings.HasPrefix(s, "in_chan[") && findMatchingDelim(s, 7, '[', ']') == len(s)-1 {
		return "<-chan " + translateTypeGoraToGo(s[8:len(s)-1])
	}
	if strings.HasPrefix(s, "out_chan[") && findMatchingDelim(s, 8, '[', ']') == len(s)-1 {
		return "chan<- " + translateTypeGoraToGo(s[9:len(s)-1])
	}
	if strings.HasPrefix(s, "[") {
		closeB := findMatchingDelim(s, 0, '[', ']')
		if closeB >= 0 && closeB+1 < len(s) {
			sizeExpr := strings.TrimSpace(s[1:closeB])
			elem := translateTypeGoraToGo(s[closeB+1:])
			if sizeExpr == "" {
				return "[]" + elem
			}
			return "[" + translateExprGoraToGo(sizeExpr, nil) + "]" + elem
		}
	}
	if strings.HasPrefix(s, "<-chan ") {
		return "<-chan " + translateTypeGoraToGo(s[7:])
	}
	if strings.HasPrefix(s, "chan<- ") {
		return "chan<- " + translateTypeGoraToGo(s[7:])
	}
	if strings.HasPrefix(s, "chan ") {
		return "chan " + translateTypeGoraToGo(s[5:])
	}
	if strings.HasPrefix(s, "fn(") || strings.HasPrefix(s, "func(") {
		lp := strings.IndexByte(s, '(')
		rp := findMatchingDelim(s, lp, '(', ')')
		if rp > lp {
			params := translateFuncParamsGoraToGo(s[lp+1 : rp])
			rest := strings.TrimSpace(s[rp+1:])
			if strings.HasPrefix(rest, "->") {
				ret := translateReturnTypesGoraToGo(strings.TrimSpace(rest[2:]))
				return "func(" + params + ") " + ret
			}
			if rest != "" {
				return "func(" + params + ") " + translateReturnTypesGoraToGo(rest)
			}
			return "func(" + params + ")"
		}
	}
	if strings.HasPrefix(s, "*") {
		return "*" + translateTypeGoraToGo(s[1:])
	}
	if strings.HasPrefix(s, "map[") {
		closeB := findMatchingDelim(s, 3, '[', ']')
		if closeB > 3 && closeB+1 < len(s) {
			return "map[" + translateTypeGoraToGo(s[4:closeB]) + "]" + translateTypeGoraToGo(s[closeB+1:])
		}
	}
	// Generic instantiation or fixed-array T[N] shorthand
	if br := strings.IndexByte(s, '['); br > 0 {
		closeB := findMatchingDelim(s, br, '[', ']')
		if closeB == len(s)-1 {
			base := strings.TrimSpace(s[:br])
			inner := strings.TrimSpace(s[br+1 : closeB])
			if _, isPrim := goraToGoTypeMap[base]; isPrim && isAllDigits(inner) {
				return "[" + inner + "]" + translateTypeGoraToGo(base)
			}
			args := splitTopLevel(inner, ',')
			for idx, a := range args {
				args[idx] = translateTypeGoraToGo(a)
			}
			return base + "[" + strings.Join(args, ", ") + "]"
		}
	}
	if mapped, ok := goraToGoTypeMap[s]; ok {
		return mapped
	}
	return s
}

func translateReturnTypesGoraToGo(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "(") && findMatchingDelim(s, 0, '(', ')') == len(s)-1 {
		inner := s[1 : len(s)-1]
		parts := splitTopLevel(inner, ',')
		var out []string
		for _, p := range parts {
			if p == "" {
				continue
			}
			if colon := findTopLevelSep(p, ':'); colon >= 0 {
				name := strings.TrimSpace(p[:colon])
				typ := translateTypeGoraToGo(p[colon+1:])
				out = append(out, name+" "+typ)
			} else {
				out = append(out, translateTypeGoraToGo(p))
			}
		}
		if len(out) == 1 && findTopLevelSep(inner, ':') < 0 {
			return out[0]
		}
		return "(" + strings.Join(out, ", ") + ")"
	}
	parts := splitTopLevel(s, ',')
	if len(parts) == 1 {
		if colon := findTopLevelSep(parts[0], ':'); colon >= 0 {
			name := strings.TrimSpace(parts[0][:colon])
			typ := translateTypeGoraToGo(parts[0][colon+1:])
			return "(" + name + " " + typ + ")"
		}
		return translateTypeGoraToGo(parts[0])
	}
	var out []string
	for _, p := range parts {
		if colon := findTopLevelSep(p, ':'); colon >= 0 {
			name := strings.TrimSpace(p[:colon])
			typ := translateTypeGoraToGo(p[colon+1:])
			out = append(out, name+" "+typ)
		} else {
			out = append(out, translateTypeGoraToGo(p))
		}
	}
	return "(" + strings.Join(out, ", ") + ")"
}

func translateFuncParamsGoraToGo(params string) string {
	params = strings.TrimSpace(params)
	if params == "" || params == "void" {
		return ""
	}
	items := splitTopLevel(params, ',')
	var out []string
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if colon := findTopLevelSep(item, ':'); colon >= 0 {
			name := strings.TrimSpace(item[:colon])
			typ := translateTypeGoraToGo(item[colon+1:])
			out = append(out, name+" "+typ)
		} else {
			out = append(out, translateTypeGoraToGo(item))
		}
	}
	return strings.Join(out, ", ")
}

type translateCtx struct {
	usedFmt      bool
	usedInHelper bool
	usedMath     bool
	usedIfHelper bool
	usedFloorDiv bool
	usedPow      bool
}

func translateFString(lit string, ctx *translateCtx) (string, bool) {
	// lit starts with `"` or `'` and ends with matching quote
	if len(lit) < 2 || (lit[0] != '"' && lit[0] != '\'') || lit[len(lit)-1] != lit[0] {
		return lit, false
	}
	quote := lit[0]
	inner := lit[1 : len(lit)-1]
	var fmtStr strings.Builder
	var args []string
	hasInterp := false

	i := 0
	for i < len(inner) {
		c := inner[i]
		if c == '\\' && i+1 < len(inner) {
			fmtStr.WriteByte(c)
			fmtStr.WriteByte(inner[i+1])
			i += 2
			continue
		}
		if quote == '\'' && c == '"' {
			fmtStr.WriteString(`\"`)
			i++
			continue
		}
		if c == '%' {
			fmtStr.WriteString("%%")
			i++
			continue
		}
		if c == '{' {
			if i+1 < len(inner) && inner[i+1] == '{' {
				fmtStr.WriteByte('{')
				i += 2
				continue
			}
			// Scan until matching `}`
			depth := 1
			var q byte
			j := i + 1
			for j < len(inner) && depth > 0 {
				ch := inner[j]
				if q != 0 {
					if q != '`' && ch == '\\' && j+1 < len(inner) {
						j++
					} else if ch == q {
						q = 0
					}
				} else {
					if ch == '"' || ch == '\'' || ch == '`' {
						q = ch
					} else if ch == '{' {
						depth++
					} else if ch == '}' {
						depth--
						if depth == 0 {
							break
						}
					}
				}
				j++
			}
			if depth != 0 {
				return lit, false
			}
			hasInterp = true
			exprPart := inner[i+1 : j]
			spec := "v"
			// Check for top-level format specifier `expr:spec`
			colon := findTopLevelSep(exprPart, ':')
			if colon >= 0 {
				specStr := strings.TrimSpace(exprPart[colon+1:])
				if specStr != "" {
					spec = specStr
				}
				exprPart = exprPart[:colon]
			}
			fmtStr.WriteByte('%')
			fmtStr.WriteString(spec)
			args = append(args, translateExprGoraToGo(strings.TrimSpace(exprPart), ctx))
			i = j + 1
			continue
		}
		if c == '}' && i+1 < len(inner) && inner[i+1] == '}' {
			fmtStr.WriteByte('}')
			i += 2
			continue
		}
		fmtStr.WriteByte(c)
		i++
	}

	if !hasInterp {
		return `"` + strings.ReplaceAll(fmtStr.String(), "%%", "%") + `"`, true
	}
	if ctx != nil {
		ctx.usedFmt = true
	}
	return `fmt.Sprintf("` + fmtStr.String() + `", ` + strings.Join(args, ", ") + `)`, true
}

func scanLambdaEnd(s string, start int) int {
	depth := 0
	var quote byte
	for i := start; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
		} else if c == '(' || c == '[' || c == '{' {
			depth++
		} else if c == ')' || c == ']' || c == '}' {
			if depth == 0 {
				return i
			}
			depth--
		} else if c == ',' && depth == 0 {
			return i
		}
	}
	return len(s)
}

// translateLambda translates `lambda <params> [-> <ret>]: <body>` starting after `lambda`.
func translateLambda(rest string, ctx *translateCtx) (goCode string, consumed int, ok bool) {
	// Find the body separator `:` (NOT part of parameter type annotations!)
	// Strategy: find `->` at depth 0, or if none, find `:` where after `:` is an expression (not a type followed by `,` / `:` / `->`).
	// Specifically:
	//   - `lambda (): expr`
	//   - `lambda (a: i32, b: i32) -> i32: expr`
	//   - `lambda x: i32 -> i32: expr`
	//   - `lambda a: i32, b: i32 -> i32: expr`
	//   - `lambda x: i32: expr`
	// Notice: if `(params)` is parenthesized, the first `:` or `->` after `)` is the return/body marker!
	// If unparenthesized, `->` precedes `: body`; without `->`, the LAST `:` before body (at depth 0, before `,`/`]`/`)`) is body!
	endIdx := scanLambdaEnd(rest, 0)
	// Wait: `lambda a: i32, b: i32 -> i32: a + b` has `,` inside params!
	// So we must first locate the `:` that starts the lambda body, THEN scanLambdaEnd from after that `:`!
	trimmedStart := 0
	for trimmedStart < len(rest) && rest[trimmedStart] == ' ' {
		trimmedStart++
	}
	_ = endIdx

	var paramsStr, retStr string
	bodyColon := -1

	if trimmedStart < len(rest) && rest[trimmedStart] == '(' {
		rp := findMatchingDelim(rest, trimmedStart, '(', ')')
		if rp < 0 {
			return "", 0, false
		}
		paramsStr = rest[trimmedStart+1 : rp]
		after := rp + 1
		for after < len(rest) && rest[after] == ' ' {
			after++
		}
		if after+2 <= len(rest) && rest[after:after+2] == "->" {
			// Find `:` after `->`
			colonRel := findTopLevelSep(rest[after+2:], ':')
			if colonRel < 0 {
				return "", 0, false
			}
			retStr = strings.TrimSpace(rest[after+2 : after+2+colonRel])
			bodyColon = after + 2 + colonRel
		} else if after < len(rest) && rest[after] == ':' {
			bodyColon = after
		} else {
			return "", 0, false
		}
	} else {
		// Unparenthesized parameter list: `x: i32 -> i32: body` or `a: i32, b: i32 -> i32: body` or `x: i32: body`
		depth := 0
		var quote byte
		arrowIdx := -1
		var colons []int
		for i := trimmedStart; i < len(rest); i++ {
			c := rest[i]
			if quote != 0 {
				if quote != '`' && c == '\\' && i+1 < len(rest) {
					i++
				} else if c == quote {
					quote = 0
				}
				continue
			}
			if c == '"' || c == '\'' || c == '`' {
				quote = c
			} else if c == '(' || c == '[' || c == '{' {
				depth++
			} else if c == ')' || c == ']' || c == '}' {
				if depth == 0 {
					break
				}
				depth--
			} else if depth == 0 {
				if c == '-' && i+1 < len(rest) && rest[i+1] == '>' {
					arrowIdx = i
					break
				}
				if c == ':' {
					colons = append(colons, i)
					// If we already have 2 colons on one param without comma, or `:` after type
				}
			}
		}
		if arrowIdx >= 0 {
			paramsStr = strings.TrimSpace(rest[trimmedStart:arrowIdx])
			colonRel := findTopLevelSep(rest[arrowIdx+2:], ':')
			if colonRel < 0 {
				return "", 0, false
			}
			retStr = strings.TrimSpace(rest[arrowIdx+2 : arrowIdx+2+colonRel])
			bodyColon = arrowIdx + 2 + colonRel
		} else if len(colons) >= 2 {
			// e.g. `x: i32: fmt.Println(x)` -> last colon before body
			bodyColon = colons[len(colons)-1]
			paramsStr = strings.TrimSpace(rest[trimmedStart:bodyColon])
		} else if len(colons) == 1 {
			// e.g. `: body` (zero args)
			bodyColon = colons[0]
			paramsStr = strings.TrimSpace(rest[trimmedStart:bodyColon])
		} else {
			return "", 0, false
		}
	}

	bodyStart := bodyColon + 1
	bodyEnd := scanLambdaEnd(rest, bodyStart)
	body := strings.TrimSpace(rest[bodyStart:bodyEnd])
	if body == "" {
		return "", 0, false
	}

	goParams := translateFuncParamsGoraToGo(paramsStr)
	goRet := translateReturnTypesGoraToGo(retStr)
	goBody := translateExprGoraToGo(body, ctx)

	var b strings.Builder
	b.WriteString("func(")
	b.WriteString(goParams)
	b.WriteByte(')')
	if goRet != "" {
		b.WriteByte(' ')
		b.WriteString(goRet)
		b.WriteString(" { return ")
		b.WriteString(goBody)
		b.WriteString(" }")
	} else {
		b.WriteString(" { ")
		b.WriteString(goBody)
		b.WriteString(" }")
	}
	return b.String(), bodyEnd, true
}

func isSimplePostfixExpr(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '(' || c == '[' {
			depth++
		} else if c == ')' || c == ']' {
			depth--
		} else if depth == 0 && (c == ' ' || c == '+' || c == '-' || c == '*' || c == '/' ||
			c == '%' || c == '&' || c == '|' || c == '^' || c == '<' || c == '>') {
			return false
		}
	}
	return true
}

func needsParensForGoPtrCast(goType string) bool {
	return strings.HasPrefix(goType, "*") || strings.HasPrefix(goType, "<-") || strings.HasPrefix(goType, "func(")
}

func hasTopLevelAssignmentOrSep(s string) bool {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '(' || c == '[' || c == '{' {
			depth++
			continue
		}
		if c == ')' || c == ']' || c == '}' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 {
			if c == ',' || c == ';' {
				return true
			}
			if c == '=' {
				if (i+1 < len(s) && s[i+1] == '=') ||
					(i > 0 && (s[i-1] == '=' || s[i-1] == '!' || s[i-1] == '<' || s[i-1] == '>')) {
					continue
				}
				return true
			}
		}
	}
	return false
}

func trySplitChainedComparisonsGora(s string) (operands []string, ops []string, ok bool) {
	depth := 0
	var quote byte
	last := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '(' || c == '[' || c == '{' {
			depth++
			continue
		}
		if c == ')' || c == ']' || c == '}' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 {
			if (c == '<' || c == '>') && (i+1 >= len(s) || s[i+1] != c) && (i == 0 || s[i-1] != c) {
				if c == '<' && i+1 < len(s) && s[i+1] == '-' {
					continue
				}
				if c == '>' && i > 0 && (s[i-1] == '-' || s[i-1] == '=') {
					continue
				}
				op := string(c)
				if i+1 < len(s) && s[i+1] == '=' {
					op += "="
				}
				operands = append(operands, strings.TrimSpace(s[last:i]))
				ops = append(ops, op)
				i += len(op) - 1
				last = i + 1
			} else if (c == '=' || c == '!') && i+1 < len(s) && s[i+1] == '=' {
				op := s[i : i+2]
				operands = append(operands, strings.TrimSpace(s[last:i]))
				ops = append(ops, op)
				i++
				last = i + 1
			}
		}
	}
	if len(ops) < 2 {
		return nil, nil, false
	}
	operands = append(operands, strings.TrimSpace(s[last:]))
	for _, o := range operands {
		if o == "" {
			return nil, nil, false
		}
	}
	return operands, ops, true
}

func hasComparisonOpGora(s string) bool {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '(' || c == '[' || c == '{' {
			depth++
		} else if c == ')' || c == ']' || c == '}' {
			if depth > 0 {
				depth--
			}
		} else if depth == 0 {
			if c == '<' || c == '>' {
				if c == '<' && i+1 < len(s) && s[i+1] == '-' {
					continue
				}
				if c == '>' && i > 0 && (s[i-1] == '-' || s[i-1] == '=') {
					continue
				}
				return true
			}
			if (c == '=' || c == '!') && i+1 < len(s) && s[i+1] == '=' {
				return true
			}
		}
	}
	return findTopLevelKeyword(s, "in") >= 0 || findTopLevelKeyword(s, "is") >= 0
}

// translateExprGoraToGo translates Gora tokens/expressions into Go.
func translateExprGoraToGo(s string, ctx *translateCtx) string {
	// Handle compound `or` / `and`, ternary, `in`, `is`, chained comparisons, `//`, `**` in pure expressions
	if !hasTopLevelAssignmentOrSep(s) && findTopLevelKeyword(s, "lambda") < 0 {
		trimmed := strings.TrimSpace(s)
		if tExpr, condExpr, fExpr, tok := splitTernary(trimmed); tok {
			if ctx != nil {
				ctx.usedIfHelper = true
			}
			return "_goraIf(" + translateExprGoraToGo(condExpr, ctx) + ", " +
				translateExprGoraToGo(tExpr, ctx) + ", " +
				translateExprGoraToGo(fExpr, ctx) + ")"
		}
		if orIdx := findTopLevelKeyword(trimmed, "or"); orIdx > 0 {
			left := translateExprGoraToGo(strings.TrimSpace(trimmed[:orIdx]), ctx)
			right := translateExprGoraToGo(strings.TrimSpace(trimmed[orIdx+2:]), ctx)
			return left + " || " + right
		}
		if andIdx := findTopLevelKeyword(trimmed, "and"); andIdx > 0 {
			left := translateExprGoraToGo(strings.TrimSpace(trimmed[:andIdx]), ctx)
			right := translateExprGoraToGo(strings.TrimSpace(trimmed[andIdx+3:]), ctx)
			return left + " && " + right
		}
		if afterNot, ok := startsWithKW(trimmed, "not"); ok && afterNot != "" {
			if inIdx := findTopLevelKeyword(afterNot, "in"); inIdx > 0 {
				key := translateExprGoraToGo(strings.TrimSpace(afterNot[:inIdx]), ctx)
				mExpr := translateExprGoraToGo(strings.TrimSpace(afterNot[inIdx+2:]), ctx)
				if ctx != nil {
					ctx.usedInHelper = true
				}
				return "!_goraIn(" + mExpr + ", " + key + ")"
			}
			if hasComparisonOpGora(afterNot) {
				return "!(" + translateExprGoraToGo(strings.TrimSpace(afterNot), ctx) + ")"
			}
		}
		if notIdx := findTopLevelKeyword(trimmed, "not"); notIdx > 0 {
			afterNot := strings.TrimSpace(trimmed[notIdx+3:])
			if inAfter, ok := startsWithKW(afterNot, "in"); ok && inAfter != "" {
				key := translateExprGoraToGo(strings.TrimSpace(trimmed[:notIdx]), ctx)
				mExpr := translateExprGoraToGo(strings.TrimSpace(inAfter), ctx)
				if ctx != nil {
					ctx.usedInHelper = true
				}
				return "!_goraIn(" + mExpr + ", " + key + ")"
			}
		}
		if inIdx := findTopLevelKeyword(trimmed, "in"); inIdx > 0 {
			key := translateExprGoraToGo(strings.TrimSpace(trimmed[:inIdx]), ctx)
			mExpr := translateExprGoraToGo(strings.TrimSpace(trimmed[inIdx+2:]), ctx)
			if ctx != nil {
				ctx.usedInHelper = true
			}
			return "_goraIn(" + mExpr + ", " + key + ")"
		}
		if isIdx := findTopLevelKeyword(trimmed, "is"); isIdx > 0 {
			left := translateExprGoraToGo(strings.TrimSpace(trimmed[:isIdx]), ctx)
			rhs := strings.TrimSpace(trimmed[isIdx+2:])
			if afterNot, ok := startsWithKW(rhs, "not"); ok && afterNot != "" {
				return left + " != " + translateExprGoraToGo(strings.TrimSpace(afterNot), ctx)
			}
			return left + " == " + translateExprGoraToGo(rhs, ctx)
		}
		if ops, cmpOps, ok := trySplitChainedComparisonsGora(trimmed); ok {
			var parts []string
			for k, op := range cmpOps {
				parts = append(parts, "("+translateExprGoraToGo(ops[k], ctx)+" "+op+" "+translateExprGoraToGo(ops[k+1], ctx)+")")
			}
			return strings.Join(parts, " && ")
		}
		if fIdx := findTopLevelStr(trimmed, "//"); fIdx > 0 {
			if ctx != nil {
				ctx.usedFloorDiv = true
				ctx.usedMath = true
			}
			return "_goraFloorDiv(" + translateExprGoraToGo(strings.TrimSpace(trimmed[:fIdx]), ctx) + ", " +
				translateExprGoraToGo(strings.TrimSpace(trimmed[fIdx+2:]), ctx) + ")"
		}
		if pIdx := findTopLevelStr(trimmed, "**"); pIdx > 0 {
			if ctx != nil {
				ctx.usedPow = true
				ctx.usedMath = true
			}
			return "_goraPow(" + translateExprGoraToGo(strings.TrimSpace(trimmed[:pIdx]), ctx) + ", " +
				translateExprGoraToGo(strings.TrimSpace(trimmed[pIdx+2:]), ctx) + ")"
		}
	}

	var out strings.Builder
	i := 0
	n := len(s)

	for i < n {
		c := s[i]

		// F-string: f"..." or f'...'
		if c == 'f' && i+1 < n && (s[i+1] == '"' || s[i+1] == '\'') && (i == 0 || !isIdentCont(s[i-1])) {
			q := s[i+1]
			j := i + 2
			for j < n {
				if s[j] == '\\' && j+1 < n {
					j += 2
				} else if s[j] == q {
					j++
					break
				} else {
					j++
				}
			}
			lit := s[i+1 : j]
			if translated, ok := translateFString(lit, ctx); ok {
				out.WriteString(translated)
				i = j
				continue
			}
		}

		// String, rune, or raw string literal
		if c == '"' || c == '\'' || c == '`' {
			quote := c
			out.WriteByte(c)
			i++
			for i < n {
				sc := s[i]
				out.WriteByte(sc)
				i++
				if quote != '`' && sc == '\\' && i < n {
					out.WriteByte(s[i])
					i++
				} else if sc == quote {
					break
				}
			}
			continue
		}

		if isIdentStart(c) {
			start := i
			for i < n && isIdentCont(s[i]) {
				i++
			}
			word := s[start:i]
			isMember := start > 0 && s[start-1] == '.'

			// Lambda expression: `lambda ...: ...`
			if !isMember && word == "lambda" && i < n && (s[i] == ' ' || s[i] == '(' || s[i] == ':') {
				if goLambda, consumed, ok := translateLambda(s[i:], ctx); ok {
					out.WriteString(goLambda)
					i += consumed
					continue
				}
			}

			// Container type in expression context (e.g., `make(list[i32], 5)`, `list[i32]{1, 2}`, `addr[Vec2]`, `chan[i32]`)
			if !isMember && i < n && s[i] == '[' &&
				(word == "addr" || word == "list" || word == "dict" || word == "set" ||
					word == "chan" || word == "in_chan" || word == "out_chan") {
				closeB := findMatchingDelim(s, i, '[', ']')
				if closeB > i {
					out.WriteString(translateTypeGoraToGo(s[start : closeB+1]))
					i = closeB + 1
					continue
				}
			}

			// `cast(Type, expr)` -> `(*Type)(expr)` or `int32(expr)`
			if !isMember && word == "cast" && i < n && s[i] == '(' {
				closeP := findMatchingDelim(s, i, '(', ')')
				if closeP > i {
					inner := s[i+1 : closeP]
					comma := findTopLevelSep(inner, ',')
					if comma >= 0 {
						goT := translateTypeGoraToGo(inner[:comma])
						valExpr := translateExprGoraToGo(strings.TrimSpace(inner[comma+1:]), ctx)
						if needsParensForGoPtrCast(goT) {
							out.WriteByte('(')
							out.WriteString(goT)
							out.WriteByte(')')
						} else {
							out.WriteString(goT)
						}
						out.WriteByte('(')
						out.WriteString(valExpr)
						out.WriteByte(')')
						i = closeP + 1
						continue
					}
				}
			}

			// `val(p)` -> `*p`, `val(2, p)` -> `**p`
			if !isMember && word == "val" && i < n && s[i] == '(' {
				closeP := findMatchingDelim(s, i, '(', ')')
				if closeP > i {
					inner := s[i+1 : closeP]
					comma := findTopLevelSep(inner, ',')
					level := 1
					exprS := strings.TrimSpace(inner)
					if comma >= 0 {
						lvlStr := strings.TrimSpace(inner[:comma])
						if parsed, err := strconv.Atoi(lvlStr); err == nil && parsed > 0 && parsed <= 16 {
							level = parsed
							exprS = strings.TrimSpace(inner[comma+1:])
						}
					}
					for d := 0; d < level; d++ {
						out.WriteByte('*')
					}
					translated := translateExprGoraToGo(exprS, ctx)
					if isSimplePostfixExpr(exprS) {
						out.WriteString(translated)
					} else {
						out.WriteByte('(')
						out.WriteString(translated)
						out.WriteByte(')')
					}
					i = closeP + 1
					continue
				}
			}

			// Scalar type conversion call: `i32(x)`, `f64(x)`, `str(x)` -> `int32(x)`, `float64(x)`, `string(x)`
			if !isMember && i < n && s[i] == '(' {
				if goT, ok := goraToGoTypeMap[word]; ok {
					out.WriteString(goT)
					continue
				}
			}

			// Check for `.addr`, `.get()`, `.put(...)` at end of primary/postfix chain
			if !isMember &&
				word != "return" && word != "case" && word != "if" && word != "elif" &&
				word != "for" && word != "while" && word != "match" && word != "switch" &&
				word != "and" && word != "or" && word != "not" && word != "in" && word != "try" {
				scan := i
				matchedPostfix := false
				for scan < n {
					if s[scan] == '[' {
						closeB := findMatchingDelim(s, scan, '[', ']')
						if closeB < 0 {
							break
						}
						scan = closeB + 1
					} else if s[scan] == '(' {
						closeP := findMatchingDelim(s, scan, '(', ')')
						if closeP < 0 {
							break
						}
						scan = closeP + 1
					} else if scan+5 <= n && s[scan:scan+5] == ".addr" &&
						(scan+5 == n || !isIdentCont(s[scan+5])) {
						out.WriteByte('&')
						out.WriteString(translateExprGoraToGo(s[start:scan], ctx))
						i = scan + 5
						matchedPostfix = true
						break
					} else if scan+6 <= n && s[scan:scan+6] == ".get()" &&
						(scan+6 == n || !isIdentCont(s[scan+6])) {
						out.WriteString("<-")
						out.WriteString(translateExprGoraToGo(s[start:scan], ctx))
						i = scan + 6
						matchedPostfix = true
						break
					} else if scan+5 <= n && s[scan:scan+5] == ".put(" {
						openP := scan + 4
						closeP := findMatchingDelim(s, openP, '(', ')')
						if closeP < 0 {
							break
						}
						out.WriteString(translateExprGoraToGo(s[start:scan], ctx))
						out.WriteString(" <- ")
						out.WriteString(translateExprGoraToGo(strings.TrimSpace(s[openP+1:closeP]), ctx))
						i = closeP + 1
						matchedPostfix = true
						break
					} else if s[scan] == '.' && scan+1 < n && isIdentStart(s[scan+1]) {
						scan++
						for scan < n && isIdentCont(s[scan]) {
							scan++
						}
					} else {
						break
					}
				}
				if matchedPostfix {
					continue
				}
			}

			// `print(...)` -> `fmt.Println(...)`
			if !isMember && word == "print" && i < n && s[i] == '(' {
				closeP := findMatchingDelim(s, i, '(', ')')
				if closeP > i {
					if ctx != nil {
						ctx.usedFmt = true
					}
					args := splitTopLevel(s[i+1:closeP], ',')
					for ai, a := range args {
						if a != "" {
							args[ai] = translateExprGoraToGo(a, ctx)
						}
					}
					out.WriteString("fmt.Println(")
					if !(len(args) == 1 && args[0] == "") {
						out.WriteString(strings.Join(args, ", "))
					}
					out.WriteByte(')')
					i = closeP + 1
					continue
				}
			}

			// `isinstance(x, T)` -> `func() bool { _, _ok := any(x).(T); return _ok }()`
			if !isMember && word == "isinstance" && i < n && s[i] == '(' {
				closeP := findMatchingDelim(s, i, '(', ')')
				if closeP > i {
					args := splitTopLevel(s[i+1:closeP], ',')
					if len(args) == 2 {
						valExpr := translateExprGoraToGo(args[0], ctx)
						goType := translateTypeGoraToGo(args[1])
						out.WriteString("func() bool { _, _ok := any(" + valExpr + ").(" + goType + "); return _ok }()")
						i = closeP + 1
						continue
					}
				}
			}

			// Negative integer literal index `xs[-1]` -> `xs[len(xs)-1]`
			if !isMember && i < n && s[i] == '[' {
				closeB := findMatchingDelim(s, i, '[', ']')
				if closeB > i+2 && s[i+1] == '-' && isAllDigits(s[i+2:closeB]) {
					out.WriteString(word)
					out.WriteString("[len(")
					out.WriteString(word)
					out.WriteString(")-")
					out.WriteString(s[i+2 : closeB])
					out.WriteByte(']')
					i = closeB + 1
					continue
				}
			}

			// Keywords and literals
			if !isMember {
				switch word {
				case "and":
					out.WriteString("&&")
					continue
				case "or":
					out.WriteString("||")
					continue
				case "not":
					out.WriteByte('!')
					if i < n && s[i] == ' ' {
						i++ // `not x` -> `!x`
					}
					continue
				case "True":
					out.WriteString("true")
					continue
				case "False":
					out.WriteString("false")
					continue
				case "None":
					out.WriteString("nil")
					continue
				}
			}

			out.WriteString(word)
			continue
		}

		// Handle parenthesized subexpressions or argument lists containing `in` / `is` / `if` / `//` / `**` / chained comparisons
		if c == '(' {
			closeP := findMatchingDelim(s, i, '(', ')')
			if closeP > i {
				inner := s[i+1 : closeP]
				if findTopLevelKeyword(inner, "lambda") < 0 {
					args := splitTopLevel(inner, ',')
					needsRecurse := false
					for _, a := range args {
						if findTopLevelKeyword(a, "in") > 0 ||
							findTopLevelKeyword(a, "is") > 0 ||
							findTopLevelKeyword(a, "if") > 0 ||
							findTopLevelStr(a, "//") > 0 ||
							findTopLevelStr(a, "**") > 0 {
							needsRecurse = true
							break
						}
						if _, _, isChained := trySplitChainedComparisonsGora(a); isChained {
							needsRecurse = true
							break
						}
					}
					if needsRecurse {
						for ai, a := range args {
							args[ai] = translateExprGoraToGo(a, ctx)
						}
						out.WriteByte('(')
						out.WriteString(strings.Join(args, ", "))
						out.WriteByte(')')
						i = closeP + 1
						continue
					}
				}
			}
		}

		// Handle `[N]T{...}` slice/array literals where T might be a Gora type like `[]i32{1, 2}` or `[2]list[i32]{...}`
		if c == '[' && (i == 0 || (!isIdentCont(s[i-1]) && s[i-1] != ']')) {
			closeB := findMatchingDelim(s, i, '[', ']')
			if closeB >= 0 && closeB+1 < n && isIdentStart(s[closeB+1]) {
				tStart := closeB + 1
				tEnd := scanGoTypeTokenEnd(s, tStart)
				if tEnd > tStart && tEnd < n && s[tEnd] == '{' {
					elem := s[tStart:tEnd]
					goElem := translateTypeGoraToGo(elem)
					sizeExpr := strings.TrimSpace(s[i+1 : closeB])
					out.WriteByte('[')
					if sizeExpr != "" {
						out.WriteString(translateExprGoraToGo(sizeExpr, ctx))
					}
					out.WriteByte(']')
					out.WriteString(goElem)
					i = tEnd
					continue
				}
			}
		}

		out.WriteByte(c)
		i++
	}
	return out.String()
}

// zeroValueForGoType returns a valid Go zero-value expression (or var declaration if needed) for `try` propagation.
func zeroValueForGoType(goType string, knownStructs map[string]bool) (expr string, needsTempVar bool) {
	t := strings.TrimSpace(goType)
	switch t {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
		"float32", "float64", "complex64", "complex128",
		"byte", "rune":
		return "0", false
	case "bool":
		return "false", false
	case "string":
		return `""`, false
	case "error", "any":
		return "nil", false
	}
	if strings.HasPrefix(t, "*") || strings.HasPrefix(t, "[]") ||
		strings.HasPrefix(t, "map[") || strings.HasPrefix(t, "chan ") ||
		strings.HasPrefix(t, "<-chan ") || strings.HasPrefix(t, "chan<- ") ||
		strings.HasPrefix(t, "func(") || strings.HasPrefix(t, "interface{") {
		return "nil", false
	}
	if knownStructs[t] {
		return t + "{}", false
	}
	// Unknown external type (could be struct, int alias like time.Duration, or interface):
	// `var _z T` works for 100% of Go types.
	return "", true
}

type blockKind int

const (
	blockNormal blockKind = iota
	blockParen
	blockSwitch
	blockCase
	blockStruct
	blockInterface
	blockFuncCall // `go def():` or `defer def():` -> closes with `}()`
)

type blockFrame struct {
	level       int
	kind        blockKind
	isFunc      bool
	retTypes    []string // Go return types of enclosing function
	errDeclared bool
}

func startsWithKW(s, kw string) (after string, ok bool) {
	if len(s) < len(kw) || s[:len(kw)] != kw {
		return "", false
	}
	if len(s) == len(kw) {
		return "", true
	}
	next := s[len(kw)]
	if next == ' ' || next == '(' || next == ':' || next == '[' {
		return strings.TrimLeft(s[len(kw):], " "), true
	}
	return "", false
}

func splitInlineHashComment(s string) (code string, comment string, hasComment bool) {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '#' && (i == 0 || s[i-1] == ' ') {
			return strings.TrimRight(s[:i], " "), s[i+1:], true
		}
	}
	return s, "", false
}

// findDeclColon splits `name: type [= val]` (not `:=`, not `==`).
func findDeclColon(s string) (lhs string, typ string, val string, hasEq bool, ok bool) {
	depth := 0
	var quote byte
	colonIdx := -1
	eqIdx := -1

	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '(' || c == '[' || c == '{' {
			depth++
			continue
		}
		if c == ')' || c == ']' || c == '}' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 {
			if colonIdx < 0 && c == ':' {
				if i+1 < len(s) && s[i+1] == '=' {
					return "", "", "", false, false // `:=` short decl
				}
				colonIdx = i
			} else if colonIdx >= 0 && eqIdx < 0 && c == '=' {
				if i+1 < len(s) && s[i+1] == '=' {
					i++
					continue
				}
				eqIdx = i
				break
			} else if colonIdx < 0 && (c == '=' || c == ';' || c == '+' || c == '-' || c == '*' || c == '/' || c == '<' || c == '>') {
				return "", "", "", false, false
			}
		}
	}
	if colonIdx < 0 {
		return "", "", "", false, false
	}

	lhs = strings.TrimSpace(s[:colonIdx])
	if lhs == "" {
		return "", "", "", false, false
	}
	// Validate lhs is `[const|var] ident [, ident]*`
	checkLHS := lhs
	if after, okKW := startsWithKW(checkLHS, "const"); okKW {
		checkLHS = strings.TrimSpace(after)
	} else if after, okKW := startsWithKW(checkLHS, "var"); okKW {
		checkLHS = strings.TrimSpace(after)
	}
	for i := 0; i < len(checkLHS); i++ {
		ch := checkLHS[i]
		if !isIdentCont(ch) && ch != ',' && ch != ' ' {
			return "", "", "", false, false
		}
	}

	if eqIdx >= 0 {
		typ = strings.TrimSpace(s[colonIdx+1 : eqIdx])
		val = strings.TrimSpace(s[eqIdx+1:])
		return lhs, typ, val, true, true
	}
	typ = strings.TrimSpace(s[colonIdx+1:])
	return lhs, typ, "", false, true
}

// splitStructTag extracts trailing backtick struct tag from a field type string.
func splitStructTag(typeAndTag string) (typ string, tag string) {
	s := strings.TrimSpace(typeAndTag)
	if len(s) >= 2 && s[len(s)-1] == '`' {
		start := strings.LastIndexByte(s[:len(s)-1], '`')
		if start > 0 && s[start-1] == ' ' {
			return strings.TrimSpace(s[:start]), s[start:]
		}
	}
	return s, ""
}

func formatGoImportSpec(spec string) (pkgPath string, alias string) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", ""
	}
	// `pkg as alias`
	if idx := findTopLevelKeyword(spec, "as"); idx > 0 {
		pkg := strings.TrimSpace(spec[:idx])
		al := strings.TrimSpace(spec[idx+2:])
		if al == "*" {
			al = "."
		}
		return quoteImportPath(pkg), al
	}
	// `alias = pkg`
	if eq := findTopLevelSep(spec, '='); eq > 0 {
		al := strings.TrimSpace(spec[:eq])
		pkg := strings.TrimSpace(spec[eq+1:])
		if al == "*" {
			al = "."
		}
		return quoteImportPath(pkg), al
	}
	return quoteImportPath(spec), ""
}

func quoteImportPath(p string) string {
	p = strings.TrimSpace(p)
	if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
		return p
	}
	return `"` + p + `"`
}

func parseFunctionHeader(rest string) (recv string, name string, typeParams string, params string, retGo string, retTypes []string, ok bool) {
	s := strings.TrimSpace(rest)
	// Optional method receiver: `(v: addr[Vec2]) Name(...)`
	if strings.HasPrefix(s, "(") {
		rp := findMatchingDelim(s, 0, '(', ')')
		if rp < 0 {
			return "", "", "", "", "", nil, false
		}
		recv = translateFuncParamsGoraToGo(s[1:rp])
		s = strings.TrimSpace(s[rp+1:])
	}

	// Function name + optional generic `[T any]`
	lp := -1
	depth := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '[' {
			depth++
		} else if s[i] == ']' {
			depth--
		} else if s[i] == '(' && depth == 0 {
			lp = i
			break
		}
	}
	if lp <= 0 {
		return "", "", "", "", "", nil, false
	}

	nameAndGen := strings.TrimSpace(s[:lp])
	if br := strings.IndexByte(nameAndGen, '['); br > 0 && nameAndGen[len(nameAndGen)-1] == ']' {
		name = strings.TrimSpace(nameAndGen[:br])
		genInner := nameAndGen[br+1 : len(nameAndGen)-1]
		genParts := splitTopLevel(genInner, ',')
		for i, gp := range genParts {
			if colon := findTopLevelSep(gp, ':'); colon >= 0 {
				genParts[i] = strings.TrimSpace(gp[:colon]) + " " + translateTypeGoraToGo(gp[colon+1:])
			} else {
				// translate constraint if needed, keeping identifier + constraint
				fields := strings.Fields(gp)
				if len(fields) >= 2 {
					genParts[i] = fields[0] + " " + translateTypeGoraToGo(strings.Join(fields[1:], " "))
				}
			}
		}
		typeParams = "[" + strings.Join(genParts, ", ") + "]"
	} else {
		name = nameAndGen
	}

	rp := findMatchingDelim(s, lp, '(', ')')
	if rp < 0 {
		return "", "", "", "", "", nil, false
	}
	params = translateFuncParamsGoraToGo(s[lp+1 : rp])

	after := strings.TrimSpace(s[rp+1:])
	if strings.HasPrefix(after, "->") {
		retRaw := strings.TrimSpace(after[2:])
		retGo = translateReturnTypesGoraToGo(retRaw)
		retTypes = extractIndividualGoReturnTypes(retRaw)
	}
	return recv, name, typeParams, params, retGo, retTypes, true
}

func extractIndividualGoReturnTypes(retRaw string) []string {
	s := strings.TrimSpace(retRaw)
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "(") && findMatchingDelim(s, 0, '(', ')') == len(s)-1 {
		s = s[1 : len(s)-1]
	}
	parts := splitTopLevel(s, ',')
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if colon := findTopLevelSep(p, ':'); colon >= 0 {
			out = append(out, translateTypeGoraToGo(p[colon+1:]))
		} else {
			out = append(out, translateTypeGoraToGo(p))
		}
	}
	return out
}

// translateIfCondition handles Pythonic `k in m` / `k not in m` / `not k in m` map membership checks.
func translateIfCondition(cond string, ctx *translateCtx) string {
	cond = strings.TrimSpace(cond)
	if semi := findTopLevelSep(cond, ';'); semi >= 0 {
		initPart := translateExprGoraToGo(strings.TrimSpace(cond[:semi]), ctx)
		testPart := translateExprGoraToGo(strings.TrimSpace(cond[semi+1:]), ctx)
		return initPart + "; " + testPart
	}
	// Only rewrite `k in m` / `k not in m` / `not k in m` when there is no `;` init clause and no top-level `and`/`or`
	if findTopLevelKeyword(cond, "and") < 0 &&
		findTopLevelKeyword(cond, "or") < 0 {
		if afterNot, ok := startsWithKW(cond, "not"); ok {
			if idx := findTopLevelKeyword(afterNot, "in"); idx > 0 {
				key := translateExprGoraToGo(strings.TrimSpace(afterNot[:idx]), ctx)
				mExpr := translateExprGoraToGo(strings.TrimSpace(afterNot[idx+2:]), ctx)
				return "_, ok := " + mExpr + "[" + key + "]; !ok"
			}
		}
		if idx := findTopLevelKeyword(cond, "not"); idx > 0 {
			afterNot := strings.TrimSpace(cond[idx+3:])
			if inAfter, ok := startsWithKW(afterNot, "in"); ok && inAfter != "" {
				key := translateExprGoraToGo(strings.TrimSpace(cond[:idx]), ctx)
				mExpr := translateExprGoraToGo(strings.TrimSpace(inAfter), ctx)
				return "_, ok := " + mExpr + "[" + key + "]; !ok"
			}
		}
		if idx := findTopLevelKeyword(cond, "in"); idx > 0 {
			key := translateExprGoraToGo(strings.TrimSpace(cond[:idx]), ctx)
			mExpr := translateExprGoraToGo(strings.TrimSpace(cond[idx+2:]), ctx)
			return "_, ok := " + mExpr + "[" + key + "]; ok"
		}
	}
	return translateExprGoraToGo(cond, ctx)
}

func translateForHeaderLines(expr string, ctx *translateCtx) (headerLine string, bodyPrep string) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "for {", ""
	}
	// Classic 3-clause Go `for` (contains `;`)
	if findTopLevelSep(expr, ';') >= 0 {
		parts := splitTopLevel(expr, ';')
		if len(parts) == 3 {
			initS := translateExprGoraToGo(parts[0], ctx)
			condS := translateExprGoraToGo(parts[1], ctx)
			stepS := translateExprGoraToGo(parts[2], ctx)
			return "for " + initS + "; " + condS + "; " + stepS + " {", ""
		}
		return "for " + translateExprGoraToGo(expr, ctx) + " {", ""
	}

	inIdx := findTopLevelKeyword(expr, "in")
	if inIdx > 0 {
		lhs := strings.TrimSpace(expr[:inIdx])
		rhs := strings.TrimSpace(expr[inIdx+2:])

		var varName string
		if colon := findTopLevelSep(lhs, ':'); colon >= 0 {
			varName = strings.TrimSpace(lhs[:colon])
		} else {
			varName = lhs
		}

		// `zip(a, b)`
		if strings.HasPrefix(rhs, "zip(") && findMatchingDelim(rhs, 3, '(', ')') == len(rhs)-1 {
			zargs := splitTopLevel(rhs[4:len(rhs)-1], ',')
			vparts := splitTopLevel(strings.Trim(varName, "()"), ',')
			if len(zargs) == 2 && len(vparts) == 2 {
				aExpr := translateExprGoraToGo(zargs[0], ctx)
				bExpr := translateExprGoraToGo(zargs[1], ctx)
				hdr := "for _zi := 0; _zi < len(" + aExpr + ") && _zi < len(" + bExpr + "); _zi++ {"
				prep := vparts[0] + ", " + vparts[1] + " := " + aExpr + "[_zi], " + bExpr + "[_zi]"
				return hdr, prep
			}
		}

		// `reversed(xs)`
		if strings.HasPrefix(rhs, "reversed(") && findMatchingDelim(rhs, 8, '(', ')') == len(rhs)-1 {
			xsExpr := translateExprGoraToGo(rhs[9:len(rhs)-1], ctx)
			hdr := "for _ri := len(" + xsExpr + ") - 1; _ri >= 0; _ri-- {"
			prep := varName + " := " + xsExpr + "[_ri]"
			if varName == "_" {
				prep = ""
			}
			return hdr, prep
		}
	}

	return translateForHeader(expr, ctx), ""
}

func translateForHeader(expr string, ctx *translateCtx) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "for {"
	}
	// Classic 3-clause Go `for` (contains `;`)
	if findTopLevelSep(expr, ';') >= 0 {
		parts := splitTopLevel(expr, ';')
		if len(parts) == 3 {
			initS := translateExprGoraToGo(parts[0], ctx)
			condS := translateExprGoraToGo(parts[1], ctx)
			stepS := translateExprGoraToGo(parts[2], ctx)
			return "for " + initS + "; " + condS + "; " + stepS + " {"
		}
		return "for " + translateExprGoraToGo(expr, ctx) + " {"
	}

	// `... in ...`
	inIdx := findTopLevelKeyword(expr, "in")
	if inIdx > 0 {
		lhs := strings.TrimSpace(expr[:inIdx])
		rhs := strings.TrimSpace(expr[inIdx+2:])

		// Check optional `: type` on lhs (`for i: i64 in range(n)`)
		var varName, varType string
		if colon := findTopLevelSep(lhs, ':'); colon >= 0 {
			varName = strings.TrimSpace(lhs[:colon])
			varType = translateTypeGoraToGo(lhs[colon+1:])
		} else {
			varName = lhs
		}

		// 1. `range(...)`
		if strings.HasPrefix(rhs, "range(") && findMatchingDelim(rhs, 5, '(', ')') == len(rhs)-1 {
			rargs := splitTopLevel(rhs[6:len(rhs)-1], ',')
			if len(rargs) >= 1 && len(rargs) <= 3 && rargs[0] != "" {
				var startS, stopS, stepS string
				if len(rargs) == 1 {
					startS = "0"
					stopS = translateExprGoraToGo(rargs[0], ctx)
				} else {
					startS = translateExprGoraToGo(rargs[0], ctx)
					stopS = translateExprGoraToGo(rargs[1], ctx)
					if len(rargs) == 3 {
						stepS = translateExprGoraToGo(rargs[2], ctx)
					}
				}
				if varName == "_" && startS == "0" && stepS == "" {
					return "for range " + stopS + " {"
				}
				loopVar := varName
				if loopVar == "_" {
					loopVar = "_i"
				}
				if varType != "" {
					startS = varType + "(" + startS + ")"
				}
				neg := strings.HasPrefix(stepS, "-")
				cmp := " < "
				if neg {
					cmp = " > "
				}
				var stepCode string
				if stepS == "" || stepS == "1" {
					stepCode = loopVar + "++"
				} else if stepS == "-1" {
					stepCode = loopVar + "--"
				} else if neg {
					stepCode = loopVar + " -= " + strings.TrimSpace(stepS[1:])
				} else {
					stepCode = loopVar + " += " + stepS
				}
				return "for " + loopVar + " := " + startS + "; " + loopVar + cmp + stopS + "; " + stepCode + " {"
			}
		}

		// 2. `enumerate(xs)`
		if strings.HasPrefix(rhs, "enumerate(") && findMatchingDelim(rhs, 9, '(', ')') == len(rhs)-1 {
			iterExpr := translateExprGoraToGo(rhs[10:len(rhs)-1], ctx)
			return "for " + varName + " := range " + iterExpr + " {"
		}

		// 3. `for k, v in m:` or `for k, _ in m:` or `for v in xs:`
		iterExpr := translateExprGoraToGo(rhs, ctx)
		if comma := findTopLevelSep(varName, ','); comma >= 0 {
			k := strings.TrimSpace(varName[:comma])
			v := strings.TrimSpace(varName[comma+1:])
			if v == "_" {
				return "for " + k + " := range " + iterExpr + " {"
			}
			return "for " + k + ", " + v + " := range " + iterExpr + " {"
		}
		if varName == "_" {
			return "for range " + iterExpr + " {"
		}
		return "for _, " + varName + " := range " + iterExpr + " {"
	}

	return "for " + translateExprGoraToGo(expr, ctx) + " {"
}

type compClauseGora struct {
	forClause string
	conds     []string
}

// tryEmitGoraComprehension expands `[expr for ...]` / `{expr for ...}` / `{k: v for ...}` into idiomatic Go loops.
func tryEmitGoraComprehension(indent string, varName, goraType, val string, ctx *translateCtx) (lines []string, ok bool) {
	val = strings.TrimSpace(val)
	if len(val) < 8 {
		return nil, false
	}
	isBracket := val[0] == '[' && findMatchingDelim(val, 0, '[', ']') == len(val)-1
	isBrace := val[0] == '{' && findMatchingDelim(val, 0, '{', '}') == len(val)-1
	if !isBracket && !isBrace {
		return nil, false
	}
	inner := strings.TrimSpace(val[1 : len(val)-1])
	forIdx := findTopLevelKeyword(inner, "for")
	if forIdx <= 0 {
		return nil, false
	}
	head := strings.TrimSpace(inner[:forIdx])
	afterFor := strings.TrimSpace(inner[forIdx+3:])
	if head == "" || afterFor == "" {
		return nil, false
	}

	// Parse one or more `var in iter [if cond...]` clauses
	var clauses []compClauseGora
	rem := afterFor
	for len(rem) > 0 {
		inIdx := findTopLevelKeyword(rem, "in")
		if inIdx <= 0 {
			return nil, false
		}
		lhsPart := strings.TrimSpace(rem[:inIdx])
		afterIn := strings.TrimSpace(rem[inIdx+2:])
		nextFor := findTopLevelKeyword(afterIn, "for")
		var seg string
		if nextFor >= 0 {
			seg = strings.TrimSpace(afterIn[:nextFor])
			rem = strings.TrimSpace(afterIn[nextFor+3:])
		} else {
			seg = afterIn
			rem = ""
		}
		var iterPart string
		var conds []string
		if firstIf := findTopLevelKeyword(seg, "if"); firstIf > 0 {
			iterPart = strings.TrimSpace(seg[:firstIf])
			condTail := strings.TrimSpace(seg[firstIf+2:])
			if condTail == "" {
				return nil, false
			}
			for len(condTail) > 0 {
				ni := findTopLevelKeyword(condTail, "if")
				if ni < 0 {
					conds = append(conds, condTail)
					break
				}
				c := strings.TrimSpace(condTail[:ni])
				if c == "" {
					return nil, false
				}
				conds = append(conds, c)
				condTail = strings.TrimSpace(condTail[ni+2:])
			}
		} else {
			iterPart = seg
		}
		if iterPart == "" {
			return nil, false
		}
		clauses = append(clauses, compClauseGora{
			forClause: lhsPart + " in " + iterPart,
			conds:     conds,
		})
	}
	if len(clauses) == 0 {
		return nil, false
	}

	goraType = strings.TrimSpace(goraType)
	goGoType := translateTypeGoraToGo(goraType)

	emitLoopBody := func(leafLine string) []string {
		var body []string
		curInd := indent
		var closers []string
		for _, cl := range clauses {
			hdr, prep := translateForHeaderLines(cl.forClause, ctx)
			body = append(body, curInd+hdr)
			closers = append(closers, curInd+"}")
			curInd += "    "
			if prep != "" {
				body = append(body, curInd+prep)
			}
			for _, cond := range cl.conds {
				body = append(body, curInd+"if "+translateIfCondition(cond, ctx)+" {")
				closers = append(closers, curInd+"}")
				curInd += "    "
			}
		}
		body = append(body, curInd+leafLine)
		for ci := len(closers) - 1; ci >= 0; ci-- {
			body = append(body, closers[ci])
		}
		return body
	}

	if isBracket {
		if !strings.HasPrefix(goGoType, "[]") {
			return nil, false
		}
		elemExpr := translateExprGoraToGo(head, ctx)
		lines = append(lines, indent+"var "+varName+" "+goGoType)
		lines = append(lines, emitLoopBody(varName+" = append("+varName+", "+elemExpr+")")...)
		return lines, true
	}

	// Set or Dict comprehension
	if !strings.HasPrefix(goGoType, "map[") {
		return nil, false
	}
	colonIdx := findTopLevelSep(head, ':')
	if colonIdx >= 0 {
		keyExpr := translateExprGoraToGo(head[:colonIdx], ctx)
		valExpr := translateExprGoraToGo(head[colonIdx+1:], ctx)
		lines = append(lines, indent+varName+" := make("+goGoType+")")
		lines = append(lines, emitLoopBody(varName+"["+keyExpr+"] = "+valExpr)...)
		return lines, true
	}

	// Set comprehension
	elemExpr := translateExprGoraToGo(head, ctx)
	lines = append(lines, indent+varName+" := make("+goGoType+")")
	lines = append(lines, emitLoopBody(varName+"["+elemExpr+"] = true")...)
	return lines, true
}

// splitTernary splits `a if cond else b` at top level.
func splitTernary(expr string) (trueExpr, condExpr, falseExpr string, ok bool) {
	ifIdx := findTopLevelKeyword(expr, "if")
	if ifIdx <= 0 {
		return "", "", "", false
	}
	rest := expr[ifIdx+2:]
	elseRel := findTopLevelKeyword(rest, "else")
	if elseRel <= 0 {
		return "", "", "", false
	}
	trueExpr = strings.TrimSpace(expr[:ifIdx])
	condExpr = strings.TrimSpace(rest[:elseRel])
	falseExpr = strings.TrimSpace(rest[elseRel+4:])
	if trueExpr == "" || condExpr == "" || falseExpr == "" {
		return "", "", "", false
	}
	return trueExpr, condExpr, falseExpr, true
}

func emitTryReturnLines(indent string, retTypes []string, knownStructs map[string]bool) []string {
	var prep []string
	var zeros []string
	tempCount := 0
	for i := 0; i < len(retTypes)-1; i++ {
		z, needsVar := zeroValueForGoType(retTypes[i], knownStructs)
		if needsVar {
			tempCount++
			vname := "_z"
			if tempCount > 1 {
				vname = "_z" + strconv.Itoa(tempCount)
			}
			prep = append(prep, indent+"    var "+vname+" "+retTypes[i])
			zeros = append(zeros, vname)
		} else {
			zeros = append(zeros, z)
		}
	}
	zeros = append(zeros, "err")
	var out []string
	out = append(out, prep...)
	out = append(out, indent+"    return "+strings.Join(zeros, ", "))
	return out
}

func activeFuncFrame(stack []blockFrame) *blockFrame {
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].isFunc {
			return &stack[i]
		}
	}
	return nil
}

type goraLogicalLine struct {
	lineNo int
	raw    string
}

func preprocessLogicalLinesGora(src string) ([]goraLogicalLine, error) {
	var rawLines []string
	p := 0
	for p <= len(src) {
		if p == len(src) {
			break
		}
		ls := p
		for p < len(src) && src[p] != '\n' && src[p] != '\r' {
			p++
		}
		rawLines = append(rawLines, src[ls:p])
		if p < len(src) {
			if src[p] == '\r' && p+1 < len(src) && src[p+1] == '\n' {
				p += 2
			} else {
				p++
			}
		}
	}

	var result []goraLogicalLine
	curLineNo := 0
	var acc strings.Builder
	parenDepth := 0
	continued := false
	tripleQuote := ""
	tripleF := false
	var tripleBody strings.Builder

	updateDepth := func(code string) {
		var q byte
		for i := 0; i < len(code); i++ {
			c := code[i]
			if q != 0 {
				if q != '`' && c == '\\' && i+1 < len(code) {
					i++
				} else if c == q {
					q = 0
				}
				continue
			}
			if c == '"' || c == '\'' || c == '`' {
				q = c
				continue
			}
			if c == '(' || c == '[' || c == '{' {
				parenDepth++
			} else if c == ')' || c == ']' || c == '}' {
				if parenDepth > 0 {
					parenDepth--
				}
			}
		}
	}

	escapeSingleLineStr := func(body string, isF bool) string {
		var b strings.Builder
		if isF {
			b.WriteString(`f"`)
		} else {
			b.WriteByte('"')
		}
		for i := 0; i < len(body); i++ {
			c := body[i]
			if c == '\n' {
				b.WriteString(`\n`)
			} else if c == '"' {
				b.WriteString(`\"`)
			} else {
				b.WriteByte(c)
			}
		}
		b.WriteByte('"')
		return b.String()
	}

	findTripleStart := func(s string) (idx int, delim string, isF bool, ok bool) {
		var q byte
		for i := 0; i < len(s); i++ {
			c := s[i]
			if q != 0 {
				if q != '`' && c == '\\' && i+1 < len(s) {
					i++
				} else if c == q {
					q = 0
				}
				continue
			}
			if c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
				break
			}
			if strings.HasPrefix(s[i:], `f"""`) && (i == 0 || !isIdentCont(s[i-1])) {
				return i, `"""`, true, true
			}
			if strings.HasPrefix(s[i:], `f'''`) && (i == 0 || !isIdentCont(s[i-1])) {
				return i, `'''`, true, true
			}
			if strings.HasPrefix(s[i:], `"""`) {
				return i, `"""`, false, true
			}
			if strings.HasPrefix(s[i:], `'''`) {
				return i, `'''`, false, true
			}
			if c == '"' || c == '\'' || c == '`' {
				q = c
			}
		}
		return 0, "", false, false
	}

	collapseSameLineTriple := func(s string) string {
		cur := s
		for {
			idx, delim, isF, ok := findTripleStart(cur)
			if !ok {
				break
			}
			startOff := idx + 3
			if isF {
				startOff = idx + 4
			}
			closeRel := strings.Index(cur[startOff:], delim)
			if closeRel < 0 {
				break
			}
			closeIdx := startOff + closeRel
			lit := escapeSingleLineStr(cur[startOff:closeIdx], isF)
			cur = cur[:idx] + lit + cur[closeIdx+3:]
		}
		return cur
	}

	for idx, raw := range rawLines {
		lineNo := idx + 1
		if tripleQuote != "" {
			if closeRel := strings.Index(raw, tripleQuote); closeRel >= 0 {
				tripleBody.WriteByte('\n')
				tripleBody.WriteString(raw[:closeRel])
				rest := raw[closeRel+3:]
				acc.WriteString(escapeSingleLineStr(tripleBody.String(), tripleF))
				acc.WriteString(collapseSameLineTriple(rest))
				tripleQuote = ""
				tripleBody.Reset()
				accStr := acc.String()
				code, _, _ := splitInlineHashComment(strings.TrimLeft(accStr, " \t"))
				parenDepth = 0
				updateDepth(code)
				continued = strings.HasSuffix(accStr, `\`)
				if continued {
					acc.Reset()
					acc.WriteString(strings.TrimRight(accStr[:len(accStr)-1], " \t") + " ")
				} else if parenDepth == 0 {
					result = append(result, goraLogicalLine{lineNo: curLineNo, raw: accStr})
					acc.Reset()
				}
			} else {
				tripleBody.WriteByte('\n')
				tripleBody.WriteString(raw)
			}
			continue
		}

		if parenDepth == 0 && !continued {
			if strings.TrimSpace(raw) == "" {
				result = append(result, goraLogicalLine{lineNo: lineNo, raw: raw})
				continue
			}
			if strings.HasSuffix(raw, " ") || strings.HasSuffix(raw, "\t") {
				return nil, &GoraError{Message: "trailing whitespace is not allowed", Line: lineNo, Col: len(raw)}
			}
			curLineNo = lineNo
		}

		line := collapseSameLineTriple(raw)
		if tIdx, delim, isF, ok := findTripleStart(line); ok {
			startOff := tIdx + 3
			if isF {
				startOff = tIdx + 4
			}
			prefix := line[:tIdx]
			if acc.Len() > 0 {
				acc.WriteByte(' ')
				acc.WriteString(strings.TrimLeft(prefix, " \t"))
			} else {
				acc.WriteString(prefix)
			}
			tripleQuote = delim
			tripleF = isF
			tripleBody.WriteString(line[startOff:])
			continue
		}

		if parenDepth > 0 || continued {
			chunk := strings.TrimSpace(line)
			code, _, _ := splitInlineHashComment(chunk)
			if strings.HasSuffix(code, `\`) {
				acc.WriteByte(' ')
				acc.WriteString(strings.TrimRight(code[:len(code)-1], " \t"))
				continued = true
				continue
			}
			continued = false
			accStr := acc.String()
			if strings.HasSuffix(accStr, "(") || strings.HasSuffix(accStr, "[") || strings.HasSuffix(accStr, "{") {
				acc.WriteString(code)
			} else if code != "" {
				acc.WriteByte(' ')
				acc.WriteString(code)
			}
			parenDepth = 0
			accStr = acc.String()
			cCode, _, _ := splitInlineHashComment(strings.TrimLeft(accStr, " \t"))
			updateDepth(cCode)
			if parenDepth == 0 {
				result = append(result, goraLogicalLine{lineNo: curLineNo, raw: accStr})
				acc.Reset()
			}
			continue
		}

		trimmedStart := strings.TrimLeft(line, " \t")
		code, _, hasComment := splitInlineHashComment(trimmedStart)
		if !hasComment && strings.HasSuffix(line, `\`) {
			acc.WriteString(strings.TrimRight(line[:len(line)-1], " \t"))
			continued = true
			continue
		}
		updateDepth(code)
		if parenDepth > 0 {
			wsLen := len(line) - len(trimmedStart)
			acc.WriteString(line[:wsLen])
			acc.WriteString(code)
		} else {
			result = append(result, goraLogicalLine{lineNo: lineNo, raw: line})
		}
	}

	if tripleQuote != "" {
		return nil, &GoraError{Message: "unterminated triple-quoted string", Line: curLineNo, Col: 1}
	}
	if acc.Len() > 0 {
		result = append(result, goraLogicalLine{lineNo: curLineNo, raw: acc.String()})
	}
	return result, nil
}

func GoraToGo(src string) (string, error) {
	// Reject UTF-8 BOM
	if strings.HasPrefix(src, "\xEF\xBB\xBF") {
		return "", &GoraError{Message: "UTF-8 BOM is not allowed", Line: 1, Col: 1}
	}

	logicalLines, err := preprocessLogicalLinesGora(src)
	if err != nil {
		return "", err
	}

	var indentChar byte
	indentWidth := 0
	var stack []blockFrame
	currentLevel := 0
	prevOpenedBlock := false
	prevBlockLine := 0

	hasPackage := false
	hasFmtImport := false
	hasMathImport := false
	packageLineIdx := -1
	var lines []string
	pendingBlanks := 0
	knownStructs := make(map[string]bool)
	ctx := &translateCtx{}

	const goIndent = "    "
	makeIndent := func(lvl int) string {
		return strings.Repeat(goIndent, lvl)
	}
	flushBlanks := func() {
		for pendingBlanks > 0 {
			lines = append(lines, "")
			pendingBlanks--
		}
	}

	closeFrame := func(f blockFrame) {
		switch f.kind {
		case blockCase:
			// no closing brace
		case blockParen:
			lines = append(lines, makeIndent(f.level)+")")
		case blockFuncCall:
			lines = append(lines, makeIndent(f.level)+"}()")
		default:
			lines = append(lines, makeIndent(f.level)+"}")
		}
	}

	for _, ll := range logicalLines {
		lineNo := ll.lineNo
		raw := ll.raw
		ws := 0
		hasSpace := false
		hasTab := false
		for ws < len(raw) {
			if raw[ws] == ' ' {
				hasSpace = true
				ws++
			} else if raw[ws] == '\t' {
				hasTab = true
				ws++
			} else {
				break
			}
		}

		// Blank line
		if ws == len(raw) {
			pendingBlanks++
			continue
		}

		if hasSpace && hasTab {
			return "", &GoraError{Message: "mixed spaces and tabs in indentation", Line: lineNo, Col: ws}
		}
		if raw[len(raw)-1] == ' ' || raw[len(raw)-1] == '\t' {
			return "", &GoraError{Message: "trailing whitespace is not allowed", Line: lineNo, Col: len(raw)}
		}

		level := 0
		if ws > 0 {
			var ch byte = ' '
			if hasTab {
				ch = '\t'
			}
			if indentWidth == 0 {
				if currentLevel == 0 && !prevOpenedBlock {
					return "", &GoraError{Message: "unexpected indentation at root level", Line: lineNo, Col: 1}
				}
				indentChar = ch
				indentWidth = ws
				level = 1
			} else {
				if ch != indentChar {
					return "", &GoraError{Message: "inconsistent indentation character (spaces vs tabs)", Line: lineNo, Col: 1}
				}
				if ws%indentWidth != 0 {
					return "", &GoraError{Message: "indentation is not a multiple of the file's indent unit", Line: lineNo, Col: ws + 1}
				}
				level = ws / indentWidth
			}
		}

		content := raw[ws:]

		if prevOpenedBlock && content[0] != '#' {
			if level != currentLevel {
				return "", &GoraError{Message: "expected indented block after ':'", Line: prevBlockLine, Col: 1}
			}
			prevOpenedBlock = false
		} else if !prevOpenedBlock && level > currentLevel && content[0] != '#' {
			return "", &GoraError{Message: "unexpected indentation increase without a block header", Line: lineNo, Col: ws + 1}
		}

		// Full-line comment
		if content[0] == '#' {
			if !prevOpenedBlock {
				for len(stack) > 0 && stack[len(stack)-1].level >= level {
					top := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					closeFrame(top)
				}
				currentLevel = level
			}
			flushBlanks()
			lines = append(lines, makeIndent(level)+"//"+content[1:])
			continue
		}

		code, inlineComment, hasInline := splitInlineHashComment(content)
		commentSuffix := ""
		if hasInline {
			commentSuffix = " //" + inlineComment
		}

		_, isElif := startsWithKW(code, "elif")
		isElse := code == "else:"
		_, isCase := startsWithKW(code, "case")
		isDefault := code == "default:"

		for len(stack) > 0 && stack[len(stack)-1].level >= level {
			top := stack[len(stack)-1]
			if top.level == level && top.kind == blockNormal && (isElif || isElse) {
				break
			}
			if top.level == level && top.kind == blockCase && (isCase || isDefault) {
				stack = stack[:len(stack)-1]
				break
			}
			stack = stack[:len(stack)-1]
			closeFrame(top)
		}
		flushBlanks()
		currentLevel = level

		// `pass`
		if code == "pass" {
			if hasInline {
				lines = append(lines, makeIndent(level)+"//"+inlineComment)
			}
			continue
		}

		// `package <name>`
		if level == 0 {
			if pkgName, ok := startsWithKW(code, "package"); ok && !strings.HasSuffix(code, ":") {
				hasPackage = true
				packageLineIdx = len(lines)
				lines = append(lines, "package "+strings.TrimSpace(pkgName)+commentSuffix)
				continue
			}
		}

		// Single-line `import ...` and `from ... import *`
		if level == 0 && code != "import:" {
			if impRest, ok := startsWithKW(code, "import"); ok {
				pkgPath, alias := formatGoImportSpec(impRest)
				if pkgPath == `"fmt"` && alias == "" {
					hasFmtImport = true
				}
				if pkgPath == `"math"` && alias == "" {
					hasMathImport = true
				}
				if alias != "" {
					lines = append(lines, "import "+alias+" "+pkgPath+commentSuffix)
				} else {
					lines = append(lines, "import "+pkgPath+commentSuffix)
				}
				continue
			}
			if fromRest, ok := startsWithKW(code, "from"); ok {
				if impIdx := findTopLevelKeyword(fromRest, "import"); impIdx > 0 {
					pkg := strings.TrimSpace(fromRest[:impIdx])
					what := strings.TrimSpace(fromRest[impIdx+6:])
					pkgPath := quoteImportPath(pkg)
					if pkgPath == `"fmt"` {
						hasFmtImport = true
					}
					if pkgPath == `"math"` {
						hasMathImport = true
					}
					if what == "*" {
						lines = append(lines, "import . "+pkgPath+commentSuffix)
					} else if strings.Contains(what, ",") {
						lines = append(lines, "import "+pkgPath+commentSuffix)
					} else {
						lines = append(lines, "import "+what+" "+pkgPath+commentSuffix)
					}
					continue
				}
			}
		}

		// Inside `import:` block
		if isInsideImportBlock(stack, lines) {
			pkgPath, alias := formatGoImportSpec(code)
			if pkgPath == `"fmt"` && alias == "" {
				hasFmtImport = true
			}
			if pkgPath == `"math"` && alias == "" {
				hasMathImport = true
			}
			if alias != "" {
				lines = append(lines, makeIndent(level)+alias+" "+pkgPath+commentSuffix)
			} else {
				lines = append(lines, makeIndent(level)+pkgPath+commentSuffix)
			}
			continue
		}

		// Block headers ending with `:`
		if strings.HasSuffix(code, ":") {
			h := strings.TrimRight(code[:len(code)-1], " ")
			ind := makeIndent(level)

			if isElif && len(stack) > 0 && stack[len(stack)-1].level == level {
				cond, _ := startsWithKW(h, "elif")
				lines = append(lines, ind+"} else if "+translateIfCondition(cond, ctx)+" {"+commentSuffix)
				currentLevel = level + 1
				prevOpenedBlock = true
				prevBlockLine = lineNo
				continue
			}
			if isElse && len(stack) > 0 && stack[len(stack)-1].level == level {
				lines = append(lines, ind+"} else {"+commentSuffix)
				currentLevel = level + 1
				prevOpenedBlock = true
				prevBlockLine = lineNo
				continue
			}

			if h == "import" && level == 0 {
				lines = append(lines, ind+"import ("+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockParen})
			} else if h == "const" || h == "var" || h == "type" {
				lines = append(lines, ind+h+" ("+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockParen})
			} else if after, ok := startsWithKW(h, "if"); ok {
				lines = append(lines, ind+"if "+translateIfCondition(after, ctx)+" {"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockNormal})
			} else if h == "loop" {
				lines = append(lines, ind+"for {"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockNormal})
			} else if after, ok := startsWithKW(h, "while"); ok {
				trimmed := strings.TrimSpace(after)
				if trimmed == "True" || trimmed == "true" {
					lines = append(lines, ind+"for {"+commentSuffix)
				} else {
					lines = append(lines, ind+"for "+translateExprGoraToGo(trimmed, ctx)+" {"+commentSuffix)
				}
				stack = append(stack, blockFrame{level: level, kind: blockNormal})
			} else if after, ok := startsWithKW(h, "for"); ok {
				hdr, prep := translateForHeaderLines(after, ctx)
				lines = append(lines, ind+hdr+commentSuffix)
				if prep != "" {
					lines = append(lines, ind+"    "+prep)
				}
				stack = append(stack, blockFrame{level: level, kind: blockNormal})
			} else if after, ok := startsWithKW(h, "with"); ok {
				lines = append(lines, ind+"{"+commentSuffix)
				if asIdx := findTopLevelKeyword(after, "as"); asIdx > 0 {
					exprS := translateExprGoraToGo(strings.TrimSpace(after[:asIdx]), ctx)
					vname := strings.TrimSpace(after[asIdx+2:])
					lines = append(lines, ind+"    "+vname+" := "+exprS)
					lines = append(lines, ind+"    defer "+vname+".Close()")
				} else {
					lines = append(lines, ind+"    "+translateExprGoraToGo(after, ctx))
				}
				stack = append(stack, blockFrame{level: level, kind: blockNormal})
			} else if h == "match" || h == "switch" {
				lines = append(lines, ind+"switch {"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockSwitch})
			} else if after, ok := startsWithKW(h, "match"); ok {
				lines = append(lines, ind+"switch "+translateExprGoraToGo(after, ctx)+" {"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockSwitch})
			} else if after, ok := startsWithKW(h, "switch"); ok {
				lines = append(lines, ind+"switch "+translateExprGoraToGo(after, ctx)+" {"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockSwitch})
			} else if h == "select" {
				lines = append(lines, ind+"select {"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockSwitch})
			} else if after, ok := startsWithKW(h, "case"); ok {
				lines = append(lines, ind+"case "+translateExprGoraToGo(after, ctx)+":"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockCase})
			} else if h == "default" {
				lines = append(lines, ind+"default:"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockCase})
			} else if after, ok := startsWithKW(h, "struct"); ok {
				sname := strings.TrimSpace(after)
				baseName := sname
				if br := strings.IndexByte(sname, '['); br > 0 {
					baseName = strings.TrimSpace(sname[:br])
					// translate generic params `[T: any]` -> `[T any]`
					if sname[len(sname)-1] == ']' {
						genInner := sname[br+1 : len(sname)-1]
						genParts := splitTopLevel(genInner, ',')
						for gi, gp := range genParts {
							if colon := findTopLevelSep(gp, ':'); colon >= 0 {
								genParts[gi] = strings.TrimSpace(gp[:colon]) + " " + translateTypeGoraToGo(gp[colon+1:])
							}
						}
						sname = baseName + "[" + strings.Join(genParts, ", ") + "]"
					}
				}
				knownStructs[baseName] = true
				lines = append(lines, ind+"type "+sname+" struct {"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockStruct})
			} else if after, ok := startsWithKW(h, "interface"); ok {
				iname := strings.TrimSpace(after)
				if br := strings.IndexByte(iname, '['); br > 0 && iname[len(iname)-1] == ']' {
					baseName := strings.TrimSpace(iname[:br])
					genInner := iname[br+1 : len(iname)-1]
					genParts := splitTopLevel(genInner, ',')
					for gi, gp := range genParts {
						if colon := findTopLevelSep(gp, ':'); colon >= 0 {
							genParts[gi] = strings.TrimSpace(gp[:colon]) + " " + translateTypeGoraToGo(gp[colon+1:])
						}
					}
					iname = baseName + "[" + strings.Join(genParts, ", ") + "]"
				}
				lines = append(lines, ind+"type "+iname+" interface {"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockInterface})
			} else if after, ok := startsWithKW(h, "go def"); ok && strings.HasPrefix(strings.TrimSpace(after), "(") {
				lines = append(lines, ind+"go func() {"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockFuncCall, isFunc: true})
			} else if after, ok := startsWithKW(h, "defer def"); ok && strings.HasPrefix(strings.TrimSpace(after), "(") {
				lines = append(lines, ind+"defer func() {"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockFuncCall, isFunc: true})
			} else if after, ok := startsWithKW(h, "def"); ok {
				recv, name, typeParams, params, retGo, retTypes, parseOK := parseFunctionHeader(after)
				if !parseOK {
					return "", &GoraError{Message: "invalid function definition", Line: lineNo, Col: 1}
				}
				var sig strings.Builder
				sig.WriteString(ind)
				sig.WriteString("func ")
				if recv != "" {
					sig.WriteByte('(')
					sig.WriteString(recv)
					sig.WriteString(") ")
				}
				sig.WriteString(name)
				sig.WriteString(typeParams)
				sig.WriteByte('(')
				sig.WriteString(params)
				sig.WriteByte(')')
				if retGo != "" {
					sig.WriteByte(' ')
					sig.WriteString(retGo)
				}
				sig.WriteString(" {")
				sig.WriteString(commentSuffix)
				lines = append(lines, sig.String())
				stack = append(stack, blockFrame{
					level:    level,
					kind:     blockNormal,
					isFunc:   true,
					retTypes: retTypes,
				})
			} else if after, ok := startsWithKW(h, "fn"); ok {
				recv, name, typeParams, params, retGo, retTypes, parseOK := parseFunctionHeader(after)
				if !parseOK {
					return "", &GoraError{Message: "invalid function definition", Line: lineNo, Col: 1}
				}
				var sig strings.Builder
				sig.WriteString(ind)
				sig.WriteString("func ")
				if recv != "" {
					sig.WriteByte('(')
					sig.WriteString(recv)
					sig.WriteString(") ")
				}
				sig.WriteString(name)
				sig.WriteString(typeParams)
				sig.WriteByte('(')
				sig.WriteString(params)
				sig.WriteByte(')')
				if retGo != "" {
					sig.WriteByte(' ')
					sig.WriteString(retGo)
				}
				sig.WriteString(" {")
				sig.WriteString(commentSuffix)
				lines = append(lines, sig.String())
				stack = append(stack, blockFrame{
					level:    level,
					kind:     blockNormal,
					isFunc:   true,
					retTypes: retTypes,
				})
			} else {
				// Generic Go block (e.g. `func() {` or raw header)
				lines = append(lines, ind+translateExprGoraToGo(h, ctx)+" {"+commentSuffix)
				stack = append(stack, blockFrame{level: level, kind: blockNormal})
			}

			currentLevel = level + 1
			prevOpenedBlock = true
			prevBlockLine = lineNo
			continue
		}

		// Non-block statement at `level`
		ind := makeIndent(level)

		// Inside `struct` block: `name: type [tag]` or embedded type
		if len(stack) > 0 && stack[len(stack)-1].kind == blockStruct && stack[len(stack)-1].level+1 == level {
			if lhs, typAndTag, _, hasEq, ok := findDeclColon(code); ok && !hasEq {
				typ, tag := splitStructTag(typAndTag)
				goT := translateTypeGoraToGo(typ)
				lineOut := ind + lhs + " " + goT
				if tag != "" {
					lineOut += " " + tag
				}
				lines = append(lines, lineOut+commentSuffix)
				continue
			}
			lines = append(lines, ind+translateTypeGoraToGo(code)+commentSuffix)
			continue
		}

		// Inside `interface` block: `method(params) -> ret` or embedded interface
		if len(stack) > 0 && stack[len(stack)-1].kind == blockInterface && stack[len(stack)-1].level+1 == level {
			if _, name, typeParams, params, retGo, _, ok := parseFunctionHeader(code); ok {
				var sig strings.Builder
				sig.WriteString(ind)
				sig.WriteString(name)
				sig.WriteString(typeParams)
				sig.WriteByte('(')
				sig.WriteString(params)
				sig.WriteByte(')')
				if retGo != "" {
					sig.WriteByte(' ')
					sig.WriteString(retGo)
				}
				lines = append(lines, sig.String()+commentSuffix)
				continue
			}
			lines = append(lines, ind+translateTypeGoraToGo(code)+commentSuffix)
			continue
		}

		// Inside `const:` / `var:` / `type:` parenthesized block
		if len(stack) > 0 && stack[len(stack)-1].kind == blockParen && stack[len(stack)-1].level+1 == level {
			if lhs, typ, val, hasEq, ok := findDeclColon(code); ok {
				goT := translateTypeGoraToGo(typ)
				if hasEq {
					lines = append(lines, ind+lhs+" "+goT+" = "+translateExprGoraToGo(val, ctx)+commentSuffix)
				} else {
					lines = append(lines, ind+lhs+" "+goT+commentSuffix)
				}
				continue
			}
			lines = append(lines, ind+translateExprGoraToGo(code, ctx)+commentSuffix)
			continue
		}

		// `type Alias = Target` or `type Defined(Target)` or `type Defined: Target`
		if level == 0 {
			if rest, ok := startsWithKW(code, "type"); ok {
				if eq := findTopLevelSep(rest, '='); eq > 0 {
					alias := strings.TrimSpace(rest[:eq])
					target := translateTypeGoraToGo(rest[eq+1:])
					lines = append(lines, ind+"type "+alias+" = "+target+commentSuffix)
					continue
				}
				if col := findTopLevelSep(rest, ':'); col > 0 {
					name := strings.TrimSpace(rest[:col])
					target := translateTypeGoraToGo(rest[col+1:])
					lines = append(lines, ind+"type "+name+" "+target+commentSuffix)
					continue
				}
			}
		}

		// `const NAME = val` (untyped constant)
		if after, ok := startsWithKW(code, "const"); ok {
			if lhs, typ, val, hasEq, dok := findDeclColon(after); dok {
				goT := translateTypeGoraToGo(typ)
				if hasEq {
					lines = append(lines, ind+"const "+lhs+" "+goT+" = "+translateExprGoraToGo(val, ctx)+commentSuffix)
				} else {
					lines = append(lines, ind+"const "+lhs+" "+goT+commentSuffix)
				}
				continue
			}
			lines = append(lines, ind+"const "+translateExprGoraToGo(after, ctx)+commentSuffix)
			continue
		}

		// `var name = val` (untyped var declaration without `: type`)
		if after, ok := startsWithKW(code, "var"); ok {
			if _, _, _, _, dok := findDeclColon(after); !dok {
				lines = append(lines, ind+"var "+translateExprGoraToGo(after, ctx)+commentSuffix)
				continue
			}
		}

		// `try fn()` standalone error propagation
		if afterTry, ok := startsWithKW(code, "try"); ok {
			fn := activeFuncFrame(stack)
			if fn == nil || len(fn.retTypes) == 0 || fn.retTypes[len(fn.retTypes)-1] != "error" {
				return "", &GoraError{Message: "'try' requires enclosing function to return error", Line: lineNo, Col: 1}
			}
			callExpr := translateExprGoraToGo(strings.TrimSpace(afterTry), ctx)
			lines = append(lines, ind+"if err := "+callExpr+"; err != nil {"+commentSuffix)
			lines = append(lines, emitTryReturnLines(ind, fn.retTypes, knownStructs)...)
			lines = append(lines, ind+"}")
			continue
		}

		// `raise expr` -> `panic(expr)`
		if afterRaise, ok := startsWithKW(code, "raise"); ok {
			if strings.TrimSpace(afterRaise) == "" {
				lines = append(lines, ind+`panic("error")`+commentSuffix)
			} else {
				lines = append(lines, ind+"panic("+translateExprGoraToGo(strings.TrimSpace(afterRaise), ctx)+")"+commentSuffix)
			}
			continue
		}

		// `del m[k]` -> `delete(m, k)`
		if afterDel, ok := startsWithKW(code, "del"); ok && afterDel != "" {
			d := strings.TrimSpace(afterDel)
			if ob := findLastTopLevelOpenBracket(d); ob > 0 && strings.HasSuffix(d, "]") {
				mExpr := translateExprGoraToGo(strings.TrimSpace(d[:ob]), ctx)
				kExpr := translateExprGoraToGo(strings.TrimSpace(d[ob+1:len(d)-1]), ctx)
				lines = append(lines, ind+"delete("+mExpr+", "+kExpr+")"+commentSuffix)
				continue
			}
		}

		// `return a if cond else b` or `return [comp]`
		if afterRet, ok := startsWithKW(code, "return"); ok {
			if tExpr, condExpr, fExpr, tok := splitTernary(afterRet); tok {
				lines = append(lines, ind+"if "+translateIfCondition(condExpr, ctx)+" {"+commentSuffix)
				lines = append(lines, ind+"    return "+translateExprGoraToGo(tExpr, ctx))
				lines = append(lines, ind+"} else {")
				lines = append(lines, ind+"    return "+translateExprGoraToGo(fExpr, ctx))
				lines = append(lines, ind+"}")
				continue
			}
			if isComprehensionLiteral(afterRet) {
				if fn := activeFuncFrame(stack); fn != nil && len(fn.retTypes) == 1 {
					if compLines, cok := tryEmitGoraComprehension(ind, "_comp", fn.retTypes[0], afterRet, ctx); cok {
						if hasInline {
							compLines[0] += commentSuffix
						}
						lines = append(lines, compLines...)
						lines = append(lines, ind+"return _comp")
						continue
					}
				}
			}
			if strings.TrimSpace(afterRet) == "" {
				lines = append(lines, ind+"return"+commentSuffix)
			} else {
				lines = append(lines, ind+"return "+translateExprGoraToGo(afterRet, ctx)+commentSuffix)
			}
			continue
		}

		// Typed variable declaration: `[var] name: type [= val]`
		if lhs, typ, val, hasEq, ok := findDeclColon(code); ok {
			isConst := false
			varName := lhs
			if after, okC := startsWithKW(varName, "const"); okC {
				isConst = true
				varName = strings.TrimSpace(after)
			} else if after, okV := startsWithKW(varName, "var"); okV {
				varName = strings.TrimSpace(after)
			}
			goT := translateTypeGoraToGo(typ)
			if !hasEq {
				if isConst {
					lines = append(lines, ind+"const "+varName+" "+goT+commentSuffix)
				} else {
					lines = append(lines, ind+"var "+varName+" "+goT+commentSuffix)
				}
				continue
			}
			if !isConst {
				if compLines, cok := tryEmitGoraComprehension(ind, varName, typ, val, ctx); cok {
					if hasInline {
						compLines[0] += commentSuffix
					}
					lines = append(lines, compLines...)
					continue
				}
				if tExpr, condExpr, fExpr, tok := splitTernary(val); tok {
					lines = append(lines, ind+"var "+varName+" "+goT+commentSuffix)
					lines = append(lines, ind+"if "+translateIfCondition(condExpr, ctx)+" {")
					lines = append(lines, ind+"    "+varName+" = "+translateExprGoraToGo(tExpr, ctx))
					lines = append(lines, ind+"} else {")
					lines = append(lines, ind+"    "+varName+" = "+translateExprGoraToGo(fExpr, ctx))
					lines = append(lines, ind+"}")
					continue
				}
			}
			kw := "var "
			if isConst {
				kw = "const "
			}
			lines = append(lines, ind+kw+varName+" "+goT+" = "+translateExprGoraToGo(val, ctx)+commentSuffix)
			continue
		}

		// Short declaration `lhs := rhs` (check `try` and error on untyped comprehension)
		if colonEq := findTopLevelStr(code, ":="); colonEq > 0 {
			lhs := strings.TrimSpace(code[:colonEq])
			rhs := strings.TrimSpace(code[colonEq+2:])
			if afterTry, ok := startsWithKW(rhs, "try"); ok {
				fn := activeFuncFrame(stack)
				if fn == nil || len(fn.retTypes) == 0 || fn.retTypes[len(fn.retTypes)-1] != "error" {
					return "", &GoraError{Message: "'try' requires enclosing function to return error", Line: lineNo, Col: 1}
				}
				callExpr := translateExprGoraToGo(strings.TrimSpace(afterTry), ctx)
				lines = append(lines, ind+lhs+", err := "+callExpr+commentSuffix)
				lines = append(lines, ind+"if err != nil {")
				lines = append(lines, emitTryReturnLines(ind, fn.retTypes, knownStructs)...)
				lines = append(lines, ind+"}")
				fn.errDeclared = true
				continue
			}
			if tExpr, condExpr, fExpr, tok := splitTernary(rhs); tok {
				ctx.usedIfHelper = true
				lines = append(lines, ind+lhs+" := _goraIf("+translateExprGoraToGo(condExpr, ctx)+", "+
					translateExprGoraToGo(tExpr, ctx)+", "+translateExprGoraToGo(fExpr, ctx)+")"+commentSuffix)
				continue
			}
			if isComprehensionLiteral(rhs) {
				return "", &GoraError{Message: "comprehension requires an explicit container type (e.g. 'xs: list[i32] = [...]')", Line: lineNo, Col: 1}
			}
			lines = append(lines, ind+lhs+" := "+translateExprGoraToGo(rhs, ctx)+commentSuffix)
			// Mark if lhs declared `err`
			for _, pName := range splitTopLevel(lhs, ',') {
				if pName == "err" {
					if fn := activeFuncFrame(stack); fn != nil {
						fn.errDeclared = true
					}
				}
			}
			continue
		}

		// Assignment `lhs = rhs` (check `try` and ternary `a if cond else b`)
		if eqIdx := findTopLevelAssignEq(code); eqIdx > 0 {
			lhs := strings.TrimSpace(code[:eqIdx])
			rhs := strings.TrimSpace(code[eqIdx+1:])
			if afterTry, ok := startsWithKW(rhs, "try"); ok {
				fn := activeFuncFrame(stack)
				if fn == nil || len(fn.retTypes) == 0 || fn.retTypes[len(fn.retTypes)-1] != "error" {
					return "", &GoraError{Message: "'try' requires enclosing function to return error", Line: lineNo, Col: 1}
				}
				callExpr := translateExprGoraToGo(strings.TrimSpace(afterTry), ctx)
				if !fn.errDeclared {
					lines = append(lines, ind+"var err error")
					fn.errDeclared = true
				}
				lines = append(lines, ind+lhs+", err = "+callExpr+commentSuffix)
				lines = append(lines, ind+"if err != nil {")
				lines = append(lines, emitTryReturnLines(ind, fn.retTypes, knownStructs)...)
				lines = append(lines, ind+"}")
				continue
			}
			if tExpr, condExpr, fExpr, tok := splitTernary(rhs); tok {
				lines = append(lines, ind+"if "+translateIfCondition(condExpr, ctx)+" {"+commentSuffix)
				lines = append(lines, ind+"    "+lhs+" = "+translateExprGoraToGo(tExpr, ctx))
				lines = append(lines, ind+"} else {")
				lines = append(lines, ind+"    "+lhs+" = "+translateExprGoraToGo(fExpr, ctx))
				lines = append(lines, ind+"}")
				continue
			}
			lines = append(lines, ind+translateExprGoraToGo(lhs, ctx)+" = "+translateExprGoraToGo(rhs, ctx)+commentSuffix)
			continue
		}

		// Standard statement
		lines = append(lines, ind+translateExprGoraToGo(code, ctx)+commentSuffix)
	}

	if prevOpenedBlock {
		return "", &GoraError{Message: "expected indented block after ':' at end of file", Line: prevBlockLine, Col: 1}
	}

	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		closeFrame(top)
	}
	flushBlanks()

	// Synthesize `package main` if omitted, and `import "fmt"` / `import "math"` if needed.
	var autoImports []string
	if ctx.usedFmt && !hasFmtImport {
		autoImports = append(autoImports, `import "fmt"`)
	}
	if ctx.usedMath && !hasMathImport {
		autoImports = append(autoImports, `import "math"`)
	}

	var finalLines []string
	if !hasPackage {
		insertAt := 0
		for insertAt < len(lines) && (lines[insertAt] == "" || strings.HasPrefix(lines[insertAt], "//")) {
			insertAt++
		}
		finalLines = append(finalLines, lines[:insertAt]...)
		finalLines = append(finalLines, "package main")
		if len(autoImports) > 0 {
			finalLines = append(finalLines, "")
			finalLines = append(finalLines, autoImports...)
		}
		if insertAt < len(lines) && lines[insertAt] != "" {
			finalLines = append(finalLines, "")
		}
		finalLines = append(finalLines, lines[insertAt:]...)
	} else if len(autoImports) > 0 && packageLineIdx >= 0 {
		finalLines = append(finalLines, lines[:packageLineIdx+1]...)
		finalLines = append(finalLines, "")
		finalLines = append(finalLines, autoImports...)
		finalLines = append(finalLines, lines[packageLineIdx+1:]...)
	} else {
		finalLines = lines
	}

	if ctx.usedInHelper {
		finalLines = append(finalLines, "", "func _goraIn[K comparable, V any](m map[K]V, k K) bool { _, ok := m[k]; return ok }")
	}
	if ctx.usedIfHelper {
		finalLines = append(finalLines, "", "func _goraIf[T any](cond bool, a, b T) T { if cond { return a }; return b }")
	}
	if ctx.usedFloorDiv || ctx.usedPow {
		finalLines = append(finalLines, "", "type _goraNum interface { ~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64 }")
	}
	if ctx.usedFloorDiv {
		finalLines = append(finalLines, "", "func _goraFloorDiv[T _goraNum](a, b T) T { return T(math.Floor(float64(a) / float64(b))) }")
	}
	if ctx.usedPow {
		finalLines = append(finalLines, "", "func _goraPow[T _goraNum](a, b T) T { return T(math.Pow(float64(a), float64(b))) }")
	}

	out := strings.Join(finalLines, "\n")
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out, nil
}

func isInsideImportBlock(stack []blockFrame, lines []string) bool {
	if len(stack) == 0 {
		return false
	}
	top := stack[len(stack)-1]
	if top.kind != blockParen {
		return false
	}
	// Walk backwards to find the opening `(` at top.level
	prefix := strings.Repeat("    ", top.level) + "import ("
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], prefix) {
			return true
		}
		if strings.HasPrefix(lines[i], strings.Repeat("    ", top.level)+"const (") ||
			strings.HasPrefix(lines[i], strings.Repeat("    ", top.level)+"var (") ||
			strings.HasPrefix(lines[i], strings.Repeat("    ", top.level)+"type (") {
			return false
		}
	}
	return false
}

func findTopLevelStr(s, sub string) int {
	depth := 0
	var quote byte
	for i := 0; i+len(sub) <= len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '(' || c == '[' || c == '{' {
			depth++
			continue
		}
		if c == ')' || c == ']' || c == '}' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 && s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func findTopLevelAssignEq(s string) int {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '(' || c == '[' || c == '{' {
			depth++
			continue
		}
		if c == ')' || c == ']' || c == '}' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth == 0 && c == '=' {
			if i+1 < len(s) && s[i+1] == '=' {
				i++
				continue
			}
			if i > 0 && (s[i-1] == ':' || s[i-1] == '!' || s[i-1] == '<' || s[i-1] == '>' ||
				s[i-1] == '+' || s[i-1] == '-' || s[i-1] == '*' || s[i-1] == '/' ||
				s[i-1] == '%' || s[i-1] == '&' || s[i-1] == '|' || s[i-1] == '^') {
				continue
			}
			return i
		}
	}
	return -1
}

func isComprehensionLiteral(val string) bool {
	val = strings.TrimSpace(val)
	if len(val) < 8 {
		return false
	}
	isBracket := val[0] == '[' && findMatchingDelim(val, 0, '[', ']') == len(val)-1
	isBrace := val[0] == '{' && findMatchingDelim(val, 0, '{', '}') == len(val)-1
	if !isBracket && !isBrace {
		return false
	}
	inner := strings.TrimSpace(val[1 : len(val)-1])
	return findTopLevelKeyword(inner, "for") > 0
}

func validIndentUnit(unit string) bool {
	if unit == "" {
		return false
	}
	ch := unit[0]
	if ch != ' ' && ch != '\t' {
		return false
	}
	for i := 0; i < len(unit); i++ {
		if unit[i] != ch {
			return false
		}
	}
	return true
}

// FormatGoraIndent formats/canonicalizes a Gora file using the requested indent unit.
func FormatGoraIndent(src, indentUnit string) (string, error) {
	if !validIndentUnit(indentUnit) {
		return "", &GoraError{Message: "invalid indent unit", Line: 1, Col: 1}
	}
	goCode, err := GoraToGo(src)
	if err != nil {
		return "", err
	}
	return GoToGoraIndent(goCode, indentUnit)
}

func GoToGora(src string) (string, error) {
	return GoToGoraIndent(src, "    ")
}

func translateTypeGoToGora(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "...") {
		return "..." + translateTypeGoToGora(s[3:])
	}
	if strings.HasPrefix(s, "*") {
		return "addr[" + translateTypeGoToGora(s[1:]) + "]"
	}
	if strings.HasPrefix(s, "[]") {
		return "list[" + translateTypeGoToGora(s[2:]) + "]"
	}
	if strings.HasPrefix(s, "<-chan ") {
		return "in_chan[" + translateTypeGoToGora(s[7:]) + "]"
	}
	if strings.HasPrefix(s, "chan<- ") {
		return "out_chan[" + translateTypeGoToGora(s[7:]) + "]"
	}
	if strings.HasPrefix(s, "chan ") {
		return "chan[" + translateTypeGoToGora(s[5:]) + "]"
	}
	if strings.HasPrefix(s, "map[") {
		closeB := findMatchingDelim(s, 3, '[', ']')
		if closeB > 3 && closeB+1 < len(s) {
			k := translateTypeGoToGora(s[4:closeB])
			vRaw := strings.TrimSpace(s[closeB+1:])
			if vRaw == "bool" {
				return "set[" + k + "]"
			}
			return "dict[" + k + ", " + translateTypeGoToGora(vRaw) + "]"
		}
	}
	if strings.HasPrefix(s, "[") {
		closeB := findMatchingDelim(s, 0, '[', ']')
		if closeB > 1 && closeB+1 < len(s) {
			return "[" + translateExprGoToGora(s[1:closeB]) + "]" + translateTypeGoToGora(s[closeB+1:])
		}
	}
	if strings.HasPrefix(s, "func(") {
		rp := findMatchingDelim(s, 4, '(', ')')
		if rp > 4 {
			params := translateGoParamListToGora(s[5:rp])
			rest := strings.TrimSpace(s[rp+1:])
			if rest != "" {
				return "fn(" + params + ") -> " + translateGoReturnListToGora(rest)
			}
			return "fn(" + params + ")"
		}
	}
	// Generic instantiation `Box[int32]` -> `Box[i32]`
	if br := strings.IndexByte(s, '['); br > 0 && s[len(s)-1] == ']' {
		base := s[:br]
		args := splitTopLevel(s[br+1:len(s)-1], ',')
		for i, a := range args {
			args[i] = translateTypeGoToGora(a)
		}
		return base + "[" + strings.Join(args, ", ") + "]"
	}
	if mapped, ok := goToGoraTypeMap[s]; ok {
		return mapped
	}
	return s
}

// translateGoParamListToGora converts `a, b int32, s string` -> `a: i32, b: i32, s: str`.
func translateGoParamListToGora(params string) string {
	params = strings.TrimSpace(params)
	if params == "" {
		return ""
	}
	groups := splitTopLevel(params, ',')
	type paramPair struct {
		name string
		typ  string
	}
	var parsed []paramPair
	for _, g := range groups {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		// Check if g has both name and type or only name (for grouped `a, b int32`)
		sp := findFirstTopLevelSpace(g)
		if sp > 0 {
			name := strings.TrimSpace(g[:sp])
			typ := strings.TrimSpace(g[sp+1:])
			parsed = append(parsed, paramPair{name: name, typ: typ})
		} else {
			parsed = append(parsed, paramPair{name: g, typ: ""})
		}
	}
	// Backfill types for grouped params like `a, b int32`
	lastType := ""
	allTypedOrUnnamed := true
	for i := len(parsed) - 1; i >= 0; i-- {
		if parsed[i].typ != "" {
			lastType = parsed[i].typ
		} else if lastType != "" {
			parsed[i].typ = lastType
		} else {
			allTypedOrUnnamed = false
		}
	}
	var out []string
	if !allTypedOrUnnamed {
		// Unnamed type-only parameters like `func(int32, string)`
		for _, p := range parsed {
			out = append(out, translateTypeGoToGora(p.name))
		}
		return strings.Join(out, ", ")
	}
	for _, p := range parsed {
		out = append(out, p.name+": "+translateTypeGoToGora(p.typ))
	}
	return strings.Join(out, ", ")
}

func findFirstTopLevelSpace(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		case ' ':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func translateGoReturnListToGora(ret string) string {
	ret = strings.TrimSpace(ret)
	if ret == "" {
		return ""
	}
	if strings.HasPrefix(ret, "(") && findMatchingDelim(ret, 0, '(', ')') == len(ret)-1 {
		inner := ret[1 : len(ret)-1]
		parts := splitTopLevel(inner, ',')
		var out []string
		hasNamed := false
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if sp := findFirstTopLevelSpace(p); sp > 0 {
				hasNamed = true
			}
		}
		if hasNamed {
			return "(" + translateGoParamListToGora(inner) + ")"
		}
		for _, p := range parts {
			out = append(out, translateTypeGoToGora(p))
		}
		return strings.Join(out, ", ")
	}
	return translateTypeGoToGora(ret)
}

func isGoUnaryPrefixPos(s string, idx int) bool {
	j := idx
	for j > 0 && s[j-1] == ' ' {
		j--
	}
	if j == 0 {
		return true
	}
	prev := s[j-1]
	if prev == '(' || prev == '[' || prev == '{' || prev == ',' || prev == '=' ||
		prev == ':' || prev == ';' || prev == '!' || prev == '+' || prev == '-' ||
		prev == '*' || prev == '/' || prev == '%' || prev == '&' || prev == '|' ||
		prev == '^' || prev == '<' || prev == '>' {
		return true
	}
	if isIdentCont(prev) {
		wStart := j - 1
		for wStart > 0 && isIdentCont(s[wStart-1]) {
			wStart--
		}
		if wStart == 0 || !isIdentCont(s[wStart-1]) {
			kw := s[wStart:j]
			if kw == "return" || kw == "case" {
				return true
			}
		}
	}
	return false
}

func scanGoUnaryOperand(s string, start int) int {
	i := start
	n := len(s)
	if i < n && (s[i] == '*' || s[i] == '&') {
		return scanGoUnaryOperand(s, i+1)
	}
	if i < n && s[i] == '(' {
		cp := findMatchingDelim(s, i, '(', ')')
		if cp >= 0 {
			i = cp + 1
		}
	}
	for i < n {
		if isIdentCont(s[i]) || s[i] == '.' {
			i++
		} else if s[i] == '(' {
			cp := findMatchingDelim(s, i, '(', ')')
			if cp >= 0 {
				i = cp + 1
			} else {
				break
			}
		} else if s[i] == '[' {
			cb := findMatchingDelim(s, i, '[', ']')
			if cb >= 0 {
				i = cb + 1
			} else {
				break
			}
		} else {
			break
		}
	}
	return i
}

// tryConvertFmtSprintf converts `fmt.Sprintf("...", args...)` -> `f"..."` when simple.
func tryConvertFmtSprintf(argsStr string) (string, bool) {
	args := splitTopLevel(argsStr, ',')
	if len(args) < 1 {
		return "", false
	}
	fmtLit := strings.TrimSpace(args[0])
	if len(fmtLit) < 2 || fmtLit[0] != '"' || fmtLit[len(fmtLit)-1] != '"' {
		return "", false
	}
	inner := fmtLit[1 : len(fmtLit)-1]
	var out strings.Builder
	out.WriteString(`f"`)
	argIdx := 1
	i := 0
	for i < len(inner) {
		c := inner[i]
		if c == '\\' && i+1 < len(inner) {
			out.WriteByte(c)
			out.WriteByte(inner[i+1])
			i += 2
			continue
		}
		if c == '{' {
			out.WriteString("{{")
			i++
			continue
		}
		if c == '}' {
			out.WriteString("}}")
			i++
			continue
		}
		if c == '%' {
			if i+1 < len(inner) && inner[i+1] == '%' {
				out.WriteByte('%')
				i += 2
				continue
			}
			// Parse format verb (e.g. %v, %s, %d, %.2f, %04d, %#x)
			j := i + 1
			for j < len(inner) && (inner[j] == '#' || inner[j] == '+' || inner[j] == '-' ||
				inner[j] == ' ' || inner[j] == '0' || (inner[j] >= '1' && inner[j] <= '9') || inner[j] == '.') {
				j++
			}
			if j >= len(inner) {
				return "", false
			}
			verb := inner[j]
			spec := inner[i+1 : j+1]
			if verb == '[' {
				return "", false // indexed arg not supported in f-string
			}
			if argIdx >= len(args) {
				return "", false
			}
			expr := translateExprGoToGora(args[argIdx])
			argIdx++
			out.WriteByte('{')
			out.WriteString(expr)
			if spec != "v" {
				out.WriteByte(':')
				out.WriteString(spec)
			}
			out.WriteByte('}')
			i = j + 1
			continue
		}
		out.WriteByte(c)
		i++
	}
	if argIdx != len(args) {
		return "", false
	}
	out.WriteByte('"')
	return out.String(), true
}

func translateExprGoToGora(s string) string {
	if strings.HasPrefix(s, "case ") && strings.HasSuffix(s, ":") {
		return "case " + translateExprGoToGora(strings.TrimSpace(s[5:len(s)-1])) + ":"
	}
	if arrowIdx := findTopLevelStr(s, "<-"); arrowIdx > 0 && !isGoUnaryPrefixPos(s, arrowIdx) {
		chExpr := translateExprGoToGora(strings.TrimSpace(s[:arrowIdx]))
		valExpr := translateExprGoToGora(strings.TrimSpace(s[arrowIdx+2:]))
		return chExpr + ".put(" + valExpr + ")"
	}

	var out strings.Builder
	i := 0
	n := len(s)

	for i < n {
		c := s[i]

		// String, rune, raw string
		if c == '"' || c == '\'' || c == '`' {
			quote := c
			out.WriteByte(c)
			i++
			for i < n {
				sc := s[i]
				out.WriteByte(sc)
				i++
				if quote != '`' && sc == '\\' && i < n {
					out.WriteByte(s[i])
					i++
				} else if sc == quote {
					break
				}
			}
			continue
		}

		// `(*T)(expr)` pointer cast -> `cast(addr[T], expr)`
		if c == '(' && i+1 < n && s[i+1] == '*' && isGoUnaryPrefixPos(s, i) {
			cpType := findMatchingDelim(s, i, '(', ')')
			if cpType > i+2 && cpType+1 < n && s[cpType+1] == '(' {
				cpArg := findMatchingDelim(s, cpType+1, '(', ')')
				if cpArg > cpType+1 {
					goraType := translateTypeGoToGora(s[i+1 : cpType])
					argExpr := translateExprGoToGora(s[cpType+2 : cpArg])
					out.WriteString("cast(" + goraType + ", " + argExpr + ")")
					i = cpArg + 1
					continue
				}
			}
		}

		// Inline `func(...) ... { return ... }` -> `lambda ...`
		if i+5 <= n && s[i:i+5] == "func(" && (i == 0 || !isIdentCont(s[i-1])) {
			rp := findMatchingDelim(s, i+4, '(', ')')
			if rp > i+4 {
				// Find `{` before any newline
				lb := rp + 1
				for lb < n && s[lb] == ' ' {
					lb++
				}
				retStart := lb
				for lb < n && s[lb] != '{' {
					lb++
				}
				if lb < n && s[lb] == '{' {
					rb := findMatchingDelim(s, lb, '{', '}')
					if rb > lb && rb < n {
						body := strings.TrimSpace(s[lb+1 : rb])
						if !strings.Contains(body, "\n") && !strings.Contains(body, ";") {
							goraParams := translateGoParamListToGora(s[i+5 : rp])
							retRaw := strings.TrimSpace(s[retStart:lb])
							goraRet := translateGoReturnListToGora(retRaw)
							isCallImmediate := rb+2 < n && s[rb+1] == '(' && s[rb+2] == ')'
							if !isCallImmediate {
								out.WriteString("lambda ")
								if goraParams == "" {
									out.WriteString("()")
								} else if strings.Contains(goraParams, ",") {
									out.WriteString("(" + goraParams + ")")
								} else {
									out.WriteString(goraParams)
								}
								if goraRet != "" {
									out.WriteString(" -> " + goraRet)
								}
								out.WriteString(": ")
								if afterRet, ok := startsWithKW(body, "return"); ok {
									out.WriteString(translateExprGoToGora(afterRet))
								} else {
									out.WriteString(translateExprGoToGora(body))
								}
								i = rb + 1
								continue
							}
						}
					}
				}
			}
		}

		// Logical operators
		if c == '&' && i+1 < n && s[i+1] == '&' {
			if out.Len() > 0 && out.String()[out.Len()-1] != ' ' {
				out.WriteByte(' ')
			}
			out.WriteString("and")
			i += 2
			if i < n && s[i] != ' ' {
				out.WriteByte(' ')
			}
			continue
		}
		if c == '|' && i+1 < n && s[i+1] == '|' {
			if out.Len() > 0 && out.String()[out.Len()-1] != ' ' {
				out.WriteByte(' ')
			}
			out.WriteString("or")
			i += 2
			if i < n && s[i] != ' ' {
				out.WriteByte(' ')
			}
			continue
		}
		// `!_goraIn(m, k)` -> `k not in m`, `_goraIn(m, k)` -> `k in m`
		if i+9 <= n && s[i:i+9] == "!_goraIn(" {
			cp := findMatchingDelim(s, i+8, '(', ')')
			if cp > i+8 {
				args := splitTopLevel(s[i+9:cp], ',')
				if len(args) == 2 {
					out.WriteString(translateExprGoToGora(args[1]) + " not in " + translateExprGoToGora(args[0]))
					i = cp + 1
					continue
				}
			}
		}
		if i+8 <= n && s[i:i+8] == "_goraIn(" && (i == 0 || !isIdentCont(s[i-1])) {
			cp := findMatchingDelim(s, i+7, '(', ')')
			if cp > i+7 {
				args := splitTopLevel(s[i+8:cp], ',')
				if len(args) == 2 {
					out.WriteString(translateExprGoToGora(args[1]) + " in " + translateExprGoToGora(args[0]))
					i = cp + 1
					continue
				}
			}
		}
		if i+8 <= n && s[i:i+8] == "_goraIf(" && (i == 0 || !isIdentCont(s[i-1])) {
			cp := findMatchingDelim(s, i+7, '(', ')')
			if cp > i+7 {
				args := splitTopLevel(s[i+8:cp], ',')
				if len(args) == 3 {
					out.WriteString(translateExprGoToGora(args[1]) + " if " + translateExprGoToGora(args[0]) + " else " + translateExprGoToGora(args[2]))
					i = cp + 1
					continue
				}
			}
		}
		if i+14 <= n && s[i:i+14] == "_goraFloorDiv(" && (i == 0 || !isIdentCont(s[i-1])) {
			cp := findMatchingDelim(s, i+13, '(', ')')
			if cp > i+13 {
				args := splitTopLevel(s[i+14:cp], ',')
				if len(args) == 2 {
					out.WriteString(translateExprGoToGora(args[0]) + " // " + translateExprGoToGora(args[1]))
					i = cp + 1
					continue
				}
			}
		}
		if i+9 <= n && s[i:i+9] == "_goraPow(" && (i == 0 || !isIdentCont(s[i-1])) {
			cp := findMatchingDelim(s, i+8, '(', ')')
			if cp > i+8 {
				args := splitTopLevel(s[i+9:cp], ',')
				if len(args) == 2 {
					out.WriteString(translateExprGoToGora(args[0]) + " ** " + translateExprGoToGora(args[1]))
					i = cp + 1
					continue
				}
			}
		}

		if c == '!' && (i+1 >= n || s[i+1] != '=') {
			out.WriteString("not ")
			i++
			continue
		}

		// Unary channel receive `<-ch` -> `ch.get()`
		if c == '<' && i+1 < n && s[i+1] == '-' && i+2 < n && s[i+2] != ' ' &&
			isGoUnaryPrefixPos(s, i) {
			opEnd := scanGoUnaryOperand(s, i+2)
			if opEnd > i+2 {
				out.WriteString(translateExprGoToGora(s[i+2 : opEnd]))
				out.WriteString(".get()")
				i = opEnd
				continue
			}
		}

		// Unary `&expr` -> `expr.addr`
		if c == '&' && i+1 < n && s[i+1] != '&' && s[i+1] != '=' && s[i+1] != '^' && s[i+1] != ' ' &&
			isGoUnaryPrefixPos(s, i) {
			opEnd := scanGoUnaryOperand(s, i+1)
			if opEnd > i+1 {
				out.WriteString(translateExprGoToGora(s[i+1 : opEnd]))
				out.WriteString(".addr")
				i = opEnd
				continue
			}
		}

		// Unary `*expr` -> `val(expr)`, `**expr` -> `val(2, expr)`
		if c == '*' && isGoUnaryPrefixPos(s, i) {
			stars := 0
			for i+stars < n && s[i+stars] == '*' {
				stars++
			}
			if i+stars < n && s[i+stars] != '=' && s[i+stars] != ' ' && s[i+stars] != ')' {
				opEnd := scanGoUnaryOperand(s, i+stars)
				if opEnd > i+stars {
					inner := s[i+stars : opEnd]
					if strings.HasPrefix(inner, "(") && findMatchingDelim(inner, 0, '(', ')') == len(inner)-1 {
						inner = inner[1 : len(inner)-1]
					}
					if stars > 1 {
						out.WriteString("val(" + strconv.Itoa(stars) + ", ")
					} else {
						out.WriteString("val(")
					}
					out.WriteString(translateExprGoToGora(inner))
					out.WriteByte(')')
					i = opEnd
					continue
				}
			}
		}

		// `fmt.Sprintf(...)` -> `f"..."`
		if i+12 <= n && s[i:i+12] == "fmt.Sprintf(" && (i == 0 || !isIdentCont(s[i-1])) {
			cp := findMatchingDelim(s, i+11, '(', ')')
			if cp > i+11 {
				if fstr, ok := tryConvertFmtSprintf(s[i+12 : cp]); ok {
					out.WriteString(fstr)
					i = cp + 1
					continue
				}
			}
		}

		// `make(T, ...)` / `new(T)`
		if i+5 <= n && s[i:i+5] == "make(" && (i == 0 || !isIdentCont(s[i-1])) {
			cp := findMatchingDelim(s, i+4, '(', ')')
			if cp > i+4 {
				args := splitTopLevel(s[i+5:cp], ',')
				if len(args) >= 1 {
					args[0] = translateTypeGoToGora(args[0])
					for k := 1; k < len(args); k++ {
						args[k] = translateExprGoToGora(args[k])
					}
					out.WriteString("make(" + strings.Join(args, ", ") + ")")
					i = cp + 1
					continue
				}
			}
		}
		if i+4 <= n && s[i:i+4] == "new(" && (i == 0 || !isIdentCont(s[i-1])) {
			cp := findMatchingDelim(s, i+3, '(', ')')
			if cp > i+3 {
				out.WriteString("new(" + translateTypeGoToGora(s[i+4:cp]) + ")")
				i = cp + 1
				continue
			}
		}

		// Slice / array / map composite literal: `[]T{...}` -> `list[T]{...}`, `[N]T{...}` -> `[N]T{...}`, `map[K]V{...}` -> `dict[K, V]{...}`
		if c == '[' && (i == 0 || (!isIdentCont(s[i-1]) && s[i-1] != ']')) {
			cb := findMatchingDelim(s, i, '[', ']')
			if cb > i && cb+1 < n && (isIdentStart(s[cb+1]) || s[cb+1] == '*' || s[cb+1] == '[') {
				tEnd := scanGoTypeTokenEnd(s, cb+1)
				if tEnd > cb+1 && tEnd < n && s[tEnd] == '{' {
					out.WriteString(translateTypeGoToGora(s[i:tEnd]))
					i = tEnd
					continue
				}
			}
		}
		if i+4 <= n && s[i:i+4] == "map[" && (i == 0 || !isIdentCont(s[i-1])) {
			cb := findMatchingDelim(s, i+3, '[', ']')
			if cb > i+3 && cb+1 < n {
				tEnd := scanGoTypeTokenEnd(s, cb+1)
				if tEnd < n && s[tEnd] == '{' {
					out.WriteString(translateTypeGoToGora(s[i:tEnd]))
					i = tEnd
					continue
				}
			}
		}

		// Type conversion calls `int32(x)` -> `i32(x)`, `string(x)` -> `str(x)`
		if isIdentStart(c) {
			start := i
			for i < n && isIdentCont(s[i]) {
				i++
			}
			word := s[start:i]
			isMember := start > 0 && s[start-1] == '.'
			if !isMember && i < n && s[i] == '(' {
				if goraT, ok := goToGoraTypeMap[word]; ok {
					out.WriteString(goraT)
					continue
				}
			}
			out.WriteString(word)
			continue
		}

		out.WriteByte(c)
		i++
	}
	return out.String()
}

func scanGoTypeTokenEnd(s string, start int) int {
	i := start
	n := len(s)
	for i < n {
		if s[i] == '*' || s[i] == '.' {
			i++
		} else if s[i] == '[' {
			cb := findMatchingDelim(s, i, '[', ']')
			if cb < 0 {
				return n
			}
			i = cb + 1
		} else if isIdentCont(s[i]) {
			i++
		} else {
			break
		}
	}
	return i
}

func GoToGoraIndent(src, indentUnit string) (string, error) {
	if !validIndentUnit(indentUnit) {
		return "", &GoraError{Message: "invalid indent unit", Line: 1, Col: 1}
	}

	// Normalize Go source with gofmt when valid Go so braces & indentation are canonical.
	formatted := src
	if b, err := format.Source([]byte(src)); err == nil {
		formatted = string(b)
	}

	rawLines := strings.Split(strings.ReplaceAll(strings.ReplaceAll(formatted, "\r\n", "\n"), "\r", "\n"), "\n")
	// Remove trailing empty element added by trailing `\n`
	if len(rawLines) > 0 && rawLines[len(rawLines)-1] == "" {
		rawLines = rawLines[:len(rawLines)-1]
	}

	var outLines []string
	var stack []goToGoraFrame
	skipNextBlank := false

	effDepth := func() int {
		d := len(stack)
		for _, f := range stack {
			if f.inCaseBody {
				d++
			}
		}
		return d
	}
	topKind := func() string {
		if len(stack) == 0 {
			return ""
		}
		return stack[len(stack)-1].kind
	}
	inParenBlock := func() bool {
		k := topKind()
		return k == "import" || k == "const" || k == "var" || k == "type"
	}
	indentStr := func(d int) string {
		return strings.Repeat(indentUnit, d)
	}

	nextCodeAt := func(startIdx int) string {
		for j := startIdx; j < len(rawLines); j++ {
			t := strings.TrimSpace(rawLines[j])
			if t == "" || strings.HasPrefix(t, "//") {
				continue
			}
			c, _, _ := splitGoInlineComment(t)
			return c
		}
		return ""
	}

	for idx := 0; idx < len(rawLines); idx++ {
		line := strings.TrimSpace(rawLines[idx])
		if line == "" {
			if skipNextBlank {
				skipNextBlank = false
				continue
			}
			outLines = append(outLines, "")
			continue
		}
		skipNextBlank = false

		// Comment-only line
		if strings.HasPrefix(line, "//") {
			outLines = append(outLines, indentStr(effDepth())+"#"+line[2:])
			continue
		}

		// Split trailing `//` comment outside strings
		code, inlineComment, hasInline := splitGoInlineComment(line)
		commentSuffix := ""
		if hasInline {
			commentSuffix = " #" + inlineComment
		}

		// Package declaration: omit `package main` (implicit in Gora); preserve other packages
		if len(stack) == 0 {
			if strings.HasPrefix(code, "func _goraIn[") ||
				strings.HasPrefix(code, "func _goraIf[") ||
				strings.HasPrefix(code, "type _goraNum ") ||
				strings.HasPrefix(code, "func _goraFloorDiv[") ||
				strings.HasPrefix(code, "func _goraPow[") {
				bDepth := strings.Count(code, "{") - strings.Count(code, "}")
				for bDepth > 0 && idx+1 < len(rawLines) {
					idx++
					l := strings.TrimSpace(rawLines[idx])
					bDepth += strings.Count(l, "{") - strings.Count(l, "}")
				}
				skipNextBlank = true
				continue
			}
			if pkg, ok := startsWithKW(code, "package"); ok {
				if strings.TrimSpace(pkg) == "main" {
					skipNextBlank = true
					continue
				}
				outLines = append(outLines, "package "+strings.TrimSpace(pkg)+commentSuffix)
				continue
			}
		}

		// Closing `)` of `import (`, `const (`, `var (`, `type (`
		if inParenBlock() && code == ")" {
			stack = stack[:len(stack)-1]
			continue
		}

		// Inside `import:` block
		if topKind() == "import" {
			outLines = append(outLines, indentStr(effDepth())+convertGoImportItem(code)+commentSuffix)
			continue
		}

		// Inside `const:`, `var:`, `type:` block
		if inParenBlock() {
			outLines = append(outLines, indentStr(effDepth())+convertGoVarConstLine(code, false, true)+commentSuffix)
			continue
		}

		// Single-line `import ...` or top-level `const (` / `var (` / `type (`
		if len(stack) == 0 {
			if rest, ok := startsWithKW(code, "import"); ok {
				r := strings.TrimSpace(rest)
				if r == "(" {
					outLines = append(outLines, "import:"+commentSuffix)
					stack = append(stack, goToGoraFrame{kind: "import"})
					continue
				}
				outLines = append(outLines, "import "+convertGoImportItem(r)+commentSuffix)
				continue
			}
			if code == "const (" || code == "var (" || code == "type (" {
				kw := code[:len(code)-2]
				outLines = append(outLines, kw+":"+commentSuffix)
				stack = append(stack, goToGoraFrame{kind: kw})
				continue
			}
		}

		// Leading `}` or `}()`
		if strings.HasPrefix(code, "}") {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			rest := strings.TrimSpace(code[1:])
			if rest == "" || rest == ";" || rest == "()" {
				continue
			}
			code = rest
		}

		// Join multi-line Go expressions/headers (unclosed parens/brackets/composite literals or trailing operators)
		if topKind() != "struct" && topKind() != "interface" {
			for isGoLineIncomplete(code) && idx+1 < len(rawLines) {
				idx++
				nextTrim := strings.TrimSpace(rawLines[idx])
				if nextTrim == "" || strings.HasPrefix(nextTrim, "//") {
					continue
				}
				nextCode, nextComment, nextHasInline := splitGoInlineComment(nextTrim)
				if nextHasInline && commentSuffix == "" {
					commentSuffix = " #" + nextComment
				}
				if strings.HasSuffix(code, "(") || strings.HasSuffix(code, "[") {
					code = code + nextCode
				} else {
					code = code + " " + nextCode
				}
			}
		}

		// Try folding `try` error propagation:
		// Pattern A: `x, err := fn()` + `if err != nil { [var _z ...] return ..., err }`
		if consumed, goraStmt, ok := tryFoldGoTryAssign(rawLines, idx); ok {
			outLines = append(outLines, indentStr(effDepth())+goraStmt+commentSuffix)
			idx += consumed - 1
			continue
		}
		// Pattern B: `if err := fn(); err != nil { [var _z ...] return ..., err }`
		if consumed, goraStmt, ok := tryFoldGoTryStmt(rawLines, idx); ok {
			outLines = append(outLines, indentStr(effDepth())+goraStmt+commentSuffix)
			idx += consumed - 1
			continue
		}

		// Try folding comprehension: `var xs []T` / `s := make(map[T]bool)` / `m := make(map[K]V)` + `for ...`
		if consumed, goraComp, ok := tryFoldGoComprehension(rawLines, idx); ok {
			outLines = append(outLines, indentStr(effDepth())+goraComp+commentSuffix)
			idx += consumed - 1
			continue
		}

		// Try folding `var x T` + `if cond { x = a } else { x = b }` -> `x: T = a if cond else b`
		if consumed, goraTern, ok := tryFoldGoTypedTernary(rawLines, idx); ok {
			outLines = append(outLines, indentStr(effDepth())+goraTern+commentSuffix)
			idx += consumed - 1
			continue
		}
		// Try folding `if cond { return a } else { return b }` or `if cond { x = a } else { x = b }`
		if !strings.HasPrefix(strings.TrimSpace(rawLines[idx]), "} else") {
			if consumed, goraTern, ok := tryFoldGoIfTernary(rawLines, idx); ok {
				outLines = append(outLines, indentStr(effDepth())+goraTern+commentSuffix)
				idx += consumed - 1
				continue
			}
		}

		// `case ...:` or `default:`
		if _, ok := startsWithKW(code, "case"); ok || code == "default:" {
			if len(stack) > 0 && stack[len(stack)-1].kind == "switch" {
				stack[len(stack)-1].inCaseBody = false
			}
			outLines = append(outLines, indentStr(effDepth())+translateExprGoToGora(code)+commentSuffix)
			if len(stack) > 0 && stack[len(stack)-1].kind == "switch" {
				stack[len(stack)-1].inCaseBody = true
			}
			nextTrim := nextCodeAt(idx + 1)
			if nextTrim == "}" || nextTrim == "default:" {
				outLines = append(outLines, indentStr(effDepth())+"pass")
			} else if _, nextIsCase := startsWithKW(nextTrim, "case"); nextIsCase {
				outLines = append(outLines, indentStr(effDepth())+"pass")
			}
			continue
		}

		// Empty `{}` on a block header (e.g. `func foo() {}`)
		if strings.HasSuffix(code, "{}") {
			header := strings.TrimSpace(code[:len(code)-2])
			if isGoBlockHeader(header) {
				outLines = append(outLines, indentStr(effDepth())+convertGoBlockHeader(header)+commentSuffix)
				outLines = append(outLines, indentStr(effDepth()+1)+"pass")
				continue
			}
		}

		// Block header ending in `{`
		if strings.HasSuffix(code, "{") {
			header := strings.TrimSpace(code[:len(code)-1])
			if isGoBlockHeader(header) {
				nextTrim := nextCodeAt(idx + 1)
				goraHeader := convertGoBlockHeader(header)
				outLines = append(outLines, indentStr(effDepth())+goraHeader+commentSuffix)
				frameKind := "normal"
				if strings.HasPrefix(goraHeader, "struct ") {
					frameKind = "struct"
				} else if strings.HasPrefix(goraHeader, "interface ") {
					frameKind = "interface"
				} else if strings.HasPrefix(goraHeader, "match") || goraHeader == "select:" {
					frameKind = "switch"
				}
				stack = append(stack, goToGoraFrame{kind: frameKind})
				if nextTrim == "}" {
					outLines = append(outLines, indentStr(effDepth())+"pass")
				}
				continue
			}
		}

		// Inside `struct`
		if topKind() == "struct" {
			outLines = append(outLines, indentStr(effDepth())+convertGoStructField(code)+commentSuffix)
			continue
		}

		// Inside `interface`
		if topKind() == "interface" {
			outLines = append(outLines, indentStr(effDepth())+convertGoIfaceMethod(code)+commentSuffix)
			continue
		}

		// Single-line `type Name = Target` or `type Name Target`
		if len(stack) == 0 {
			if rest, ok := startsWithKW(code, "type"); ok {
				if eq := findTopLevelSep(rest, '='); eq > 0 {
					alias := strings.TrimSpace(rest[:eq])
					target := translateTypeGoToGora(rest[eq+1:])
					outLines = append(outLines, "type "+alias+" = "+target+commentSuffix)
					continue
				}
				if sp := findFirstTopLevelSpace(rest); sp > 0 {
					name := strings.TrimSpace(rest[:sp])
					target := translateTypeGoToGora(rest[sp+1:])
					outLines = append(outLines, "type "+name+": "+target+commentSuffix)
					continue
				}
			}
		}

		// `var x T [= val]` or `const X [T] = val`
		if after, ok := startsWithKW(code, "var"); ok {
			outLines = append(outLines, indentStr(effDepth())+convertGoVarConstLine(after, false, false)+commentSuffix)
			continue
		}
		if after, ok := startsWithKW(code, "const"); ok {
			outLines = append(outLines, indentStr(effDepth())+convertGoVarConstLine(after, true, false)+commentSuffix)
			continue
		}

		// Standard statement / `:=`
		outLines = append(outLines, indentStr(effDepth())+translateExprGoToGora(code)+commentSuffix)
	}

	result := strings.Join(outLines, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result, nil
}

func splitGoInlineComment(line string) (code, comment string, has bool) {
	var quote byte
	for i := 0; i+1 < len(line); i++ {
		c := line[i]
		if quote != 0 {
			if quote != '`' && c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '/' && line[i+1] == '/' {
			return strings.TrimRight(line[:i], " "), line[i+2:], true
		}
	}
	return line, "", false
}

func convertGoImportItem(item string) string {
	item = strings.TrimSpace(item)
	if item == "" {
		return ""
	}
	// `alias "pkg"` or `. "pkg"` or `"pkg"`
	if strings.HasPrefix(item, `"`) && strings.HasSuffix(item, `"`) {
		inner := item[1 : len(item)-1]
		return inner
	}
	sp := strings.IndexByte(item, ' ')
	if sp > 0 {
		alias := strings.TrimSpace(item[:sp])
		pkg := strings.TrimSpace(item[sp+1:])
		if len(pkg) >= 2 && pkg[0] == '"' && pkg[len(pkg)-1] == '"' {
			pkg = pkg[1 : len(pkg)-1]
		}
		if alias == "." {
			return pkg + " as *"
		}
		return pkg + " as " + alias
	}
	return item
}

type goToGoraFrame struct {
	kind       string // "normal", "switch", "struct", "interface", "import", "const", "var", "type"
	inCaseBody bool
}

func isGoBlockHeader(header string) bool {
	h := strings.TrimSpace(header)
	if h == "else" || h == "for" || h == "switch" || h == "select" || h == "go func()" || h == "defer func()" {
		return true
	}
	for _, kw := range []string{"func", "if", "else if", "for", "switch", "go func", "defer func"} {
		if _, ok := startsWithKW(h, kw); ok {
			return true
		}
	}
	if after, ok := startsWithKW(h, "type"); ok {
		if strings.HasSuffix(after, " struct") || strings.HasSuffix(after, " interface") {
			return true
		}
	}
	if ce := findTopLevelStr(h, ":="); ce > 0 {
		rhs := strings.TrimSpace(h[ce+2:])
		if _, ok := startsWithKW(rhs, "func"); ok {
			return true
		}
	}
	if eq := findTopLevelAssignEq(h); eq > 0 {
		rhs := strings.TrimSpace(h[eq+1:])
		if _, ok := startsWithKW(rhs, "func"); ok {
			return true
		}
	}
	return false
}

func isGoLineIncomplete(code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}
	var quote byte
	parenD, bracketD, braceD := 0, 0, 0
	for i := 0; i < len(code); i++ {
		c := code[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(code) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		switch c {
		case '(':
			parenD++
		case ')':
			if parenD > 0 {
				parenD--
			}
		case '[':
			bracketD++
		case ']':
			if bracketD > 0 {
				bracketD--
			}
		case '{':
			braceD++
		case '}':
			if braceD > 0 {
				braceD--
			}
		}
	}
	if parenD == 0 && bracketD == 0 && braceD == 1 && strings.HasSuffix(code, "{") {
		if isGoBlockHeader(strings.TrimSpace(code[:len(code)-1])) {
			return false
		}
	}
	if parenD > 0 || bracketD > 0 || braceD > 0 {
		return true
	}
	if strings.HasSuffix(code, "++") || strings.HasSuffix(code, "--") {
		return false
	}
	for _, suf := range []string{",", "||", "&&", "+", "-", "*", "/", "==", "!=", "<=", ">=", ":=", "="} {
		if strings.HasSuffix(code, suf) {
			return true
		}
	}
	return false
}

// splitGoNamesAndType splits `<ident>[, <ident>...] <goType>` into `(names, goType, true)`.
func splitGoNamesAndType(s string) (names, goType string, ok bool) {
	s = strings.TrimSpace(s)
	i := 0
	n := len(s)
	for {
		for i < n && s[i] == ' ' {
			i++
		}
		start := i
		if i >= n || !isIdentStart(s[i]) {
			return "", "", false
		}
		for i < n && isIdentCont(s[i]) {
			i++
		}
		if i == start {
			return "", "", false
		}
		j := i
		for j < n && s[j] == ' ' {
			j++
		}
		if j < n && s[j] == ',' {
			i = j + 1
			continue
		}
		if j > i && j < n {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[j:]), true
		}
		return "", "", false
	}
}

func convertGoVarConstLine(s string, isConst bool, inBlock bool) string {
	s = strings.TrimSpace(s)
	prefix := ""
	if isConst {
		prefix = "const "
	}
	eq := findTopLevelAssignEq(s)
	var lhs, rhs string
	hasEq := eq >= 0
	if hasEq {
		lhs = strings.TrimSpace(s[:eq])
		rhs = strings.TrimSpace(s[eq+1:])
	} else {
		lhs = s
	}
	if names, rawType, ok := splitGoNamesAndType(lhs); ok {
		typ := translateTypeGoToGora(rawType)
		if hasEq {
			return prefix + names + ": " + typ + " = " + translateExprGoToGora(rhs)
		}
		return prefix + names + ": " + typ
	}
	if !isConst && !inBlock {
		prefix = "var "
	}
	if hasEq {
		return prefix + lhs + " = " + translateExprGoToGora(rhs)
	}
	return prefix + lhs
}

func convertGoStructField(s string) string {
	s = strings.TrimSpace(s)
	// Preserve trailing backtick tag
	tag := ""
	if len(s) >= 2 && s[len(s)-1] == '`' {
		start := strings.LastIndexByte(s[:len(s)-1], '`')
		if start > 0 && s[start-1] == ' ' {
			tag = " " + s[start:]
			s = strings.TrimSpace(s[:start])
		}
	}
	if names, rawType, ok := splitGoNamesAndType(s); ok {
		typ := translateTypeGoToGora(rawType)
		return names + ": " + typ + tag
	}
	return translateTypeGoToGora(s) + tag
}

func convertGoIfaceMethod(s string) string {
	s = strings.TrimSpace(s)
	lp := strings.IndexByte(s, '(')
	if lp <= 0 {
		return translateTypeGoToGora(s)
	}
	rp := findMatchingDelim(s, lp, '(', ')')
	if rp < 0 {
		return s
	}
	name := strings.TrimSpace(s[:lp])
	params := translateGoParamListToGora(s[lp+1 : rp])
	rest := strings.TrimSpace(s[rp+1:])
	if rest != "" {
		return name + "(" + params + ") -> " + translateGoReturnListToGora(rest)
	}
	return name + "(" + params + ")"
}

func convertGoIfCondToGora(cond string) string {
	cond = strings.TrimSpace(cond)
	// Match `_, ok := m[k]; ok` or `_, ok := m[k]; !ok`
	if semi := findTopLevelSep(cond, ';'); semi > 0 {
		initS := strings.TrimSpace(cond[:semi])
		testS := strings.TrimSpace(cond[semi+1:])
		if strings.HasPrefix(initS, "_, ok :=") || strings.HasPrefix(initS, "_,ok :=") {
			rhs := strings.TrimSpace(initS[strings.Index(initS, ":=")+2:])
			if strings.HasSuffix(rhs, "]") {
				ob := findLastTopLevelOpenBracket(rhs)
				if ob > 0 {
					mExpr := translateExprGoToGora(rhs[:ob])
					kExpr := translateExprGoToGora(rhs[ob+1 : len(rhs)-1])
					if testS == "ok" {
						return kExpr + " in " + mExpr
					}
					if testS == "!ok" {
						return kExpr + " not in " + mExpr
					}
				}
			}
		}
	}
	return translateExprGoToGora(cond)
}

func findLastTopLevelOpenBracket(s string) int {
	depth := 0
	var quote byte
	last := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if quote != '`' && c == '\\' && i+1 < len(s) {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote = c
			continue
		}
		if c == '(' || c == '[' || c == '{' {
			if c == '[' && depth == 0 {
				last = i
			}
			depth++
		} else if c == ')' || c == ']' || c == '}' {
			if depth > 0 {
				depth--
			}
		}
	}
	return last
}

func convertGoForHeaderToGora(expr string) (string, bool) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "while True:", true
	}
	// Check for `range`:
	//   `for range n` -> `for _ in range(n):`
	//   `for _, v := range xs` -> `for v in xs:`
	//   `for i, v := range xs` -> `for i, v in enumerate(xs):`
	//   `for k := range m` -> `for k, _ in m:`
	if afterRange, ok := startsWithKW(expr, "range"); ok {
		return "for _ in range(" + translateExprGoToGora(afterRange) + "):", true
	}
	if ce := findTopLevelStr(expr, ":="); ce > 0 {
		lhs := strings.TrimSpace(expr[:ce])
		rhs := strings.TrimSpace(expr[ce+2:])
		if iterS, ok := startsWithKW(rhs, "range"); ok {
			iterGora := translateExprGoToGora(iterS)
			if comma := findTopLevelSep(lhs, ','); comma >= 0 {
				v1 := strings.TrimSpace(lhs[:comma])
				v2 := strings.TrimSpace(lhs[comma+1:])
				if v1 == "_" {
					return "for " + v2 + " in " + iterGora + ":", true
				}
				return "for " + v1 + ", " + v2 + " in enumerate(" + iterGora + "):", true
			}
			if lhs == "_" {
				return "for _ in range(" + iterGora + "):", true
			}
			return "for " + lhs + ", _ in " + iterGora + ":", true
		}
	}

	// 3-clause Go for: `i := start; i < stop; i++`
	if findTopLevelSep(expr, ';') >= 0 {
		parts := splitTopLevel(expr, ';')
		if len(parts) == 3 {
			if rangeHeader, ok := tryConvertGo3ClauseToRange(parts[0], parts[1], parts[2]); ok {
				return "for " + rangeHeader + ":", true
			}
			return "for " + translateExprGoToGora(parts[0]) + "; " + translateExprGoToGora(parts[1]) + "; " + translateExprGoToGora(parts[2]) + ":", true
		}
		return "for " + translateExprGoToGora(expr) + ":", true
	}

	// Single-condition `for cond {` -> `while cond:`
	return "while " + translateExprGoToGora(expr) + ":", true
}

func tryConvertGo3ClauseToRange(initS, condS, stepS string) (string, bool) {
	initS = strings.TrimSpace(initS)
	condS = strings.TrimSpace(condS)
	stepS = strings.TrimSpace(stepS)

	ce := strings.Index(initS, ":=")
	if ce <= 0 {
		return "", false
	}
	vname := strings.TrimSpace(initS[:ce])
	startS := strings.TrimSpace(initS[ce+2:])
	if vname == "" || startS == "" {
		return "", false
	}

	// Unwrap typed start like `int64(0)`
	var varType string
	if lp := strings.IndexByte(startS, '('); lp > 0 && strings.HasSuffix(startS, ")") {
		tCandidate := startS[:lp]
		if goraT, ok := goToGoraTypeMap[tCandidate]; ok {
			varType = goraT
			startS = strings.TrimSpace(startS[lp+1 : len(startS)-1])
		}
	}

	// Parse condition `vname < stop` or `vname > stop`
	if !strings.HasPrefix(condS, vname) {
		return "", false
	}
	rest := strings.TrimSpace(condS[len(vname):])
	if len(rest) < 2 || (rest[0] != '<' && rest[0] != '>') || rest[1] == '=' {
		return "", false
	}
	cmp := rest[0]
	stopS := strings.TrimSpace(rest[1:])
	if stopS == "" || findTopLevelStr(stopS, "&&") >= 0 || findTopLevelStr(stopS, "||") >= 0 {
		return "", false
	}

	// Parse step
	var customStep string
	neg := false
	switch {
	case stepS == vname+"++":
		if cmp != '<' {
			return "", false
		}
	case stepS == vname+"--":
		if cmp != '>' {
			return "", false
		}
		customStep = "-1"
		neg = true
	case strings.HasPrefix(stepS, vname) && strings.Contains(stepS[len(vname):], "+="):
		if cmp != '<' {
			return "", false
		}
		idx := strings.Index(stepS, "+=")
		customStep = strings.TrimSpace(stepS[idx+2:])
	case strings.HasPrefix(stepS, vname) && strings.Contains(stepS[len(vname):], "-="):
		if cmp != '>' {
			return "", false
		}
		idx := strings.Index(stepS, "-=")
		customStep = strings.TrimSpace(stepS[idx+2:])
		neg = true
	default:
		return "", false
	}

	var lhs strings.Builder
	if vname == "_i" {
		lhs.WriteString("_")
	} else {
		lhs.WriteString(vname)
	}
	if varType != "" {
		lhs.WriteString(": ")
		lhs.WriteString(varType)
	}
	lhs.WriteString(" in range(")
	if startS == "0" && customStep == "" {
		lhs.WriteString(translateExprGoToGora(stopS))
	} else {
		lhs.WriteString(translateExprGoToGora(startS))
		lhs.WriteString(", ")
		lhs.WriteString(translateExprGoToGora(stopS))
		if customStep != "" {
			lhs.WriteString(", ")
			if neg && !strings.HasPrefix(customStep, "-") {
				lhs.WriteByte('-')
			}
			lhs.WriteString(translateExprGoToGora(customStep))
		}
	}
	lhs.WriteByte(')')
	return lhs.String(), true
}

func convertGoBlockHeader(header string) string {
	h := strings.TrimSpace(header)
	if after, ok := startsWithKW(h, "else if"); ok {
		return "elif " + convertGoIfCondToGora(after) + ":"
	}
	if h == "else" {
		return "else:"
	}
	if after, ok := startsWithKW(h, "if"); ok {
		return "if " + convertGoIfCondToGora(after) + ":"
	}
	if h == "for" {
		return "while True:"
	}
	if after, ok := startsWithKW(h, "for"); ok {
		if res, ok2 := convertGoForHeaderToGora(after); ok2 {
			return res
		}
	}
	if h == "switch" {
		return "match:"
	}
	if after, ok := startsWithKW(h, "switch"); ok {
		return "match " + translateExprGoToGora(after) + ":"
	}
	if h == "select" {
		return "select:"
	}
	if h == "go func()" {
		return "go def():"
	}
	if h == "defer func()" {
		return "defer def():"
	}
	if after, ok := startsWithKW(h, "type"); ok {
		if strings.HasSuffix(after, " struct") {
			return "struct " + strings.TrimSpace(after[:len(after)-7]) + ":"
		}
		if strings.HasSuffix(after, " interface") {
			return "interface " + strings.TrimSpace(after[:len(after)-10]) + ":"
		}
	}
	if after, ok := startsWithKW(h, "func"); ok {
		return convertGoFuncHeaderToGora(after) + ":"
	}
	return translateExprGoToGora(h) + ":"
}

func convertGoFuncHeaderToGora(rest string) string {
	s := strings.TrimSpace(rest)
	var recv string
	if strings.HasPrefix(s, "(") {
		rp := findMatchingDelim(s, 0, '(', ')')
		if rp > 0 {
			recv = "(" + translateGoParamListToGora(s[1:rp]) + ") "
			s = strings.TrimSpace(s[rp+1:])
		}
	}
	lp := strings.IndexByte(s, '(')
	if lp <= 0 {
		return "def " + recv + s
	}
	rp := findMatchingDelim(s, lp, '(', ')')
	if rp < 0 {
		return "def " + recv + s
	}
	nameAndGen := strings.TrimSpace(s[:lp])
	params := translateGoParamListToGora(s[lp+1 : rp])
	ret := strings.TrimSpace(s[rp+1:])
	out := "def " + recv + nameAndGen + "(" + params + ")"
	if ret != "" {
		out += " -> " + translateGoReturnListToGora(ret)
	}
	return out
}

func tryFoldGoTryAssign(lines []string, idx int) (consumed int, goraStmt string, ok bool) {
	line := strings.TrimSpace(lines[idx])
	ce := findTopLevelStr(line, ":=")
	if ce <= 0 {
		return 0, "", false
	}
	lhs := strings.TrimSpace(line[:ce])
	rhs := strings.TrimSpace(line[ce+2:])
	parts := splitTopLevel(lhs, ',')
	if len(parts) < 2 || parts[len(parts)-1] != "err" {
		return 0, "", false
	}
	if idx+2 >= len(lines) {
		return 0, "", false
	}
	ifLine := strings.TrimSpace(lines[idx+1])
	if ifLine != "if err != nil {" {
		return 0, "", false
	}
	// Skip optional `var _z...` lines before `return ..., err`
	k := idx + 2
	for k < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[k]), "var _z") {
		k++
	}
	if k+1 >= len(lines) {
		return 0, "", false
	}
	retLine := strings.TrimSpace(lines[k])
	closeLine := strings.TrimSpace(lines[k+1])
	if !strings.HasPrefix(retLine, "return ") || closeLine != "}" {
		return 0, "", false
	}
	retParts := splitTopLevel(strings.TrimSpace(retLine[7:]), ',')
	if len(retParts) == 0 || retParts[len(retParts)-1] != "err" {
		return 0, "", false
	}
	for _, rp := range retParts[:len(retParts)-1] {
		if !isGoZeroReturnExpr(rp) {
			return 0, "", false
		}
	}
	targetLHS := strings.Join(parts[:len(parts)-1], ", ")
	return (k + 2) - idx, targetLHS + " := try " + translateExprGoToGora(rhs), true
}

func isGoZeroReturnExpr(expr string) bool {
	e := strings.TrimSpace(expr)
	if e == "0" || e == `""` || e == "false" || e == "nil" || strings.HasPrefix(e, "_z") {
		return true
	}
	if strings.HasSuffix(e, "{}") {
		prefix := strings.TrimSpace(e[:len(e)-2])
		return prefix != "" && scanGoTypeTokenEnd(prefix, 0) == len(prefix)
	}
	return false
}

func tryFoldGoTryStmt(lines []string, idx int) (consumed int, goraStmt string, ok bool) {
	line := strings.TrimSpace(lines[idx])
	if !strings.HasPrefix(line, "if err := ") || !strings.HasSuffix(line, "; err != nil {") {
		return 0, "", false
	}
	call := strings.TrimSpace(line[len("if err := ") : len(line)-len("; err != nil {")])
	if call == "" || idx+2 >= len(lines) {
		return 0, "", false
	}
	k := idx + 1
	for k < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[k]), "var _z") {
		k++
	}
	if k+1 >= len(lines) {
		return 0, "", false
	}
	retLine := strings.TrimSpace(lines[k])
	closeLine := strings.TrimSpace(lines[k+1])
	if !strings.HasPrefix(retLine, "return ") || closeLine != "}" {
		return 0, "", false
	}
	retParts := splitTopLevel(strings.TrimSpace(retLine[7:]), ',')
	if len(retParts) == 0 || retParts[len(retParts)-1] != "err" {
		return 0, "", false
	}
	for _, rp := range retParts[:len(retParts)-1] {
		if !isGoZeroReturnExpr(rp) {
			return 0, "", false
		}
	}
	return (k + 2) - idx, "try " + translateExprGoToGora(call), true
}

func tryFoldGoTypedTernary(lines []string, idx int) (consumed int, goraStmt string, ok bool) {
	if idx+5 >= len(lines) {
		return 0, "", false
	}
	decl := strings.TrimSpace(lines[idx])
	afterVar, okV := startsWithKW(decl, "var")
	if !okV || strings.Contains(afterVar, "=") {
		return 0, "", false
	}
	sp := findFirstTopLevelSpace(afterVar)
	if sp <= 0 {
		return 0, "", false
	}
	varName := strings.TrimSpace(afterVar[:sp])
	goType := strings.TrimSpace(afterVar[sp+1:])

	c2, tern, okT := tryFoldGoIfTernary(lines, idx+1)
	if !okT {
		return 0, "", false
	}
	eq := findTopLevelAssignEq(tern)
	if eq <= 0 || strings.TrimSpace(tern[:eq]) != varName {
		return 0, "", false
	}
	rhs := strings.TrimSpace(tern[eq+1:])
	return 1 + c2, varName + ": " + translateTypeGoToGora(goType) + " = " + rhs, true
}

func tryFoldGoIfTernary(lines []string, idx int) (consumed int, goraStmt string, ok bool) {
	if idx+4 >= len(lines) {
		return 0, "", false
	}
	l0 := strings.TrimSpace(lines[idx])
	l1 := strings.TrimSpace(lines[idx+1])
	l2 := strings.TrimSpace(lines[idx+2])
	l3 := strings.TrimSpace(lines[idx+3])
	l4 := strings.TrimSpace(lines[idx+4])

	if !strings.HasPrefix(l0, "if ") || !strings.HasSuffix(l0, " {") {
		return 0, "", false
	}
	if l2 != "} else {" || l4 != "}" {
		return 0, "", false
	}
	cond := strings.TrimSpace(l0[3 : len(l0)-1])
	if cond == "" {
		return 0, "", false
	}
	condGora := convertGoIfCondToGora(cond)
	if findTopLevelSep(condGora, ';') >= 0 {
		return 0, "", false
	}

	// `return a` / `return b`
	if strings.HasPrefix(l1, "return ") && strings.HasPrefix(l3, "return ") {
		a := translateExprGoToGora(strings.TrimSpace(l1[7:]))
		b := translateExprGoToGora(strings.TrimSpace(l3[7:]))
		return 5, "return " + a + " if " + condGora + " else " + b, true
	}

	// `x = a` / `x = b`
	eq1 := findTopLevelAssignEq(l1)
	eq3 := findTopLevelAssignEq(l3)
	if eq1 > 0 && eq3 > 0 {
		lhs1 := strings.TrimSpace(l1[:eq1])
		lhs3 := strings.TrimSpace(l3[:eq3])
		if lhs1 == lhs3 {
			lhsGora := translateExprGoToGora(lhs1)
			a := translateExprGoToGora(strings.TrimSpace(l1[eq1+1:]))
			b := translateExprGoToGora(strings.TrimSpace(l3[eq3+1:]))
			return 5, lhsGora + " = " + a + " if " + condGora + " else " + b, true
		}
	}
	return 0, "", false
}

func tryFoldGoComprehension(lines []string, idx int) (consumed int, goraStmt string, ok bool) {
	if idx+3 >= len(lines) {
		return 0, "", false
	}
	decl := strings.TrimSpace(lines[idx])
	var varName, goType, kindHint string

	if afterVar, okV := startsWithKW(decl, "var"); okV && !strings.Contains(afterVar, "=") {
		sp := findFirstTopLevelSpace(afterVar)
		if sp <= 0 {
			return 0, "", false
		}
		varName = strings.TrimSpace(afterVar[:sp])
		goType = strings.TrimSpace(afterVar[sp+1:])
		if strings.HasPrefix(goType, "[]") {
			kindHint = "list"
		}
	} else if ce := findTopLevelStr(decl, ":="); ce > 0 {
		lhs := strings.TrimSpace(decl[:ce])
		rhs := strings.TrimSpace(decl[ce+2:])
		if strings.HasPrefix(rhs, "make(map[") && strings.HasSuffix(rhs, ")") {
			varName = lhs
			goType = strings.TrimSpace(rhs[5 : len(rhs)-1])
			if strings.HasSuffix(goType, "]bool") {
				kindHint = "set"
			} else {
				kindHint = "dict"
			}
		}
	}
	if kindHint == "" {
		return 0, "", false
	}

	forLine := strings.TrimSpace(lines[idx+1])
	if !strings.HasPrefix(forLine, "for ") || !strings.HasSuffix(forLine, " {") {
		return 0, "", false
	}
	forHeader, okF := convertGoForHeaderToGora(forLine[4 : len(forLine)-1])
	if !okF || !strings.HasPrefix(forHeader, "for ") || !strings.HasSuffix(forHeader, ":") {
		return 0, "", false
	}
	forClause := forHeader[4 : len(forHeader)-1]

	cur := idx + 2
	var condGora string
	if cur < len(lines) {
		l := strings.TrimSpace(lines[cur])
		if strings.HasPrefix(l, "if ") && strings.HasSuffix(l, " {") {
			condGora = convertGoIfCondToGora(l[3 : len(l)-1])
			cur++
		}
	}
	if cur >= len(lines) {
		return 0, "", false
	}
	stmt := strings.TrimSpace(lines[cur])
	cur++
	if condGora != "" {
		if cur >= len(lines) || strings.TrimSpace(lines[cur]) != "}" {
			return 0, "", false
		}
		cur++
	}
	if cur >= len(lines) || strings.TrimSpace(lines[cur]) != "}" {
		return 0, "", false
	}
	cur++

	goraType := translateTypeGoToGora(goType)
	filterSuffix := ""
	if condGora != "" {
		filterSuffix = " if " + condGora
	}

	switch kindHint {
	case "list":
		prefix := varName + " = append(" + varName + ", "
		if strings.HasPrefix(stmt, prefix) && strings.HasSuffix(stmt, ")") {
			elem := translateExprGoToGora(stmt[len(prefix) : len(stmt)-1])
			return cur - idx, fmt.Sprintf("%s: %s = [%s for %s%s]", varName, goraType, elem, forClause, filterSuffix), true
		}
	case "set":
		if strings.HasPrefix(stmt, varName+"[") {
			cb := findMatchingDelim(stmt, len(varName), '[', ']')
			if cb > len(varName) && strings.TrimSpace(stmt[cb+1:]) == "= true" {
				elem := translateExprGoToGora(stmt[len(varName)+1 : cb])
				return cur - idx, fmt.Sprintf("%s: %s = {%s for %s%s}", varName, goraType, elem, forClause, filterSuffix), true
			}
		}
	case "dict":
		if strings.HasPrefix(stmt, varName+"[") {
			cb := findMatchingDelim(stmt, len(varName), '[', ']')
			if cb > len(varName) {
				rest := strings.TrimSpace(stmt[cb+1:])
				if strings.HasPrefix(rest, "=") && !strings.HasPrefix(rest, "==") {
					kExpr := translateExprGoToGora(stmt[len(varName)+1 : cb])
					vExpr := translateExprGoToGora(strings.TrimSpace(rest[1:]))
					return cur - idx, fmt.Sprintf("%s: %s = {%s: %s for %s%s}", varName, goraType, kExpr, vExpr, forClause, filterSuffix), true
				}
			}
		}
	}
	return 0, "", false
}

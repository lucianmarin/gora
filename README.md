# Gora — Pythonic Go Bidirectional Transpiler

**Gora** is a human-readable, zero-overhead **1-to-1 syntactic representation of Go** inspired by Python's clean naming, indentation, type annotations, and ergonomics—built on the same design principles as [Pura](../pura).

Every Gora construct maps directly and deterministically to idiomatic Go—with **no curly braces (`{` `}`) for blocks**, **Pythonic `def` functions and postfix `: type` declarations**, **optional `package main`**, **concise types (`i8`..`i64`, `u8`..`u64`, `f32`, `f64`, `str`, `list[T]`, `dict[K, V]`, `set[T]`, `chan[T]`, `addr[T]`)**, **Pythonic `try` error propagation**, **`f"..."` strings**, **comprehensions**, **`in` / `not in` map checks**, **inline `lambda`**, **`match`/`select`**, and **full bidirectional `--from-go` conversion**.

- **Extension:** `.gora`
- **Target Language:** Go 1.22+
- **Encoding:** UTF-8 (without BOM)

---

## Side-by-Side Comparison

### Gora (`.gora`)

```python
import:
    errors
    strconv

def parsePositive(raw: str) -> i32, error:
    n := try strconv.Atoi(raw)
    if n < 0:
        return 0, errors.New("negative number")
    return i32(n), nil

def main():
    nums: list[int] = [i * i for i in range(1, 6) if i % 2 == 1]
    seen: set[int] = {x for x in nums}
    if 9 in seen and 4 not in seen:
        v, err := parsePositive("7")
        if err == nil:
            tag: str = "big" if v > 5 else "small"
            fmt.Println(f"v={v} ({tag}), nums={nums}")
```

### Equivalent Go (`.go`)

```go
package main

import (
    "errors"
    "fmt"
    "strconv"
)

func parsePositive(raw string) (int32, error) {
    n, err := strconv.Atoi(raw)
    if err != nil {
        return 0, err
    }
    if n < 0 {
        return 0, errors.New("negative number")
    }
    return int32(n), nil
}

func main() {
    var nums []int
    for i := 1; i < 6; i++ {
        if i%2 == 1 {
            nums = append(nums, i*i)
        }
    }
    seen := make(map[int]bool)
    for _, x := range nums {
        seen[x] = true
    }
    if _goraIn(seen, 9) && !_goraIn(seen, 4) {
        v, err := parsePositive("7")
        if err == nil {
            var tag string
            if v > 5 {
                tag = "big"
            } else {
                tag = "small"
            }
            fmt.Println(fmt.Sprintf("v=%v (%v), nums=%v", v, tag, nums))
        }
    }
}
```

---

## Gora Syntax Features

| Category | Gora (`.gora`) | Go (`.go`) |
| :--- | :--- | :--- |
| **Package** | _(omitted)_ or `package util` | `package main` or `package util` |
| **Imports** | `import fmt`, `import net/http as h`, `import:`, `from p import *` | `import "fmt"`, `import h "net/http"`, `import (...)`, `import . "p"` |
| **Types** | `i8`..`i64`, `u8`..`u64`, `f32`, `f64`, `str` | `int8`..`int64`, `uint8`..`uint64`, `float32`, `float64`, `string` |
| **Containers** | `list[T]`, `dict[K, V]`, `set[T]`, `chan[T]`, `in_chan[T]`, `out_chan[T]` | `[]T`, `map[K]V`, `map[T]bool`, `chan T`, `<-chan T`, `chan<- T` |
| **Pointers & Casts** | `addr[T]`, `x.addr`, `val(p)`, `cast(addr[T], p)` | `*T`, `&x`, `*p`, `(*T)(p)` |
| **Functions & Methods** | `def f(a: i32) -> i32, error:`, `def (v: addr[Vec2]) move():` | `func f(a int32) (int32, error) {`, `func (v *Vec2) move() {` |
| **Error Propagation** | `x := try fn()` / `try fn()` | `x, err := fn(); if err != nil { return <zero>, err }` |
| **Loops** | `for i in range(n):`, `for v in xs:`, `for i, v in enumerate(xs):`, `while cond:`, `loop:` | `for i := 0; i < n; i++ {`, `for _, v := range xs {`, `for i, v := range xs {`, `for cond {`, `for {` |
| **Map Membership** | `if k in m:` / `if k not in m:` | `if _, ok := m[k]; ok {` / `if _, ok := m[k]; !ok {` |
| **Comprehensions** | `[x * 2 for x in xs if x > 0]`, `{x for x in xs}`, `{k: v for k, v in m}` | Idiomatic Go `append` / `make(map[...])` loops |
| **Control & Concurrency** | `match x:`, `select:`, `ch.put(v)`, `ch.get()`, `go def():`, `defer def():` | `switch x {`, `select {`, `ch <- v`, `<-ch`, `go func() { ... }()`, `defer func() { ... }()` |
| **F-Strings & Lambdas** | `f"hi {name} {pi:.2f}"`, `lambda x: i32 -> i32: x + 1` | `fmt.Sprintf(...)` (auto-imports `fmt`), `func(x int32) int32 { return x + 1 }` |
| **Ternary Statements** | `x: str = "a" if ok else "b"`, `return a if ok else b` | `if ok { ... } else { ... }` |

---

## Build & CLI Usage

```sh
# Build the gora binary
make

# Install to Homebrew prefix (or /usr/local/bin)
make install

# Transpile a .gora file to Go on stdout
./gora examples/hello.gora

# Transpile and run immediately via go run
./gora --run examples/hello.gora

# Compile a .gora file to a native binary
./gora --compile examples/shapes.gora -o shapes && ./shapes

# Format/canonicalize Gora (default 4 spaces, or N spaces / tab)
./gora --gora examples/hello.gora
./gora --gora --indent tab examples/hello.gora

# Convert existing Go source code into Gora
./gora --from-go input.go

# Validate Gora syntax & Go AST
./gora --check examples/hello.gora

# Run tests & throughput benchmark
make test && make bench
```

### Examples

- [`examples/hello.gora`](examples/hello.gora) — Basics, `f"..."` strings, list comprehension, and `range()`
- [`examples/shapes.gora`](examples/shapes.gora) — `struct`, `interface`, pointer receiver methods, `match`/`case`
- [`examples/comprehensions.gora`](examples/comprehensions.gora) — List, set, and dict comprehensions, `in`/`not in`, `enumerate()`
- [`examples/errors_and_concurrency.gora`](examples/errors_and_concurrency.gora) — `try` propagation, multi-return without parens, `lambda`, ternary, `chan[T]`, `go def():`, `select`
- [`examples/tabs.gora`](examples/tabs.gora) — Tab-indented Fibonacci calculator (`for _ in range(2, n + 1):`)

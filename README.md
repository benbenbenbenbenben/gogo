# gogo

**gogo** is a superset of Go. It plugs directly into the Go compiler and language service and is fully backwards-compatible with `.go` syntax. Write `.gogo` files; gogo pre-processes them and feeds the result straight to `go`.

## Installation

```sh
go install github.com/benbenbenbenbenben/gogo/cmd/gogo@latest
```

## Usage

```
gogo run   [goflags] <file.gogo> [-- program args]
gogo build [goflags] [packages]
gogo test  [goflags] [packages]
gogo preprocess <file.gogo>         # print preprocessed Go to stdout (for debugging)
gogo <go command> [args...]         # passed through to the Go toolchain unchanged
```

## Language extensions

### Union types

gogo adds a concise union-type declaration syntax using `=` and `|`:

```gogo
type Number   = int | float64
type Signed   = ~int | ~int8 | ~int16 | ~int32 | ~int64
type Stringy  = string | []byte
```

gogo transforms these into Go 1.18+ interface type constraints:

```go
type Number   interface{ int | float64 }
type Signed   interface{ ~int | ~int8 | ~int16 | ~int32 | ~int64 }
type Stringy  interface{ string | []byte }
```

Union types can then be used as bounds on generic type parameters:

```gogo
type Number = int | float64

func Sum[T Number](a, b T) T { return a + b }

func main() {
    println(Sum(1, 2))       // 3
    println(Sum(1.5, 2.5))  // 4
}
```

Multi-line declarations are supported — continue a union across lines by ending each line (except the last) with `|`:

```gogo
type Ordered = ~int  | ~int8  | ~int16 | ~int32 | ~int64  |
               ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
               ~float32 | ~float64 | ~string
```

All existing `.go` syntax is accepted unchanged — gogo is a strict superset.

## Example

See [`examples/union/main.gogo`](examples/union/main.gogo) for a working example.

```sh
gogo run examples/union/main.gogo
# Sum(1, 2)         = 3
# Sum(1.5, 2.5)     = 4
# Max("go", "gogo") = gogo
# Max(42, 7)        = 42
```

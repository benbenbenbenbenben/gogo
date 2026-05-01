package preprocessor

import (
	"strings"
	"testing"
)

func TestTransformUnionType(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple two-type union",
			input: `type StringOrInt = string | int`,
			want:  `type StringOrInt interface{ string | int }`,
		},
		{
			name:  "three-type union",
			input: `type Numeric = int | float32 | float64`,
			want:  `type Numeric interface{ int | float32 | float64 }`,
		},
		{
			name:  "approximation elements (~)",
			input: `type Integer = ~int | ~int8 | ~int16 | ~int32 | ~int64`,
			want:  `type Integer interface{ ~int | ~int8 | ~int16 | ~int32 | ~int64 }`,
		},
		{
			name:  "pointer types",
			input: `type PtrOrVal = *Foo | Foo`,
			want:  `type PtrOrVal interface{ *Foo | Foo }`,
		},
		{
			name:  "indented union",
			input: `	type Num = int | float64`,
			want:  `	type Num interface{ int | float64 }`,
		},
		{
			name:  "qualified type names",
			input: `type IOWriter = io.Writer | io.ReadWriter`,
			want:  `type IOWriter interface{ io.Writer | io.ReadWriter }`,
		},

		// Lines that must NOT be transformed.
		{
			name:  "plain type alias — no pipe",
			input: `type MyInt = int`,
			want:  `type MyInt = int`,
		},
		{
			name:  "type definition — no equals",
			input: `type MyStruct struct{ X int }`,
			want:  `type MyStruct struct{ X int }`,
		},
		{
			name:  "already valid Go interface constraint",
			input: `type Num interface{ int | float64 }`,
			want:  `type Num interface{ int | float64 }`,
		},
		{
			name:  "already valid Go interface alias",
			input: `type Num = interface{ int | float64 }`,
			want:  `type Num = interface{ int | float64 }`,
		},
		{
			name:  "regular variable declaration",
			input: `x := a | b`,
			want:  `x := a | b`,
		},
		{
			name:  "non-type line",
			input: `fmt.Println("hello")`,
			want:  `fmt.Println("hello")`,
		},
		{
			name:  "empty line",
			input: ``,
			want:  ``,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := transformUnionType(tc.input)
			if got != tc.want {
				t.Errorf("\ninput: %q\n  got: %q\n want: %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestProcess(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "union type in a full file",
			input: strings.TrimSpace(`
package main

type Number = int | float64

func main() {}
`),
			want: strings.TrimSpace(`
package main

type Number interface{ int | float64 }

func main() {}
`),
		},
		{
			name: "multiple union types",
			input: strings.TrimSpace(`
package foo

type Signed = int | int8 | int16 | int32 | int64
type Unsigned = uint | uint8 | uint16 | uint32 | uint64
`),
			want: strings.TrimSpace(`
package foo

type Signed interface{ int | int8 | int16 | int32 | int64 }
type Unsigned interface{ uint | uint8 | uint16 | uint32 | uint64 }
`),
		},
		{
			name: "line comments are preserved",
			input: strings.TrimSpace(`
package foo

// type Foo = int | string  ← this is a comment, do not transform
type Bar = int | string
`),
			want: strings.TrimSpace(`
package foo

// type Foo = int | string  ← this is a comment, do not transform
type Bar interface{ int | string }
`),
		},
		{
			name: "block comments are preserved",
			input: strings.TrimSpace(`
package foo

/*
type Foo = int | string
*/
type Bar = int | string
`),
			want: strings.TrimSpace(`
package foo

/*
type Foo = int | string
*/
type Bar interface{ int | string }
`),
		},
		{
			name: "multi-line union type",
			input: strings.Join([]string{
				"package foo",
				"",
				"type Ordered = ~int | ~int8 | ~int16 |",
				"\t~uint | ~uint8 |",
				"\t~float32 | ~float64",
			}, "\n"),
			want: strings.Join([]string{
				"package foo",
				"",
				"type Ordered interface{ ~int | ~int8 | ~int16 | ~uint | ~uint8 | ~float32 | ~float64 }",
			}, "\n"),
		},
		{
			name: "plain go file passes through unchanged",
			input: strings.TrimSpace(`
package main

import "fmt"

func main() {
	fmt.Println("hello, world")
}
`),
			want: strings.TrimSpace(`
package main

import "fmt"

func main() {
	fmt.Println("hello, world")
}
`),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Process(tc.input)
			if got != tc.want {
				t.Errorf("\n=== INPUT ===\n%s\n=== GOT ===\n%s\n=== WANT ===\n%s", tc.input, got, tc.want)
			}
		})
	}
}

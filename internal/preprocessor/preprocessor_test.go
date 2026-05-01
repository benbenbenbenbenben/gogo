package preprocessor

import (
	"go/parser"
	"go/token"
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

func TestTransformEnumType(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		wantInfo enumInfo
		want     string
	}{
		{
			name: "simple enum",
			input: []string{
				"type Color enum {",
				"\tRed",
				"\tGreen",
				"\tBlue",
				"}",
			},
			wantInfo: enumInfo{
				Name:  "Color",
				IsADT: false,
				Variants: []enumVariant{
					{Name: "Red", ConstructorName: "ColorRed", ConcreteType: "_gogo_internal_Color_Red"},
					{Name: "Green", ConstructorName: "ColorGreen", ConcreteType: "_gogo_internal_Color_Green"},
					{Name: "Blue", ConstructorName: "ColorBlue", ConcreteType: "_gogo_internal_Color_Blue"},
				},
			},
			want: strings.Join([]string{
				"type Color int",
				"",
				"const (",
				"\tColorRed Color = iota",
				"\tColorGreen",
				"\tColorBlue",
				")",
			}, "\n"),
		},
		{
			name: "payload enum",
			input: []string{
				"type Color enum {",
				"\tRed",
				"\tRGB(r byte, g byte, b byte)",
				"}",
			},
			wantInfo: enumInfo{
				Name:  "Color",
				IsADT: true,
				Variants: []enumVariant{
					{Name: "Red", ConstructorName: "ColorRed", ConcreteType: "_gogo_internal_Color_Red"},
					{Name: "RGB", ConstructorName: "ColorRGB", ConcreteType: "_gogo_internal_Color_RGB", Fields: []enumField{{Name: "r", Type: "byte"}, {Name: "g", Type: "byte"}, {Name: "b", Type: "byte"}}},
				},
			},
			want: strings.Join([]string{
				"type Color interface{ isColor() }",
				"",
				"type _gogo_internal_Color_Red struct{}",
				"func (_gogo_internal_Color_Red) isColor() {}",
				"var ColorRed Color = _gogo_internal_Color_Red{}",
				"",
				"type _gogo_internal_Color_RGB struct {",
				"\tr byte",
				"\tg byte",
				"\tb byte",
				"}",
				"func (_gogo_internal_Color_RGB) isColor() {}",
				"func ColorRGB(r byte, g byte, b byte) Color {",
				"\treturn _gogo_internal_Color_RGB{r: r, g: g, b: b}",
				"}",
			}, "\n"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotInfo, got, ok := transformEnumType(tc.input)
			if !ok {
				t.Fatalf("transformEnumType(%#v) reported failure", tc.input)
			}
			if got != tc.want {
				t.Errorf("\ninput: %#v\n  got: %q\n want: %q", tc.input, got, tc.want)
			}
			if gotInfo.Name != tc.wantInfo.Name || gotInfo.IsADT != tc.wantInfo.IsADT {
				t.Fatalf("unexpected enum info: %#v", gotInfo)
			}
			if len(gotInfo.Variants) != len(tc.wantInfo.Variants) {
				t.Fatalf("unexpected variant count: %#v", gotInfo)
			}
			for i, variant := range gotInfo.Variants {
				wantVariant := tc.wantInfo.Variants[i]
				if variant.Name != wantVariant.Name || variant.ConstructorName != wantVariant.ConstructorName || variant.ConcreteType != wantVariant.ConcreteType {
					t.Fatalf("unexpected variant %d: %#v", i, variant)
				}
				if len(variant.Fields) != len(wantVariant.Fields) {
					t.Fatalf("unexpected variant field count %d: %#v", i, variant.Fields)
				}
				for j, field := range variant.Fields {
					if field != wantVariant.Fields[j] {
						t.Fatalf("unexpected variant field %d/%d: %#v", i, j, field)
					}
				}
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
			name: "simple enum stays integer-backed and supports selectors",
			input: strings.Join([]string{
				"package foo",
				"",
				"type Color enum {",
				"\tRed",
				"\tGreen",
				"\tBlue",
				"}",
				"",
				"var favorite = Color.Red",
			}, "\n"),
			want: strings.Join([]string{
				"package foo",
				"",
				"type Color int",
				"",
				"const (",
				"\tColorRed Color = iota",
				"\tColorGreen",
				"\tColorBlue",
				")",
				"",
				"var favorite = ColorRed",
			}, "\n"),
		},
		{
			name: "payload enum selectors and constructors are rewritten",
			input: strings.Join([]string{
				"package foo",
				"",
				"type Color enum {",
				"\tRed",
				"\tRGB(r byte, g byte, b byte)",
				"}",
				"",
				"var a = Color.Red",
				"var b = Color.RGB(1, 2, 3)",
			}, "\n"),
			want: strings.Join([]string{
				"package foo",
				"",
				"type Color interface{ isColor() }",
				"",
				"type _gogo_internal_Color_Red struct{}",
				"func (_gogo_internal_Color_Red) isColor() {}",
				"var ColorRed Color = _gogo_internal_Color_Red{}",
				"",
				"type _gogo_internal_Color_RGB struct {",
				"\tr byte",
				"\tg byte",
				"\tb byte",
				"}",
				"func (_gogo_internal_Color_RGB) isColor() {}",
				"func ColorRGB(r byte, g byte, b byte) Color {",
				"\treturn _gogo_internal_Color_RGB{r: r, g: g, b: b}",
				"}",
				"",
				"var a = ColorRed",
				"var b = ColorRGB(1, 2, 3)",
			}, "\n"),
		},
		{
			name: "simple enum match rewrites to switch",
			input: strings.Join([]string{
				"package foo",
				"",
				"type Color enum {",
				"\tRed",
				"\tGreen",
				"}",
				"",
				"func describe(color Color) string {",
				"\tmatch color {",
				"\tcase Color.Red:",
				"\t\treturn \"red\"",
				"\tcase Color.Green:",
				"\t\treturn \"green\"",
				"\tdefault:",
				"\t\treturn \"unknown\"",
				"\t}",
				"}",
			}, "\n"),
			want: strings.Join([]string{
				"package foo",
				"",
				"type Color int",
				"",
				"const (",
				"\tColorRed Color = iota",
				"\tColorGreen",
				")",
				"",
				"func describe(color Color) string {",
				"\tswitch _gogo_internal_match_0 := color; _gogo_internal_match_0 {",
				"\tcase ColorRed:",
				"\t\treturn \"red\"",
				"\tcase ColorGreen:",
				"\t\treturn \"green\"",
				"\tdefault:",
				"\t\treturn \"unknown\"",
				"\t}",
				"}",
			}, "\n"),
		},
		{
			name: "payload enum match rewrites to type switch with bindings",
			input: strings.Join([]string{
				"package foo",
				"",
				"type Color enum {",
				"\tRed",
				"\tRGB(r byte, g byte, b byte)",
				"}",
				"",
				"func describe(color Color) string {",
				"\tmatch color {",
				"\tcase Color.Red:",
				"\t\treturn \"red\"",
				"\tcase Color.RGB(r, g, b):",
				"\t\treturn string([]byte{r, g, b})",
				"\tdefault:",
				"\t\treturn \"unknown\"",
				"\t}",
				"}",
			}, "\n"),
			want: strings.Join([]string{
				"package foo",
				"",
				"type Color interface{ isColor() }",
				"",
				"type _gogo_internal_Color_Red struct{}",
				"func (_gogo_internal_Color_Red) isColor() {}",
				"var ColorRed Color = _gogo_internal_Color_Red{}",
				"",
				"type _gogo_internal_Color_RGB struct {",
				"\tr byte",
				"\tg byte",
				"\tb byte",
				"}",
				"func (_gogo_internal_Color_RGB) isColor() {}",
				"func ColorRGB(r byte, g byte, b byte) Color {",
				"\treturn _gogo_internal_Color_RGB{r: r, g: g, b: b}",
				"}",
				"",
				"func describe(color Color) string {",
				"\tswitch _gogo_internal_match_0 := (color).(type) {",
				"\tcase _gogo_internal_Color_Red:",
				"\t\treturn \"red\"",
				"\tcase _gogo_internal_Color_RGB:",
				"\t\tr, g, b := _gogo_internal_match_0.r, _gogo_internal_match_0.g, _gogo_internal_match_0.b",
				"\t\treturn string([]byte{r, g, b})",
				"\tdefault:",
				"\t\treturn \"unknown\"",
				"\t}",
				"}",
			}, "\n"),
		},
		{
			name: "invalid enum variants pass through unchanged",
			input: strings.Join([]string{
				"package foo",
				"",
				"type Bad enum {",
				"\t123Invalid",
				"}",
			}, "\n"),
			want: strings.Join([]string{
				"package foo",
				"",
				"type Bad enum {",
				"\t123Invalid",
				"}",
			}, "\n"),
		},
		{
			name: "duplicate enum variants pass through unchanged",
			input: strings.Join([]string{
				"package foo",
				"",
				"type Bad enum {",
				"\tRed",
				"\tRed",
				"}",
			}, "\n"),
			want: strings.Join([]string{
				"package foo",
				"",
				"type Bad enum {",
				"\tRed",
				"\tRed",
				"}",
			}, "\n"),
		},
		{
			name: "comments with enum syntax are preserved",
			input: strings.TrimSpace(`
package foo

// type Color enum { Red, Green, Blue }
type Number = int | float64
`),
			want: strings.TrimSpace(`
package foo

// type Color enum { Red, Green, Blue }
type Number interface{ int | float64 }
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

func TestProcessProducesParseableGo(t *testing.T) {
	src := strings.Join([]string{
		"package foo",
		"",
		"type Color enum {",
		"\tRed",
		"\tRGB(r byte, g byte, b byte)",
		"}",
		"",
		"func describe(color Color) string {",
		"\tmatch color {",
		"\tcase Color.Red:",
		"\t\treturn \"red\"",
		"\tcase Color.RGB(r, g, b):",
		"\t\treturn string([]byte{r, g, b})",
		"\tdefault:",
		"\t\treturn \"unknown\"",
		"\t}",
		"}",
	}, "\n")

	got := Process(src)
	if _, err := parser.ParseFile(token.NewFileSet(), "generated.go", got, parser.AllErrors); err != nil {
		t.Fatalf("generated Go did not parse: %v\n%s", err, got)
	}
}

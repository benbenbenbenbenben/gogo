// Package preprocessor transforms .gogo source files into valid Go source.
//
// It handles gogo language extensions that are not valid Go syntax, translating
// them into equivalent Go constructs before the code reaches the Go compiler.
//
// # Union types
//
// gogo extends Go with a concise union-type declaration syntax:
//
//	type Name = TypeA | TypeB | TypeC
//
// This is transformed into a Go 1.18+ interface type constraint:
//
//	type Name interface{ TypeA | TypeB | TypeC }
//
// The ~ approximation element is also supported:
//
//	type Integer = ~int | ~int8 | ~int16 | ~int32 | ~int64
//
// Union types can be used as constraints for generic functions:
//
//	func Sum[T Number](a, b T) T { return a + b }
package preprocessor

import (
	"strings"
)

// Process transforms gogo source code into valid Go source code.
// All valid .go syntax passes through unchanged — gogo is a strict superset.
func Process(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	inBlockComment := false

	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Track block comments — never transform inside them.
		if inBlockComment {
			out = append(out, line)
			if strings.Contains(line, "*/") {
				inBlockComment = false
			}
			i++
			continue
		}
		if strings.Contains(trimmed, "/*") && !strings.Contains(trimmed, "*/") {
			inBlockComment = true
			out = append(out, line)
			i++
			continue
		}

		// Skip line comments.
		if strings.HasPrefix(trimmed, "//") {
			out = append(out, line)
			i++
			continue
		}

		// Detect the start of a union type declaration.  A declaration may
		// span multiple lines when each line (except the last) ends with '|'.
		if isUnionTypeStart(trimmed) {
			// Accumulate continuation lines.
			combined := trimmed
			for strings.HasSuffix(strings.TrimSpace(combined), "|") {
				i++
				if i >= len(lines) {
					break
				}
				next := strings.TrimSpace(lines[i])
				if strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/*") {
					// A comment terminates the continuation.
					break
				}
				combined = strings.TrimSpace(combined) + " " + next
			}

			// Preserve original indentation from the first line.
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			out = append(out, transformUnionType(indent+strings.TrimSpace(combined)))
			i++
			continue
		}

		out = append(out, transformUnionType(line))
		i++
	}
	return strings.Join(out, "\n")
}

// isUnionTypeStart reports whether trimmed is the beginning of a gogo union
// type declaration (i.e. "type Name = …" where the RHS contains '|').
func isUnionTypeStart(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "type ") {
		return false
	}
	eqIdx := strings.Index(trimmed, "=")
	if eqIdx < 0 {
		return false
	}
	rhs := strings.TrimSpace(trimmed[eqIdx+1:])
	return strings.Contains(rhs, "|") &&
		!strings.Contains(rhs, "interface") &&
		!strings.Contains(rhs, "{")
}

// transformUnionType converts a gogo union type declaration to a Go interface
// constraint if the line matches the pattern:
//
//	type <Identifier> = <T1> | <T2> [ | <T3> ...]
//
// Lines that are already valid Go (e.g. type aliases without |, or interface
// types) are returned unchanged.
func transformUnionType(line string) string {
	trimmed := strings.TrimSpace(line)

	// Must start with "type ".
	if !strings.HasPrefix(trimmed, "type ") {
		return line
	}

	// Strip leading "type ".
	rest := strings.TrimSpace(trimmed[len("type "):])

	// Parse the type name (identifier characters only).
	nameEnd := strings.IndexAny(rest, " \t=[{(")
	if nameEnd <= 0 {
		return line
	}
	name := rest[:nameEnd]
	rest = strings.TrimSpace(rest[nameEnd:])

	// Expect '=' for gogo union alias syntax.
	if !strings.HasPrefix(rest, "=") {
		return line
	}
	rhs := strings.TrimSpace(rest[1:])

	// The right-hand side must contain '|' to be a union declaration.
	if !strings.Contains(rhs, "|") {
		return line
	}

	// Skip lines that are already valid Go generics syntax.
	if strings.Contains(rhs, "interface") || strings.Contains(rhs, "{") {
		return line
	}

	// Preserve original indentation.
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]

	return indent + "type " + name + " interface{ " + rhs + " }"
}

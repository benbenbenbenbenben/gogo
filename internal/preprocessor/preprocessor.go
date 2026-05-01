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
// func Sum[T Number](a, b T) T { return a + b }
//
// # Enum ADTs
//
// gogo also supports enum declarations, including payload-carrying variants:
//
// type Color enum {
// Red
// Green
// Blue
// RGB(r byte, g byte, b byte)
// }
//
// Plain enums are transformed into Go integer-backed enums, while payload enums
// are transformed into tagged interface-based ADTs with generated constructors.
package preprocessor

import (
	"fmt"
	"strings"
	"unicode"
)

// Process transforms gogo source code into valid Go source code.
// All valid .go syntax passes through unchanged — gogo is a strict superset.
func Process(src string) string {
	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines))
	inBlockComment := false
	enums := map[string]enumInfo{}

	for i := 0; i < len(lines); {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

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

		if strings.HasPrefix(trimmed, "//") {
			out = append(out, line)
			i++
			continue
		}

		if isEnumTypeStart(trimmed) {
			var block []string
			for {
				if i >= len(lines) {
					return finalizeProcessedSource(strings.Join(append(out, block...), "\n"), enums)
				}
				block = append(block, lines[i])
				if isEnumTypeEnd(lines[i], len(block) == 1) {
					break
				}
				i++
			}
			if info, rendered, ok := transformEnumType(block); ok {
				enums[info.Name] = info
				out = append(out, rendered)
			} else {
				out = append(out, strings.Join(block, "\n"))
			}
			i++
			continue
		}

		if isUnionTypeStart(trimmed) {
			combined := trimmed
			for strings.HasSuffix(strings.TrimSpace(combined), "|") {
				i++
				if i >= len(lines) {
					break
				}
				next := strings.TrimSpace(lines[i])
				if strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/*") {
					break
				}
				combined = strings.TrimSpace(combined) + " " + next
			}

			indent := getIndent(line)
			out = append(out, transformUnionType(indent+strings.TrimSpace(combined)))
			i++
			continue
		}

		out = append(out, transformUnionType(line))
		i++
	}

	return finalizeProcessedSource(strings.Join(out, "\n"), enums)
}

type enumInfo struct {
	Name     string
	Variants []enumVariant
	IsADT    bool
}

type enumVariant struct {
	Name            string
	Fields          []enumField
	ConstructorName string
	ConcreteType    string
}

type enumField struct {
	Name string
	Type string
}

func finalizeProcessedSource(src string, enums map[string]enumInfo) string {
	if len(enums) == 0 {
		return src
	}
	src = transformMatchStatements(src, enums)
	return transformEnumSelectors(src, enums)
}

// isEnumTypeStart reports whether trimmed begins a gogo enum declaration.
func isEnumTypeStart(trimmed string) bool {
	if !strings.HasPrefix(trimmed, "type ") {
		return false
	}
	rest := strings.TrimSpace(trimmed[len("type "):])
	nameEnd := strings.IndexAny(rest, " \t{")
	if nameEnd <= 0 {
		return false
	}
	rest = strings.TrimSpace(rest[nameEnd:])
	return hasEnumKeywordPrefix(rest) && strings.Contains(rest, "{")
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

// transformEnumType converts a gogo enum declaration block into Go code.
func transformEnumType(lines []string) (enumInfo, string, bool) {
	if len(lines) == 0 {
		return enumInfo{}, "", false
	}

	header := strings.TrimSpace(lines[0])
	rest := strings.TrimSpace(header[len("type "):])
	nameEnd := strings.IndexAny(rest, " \t{")
	if nameEnd <= 0 {
		return enumInfo{}, "", false
	}
	name := rest[:nameEnd]
	rest = strings.TrimSpace(rest[nameEnd:])
	if !hasEnumKeywordPrefix(rest) {
		return enumInfo{}, "", false
	}

	bodyStart := strings.Index(header, "{")
	if bodyStart < 0 {
		return enumInfo{}, "", false
	}

	bodyParts := []string{header[bodyStart+1:]}
	for _, line := range lines[1:] {
		bodyParts = append(bodyParts, line)
	}
	body := strings.Join(bodyParts, "\n")
	if end := strings.LastIndex(body, "}"); end >= 0 {
		body = body[:end]
	}

	variants, ok := extractEnumVariants(body, name)
	if !ok || len(variants) == 0 {
		return enumInfo{}, "", false
	}

	info := enumInfo{Name: name, Variants: variants}
	for _, variant := range variants {
		if len(variant.Fields) > 0 {
			info.IsADT = true
			break
		}
	}

	indent := getIndent(lines[0])
	if !info.IsADT {
		return info, renderSimpleEnumType(info, indent), true
	}
	return info, renderADTEnumType(info, indent), true
}

func renderSimpleEnumType(info enumInfo, indent string) string {
	var b strings.Builder
	b.WriteString(indent)
	b.WriteString("type ")
	b.WriteString(info.Name)
	b.WriteString(" int\n\n")
	b.WriteString(indent)
	b.WriteString("const (\n")
	for i, variant := range info.Variants {
		b.WriteString(indent)
		b.WriteString("\t")
		b.WriteString(variant.ConstructorName)
		if i == 0 {
			b.WriteString(" ")
			b.WriteString(info.Name)
			b.WriteString(" = iota\n")
			continue
		}
		b.WriteString("\n")
	}
	b.WriteString(indent)
	b.WriteString(")")
	return b.String()
}

func renderADTEnumType(info enumInfo, indent string) string {
	var b strings.Builder
	marker := enumMarkerMethod(info.Name)

	b.WriteString(indent)
	b.WriteString("type ")
	b.WriteString(info.Name)
	b.WriteString(" interface{ ")
	b.WriteString(marker)
	b.WriteString("() }\n\n")

	for idx, variant := range info.Variants {
		b.WriteString(indent)
		b.WriteString("type ")
		b.WriteString(variant.ConcreteType)
		if len(variant.Fields) == 0 {
			b.WriteString(" struct{}\n")
		} else {
			b.WriteString(" struct {\n")
			for _, field := range variant.Fields {
				b.WriteString(indent)
				b.WriteString("\t")
				b.WriteString(field.Name)
				b.WriteString(" ")
				b.WriteString(field.Type)
				b.WriteString("\n")
			}
			b.WriteString(indent)
			b.WriteString("}\n")
		}

		b.WriteString(indent)
		b.WriteString("func (")
		b.WriteString(variant.ConcreteType)
		b.WriteString(") ")
		b.WriteString(marker)
		b.WriteString("() {}\n")

		b.WriteString(indent)
		if len(variant.Fields) == 0 {
			b.WriteString("var ")
			b.WriteString(variant.ConstructorName)
			b.WriteString(" ")
			b.WriteString(info.Name)
			b.WriteString(" = ")
			b.WriteString(variant.ConcreteType)
			b.WriteString("{}\n")
		} else {
			b.WriteString("func ")
			b.WriteString(variant.ConstructorName)
			b.WriteString("(")
			b.WriteString(renderFieldParams(variant.Fields))
			b.WriteString(") ")
			b.WriteString(info.Name)
			b.WriteString(" {\n")
			b.WriteString(indent)
			b.WriteString("\treturn ")
			b.WriteString(variant.ConcreteType)
			b.WriteString("{")
			b.WriteString(renderFieldAssignments(variant.Fields))
			b.WriteString("}\n")
			b.WriteString(indent)
			b.WriteString("}\n")
		}

		if idx < len(info.Variants)-1 {
			b.WriteString("\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// transformUnionType converts a gogo union type declaration to a Go interface
// constraint if the line matches the pattern:
//
// type <Identifier> = <T1> | <T2> [ | <T3> ...]
//
// Lines that are already valid Go (e.g. type aliases without |, or interface
// types) are returned unchanged.
func transformUnionType(line string) string {
	trimmed := strings.TrimSpace(line)

	if !strings.HasPrefix(trimmed, "type ") {
		return line
	}

	rest := strings.TrimSpace(trimmed[len("type "):])
	nameEnd := strings.IndexAny(rest, " \t=[{(")
	if nameEnd <= 0 {
		return line
	}
	name := rest[:nameEnd]
	rest = strings.TrimSpace(rest[nameEnd:])

	if !strings.HasPrefix(rest, "=") {
		return line
	}
	rhs := strings.TrimSpace(rest[1:])

	if !strings.Contains(rhs, "|") {
		return line
	}

	if strings.Contains(rhs, "interface") || strings.Contains(rhs, "{") {
		return line
	}

	indent := getIndent(line)

	return indent + "type " + name + " interface{ " + rhs + " }"
}

func getIndent(line string) string {
	return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
}

func isEnumTypeEnd(line string, allowInline bool) bool {
	trimmed := strings.TrimSpace(line)
	if allowInline {
		return strings.Contains(trimmed, "}")
	}
	return strings.HasPrefix(trimmed, "}")
}

func hasEnumKeywordPrefix(s string) bool {
	if !strings.HasPrefix(s, "enum") {
		return false
	}
	if len(s) == len("enum") {
		return true
	}
	switch s[len("enum")] {
	case ' ', '\t', '{':
		return true
	default:
		return false
	}
}

func extractEnumVariants(body, enumName string) ([]enumVariant, bool) {
	body = stripBlockComments(body)
	entries := splitEnumEntries(body)
	if len(entries) == 0 {
		return nil, false
	}

	variants := make([]enumVariant, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		variant, ok := parseEnumVariant(entry, enumName)
		if !ok || seen[variant.Name] {
			return nil, false
		}
		seen[variant.Name] = true
		variants = append(variants, variant)
	}
	return variants, true
}

func splitEnumEntries(body string) []string {
	var entries []string
	var current strings.Builder
	depth := 0

	flush := func() {
		entry := strings.TrimSpace(current.String())
		if entry != "" {
			entries = append(entries, entry)
		}
		current.Reset()
	}

	for _, r := range body {
		switch r {
		case '\n':
			if depth == 0 {
				flush()
				continue
			}
		case ',':
			if depth == 0 {
				flush()
				continue
			}
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		}
		current.WriteRune(r)
	}
	flush()
	return entries
}

func parseEnumVariant(entry, enumName string) (enumVariant, bool) {
	entry = strings.TrimSpace(stripLineComment(entry))
	if entry == "" {
		return enumVariant{}, false
	}
	open := strings.Index(entry, "(")
	if open < 0 {
		if !isValidGoIdentifier(entry) {
			return enumVariant{}, false
		}
		return enumVariant{
			Name:            entry,
			ConstructorName: enumName + entry,
			ConcreteType:    generatedVariantType(enumName, entry),
		}, true
	}
	if !strings.HasSuffix(entry, ")") {
		return enumVariant{}, false
	}
	name := strings.TrimSpace(entry[:open])
	if !isValidGoIdentifier(name) {
		return enumVariant{}, false
	}
	fields, ok := parseEnumFields(entry[open+1 : len(entry)-1])
	if !ok || len(fields) == 0 {
		return enumVariant{}, false
	}
	return enumVariant{
		Name:            name,
		Fields:          fields,
		ConstructorName: enumName + name,
		ConcreteType:    generatedVariantType(enumName, name),
	}, true
}

func parseEnumFields(src string) ([]enumField, bool) {
	parts := splitTopLevel(src, ',')
	fields := make([]enumField, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, rest, ok := cutLeadingIdentifier(part)
		if !ok || !isValidGoIdentifier(name) {
			return nil, false
		}
		typ := strings.TrimSpace(rest)
		if typ == "" || seen[name] {
			return nil, false
		}
		seen[name] = true
		fields = append(fields, enumField{Name: name, Type: typ})
	}
	if len(fields) == 0 {
		return nil, false
	}
	return fields, true
}

func splitTopLevel(src string, sep rune) []string {
	var parts []string
	var current strings.Builder
	depthParen, depthBracket, depthBrace := 0, 0, 0
	for _, r := range src {
		switch r {
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '[':
			depthBracket++
		case ']':
			if depthBracket > 0 {
				depthBracket--
			}
		case '{':
			depthBrace++
		case '}':
			if depthBrace > 0 {
				depthBrace--
			}
		case sep:
			if depthParen == 0 && depthBracket == 0 && depthBrace == 0 {
				parts = append(parts, current.String())
				current.Reset()
				continue
			}
		}
		current.WriteRune(r)
	}
	parts = append(parts, current.String())
	return parts
}

func stripLineComment(s string) string {
	if idx := strings.Index(s, "//"); idx >= 0 {
		return s[:idx]
	}
	return s
}

func renderFieldParams(fields []enumField) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, field.Name+" "+field.Type)
	}
	return strings.Join(parts, ", ")
}

func renderFieldAssignments(fields []enumField) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, field.Name+": "+field.Name)
	}
	return strings.Join(parts, ", ")
}

func enumMarkerMethod(name string) string {
	return "is" + name
}

func generatedVariantType(enumName, variantName string) string {
	return fmt.Sprintf("__gogo_internal_%s_%s", enumName, variantName)
}

func transformEnumSelectors(src string, enums map[string]enumInfo) string {
	var b strings.Builder
	for i := 0; i < len(src); {
		if end, ok := scanCommentOrString(src, i); ok {
			b.WriteString(src[i:end])
			i = end
			continue
		}

		if !isIdentifierStart(rune(src[i])) {
			b.WriteByte(src[i])
			i++
			continue
		}

		ident, next := readIdentifier(src, i)
		info, ok := enums[ident]
		if !ok {
			b.WriteString(ident)
			i = next
			continue
		}

		j := skipSpaces(src, next)
		if j >= len(src) || src[j] != '.' {
			b.WriteString(ident)
			i = next
			continue
		}
		k := skipSpaces(src, j+1)
		variantName, afterVariant := readIdentifier(src, k)
		if variantName == "" {
			b.WriteString(ident)
			i = next
			continue
		}
		variant, found := info.variantByName(variantName)
		if !found {
			b.WriteString(ident)
			i = next
			continue
		}

		l := skipSpaces(src, afterVariant)
		if len(variant.Fields) > 0 && l < len(src) && src[l] == '(' {
			end, ok := scanBalanced(src, l, '(', ')')
			if !ok {
				b.WriteString(src[i:afterVariant])
				i = afterVariant
				continue
			}
			b.WriteString(variant.ConstructorName)
			b.WriteString(src[l : end+1])
			i = end + 1
			continue
		}
		if len(variant.Fields) == 0 {
			b.WriteString(variant.ConstructorName)
			i = afterVariant
			continue
		}

		b.WriteString(src[i:afterVariant])
		i = afterVariant
	}
	return b.String()
}

func transformMatchStatements(src string, enums map[string]enumInfo) string {
	var b strings.Builder
	matchIndex := 0

	for i := 0; i < len(src); {
		if end, ok := scanCommentOrString(src, i); ok {
			b.WriteString(src[i:end])
			i = end
			continue
		}
		if !isWordAt(src, i, "match") {
			b.WriteByte(src[i])
			i++
			continue
		}

		exprStart := skipSpaces(src, i+len("match"))
		bracePos, ok := findMatchBlockStart(src, exprStart)
		if !ok {
			b.WriteByte(src[i])
			i++
			continue
		}
		blockEnd, ok := scanBalanced(src, bracePos, '{', '}')
		if !ok {
			b.WriteByte(src[i])
			i++
			continue
		}

		expr := strings.TrimSpace(src[exprStart:bracePos])
		indent := lineIndentAt(src, i)
		transformed, ok := transformMatchBlock(expr, src[bracePos+1:blockEnd], indent, enums, matchIndex)
		if !ok {
			b.WriteByte(src[i])
			i++
			continue
		}
		lineStart := strings.LastIndex(src[:i], "\n") + 1
		if strings.TrimSpace(src[lineStart:i]) == "" && indent != "" {
			prefix := b.String()
			b.Reset()
			b.WriteString(prefix[:len(prefix)-len(indent)])
		}
		b.WriteString(transformed)
		i = blockEnd + 1
		matchIndex++
	}

	return b.String()
}

type matchClause struct {
	Header string
	Body   string
}

func transformMatchBlock(expr, body, indent string, enums map[string]enumInfo, matchIndex int) (string, bool) {
	clauses, ok := parseMatchClauses(body)
	if !ok || len(clauses) == 0 {
		return "", false
	}

	var enumName string
	parsed := make([]parsedMatchClause, 0, len(clauses))
	for _, clause := range clauses {
		pc, ok := parseMatchClause(clause, enums)
		if !ok {
			return "", false
		}
		if pc.Default {
			parsed = append(parsed, pc)
			continue
		}
		if enumName == "" {
			enumName = pc.EnumName
		} else if enumName != pc.EnumName {
			return "", false
		}
		parsed = append(parsed, pc)
	}
	if enumName == "" {
		return "", false
	}
	info, ok := enums[enumName]
	if !ok {
		return "", false
	}

	var b strings.Builder
	matchVar := fmt.Sprintf("__gogo_internal_match_%d", matchIndex)
	if info.IsADT {
		b.WriteString(indent)
		b.WriteString("switch ")
		b.WriteString(matchVar)
		b.WriteString(" := (")
		b.WriteString(expr)
		b.WriteString(").(type) {\n")
	} else {
		b.WriteString(indent)
		b.WriteString("switch ")
		b.WriteString(matchVar)
		b.WriteString(" := ")
		b.WriteString(expr)
		b.WriteString("; ")
		b.WriteString(matchVar)
		b.WriteString(" {\n")
	}

	for _, clause := range parsed {
		b.WriteString(indent)
		if clause.Default {
			b.WriteString("default")
		} else {
			b.WriteString("case ")
			if info.IsADT {
				b.WriteString(clause.Variant.ConcreteType)
			} else {
				b.WriteString(clause.Variant.ConstructorName)
			}
		}
		b.WriteString(":\n")
		if info.IsADT && len(clause.Bindings) > 0 {
			b.WriteString(indent)
			b.WriteString("\t")
			for i, binding := range clause.Bindings {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(binding)
			}
			b.WriteString(" := ")
			for i, field := range clause.Variant.Fields {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(matchVar)
				b.WriteString(".")
				b.WriteString(field.Name)
			}
			b.WriteString("\n")
		}
		if clause.Body != "" {
			body := strings.TrimRight(clause.Body, " \t\r\n")
			b.WriteString(body)
			if !strings.HasSuffix(body, "\n") {
				b.WriteString("\n")
			}
		}
	}
	b.WriteString(indent)
	b.WriteString("}")
	return b.String(), true
}

type parsedMatchClause struct {
	Default  bool
	EnumName string
	Variant  enumVariant
	Bindings []string
	Body     string
}

func parseMatchClauses(body string) ([]matchClause, bool) {
	lines := strings.Split(body, "\n")
	var clauses []matchClause
	var current *matchClause
	depth := 0
	inBlockComment := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if depth == 0 && !inBlockComment && isMatchCaseHeader(trimmed) {
			clauses = append(clauses, matchClause{Header: trimmed})
			current = &clauses[len(clauses)-1]
			continue
		}
		if current == nil {
			if trimmed == "" {
				continue
			}
			return nil, false
		}
		if current.Body == "" {
			current.Body = line
		} else {
			current.Body += "\n" + line
		}
		var delta int
		delta, inBlockComment = braceDelta(line, inBlockComment)
		depth += delta
		if depth < 0 {
			return nil, false
		}
	}
	return clauses, true
}

func isMatchCaseHeader(trimmed string) bool {
	return strings.HasPrefix(trimmed, "case ") || trimmed == "default:" || strings.HasPrefix(trimmed, "default:")
}

func parseMatchClause(clause matchClause, enums map[string]enumInfo) (parsedMatchClause, bool) {
	if clause.Header == "default:" {
		return parsedMatchClause{Default: true, Body: clause.Body}, true
	}
	if !strings.HasPrefix(clause.Header, "case ") || !strings.HasSuffix(clause.Header, ":") {
		return parsedMatchClause{}, false
	}
	pattern := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(clause.Header[len("case "):]), ":"))
	enumName, variantName, bindings, ok := parseMatchPattern(pattern)
	if !ok {
		return parsedMatchClause{}, false
	}
	info, ok := enums[enumName]
	if !ok {
		return parsedMatchClause{}, false
	}
	variant, ok := info.variantByName(variantName)
	if !ok {
		return parsedMatchClause{}, false
	}
	if len(bindings) > 0 {
		if !info.IsADT || len(bindings) != len(variant.Fields) {
			return parsedMatchClause{}, false
		}
		seen := map[string]bool{}
		for _, binding := range bindings {
			if !isValidGoIdentifier(binding) || seen[binding] {
				return parsedMatchClause{}, false
			}
			seen[binding] = true
		}
	}
	if len(bindings) == 0 && len(variant.Fields) > 0 && strings.Contains(pattern, "(") {
		return parsedMatchClause{}, false
	}
	return parsedMatchClause{
		EnumName: enumName,
		Variant:  variant,
		Bindings: bindings,
		Body:     clause.Body,
	}, true
}

func parseMatchPattern(pattern string) (string, string, []string, bool) {
	dot := strings.Index(pattern, ".")
	if dot <= 0 {
		return "", "", nil, false
	}
	enumName := strings.TrimSpace(pattern[:dot])
	rest := strings.TrimSpace(pattern[dot+1:])
	if !isValidGoIdentifier(enumName) {
		return "", "", nil, false
	}
	open := strings.Index(rest, "(")
	if open < 0 {
		if !isValidGoIdentifier(rest) {
			return "", "", nil, false
		}
		return enumName, rest, nil, true
	}
	if !strings.HasSuffix(rest, ")") {
		return "", "", nil, false
	}
	variantName := strings.TrimSpace(rest[:open])
	if !isValidGoIdentifier(variantName) {
		return "", "", nil, false
	}
	bindingsSrc := strings.TrimSpace(rest[open+1 : len(rest)-1])
	parts := splitTopLevel(bindingsSrc, ',')
	bindings := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		bindings = append(bindings, part)
	}
	return enumName, variantName, bindings, true
}

func findMatchBlockStart(src string, start int) (int, bool) {
	depthParen, depthBracket := 0, 0
	for i := start; i < len(src); i++ {
		if end, ok := scanCommentOrString(src, i); ok {
			i = end - 1
			continue
		}
		switch src[i] {
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '[':
			depthBracket++
		case ']':
			if depthBracket > 0 {
				depthBracket--
			}
		case '{':
			if depthParen == 0 && depthBracket == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func lineIndentAt(src string, pos int) string {
	lineStart := strings.LastIndex(src[:pos], "\n") + 1
	return getIndent(src[lineStart:pos])
}

func braceDelta(line string, inBlockComment bool) (int, bool) {
	delta := 0
	for i := 0; i < len(line); i++ {
		if inBlockComment {
			if i+1 < len(line) && line[i] == '*' && line[i+1] == '/' {
				inBlockComment = false
				i++
			}
			continue
		}
		if i+1 < len(line) && line[i] == '/' && line[i+1] == '/' {
			break
		}
		if i+1 < len(line) && line[i] == '/' && line[i+1] == '*' {
			inBlockComment = true
			i++
			continue
		}
		if line[i] == '"' || line[i] == '\'' || line[i] == '`' {
			end, ok := scanStringLiteral(line, i)
			if ok {
				i = end - 1
				continue
			}
		}
		switch line[i] {
		case '{':
			delta++
		case '}':
			delta--
		}
	}
	return delta, inBlockComment
}

func scanCommentOrString(src string, i int) (int, bool) {
	if i >= len(src) {
		return 0, false
	}
	if src[i] == '"' || src[i] == '\'' || src[i] == '`' {
		end, ok := scanStringLiteral(src, i)
		return end, ok
	}
	if i+1 >= len(src) {
		return 0, false
	}
	if src[i] == '/' && src[i+1] == '/' {
		end := i + 2
		for end < len(src) && src[end] != '\n' {
			end++
		}
		return end, true
	}
	if src[i] == '/' && src[i+1] == '*' {
		end := i + 2
		for end+1 < len(src) {
			if src[end] == '*' && src[end+1] == '/' {
				return end + 2, true
			}
			end++
		}
		return len(src), true
	}
	return 0, false
}

func scanStringLiteral(src string, start int) (int, bool) {
	quote := src[start]
	if quote == '`' {
		end := start + 1
		for end < len(src) {
			if src[end] == '`' {
				return end + 1, true
			}
			end++
		}
		return len(src), true
	}
	for i := start + 1; i < len(src); i++ {
		if src[i] == '\\' {
			i++
			continue
		}
		if src[i] == quote {
			return i + 1, true
		}
	}
	return len(src), true
}

func scanBalanced(src string, start int, open, close byte) (int, bool) {
	if start >= len(src) || src[start] != open {
		return 0, false
	}
	depth := 0
	for i := start; i < len(src); i++ {
		if end, ok := scanCommentOrString(src, i); ok {
			i = end - 1
			continue
		}
		switch src[i] {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func isWordAt(src string, i int, word string) bool {
	if !strings.HasPrefix(src[i:], word) {
		return false
	}
	if i > 0 && isIdentifierPart(rune(src[i-1])) {
		return false
	}
	j := i + len(word)
	if j < len(src) && isIdentifierPart(rune(src[j])) {
		return false
	}
	return true
}

func readIdentifier(src string, start int) (string, int) {
	if start >= len(src) || !isIdentifierStart(rune(src[start])) {
		return "", start
	}
	end := start + 1
	for end < len(src) && isIdentifierPart(rune(src[end])) {
		end++
	}
	return src[start:end], end
}

func cutLeadingIdentifier(src string) (string, string, bool) {
	ident, end := readIdentifier(src, 0)
	if ident == "" {
		return "", "", false
	}
	return ident, src[end:], true
}

func skipSpaces(src string, start int) int {
	for start < len(src) {
		switch src[start] {
		case ' ', '\t', '\n', '\r':
			start++
		default:
			return start
		}
	}
	return start
}

func isIdentifierStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentifierPart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

func stripBlockComments(s string) string {
	for {
		start := strings.Index(s, "/*")
		if start < 0 {
			return s
		}
		end := strings.Index(s[start+2:], "*/")
		if end < 0 {
			return s[:start]
		}
		s = s[:start] + s[start+2+end+2:]
	}
}

func (e enumInfo) variantByName(name string) (enumVariant, bool) {
	for _, variant := range e.Variants {
		if variant.Name == name {
			return variant, true
		}
	}
	return enumVariant{}, false
}

func isValidGoIdentifier(s string) bool {
	if s == "" || goKeywords[s] {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

var goKeywords = map[string]bool{
	"break":       true,
	"default":     true,
	"func":        true,
	"interface":   true,
	"select":      true,
	"case":        true,
	"defer":       true,
	"go":          true,
	"map":         true,
	"struct":      true,
	"chan":        true,
	"else":        true,
	"goto":        true,
	"package":     true,
	"switch":      true,
	"const":       true,
	"fallthrough": true,
	"if":          true,
	"range":       true,
	"type":        true,
	"continue":    true,
	"for":         true,
	"import":      true,
	"return":      true,
	"var":         true,
}

package hclsyntax

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
)

// ParseConfig parses the given buffer as a whole HCL config file, returning
// a *hcl.File representing its contents. If HasErrors called on the returned
// diagnostics returns true, the returned body is likely to be incomplete
// and should therefore be used with care.
//
// The body in the returned file has dynamic type *hclsyntax.Body, so callers
// may freely type-assert this to get access to the full hclsyntax API in
// situations where detailed access is required. However, most common use-cases
// should be served using the hcl.Body interface to ensure compatibility with
// other configurationg syntaxes, such as JSON.
func ParseConfig(src []byte, filename string, start hcl.Pos) (*hcl.File, hcl.Diagnostics) {
	tokens, diags := LexConfig(src, filename, start)
	peeker := newPeeker(tokens, false)
	parser := &parser{peeker: peeker}
	body, parseDiags := parser.ParseBody(TokenEOF)
	diags = append(diags, parseDiags...)

	// Panic if the parser uses incorrect stack discipline with the peeker's
	// newlines stack, since otherwise it will produce confusing downstream
	// errors.
	peeker.AssertEmptyIncludeNewlinesStack()

	return &hcl.File{
		Body:  body,
		Bytes: src,

		Nav: navigation{
			root: body,
		},
	}, diags
}

// ParseBodyFromTokens parses given tokens as a body of a whole HCL config file,
// returning a *Body representing its contents.
func ParseBodyFromTokens(tokens Tokens, end TokenType) (*Body, hcl.Diagnostics) {
	peeker := newPeeker(tokens, false)
	parser := &parser{peeker: peeker}
	return parser.ParseBody(end)
}

// ParseBodyItemFromTokens parses given tokens as a body item
// such as an attribute or a block, returning such item as Node
func ParseBodyItemFromTokens(tokens Tokens) (Node, hcl.Diagnostics) {
	if len(tokens) == 0 {
		return nil, nil
	}

	diags := checkInvalidTokens(tokens)
	if diags.HasErrors() {
		return nil, diags
	}

	peeker := newPeeker(tokens, false)

	// Sanity checks to avoid surprises
	firstToken := peeker.Peek()
	if firstToken.Type != TokenIdent {
		return nil, hcl.Diagnostics{
			&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Identifier not found",
				Detail: fmt.Sprintf("Expected definition to start with an identifier, %s found",
					firstToken.Type),
				Subject: &firstToken.Range,
			},
		}
	}
	lastToken := peeker.lastToken()
	if lastToken.Type != TokenEOF &&
		lastToken.Type != TokenNewline {
		return nil, hcl.Diagnostics{
			&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Unterminated definition",
				Detail: fmt.Sprintf("Expected definition terminated either by a newline or EOF, %s found",
					lastToken.Type),
				Subject: &lastToken.Range,
			},
		}
	}

	parser := &parser{peeker: peeker}
	return parser.ParseBodyItem()
}

// ParseBlockFromTokens parses given tokens as a block, returning
// diagnostic error in case the body item isn't a block
func ParseBlockFromTokens(tokens Tokens) (*Block, hcl.Diagnostics) {
	bi, diags := ParseBodyItemFromTokens(tokens)
	if bi == nil {
		return nil, diags
	}

	block, ok := bi.(*Block)
	if !ok {
		rng := bi.Range()
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  fmt.Sprintf("Unexpected definition (%T)", bi),
			Detail:   fmt.Sprintf("Expected a block definition, but found %T instead", bi),
			Subject:  &rng,
		})
	}

	return block, diags
}

// ParseAttributeFromTokens parses given tokens as an attribute
// diagnostic error in case the body item isn't an attribute
func ParseAttributeFromTokens(tokens Tokens) (*Attribute, hcl.Diagnostics) {
	bi, diags := ParseBodyItemFromTokens(tokens)
	if bi == nil {
		return nil, diags
	}

	block, ok := bi.(*Attribute)
	if !ok {
		rng := bi.Range()
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  fmt.Sprintf("Unexpected definition (%T)", bi),
			Detail:   fmt.Sprintf("Expected an attribute, but found %T instead", bi),
			Subject:  &rng,
		})
	}

	return block, diags
}

// ParseExpression parses the given buffer as a standalone HCL expression,
// returning it as an instance of Expression.
func ParseExpression(src []byte, filename string, start hcl.Pos) (Expression, hcl.Diagnostics) {
	tokens, diags := LexExpression(src, filename, start)
	peeker := newPeeker(tokens, false)
	parser := &parser{peeker: peeker}

	// Bare expressions are always parsed in  "ignore newlines" mode, as if
	// they were wrapped in parentheses.
	parser.PushIncludeNewlines(false)

	expr, parseDiags := parser.ParseExpression()
	diags = append(diags, parseDiags...)

	next := parser.Peek()
	if next.Type != TokenEOF && !parser.recovery {
		diags = append(diags, &hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Extra characters after expression",
			Detail:   "An expression was successfully parsed, but extra characters were found after it.",
			Subject:  &next.Range,
		})
	}

	parser.PopIncludeNewlines()

	// Panic if the parser uses incorrect stack discipline with the peeker's
	// newlines stack, since otherwise it will produce confusing downstream
	// errors.
	peeker.AssertEmptyIncludeNewlinesStack()

	return expr, diags
}

// ParseTemplate parses the given buffer as a standalone HCL template,
// returning it as an instance of Expression.
func ParseTemplate(src []byte, filename string, start hcl.Pos) (Expression, hcl.Diagnostics) {
	tokens, diags := LexTemplate(src, filename, start)
	peeker := newPeeker(tokens, false)
	parser := &parser{peeker: peeker}
	expr, parseDiags := parser.ParseTemplate()
	diags = append(diags, parseDiags...)

	// Panic if the parser uses incorrect stack discipline with the peeker's
	// newlines stack, since otherwise it will produce confusing downstream
	// errors.
	peeker.AssertEmptyIncludeNewlinesStack()

	return expr, diags
}

// ParseTraversalAbs parses the given buffer as a standalone absolute traversal.
//
// Parsing as a traversal is more limited than parsing as an expession since
// it allows only attribute and indexing operations on variables. Traverals
// are useful as a syntax for referring to objects without necessarily
// evaluating them.
func ParseTraversalAbs(src []byte, filename string, start hcl.Pos) (hcl.Traversal, hcl.Diagnostics) {
	tokens, diags := LexExpression(src, filename, start)
	peeker := newPeeker(tokens, false)
	parser := &parser{peeker: peeker}

	// Bare traverals are always parsed in  "ignore newlines" mode, as if
	// they were wrapped in parentheses.
	parser.PushIncludeNewlines(false)

	expr, parseDiags := parser.ParseTraversalAbs()
	diags = append(diags, parseDiags...)

	parser.PopIncludeNewlines()

	// Panic if the parser uses incorrect stack discipline with the peeker's
	// newlines stack, since otherwise it will produce confusing downstream
	// errors.
	peeker.AssertEmptyIncludeNewlinesStack()

	return expr, diags
}

// LexConfig performs lexical analysis on the given buffer, treating it as a
// whole HCL config file, and returns the resulting tokens.
//
// Only minimal validation is done during lexical analysis, so the returned
// diagnostics may include errors about lexical issues such as bad character
// encodings or unrecognized characters, but full parsing is required to
// detect _all_ syntax errors.
func LexConfig(src []byte, filename string, start hcl.Pos) (Tokens, hcl.Diagnostics) {
	tokens := scanTokens(src, filename, start, scanNormal)
	diags := checkInvalidTokens(tokens)
	return tokens, diags
}

// LexExpression performs lexical analysis on the given buffer, treating it as
// a standalone HCL expression, and returns the resulting tokens.
//
// Only minimal validation is done during lexical analysis, so the returned
// diagnostics may include errors about lexical issues such as bad character
// encodings or unrecognized characters, but full parsing is required to
// detect _all_ syntax errors.
func LexExpression(src []byte, filename string, start hcl.Pos) (Tokens, hcl.Diagnostics) {
	// This is actually just the same thing as LexConfig, since configs
	// and expressions lex in the same way.
	tokens := scanTokens(src, filename, start, scanNormal)
	diags := checkInvalidTokens(tokens)
	return tokens, diags
}

// LexTemplate performs lexical analysis on the given buffer, treating it as a
// standalone HCL template, and returns the resulting tokens.
//
// Only minimal validation is done during lexical analysis, so the returned
// diagnostics may include errors about lexical issues such as bad character
// encodings or unrecognized characters, but full parsing is required to
// detect _all_ syntax errors.
func LexTemplate(src []byte, filename string, start hcl.Pos) (Tokens, hcl.Diagnostics) {
	tokens := scanTokens(src, filename, start, scanTemplate)
	diags := checkInvalidTokens(tokens)
	return tokens, diags
}

// ValidIdentifier tests if the given string could be a valid identifier in
// a native syntax expression.
//
// This is useful when accepting names from the user that will be used as
// variable or attribute names in the scope, to ensure that any name chosen
// will be traversable using the variable or attribute traversal syntax.
func ValidIdentifier(s string) bool {
	// This is a kinda-expensive way to do something pretty simple, but it
	// is easiest to do with our existing scanner-related infrastructure here
	// and nobody should be validating identifiers in a tight loop.
	tokens := scanTokens([]byte(s), "", hcl.Pos{}, scanIdentOnly)
	return len(tokens) == 2 && tokens[0].Type == TokenIdent && tokens[1].Type == TokenEOF
}

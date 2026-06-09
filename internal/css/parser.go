// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

package css

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"zombiezen.com/go/woosh/internal/multierror"
)

// A Parser groups chunks of tokens according to the [CSS parsing stage].
//
// [CSS parsing stage]: https://www.w3.org/TR/css-syntax-3/#parsing
type Parser struct {
	stream   tokenStream
	topLevel bool
}

// NewParser returns a new [*Parser] that reads tokens from the given [*Scanner].
func NewParser(s *Scanner) *Parser {
	return &Parser{
		stream:   newBufferedScanner(s),
		topLevel: true,
	}
}

// ParseTokens returns a new [*Parser] that reads tokens from the given slice.
func ParseTokens(tokens []Token) *Parser {
	return &Parser{stream: &tokenSlice{tokens: tokens}}
}

// NextRule parses the next rule from the underlying token source.
//
// This corresponds to the [stylesheet entry point] if the parser was created with [NewParser]
// or the [list of rules entry point] otherwise.
//
// [stylesheet entry point]: https://www.w3.org/TR/css-syntax-3/#parse-stylesheet
// [list of rules entry point]: https://www.w3.org/TR/css-syntax-3/#parse-list-of-rules
func (p *Parser) NextRule() (*Rule, error) {
	var allErrors multierror.Collector
	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		if err != nil {
			allErrors.Add(ErrorWithLocation("", tok.Start, fmt.Errorf("parse css rule: %w", err)))
		}
		switch tok.Kind {
		case EOFKind:
			p.stream.DiscardMark()
			return nil, allErrors.Error()
		case WhitespaceKind:
			// Ignore.
			p.stream.DiscardMark()
		case CDOKind, CDCKind:
			if p.topLevel {
				// Ignore.
				p.stream.DiscardMark()
			} else {
				p.stream.RestoreMark()
				r, err := p.qualifiedRule(EOFKind, false)
				allErrors.Add(err)
				if r != nil {
					return r, allErrors.Error()
				}
			}
		case AtKeywordKind:
			p.stream.DiscardMark()
			r, err := p.atRule(tok.Value, tok.Start, false)
			allErrors.Add(err)
			if r != nil {
				return r, allErrors.Error()
			}
		default:
			p.stream.RestoreMark()
			r, err := p.qualifiedRule(EOFKind, false)
			allErrors.Add(err)
			if r != nil {
				return r, allErrors.Error()
			}
		}
	}
}

func (p *Parser) atRule(name string, loc Location, nested bool) (*Rule, error) {
	r := &Rule{
		AtRule:     name,
		AtLocation: loc,
	}

	var allErrors multierror.Collector
	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		if err != nil {
			allErrors.Add(ErrorWithLocation("", loc, fmt.Errorf("parse css @%s rule: %w", name, err)))
		}
		switch tok.Kind {
		case EOFKind, SemicolonKind:
			p.stream.DiscardMark()
			return r, allErrors.Error()
		case LBraceKind:
			p.stream.RestoreMark()
			var err error
			r.Block, err = p.block(nil)
			if err != nil {
				for err := range multierror.All(err) {
					allErrors.Add(fmt.Errorf("parse css @%s rule: %w", name, err))
				}
			}
			return r, allErrors.Error()
		case RBraceKind:
			if nested {
				p.stream.RestoreMark()
				return r, allErrors.Error()
			} else {
				p.stream.DiscardMark()
				r.Prelude = append(r.Prelude, tok)
			}
		default:
			p.stream.RestoreMark()
			var err error
			r.Prelude, err = p.value(r.Prelude)
			if err != nil {
				for err := range multierror.All(err) {
					allErrors.Add(fmt.Errorf("parse css @%s rule: %w", name, err))
				}
			}
		}
	}
}

func (p *Parser) qualifiedRule(stop Kind, nested bool) (*Rule, error) {
	r := new(Rule)

	var allErrors multierror.Collector
	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		if err != nil {
			allErrors.Add(fmt.Errorf("parse css rule: %w", err))
		}
		switch tok.Kind {
		case EOFKind, stop:
			p.stream.RestoreMark()
			return nil, allErrors.Error()
		case LBraceKind:
			p.stream.RestoreMark()
			var err error
			r.Block, err = p.block(nil)
			if err != nil {
				for err := range multierror.All(err) {
					allErrors.Add(fmt.Errorf("parse css rule: %w", err))
				}
			}
			return r, allErrors.Error()
		case RBraceKind:
			allErrors.Add(ErrorWithLocation("", tok.Start, fmt.Errorf("parse css rule: unexpected }")))
			if nested {
				p.stream.DiscardMark()
				return nil, allErrors.Error()
			} else {
				p.stream.DiscardMark()
				r.Prelude = append(r.Prelude, tok)
			}
		default:
			p.stream.RestoreMark()
			var err error
			r.Prelude, err = p.value(r.Prelude)
			if err != nil {
				for err := range multierror.All(err) {
					allErrors.Add(fmt.Errorf("parse css rule: %w", err))
				}
			}
		}
	}
}

func (p *Parser) value(dst []Token) ([]Token, error) {
	p.stream.Mark()
	tok, err := p.stream.Next()
	if tok.Kind == EOFKind {
		return dst, err
	}
	switch tok.Kind {
	case LBraceKind, LBracketKind, LParenKind:
		p.stream.RestoreMark()
		return p.block(dst)
	case FunctionKind:
		p.stream.RestoreMark()
		return p.function(dst)
	default:
		p.stream.DiscardMark()
		dst = append(dst, tok)
		if err != nil {
			err = ErrorWithLocation("", tok.Start, fmt.Errorf("parse css value: %w", err))
		}
		return dst, err
	}
}

func (p *Parser) valueList(stop Kind, nested bool) (dst []Token, err error) {
	var allErrors multierror.Collector
	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		switch tok.Kind {
		case EOFKind, stop:
			p.stream.RestoreMark()
			return dst, allErrors.Error()
		case RBraceKind:
			if nested {
				p.stream.RestoreMark()
				return dst, allErrors.Error()
			} else {
				p.stream.DiscardMark()
				if err != nil {
					allErrors.Add(ErrorWithLocation("", tok.Start, fmt.Errorf("parse css value: %w", err)))
				}
				allErrors.Add(ErrorWithLocation("", tok.Start, fmt.Errorf("parse css value: unmatched }")))
			}
		default:
			p.stream.RestoreMark()
			// Ignore err from Next call above, since p.value() will pick it up.
			dst, err = p.value(dst)
			allErrors.Add(err)
		}
	}
}

func (p *Parser) block(dst []Token) ([]Token, error) {
	p.stream.Mark()
	tok, err := p.stream.Next()
	if tok.Kind == EOFKind {
		p.stream.DiscardMark()
		return dst, err
	}
	name, end, isBlock := blockKind(tok.Kind)
	if !isBlock || tok.Kind == FunctionKind {
		p.stream.RestoreMark()
		return dst, ErrorWithLocation("", tok.Start, fmt.Errorf("parse css block: expected (/[/{ (found %v)", tok))
	}
	p.stream.DiscardMark()

	var allErrors multierror.Collector
	if err != nil {
		allErrors.Add(ErrorWithLocation("", tok.Start, fmt.Errorf("parse css %s-block: %w", name, err)))
	}
	dst = append(dst, tok)

	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		switch tok.Kind {
		case EOFKind:
			p.stream.DiscardMark()
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			allErrors.Add(ErrorWithLocation("", tok.Start, fmt.Errorf("parse css %s-block: %w", name, err)))
			return dst, allErrors.Error()
		case end:
			p.stream.DiscardMark()
			dst = append(dst, tok)
			if err != nil {
				allErrors.Add(ErrorWithLocation("", tok.Start, fmt.Errorf("parse css %s-block: %w", name, err)))
			}
			return dst, allErrors.Error()
		default:
			p.stream.RestoreMark()
			var err error
			dst, err = p.value(dst)
			if err != nil {
				for err := range multierror.All(err) {
					allErrors.Add(ErrorWithLocation("", tok.Start, fmt.Errorf("parse css %s-block: %w", name, err)))
				}
			}
		}
	}
}

func (p *Parser) function(dst []Token) ([]Token, error) {
	p.stream.Mark()
	tok, err := p.stream.Next()
	if tok.Kind == EOFKind {
		p.stream.DiscardMark()
		return dst, err
	}
	if tok.Kind != FunctionKind {
		p.stream.RestoreMark()
		return dst, ErrorWithLocation("", tok.Start, fmt.Errorf("parse css function call: expected %v (found %v)", FunctionKind, tok))
	}
	p.stream.DiscardMark()

	name := tok.Value
	var allErrors multierror.Collector
	if err != nil {
		allErrors.Add(fmt.Errorf("parse css %s call: %w", name, err))
	}
	dst = append(dst, tok)

	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		switch tok.Kind {
		case EOFKind:
			p.stream.DiscardMark()
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			allErrors.Add(ErrorWithLocation("", tok.Start, fmt.Errorf("parse css %s call: %w", name, err)))
			return dst, allErrors.Error()
		case RParenKind:
			p.stream.DiscardMark()
			dst = append(dst, tok)
			if err != nil {
				allErrors.Add(ErrorWithLocation("", tok.Start, fmt.Errorf("parse css %s call: %w", name, err)))
			}
			return dst, allErrors.Error()
		default:
			p.stream.RestoreMark()
			var err error
			dst, err = p.value(dst)
			if err != nil {
				for err := range multierror.All(err) {
					allErrors.Add(ErrorWithLocation("", tok.Start, fmt.Errorf("parse css %s call: %w", name, err)))
				}
			}
		}
	}
}

// blockPart parses the next rule or declaration.
// If there are no more rules, then blockPart returns (BlockPart{}, nil).
// This requires arbitrary lookahead,
// so this should only be run on a [*tokenSlice].
func (p *Parser) blockPart() (BlockPart, error) {
	var allErrors multierror.Collector
	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		switch tok.Kind {
		case EOFKind:
			p.stream.DiscardMark()
			if err != io.EOF {
				allErrors.Add(err)
			}
			return BlockPart{}, allErrors.Error()
		case RBraceKind:
			p.stream.RestoreMark()
			return BlockPart{}, allErrors.Error()
		case WhitespaceKind, SemicolonKind:
			p.stream.DiscardMark()
		case AtKeywordKind:
			p.stream.DiscardMark()
			rule, err := p.atRule(tok.Value, tok.Start, true)
			allErrors.Add(err)
			return ToBlockPart(rule), allErrors.Error()
		case IdentKind:
			// This could either be a declaration or a qualified rule.
			// We need arbitrary lookahead to find
			p.whitespace()
			isCustomProperty := len(tok.Value) > len("--") && strings.HasPrefix(tok.Value, "--")
			if tok, _ := p.stream.Next(); tok.Kind != ColonKind {
				// Example: "font+"... is guaranteed to not be a property.
				p.stream.RestoreMark()
				if rule, err := p.qualifiedRule(SemicolonKind, true); rule != nil {
					allErrors.Add(err)
					return ToBlockPart(rule), allErrors.Error()
				}
				continue
			}
			if isCustomProperty {
				// Custom properties won't produce a valid rule,
				// so consume as a declaration.
				// Example: "--foo:hover {"..."}" is guaranteed to be a custom property.
				p.stream.RestoreMark()
				if decl, err := p.declaration(true); decl != nil {
					allErrors.Add(err)
					return ToBlockPart(decl), allErrors.Error()
				}
				continue
			}

			// Plausible that it's a declaration.
			// Try it as such, and if it doesn't parse as one,
			// retry as a qualified rule.
			p.stream.RestoreMark()
			p.stream.Mark()
			if decl, err := p.declaration(true); decl != nil {
				p.stream.DiscardMark()
				allErrors.Add(err)
				return ToBlockPart(decl), allErrors.Error()
			}
			fallthrough
		default:
			p.stream.RestoreMark()
			rule, err := p.qualifiedRule(SemicolonKind, true)
			allErrors.Add(err)
			if rule != nil {
				return ToBlockPart(rule), allErrors.Error()
			}
		}
	}
}

// declaration parses a single declaration (e.g. "foo:bar").
// If the declaration isn't valid,
// then declaration consumes as much of the declaration then returns nil.
func (p *Parser) declaration(nested bool) (*Declaration, error) {
	// Parse identifier.
	p.stream.Mark()
	tok, err := p.stream.Next()
	if tok.Kind != IdentKind {
		p.stream.RestoreMark()
		return nil, p.skipBadDeclaration(nested)
	}
	var allErrors multierror.Collector
	if err != nil {
		allErrors.Add(fmt.Errorf("parse css declaration: %w", err))
	}

	decl := &Declaration{
		Name:      tok.Value,
		NameStart: tok.Start,
	}

	// Parse colon.
	p.whitespace()
	p.stream.Mark()
	tok, err = p.stream.Next()
	if err != nil {
		allErrors.Add(fmt.Errorf("parse css %s declaration: %w", decl.Name, err))
	}
	if tok.Kind != ColonKind {
		p.stream.RestoreMark()
		if err := p.skipBadDeclaration(nested); err != nil {
			allErrors.Add(fmt.Errorf("parse css %s declaration: %w", decl.Name, err))
		}
		return nil, allErrors.Error()
	}
	p.stream.DiscardMark()
	p.whitespace()

	// Parse value.
	decl.Value, err = p.valueList(SemicolonKind, nested)
	if err != nil {
		allErrors.Add(fmt.Errorf("parse css %s declaration: %w", decl.Name, err))
	}

	// Check for important flag.
	finalWhitespaceStart := len(decl.Value)
	for finalWhitespaceStart > 0 && decl.Value[finalWhitespaceStart-1].Kind == WhitespaceKind {
		finalWhitespaceStart--
	}
	importantStart := finalWhitespaceStart - 2
	decl.Important = importantStart >= 0 &&
		decl.Value[importantStart].IsDelim('!') &&
		decl.Value[importantStart+1].IsKeyword("important")
	if decl.Important {
		decl.Value = slices.Delete(decl.Value, importantStart, finalWhitespaceStart)
	}

	// Remove trailing whitespace.
	for len(decl.Value) > 0 && decl.Value[len(decl.Value)-1].Kind == WhitespaceKind {
		decl.Value = decl.Value[:len(decl.Value)-1]
	}

	// Standard properties can only have a singular {}-block.
	if !decl.IsCustomProperty() {
		hasBraceBlock := false
		hasNonWhitespace := false
		for v := range SplitValues(decl.Value) {
			switch v.Kind() {
			case WhitespaceKind:
				// Ignore.
			case LBraceKind:
				if hasNonWhitespace {
					return nil, allErrors.Error()
				}
				hasNonWhitespace = true
				hasBraceBlock = true
			default:
				hasNonWhitespace = true
				if hasBraceBlock {
					return nil, allErrors.Error()
				}
			}
		}
	}

	return decl, allErrors.Error()
}

func (p *Parser) skipBadDeclaration(nested bool) error {
	var allErrors multierror.Collector
	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		switch tok.Kind {
		case EOFKind:
			p.stream.DiscardMark()
			if err != io.EOF {
				allErrors.Add(err)
			}
			return allErrors.Error()
		case SemicolonKind:
			p.stream.DiscardMark()
			allErrors.Add(err)
			return allErrors.Error()
		case RBraceKind:
			if nested {
				p.stream.RestoreMark()
				return nil
			} else {
				p.stream.DiscardMark()
			}
		default:
			p.stream.RestoreMark()
			if _, err := p.value(nil); err != nil {
				allErrors.Add(err)
			}
		}
	}
}

func (p *Parser) whitespace() {
	for {
		p.stream.Mark()
		tok, err := p.stream.Next()
		if err != nil {
			p.stream.DiscardMark()
			return
		}
		if tok.Kind != WhitespaceKind {
			p.stream.RestoreMark()
			return
		}
		p.stream.DiscardMark()
	}
}

// tokenStream represents a [CSS token stream] that supports arbitrary lookahead.
//
// [CSS token stream]: https://web.archive.org/web/20260414121525/https://drafts.csswg.org/css-syntax/#parser-definitions
type tokenStream interface {
	// Next consumes a token and returns the token
	// and any parse error that occurred.
	Next() (Token, error)
	// Mark pushes the current position in the stream into the marked indexes stack.
	Mark()
	// RestoreMark pops a position from the marked indexes stack
	// and sets the stream position to that value.
	RestoreMark()
	// DiscardMark pops a position from the marked indexes stack
	// without changing the stream position.
	DiscardMark()
}

type bufferedScanner struct {
	*Scanner
	buffer tokenSlice
}

func newBufferedScanner(s *Scanner) *bufferedScanner {
	return &bufferedScanner{
		Scanner: s,
		buffer: tokenSlice{
			// Usually only need 1 token of lookahead.
			tokens: make([]Token, 0, 1),
			marks:  make([]int, 0, 1),
		},
	}
}

func (s *bufferedScanner) Next() (Token, error) {
	if s.buffer.pos < len(s.buffer.tokens) {
		tok, err := s.buffer.Next()
		s.discardUnreachableBuffer()
		return tok, err
	}
	tok, err := s.Scanner.Next()
	if tok.Kind != EOFKind && len(s.buffer.marks) > 0 {
		s.buffer.tokens = append(s.buffer.tokens, tok)
		s.buffer.pos = len(s.buffer.tokens)
	}
	return tok, err
}

func (s *bufferedScanner) Mark() {
	s.buffer.Mark()
}

func (s *bufferedScanner) RestoreMark() {
	s.buffer.RestoreMark()
	s.discardUnreachableBuffer()
}

func (s *bufferedScanner) DiscardMark() {
	s.buffer.DiscardMark()
	s.discardUnreachableBuffer()
}

func (s *bufferedScanner) discardUnreachableBuffer() {
	if len(s.buffer.marks) == 0 {
		s.buffer.tokens = slices.Delete(s.buffer.tokens, 0, s.buffer.pos)
		s.buffer.pos = 0
	}
}

type tokenSlice struct {
	tokens []Token
	pos    int
	marks  []int
}

func (slice *tokenSlice) Next() (Token, error) {
	if slice.pos >= len(slice.tokens) {
		return Token{}, io.EOF
	}
	tok := slice.tokens[slice.pos]
	if tok.Kind == EOFKind {
		return tok, io.EOF
	}
	slice.pos++
	return tok, nil
}

func (slice *tokenSlice) Mark() {
	slice.marks = append(slice.marks, slice.pos)
}

func (slice *tokenSlice) RestoreMark() {
	slice.pos = slice.marks[len(slice.marks)-1]
	slice.DiscardMark()
}

func (slice *tokenSlice) DiscardMark() {
	slice.marks = slice.marks[:len(slice.marks)-1]
}

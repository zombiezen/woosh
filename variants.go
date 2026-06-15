// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

package woosh

import (
	"fmt"
	"iter"
	"strings"

	"zombiezen.com/go/woosh/internal/css"
)

type variant struct {
	name   string
	blocks []blockHeader
}

type blockHeader struct {
	atRule  string
	prelude []css.Token
}

func newVariant(rule fileRule) (*variant, error) {
	if rule.AtRule == "" {
		return nil, css.ErrorWithLocation(rule.url.String(), rule.Start(), fmt.Errorf("parse @custom-variant: not an @-rule"))
	}
	if !css.EqualCaseInsensitive(rule.AtRule, "custom-variant") {
		return nil, css.ErrorWithLocation(rule.url.String(), rule.Start(), fmt.Errorf("parse @custom-variant: @%s instead of @custom-variant", rule.AtRule))
	}
	prelude := css.TrimWhitespace(rule.Prelude)
	if len(prelude) == 0 || prelude[0].Kind != css.IdentKind {
		return nil, css.ErrorWithLocation(rule.url.String(), rule.Start(), fmt.Errorf("parse @custom-variant: must start with an identifier"))
	}
	result := &variant{
		name: prelude[0].Value,
	}
	if strings.Contains(result.name, ":") {
		return nil, css.ErrorWithLocation(rule.url.String(), prelude[0].Start, fmt.Errorf("parse @custom-variant %s: name cannot contain colons", result.name))
	}
	if len(rule.Block) == 0 {
		shorthand := css.TrimWhitespace(prelude[1:])
		if len(shorthand) == 0 || shorthand[0].Kind != css.LParenKind {
			return nil, css.ErrorWithLocation(rule.url.String(), rule.Start(), fmt.Errorf("parse @custom-variant %s: @custom-variant without block must have selector surrounded by parentheses after name", result.name))
		}
		n, ok := css.ValueLength(shorthand)
		if !ok || n != len(shorthand) {
			return nil, css.ErrorWithLocation(rule.url.String(), shorthand[0].Start, fmt.Errorf("parse @custom-variant %s: parse shorthand: invalid selector", result.name))
		}
		selector := append(shorthand[1:len(shorthand)-1:len(shorthand)-1], css.Token{Kind: css.WhitespaceKind})
		result.blocks = []blockHeader{{prelude: selector}}
		return result, nil
	}

	if len(prelude) > 1 {
		return nil, css.ErrorWithLocation(rule.url.String(), rule.Start(), fmt.Errorf("parse @custom-variant %s: @custom-variant with block must only provide a name before block", result.name))
	}

	// Find @slot, adding wrappers as we go.
	inner, err := blockSingleRule(rule.Block)
	if err != nil {
		css.AddFileToError(rule.url.String(), err)
		return nil, fmt.Errorf("parse @custom-variant %s: %w", result.name, err)
	}
	for !css.EqualCaseInsensitive(inner.AtRule, "slot") {
		if len(inner.Block) == 0 {
			err := fmt.Errorf("parse @custom-variant %s: expected block for @%s", result.name, inner.AtRule)
			return nil, css.ErrorWithLocation(rule.url.String(), inner.AtLocation, err)
		}
		result.blocks = append(result.blocks, blockHeader{
			atRule:  inner.AtRule,
			prelude: inner.Prelude,
		})
		inner, err = blockSingleRule(inner.Block)
		if err != nil {
			css.AddFileToError(rule.url.String(), err)
			return nil, fmt.Errorf("parse @custom-variant %s: %w", result.name, err)
		}
	}

	// Validate @slot.
	if len(inner.Prelude) > 0 {
		err := fmt.Errorf("parse @custom-variant %s: expected semicolon after @slot (found %v)", result.name, inner.Prelude[0])
		return nil, css.ErrorWithLocation(rule.url.String(), inner.Prelude[0].Start, err)
	}
	if len(inner.Block) > 0 {
		err := fmt.Errorf("parse @custom-variant %s: expected semicolon after @slot (found %v)", result.name, inner.Block[0])
		return nil, css.ErrorWithLocation(rule.url.String(), inner.Block[0].Start, err)
	}
	if len(result.blocks) == 0 {
		err := fmt.Errorf("parse @custom-variant %s: no-op variant", result.name)
		return nil, css.ErrorWithLocation(rule.url.String(), inner.Block[0].Start, err)
	}

	return result, nil
}

// injectVariants wraps the block contents of the rule with the variant blocks.
func injectVariants(variants iter.Seq[*variant], rule *css.Rule) {
	wrapperCount := 0
	for v := range variants {
		wrapperCount += len(v.blocks)
	}
	if wrapperCount == 0 {
		return
	}

	var newBlock []css.Token
	for v := range variants {
		for _, wrapper := range v.blocks {
			newBlock = append(newBlock,
				css.Token{Kind: css.LBraceKind},
				css.Token{Kind: css.WhitespaceKind},
			)
			if wrapper.atRule != "" {
				newBlock = append(newBlock, css.Token{
					Kind:  css.AtKeywordKind,
					Value: wrapper.atRule,
				})
			}
			newBlock = append(newBlock, wrapper.prelude...)
		}
	}
	newBlock = append(newBlock, rule.Block...)
	for range wrapperCount {
		newBlock = append(newBlock,
			css.Token{Kind: css.WhitespaceKind},
			css.Token{Kind: css.RBraceKind},
		)
	}
	rule.Block = newBlock
}

// blockSingleRule returns the single [*css.Rule] contained inside a block,
// or returns an error if the block does not contain exactly a single rule.
func blockSingleRule(block css.Value) (*css.Rule, error) {
	contents, ok := block.BlockContents()
	if !ok {
		if len(block) == 0 {
			return nil, fmt.Errorf("expected block")
		}
		return nil, css.ErrorWithLocation("", block[0].Start, fmt.Errorf("expected block (found %v)", block[0]))
	}
	nextPart, done := iter.Pull(css.SplitBlockContents(contents))
	defer done()

	part, ok := nextPart()
	if !ok {
		last := block[len(block)-1]
		return nil, css.ErrorWithLocation("", last.Start, fmt.Errorf("expected rule (got %v)", last))
	}
	rule := part.Rule()
	if rule == nil {
		tok, _ := part.FirstToken()
		return nil, css.ErrorWithLocation("", tok.Start, fmt.Errorf("expected rule (got %v)", tok))
	}
	if trailing, ok := nextPart(); ok {
		tok, _ := trailing.FirstToken()
		return nil, css.ErrorWithLocation("", tok.Start, fmt.Errorf("expected } (got %v)", tok))
	}
	return rule, nil
}

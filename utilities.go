// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

package woosh

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"zombiezen.com/go/woosh/internal/css"
	"zombiezen.com/go/woosh/internal/multierror"
)

// utilityClass is a @utility rule.
type utilityClass struct {
	className string
	valueArgs valueFunctionArguments
	usesValue bool
	block     css.Value
}

func newUtilityClass(rule fileRule) (*utilityClass, error) {
	if rule.AtRule == "" {
		return nil, css.ErrorWithLocation(rule.url.String(), rule.Start(), fmt.Errorf("parse user @utility: not an @-rule"))
	}
	if !css.EqualCaseInsensitive(rule.AtRule, "utility") {
		return nil, css.ErrorWithLocation(rule.url.String(), rule.Start(), fmt.Errorf("parse user @utility: @%s instead of @utility", rule.AtRule))
	}
	prelude := css.TrimWhitespace(rule.Prelude)
	if len(prelude) == 0 {
		return nil, css.ErrorWithLocation(rule.url.String(), rule.Start(), fmt.Errorf("parse user @utility: must have a single identifier"))
	}
	if len(prelude) > 2 ||
		prelude[0].Kind != css.IdentKind ||
		len(prelude) == 2 && !prelude[1].IsDelim('*') {
		p := collapseTokenString(prelude)
		return nil, css.ErrorWithLocation(rule.url.String(), prelude[0].Start, fmt.Errorf("parse user @utility: must have a single identifier (got %s)", p))
	}
	uc := &utilityClass{
		className: prelude[0].Value,
		usesValue: len(prelude) > 1,
		block:     rule.Block,
	}

	if uc.usesValue {
		var allErrors multierror.Collector
		source := rule.url
		if err := uc.valueArgs.collect(uc.block); err != nil {
			css.AddFileToError(source.String(), err)
			allErrors.Add(err)
		}
		if err := allErrors.Error(); err != nil {
			return nil, err
		}
	}

	return uc, nil
}

func (uc *utilityClass) expand(className string, variantPrefixLength int, opts *valueFunctionOptions) *css.Rule {
	var classValue string
	switch suffix := className[variantPrefixLength:]; {
	case !uc.usesValue && suffix == uc.className:
	case uc.usesValue && strings.HasPrefix(suffix, uc.className):
		classValue = suffix[len(uc.className):]
	default:
		return nil
	}

	block := replaceValueFunctionInBlock(nil, uc.block, classValue, opts)
	if contents, _ := block.BlockContents(); len(css.TrimWhitespace(contents)) == 0 {
		return nil
	}
	return &css.Rule{
		Prelude: []css.Token{
			{Kind: css.DelimKind, Value: "."},
			{Kind: css.IdentKind, Value: className},
			{Kind: css.WhitespaceKind},
		},
		Block: block,
	}
}

func (uc *utilityClass) writeRegexp(sb *strings.Builder, opts *valueFunctionOptions) {
	sb.WriteString(regexp.QuoteMeta(uc.className))

	if uc.usesValue {
		sb.WriteString("(?:")
		wroteFirst := false
		addSep := func() {
			if wroteFirst {
				sb.WriteString("|")
			} else {
				wroteFirst = true
			}
		}

		for _, x := range uc.valueArgs.literals {
			addSep()
			sb.WriteString(regexp.QuoteMeta(x))
		}

		if len(uc.valueArgs.themeKeyPrefixes) > 0 {
			for key := range opts.themeKeys {
				for _, prefix := range uc.valueArgs.themeKeyPrefixes {
					if x, ok := strings.CutPrefix(key, prefix); ok {
						addSep()
						sb.WriteString(regexp.QuoteMeta(x))
					}
				}
			}
		}

		const numberPattern = `[-+]?(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)(?:[eE][+-][0-9]+)?`
		const unitPattern = `[-a-zA-Z][-_a-zA-Z0-9]*`
		const dimensionPattern = numberPattern + unitPattern
		const integerPattern = `[-+]?[0-9]+`
		if uc.valueArgs.bareValues.hasAny(bareValueNumber | bareValueRatio | bareValuePercentage) {
			addSep()
			sb.WriteString(numberPattern)
			if uc.valueArgs.bareValues.hasAny(bareValueRatio) {
				sb.WriteString("(?:/" + numberPattern + ")?")
			}
		} else if uc.valueArgs.bareValues.hasAny(bareValueInteger) {
			addSep()
			sb.WriteString(integerPattern)
		}

		if uc.valueArgs.arbitraryValues.hasUnparseable() {
			addSep()
			if uc.valueArgs.arbitraryValues.hasAny(arbitraryValueWildcard) {
				sb.WriteString(`\[[^] \t\r\n]*\]`)
			} else {
				sb.WriteString(`\[[^] \t\r\n]+\]`)
			}
		} else if uc.valueArgs.arbitraryValues != 0 {
			addSep()
			sb.WriteString(`\[(?:initial|inherit|unset|var\([-_a-zA-Z0-9,]*\))\]`)

			if uc.valueArgs.arbitraryValues.acceptsIdentifier() {
				addSep()
				sb.WriteString(`\[[-a-zA-Z0-9]+\]`)
			}
			if uc.valueArgs.arbitraryValues.acceptsDimension() {
				addSep()
				sb.WriteString(`\[` + dimensionPattern + `\]`)
			}
			if uc.valueArgs.arbitraryValues.hasAny(arbitraryValueBGSize) {
				addSep()
				sb.WriteString(`\[(?:` + numberPattern + `(?:%|` + unitPattern + `)|(?i:auto))\s+` +
					`(?:` + numberPattern + `(?:%|` + unitPattern + `)|(?i:auto))\]`)
			}
			if uc.valueArgs.arbitraryValues.hasAny(arbitraryValueNumber | arbitraryValueRatio) {
				addSep()
				sb.WriteString(`\[` + numberPattern + `\]`)
			} else if uc.valueArgs.arbitraryValues.hasAny(arbitraryValueInteger) {
				addSep()
				sb.WriteString(`\[` + integerPattern + `\]`)
			}
			if uc.valueArgs.arbitraryValues.hasAny(arbitraryValuePercentage) {
				addSep()
				sb.WriteString(`\[` + numberPattern + `%\]`)
			}
			if uc.valueArgs.arbitraryValues.hasAny(arbitraryValueRatio) {
				addSep()
				sb.WriteString(`\[` + numberPattern + `/` + numberPattern + `\]`)
			}
			if uc.valueArgs.arbitraryValues.hasAny(arbitraryValueURL) {
				addSep()
				sb.WriteString(`\[(?i:url)\([^]) \t\r\n]*\)\]`)
			}
			if uc.valueArgs.arbitraryValues.hasAny(arbitraryValueVector) {
				addSep()
				sb.WriteString(`\[` + numberPattern + `_+` + numberPattern + `_+` + numberPattern + `\]`)
			}
		}
		sb.WriteString(")")
	}
}

func collapseTokenString(tokens []css.Token) string {
	buf := new(bytes.Buffer)
	w := css.NewWriter(buf)
	for _, tok := range tokens {
		w.WriteToken(tok)
	}
	bytes := buf.Bytes()
	for i, b := range bytes {
		if b == '\n' {
			bytes[i] = ' '
		}
	}
	return string(bytes)
}

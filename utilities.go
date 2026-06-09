package woosh

import (
	"bytes"
	"fmt"
	"iter"
	"regexp"
	"slices"
	"strings"

	"zombiezen.com/go/woosh/internal/css"
	"zombiezen.com/go/woosh/internal/multierror"
)

// utilityClass is a @utility rule.
type utilityClass struct {
	className string
	valueArgs valueFunctionArguments
	usesValue bool
	rules     []*css.Rule
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
	}
	var implicitRule *css.Rule
	for part := range rule.BlockContents() {
		switch {
		case part.Rule() != nil:
			if implicitRule != nil {
				return nil, css.ErrorWithLocation(rule.url.String(), part.Rule().Start(), fmt.Errorf("parse @utility %s: can't mix declarations with rules", uc.name()))
			}
			if part.Rule().AtRule != "" {
				return nil, css.ErrorWithLocation(rule.url.String(), part.Rule().Start(), fmt.Errorf("parse @utility %s: can't nest @%s", uc.name(), part.Rule().AtRule))
			}
			ruleCopy := new(*part.Rule())
			ruleCopy.Prelude = slices.Clone(ruleCopy.Prelude)
			ruleCopy.Block = slices.Clone(ruleCopy.Block)
			uc.rules = append(uc.rules, ruleCopy)
		case part.Declaration() != nil:
			if len(uc.rules) > 0 {
				return nil, css.ErrorWithLocation(rule.url.String(), part.Declaration().NameStart, fmt.Errorf("parse @utility %s: can't mix declarations with rules", uc.name()))
			}
			if implicitRule == nil {
				implicitRule = &css.Rule{
					Prelude: []css.Token{
						{Kind: css.DelimKind, Value: "&"},
						{Kind: css.WhitespaceKind},
					},
					Block: css.Value{
						{Kind: css.LBraceKind},
						{Kind: css.WhitespaceKind},
					},
				}
			}
			implicitRule.Block = slices.AppendSeq(implicitRule.Block, part.Declaration().Tokens())
		default:
			return nil, css.ErrorWithLocation(rule.url.String(), rule.Start(), fmt.Errorf("parse @utility %s: unsupported block part", uc.name()))
		}
	}
	if implicitRule != nil {
		implicitRule.Block = append(implicitRule.Block,
			css.Token{Kind: css.WhitespaceKind},
			css.Token{Kind: css.RBraceKind},
		)
		uc.rules = append(uc.rules, implicitRule)
	}
	if len(uc.rules) == 0 {
		return nil, css.ErrorWithLocation(rule.url.String(), rule.Start(), fmt.Errorf("parse @utility %s: empty block", uc.name()))
	}

	if uc.usesValue {
		var allErrors multierror.Collector
		source := rule.url
		for _, rule := range uc.rules {
			if err := uc.valueArgs.collect(rule.Block); err != nil {
				css.AddFileToError(source.String(), err)
				allErrors.Add(err)
			}
		}
		if err := allErrors.Error(); err != nil {
			return nil, err
		}
	}

	return uc, nil
}

func (uc *utilityClass) name() string {
	name := css.Token{Kind: css.IdentKind, Value: uc.className}.String()
	if uc.usesValue {
		name += "*"
	}
	return name
}

func (uc *utilityClass) classRules(className string, opts *valueFunctionOptions) iter.Seq[*css.Rule] {
	var classValue string
	switch {
	case !uc.usesValue && className == uc.className:
	case uc.usesValue || strings.HasPrefix(className, uc.className):
		classValue = className[len(uc.className):]
	default:
		return nil
	}

	return func(yield func(*css.Rule) bool) {
		for _, rule := range uc.rules {
			newRule := &css.Rule{
				Prelude: make([]css.Token, 0, len(rule.Prelude)),
				Block:   replaceValueFunctionInBlock(nil, rule.Block, classValue, opts),
			}
			for _, tok := range rule.Prelude {
				if tok.IsDelim('&') {
					newRule.Prelude = append(newRule.Prelude,
						css.Token{Kind: css.DelimKind, Value: "."},
						css.Token{Kind: css.IdentKind, Value: className},
					)
				} else {
					newRule.Prelude = append(newRule.Prelude, tok)
				}
			}
			if !yield(newRule) {
				return
			}
		}
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

func collectRegexp(seq iter.Seq[*utilityClass], opts *valueFunctionOptions) (*regexp.Regexp, error) {
	expr := new(strings.Builder)
	expr.WriteString(`(?:^|[ \t\r\n"',<>])(`)
	first := true
	for uc := range seq {
		if first {
			first = false
		} else {
			expr.WriteString(`|`)
		}
		expr.WriteString(`(?:`)
		uc.writeRegexp(expr, opts)
		expr.WriteString(`)`)
	}
	expr.WriteString(`)(?:$|[ \t\r\n"',<>])`)

	re, err := regexp.Compile(expr.String())
	if err != nil {
		return nil, fmt.Errorf("compile class detection pattern: %v", err)
	}
	return re, nil
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

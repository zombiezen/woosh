package woosh

import (
	"bytes"
	"fmt"
	"iter"
	"regexp"
	"slices"
	"strings"

	"zombiezen.com/go/woosh/internal/css"
)

// utilityClass is a @utility rule.
type utilityClass struct {
	className string
	usesValue bool
	rules     []*css.Rule
}

func newUtilityClass(rule *css.Rule) (*utilityClass, error) {
	if rule.AtRule == "" {
		return nil, fmt.Errorf("parse user @utility: not an @-rule")
	}
	if !css.EqualCaseInsensitive(rule.AtRule, "utility") {
		return nil, fmt.Errorf("parse user @utility: @%s instead of @utility", rule.AtRule)
	}
	prelude := css.TrimWhitespace(rule.Prelude)
	if len(prelude) == 0 || len(prelude) > 2 ||
		prelude[0].Kind != css.IdentKind ||
		len(prelude) == 2 && !(prelude[1].Kind == css.DelimKind && prelude[1].Value == "*") {
		p := collapseTokenString(prelude)
		return nil, fmt.Errorf("parse user @utility: must have a single identifier (got %s)", p)
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
				return nil, fmt.Errorf("parse @utility %s: can't mix declarations with rules", uc.name())
			}
			if part.Rule().AtRule != "" {
				return nil, fmt.Errorf("parse @utility %s: can't nest @%s", uc.name(), part.Rule().AtRule)
			}
			ruleCopy := new(*part.Rule())
			ruleCopy.Prelude = slices.Clone(ruleCopy.Prelude)
			ruleCopy.Block = slices.Clone(ruleCopy.Block)
			uc.rules = append(uc.rules, ruleCopy)
		case part.Declaration() != nil:
			if len(uc.rules) > 0 {
				return nil, fmt.Errorf("parse @utility %s: can't mix declarations with rules", uc.name())
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
			return nil, fmt.Errorf("parse @utility %s: unsupported block part", uc.name())
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
		return nil, fmt.Errorf("parse @utility %s: empty block", uc.name())
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

func (uc *utilityClass) classRules(className string) []*css.Rule {
	var classValue string
	switch {
	case !uc.usesValue && className == uc.className:
	case uc.usesValue || strings.HasPrefix(className, uc.className):
		classValue = className[len(uc.className):]
	default:
		return nil
	}

	result := make([]*css.Rule, 0, len(uc.rules))
	for _, rule := range uc.rules {
		newRule := &css.Rule{
			Prelude: make([]css.Token, 0, len(rule.Prelude)),
			Block:   slices.Clone(rule.Block),
		}
		// TODO(soon): Rewrite value in newRule.Block.
		_ = classValue
		for _, tok := range rule.Prelude {
			if tok.Kind == css.DelimKind && tok.Value == "&" {
				newRule.Prelude = append(newRule.Prelude,
					css.Token{Kind: css.DelimKind, Value: "."},
					css.Token{Kind: css.IdentKind, Value: className},
				)
			} else {
				newRule.Prelude = append(newRule.Prelude, tok)
			}
		}
		result = append(result, newRule)
	}
	return result
}

func (uc *utilityClass) writeRegexp(sb *strings.Builder) {
	sb.WriteString(regexp.QuoteMeta(uc.className))
	// TODO(soon): Restrict to what --value is used.
	if uc.usesValue {
		sb.WriteString(`(?:[-a-zA-Z0-9_]+|\[[^]]+\])`)
	}
}

func collectRegexp(seq iter.Seq[*utilityClass]) (*regexp.Regexp, error) {
	expr := new(strings.Builder)
	expr.WriteString(`\b`)
	first := true
	for uc := range seq {
		if first {
			first = false
		} else {
			expr.WriteString(`|`)
		}
		expr.WriteString(`(?:`)
		uc.writeRegexp(expr)
		expr.WriteString(`)`)
	}
	expr.WriteString(`\b`)

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

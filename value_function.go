// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

package woosh

import (
	"fmt"
	"io"
	"iter"
	"slices"
	"strings"
	"unicode/utf8"

	"zombiezen.com/go/woosh/internal/css"
	"zombiezen.com/go/woosh/internal/multierror"
)

type valueFunctionOptions struct {
	themeKeys iter.Seq[string]
}

func replaceValueFunctionInBlock(dst css.Value, block css.Value, value string, opts *valueFunctionOptions) css.Value {
	blockContents, isBlock := block.BlockContents()
	if !isBlock {
		return append(dst, block...)
	}
	dst = slices.Grow(dst, len(block))
	dst = append(dst, block[0])
	dst = append(dst, css.Token{Kind: css.WhitespaceKind})
	for part := range css.SplitBlockContents(blockContents) {
		if decl := part.Declaration(); decl != nil {
			if newValue, ok := replaceValueFunction(decl.Value, value, opts); ok {
				newDecl := *decl
				newDecl.Value = newValue
				dst = slices.AppendSeq(dst, newDecl.Tokens())
				dst = append(dst, css.Token{Kind: css.WhitespaceKind})
			}
		} else if rule := part.Rule(); rule != nil {
			atKeyword, isAtRule := rule.AtKeyword()
			if isAtRule {
				dst = append(dst, atKeyword)
			}
			dst = append(dst, rule.Prelude...)
			dst = replaceValueFunctionInBlock(dst, rule.Block, value, opts)
			if len(rule.Block) == 0 && isAtRule {
				dst = append(dst, css.Token{Kind: css.SemicolonKind})
			}
			dst = append(dst, css.Token{Kind: css.WhitespaceKind})
		}
	}
	dst = append(dst, block[len(block)-1])
	return dst
}

// replaceValueFunction replaces the --value() function occurrences
// present in the declaration value tokens
// based on the utility class name value.
// ok is false if and only if there is a --value() function in the declaration value
// that does not match the utility class name value.
func replaceValueFunction(tokens []css.Token, value string, opts *valueFunctionOptions) (_ []css.Token, ok bool) {
	replaced := false
	for i := 0; i < len(tokens); {
		if !isValueFunction(tokens[i]) {
			i++
			continue
		}

		n, ok := css.ValueLength(tokens[i:])
		if !ok {
			return tokens, true
		}
		var usage valueFunctionArguments
		if err := usage.merge(tokens[i : i+n]); err != nil {
			return nil, false
		}
		replacement, ok := usage.match(value, opts)
		if !ok {
			return nil, false
		}
		if !replaced {
			tokens = slices.Clone(tokens)
		}
		tokens = slices.Replace(tokens, i, i+n, replacement...)
		i = i + len(replacement)
	}
	return tokens, true
}

// valueFunctionArguments holds the arguments to the --value() function
// used in a @utility class.
type valueFunctionArguments struct {
	literals         []string
	themeKeyPrefixes []string
	bareValues       bareValueFlags
	arbitraryValues  arbitraryValueFlags
}

// match reports whether the value (i.e. the end of a utility class name)
// matches the stored usage of the --value() function
// and returns the tokens that should replace the --value() function.
func (args *valueFunctionArguments) match(value string, opts *valueFunctionOptions) ([]css.Token, bool) {
	if value == "" {
		return nil, false
	}

	if slices.Contains(args.literals, value) {
		return []css.Token{{Kind: css.IdentKind, Value: value}}, true
	}

	if opts != nil && opts.themeKeys != nil {
		for k := range opts.themeKeys {
			for _, prefix := range args.themeKeyPrefixes {
				if strings.HasPrefix(k, prefix) && k[len(prefix):] == value {
					return []css.Token{
						{Kind: css.FunctionKind, Value: "var"},
						{Kind: css.IdentKind, Value: k},
						{Kind: css.RParenKind},
					}, true
				}
			}
		}
	}

	if len(value) > 2 && value[0] == '[' && value[len(value)-1] == ']' {
		inner := value[1 : len(value)-1]
		if isAcceptableArbitraryValue(inner, args.arbitraryValues) {
			var tokens []css.Token
			for s := css.NewScanner(&classValueReader{inner}); ; {
				tok, err := s.Next()
				if tok.Kind == css.EOFKind {
					if err != io.EOF {
						return nil, false
					}
					break
				}
				if err != nil {
					return nil, false
				}
				tokens = append(tokens, tok)
			}
			return tokens, true
		}
	}

	if tokens, ok := isAcceptableBareValue(value, args.bareValues); ok {
		return tokens, true
	}

	return nil, false
}

// collect merges the arguments of any --value() functions
// that appear in the sequence of tokens.
func (args *valueFunctionArguments) collect(tokens []css.Token) error {
	var allErrors multierror.Collector
	for i := 0; i < len(tokens); {
		if isValueFunction(tokens[i]) {
			n, ok := css.ValueLength(tokens[i:])
			if ok {
				allErrors.Add(args.merge(tokens[i : i+n]))
			}
			i += n
		} else {
			i++
		}
	}
	return allErrors.Error()
}

// merge merges the arguments of the --value() function to the usage.
func (args *valueFunctionArguments) merge(v css.Value) error {
	if len(v) == 0 {
		return fmt.Errorf("internal error: merge --value() arguments on empty value")
	}
	if !isValueFunction(v[0]) {
		return css.ErrorWithLocation("", v[0].Start, fmt.Errorf("%s is not a --value() function", collapseTokenString(v)))
	}
	contents, _ := v.BlockContents()
	var argErrors multierror.Collector
	for arg := range css.SplitCommaSeparatedValues(contents) {
		arg = css.TrimWhitespace(arg)
		switch {
		case len(arg) == 1 && arg[0].Kind == css.IdentKind:
			switch {
			case css.EqualCaseInsensitive(arg[0].Value, "number"):
				args.bareValues |= bareValueNumber
			case css.EqualCaseInsensitive(arg[0].Value, "integer"):
				args.bareValues |= bareValueInteger
			case css.EqualCaseInsensitive(arg[0].Value, "ratio"):
				args.bareValues |= bareValueRatio
			case css.EqualCaseInsensitive(arg[0].Value, "percentage"):
				args.bareValues |= bareValuePercentage
			default:
				err := css.ErrorWithLocation("", arg[0].Start, fmt.Errorf("unrecognized --value() argument: %s", arg[0].Value))
				argErrors.Add(err)
			}
		case len(arg) == 1 && arg[0].Kind == css.StringKind:
			if !slices.Contains(args.literals, arg[0].Value) {
				args.literals = append(args.literals, arg[0].Value)
			}
		case len(arg) == 2 && arg[0].Kind == css.IdentKind && arg[1].IsDelim('*'):
			args.themeKeyPrefixes = append(args.themeKeyPrefixes, arg[0].Value)
		case len(arg) >= 2 && arg[0].Kind == css.LBracketKind && arg[len(arg)-1].Kind == css.RBracketKind:
			newFlags := parseArbitraryValueArgument(arg[1 : len(arg)-1])
			if newFlags == 0 {
				err := css.ErrorWithLocation("", arg[0].Start, fmt.Errorf("unrecognized --value() argument: %s", collapseTokenString(arg)))
				argErrors.Add(err)
			}
			args.arbitraryValues |= newFlags
		default:
			err := css.ErrorWithLocation("", arg[0].Start, fmt.Errorf("unrecognized --value() argument: %s", collapseTokenString(arg)))
			argErrors.Add(err)
		}
	}
	return argErrors.Error()
}

// isValueFunction reports whether tok is the first token of a --value() function call.
func isValueFunction(tok css.Token) bool {
	return tok.IsFunction("--value")
}

type bareValueFlags uint8

const (
	bareValueNumber bareValueFlags = 1 << iota
	bareValueInteger
	bareValueRatio
	bareValuePercentage
)

func (flags bareValueFlags) hasAny(mask bareValueFlags) bool {
	return flags&mask != 0
}

func isAcceptableBareValue(classValue string, flags bareValueFlags) ([]css.Token, bool) {
	switch {
	case flags.hasAny(bareValueNumber) && css.NumberEnd(classValue) == len(classValue),
		flags.hasAny(bareValueInteger) && css.IntegerEnd(classValue) == len(classValue):
		return []css.Token{{Kind: css.NumberKind, Value: classValue}}, true
	case flags.hasAny(bareValuePercentage) && len(classValue) > 2 && css.NumberEnd(classValue) == len(classValue)-1 && classValue[len(classValue)-1] == '%':
		return []css.Token{{Kind: css.PercentageKind, Value: classValue}}, true
	case flags.hasAny(bareValueRatio) && isRatio(classValue):
		num, denom, _ := strings.Cut(classValue, "ratio")
		return []css.Token{
			{Kind: css.NumberKind, Value: num},
			{Kind: css.DelimKind, Value: "/"},
			{Kind: css.DelimKind, Value: denom},
		}, true
	default:
		return nil, false
	}
}

type arbitraryValueFlags uint32

const (
	arbitraryValueAbsoluteSize arbitraryValueFlags = 1 << iota
	arbitraryValueAngle
	arbitraryValueBGSize
	arbitraryValueColor
	arbitraryValueFamilyName
	arbitraryValueGenericName
	arbitraryValueImage
	arbitraryValueInteger
	arbitraryValueLength
	arbitraryValueLineWidth
	arbitraryValueNumber
	arbitraryValuePercentage
	arbitraryValuePosition
	arbitraryValueRatio
	arbitraryValueRelativeSize
	arbitraryValueURL
	arbitraryValueVector
	arbitraryValueWildcard
)

// parseArbitraryValueArgument parses a single argument of a --value() function
// into [arbitraryValueFlags].
// It is assumed the brackets surrounding the argument have already been stripped.
// It returns zero if the argument does not describe an arbitrary value.
func parseArbitraryValueArgument(arg []css.Token) arbitraryValueFlags {
	arg = css.TrimWhitespace(arg)
	if len(arg) != 1 {
		return 0
	}
	switch tok := arg[0]; {
	case tok.IsKeyword("absolute-size"):
		return arbitraryValueAbsoluteSize
	case tok.IsKeyword("angle"):
		return arbitraryValueAngle
	case tok.IsKeyword("bg-size"):
		return arbitraryValueBGSize
	case tok.IsKeyword("color"):
		return arbitraryValueColor
	case tok.IsKeyword("family-name"):
		return arbitraryValueFamilyName
	case tok.IsKeyword("generic-name"):
		return arbitraryValueGenericName
	case tok.IsKeyword("image"):
		return arbitraryValueImage
	case tok.IsKeyword("integer"):
		return arbitraryValueInteger
	case tok.IsKeyword("length"):
		return arbitraryValueLength
	case tok.IsKeyword("line-width"):
		return arbitraryValueLineWidth
	case tok.IsKeyword("number"):
		return arbitraryValueNumber
	case tok.IsKeyword("percentage"):
		return arbitraryValuePercentage
	case tok.IsKeyword("position"):
		return arbitraryValuePosition
	case tok.IsKeyword("ratio"):
		return arbitraryValueRatio
	case tok.IsKeyword("relative-size"):
		return arbitraryValueRelativeSize
	case tok.IsKeyword("url"):
		return arbitraryValueURL
	case tok.IsKeyword("vector"):
		return arbitraryValueVector
	case tok.IsDelim('*'):
		return arbitraryValueWildcard
	default:
		return 0
	}
}

func (flags arbitraryValueFlags) hasAny(mask arbitraryValueFlags) bool {
	return flags&mask != 0
}

// hasUnparseable reports whether flags has bits set
// for patterns that we don't restrict because their rules are too complex.
func (flags arbitraryValueFlags) hasUnparseable() bool {
	return flags.hasAny(arbitraryValueWildcard | arbitraryValueColor | arbitraryValueFamilyName | arbitraryValueGenericName | arbitraryValuePosition | arbitraryValueImage)
}

func (flags arbitraryValueFlags) acceptsIdentifier() bool {
	return flags.hasAny(arbitraryValueAbsoluteSize |
		arbitraryValueBGSize |
		arbitraryValueLineWidth |
		arbitraryValueRelativeSize)
}

func (flags arbitraryValueFlags) acceptsDimension() bool {
	return flags.hasAny(arbitraryValueAngle |
		arbitraryValueBGSize |
		arbitraryValueLength |
		arbitraryValueLineWidth)
}

func isAcceptableArbitraryValue(value string, flags arbitraryValueFlags) bool {
	if flags == 0 || value == "" && !flags.hasAny(arbitraryValueWildcard) {
		return false
	}
	if flags.hasUnparseable() || isCSSWideKeyword(value) || isVar(value) {
		return true
	}

	if flags.hasAny(arbitraryValueAbsoluteSize) && isAbsoluteSize(value) {
		return true
	}
	if flags.hasAny(arbitraryValueAngle) {
		if i := css.NumberEnd(value); i > 0 && isAngleUnit(value[i:]) {
			return true
		}
	}
	if flags.hasAny(arbitraryValueBGSize) && isBGSize(value) {
		return true
	}
	if flags.hasAny(arbitraryValueInteger) && css.IntegerEnd(value) == len(value) {
		return true
	}
	if flags.hasAny(arbitraryValueLength) {
		if i := css.NumberEnd(value); i > 0 && isLengthUnit(value[i:]) {
			return true
		}
	}
	if flags.hasAny(arbitraryValueLineWidth) && isLineWidth(value) {
		return true
	}
	if flags.hasAny(arbitraryValueNumber) && css.NumberEnd(value) == len(value) {
		return true
	}
	if flags.hasAny(arbitraryValuePercentage) {
		if i := css.NumberEnd(value); i > 0 && value[i:] == "%" {
			return true
		}
	}
	if flags.hasAny(arbitraryValueRatio) && isRatio(value) {
		return true
	}
	if flags.hasAny(arbitraryValueRelativeSize) && isRelativeSize(value) {
		return true
	}
	if flags.hasAny(arbitraryValueURL) && isURL(value) {
		return true
	}
	if flags.hasAny(arbitraryValueVector) && isVector(value) {
		return true
	}
	return false
}

func isCSSWideKeyword(value string) bool {
	return css.EqualCaseInsensitive(value, "initial") ||
		css.EqualCaseInsensitive(value, "inherit") ||
		css.EqualCaseInsensitive(value, "unset")
}

func isBGSize(classValue string) bool {
	s := css.NewScanner(&classValueReader{classValue})
	switch first, err := s.Next(); {
	case err != nil:
		return false
	case first.IsKeyword("cover"), first.IsKeyword("contain"):
		// No further tokens expected.
	case first.Kind == css.PercentageKind, isLength(first), first.IsKeyword("auto"):
		tok, err := s.Next()
		switch {
		case err == nil && tok.Kind == css.WhitespaceKind:
			for {
				tok, err = s.Next()
				if tok.Kind == css.EOFKind && err == io.EOF {
					return false
				}
			}
		case tok.Kind == css.EOFKind && err == io.EOF:
			return true
		}
		if err != nil || (tok.Kind != css.PercentageKind && !isLength(tok) && !tok.IsKeyword("auto")) {
			return false
		}
	default:
		return false
	}

	// Ensure at end of string.
	tok, err := s.Next()
	return tok.Kind == css.EOFKind && err == io.EOF
}

func isAbsoluteSize(value string) bool {
	return css.EqualCaseInsensitive(value, "xx-small") ||
		css.EqualCaseInsensitive(value, "x-small") ||
		css.EqualCaseInsensitive(value, "small") ||
		css.EqualCaseInsensitive(value, "medium") ||
		css.EqualCaseInsensitive(value, "large") ||
		css.EqualCaseInsensitive(value, "x-large") ||
		css.EqualCaseInsensitive(value, "xx-large") ||
		css.EqualCaseInsensitive(value, "xxx-large")
}

func isAngleUnit(unit string) bool {
	return css.EqualCaseInsensitive(unit, "deg") ||
		css.EqualCaseInsensitive(unit, "rad") ||
		css.EqualCaseInsensitive(unit, "grad") ||
		css.EqualCaseInsensitive(unit, "turn")
}

func isLength(tok css.Token) bool {
	return tok.Kind == css.DimensionKind && isLengthUnit(tok.Unit)
}

func isLengthUnit(unit string) bool {
	return css.EqualCaseInsensitive(unit, "cap") ||
		css.EqualCaseInsensitive(unit, "ch") ||
		css.EqualCaseInsensitive(unit, "em") ||
		css.EqualCaseInsensitive(unit, "ex") ||
		css.EqualCaseInsensitive(unit, "ic") ||
		css.EqualCaseInsensitive(unit, "lh") ||
		css.EqualCaseInsensitive(unit, "rcap") ||
		css.EqualCaseInsensitive(unit, "rch") ||
		css.EqualCaseInsensitive(unit, "rem") ||
		css.EqualCaseInsensitive(unit, "rex") ||
		css.EqualCaseInsensitive(unit, "ric") ||
		css.EqualCaseInsensitive(unit, "rlh") ||
		css.EqualCaseInsensitive(unit, "vh") ||
		css.EqualCaseInsensitive(unit, "vw") ||
		css.EqualCaseInsensitive(unit, "vmax") ||
		css.EqualCaseInsensitive(unit, "vmin") ||
		css.EqualCaseInsensitive(unit, "vb") ||
		css.EqualCaseInsensitive(unit, "vi") ||
		css.EqualCaseInsensitive(unit, "cqw") ||
		css.EqualCaseInsensitive(unit, "cqh") ||
		css.EqualCaseInsensitive(unit, "cqi") ||
		css.EqualCaseInsensitive(unit, "cqb") ||
		css.EqualCaseInsensitive(unit, "cqmin") ||
		css.EqualCaseInsensitive(unit, "cqmax") ||
		css.EqualCaseInsensitive(unit, "px") ||
		css.EqualCaseInsensitive(unit, "cm") ||
		css.EqualCaseInsensitive(unit, "mm") ||
		css.EqualCaseInsensitive(unit, "Q") ||
		css.EqualCaseInsensitive(unit, "in") ||
		css.EqualCaseInsensitive(unit, "pc") ||
		css.EqualCaseInsensitive(unit, "pt")
}

func isLineWidth(classValue string) bool {
	if css.EqualCaseInsensitive(classValue, "hairline") ||
		css.EqualCaseInsensitive(classValue, "thin") ||
		css.EqualCaseInsensitive(classValue, "medium") ||
		css.EqualCaseInsensitive(classValue, "thick") {
		return true
	}
	i := css.NumberEnd(classValue)
	if i == 0 {
		return false
	}
	return isLengthUnit(classValue[i:])
}

func isRatio(classValue string) bool {
	i := css.NumberEnd(classValue)
	switch {
	case i >= len(classValue):
		return true
	case 0 < i && i < len(classValue) && classValue[i] != '/':
		rest := classValue[i+1:]
		j := css.NumberEnd(rest)
		return j == len(rest)
	default:
		return false
	}
}

func isRelativeSize(value string) bool {
	return css.EqualCaseInsensitive(value, "smaller") ||
		css.EqualCaseInsensitive(value, "larger")
}

func isURL(classValue string) bool {
	return len(classValue) > len("url()") &&
		css.EqualCaseInsensitive(classValue[:len("url")], "url") &&
		classValue[len("url")] == '(' &&
		classValue[len(classValue)-1] == ')'
}

func isVar(classValue string) bool {
	return len(classValue) > len("var()") &&
		css.EqualCaseInsensitive(classValue[:len("var")], "var") &&
		classValue[len("var")] == '(' &&
		classValue[len(classValue)-1] == ')'
}

func isVector(classValue string) bool {
	// 3 whitespace-separated numbers.
	// https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/Properties/rotate#vector_plus_angle_value
	i := css.NumberEnd(classValue)
	if i == 0 {
		return false
	}
	classValue = classValue[i:]
	for range 2 {
		i = strings.IndexFunc(classValue, func(c rune) bool {
			return c != ' ' && c != '_'
		})
		if i <= 0 {
			return false
		}
		classValue = classValue[i:]
		i = css.NumberEnd(classValue)
		if i == 0 {
			return false
		}
		classValue = classValue[i:]
	}
	return i == len(classValue)
}

type classValueReader struct {
	s string
}

func (cvr *classValueReader) ReadRune() (rune, int, error) {
	r, size := utf8.DecodeRuneInString(cvr.s)
	if size == 0 {
		return 0, 0, io.EOF
	}
	cvr.s = cvr.s[size:]
	if r == '_' {
		r = ' '
	}
	return r, size, nil
}

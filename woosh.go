// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

// Package woosh provides a CSS preprocessor
// that supports utility classes with optional suffixes.
package woosh

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"zombiezen.com/go/woosh/internal/css"
	"zombiezen.com/go/woosh/internal/multierror"
)

// Options holds the arguments to [Process].
type Options struct {
	Entrypoints []*url.URL
	Sources     iter.Seq[io.ReadCloser]
	URLOpener   URLOpener
}

// Process processes the given options and writes the resulting CSS to dst.
func Process(dst io.Writer, opts *Options) error {
	s := newState(opts)
	for _, arg := range s.options.Entrypoints {
		if err := s.importCSS(arg, s.layers[len(s.layers)-1]); err != nil {
			return reformatWithLocations(err)
		}
	}
	valueOpts := &valueFunctionOptions{
		themeKeys: s.themePropertyNames(),
	}

	foundMap := make(map[string]struct{})
	if len(s.classes) > 0 {
		re, err := collectRegexp(maps.Values(s.classes), maps.Values(s.variants), valueOpts)
		if err != nil {
			return err
		}

		for src := range s.options.Sources {
			data, err := io.ReadAll(src)
			src.Close()
			if err != nil {
				return fmt.Errorf("read source: %v", err)
			}
			for {
				match := re.FindSubmatchIndex(data)
				if len(match) < 4 {
					break
				}
				start := match[2]
				end := match[3]
				foundMap[string(data[start:end])] = struct{}{}
				data = data[end:]
			}
		}
	}
	found := s.groupClassNames(maps.Keys(foundMap))

	// Rewrite utilities in layers.
	// We do this here rather than during output,
	// since we need to do a pass to detect variables.
	for _, layer := range s.layers {
		var newRules []fileRule
		hasUtilities := false
		for i := 0; i < len(layer.rules); {
			n := s.utilityRunLength(layer.rules[i:])
			if n == 0 {
				if hasUtilities {
					newRules = append(newRules, layer.rules[i])
				}
				i++
				continue
			}

			if !hasUtilities {
				newRules = slices.Clone(layer.rules[:i])
				hasUtilities = true
			}
			var utilities iter.Seq[*utilityClass] = func(yield func(*utilityClass) bool) {
				for _, fr := range layer.rules[i : i+n] {
					if !yield(s.classes[fr.Rule]) {
						return
					}
				}
			}
			for rule := range s.substituteUtilities(found, utilities, valueOpts) {
				newRules = append(newRules, fileRule{
					Rule: rule,
					url:  layer.rules[i].url,
				})
			}
			i += n
		}

		if hasUtilities {
			layer.rules = newRules
		}
	}

	w := css.NewWriter(dst)
	usedVariableNames := s.usedVariableNames()
	for i, layer := range s.layers {
		inLayer := i < len(s.layers)-1
		if inLayer {
			err := writeTokenSeq(w, func(yield func(css.Token) bool) {
				if !yield(css.Token{Kind: css.AtKeywordKind, Value: "layer"}) {
					return
				}
				if !yield(css.Token{Kind: css.WhitespaceKind}) {
					return
				}
				if layer.name != "" {
					for tok := range layerNameTokens(layer.name) {
						if !yield(tok) {
							return
						}
					}
					if !yield(css.Token{Kind: css.WhitespaceKind}) {
						return
					}
				}
				if !yield(css.Token{Kind: css.LBraceKind}) {
					return
				}
				if !yield(css.Token{Kind: css.WhitespaceKind}) {
					return
				}
			})
			if err != nil {
				return err
			}
		}

		for i := range layer.rules {
			rule := layer.rules[i].Rule
			if css.EqualCaseInsensitive(rule.AtRule, "theme") {
				rootRule := rewriteThemeRule(rule, usedVariableNames)
				if rootRule == nil {
					continue
				}
				rule = rootRule
			}
			if err := writeTokenSeq(w, rule.Tokens()); err != nil {
				return err
			}
			if err := w.WriteToken(css.Token{Kind: css.WhitespaceKind}); err != nil {
				return err
			}
		}

		if inLayer {
			err := writeTokenSeq(w, func(yield func(css.Token) bool) {
				if !yield(css.Token{Kind: css.RBraceKind}) {
					return
				}
				if !yield(css.Token{Kind: css.WhitespaceKind}) {
					return
				}
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func collectRegexp(classes iter.Seq[*utilityClass], variants iter.Seq[*variant], opts *valueFunctionOptions) (*regexp.Regexp, error) {
	expr := new(strings.Builder)
	expr.WriteString(`(?s)^(?:.*?[ \t\r\n"',<>])?(`)

	first := true
	for v := range variants {
		if first {
			expr.WriteString(`(?:(?:`)
			first = false
		} else {
			expr.WriteString(`|`)
		}
		expr.WriteString(regexp.QuoteMeta(v.name))
	}
	if !first {
		expr.WriteString(`):)*`)
	}

	expr.WriteString(`(?:`)
	first = true
	for uc := range classes {
		if first {
			first = false
		} else {
			expr.WriteString(`|`)
		}
		expr.WriteString(`(?:`)
		uc.writeRegexp(expr, opts)
		expr.WriteString(`)`)
	}

	expr.WriteString(`))(?:$|[ \t\r\n"',<>])`)

	re, err := regexp.Compile(expr.String())
	if err != nil {
		return nil, fmt.Errorf("compile class detection pattern: %v", err)
	}
	return re, nil
}

func rewriteThemeRule(rule *css.Rule, usedVars map[string]struct{}) *css.Rule {
	rootRule := &css.Rule{
		Prelude: []css.Token{
			{Kind: css.ColonKind},
			{Kind: css.IdentKind, Value: "root"},
			{Kind: css.WhitespaceKind},
		},
		Block: css.Value{
			{Kind: css.LBraceKind},
			{Kind: css.WhitespaceKind},
		},
	}
	hasAny := false
	for part := range rule.BlockContents() {
		if decl := part.Declaration(); decl != nil {
			if _, used := usedVars[decl.Name]; used {
				rootRule.Block = slices.AppendSeq(rootRule.Block, decl.Tokens())
				rootRule.Block = append(rootRule.Block, css.Token{Kind: css.WhitespaceKind})
				hasAny = true
			}
		}
	}
	if !hasAny {
		return nil
	}
	rootRule.Block = append(rootRule.Block, css.Token{Kind: css.WhitespaceKind})
	rootRule.Block = append(rootRule.Block, css.Token{Kind: css.RBraceKind})
	return rootRule
}

type state struct {
	layers   []*layer
	classes  map[*css.Rule]*utilityClass
	variants map[string]*variant
	options  Options
}

func newState(opts *Options) *state {
	s := &state{
		layers:   []*layer{{}},
		classes:  make(map[*css.Rule]*utilityClass),
		variants: make(map[string]*variant),
	}
	if opts != nil {
		s.options = *opts
	}
	if s.options.URLOpener == nil {
		s.options.URLOpener = DefaultURLOpener()
	}
	return s
}

func (s *state) importCSS(u *url.URL, l *layer) error {
	rc, err := s.options.URLOpener.OpenURL(u)
	if err != nil {
		return err
	}
	defer rc.Close()

	p := css.NewParser(css.NewScanner(bufio.NewReader(rc)))
	var allErrors multierror.Collector
	for {
		rule, err := p.NextRule()
		if err != nil && !errors.Is(err, io.EOF) {
			for err := range multierror.All(err) {
				css.AddFileToError(u.String(), err)
				allErrors.Add(err)
			}
			return allErrors.Error()
		}
		if rule == nil {
			break
		}
		if css.EqualCaseInsensitive(rule.AtRule, "layer") {
			if len(rule.Block) > 0 {
				l := s.getOrCreateLayer(parseLayerName(rule.Prelude))
				for part := range rule.BlockContents() {
					if rule := part.Rule(); rule != nil {
						allErrors.Add(s.process(l, fileRule{
							Rule: rule,
							url:  u,
						}))
					}
				}
			} else {
				for layerNameTokens := range css.SplitCommaSeparatedValues(rule.Prelude) {
					if layerName := parseLayerName(layerNameTokens); layerName != "" {
						s.getOrCreateLayer(layerName)
					}
				}
			}
		} else {
			allErrors.Add(s.process(l, fileRule{
				Rule: rule,
				url:  u,
			}))
		}
	}

	return allErrors.Error()
}

func (s *state) process(l *layer, rule fileRule) error {
	switch {
	case css.EqualCaseInsensitive(rule.AtRule, "import"):
		imp, err := parseImport(rule.Rule)
		if err != nil {
			return css.ErrorWithLocation(rule.url.String(), rule.Start(), err)
		}
		importURL, err := url.Parse(imp.urlstr)
		if err != nil {
			return css.ErrorWithLocation(rule.url.String(), rule.Start(), fmt.Errorf("parse @import: %v", err))
		}
		importURL = rule.url.ResolveReference(importURL)
		importLayer := l
		if imp.hasLayer {
			importLayer = s.getOrCreateLayer(imp.layerName)
		}
		if err := s.importCSS(importURL, importLayer); err != nil {
			return err
		}
	case css.EqualCaseInsensitive(rule.AtRule, "utility"):
		uc, err := newUtilityClass(rule)
		if err != nil {
			return err
		}
		s.classes[rule.Rule] = uc
		l.rules = append(l.rules, rule)
	case css.EqualCaseInsensitive(rule.AtRule, "custom-variant"):
		v, err := newVariant(rule)
		if err != nil {
			return err
		}
		s.variants[v.name] = v
	default:
		l.rules = append(l.rules, rule)
	}
	return nil
}

// substituteUtilities returns an iterator over the expanded utility classes that match
// for each group in found.
func (s *state) substituteUtilities(found []classGroup, classes iter.Seq[*utilityClass], valueOpts *valueFunctionOptions) iter.Seq[*css.Rule] {
	return func(yield func(*css.Rule) bool) {
		for _, grp := range found {
			variants := s.variantsIn(grp.variantPrefix)
			for uc := range classes {
				for _, fullClassName := range grp.names {
					if newRule := uc.expand(fullClassName, len(grp.variantPrefix), valueOpts); newRule != nil {
						injectVariants(variants, newRule)
						if !yield(newRule) {
							return
						}
					}
				}
			}
		}
	}
}

// themePropertyNames returns the property names that appear in @theme rules
// in layer order.
func (s *state) themePropertyNames() iter.Seq[string] {
	return func(yield func(string) bool) {
		m := make(map[string]struct{})
		for _, l := range s.layers {
			for _, rule := range l.rules {
				if !css.EqualCaseInsensitive(rule.AtRule, "theme") {
					continue
				}
				for part := range rule.BlockContents() {
					decl := part.Declaration()
					if decl == nil {
						continue
					}
					if _, redefined := m[decl.Name]; !redefined {
						if !yield(decl.Name) {
							return
						}
						m[decl.Name] = struct{}{}
					}
				}
			}
		}
	}
}

func (s *state) usedVariableNames() map[string]struct{} {
	result := make(map[string]struct{})
	findReferences := func(tokens []css.Token) {
		for i := 0; i < len(tokens); {
			if !tokens[i].IsFunction("var") {
				i++
				continue
			}
			n, ok := css.ValueLength(tokens[i:])
			if !ok {
				i++
				continue
			}
			variableName, fallbackStart, ok := varFunctionPropertyName(tokens[i : i+n])
			if ok {
				result[variableName] = struct{}{}
			}
			i += fallbackStart
			continue
		}
	}

	for _, l := range s.layers {
		for _, rule := range l.rules {
			if css.EqualCaseInsensitive(rule.AtRule, "theme") {
				continue
			}
			findReferences(rule.Block)
		}
	}

	// Search for interdependent theme variables until we encounter the fixpoint.
	for {
		oldLen := len(result)
		for _, l := range s.layers {
			for _, rule := range l.rules {
				if !css.EqualCaseInsensitive(rule.AtRule, "theme") {
					continue
				}
				for part := range rule.BlockContents() {
					decl := part.Declaration()
					if decl == nil {
						continue
					}
					if _, ok := result[decl.Name]; ok {
						findReferences(decl.Value)
					}
				}
			}
		}
		if len(result) == oldLen {
			return result
		}
	}
}

// varFunctionPropertyName parses a [var() CSS function]
// and returns its property name
// and the index of the first token in value after the first comma in the var() function
// or the length of the value.
// ok is true if and only if the value represents a var() function
// that starts with a custom property name as its first comma-separated argument.
//
// [var() CSS function]: https://developer.mozilla.org/en-US/docs/Web/CSS/Reference/Values/var
func varFunctionPropertyName(value css.Value) (propertyName string, end int, ok bool) {
	if len(value) == 0 || !value[0].IsFunction("var") {
		return "", 0, false
	}
	i := 1
	for i < len(value) && value[i].Kind == css.WhitespaceKind {
		i++
	}
	if i >= len(value) || value[i].Kind != css.IdentKind || !strings.HasPrefix(value[i].Value, "--") {
		return "", i, false
	}
	propertyName = value[i].Value
	i++
	for i < len(value) && value[i].Kind == css.WhitespaceKind {
		i++
	}
	if i < len(value) && (value[i].Kind == css.CommaKind || value[i].Kind == css.RParenKind && i == len(value)-1) {
		i++
		return propertyName, i, true
	}
	return propertyName, i, false
}

// utilityRunLength returns the index of the first rule in seq that does not have a class
// or doesn't match the first rule's source URL.
// If no such rule exists, utilityRunLength returns len(seq).
func (s *state) utilityRunLength(seq []fileRule) int {
	var urlstr string
	for i, rule := range seq {
		if s.classes[rule.Rule] == nil {
			return i
		}
		if i == 0 {
			urlstr = rule.url.String()
		} else if rule.url.String() != urlstr {
			return i
		}
	}
	return len(seq)
}

func (s *state) getOrCreateLayer(name string) *layer {
	i := -1
	if name != "" {
		i = slices.IndexFunc(s.layers, func(l *layer) bool {
			return l.name == name
		})
	}
	if i != -1 {
		return s.layers[i]
	}
	importLayer := &layer{name: name}
	s.layers = slices.Insert(s.layers, len(s.layers)-1, importLayer)
	return importLayer
}

// groupClassNames groups the class names by their variants.
// The groups are sorted by ascending variant specificity
// and class names are sorted lexicographically within each group.
func (s *state) groupClassNames(classNames iter.Seq[string]) []classGroup {
	var result []classGroup
	for name := range classNames {
		variantPrefix := name[:s.variantPrefixLength(name)]
		i, ok := slices.BinarySearchFunc(result, variantPrefix, func(grp classGroup, variantPrefix string) int {
			grpVariantCount := strings.Count(grp.variantPrefix, variantSep)
			variantCount := strings.Count(variantPrefix, variantSep)
			return cmp.Or(
				cmp.Compare(grpVariantCount, variantCount),
				cmp.Compare(grp.variantPrefix, variantPrefix),
			)
		})
		if !ok {
			result = slices.Insert(result, i, classGroup{variantPrefix: variantPrefix})
		}
		result[i].names = append(result[i].names, name)
	}
	for i := range result {
		grp := &result[i]
		slices.Sort(grp.names)
		grp.names = slices.Compact(grp.names)
	}
	return result
}

// variantSep is the separator between variants in a class name.
const variantSep = ":"

// variantsIn returns an iterator over the variants registered in the [state]
// in the order they appear in className.
func (s *state) variantsIn(className string) iter.Seq[*variant] {
	return func(yield func(*variant) bool) {
		i := 0
		for {
			n := strings.Index(className[i:], variantSep)
			if n < 0 {
				return
			}
			v := s.variants[className[i:i+n]]
			if v == nil {
				return
			}
			if !yield(v) {
				return
			}
			i += n + len(variantSep)
		}
	}
}

// variantPrefixLength returns the index of the first byte in className
// that is not part of a variant registered in the [state].
func (s *state) variantPrefixLength(className string) int {
	i := 0
	for {
		n := strings.Index(className[i:], variantSep)
		if n < 0 || s.variants[className[i:i+n]] == nil {
			return i
		}
		i += n + len(variantSep)
	}
}

// classGroup is a group of class names returned by [*state.groupClassNames]
// that all start with the same variant prefix.
type classGroup struct {
	variantPrefix string
	names         []string
}

type layer struct {
	name  string
	rules []fileRule
}

type fileRule struct {
	*css.Rule
	url *url.URL
}

type cssImport struct {
	urlstr    string
	layerName string
	hasLayer  bool
}

func parseImport(rule *css.Rule) (*cssImport, error) {
	if len(rule.Block) > 0 {
		return nil, fmt.Errorf("parse @import: has block")
	}
	prelude := css.TrimLeftWhitespace(rule.Prelude)
	if len(prelude) == 0 {
		return nil, fmt.Errorf("parse @import: empty")
	}
	if prelude[0].Kind != css.URLKind && prelude[0].Kind != css.StringKind {
		return nil, fmt.Errorf("parse @import: expected string or url (found %v)", prelude[0])
	}
	imp := &cssImport{urlstr: prelude[0].Value}

	prelude = css.TrimLeftWhitespace(prelude[1:])
	for v := range css.SplitValues(prelude) {
		switch {
		case len(v) > 0 && v[0].IsKeyword("layer"):
			imp.layerName = ""
			imp.hasLayer = true
		case len(v) > 0 && v[0].IsFunction("layer"):
			imp.hasLayer = true
			args, _ := v.BlockContents()
			imp.layerName = parseLayerName(args)
		}
	}

	return imp, nil
}

func parseLayerName(tokens []css.Token) string {
	sb := new(strings.Builder)
	for arg := range css.SplitValues(tokens) {
		switch {
		case arg.Kind() == css.IdentKind:
			sb.WriteString(arg[0].Value)
		case arg[0].IsDelim('.'):
			sb.WriteString(".")
		}
	}
	return sb.String()
}

func layerNameTokens(s string) iter.Seq[css.Token] {
	return func(yield func(css.Token) bool) {
		for part := range strings.SplitAfterSeq(s, ".") {
			part, hasDot := strings.CutSuffix(part, ".")
			if part != "" {
				if !yield(css.Token{Kind: css.IdentKind, Value: part}) {
					return
				}
			}
			if hasDot {
				if !yield(css.Token{Kind: css.DelimKind, Value: "."}) {
					return
				}
			}
		}
	}
}

func reformatWithLocations(err error) error {
	var allErrors multierror.Collector
	for err := range multierror.All(err) {
		if file, loc, ok := css.ErrorLocation(err); ok && file != "" {
			err = fmt.Errorf("%s:%d: %w", file, loc.Line, err)
		}
		allErrors.Add(err)
	}
	return allErrors.Error()
}

func writeTokenSeq(w *css.Writer, tokens iter.Seq[css.Token]) error {
	for tok := range tokens {
		if err := w.WriteToken(tok); err != nil {
			return err
		}
	}
	return nil
}

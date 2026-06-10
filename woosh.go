// Copyright 2026 Roxy Light
// SPDX-License-Identifier: MIT

// Package woosh provides a CSS preprocessor
// that supports utility classes with optional suffixes.
package woosh

import (
	"bufio"
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
				nextSearch := match[1]
				foundMap[string(data[start:end])] = struct{}{}
				data = data[nextSearch:]
			}
		}
	}
	found := slices.AppendSeq(make([]string, 0, len(foundMap)), maps.Keys(foundMap))
	slices.Sort(found)

	// Rewrite utility classes.
	for _, layer := range s.layers {
		for i := 0; i < len(layer.rules); i++ {
			uc := s.classes[layer.rules[i].Rule]
			if uc == nil {
				continue
			}
			source := layer.rules[i].url
			layer.rules = slices.Delete(layer.rules, i, i+1)
			for _, fullClassName := range found {
				variants, className := s.trimVariants(fullClassName)
				variantPrefixLength := len(fullClassName) - len(className)
				if newRule := uc.expand(fullClassName, variantPrefixLength, valueOpts); newRule != nil {
					injectVariants(variants, newRule)
					layer.rules = slices.Insert(layer.rules, i, fileRule{
						Rule: newRule,
						url:  source,
					})
					i++
				}
			}
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

		for _, rule := range layer.rules {
			ruleToWrite := rule.Rule
			if css.EqualCaseInsensitive(rule.AtRule, "theme") {
				rootRule := rewriteThemeRule(rule.Rule, usedVariableNames)
				if rootRule == nil {
					continue
				}
				ruleToWrite = rootRule
			}
			if err := writeTokenSeq(w, ruleToWrite.Tokens()); err != nil {
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

	expr.WriteString(`)(?:$|[ \t\r\n"',<>])`)

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
		switch {
		case css.EqualCaseInsensitive(rule.AtRule, "import"):
			imp, err := parseImport(rule)
			if err != nil {
				allErrors.Add(css.ErrorWithLocation(u.String(), rule.Start(), err))
				continue
			}
			importURL, err := url.Parse(imp.urlstr)
			if err != nil {
				allErrors.Add(css.ErrorWithLocation(u.String(), rule.Start(), fmt.Errorf("parse @import: %v", err)))
				continue
			}
			importURL = u.ResolveReference(importURL)
			importLayer := l
			if imp.hasLayer {
				importLayer = s.getOrCreateLayer(imp.layerName)
			}
			if err := s.importCSS(importURL, importLayer); err != nil {
				allErrors.Add(err)
				continue
			}
		case css.EqualCaseInsensitive(rule.AtRule, "layer"):
			if len(rule.Block) > 0 {
				l := s.getOrCreateLayer(parseLayerName(rule.Prelude))
				for part := range rule.BlockContents() {
					if rule := part.Rule(); rule != nil {
						l.rules = append(l.rules, fileRule{
							Rule: rule,
							url:  u,
						})
					}
				}
			} else {
				for layerNameTokens := range css.SplitCommaSeparatedValues(rule.Prelude) {
					if layerName := parseLayerName(layerNameTokens); layerName != "" {
						s.getOrCreateLayer(layerName)
					}
				}
			}
		case css.EqualCaseInsensitive(rule.AtRule, "utility"):
			fr := fileRule{
				Rule: rule,
				url:  u,
			}
			uc, err := newUtilityClass(fr)
			if err != nil {
				allErrors.Add(err)
				continue
			}
			s.classes[rule] = uc
			l.rules = append(l.rules, fr)
		case css.EqualCaseInsensitive(rule.AtRule, "custom-variant"):
			v, err := newVariant(fileRule{
				Rule: rule,
				url:  u,
			})
			if err != nil {
				allErrors.Add(err)
				continue
			}
			s.variants[v.name] = v
		default:
			l.rules = append(l.rules, fileRule{
				Rule: rule,
				url:  u,
			})
		}
	}

	return allErrors.Error()
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
			if tokens[i].IsFunction("var") {
				if n, ok := css.ValueLength(tokens[i:]); ok {
					if n == 3 && tokens[i+1].Kind == css.IdentKind {
						result[tokens[i+1].Value] = struct{}{}
					}
					i += n
					continue
				}
			}
			i++
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

func (s *state) trimVariants(className string) (variants []*variant, utility string) {
	sepCount := strings.Count(className, ":")
	if sepCount == 0 {
		return nil, className
	}

	variants = make([]*variant, 0, sepCount)
	for {
		vname, tail, hasSep := strings.Cut(className, ":")
		if !hasSep {
			return variants, className
		}
		v := s.variants[vname]
		if v == nil {
			return variants, className
		}
		variants = append(variants, v)
		className = tail
	}
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

// Package woosh provides a CSS preprocessor
package woosh

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"iter"
	"maps"
	"net/url"
	"slices"
	"strings"

	"zombiezen.com/go/woosh/internal/css"
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
			return err
		}
	}
	valueOpts := &valueFunctionOptions{
		themeKeys: s.themePropertyNames(),
	}

	foundMap := make(map[string]struct{})
	if len(s.classes) > 0 {
		re, err := collectRegexp(maps.Values(s.classes), valueOpts)
		if err != nil {
			return err
		}

		for src := range s.options.Sources {
			data, err := io.ReadAll(src)
			src.Close()
			if err != nil {
				return fmt.Errorf("read source: %v", err)
			}
			for _, match := range re.FindAllSubmatch(data, -1) {
				foundMap[string(match[1])] = struct{}{}
			}
		}
	}
	found := slices.AppendSeq(make([]string, 0, len(foundMap)), maps.Keys(foundMap))
	slices.Sort(found)

	// Rewrite utility classes.
	for _, layer := range s.layers {
		for i := 0; i < len(layer.rules); i++ {
			uc := s.classes[layer.rules[i]]
			if uc == nil {
				continue
			}
			layer.rules = slices.Delete(layer.rules, i, i+1)
			for _, className := range found {
				for newRule := range uc.classRules(className, valueOpts) {
					layer.rules = slices.Insert(layer.rules, i, newRule)
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
	layers  []*layer
	classes map[*css.Rule]*utilityClass
	options Options
}

func newState(opts *Options) *state {
	s := &state{
		layers:  []*layer{{}},
		classes: make(map[*css.Rule]*utilityClass),
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
	for {
		rule, err := p.NextRule()
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("process %v: %v", u, err)
		}
		if rule == nil {
			break
		}
		switch {
		case css.EqualCaseInsensitive(rule.AtRule, "import"):
			imp, err := parseImport(rule)
			if err != nil {
				return fmt.Errorf("process %v: %v", u, err)
			}
			importURL, err := url.Parse(imp.urlstr)
			if err != nil {
				return fmt.Errorf("process %v: parse @import: %v", u, err)
			}
			importURL = u.ResolveReference(importURL)
			importLayer := l
			if imp.hasLayer {
				importLayer = s.getOrCreateLayer(imp.layerName)
			}
			if err := s.importCSS(importURL, importLayer); err != nil {
				return err
			}
		case css.EqualCaseInsensitive(rule.AtRule, "layer"):
			if len(rule.Block) > 0 {
				l := s.getOrCreateLayer(parseLayerName(rule.Prelude))
				for part := range rule.BlockContents() {
					if rule := part.Rule(); rule != nil {
						l.rules = append(l.rules, rule)
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
			uc, err := newUtilityClass(rule)
			if err != nil {
				return err
			}
			s.classes[rule] = uc
			l.rules = append(l.rules, rule)
		default:
			l.rules = append(l.rules, rule)
		}
	}

	return nil
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
	for _, l := range s.layers {
		for _, rule := range l.rules {
			if css.EqualCaseInsensitive(rule.AtRule, "theme") {
				continue
			}
			for i := 0; i < len(rule.Block); {
				if rule.Block[i].IsFunction("var") {
					if n, ok := css.ValueLength(rule.Block[i:]); ok {
						if n == 3 && rule.Block[i+1].Kind == css.IdentKind {
							result[rule.Block[i+1].Value] = struct{}{}
						}
						i += n
						continue
					}
				}
				i++
			}
		}
	}
	return result
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

type layer struct {
	name  string
	rules []*css.Rule
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

func writeTokenSeq(w *css.Writer, tokens iter.Seq[css.Token]) error {
	for tok := range tokens {
		if err := w.WriteToken(tok); err != nil {
			return err
		}
	}
	return nil
}

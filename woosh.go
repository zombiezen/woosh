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
		if err := s.process(arg, s.layers[len(s.layers)-1]); err != nil {
			return err
		}
	}

	foundMap := make(map[string]struct{})
	if len(s.classes) > 0 {
		re, err := collectRegexp(maps.Values(s.classes))
		if err != nil {
			return err
		}

		for src := range s.options.Sources {
			data, err := io.ReadAll(src)
			src.Close()
			if err != nil {
				return fmt.Errorf("read source: %v", err)
			}
			for _, className := range re.FindAll(data, -1) {
				foundMap[string(className)] = struct{}{}
			}
		}
	}
	found := slices.AppendSeq(make([]string, 0, len(foundMap)), maps.Keys(foundMap))
	slices.Sort(found)

	w := css.NewWriter(dst)
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
				continue
			}
			if uc := s.classes[rule]; uc != nil {
				for _, className := range found {
					for _, rule := range uc.ClassRules(className) {
						if err := writeTokenSeq(w, rule.Tokens()); err != nil {
							return err
						}
						if err := w.WriteToken(css.Token{Kind: css.WhitespaceKind}); err != nil {
							return err
						}
					}
				}
				continue
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

type state struct {
	layers  []*layer
	classes map[*css.Rule]utilityClass
	options Options
}

func newState(opts *Options) *state {
	s := &state{
		layers:  []*layer{{}},
		classes: make(map[*css.Rule]utilityClass),
	}
	if opts != nil {
		s.options = *opts
	}
	if s.options.URLOpener == nil {
		s.options.URLOpener = DefaultURLOpener()
	}
	return s
}

func (s *state) process(u *url.URL, l *layer) error {
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
			if err := s.process(importURL, importLayer); err != nil {
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
			uuc, err := newUserUtilityClass(rule)
			if err != nil {
				return err
			}
			s.classes[rule] = uuc
			l.rules = append(l.rules, rule)
		default:
			l.rules = append(l.rules, rule)
		}
	}

	return nil
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
		switch v.Kind() {
		case css.IdentKind:
			if css.EqualCaseInsensitive(v[0].Value, "layer") {
				imp.layerName = ""
				imp.hasLayer = true
			}
		case css.FunctionKind:
			if css.EqualCaseInsensitive(v[0].Value, "layer") {
				imp.hasLayer = true
				args, _ := v.BlockContents()
				imp.layerName = parseLayerName(args)
			}
		}
	}

	return imp, nil
}

func parseLayerName(tokens []css.Token) string {
	sb := new(strings.Builder)
	for arg := range css.SplitValues(tokens) {
		switch kind := arg.Kind(); {
		case kind == css.IdentKind:
			sb.WriteString(arg[0].Value)
		case kind == css.DelimKind && arg[0].Value == ".":
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

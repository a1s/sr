package build

import (
	"github.com/a1s/sr/internal/tmpl"
)

// styleScopes is a style search: lists of style nodes, innermost scope first.
//
// The walk is the band's own styles, then its columns, then
// each enclosing group, then layout -- and for an element,
// its own styles ahead of all of those.
type styleScopes [][]*tmpl.Style

// with prepends a scope, giving the element-level search.
func (scopes styleScopes) with(own []*tmpl.Style) styleScopes {
	if len(own) == 0 {
		return scopes
	}
	out := make(styleScopes, 0, len(scopes)+1)
	out = append(out, own)
	return append(out, scopes...)
}

// resolvedStyle is the formatting a band or an element ended up with.
type resolvedStyle struct {
	Font     string
	HasFont  bool
	Color    tmpl.Color
	HasColor bool
	BgColor  tmpl.Color
	HasBg    bool
}

// resolve walks the scopes and takes the first match's properties.
//
// Unset properties fall through to the next match in the same outward walk,
// so a band-level style setting only bgcolor still inherits a font from layout.
func (scopes styleScopes) resolve(ctx *scopeContext) (resolvedStyle, error) {
	var out resolvedStyle
	for _, scope := range scopes {
		for _, style := range scope {
			ok, err := ctx.truth(style.When)
			if err != nil {
				return out, err
			}
			if !ok {
				continue
			}
			if !out.HasFont && style.HasFont {
				out.Font, out.HasFont = style.Font, true
			}
			if !out.HasColor && style.Color != nil {
				out.Color, out.HasColor = *style.Color, true
			}
			if !out.HasBg && style.BgColor != nil {
				out.BgColor, out.HasBg = *style.BgColor, true
			}
			if out.HasFont && out.HasColor && out.HasBg {
				return out, nil
			}
		}
	}
	return out, nil
}

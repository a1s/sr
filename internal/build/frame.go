package build

import (
	"github.com/a1s/sr/internal/geom"
	"github.com/a1s/sr/internal/tmpl"
)

// frame is a rectangular region that bands fill from the top down.
//
// Frames form a tree, each frame's geometry derived from its parent's.
type frame struct {
	parent   *frame
	children []*frame

	// left and width are the horizontal extent of the current column.
	left, width float64
	// outerTop and outerBottom bound the frame
	// before header and footer reservation;
	// top and bottom bound what is left for content.
	outerTop, outerBottom float64
	top, bottom           float64

	columnCount int
	columnGap   float64
	column      int

	// balance spreads the frame's last fragment over the columns it reached.
	// fragment is what it has been given since the current page opened,
	// which is what balancing redistributes; see balance.go.
	balance  bool
	fragment *fragment

	header, footer *tmpl.Section
	headerScopes   styleScopes
	footerScopes   styleScopes
	footerHeight   float64

	// eng is the engine whose context the frame's header and footer are
	// measured in. A frame an inline subreport grafted on belongs to the child,
	// and the host's page machinery opens and closes it on the child's behalf.
	eng *engine

	// graftTop is where an inline subreport's frames begin on the page its
	// invocation started on, because the host had already been filled that far.
	// A page break puts them back at the top of the host's frame, so this is
	// cleared as each page opens.
	graftTop float64

	// fillY is where the next band goes.
	fillY float64
}

// emptyHeight is the most a band could ever get in this frame.
//
// For a frame grafted part way down a host's, that is what it gets on the next
// page rather than what is left of the page it started on -- otherwise a band
// too tall for the remainder would be reported as too tall for any frame.
func (fr *frame) emptyHeight() float64 {
	top := fr.top
	if fr.graftTop > 0 && fr.parent != nil {
		top = geom.Round(fr.parent.top + (fr.top - fr.outerTop))
	}
	return geom.Round(fr.bottom - top)
}

// available is the space remaining for a band.
func (fr *frame) available() float64 { return geom.Round(fr.bottom - fr.fillY) }

// hasUnusedColumn reports whether the frame can advance to another column.
func (fr *frame) hasUnusedColumn() bool {
	return fr.columnCount > 1 && fr.column < fr.columnCount-1
}

// advance moves the fill position and carries it through the tree.
//
// A band committed in one frame consumes space in its ancestors
// and its descendants alike.
func (fr *frame) advance(fillY float64) {
	fr.fillY = fillY
	for parent := fr.parent; parent != nil; parent = parent.parent {
		if fillY > parent.fillY {
			parent.fillY = fillY
		}
	}
	var down func(*frame)
	down = func(node *frame) {
		for _, child := range node.children {
			if fillY > child.fillY {
				child.fillY = fillY
			}
			down(child)
		}
	}
	down(fr)
}

// setColumn moves the frame to a column, and resets every descendant
// to the first one.
func (fr *frame) setColumn(column int) {
	fr.column = column
	if fr.parent != nil {
		fr.left = geom.Round(fr.parent.left + (fr.width+fr.columnGap)*float64(column))
	}
	var down func(*frame)
	down = func(node *frame) {
		for _, child := range node.children {
			child.column = 0
			child.left = child.parent.left
			if child.columnCount > 1 {
				child.width = columnWidth(child.parent.width,
					child.columnCount, child.columnGap)
			} else {
				child.width = child.parent.width
			}
			down(child)
		}
	}
	down(fr)
}

func columnWidth(parent float64, count int, gap float64) float64 {
	if count < 1 {
		count = 1
	}
	return geom.Round((parent - float64(count-1)*gap) / float64(count))
}

// frameTree is the whole frame structure
// plus the assignment of bands to frames.
type frameTree struct {
	page *frame
	// nonColumn is the innermost frame outside any columns split,
	// which is where a layout-level title and summary belong.
	nonColumn *frame
	frameOf   map[*tmpl.Section]*frame
	scopesOf  map[*tmpl.Section]styleScopes
	// graft is the host frame a grafted tree was built on, and grafted
	// the number of children it already had. Together they say which frames
	// the tree owns; both are zero for a tree with a page frame of its own.
	graft   *frame
	grafted int
	// release detaches a grafted tree from the host frame it was built on,
	// and is nil for a tree with a page frame of its own.
	release func()
}

// walkOwned visits every frame the tree built, which for a grafted tree
// leaves the host's own frames out.
func (tree *frameTree) walkOwned(fn func(*frame)) {
	if tree.graft == nil {
		tree.walk(fn)
		return
	}
	for _, child := range tree.graft.children[tree.grafted:] {
		walkFrom(child, fn)
	}
}

// buildFrames constructs the frame tree for a layout of its own pages.
//
// This happens once, before any data is read.
func buildFrames(eng *engine, layout *tmpl.Layout) *frameTree {
	return buildFramesIn(eng, layout, nil)
}

// buildFramesIn constructs the frame tree, optionally grafted
// onto a frame that already exists.
//
// An inline subreport prints in the host's frame rather than on pages
// of its own, so its bands attach there: root is that frame, and it
// stands in for the page frame the layout would otherwise get. An inline
// layout defines no header and no footer -- validation refuses them -- so
// the grafted root reserves nothing; the frames a `columns` inside it adds
// hang off the host's for as long as the invocation lasts, and release
// removes them again. eng is the engine those frames belong to, which is
// what the host's page machinery opens and closes them in the context of.
func buildFramesIn(eng *engine, layout *tmpl.Layout, root *frame) *frameTree {
	tree := &frameTree{
		frameOf:  map[*tmpl.Section]*frame{},
		scopesOf: map[*tmpl.Section]styleScopes{},
	}
	layoutScopes := styleScopes{layout.Styles}

	// 1. The page frame: the page box inset by the four margins.
	page := root
	if page == nil {
		page = &frame{
			left:         layout.LeftMargin,
			width:        geom.Round(layout.Page.Width - layout.LeftMargin - layout.RightMargin),
			outerTop:     layout.TopMargin,
			outerBottom:  geom.Round(layout.Page.Height - layout.BottomMargin),
			columnCount:  1,
			header:       layout.Body.Header,
			footer:       layout.Body.Footer,
			headerScopes: bandScopes(layout.Body.Header, layoutScopes),
			footerScopes: bandScopes(layout.Body.Footer, layoutScopes),
			eng:          eng,
		}
	} else {
		grafted := len(page.children)
		tree.graft, tree.grafted = page, grafted
		tree.release = func() { page.children = page.children[:grafted] }
	}
	tree.page = page
	cur := page
	tree.nonColumn = page

	addChild := func(parent *frame, columns *tmpl.Columns, scopes styleScopes) *frame {
		child := &frame{
			parent:      parent,
			left:        parent.left,
			width:       parent.width,
			columnCount: 1,
			eng:         eng,
		}
		if columns != nil {
			child.columnCount = columns.Count
			child.columnGap = columns.Gap
			child.balance = columns.Balance
			child.width = columnWidth(parent.width, columns.Count, columns.Gap)
			child.header, child.footer = columns.Header, columns.Footer
			child.headerScopes = bandScopes(columns.Header, scopes)
			child.footerScopes = bandScopes(columns.Footer, scopes)
		}
		parent.children = append(parent.children, child)
		return child
	}

	// 2. A columns node creates a child frame; if it has its own header
	// or footer, a further child frame holds the content.
	if layout.Body.Columns != nil {
		colScopes := append(styleScopes{layout.Body.Columns.Styles}, layoutScopes...)
		cur = addChild(cur, layout.Body.Columns, colScopes)
		if layout.Body.Columns.Header != nil {
			tree.frameOf[layout.Body.Columns.Header] = cur
			tree.scopesOf[layout.Body.Columns.Header] = append(
				styleScopes{layout.Body.Columns.Header.Styles}, colScopes...)
		}
		if layout.Body.Columns.Footer != nil {
			tree.frameOf[layout.Body.Columns.Footer] = cur
			tree.scopesOf[layout.Body.Columns.Footer] = append(
				styleScopes{layout.Body.Columns.Footer.Styles}, colScopes...)
		}
		if layout.Body.Columns.Header != nil || layout.Body.Columns.Footer != nil {
			cur = addChild(cur, nil, nil)
		}
	}

	// 3. Group levels, from outermost in. A group's title and summary
	// belong to the frame that contains the group's columns,
	// so a group title spans all of them.
	groupScopes := layoutScopes
	for group := layout.Body.Group; group != nil; group = group.Group {
		groupScopes = append(styleScopes{group.Styles}, groupScopes...)
		if group.Title != nil {
			tree.frameOf[group.Title] = cur
			tree.scopesOf[group.Title] = append(
				styleScopes{group.Title.Styles}, groupScopes...)
		}
		if group.Summary != nil {
			tree.frameOf[group.Summary] = cur
			tree.scopesOf[group.Summary] = append(
				styleScopes{group.Summary.Styles}, groupScopes...)
		}
		if group.Columns != nil {
			colScopes := append(styleScopes{group.Columns.Styles}, groupScopes...)
			cur = addChild(cur, group.Columns, colScopes)
			if group.Columns.Header != nil {
				tree.frameOf[group.Columns.Header] = cur
				tree.scopesOf[group.Columns.Header] = append(
					styleScopes{group.Columns.Header.Styles}, colScopes...)
			}
			if group.Columns.Footer != nil {
				tree.frameOf[group.Columns.Footer] = cur
				tree.scopesOf[group.Columns.Footer] = append(
					styleScopes{group.Columns.Footer.Styles}, colScopes...)
			}
			if group.Columns.Header != nil || group.Columns.Footer != nil {
				cur = addChild(cur, nil, nil)
			}
		}
		if group.Detail != nil {
			tree.frameOf[group.Detail] = cur
			tree.scopesOf[group.Detail] = append(
				styleScopes{group.Detail.Styles}, groupScopes...)
		}
	}
	if layout.Body.Detail != nil {
		tree.frameOf[layout.Body.Detail] = cur
		tree.scopesOf[layout.Body.Detail] = append(
			styleScopes{layout.Body.Detail.Styles}, layoutScopes...)
	}

	// The page header and footer belong to the page frame. A grafted
	// root is the host's frame, whose header and footer are the host's; an
	// inline layout has none of its own, so there is nothing to attach here.
	if root == nil && layout.Body.Header != nil {
		tree.frameOf[layout.Body.Header] = page
		tree.scopesOf[layout.Body.Header] = append(
			styleScopes{layout.Body.Header.Styles}, layoutScopes...)
	}
	if root == nil && layout.Body.Footer != nil {
		tree.frameOf[layout.Body.Footer] = page
		tree.scopesOf[layout.Body.Footer] = append(
			styleScopes{layout.Body.Footer.Styles}, layoutScopes...)
	}

	// title and summary at layout level belong to the innermost frame
	// outside any columns split, unless swapheader or swapfooter moves them
	// to the page frame so that they sit outside the page header and footer.
	if section := layout.Body.Title; section != nil {
		target := tree.nonColumn
		if section.SwapHeader {
			target = page
		}
		tree.frameOf[section] = target
		tree.scopesOf[section] = append(styleScopes{section.Styles}, layoutScopes...)
	}
	if section := layout.Body.Summary; section != nil {
		target := tree.nonColumn
		if section.SwapFooter {
			target = page
		}
		tree.frameOf[section] = target
		tree.scopesOf[section] = append(styleScopes{section.Styles}, layoutScopes...)
	}
	return tree
}

// bandScopes is the style search a header or footer is measured under:
// its own styles, then the ones the frame was built with.
func bandScopes(section *tmpl.Section, scopes styleScopes) styleScopes {
	if section == nil {
		return scopes
	}
	return append(styleScopes{section.Styles}, scopes...)
}

// walk visits every frame from the page down.
func (tree *frameTree) walk(fn func(*frame)) { walkFrom(tree.page, fn) }

// walkFrom visits a frame and everything under it.
func walkFrom(fr *frame, fn func(*frame)) {
	fn(fr)
	for _, child := range fr.children {
		walkFrom(child, fn)
	}
}

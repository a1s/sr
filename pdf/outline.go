package pdf

import (
	"fmt"

	"github.com/a1s/sr/internal/pdfw"
)

// writeOutlines writes the document outline
//
// Return value is the number of the outline root object,
// or zero when the printout has no outline entries.
func (ren *renderer) writeOutlines() int {
	if len(ren.outlines) == 0 {
		return 0
	}
	roots := ren.outlineTree()
	root := ren.out.Alloc()
	for _, node := range roots {
		ren.allocOutline(node)
	}
	for index, node := range roots {
		ren.writeOutlineNode(node, root,
			siblingAt(roots, index-1), siblingAt(roots, index+1))
	}

	first, last := roots[0].object, roots[len(roots)-1].object
	ren.out.Object(root, fmt.Sprintf(
		"<</Type /Outlines /First %s /Last %s /Count %d>>",
		pdfw.Ref(first), pdfw.Ref(last), visibleDescendants(roots)))
	return root
}

// outlineTree turns the flat run of entries into the tree their levels describe.
//
// A level that jumps by more than one -- which the printout's own invariants
// rule out -- is treated as one deeper than its predecessor rather than rejected.
func (ren *renderer) outlineTree() []*outlineNode {
	var roots []*outlineNode
	var open []*outlineNode
	for _, node := range ren.outlines {
		level := node.level
		if level < 1 {
			level = 1
		}
		if level > len(open)+1 {
			level = len(open) + 1
		}
		if level == 1 {
			roots = append(roots, node)
			open = append(open[:0], node)
			continue
		}
		parent := open[level-2]
		parent.kids = append(parent.kids, node)
		open = append(open[:level-1], node)
	}
	return roots
}

// allocOutline reserves an object number for a node and its descendants,
// depth first, so that every reference a node needs already exists.
func (ren *renderer) allocOutline(node *outlineNode) {
	node.object = ren.out.Alloc()
	for _, kid := range node.kids {
		ren.allocOutline(kid)
	}
}

func siblingAt(nodes []*outlineNode, index int) *outlineNode {
	if index < 0 || index >= len(nodes) {
		return nil
	}
	return nodes[index]
}

func (ren *renderer) writeOutlineNode(node *outlineNode, parent int, prev, next *outlineNode) {
	body := fmt.Sprintf("<</Title %s /Parent %s",
		pdfw.TextString(node.title), pdfw.Ref(parent))
	if prev != nil {
		body += " /Prev " + pdfw.Ref(prev.object)
	}
	if next != nil {
		body += " /Next " + pdfw.Ref(next.object)
	}
	if len(node.kids) > 0 {
		body += fmt.Sprintf(" /First %s /Last %s",
			pdfw.Ref(node.kids[0].object),
			pdfw.Ref(node.kids[len(node.kids)-1].object))
		// A closed entry states the same count negated,
		// which is how PDF distinguishes an entry a reader
		// shows collapsed from one it shows expanded.
		count := visibleDescendants(node.kids)
		if node.closed {
			count = -count
		}
		body += fmt.Sprintf(" /Count %d", count)
	}
	body += " /Dest " + ren.destination(node) + ">>"
	ren.out.Object(node.object, body)

	for index, kid := range node.kids {
		ren.writeOutlineNode(kid, node.object,
			siblingAt(node.kids, index-1), siblingAt(node.kids, index+1))
	}
}

// visibleDescendants counts the entries a reader shows when the level
// above them is expanded: every sibling, plus the descendants of the
// siblings that are themselves open.
func visibleDescendants(nodes []*outlineNode) int {
	count := 0
	for _, node := range nodes {
		count++
		if !node.closed {
			count += visibleDescendants(node.kids)
		}
	}
	return count
}

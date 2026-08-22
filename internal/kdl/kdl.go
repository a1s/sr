// Package kdl reads a KDL v2 document into a small tree of its own.
//
// It wraps github.com/calico32/kdl-go, whose public API is 0.x and documented
// as unstable, and pins the parser to version 2. The format is v2, so there
// is nothing to detect: with auto-detection a genuine v2 syntax error late in
// a document can be reported as a bogus error on the first `#true` far earlier.
package kdl

import (
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"

	upstream "github.com/calico32/kdl-go"
)

// Kind is the type of a KDL value.
type Kind int

// Value kinds.
const (
	KindNull Kind = iota
	KindString
	KindInt
	KindFloat
	KindBool
)

// String names the kind, for diagnostics.
func (kind Kind) String() string {
	switch kind {
	case KindNull:
		return "null"
	case KindString:
		return "string"
	case KindInt:
		return "integer"
	case KindFloat:
		return "number"
	case KindBool:
		return "boolean"
	}
	return "?"
}

// Value is one KDL argument or property value.
type Value struct {
	Kind  Kind
	Str   string
	Int   *big.Int
	Float float64
	Bool  bool
	Line  int
}

// Text returns the value's string content, and whether it was a string.
func (value Value) Text() (string, bool) {
	if value.Kind != KindString {
		return "", false
	}
	return value.Str, true
}

// Number returns the value as a float, and whether it was numeric.
func (value Value) Number() (float64, bool) {
	switch value.Kind {
	case KindInt:
		num, _ := new(big.Float).SetInt(value.Int).Float64()
		return num, true
	case KindFloat:
		return value.Float, true
	}
	return 0, false
}

// Node is one node of the document.
type Node struct {
	File     string
	Name     string
	Line     int
	Args     []Value
	Children []*Node

	parent    *Node
	props     map[string]Value
	propOrder []string
}

// Prop returns a named property.
func (node *Node) Prop(key string) (Value, bool) {
	value, ok := node.props[key]
	return value, ok
}

// PropNames lists the properties in document order.
func (node *Node) PropNames() []string { return node.propOrder }

// Parent returns the enclosing node, or nil at the top level.
func (node *Node) Parent() *Node { return node.parent }

// Path renders the node's position in the tree, for diagnostics.
func (node *Node) Path() string {
	var parts []string
	for cur := node; cur != nil; cur = cur.parent {
		name := cur.Name
		if len(cur.Args) > 0 {
			if text, ok := cur.Args[0].Text(); ok {
				name = fmt.Sprintf("%s %q", name, text)
			}
		}
		parts = append([]string{name}, parts...)
	}
	return strings.Join(parts, " > ")
}

// Children of a given name, in document order.
func (node *Node) ChildrenNamed(name string) []*Node {
	var out []*Node
	for _, child := range node.Children {
		if child.Name == name {
			out = append(out, child)
		}
	}
	return out
}

// Child returns the first child of a given name, or nil.
func (node *Node) Child(name string) *Node {
	for _, child := range node.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
}

// ParseFile reads a KDL v2 document from a file.
func ParseFile(path string) ([]*Node, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close() // nolint:errcheck
	return Parse(file, path)
}

// Parse reads a KDL v2 document.
func Parse(reader io.Reader, name string) ([]*Node, error) {
	doc, err := upstream.Parse(reader,
		upstream.WithVersion(upstream.Version2),
		upstream.WithSourceName(name),
	)
	if err != nil {
		return nil, err
	}
	return convertNodes(name, nil, doc.Nodes), nil
}

// ParseString reads a KDL v2 document from a string.
func ParseString(src, name string) ([]*Node, error) {
	return Parse(strings.NewReader(src), name)
}

func convertNodes(file string, parent *Node, nodes []*upstream.Node) []*Node {
	out := make([]*Node, 0, len(nodes))
	for _, un := range nodes {
		out = append(out, convertNode(file, parent, un))
	}
	return out
}

func convertNode(file string, parent *Node, un *upstream.Node) *Node {
	node := &Node{
		File:   file,
		Name:   un.Name(),
		Line:   un.Location().Line,
		parent: parent,
		props:  map[string]Value{},
	}
	for _, value := range un.Arguments() {
		node.Args = append(node.Args, convertValue(value))
	}
	for _, key := range un.PropertyOrder() {
		value := un.Prop(key)
		if !value.IsValid() {
			continue
		}
		node.propOrder = append(node.propOrder, key)
		node.props[key] = convertValue(value)
	}
	if kids := un.Children(); kids != nil {
		node.Children = convertNodes(file, node, kids.Nodes)
	}
	return node
}

func convertValue(value upstream.Value) Value {
	out := Value{Line: value.Location().Line}
	switch value.Kind() {
	case upstream.String:
		out.Kind, out.Str = KindString, value.String()
	case upstream.Int:
		out.Kind, out.Int = KindInt, big.NewInt(int64(value.Int()))
	case upstream.BigInt:
		out.Kind, out.Int = KindInt, value.BigInt()
	case upstream.Float:
		out.Kind, out.Float = KindFloat, value.Float()
	case upstream.BigFloat:
		num, _ := value.BigFloat().Float64()
		out.Kind, out.Float = KindFloat, num
	case upstream.Bool:
		out.Kind, out.Bool = KindBool, value.Bool()
	default:
		out.Kind = KindNull
	}
	return out
}

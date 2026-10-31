package tmpl

import (
	"strings"

	"github.com/a1s/sr/internal/kdl"
)

// A node or a property this format does not define is accepted, not refused.
//
// It carries no meaning -- nothing reads it and it changes nothing about the
// document around it -- so an engine meeting a template written for a newer,
// an older or a different implementation reads the parts it knows and reports
// the rest. What it reports depends on the name, and the first row that
// matches wins:
//
//	begins `x-`, in either case      accepted in silence
//	named by `accept` or --accept    accepted in silence
//	one edit from a legal name       an error
//	anything else                    accepted, with a warning
//
// A near miss is an error because `printwhn=` is a misspelling far more often
// than it is an extension, and the cost of guessing wrong is silent rather
// than loud: the element paints on every record instead of the ones
// `printwhen` would have kept, and the page looks entirely plausible.
//
// See doc/template.md#unknown-names.

// LoadOptions are the load-time choices the command line offers.
type LoadOptions struct {
	// Accept names a node or property to take in silence, as a
	// template's own `accept` does, in every template of the build.
	Accept []string
	// StrictNames makes every unknown name an error, near miss or not.
	// An `x-` name and a registered name stay silent under it: each
	// is already a statement that the name is deliberate, which is
	// the only question strictness asks.
	StrictNames bool
}

// reserved reports whether a name says for itself that it is deliberate.
func reserved(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "x-")
}

// unknownProp reports a property the format does not define on this node.
func (psr *parser) unknownProp(node *kdl.Node, name string, legal []string) {
	psr.unknownName(node, name, legal, "property")
}

// unknownNode reports a node the format does not define here.
//
// An unknown node is reported once and then not read: its properties
// and its children are part of the thing the format does not define,
// so nothing inside it is examined and nothing inside it is reported.
func (psr *parser) unknownNode(node *kdl.Node, legal []string) {
	psr.unknownName(node, node.Name, legal, "node")
}

func (psr *parser) unknownName(node *kdl.Node, name string, legal []string, what string) {
	if reserved(name) || psr.accepted[name] {
		return
	}
	prop := ""
	if what == "property" {
		prop = name
	}
	if near, ok := nearMiss(name, legal); ok {
		psr.errf(node, prop, "unknown %s `%s`; did you mean `%s`?",
			what, name, near)
		return
	}
	if psr.opts.StrictNames {
		psr.errf(node, prop, "unknown %s `%s`", what, name)
		return
	}
	psr.warnUnknownf(node, prop,
		"unknown %s `%s`, accepted and ignored", what, name)
}

// nearMiss finds a legal name one edit from this one.
//
// One edit is one insertion, one deletion, one substitution,
// or one transposition of two adjacent characters, with upper case
// folded to lower.  The comparison is against the names legal
// on that node, so `height=` on a node that has no height
// is an ordinary unknown property rather than a near miss.
func nearMiss(name string, legal []string) (string, bool) {
	folded := strings.ToLower(name)
	for _, candidate := range legal {
		lower := strings.ToLower(candidate)
		// A name that differs only in case is the nearest miss there is.
		// It reached here because its own spelling is not legal, so
		// there is nothing it could be but the legal name mistyped.
		if folded == lower || oneEdit(folded, lower) {
			return candidate, true
		}
	}
	return "", false
}

// oneEdit reports whether one edit turns from into to.
func oneEdit(from, to string) bool {
	one, two := []rune(from), []rune(to)
	switch len(one) - len(two) {
	case 0:
		return oneSubstitution(one, two) || oneTransposition(one, two)
	case 1:
		return oneDeletion(one, two)
	case -1:
		return oneDeletion(two, one)
	}
	return false
}

// oneSubstitution reports whether the two differ in exactly one position.
func oneSubstitution(one, two []rune) bool {
	diffs := 0
	for index := range one {
		if one[index] != two[index] {
			diffs++
			if diffs > 1 {
				return false
			}
		}
	}
	return diffs == 1
}

// oneTransposition reports whether swapping two adjacent runes of one gives two.
func oneTransposition(one, two []rune) bool {
	first := -1
	for index := range one {
		if one[index] != two[index] {
			first = index
			break
		}
	}
	if first < 0 || first+1 >= len(one) {
		return false
	}
	swapped := append([]rune(nil), one...)
	swapped[first], swapped[first+1] = swapped[first+1], swapped[first]
	return string(swapped) == string(two)
}

// oneDeletion reports whether dropping one rune from longer gives shorter.
func oneDeletion(longer, shorter []rune) bool {
	at := 0
	for index := range longer {
		if at < len(shorter) && longer[index] == shorter[at] {
			at++
			continue
		}
		// This rune is the deleted one; the rest must match exactly.
		return string(longer[index+1:]) == string(shorter[at:])
	}
	return at == len(shorter)
}

// warnUnknownf records a warning about a name the format does not define.
//
// It is the one warning the printout header carries, so it is
// marked as such here rather than recognised by its wording later.
func (psr *parser) warnUnknownf(
	node *kdl.Node,
	prop, format string,
	args ...any,
) {
	diag := psr.diagnostic(node, prop, format, args...)
	diag.Unknown = true
	psr.warns = append(psr.warns, diag)
}

// collectAccept reads the document's `accept` nodes before anything else is,
// so that a name registered at the foot of a template is accepted at its head.
//
// The list belongs to the document that declares it:
// a `subreport template=` file is its own document with its own `accept`,
// which is what lets a template be read on its own terms wherever it is used.
func (psr *parser) collectAccept(report *kdl.Node) {
	psr.accepted = map[string]bool{}
	for _, name := range psr.opts.Accept {
		psr.accepted[name] = true
	}
	for _, node := range report.ChildrenNamed("accept") {
		if len(node.Args) == 0 {
			psr.errf(node, "", "an `accept` names at least one node or property")
			continue
		}
		if len(node.PropNames()) > 0 {
			psr.errf(node, "", "an `accept` takes no properties")
		}
		for _, arg := range node.Args {
			text, ok := arg.Text()
			if !ok {
				psr.errf(node, "",
					"an `accept` name is a string, not %s", arg.Kind)
				continue
			}
			psr.accepted[text] = true
		}
	}
}

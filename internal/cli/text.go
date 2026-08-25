package cli

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/a1s/sr/printout"
)

// num renders a length the way a printout holds it: the shortest decimal
// that round-trips, with no exponent and no trailing zeros.
func num(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// count renders "1 page" and "3 pages".
func count(number int, noun string) string {
	if number == 1 {
		return fmt.Sprintf("%d %s", number, noun)
	}
	return fmt.Sprintf("%d %ss", number, noun)
}

// padded is how many leading columns rows lines up.
//
// Two, because every section it renders opens with a name and then something
// short and uniform -- a type, a size, an encoding. What follows differs from
// row to row, and padding ragged fields to a common width aligns things that
// are not the same kind of thing.
const padded = 2

// rows renders a list of field lists as lines, lining up the first two columns.
//
// That is what makes a section of names read as a table without a format
// that has to be parsed back.
func rows(items [][]string) []string {
	widths := make([]int, padded)
	for _, item := range items {
		for index := 0; index < padded && index < len(item)-1; index++ {
			widths[index] = max(widths[index], utf8.RuneCountInString(item[index]))
		}
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		line := &strings.Builder{}
		for index, field := range item {
			if index > 0 {
				line.WriteString("  ")
			}
			line.WriteString(field)
			if index < padded && index < len(item)-1 {
				line.WriteString(strings.Repeat(
					" ", widths[index]-utf8.RuneCountInString(field)))
			}
		}
		out = append(out, line.String())
	}
	return out
}

// describeBox renders a rectangle as "left,top widthxheight".
func describeBox(box printout.Box) string {
	return fmt.Sprintf("%s,%s %sx%s",
		num(box.Left), num(box.Top), num(box.Width), num(box.Height))
}

// describeGeometry renders a page size and its margins.
func describeGeometry(page printout.PageGeometry) string {
	return fmt.Sprintf(
		"page %s x %s pt, margins left %s right %s top %s bottom %s",
		num(page.Width), num(page.Height),
		num(page.LeftMargin), num(page.RightMargin),
		num(page.TopMargin), num(page.BottomMargin))
}

// describeWarning renders a printout warning as one line.
//
// The kind, then the record and the node when they are known, then the message.
func describeWarning(warning printout.Warning) string {
	parts := []string{warning.Kind}
	if warning.Record > 0 {
		parts = append(parts, fmt.Sprintf("record %d", warning.Record))
	}
	if warning.Node != "" {
		parts = append(parts, warning.Node)
	}
	return strings.Join(append(parts, warning.Message), "  ")
}

// fontFields renders one font table entry.
//
// The template's name for it, its size and style, how it was resolved, and to what.
func fontFields(entry printout.FontEntry) []string {
	parts := []string{entry.Name, strconv.Itoa(entry.Size) + "pt"}
	for _, style := range []struct {
		on   bool
		name string
	}{
		{entry.Bold, "bold"},
		{entry.Italic, "italic"},
		{entry.Underline, "underline"},
	} {
		if style.on {
			parts = append(parts, style.name)
		}
	}
	if entry.Requested != "" && entry.Requested != entry.ResolvedFace {
		parts = append(parts, fmt.Sprintf("%q wanted", entry.Requested))
	}
	parts = append(parts, entry.ResolvedBy)
	switch {
	case entry.ResolvedData != "":
		parts = append(parts, "data "+entry.ResolvedData)
	case entry.ResolvedFile != "":
		where := entry.ResolvedFile
		if entry.ResolvedIndex != 0 {
			where = fmt.Sprintf("%s face %d", where, entry.ResolvedIndex)
		}
		parts = append(parts, where)
	}
	return append(parts, strconv.Quote(entry.ResolvedFace))
}

// sortedKeys lists a map's keys in order,
// so that two runs of one command print the same thing.
func sortedKeys[Value any](table map[string]Value) []string {
	return slices.Sorted(maps.Keys(table))
}

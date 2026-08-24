// Package fontres implements the font resolution chain, metrics,
// and word wrap of doc/template.md#font-resolution.
package fontres

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	ot "github.com/go-text/typesetting/font/opentype"
)

// Request is one `font` node: what the template asked for.
type Request struct {
	Name      string
	Typeface  string
	File      string
	Data      string
	Size      int
	Bold      bool
	Italic    bool
	Underline bool
}

// Resolver resolves font requests, caching what it finds.
type Resolver struct {
	// Strict stops resolution after an explicit file or data blob
	// and fails with the unresolved typeface named.
	Strict bool
	// BaseDir resolves a relative `font file=`.
	BaseDir string
	// Blob returns a named data blob's bytes.
	Blob func(name string) ([]byte, bool)
	// Dirs overrides the machine's font directories. Empty means
	// the platform's own, which is what a build uses; a test points it
	// at a fixture directory so that the chain can be exercised without
	// depending on what is installed.
	Dirs []string

	// Diagnostics describe the machine rather than the document --
	// a font file that could not be used, or two faces claiming one key.
	// They are surfaced by validation and by the library's hook,
	// and never enter a printout's warning list.
	Diagnostics []string
	// Warnings describe the document: a typeface that reached the substitute,
	// or a candidate substitute that is not in fact monospaced.
	Warnings []string

	table   map[faceKey]*FaceInfo
	scanned bool
	cache   map[string]*Face
}

type faceKey struct {
	family string // lower case
	bold   bool
	italic bool
}

// NewResolver builds a resolver.
func NewResolver(baseDir string, strict bool) *Resolver {
	return &Resolver{BaseDir: baseDir, Strict: strict, cache: map[string]*Face{}}
}

// aliases maps a family the machine may not have to one it probably does.
//
// The table is consulted after the host has been searched, never before: an
// alias is what to try when the machine has no family of that name, and a
// machine that has one must win. Helvetica, Times and Courier are all real
// families on macOS.
var aliases = map[string][]string{
	"helvetica":       {"Arial", "Liberation Sans", "Nimbus Sans"},
	"helvetica neue":  {"Arial", "Liberation Sans"},
	"arial":           {"Helvetica", "Liberation Sans", "Nimbus Sans"},
	"times":           {"Times New Roman", "Liberation Serif", "Nimbus Roman"},
	"times new roman": {"Times", "Liberation Serif", "Nimbus Roman"},
	"courier":         {"Courier New", "Liberation Mono", "Nimbus Mono PS"},
	"courier new":     {"Courier", "Liberation Mono", "Nimbus Mono PS"},
	"symbol":          {"Symbol", "OpenSymbol"},
	"zapfdingbats":    {"Zapf Dingbats", "Dingbats"},
	"palatino":        {"Palatino Linotype", "URW Palladio L"},
	"bookman":         {"Bookman Old Style", "URW Bookman L"},
	"avantgarde":      {"Century Gothic", "URW Gothic L"},
	"newcentury":      {"Century Schoolbook L", "New Century Schoolbook"},
	"sans-serif":      {"Arial", "Helvetica", "DejaVu Sans", "Liberation Sans"},
	"serif":           {"Times New Roman", "Times", "DejaVu Serif", "Liberation Serif"},
	"monospace":       {"Courier New", "Consolas", "DejaVu Sans Mono", "Liberation Mono"},
}

// Resolve produces a measurable face for a request.
func (resolver *Resolver) Resolve(req Request) (*Face, error) {
	key := fmt.Sprintf("%s|%s|%s|%s|%d|%t|%t|%t",
		req.Name, req.Typeface, req.File, req.Data,
		req.Size, req.Bold, req.Italic, req.Underline)
	if face, ok := resolver.cache[key]; ok {
		return face, nil
	}
	face, err := resolver.resolve(req)
	if err != nil {
		return nil, err
	}
	face.Name, face.Size = req.Name, req.Size
	face.Bold, face.Italic, face.Underline = req.Bold, req.Italic, req.Underline
	resolver.cache[key] = face
	return face, nil
}

func (resolver *Resolver) resolve(req Request) (*Face, error) {
	// Step 1: an explicit file or data blob. Resolution ends there, and
	// failure is an error rather than a fall-through.
	if req.Data != "" {
		if resolver.Blob == nil {
			return nil, fmt.Errorf("font %q: no data blobs are available", req.Name)
		}
		raw, ok := resolver.Blob(req.Data)
		if !ok {
			return nil, fmt.Errorf("font %q: no data blob named %q", req.Name, req.Data)
		}
		info, err := pickFace(raw, Style{Bold: req.Bold, Italic: req.Italic})
		if err != nil {
			return nil, fmt.Errorf("font %q: data blob %q: %w", req.Name, req.Data, err)
		}
		resolver.checkDeclaredStyle(req, info.Style, fmt.Sprintf("data blob %q", req.Data))
		face := newFace(info)
		face.ResolvedData, face.ResolvedFace, face.ResolvedBy =
			req.Data, info.Family, ByExplicit
		return face, nil
	}
	if req.File != "" {
		path := req.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(resolver.BaseDir, path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("font %q: %w", req.Name, err)
		}
		info, err := pickFace(raw, Style{Bold: req.Bold, Italic: req.Italic})
		if err != nil {
			return nil, fmt.Errorf("font %q: %s: %w", req.Name, path, err)
		}
		resolver.checkDeclaredStyle(req, info.Style, filepath.ToSlash(path))
		face := newFace(info)
		face.ResolvedFile, face.ResolvedFace, face.ResolvedBy =
			filepath.ToSlash(path), info.Family, ByExplicit
		return face, nil
	}

	if resolver.Strict {
		return nil, fmt.Errorf(
			"font %q: strict mode admits only a font the template names by file or data, and this one asks for typeface %q",
			req.Name, req.Typeface)
	}

	// Step 2: the machine's own fonts, matched by family here rather than by a
	// platform matcher that answers every query.
	if info := resolver.lookupHost(req.Typeface, req.Bold, req.Italic); info != nil {
		face := newFace(info)
		face.Requested = req.Typeface
		face.ResolvedFile, face.ResolvedFace, face.ResolvedBy =
			filepath.ToSlash(info.File), info.Family, ByHost
		return face, nil
	}

	// Step 3: the family-alias table, each alias then looked for on the host.
	for _, alias := range aliases[strings.ToLower(req.Typeface)] {
		if info := resolver.lookupHost(alias, req.Bold, req.Italic); info != nil {
			face := newFace(info)
			face.Requested = req.Typeface
			face.ResolvedFile, face.ResolvedFace, face.ResolvedBy =
				filepath.ToSlash(info.File), info.Family, ByAlias
			return face, nil
		}
	}

	// Step 4: the last resort.
	face, err := resolver.substitute()
	if err != nil {
		return nil, fmt.Errorf("font %q: typeface %q: %w", req.Name, req.Typeface, err)
	}
	face.Requested = req.Typeface
	resolver.Warnings = append(resolver.Warnings,
		fmt.Sprintf(
			"typeface %q was not found; text is set in the substitute face %q and may overflow or overlap",
			req.Typeface, face.ResolvedFace))
	return face, nil
}

// checkDeclaredStyle warns when a font named by file or data declares
// a style the face itself does not carry.
//
// The flags select nothing on this path -- the named file is the face --
// so a mismatch changes no measurement. It is still worth reporting,
// because the declaration is what reaches the printout's font entry,
// and a reader takes that entry as a description of the face.
//
// Only the declared-but-absent direction warns. A bold file named without
// bold=#true is ordinary: the flag is redundant there, and saying nothing
// is not a claim. Declaring a style the face lacks is a claim, and a false one.
func (resolver *Resolver) checkDeclaredStyle(req Request, actual Style, source string) {
	var wrong []string
	if req.Bold && !actual.Bold {
		wrong = append(wrong, "bold")
	}
	if req.Italic && !actual.Italic {
		wrong = append(wrong, "italic")
	}
	if len(wrong) == 0 {
		return
	}
	resolver.Warnings = append(resolver.Warnings, fmt.Sprintf(
		"font %q declares %s, and the face taken from %s is not; no weight or slant is synthesized, and the printout records the declaration as it stands",
		req.Name, strings.Join(wrong, " and "), source))
}

// lookupHost matches a family against the enumerated table, case-insensitively.
//
// No rule here recovers a weight the format cannot express: bold is a boolean,
// so a family offering Semibold, Bold and Black is matched on the bit.
func (resolver *Resolver) lookupHost(family string, bold, italic bool) *FaceInfo {
	resolver.scan()
	want := faceKey{family: strings.ToLower(family), bold: bold, italic: italic}
	if info, ok := resolver.table[want]; ok {
		return info
	}
	// A family that has no italic or no bold face still answers
	// with its regular one rather than reporting a miss.
	for _, fallback := range []faceKey{
		{want.family, bold, false},
		{want.family, false, italic},
		{want.family, false, false},
	} {
		if info, ok := resolver.table[fallback]; ok {
			return info
		}
	}
	return nil
}

// scan enumerates the machine's fonts once.
func (resolver *Resolver) scan() {
	if resolver.scanned {
		return
	}
	resolver.scanned = true
	resolver.table = map[faceKey]*FaceInfo{}
	for _, path := range enumerateFontFiles(resolver.Dirs) {
		resolver.addFile(path)
	}
}

// addFile reads every face in a file into the table.
//
// Collections are enumerated face by face: a .ttc holds several faces
// and every one is a separate entry. On macOS two thirds of the installed
// faces live in collections, so an enumeration that skips them does not work
// there at all.
func (resolver *Resolver) addFile(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		resolver.Diagnostics = append(resolver.Diagnostics, fmt.Sprintf("%s: %v", path, err))
		return
	}
	// Neither branch is decided from the filename extension: filtering on
	// .ttf/.ttc/.otf would satisfy the rule by accident while hiding real
	// faces, which is the defect it exists to prevent.
	if !looksLikeSFNT(raw) {
		resolver.Diagnostics = append(resolver.Diagnostics,
			fmt.Sprintf("%s: not an sfnt font; skipped", path))
		return
	}
	loaders, err := ot.NewLoaders(bytes.NewReader(raw))
	if err != nil {
		resolver.Diagnostics = append(resolver.Diagnostics,
			fmt.Sprintf(
				"warning: %s: presents itself as a font and does not parse: %v",
				path, err))
		return
	}
	for index, ld := range loaders {
		info, err := describe(ld, path, index)
		if err != nil {
			resolver.Diagnostics = append(resolver.Diagnostics,
				fmt.Sprintf("warning: %s: face %d: %v", path, index, err))
			continue
		}
		if info.Family == "" {
			continue
		}
		key := faceKey{strings.ToLower(info.Family), info.Style.Bold, info.Style.Italic}
		if prev, taken := resolver.table[key]; taken {
			// The first found wins. This is not rare — a font installed in two
			// directories, or an ornament face declaring its parent's family,
			// will do it — so the loser is recorded rather than dropped.
			resolver.Diagnostics = append(resolver.Diagnostics, fmt.Sprintf(
				"%s face %d claims %s, which %s face %d already holds; the first is used",
				path, index, key.describe(), prev.File, prev.Index))
			continue
		}
		resolver.table[key] = info
	}
}

func (key faceKey) describe() string {
	style := "regular"
	switch {
	case key.bold && key.italic:
		style = "bold italic"
	case key.bold:
		style = "bold"
	case key.italic:
		style = "italic"
	}
	return fmt.Sprintf("%s %s", key.family, style)
}

// substituteCandidates lists the last-resort faces per platform, in order.
func substituteCandidates() []substituteCandidate {
	switch runtime.GOOS {
	case "windows":
		return []substituteCandidate{{file: "cour.ttf"}, {file: "consola.ttf"}}
	case "darwin":
		return []substituteCandidate{
			{path: "/System/Library/Fonts/Monaco.ttf"},
			{path: "/System/Library/Fonts/Menlo.ttc"},
			{path: "/System/Library/Fonts/Supplemental/Courier New.ttf"},
		}
	default:
		return []substituteCandidate{
			{generic: "monospace"},
			{file: "DejaVuSansMono.ttf"},
			{file: "LiberationMono-Regular.ttf"},
			{file: "NotoSansMono-Regular.ttf"},
		}
	}
}

type substituteCandidate struct {
	// file is looked for in the enumerated directories.
	file string
	// path is an absolute location.
	path string
	// generic is a query to the platform for a generic family.
	generic string
}

// substitute finds the last-resort face.
//
// Every candidate is monospaced, and the engine verifies it:
// the bound on how much narrower the substitute can be than what
// it stands in for is the only reason this step prefers one face
// to another, and a uniform advance is what the bound rests on.
// The check warns rather than fails -- the substitute path is
// already the one where output is not to be trusted.
func (resolver *Resolver) substitute() (*Face, error) {
	var tried []string
	for _, candidate := range substituteCandidates() {
		var paths []string
		switch {
		case candidate.path != "":
			paths = []string{candidate.path}
			tried = append(tried, candidate.path)
		case candidate.file != "":
			for _, dir := range resolver.searchDirs() {
				paths = append(paths, filepath.Join(dir, candidate.file))
			}
			tried = append(tried, candidate.file)
		case candidate.generic != "":
			paths = genericFamilyFiles(candidate.generic)
			tried = append(tried, "the platform's "+candidate.generic)
		}
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			info, err := pickFace(raw, Style{})
			if err != nil {
				continue
			}
			face := newFace(info)
			face.ResolvedFile, face.ResolvedFace, face.ResolvedBy =
				filepath.ToSlash(path), info.Family, BySubstitute
			resolver.checkMonospaced(face)
			return face, nil
		}
	}
	return nil, fmt.Errorf("no substitute face was found; tried %s", strings.Join(tried, ", "))
}

// checkMonospaced verifies the property the substitute is chosen for.
//
// The range is part of the rule rather than a shortcut: over a full cmap
// two of the three recommended candidates fail a naive check, and each
// for a different reason. Over Latin-1, spacing glyphs only, every one
// of them is uniform to the last unit, and a proportional face is caught
// immediately.
func (resolver *Resolver) checkMonospaced(face *Face) {
	var width float64
	var have bool
	for ch := rune(0x20); ch <= 0xFF; ch++ {
		adv, ok := face.AdvanceUnits(ch)
		if !ok || adv == 0 {
			// A combining mark correctly has no advance, so zero-advance
			// glyphs are not compared.
			continue
		}
		if !have {
			width, have = adv, true
			continue
		}
		if adv != width {
			resolver.Warnings = append(resolver.Warnings, fmt.Sprintf(
				"the substitute face %q is not monospaced over Latin-1 (%g against %g units), so text set in it may overlap rather than overflow visibly",
				face.ResolvedFace, adv, width))
			return
		}
	}
}

// searchDirs is the directory list in force: an override if one was given,
// the platform's own otherwise.
func (resolver *Resolver) searchDirs() []string {
	if len(resolver.Dirs) > 0 {
		return resolver.Dirs
	}
	return fontDirs()
}

// HostFamilies lists the families the machine offers, for diagnostics.
func (resolver *Resolver) HostFamilies() []string {
	resolver.scan()
	seen := map[string]bool{}
	var out []string
	for key := range resolver.table {
		if !seen[key.family] {
			seen[key.family] = true
			out = append(out, key.family)
		}
	}
	sort.Strings(out)
	return out
}

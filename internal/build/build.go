// Package build is the layout engine.
//
// It applies a template to a sequence of records
// and produces a printout, per doc/layout.md.
//
// Layout is speculative. Every band is measured into a scratch context,
// then decided against the space remaining, and only then committed.
// Band splitting, keep-together, orphan and widow control,
// measured header reservation, and correct deferred page counts
// are all consequences of that separation.
package build

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	gotime "time"

	"github.com/a1s/sr/internal/data"
	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/fontres"
	"github.com/a1s/sr/internal/tmpl"
	"github.com/a1s/sr/internal/vars"
	"github.com/a1s/sr/printout"
	startime "go.starlark.net/lib/time"
	"go.starlark.net/starlark"
)

// Options configure a build.
type Options struct {
	// TextParams are values supplied as text, parsed per the parameter's type.
	TextParams map[string]string
	// Values are values supplied already typed.
	Values map[string]starlark.Value
	// BuildTime fixes BUILD_TIME. The zero value means now.
	BuildTime gotime.Time
	// StrictFonts resolves only fonts the template names by file or data.
	StrictFonts bool
	// AllowOverflow downgrades oversized-band errors to warnings
	// recorded in the printout.
	AllowOverflow bool
	// Engine names the producing engine in the printout header.
	Engine string
	// Diagnostics receives messages about the machine: fonts that
	// could not be read, faces that collided. They describe the host
	// rather than the document, so they never enter the printout's warning list.
	Diagnostics func(string)
}

// engine holds one build in progress.
type engine struct {
	report *tmpl.Report
	opts   Options
	ctx    *scopeContext
	out    *printout.Printout

	resolver *fontres.Resolver
	faces    map[string]*fontres.Face

	frames *frameTree
	page   *printout.Page

	records []*expr.Record
	index   int

	images      map[string]*decodedImage
	blobs       map[string][]byte
	blobNames   map[string]string
	glyphWarned map[string]bool

	// currentScopes is the style search in force while a band is measured,
	// so that an xref's children can extend it.
	currentScopes styleScopes

	// deferrals waiting for their scope to end, keyed by scope name.
	pending map[string][]*deferral

	groupRuns map[string]int
	groupKeys map[string]map[string]bool

	// groups tracks each group and its last key, outermost first.
	groups    []*tmpl.Group
	groupKey  []starlark.Value
	groupOpen []bool

	// swapTitle is a title band that has to be placed above the page header.
	swapTitle *tmpl.Section
}

// Build applies a template to records and returns a printout.
func Build(report *tmpl.Report, rows []map[string]any, opts Options) (*printout.Printout, error) {
	eng := newEngine(report, opts)

	if node := firstSubreport(report); node != "" {
		return nil, fmt.Errorf(
			"%s: subreports are not implemented yet; this engine builds everything else",
			node)
	}
	if err := eng.bindParams(true); err != nil {
		return nil, err
	}
	if err := eng.bindVariables(); err != nil {
		return nil, err
	}
	records, err := data.Records(rows, report.Records)
	if err != nil {
		return nil, err
	}
	eng.records = records
	eng.ctx.dataCount = len(records)

	if err := eng.run(); err != nil {
		return nil, err
	}

	eng.finish()
	return eng.out, nil
}

// newEngine prepares a build: the printout header the template alone
// determines, and the font resolver.
//
// It stops short of binding parameters and reading records, so that a check
// that has no data -- see Fonts -- can share everything up to that point.
func newEngine(report *tmpl.Report, opts Options) *engine {
	if opts.Engine == "" {
		opts.Engine = "sr"
	}
	buildTime := opts.BuildTime
	if buildTime.IsZero() {
		buildTime = gotime.Now().UTC()
	}
	buildTime = buildTime.UTC().Truncate(gotime.Second)

	eng := &engine{
		report:      report,
		opts:        opts,
		ctx:         newScopeContext(startime.Time(buildTime)),
		images:      map[string]*decodedImage{},
		blobs:       map[string][]byte{},
		blobNames:   map[string]string{},
		faces:       map[string]*fontres.Face{},
		glyphWarned: map[string]bool{},
		pending:     map[string][]*deferral{},
		groupRuns:   map[string]int{},
		groupKeys:   map[string]map[string]bool{},
	}
	eng.out = &printout.Printout{Header: printout.Header{
		SR:          printout.Version,
		Kind:        "header",
		Built:       buildTime.Format(gotime.RFC3339),
		Engine:      opts.Engine,
		StrictFonts: opts.StrictFonts,
		Data:        map[string]*printout.Blob{},
	}}
	if report.Name != "" || report.Description != "" ||
		report.Version != "" || report.Author != "" {
		eng.out.Header.Report = &printout.ReportMeta{
			Name:        report.Name,
			Description: report.Description,
			Version:     report.Version,
			Author:      report.Author,
		}
	}
	eng.out.Header.Page = printout.PageGeometry{
		Width:        report.Layout.Page.Width,
		Height:       report.Layout.Page.Height,
		LeftMargin:   report.Layout.LeftMargin,
		RightMargin:  report.Layout.RightMargin,
		TopMargin:    report.Layout.TopMargin,
		BottomMargin: report.Layout.BottomMargin,
	}

	eng.resolver = fontres.NewResolver(report.BaseDir, opts.StrictFonts)
	eng.resolver.Blob = func(name string) ([]byte, bool) {
		raw, err := eng.blobBytes(name)
		if err != nil {
			return nil, false
		}
		return raw, true
	}

	return eng
}

// firstSubreport names the first subreport in the template, or "".
//
// Subreports are the most entangled part of the design and are staged last;
// refusing up front names the node rather than failing part way
// through a build.
func firstSubreport(report *tmpl.Report) string {
	found := ""
	tmpl.ForEachSection(report, func(section *tmpl.Section) {
		if found == "" && len(section.Subreports) > 0 {
			found = section.Subreports[0].Node.Path()
		}
	})
	return found
}

// bindParams binds each declared parameter to a value.
//
// required is false for a check that runs without a caller's values:
// there a parameter with neither a value nor a default is left unbound
// rather than refused, and an expression that reads it fails on its own terms.
// A build passes true, because a report missing a parameter is not a report.
func (eng *engine) bindParams(required bool) error {
	for _, param := range eng.report.Params {
		if value, ok := eng.opts.Values[param.Name]; ok {
			if !data.MatchesType(value, param.Type) {
				return fmt.Errorf(
					"parameter %q: the value supplied is %s, and the declared type is %s",
					param.Name, value.Type(), param.Type)
			}
			eng.ctx.params[param.Name] = value
			continue
		}
		if text, ok := eng.opts.TextParams[param.Name]; ok {
			value, err := data.ParamText(text, param.Type, param.Format)
			if err != nil {
				return fmt.Errorf(
					"parameter %q declared %s: %w", param.Name, param.Type, err)
			}
			eng.ctx.params[param.Name] = value
			continue
		}
		switch {
		case param.HasDefault:
			value, err := data.ParamText(param.Default, param.Type, param.Format)
			if err != nil {
				return fmt.Errorf(
					"parameter %q default declared %s: %w", param.Name, param.Type, err)
			}
			eng.ctx.params[param.Name] = value
		case param.DefaultExpr != nil:
			value, err := eng.ctx.eval(param.DefaultExpr)
			if err != nil {
				if !required {
					// The expression may read a parameter that a check
					// has no value for. Leave this one unbound too.
					continue
				}
				return fmt.Errorf("parameter %q: %w", param.Name, err)
			}
			if !data.MatchesType(value, param.Type) {
				return fmt.Errorf(
					"parameter %q: defaultexpr produced %s, and the declared type is %s",
					param.Name, value.Type(), param.Type)
			}
			eng.ctx.params[param.Name] = value
		case !required:
			// Left unbound on purpose: see the doc comment.
		default:
			return fmt.Errorf(
				"parameter %q is required: it has neither a default nor a defaultexpr, and no value was supplied",
				param.Name)
		}
	}
	return nil
}

func (eng *engine) bindVariables() error {
	for _, variable := range eng.report.Variables {
		state := &varState{def: variable, acc: vars.New(variable.Calc)}
		eng.ctx.varByName[variable.Name] = state
		eng.ctx.varOrder = append(eng.ctx.varOrder, state)
	}
	for _, name := range eng.report.GroupNames {
		eng.ctx.groupCount[name] = 0
		eng.ctx.groupPageNumber[name] = 1
		eng.groupKeys[name] = map[string]bool{}
	}
	return nil
}

// face resolves a template font name to a measurable face.
//
// It records the face in the printout's font table the first time the face is used.
func (eng *engine) face(name string) (*fontres.Face, error) {
	if face, ok := eng.faces[name]; ok {
		return face, nil
	}
	def, ok := eng.report.FontByName[name]
	if !ok {
		return nil, fmt.Errorf("no font named %q", name)
	}
	face, err := eng.resolver.Resolve(fontres.Request{
		Name:      def.Name,
		Typeface:  def.Typeface,
		File:      def.File,
		Data:      def.Data,
		Size:      def.Size,
		Bold:      def.Bold,
		Italic:    def.Italic,
		Underline: def.Underline,
	})
	if err != nil {
		return nil, err
	}
	eng.faces[name] = face

	index := face.FaceIndex()
	entry := printout.FontEntry{
		Name:          face.Name,
		Size:          face.Size,
		Bold:          face.Bold,
		Italic:        face.Italic,
		Underline:     face.Underline,
		Requested:     face.Requested,
		ResolvedIndex: index,
		ResolvedFace:  face.ResolvedFace,
		ResolvedBy:    string(face.ResolvedBy),
	}
	if face.ResolvedData != "" {
		entry.ResolvedData = face.ResolvedData
		if err := eng.publishBlob(face.ResolvedData); err != nil {
			return nil, err
		}
	} else {
		// A path the template named is a project asset
		// and travels with the printout; one the engine found
		// on the host is written as it was opened.
		entry.SetResolvedPath(face.ResolvedFile, face.ResolvedBy == fontres.ByExplicit)
	}
	eng.out.Header.Fonts = append(eng.out.Header.Fonts, entry)
	sort.Slice(eng.out.Header.Fonts, func(one, two int) bool {
		return eng.out.Header.Fonts[one].Name < eng.out.Header.Fonts[two].Name
	})
	return face, nil
}

// blobBytes decodes a template data node.
func (eng *engine) blobBytes(name string) ([]byte, error) {
	if raw, ok := eng.blobs[name]; ok {
		return raw, nil
	}
	def, ok := eng.report.DataByName[name]
	if !ok {
		return nil, fmt.Errorf("no data blob named %q", name)
	}
	var content string
	if def.Expr != nil {
		value, err := eng.ctx.eval(def.Expr)
		if err != nil {
			return nil, err
		}
		content = expr.Str(value)
	} else {
		content = def.Content
	}
	raw, err := decodeBlob(content, def.Encoding, def.Compress)
	if err != nil {
		return nil, fmt.Errorf("data %q: %w", name, err)
	}
	eng.blobs[name] = raw
	return raw, nil
}

func (eng *engine) blobText(name string) (string, error) {
	raw, err := eng.blobBytes(name)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// publishBlob puts a named template blob into the printout's data table.
func (eng *engine) publishBlob(name string) error {
	if _, done := eng.out.Header.Data[name]; done {
		return nil
	}
	raw, err := eng.blobBytes(name)
	if err != nil {
		return err
	}
	def := eng.report.DataByName[name]
	entry := &printout.Blob{}
	if def != nil && def.Encoding == "" && def.Compress == "" {
		entry.Content = string(raw)
	} else {
		entry.Encoding = "base64"
		entry.Content = base64.StdEncoding.EncodeToString(raw)
	}
	eng.out.Header.Data[name] = entry
	return nil
}

// addBlob puts an embedded image's bytes into the data table
// under a generated name, stable for a given source and distinct
// from every declared name. Two images from one source share one entry.
//
// An image with no file of its own is keyed by a digest of its bytes.
func (eng *engine) addBlob(img *decodedImage) string {
	key := img.file
	if key == "" {
		key = fmt.Sprintf("%x", sha256.Sum256(img.data))
	}
	if name, ok := eng.blobNames[key]; ok {
		return name
	}
	base := "image"
	if img.file != "" {
		base = filepath.Base(img.file)
	}
	name := base
	for serial := 1; ; serial++ {
		if _, taken := eng.out.Header.Data[name]; !taken {
			if _, declared := eng.report.DataByName[name]; !declared {
				break
			}
		}
		name = fmt.Sprintf("%s-%d", base, serial)
	}
	eng.out.Header.Data[name] = &printout.Blob{
		Encoding: "base64",
		Content:  base64.StdEncoding.EncodeToString(img.data),
	}
	eng.blobNames[key] = name
	return name
}

func (eng *engine) resolvePath(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(eng.report.BaseDir, rel)
}

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

// finish completes the header once every page is built.
func (eng *engine) finish() {
	eng.out.Header.Pages = len(eng.out.Pages)
	if len(eng.groupRuns) > 0 {
		eng.out.Header.GroupRuns = eng.groupRuns
		keys := map[string]int{}
		for name, set := range eng.groupKeys {
			keys[name] = len(set)
		}
		eng.out.Header.GroupKeys = keys
	}
	// A typeface that reached the substitute is a document warning;
	// a font file on the machine that this report did not use is not.
	for _, warning := range eng.resolver.Warnings {
		eng.out.AddWarning(printout.WarnFont, "", 0, warning)
	}
	if eng.opts.Diagnostics != nil {
		for _, diag := range eng.resolver.Diagnostics {
			eng.opts.Diagnostics(diag)
		}
	}
}

// overflow reports an oversized band, or records a warning
// when the caller has asked for overflow to be tolerated.
func (eng *engine) overflow(node, message string) error {
	if !eng.opts.AllowOverflow {
		return fmt.Errorf("%s: %s", node, message)
	}
	eng.out.AddWarning(printout.WarnOverflow, node, eng.index, message)
	return nil
}

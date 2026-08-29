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
	"strconv"
	"strings"
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

// shared is what every engine building one document has in common.
//
// A subreport is a nested engine, and most of what it needs is its own --
// its context, its records, its frames. What is not is here: the page
// being filled, so that an inline subreport writes to the host's page
// and an eject anywhere moves them both, and the registries that belong
// to the document rather than to one template.
type shared struct {
	// page is the page marks are appended to.
	page *printout.Page
	// units are the loaded subreport layouts, one per subreport node,
	// so that a subreport invoked per record resolves its fonts once.
	units map[*tmpl.Subreport]*unit
	// resolvers are every font resolver in the document, since
	// each template resolves relative to its own base directory.
	resolvers []*fontres.Resolver
	// declared is every data blob name any template in the document
	// declares, so a generated name never takes one.
	declared map[string]bool
	// blobNames maps a blob's content to the name it was published under,
	// and faceNames does the same for a resolved face.
	blobNames   map[string]string
	faceNames   map[string]string
	glyphWarned map[string]bool
	groupRuns   map[string]int
	groupKeys   map[string]map[string]bool
}

// unit is one template's state, kept between the invocations of a subreport:
// the caches keyed by names that belong to that template.
//
// An embedded layout shares the enclosing report's fonts and data, so it
// shares that report's unit; a template named by file gets one of its own.
type unit struct {
	report    *tmpl.Report
	resolver  *fontres.Resolver
	faces     map[string]*fontres.Face
	fontNames map[string]string
	blobs     map[string][]byte
	published map[string]string
	images    map[string]*decodedImage
}

// engine holds one build in progress.
type engine struct {
	report *tmpl.Report
	opts   Options
	ctx    *scopeContext
	out    *printout.Printout
	doc    *shared
	unit   *unit

	// host is the engine this one is nested in, and inline says
	// whether it prints on the host's pages. Both are zero at the root.
	host   *engine
	inline bool
	depth  int

	resolver *fontres.Resolver
	faces    map[string]*fontres.Face
	// fontNames maps a template font name to the name the printout
	// publishes it under, which is what a mark refers to.
	fontNames map[string]string

	frames *frameTree

	records []*expr.Record
	index   int

	// args are the values a subreport's arg nodes supplied, evaluated in the
	// host's context. They stand where the command line stands at the root.
	args map[string]starlark.Value

	images      map[string]*decodedImage
	blobs       map[string][]byte
	published   map[string]string
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

	doc := &shared{
		units:       map[*tmpl.Subreport]*unit{},
		declared:    map[string]bool{},
		blobNames:   map[string]string{},
		faceNames:   map[string]string{},
		glyphWarned: map[string]bool{},
		groupRuns:   map[string]int{},
		groupKeys:   map[string]map[string]bool{},
	}
	eng := &engine{
		report:  report,
		opts:    opts,
		ctx:     newScopeContext(startime.Time(buildTime)),
		pending: map[string][]*deferral{},
	}
	eng.adopt(doc)
	eng.attach(newUnit(report, opts, doc))
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

	return eng
}

// newUnit prepares one template's caches and its font resolver.
//
// The resolver is per template, because a `font file=` is relative to that
// template's own base directory; the document keeps them all, so that what
// the machine offered can be reported for every one of them.
func newUnit(report *tmpl.Report, opts Options, doc *shared) *unit {
	item := &unit{
		report:    report,
		faces:     map[string]*fontres.Face{},
		fontNames: map[string]string{},
		blobs:     map[string][]byte{},
		published: map[string]string{},
		images:    map[string]*decodedImage{},
	}
	item.resolver = fontres.NewResolver(report.BaseDir, opts.StrictFonts)
	doc.resolvers = append(doc.resolvers, item.resolver)
	for name := range report.DataByName {
		doc.declared[name] = true
	}
	return item
}

// adopt points the engine at the document-wide registries.
func (eng *engine) adopt(doc *shared) {
	eng.doc = doc
	eng.blobNames = doc.blobNames
	eng.glyphWarned = doc.glyphWarned
	eng.groupRuns = doc.groupRuns
	eng.groupKeys = doc.groupKeys
}

// attach points the engine at one template's caches.
//
// A `data expr=` blob is evaluated in a context, and a font may be read from
// one, so the resolver's way back to the blobs is bound here rather than when
// the unit was made: whichever engine is running owns the context that a blob
// would be evaluated in. An engine resuming after a subreport attaches again.
func (eng *engine) attach(item *unit) {
	eng.unit = item
	eng.report = item.report
	eng.resolver = item.resolver
	eng.faces = item.faces
	eng.fontNames = item.fontNames
	eng.blobs = item.blobs
	eng.published = item.published
	eng.images = item.images
	item.resolver.Blob = func(name string) ([]byte, bool) {
		raw, err := eng.blobBytes(name)
		if err != nil {
			return nil, false
		}
		return raw, true
	}
}

// pageGeometry is what this engine's own pages measure.
func (eng *engine) pageGeometry() printout.PageGeometry {
	layout := eng.report.Layout
	return printout.PageGeometry{
		Width:        layout.Page.Width,
		Height:       layout.Page.Height,
		LeftMargin:   layout.LeftMargin,
		RightMargin:  layout.RightMargin,
		TopMargin:    layout.TopMargin,
		BottomMargin: layout.BottomMargin,
	}
}

// checkSupplied refuses a value supplied for a name the template does not declare.
//
// Nothing else in the run would mention it. The report builds, with the
// default in place of what the caller meant, and says nothing -- which is
// the worst way for a misspelled parameter name to behave, and the likelier
// mistake in a generated command line than a name given twice.
//
// A check that binds leniently does not run this: there the caller has
// supplied values for a template they may be checking rather than building,
// and the CLI reports the same thing as a usage error before this could.
func (eng *engine) checkSupplied() error {
	var unknown []string
	for name := range eng.opts.Values {
		if _, ok := eng.report.ParamByName[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	for name := range eng.opts.TextParams {
		if _, ok := eng.report.ParamByName[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	quoted := make([]string, 0, len(unknown))
	for _, name := range unknown {
		quoted = append(quoted, strconv.Quote(name))
	}
	if len(eng.report.Params) == 0 {
		return fmt.Errorf("no parameter named %s; the template declares none",
			strings.Join(quoted, ", "))
	}
	declared := make([]string, 0, len(eng.report.Params))
	for _, param := range eng.report.Params {
		declared = append(declared, param.Name)
	}
	return fmt.Errorf("no parameter named %s; the template declares %s",
		strings.Join(quoted, ", "), strings.Join(declared, ", "))
}

// bindParams binds each declared parameter to a value.
//
// required is false for a check that runs without a caller's values:
// there a parameter with neither a value nor a default is left unbound
// rather than refused, and an expression that reads it fails on its own terms.
// A build passes true, because a report missing a parameter is not a report.
func (eng *engine) bindParams(required bool) error {
	if required && eng.host == nil {
		if err := eng.checkSupplied(); err != nil {
			return err
		}
	}
	for _, param := range eng.report.Params {
		// A subreport has no command line. Its values come from its arg
		// nodes, which the host evaluated and type-checked before this ran.
		if eng.host != nil {
			if value, ok := eng.args[param.Name]; ok {
				eng.ctx.params[param.Name] = value
				continue
			}
		} else {
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
		// The key sets are the document's, and a subreport binds
		// its variables once per invocation, so an existing set
		// is added to rather than replaced.
		if _, ok := eng.groupKeys[name]; !ok {
			eng.groupKeys[name] = map[string]bool{}
		}
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

	entry := printout.FontEntry{
		Name:          face.Name,
		Size:          face.Size,
		Bold:          face.Bold,
		Italic:        face.Italic,
		Underline:     face.Underline,
		Requested:     face.Requested,
		ResolvedIndex: face.FaceIndex(),
		ResolvedFace:  face.ResolvedFace,
		ResolvedBy:    string(face.ResolvedBy),
	}
	if face.ResolvedData != "" {
		published, err := eng.publishBlob(face.ResolvedData)
		if err != nil {
			// Named here so that every error out of this function names
			// the font, which is what a caller collecting them relies on.
			// The resolver's own messages already do.
			return nil, fmt.Errorf("font %q: %w", name, err)
		}
		entry.ResolvedData = published
	} else {
		// A path the template named is a project asset
		// and travels with the printout; one the engine found
		// on the host is written as it was opened.
		entry.SetResolvedPath(face.ResolvedFile, face.ResolvedBy == fontres.ByExplicit)
	}
	eng.fontNames[name] = eng.publishFont(entry)
	return face, nil
}

// fontName is the name the printout knows a template font by.
//
// The two differ only where two templates in one document give one name
// to different faces, which a subreport naming an external template can do.
func (eng *engine) fontName(name string) string {
	if published, ok := eng.fontNames[name]; ok {
		return published
	}
	return name
}

// publishFont puts a resolved face in the printout's font table
// and returns the name marks refer to it by.
//
// A face already there under any name is reused, so a file that a report
// and its subreport both name is measured and embedded once. A name already
// taken by a different face takes a suffix, because the table is what a mark
// resolves through and two entries cannot answer to one name.
func (eng *engine) publishFont(entry printout.FontEntry) string {
	key := faceKey(entry)
	if name, ok := eng.doc.faceNames[key]; ok {
		return name
	}
	taken := func(name string) bool {
		for _, existing := range eng.out.Header.Fonts {
			if existing.Name == name {
				return true
			}
		}
		return false
	}
	want := entry.Name
	for serial := 2; taken(entry.Name); serial++ {
		entry.Name = fmt.Sprintf("%s-%d", want, serial)
	}
	eng.out.Header.Fonts = append(eng.out.Header.Fonts, entry)
	sort.Slice(eng.out.Header.Fonts, func(one, two int) bool {
		return eng.out.Header.Fonts[one].Name < eng.out.Header.Fonts[two].Name
	})
	eng.doc.faceNames[key] = entry.Name
	return entry.Name
}

// faceKey identifies a face for the document, independent of the route
// the template that named it was reached by.
//
// The path is canonicalised, because a subreport is loaded through its host's
// base directory while the host may have been named relatively on the command
// line: the same file arrives spelled two ways, and it is one face.
//
// The requested typeface is part of the identity rather than of the resolution
// alone: two templates asking for different families that land on one file are
// two records of what was asked, and the entry is that record.
func faceKey(entry printout.FontEntry) string {
	file := entry.ResolvedFile
	if file != "" {
		if abs, err := filepath.Abs(file); err == nil {
			file = filepath.ToSlash(abs)
		}
	}
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%d\x00%t\x00%t\x00%t\x00%s\x00%s",
		file, entry.ResolvedData, entry.Requested, entry.Size, entry.ResolvedIndex,
		entry.Bold, entry.Italic, entry.Underline, entry.ResolvedFace, entry.ResolvedBy)
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

// publishBlob puts a named template blob into the printout's data table
// and returns the name it went in under, which is what a mark refers to.
func (eng *engine) publishBlob(name string) (string, error) {
	if published, done := eng.published[name]; done {
		return published, nil
	}
	raw, err := eng.blobBytes(name)
	if err != nil {
		return "", err
	}
	def := eng.report.DataByName[name]
	entry := &printout.Blob{}
	if def != nil && def.Encoding == "" && def.Compress == "" {
		entry.Content = string(raw)
	} else {
		entry.Encoding = "base64"
		entry.Content = base64.StdEncoding.EncodeToString(raw)
	}
	published := eng.putBlob(name, entry, true)
	eng.published[name] = published
	return published, nil
}

// addBlob puts an embedded image's bytes into the data table
// under a generated name, stable for a given source and distinct
// from every declared name. Two images from one source share one entry.
func (eng *engine) addBlob(img *decodedImage) string {
	return eng.putBlob(imageBlobName(img), &printout.Blob{
		Encoding: "base64",
		Content:  base64.StdEncoding.EncodeToString(img.data),
	}, false)
}

// imageBlobName is what an embedded image would like to be called.
func imageBlobName(img *decodedImage) string {
	if img.file != "" {
		return filepath.Base(img.file)
	}
	return "image"
}

// putBlob adds content to the printout's data table and returns its name.
//
// Content already there is shared, whichever template asked for it: the table
// is the document's, and one image used by a report and its subreport is one
// blob. A wanted name that is taken by different content takes a suffix, and
// a generated name also gives way to any name a template declares, so that a
// declared blob still gets the name its author wrote.
func (eng *engine) putBlob(want string, entry *printout.Blob, declared bool) string {
	key := fmt.Sprintf("%s\x00%x", entry.Encoding, sha256.Sum256([]byte(entry.Content)))
	if name, ok := eng.blobNames[key]; ok {
		return name
	}
	name := want
	for serial := 2; ; serial++ {
		_, taken := eng.out.Header.Data[name]
		if !taken && (declared || !eng.doc.declared[name]) {
			break
		}
		name = fmt.Sprintf("%s-%d", want, serial)
	}
	eng.out.Header.Data[name] = entry
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
	// Every template in the document resolves through a resolver of its own,
	// so a subreport's substituted typeface is reported here too.
	for _, resolver := range eng.doc.resolvers {
		for _, warning := range resolver.Warnings {
			eng.out.AddWarning(printout.WarnFont, "", 0, warning)
		}
		if eng.opts.Diagnostics != nil {
			for _, diag := range resolver.Diagnostics {
				eng.opts.Diagnostics(diag)
			}
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

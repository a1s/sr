// Package sr applies a banded report template to a sequence of records
// and produces a paginated document.
//
// A template is a KDL v2 document; records are Go maps or structs, or JSON
// on the way in; the result is a printout -- pages of absolutely-positioned
// marks with nothing evaluable left in them.
//
//	tpl, err := sr.LoadTemplate("sakila.kdl")
//	out, err := tpl.Build(rows, sr.StrictFonts())
//
// The specification the implementation follows is in the doc directory:
// template.md, expressions.md, layout.md and printout.md.
package sr

import (
	"io"
	"time"

	"github.com/a1s/sr/internal/build"
	"github.com/a1s/sr/internal/data"
	"github.com/a1s/sr/internal/expr"
	"github.com/a1s/sr/internal/tmpl"
	"github.com/a1s/sr/meta"
	"github.com/a1s/sr/printout"
	"github.com/shopspring/decimal"
	startime "go.starlark.net/lib/time"
	"go.starlark.net/starlark"
)

// Printout is the document a build produces. See package printout.
type Printout = printout.Printout

// Template is a loaded, validated template.
type Template struct {
	report *tmpl.Report
}

// LoadTemplate reads and validates a template file.
//
// Validation runs once, here, before any data is read: node nesting,
// required properties, name collisions, geometry over-specification,
// content sources, and expression syntax are all load-time diagnostics
// rather than surprises mid-report.
func LoadTemplate(path string) (*Template, error) {
	report, err := tmpl.Load(path)
	if err != nil {
		return nil, err
	}
	return &Template{report: report}, nil
}

// ParseTemplate reads a template from memory.
//
// name is used in diagnostics and as the base for relative paths.
func ParseTemplate(name, source string) (*Template, error) {
	report, err := tmpl.LoadString(source, name)
	if err != nil {
		return nil, err
	}
	return &Template{report: report}, nil
}

// Warnings are load-time diagnostics that did not stop the template loading.
func (tpl *Template) Warnings() []string {
	out := make([]string, 0, len(tpl.report.Warnings))
	for _, diag := range tpl.report.Warnings {
		out = append(out, diag.Error())
	}
	return out
}

// Name is the template's report name.
func (tpl *Template) Name() string { return tpl.report.Name }

// Option configures a build.
type Option func(*build.Options)

// WithParam supplies a report parameter as a typed value.
//
// Its type must match the parameter's declaration.
func WithParam(name string, value any) Option {
	return func(opts *build.Options) {
		if opts.Values == nil {
			opts.Values = map[string]starlark.Value{}
		}
		opts.Values[name] = toStarlark(value)
	}
}

// WithTextParam supplies a report parameter as text,
// parsed per the parameter's declared type -- the form the command line uses.
func WithTextParam(name, value string) Option {
	return func(opts *build.Options) {
		if opts.TextParams == nil {
			opts.TextParams = map[string]string{}
		}
		opts.TextParams[name] = value
	}
}

// WithBuildTime fixes BUILD_TIME.
//
// With it, and with StrictFonts, the same template over the same data
// produces a byte-identical printout on any machine.
func WithBuildTime(when time.Time) Option {
	return func(opts *build.Options) { opts.BuildTime = when }
}

// StrictFonts resolves only fonts the template names by file or data.
//
// It fails with the unresolved typeface named.
// Tests run this way, so no outcome depends on what is installed.
func StrictFonts() Option {
	return func(opts *build.Options) { opts.StrictFonts = true }
}

// AllowOverflow downgrades oversized-band errors to warnings
// recorded in the printout header, so an overflowing document
// is identifiable from the artifact.
func AllowOverflow() Option {
	return func(opts *build.Options) { opts.AllowOverflow = true }
}

// WithDiagnostics receives messages about the machine:
// a font file that could not be read, two faces claiming one family and style.
//
// They describe the host rather than the document, so they are not printout warnings.
func WithDiagnostics(fn func(string)) Option {
	return func(opts *build.Options) { opts.Diagnostics = fn }
}

// Build applies the template to records.
//
// records may be a []map[string]any or a slice of structs;
// struct fields map to declared members by name, or by an `sr:"..."` tag.
// Pass nil for a template with no records.
func (tpl *Template) Build(records any, options ...Option) (*Printout, error) {
	opts := build.Options{Engine: "sr " + meta.Version}
	for _, opt := range options {
		opt(&opts)
	}
	rows, err := data.Rows(records)
	if err != nil {
		return nil, err
	}
	return build.Build(tpl.report, rows, opts)
}

// BuildJSON applies the template to records read from a JSON array document
// or from NDJSON.
//
// The whole dataset is buffered: DATA_COUNT, report-scoped aggregates,
// and keep-together lookahead all need the full sequence.
func (tpl *Template) BuildJSON(reader io.Reader, options ...Option) (*Printout, error) {
	rows, err := data.ReadJSON(reader)
	if err != nil {
		return nil, err
	}
	return tpl.Build(rows, options...)
}

// Decimal builds an exact decimal value,
// the type a member declared type="decimal" produces.
func Decimal(text string) decimal.Decimal {
	dec, err := decimal.NewFromString(text)
	if err != nil {
		panic("sr.Decimal: " + err.Error())
	}
	return dec
}

// toStarlark converts a Go value supplied as a parameter.
func toStarlark(value any) starlark.Value {
	switch typed := value.(type) {
	case starlark.Value:
		return typed
	case decimal.Decimal:
		return expr.NewDecimal(typed)
	case time.Time:
		return startime.Time(typed)
	case string:
		return starlark.String(typed)
	case bool:
		return starlark.Bool(typed)
	case int:
		return starlark.MakeInt(typed)
	case int64:
		return starlark.MakeInt64(typed)
	case float64:
		return starlark.Float(typed)
	}
	return data.Generic(value)
}

// Package pdf renders a printout as PDF.
//
// It does no layout. Every box in a printout is already absolute,
// every string already wrapped, every barcode already encoded,
// so rendering is a translation of marks into drawing operators
// and nothing else. What the renderer decides is only what a printout
// deliberately leaves to it: where a baseline sits inside its leading,
// how a justified line distributes its slack, and what a dash pattern
// measures.
//
// The rules it follows are written down in doc/render.md.
//
//	out, err := tpl.Build(rows)
//	err = pdf.WriteFile(out, "report.pdf")
package pdf

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/a1s/sr/internal/pdfw"
	"github.com/a1s/sr/printout"
)

// Option configures a render.
type Option func(*config)

type config struct {
	compress bool
	producer string
}

// Uncompressed leaves the content streams, fonts and images
// uncompressed, which makes the file readable in a text editor.
func Uncompressed() Option {
	return func(cfg *config) { cfg.compress = false }
}

// WithProducer overrides the Producer string in the document
// information dictionary, which defaults to the printout's engine.
func WithProducer(name string) Option {
	return func(cfg *config) { cfg.producer = name }
}

// Write renders a printout to a writer.
func Write(doc *printout.Printout, writer io.Writer, options ...Option) error {
	cfg := config{compress: true}
	for _, option := range options {
		option(&cfg)
	}
	ren := &renderer{
		src:    doc,
		out:    pdfw.New(),
		cfg:    cfg,
		fonts:  map[string]*renderFont{},
		images: map[string]*picture{},
		named:  map[string]int{},
	}
	ren.out.Compress = cfg.compress
	raw, err := ren.render()
	if err != nil {
		return err
	}
	_, err = writer.Write(raw)
	return err
}

// WriteFile renders a printout to a file.
//
// The whole document is rendered before the file is touched.
// A render that fails -- a font file that has moved since the printout
// was written is the ordinary way -- leaves the previous report where
// it was rather than truncating it to nothing.
func WriteFile(doc *printout.Printout, path string, options ...Option) error {
	var raw bytes.Buffer
	if err := Write(doc, &raw, options...); err != nil {
		return err
	}
	return os.WriteFile(path, raw.Bytes(), 0o644)
}

// renderer holds one render's state: the resources gathered
// from the whole document, then the objects written for them.
type renderer struct {
	src *printout.Printout
	out *pdfw.Doc
	cfg config

	fonts     map[string]*renderFont
	fontNames []string
	images    map[string]*picture
	imageKeys []string

	// pageObjects are allocated before anything is written, because an
	// outline entry and a link both name the page they jump to.
	pageObjects []int

	outlines []*outlineNode
	named    map[string]int
}

// render walks the printout twice: once to gather every font, glyph
// and image it uses, and once to write the pages. The first pass
// exists because a font's objects state which glyphs the subset holds,
// and that is only known after the last page has been read.
func (ren *renderer) render() ([]byte, error) {
	if len(ren.src.Pages) == 0 {
		// A PDF has at least one page, so there is nothing to write
		// and nothing sensible to invent.
		return nil, fmt.Errorf("the printout has no pages")
	}
	if err := ren.gather(); err != nil {
		return nil, err
	}

	pagesObject := ren.out.Alloc()
	resources := ren.out.Alloc()
	ren.pageObjects = make([]int, len(ren.src.Pages))
	contentObjects := make([]int, len(ren.src.Pages))
	for index := range ren.src.Pages {
		ren.pageObjects[index] = ren.out.Alloc()
		contentObjects[index] = ren.out.Alloc()
	}

	if err := ren.writeFonts(); err != nil {
		return nil, err
	}
	if err := ren.writeImages(); err != nil {
		return nil, err
	}

	for index, page := range ren.src.Pages {
		content, annots, err := ren.page(index, page)
		if err != nil {
			return nil, err
		}
		ren.out.Object(ren.pageObjects[index], fmt.Sprintf(
			"<</Type /Page /Parent %s /MediaBox [0 0 %s %s] /Resources %s%s /Contents %s>>",
			pdfw.Ref(pagesObject),
			pdfw.Num(ren.pageWidth(page)), pdfw.Num(ren.pageHeight(page)),
			pdfw.Ref(resources), annots, pdfw.Ref(contentObjects[index])))
		ren.out.Stream(contentObjects[index], "", content)
	}

	kids := make([]string, len(ren.pageObjects))
	for index, object := range ren.pageObjects {
		kids[index] = pdfw.Ref(object)
	}
	ren.out.Object(pagesObject, fmt.Sprintf("<</Type /Pages /Count %d /Kids [%s]>>",
		len(kids), strings.Join(kids, " ")))
	ren.out.Object(resources, ren.resourceDict())

	outlineRoot := ren.writeOutlines()
	info := ren.writeInfo()

	catalog := ren.out.Alloc()
	extra := ""
	if outlineRoot > 0 {
		extra = fmt.Sprintf(" /Outlines %s /PageMode /UseOutlines", pdfw.Ref(outlineRoot))
	}
	ren.out.Object(catalog, "<</Type /Catalog /Pages "+pdfw.Ref(pagesObject)+extra+">>")
	return ren.out.Finish(catalog, info), nil
}

// pageWidth and pageHeight fall back to the header's defaults,
// which is what a page that does not override them inherits.
func (ren *renderer) pageWidth(page *printout.Page) float64 {
	if page.Width > 0 {
		return page.Width
	}
	return ren.src.Header.Page.Width
}

func (ren *renderer) pageHeight(page *printout.Page) float64 {
	if page.Height > 0 {
		return page.Height
	}
	return ren.src.Header.Page.Height
}

// resourceDict names every font and image, shared by all pages.
//
// One dictionary rather than one per page keeps the file small;
// a reader loads a resource when a page draws with it,
// not when it is listed.
func (ren *renderer) resourceDict() string {
	var out strings.Builder
	out.WriteString("<</ProcSet [/PDF /Text /ImageB /ImageC]")
	if len(ren.fontNames) > 0 {
		out.WriteString(" /Font <<")
		for index, name := range ren.fontNames {
			font := ren.fonts[name]
			if index > 0 {
				out.WriteString(" ")
			}
			fmt.Fprintf(&out, "/%s %s", font.resource, pdfw.Ref(font.object))
		}
		out.WriteString(">>")
	}
	if len(ren.imageKeys) > 0 {
		out.WriteString(" /XObject <<")
		for index, key := range ren.imageKeys {
			pic := ren.images[key]
			if index > 0 {
				out.WriteString(" ")
			}
			fmt.Fprintf(&out, "/%s %s", pic.resource, pdfw.Ref(pic.object))
		}
		out.WriteString(">>")
	}
	out.WriteString(">>")
	return out.String()
}

// writeInfo writes the document information dictionary from the
// printout's own header, so a reader shows what the template declared
// and the run recorded.
func (ren *renderer) writeInfo() int {
	header := ren.src.Header
	var out strings.Builder
	out.WriteString("<<")
	if header.Report != nil {
		if header.Report.Name != "" {
			fmt.Fprintf(&out, "/Title %s ", pdfw.TextString(header.Report.Name))
		}
		if header.Report.Author != "" {
			fmt.Fprintf(&out, "/Author %s ", pdfw.TextString(header.Report.Author))
		}
		if header.Report.Description != "" {
			fmt.Fprintf(&out, "/Subject %s ", pdfw.TextString(header.Report.Description))
		}
	}
	if header.Engine != "" {
		fmt.Fprintf(&out, "/Creator %s ", pdfw.TextString(header.Engine))
	}
	producer := ren.cfg.producer
	if producer == "" {
		producer = header.Engine
	}
	if producer != "" {
		fmt.Fprintf(&out, "/Producer %s ", pdfw.TextString(producer))
	}
	// The build time rather than the moment of rendering:
	// rendering one printout twice gives one document,
	// and BUILD_TIME is the date the document actually carries.
	if built, err := time.Parse(time.RFC3339, header.Built); err == nil {
		fmt.Fprintf(&out, "/CreationDate %s /ModDate %s ",
			pdfw.Date(built), pdfw.Date(built))
	}
	out.WriteString(">>")

	info := ren.out.Alloc()
	ren.out.Object(info, out.String())
	return info
}

package pdf

import (
	"fmt"
	"os"

	"github.com/a1s/sr/internal/pdfw"
	"github.com/a1s/sr/printout"
)

// picture is one decoded image, shared by every mark that draws it.
type picture struct {
	pic      *pdfw.Picture
	resource string
	object   int
}

// outlineNode is one outline entry, with the page it jumps to.
type outlineNode struct {
	title  string
	level  int
	closed bool
	page   int
	left   float64
	top    float64

	kids   []*outlineNode
	object int
}

// gather is the first pass: it opens the fonts, notes every character
// the document sets, decodes every image once however many marks draw it,
// and collects the outline entries in the order they appear.
func (ren *renderer) gather() error {
	if err := ren.loadFonts(); err != nil {
		return err
	}
	for index, page := range ren.src.Pages {
		if err := ren.gatherMarks(index, page.Marks); err != nil {
			return fmt.Errorf("page %d: %w", page.Number, err)
		}
	}
	return nil
}

func (ren *renderer) gatherMarks(pageIndex int, marks []printout.Mark) error {
	for _, mark := range marks {
		switch typed := mark.(type) {
		case *printout.Text:
			font, ok := ren.fonts[typed.Font]
			if !ok {
				return fmt.Errorf(
					"a text mark names font %q, which the header does not carry",
					typed.Font)
			}
			for _, line := range typed.Lines {
				font.note(line)
			}
		case *printout.Image:
			if err := ren.gatherImage(typed); err != nil {
				return err
			}
		case *printout.Outline:
			node := &outlineNode{
				title:  typed.Title,
				level:  typed.Level,
				closed: typed.Closed,
				page:   pageIndex,
				left:   typed.Left,
				top:    typed.Top,
			}
			if typed.Name != "" {
				ren.named[typed.Name] = len(ren.outlines)
			}
			ren.outlines = append(ren.outlines, node)
		case *printout.Xref:
			if err := ren.gatherMarks(pageIndex, typed.Marks); err != nil {
				return err
			}
		}
	}
	return nil
}

// imageKey identifies an image's source, so two marks reading one blob
// or one file share a single XObject.
func imageKey(mark *printout.Image) string {
	if mark.Data != "" {
		return "data:" + mark.Data
	}
	return "file:" + mark.ResolvedPath()
}

func (ren *renderer) gatherImage(mark *printout.Image) error {
	key := imageKey(mark)
	if _, done := ren.images[key]; done {
		return nil
	}
	raw, err := ren.imageBytes(mark)
	if err != nil {
		return err
	}
	pic, err := pdfw.DecodePicture(raw)
	if err != nil {
		return fmt.Errorf("image %s: %w", key, err)
	}
	ren.images[key] = &picture{
		pic:      pic,
		resource: fmt.Sprintf("Im%d", len(ren.imageKeys)+1),
	}
	ren.imageKeys = append(ren.imageKeys, key)
	return nil
}

// imageBytes reads an image mark's blob out of the header,
// or its file off the disk. Exactly one of the two is present.
func (ren *renderer) imageBytes(mark *printout.Image) ([]byte, error) {
	if mark.Data != "" {
		blob, ok := ren.src.Header.Data[mark.Data]
		if !ok || blob == nil {
			return nil, fmt.Errorf(
				"an image names data entry %q, which the header does not carry",
				mark.Data)
		}
		return blobBytes(blob)
	}
	path := mark.ResolvedPath()
	if path == "" {
		return nil, fmt.Errorf("an image mark names neither a file nor a data entry")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("image %s: %w", path, err)
	}
	return raw, nil
}

func (ren *renderer) writeImages() error {
	for _, key := range ren.imageKeys {
		pic := ren.images[key]
		pic.object = ren.out.WriteImage(pic.pic)
	}
	return nil
}

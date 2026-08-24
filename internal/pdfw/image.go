package pdfw

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"strings"

	// The decoders for the three formats a printout's image mark names.
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
)

// Picture is an image ready to become an XObject: samples, the colour
// space they are in, and a soft mask when the source had transparency.
type Picture struct {
	Width, Height int
	// ColorSpace is a PDF name without its slash: DeviceRGB or DeviceGray.
	ColorSpace string
	// Filter is the PDF filter the samples already carry, empty
	// when they are raw and the writer is free to compress them.
	Filter string
	Data   []byte
	// Alpha holds one grey sample per pixel, absent for an opaque image.
	Alpha []byte
}

// DecodePicture turns an image file into samples a PDF can carry.
//
// A JPEG whose own compression a PDF can read is passed through
// untouched, so a photograph does not grow twentyfold on the way
// into the document. Everything else is decoded and re-encoded,
// which is the simple road and correct for every format the format admits.
func DecodePicture(raw []byte) (*Picture, error) {
	if pic := jpegPassthrough(raw); pic != nil {
		return pic, nil
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return samplePicture(decoded), nil
}

// samplePicture reads an image out pixel by pixel.
//
// A grey source stays grey, which is a third of the bytes;
// anything else becomes eight-bit RGB.
// Transparency, if any pixel has it, becomes a soft mask.
func samplePicture(source image.Image) *Picture {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	grey := isGrey(source)

	pic := &Picture{Width: width, Height: height}
	if grey {
		pic.ColorSpace = "DeviceGray"
		pic.Data = make([]byte, 0, width*height)
	} else {
		pic.ColorSpace = "DeviceRGB"
		pic.Data = make([]byte, 0, 3*width*height)
	}
	alpha := make([]byte, 0, width*height)
	opaque := true

	for top := bounds.Min.Y; top < bounds.Max.Y; top++ {
		for left := bounds.Min.X; left < bounds.Max.X; left++ {
			red, green, blue, transparency := source.At(left, top).RGBA()
			// RGBA returns alpha-premultiplied values; undo that, so a
			// half-transparent red stays red rather than turning dark.
			// Clamped, because a component larger than the alpha it was
			// premultiplied by is not a colour -- and it happens, in an
			// image assembled in memory rather than decoded from a file.
			if transparency > 0 && transparency < 0xFFFF {
				red = clamp(red * 0xFFFF / transparency)
				green = clamp(green * 0xFFFF / transparency)
				blue = clamp(blue * 0xFFFF / transparency)
			}
			if grey {
				pic.Data = append(pic.Data, byte(red>>8))
			} else {
				pic.Data = append(pic.Data,
					byte(red>>8), byte(green>>8), byte(blue>>8))
			}
			alpha = append(alpha, byte(transparency>>8))
			if transparency < 0xFFFF {
				opaque = false
			}
		}
	}
	if !opaque {
		pic.Alpha = alpha
	}
	return pic
}

// clamp keeps a colour component inside the range a sample can hold.
func clamp(value uint32) uint32 {
	if value > 0xFFFF {
		return 0xFFFF
	}
	return value
}

func isGrey(source image.Image) bool {
	switch source.ColorModel() {
	case color.GrayModel, color.Gray16Model:
		return true
	}
	return false
}

// jpegPassthrough recognises a JPEG whose entropy coding a PDF reader
// can decode as it stands, and returns it unchanged.
//
// Progressive and CMYK files are not passed through: the first is not
// universally supported by readers, and the second needs an inverted
// Decode array that is easy to get wrong. Both go the decoding road.
func jpegPassthrough(raw []byte) *Picture {
	if len(raw) < 4 || raw[0] != 0xFF || raw[1] != 0xD8 {
		return nil
	}
	baseline := false
	components := 0
	for at := 2; at+4 <= len(raw); {
		if raw[at] != 0xFF {
			return nil
		}
		marker := raw[at+1]
		if marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			at += 2
			continue
		}
		if marker == 0xD9 || marker == 0xDA {
			break
		}
		length := int(binary.BigEndian.Uint16(raw[at+2 : at+4]))
		switch marker {
		case 0xC0, 0xC1:
			if at+10 > len(raw) {
				return nil
			}
			baseline = true
			components = int(raw[at+9])
		case 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF:
			// Progressive, arithmetic, or lossless: decode instead.
			return nil
		}
		at += 2 + length
	}
	if !baseline {
		return nil
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	space := ""
	switch components {
	case 1:
		space = "DeviceGray"
	case 3:
		space = "DeviceRGB"
	default:
		return nil
	}
	return &Picture{
		Width: config.Width, Height: config.Height,
		ColorSpace: space, Filter: "/DCTDecode", Data: raw,
	}
}

// WriteImage writes an image XObject, and its soft mask when it has one,
// returning the number of the image object.
func (doc *Doc) WriteImage(pic *Picture) int {
	mask := 0
	if len(pic.Alpha) > 0 {
		mask = doc.Alloc()
		doc.Stream(mask, fmt.Sprintf(
			"/Type /XObject /Subtype /Image /Width %d /Height %d"+
				" /ColorSpace /DeviceGray /BitsPerComponent 8",
			pic.Width, pic.Height), pic.Alpha)
	}

	num := doc.Alloc()
	var dict strings.Builder
	fmt.Fprintf(&dict,
		"/Type /XObject /Subtype /Image /Width %d /Height %d"+
			" /ColorSpace /%s /BitsPerComponent 8",
		pic.Width, pic.Height, pic.ColorSpace)
	if mask > 0 {
		fmt.Fprintf(&dict, " /SMask %s", Ref(mask))
	}
	if pic.Filter != "" {
		fmt.Fprintf(&dict, " /Filter %s", pic.Filter)
		doc.RawStream(num, dict.String(), pic.Data)
		return num
	}
	doc.Stream(num, dict.String(), pic.Data)
	return num
}

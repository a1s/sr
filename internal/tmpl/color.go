package tmpl

import (
	"fmt"
	"strconv"
	"strings"
)

// Color is an RGB colour. The canonical form,
// and what a printout carries, is always "#RRGGBB".
type Color struct {
	Red, Green, Blue uint8
}

// Hex renders the canonical form.
func (color Color) Hex() string {
	return fmt.Sprintf("#%02X%02X%02X", color.Red, color.Green, color.Blue)
}

// namedColors are the sixteen HTML 4.01 names plus the six the format adds.
var namedColors = map[string]Color{
	"black":   {0x00, 0x00, 0x00},
	"silver":  {0xC0, 0xC0, 0xC0},
	"gray":    {0x80, 0x80, 0x80},
	"white":   {0xFF, 0xFF, 0xFF},
	"maroon":  {0x80, 0x00, 0x00},
	"red":     {0xFF, 0x00, 0x00},
	"purple":  {0x80, 0x00, 0x80},
	"fuchsia": {0xFF, 0x00, 0xFF},
	"green":   {0x00, 0x80, 0x00},
	"lime":    {0x00, 0xFF, 0x00},
	"olive":   {0x80, 0x80, 0x00},
	"yellow":  {0xFF, 0xFF, 0x00},
	"navy":    {0x00, 0x00, 0x80},
	"blue":    {0x00, 0x00, 0xFF},
	"teal":    {0x00, 0x80, 0x80},
	"aqua":    {0x00, 0xFF, 0xFF},

	"cyan":      {0x00, 0xFF, 0xFF},
	"darkgray":  {0xA9, 0xA9, 0xA9},
	"lightgray": {0xD3, 0xD3, 0xD3},
	"magenta":   {0xFF, 0x00, 0xFF},
	"orange":    {0xFF, 0xA5, 0x00},
	"pink":      {0xFF, 0xC0, 0xCB},
}

// ParseColor accepts "#RRGGBB", a name, three comma-separated components
// as integers 0-255 or floats 0-1, or a single packed integer.
func ParseColor(spec string) (Color, error) {
	text := strings.TrimSpace(spec)
	if text == "" {
		return Color{}, fmt.Errorf("empty colour")
	}
	if strings.HasPrefix(text, "#") {
		digits := text[1:]
		if len(digits) != 6 {
			return Color{}, fmt.Errorf("bad colour %q: want #RRGGBB", spec)
		}
		bits, err := strconv.ParseUint(digits, 16, 32)
		if err != nil {
			return Color{}, fmt.Errorf("bad colour %q", spec)
		}
		return Color{uint8(bits >> 16), uint8(bits >> 8), uint8(bits)}, nil
	}
	if color, ok := namedColors[strings.ToLower(text)]; ok {
		return color, nil
	}
	if strings.Contains(text, ",") {
		parts := strings.Split(text, ",")
		if len(parts) != 3 {
			return Color{}, fmt.Errorf("bad colour %q: want three components", spec)
		}
		var out [3]uint8
		// Floats 0-1 and integers 0-255 are distinguished by whether every
		// component parses as an integer.
		allInt := true
		for _, part := range parts {
			if _, err := strconv.Atoi(strings.TrimSpace(part)); err != nil {
				allInt = false
				break
			}
		}
		for index, part := range parts {
			part = strings.TrimSpace(part)
			if allInt {
				num, err := strconv.Atoi(part)
				if err != nil || num < 0 || num > 255 {
					return Color{}, fmt.Errorf(
						"bad colour %q: component %q out of 0-255", spec, part)
				}
				out[index] = uint8(num)
				continue
			}
			frac, err := strconv.ParseFloat(part, 64)
			if err != nil || frac < 0 || frac > 1 {
				return Color{}, fmt.Errorf(
					"bad colour %q: component %q out of 0-1", spec, part)
			}
			out[index] = uint8(frac*255 + 0.5)
		}
		return Color{out[0], out[1], out[2]}, nil
	}
	packed, err := strconv.ParseUint(text, 10, 32)
	if err != nil {
		return Color{}, fmt.Errorf("bad colour %q", spec)
	}
	return Color{uint8(packed >> 16), uint8(packed >> 8), uint8(packed)}, nil
}

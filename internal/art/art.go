// Package art renders keel's anchor mascot (the real Gemini pixel art) to the
// terminal using Unicode half-blocks + 24-bit colour. The studio (browser) uses
// the same anchor.jpg directly.
package art

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	_ "image/jpeg"
	"strings"
)

//go:embed anchor.jpg
var anchorJPG []byte

// cache memoizes the (expensive) decode+render by width, so a TUI redrawing on
// every mouse event doesn't re-decode the JPEG each frame.
var cache = map[int]string{}

// Anchor returns the anchor mascot as ANSI half-block art, cols wide (memoized).
func Anchor(cols int) string {
	if s, ok := cache[cols]; ok {
		return s
	}
	s := render(anchorJPG, cols)
	cache[cols] = s
	return s
}

// render draws an image as half-block rows.
//
// Each cell is one "▀": the foreground paints its top half and the background
// paints its bottom, so a cell carries two colour samples and a row of cells
// carries two rows of image. A terminal cell is about twice as tall as it is
// wide, which is why two samples per cell come out square.
func render(data []byte, cols int) string {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	b := crop(img)
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 || cols < 2 {
		return ""
	}
	// Rounded rather than truncated: at small sizes losing most of a row to
	// integer division visibly squashes the image.
	rows := (cols*h + w) / (2 * w)
	if rows < 1 {
		rows = 1
	}
	// A sample is "background" (the dark navy card the anchor sits on) if it is
	// dark; those render as a space, so the anchor sits transparently on the
	// terminal's own background rather than inside a blue box.
	lit := func(r, g, bl uint8) bool { return (int(r)+int(g)+int(bl))/3 > 55 }

	var sb strings.Builder
	for ry := 0; ry < rows; ry++ {
		for cx := 0; cx < cols; cx++ {
			x0 := b.Min.X + cx*w/cols
			x1 := b.Min.X + (cx+1)*w/cols
			yTop := b.Min.Y + (ry*2)*h/(rows*2)
			yMid := b.Min.Y + (ry*2+1)*h/(rows*2)
			yBot := b.Min.Y + (ry*2+2)*h/(rows*2)
			tR, tG, tB := mean(img, x0, yTop, x1, yMid)
			bR, bG, bB := mean(img, x0, yMid, x1, yBot)
			switch tOn, bOn := lit(tR, tG, tB), lit(bR, bG, bB); {
			case tOn && bOn:
				fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%d;48;2;%d;%d;%dm▀\x1b[0m", tR, tG, tB, bR, bG, bB)
			case tOn:
				fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm▀\x1b[0m", tR, tG, tB)
			case bOn:
				fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm▄\x1b[0m", bR, bG, bB)
			default:
				sb.WriteByte(' ')
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// crop is the bounding box of the artwork inside the image.
//
// anchor.jpg is the anchor centred on a dark navy card with a wide margin, and
// the margin is rendered as blank cells. Drawing the whole square therefore
// spent rows and columns on nothing, left the anchor small, and gave the block
// the card's square proportions rather than the anchor's own taller ones.
//
// A row or column counts as artwork when a few of its pixels are lit, not one:
// JPEG ringing puts stray bright pixels into the card, and a single-pixel test
// finds them and crops nothing.
func crop(img image.Image) image.Rectangle {
	b := img.Bounds()
	const floor = 70 // above the card, below anything that is part of the anchor
	minRun := b.Dx() / 100
	if minRun < 2 {
		minRun = 2
	}
	x0, y0, x1, y1 := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if (int(r>>8)+int(g>>8)+int(bl>>8))/3 <= floor {
				continue
			}
			if x < x0 {
				x0 = x
			}
			if x >= x1 {
				x1 = x + 1
			}
			if y < y0 {
				y0 = y
			}
			if y >= y1 {
				y1 = y + 1
			}
		}
	}
	if x1-x0 < minRun || y1-y0 < minRun {
		return b // nothing recognisable; draw it all rather than nothing
	}
	return image.Rect(x0, y0, x1, y1)
}

// mean is the average colour of the source rectangle one half-cell covers.
//
// Averaging rather than picking a single pixel is what stops the anchor looking
// speckled: at 16 columns one half-cell stands for a 64x32 block of a 1024x1024
// image, and sampling one pixel out of two thousand returns whatever noise
// happens to sit at that coordinate instead of what that part of the anchor
// actually looks like.
func mean(img image.Image, x0, y0, x1, y1 int) (uint8, uint8, uint8) {
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	var sr, sg, sb, n uint32
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			sr, sg, sb, n = sr+r>>8, sg+g>>8, sb+b>>8, n+1
		}
	}
	if n == 0 {
		return 0, 0, 0
	}
	return uint8(sr / n), uint8(sg / n), uint8(sb / n)
}

package art

import (
	"bytes"
	"image"
	"image/color"
	"regexp"
	"strings"
	"testing"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// plain strips the colour escapes so a row's real width can be measured.
func plain(s string) string { return ansi.ReplaceAllString(s, "") }

func lines(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

// Every row is exactly cols cells wide, and there are as many rows as the
// cropped artwork's aspect calls for.
func TestAnchorGeometry(t *testing.T) {
	for _, cols := range []int{16, 20, 32} {
		got := lines(Anchor(cols))
		for i, ln := range got {
			if n := len([]rune(plain(ln))); n != cols {
				t.Errorf("cols=%d row %d is %d cells wide", cols, i, n)
			}
		}
		img, _, err := image.Decode(bytes.NewReader(anchorJPG))
		if err != nil {
			t.Fatal(err)
		}
		b := crop(img)
		want := (cols*b.Dy() + b.Dx()) / (2 * b.Dx())
		if len(got) != want {
			t.Errorf("cols=%d gave %d rows, want %d for a %dx%d crop", cols, len(got), want, b.Dx(), b.Dy())
		}
	}
}

// The artwork is cropped to itself, so no row or column of the rendering is
// blank padding.
//
// anchor.jpg is the anchor on a dark navy card with a wide margin, and the
// margin renders as blank cells: drawing the whole square spent rows and columns
// on nothing, made the anchor smaller than the space allowed, and gave the block
// the card's square proportions instead of the anchor's taller ones.
func TestAnchorIsCroppedToTheArtwork(t *testing.T) {
	img, _, err := image.Decode(bytes.NewReader(anchorJPG))
	if err != nil {
		t.Fatal(err)
	}
	full := img.Bounds()
	b := crop(img)
	if b == full {
		t.Fatal("crop found no margin to remove")
	}
	if !b.In(full) || b.Empty() {
		t.Fatalf("crop %v is not a sub-rectangle of %v", b, full)
	}
	// The anchor is taller than it is wide; the card it sits on is square.
	if b.Dy() <= b.Dx() {
		t.Errorf("cropped artwork is %dx%d, expected it to be taller than wide", b.Dx(), b.Dy())
	}

	rows := lines(Anchor(24))
	if strings.TrimSpace(plain(rows[0])) == "" {
		t.Error("the first row is blank padding")
	}
	if strings.TrimSpace(plain(rows[len(rows)-1])) == "" {
		t.Error("the last row is blank padding")
	}
}

// Cells carry two colour samples, not one.
//
// A cell whose halves are both lit is drawn as "▀" with a foreground and a
// background, so it shows the top and bottom sample. It used to draw a full
// block in the top colour alone, which threw away half the vertical resolution
// the half-block trick exists to provide.
func TestAnchorUsesBothHalvesOfACell(t *testing.T) {
	out := Anchor(24)
	if strings.Contains(out, "█") {
		t.Error("a full block means one colour for two samples")
	}
	if !strings.Contains(out, ";48;2;") {
		t.Error("no cell sets a background, so no cell carries two colours")
	}
	// The dark card is dropped rather than painted, so the anchor sits on the
	// terminal's own background instead of inside a blue box.
	if !strings.Contains(plain(out), " ") {
		t.Error("nothing is transparent, so the card is being drawn")
	}
}

// Downsampling averages the source region rather than sampling one pixel of it.
// Point sampling a 1024x1024 photo down to a 24-cell block picks up whatever
// speckle sits at each coordinate, which is what made the anchor look noisy.
func TestMeanAveragesTheRegion(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, colorOf(0, 0, 0))
	img.Set(1, 0, colorOf(200, 100, 50))
	r, g, b := mean(img, 0, 0, 2, 1)
	if r != 100 || g != 50 || b != 25 {
		t.Errorf("mean = (%d,%d,%d), want the average (100,50,25)", r, g, b)
	}
	// An empty rectangle is widened rather than dividing by zero.
	if _, _, _ = mean(img, 0, 0, 0, 0); false {
		t.Fatal("unreachable")
	}
}

// Anchor memoizes by width: the console redraws on every mouse event, and
// decoding a 1024x1024 JPEG per frame would be felt.
func TestAnchorIsMemoized(t *testing.T) {
	a := Anchor(18)
	if b := Anchor(18); a != b {
		t.Error("two calls at the same width disagree")
	}
	if _, ok := cache[18]; !ok {
		t.Error("the render was not cached")
	}
	// A width too small to draw anything returns empty rather than panicking.
	if Anchor(1) != "" {
		t.Error("a one-column anchor should be empty")
	}
}

// colorOf is an opaque RGBA colour, for building fixture images.
func colorOf(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 255} }

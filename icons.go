package guardian

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

const iconColor = "#3b4a8a"

// makeIcons draws the home-screen icons: a light shield on indigo.
func makeIcons() map[string][]byte {
	return map[string][]byte{
		"apple-touch-icon.png": drawIcon(180),
		"icon-192.png":         drawIcon(192),
		"icon-512.png":         drawIcon(512),
	}
}

func drawIcon(size int) []byte {
	bg := color.RGBA{0x3b, 0x4a, 0x8a, 0xff}
	shield := color.RGBA{0xf4, 0xf3, 0xf0, 0xff}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx, fy := (float64(x)+0.5)/s, (float64(y)+0.5)/s
			c := bg
			if inShield(fx, fy) && !inCheck(fx, fy) {
				c = shield
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// inShield: a shield shape, flat top at y=0.22, pointed bottom at y=0.82.
func inShield(x, y float64) bool {
	if y < 0.22 || y > 0.82 {
		return false
	}
	half := 0.27
	if y > 0.5 {
		t := (y - 0.5) / 0.32
		half = 0.27 * math.Sqrt(math.Max(0, 1-t*t))
	}
	return math.Abs(x-0.5) <= half
}

// inCheck cuts a tick mark out of the shield.
func inCheck(x, y float64) bool {
	w := 0.05
	// short stroke down-right, long stroke up-right
	d1 := math.Abs((x-0.38)*-1+(y-0.50)) / math.Sqrt2
	on1 := d1 < w && x >= 0.36 && x <= 0.47 && y >= 0.48 && y <= 0.60
	d2 := math.Abs((x-0.46)+(y-0.58)) / math.Sqrt2
	on2 := d2 < w && x >= 0.44 && x <= 0.64 && y >= 0.40 && y <= 0.60
	return on1 || on2
}

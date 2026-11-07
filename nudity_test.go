package guardian

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"sync"
	"testing"
)

// Synthetic test pictures only: gradients, solid colours, noise and simple
// shapes. They are generated identically by the Node script that produced
// the expected nsfwjs 4.3.0 probabilities below (tfjs 4.22.0, CPU backend,
// model.infer on the same pixels as an int32 tensor).

func nudityXorshift(seed uint32) func() uint32 {
	s := seed
	return func() uint32 {
		s ^= s << 13
		s ^= s >> 17
		s ^= s << 5
		return s
	}
}

func nuditySynthetic(name string) image.Image {
	var w, h int
	var f func(x, y int) [3]uint8
	switch name {
	case "gradient":
		w, h = 224, 224
		f = func(x, y int) [3]uint8 { return [3]uint8{uint8(x), uint8(y), uint8((x + y) >> 1)} }
	case "solid":
		w, h = 300, 200
		f = func(x, y int) [3]uint8 { return [3]uint8{200, 80, 40} }
	case "noise":
		w, h = 320, 240
		r := nudityXorshift(12345)
		f = func(x, y int) [3]uint8 { v := r(); return [3]uint8{uint8(v), uint8(v >> 8), uint8(v >> 16)} }
	case "shapes":
		w, h = 256, 256
		f = func(x, y int) [3]uint8 {
			dx, dy := x-90, y-100
			if dx*dx+dy*dy < 50*50 {
				return [3]uint8{220, 30, 30}
			}
			if x >= 140 && x < 230 && y >= 150 && y < 220 {
				return [3]uint8{30, 60, 200}
			}
			return [3]uint8{245, 245, 245}
		}
	case "checker":
		w, h = 97, 131
		f = func(x, y int) [3]uint8 {
			if ((x>>3)+(y>>3))&1 == 1 {
				return [3]uint8{20, 160, 40}
			}
			return [3]uint8{250, 230, 90}
		}
	default:
		panic(name)
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h { // row-major, same order as the Node script (noise)
		for x := range w {
			c := f(x, y)
			img.SetNRGBA(x, y, color.NRGBA{c[0], c[1], c[2], 255})
		}
	}
	return img
}

func nudityPNG(t testing.TB, img image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

var nudityExpected = []struct {
	name string
	p    [5]float32 // Drawing, Hentai, Neutral, Porn, Sexy
}{
	{"gradient", [5]float32{0.8874111, 0.02024283, 0.08735526, 0.004046256, 0.0009445689}},
	{"solid", [5]float32{0.8507589, 0.01245504, 0.1322486, 0.003678750, 0.0008587603}},
	{"noise", [5]float32{0.1271803, 0.01020921, 0.8422841, 0.01784226, 0.002484142}},
	{"shapes", [5]float32{0.3418715, 0.001278667, 0.6568225, 0.00002273239, 0.000004630135}},
	{"checker", [5]float32{0.002053858, 0.0005372449, 0.9951974, 0.001994479, 0.0002170937}},
}

func TestNudityParityWithNsfwjs(t *testing.T) {
	m, err := newNudityModel()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range nudityExpected {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.Classify(nudityPNG(t, nuditySynthetic(tc.name)))
			if err != nil {
				t.Fatal(err)
			}
			var worst float64
			for i := range got {
				worst = max(worst, math.Abs(float64(got[i]-tc.p[i])))
			}
			t.Logf("max |go - nsfwjs| = %.2g", worst)
			if worst > 1e-3 {
				t.Errorf("got %v, nsfwjs %v", got, tc.p)
			}
			nude, err := m.LooksNude(nudityPNG(t, nuditySynthetic(tc.name)))
			if err != nil || nude {
				t.Errorf("LooksNude = %v, %v; want false", nude, err)
			}
		})
	}
}

func TestNudityJPEGAndConcurrency(t *testing.T) {
	m, err := newNudityModel()
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, nuditySynthetic("shapes"), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	want, err := m.Classify(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var sum float32
	for _, v := range want {
		sum += v
	}
	if math.Abs(float64(sum-1)) > 1e-4 || want[nudityNeutral] < want[nudityPorn] {
		t.Fatalf("odd probabilities for a JPEG of shapes: %v", want)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			got, err := m.Classify(b.Bytes())
			if err != nil || got != want {
				t.Errorf("concurrent Classify = %v, %v; want %v", got, err, want)
			}
		})
	}
	wg.Wait()
}

// nudityFakePNG is a valid small PNG whose header claims w x h pixels.
func nudityFakePNG(t *testing.T, w, h uint32) []byte {
	b := nudityPNG(t, image.NewGray(image.Rect(0, 0, 8, 8)))
	// IHDR: 8 sig + 4 len + 4 type, then width, height, ...; CRC after 13 bytes.
	binary.BigEndian.PutUint32(b[16:], w)
	binary.BigEndian.PutUint32(b[20:], h)
	binary.BigEndian.PutUint32(b[29:], crc32.ChecksumIEEE(b[12:29]))
	return b
}

func TestNudityRefusesOversizedAndJunk(t *testing.T) {
	m, err := newNudityModel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Classify(nudityFakePNG(t, 8000, 6000)); !errors.Is(err, errNudityTooLarge) {
		t.Errorf("48 megapixels: err = %v, want too large", err)
	}
	if _, err := m.Classify(nudityFakePNG(t, 20000, 20000)); !errors.Is(err, errNudityTooLarge) {
		t.Errorf("huge header: err = %v, want too large", err)
	}
	big := append(nudityPNG(t, nuditySynthetic("solid")), make([]byte, nudityMaxBytes)...)
	if _, err := m.Classify(big); !errors.Is(err, errNudityTooLarge) {
		t.Errorf("over 10 MB: err = %v, want too large", err)
	}
	if nude, err := m.LooksNude(big); err == nil || nude {
		t.Errorf("LooksNude on over 10 MB = %v, %v", nude, err)
	}
	if _, err := m.Classify([]byte("GIF89a not a photo")); !errors.Is(err, errNudityFormat) {
		t.Errorf("junk: err = %v, want format error", err)
	}
	if _, err := m.Classify(nil); err == nil {
		t.Error("empty input accepted")
	}
	// Tiny images are fine.
	if _, err := m.Classify(nudityPNG(t, image.NewGray(image.Rect(0, 0, 1, 1)))); err != nil {
		t.Errorf("1x1 image: %v", err)
	}
}

func BenchmarkNudityClassify(b *testing.B) {
	m, err := newNudityModel()
	if err != nil {
		b.Fatal(err)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, nuditySynthetic("noise"), &jpeg.Options{Quality: 85}); err != nil {
		b.Fatal(err)
	}
	img := buf.Bytes()
	for b.Loop() {
		if _, err := m.Classify(img); err != nil {
			b.Fatal(err)
		}
	}
}

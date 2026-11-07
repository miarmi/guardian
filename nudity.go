package guardian

// Nudity check for photos the child receives. A small image classifier
// (MobileNetV2 from nsfwjs / GantMan's nsfw_model, MIT, see nudity/LICENSE)
// runs here, in pure Go, inside Guardian. The photo is decoded in memory,
// scaled down to 224x224 and scored; it is never written anywhere, never
// sent anywhere and never shown.
//
// The weights are the original uint8-quantized tensors from nsfwjs 4.3.0
// (nudity/mobilenet_v2.bin, 2.6 MB); nudity/mobilenet_v2.json lists the
// layers (batch norms already paired with their convolutions) and where each
// tensor sits in the .bin. Preprocessing matches nsfwjs: RGB / 255, then a
// bilinear resize to 224x224 with align_corners=true (no cropping, the
// aspect ratio is not kept), as tf.image.resizeBilinear does.

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"sync"
)

//go:embed nudity/mobilenet_v2.bin
var nudityWeights []byte

//go:embed nudity/mobilenet_v2.json
var nudityTopology []byte

// nudityClass indexes the classifier's five probabilities.
type nudityClass int

const (
	nudityDrawing nudityClass = iota // non-explicit drawings and art
	nudityHentai                     // explicit drawings
	nudityNeutral                    // everyday photos
	nudityPorn                       // explicit photos
	nuditySexy                       // revealing but not explicit (swimwear, underwear)
)

var nudityClassNames = [5]string{"Drawing", "Hentai", "Neutral", "Porn", "Sexy"}

func (c nudityClass) String() string {
	if c < 0 || int(c) >= len(nudityClassNames) {
		return fmt.Sprintf("nudityClass(%d)", int(c))
	}
	return nudityClassNames[c]
}

const (
	// nudityMaxBytes and nudityMaxPixels bound the work an incoming photo can
	// cause. WhatsApp photos are well under 1 MB and 4096 px a side.
	nudityMaxBytes  = 10 << 20
	nudityMaxPixels = 40_000_000

	// nudityThreshold: a photo looks like nudity when Porn + Hentai >= 0.5,
	// i.e. when the explicit classes together are the model's majority
	// verdict. Reasons:
	//   - "Sexy" never counts: swimwear and underwear are normal on a
	//     child's phone, and the model puts beach photos there.
	//   - Porn and Hentai are added up because the model often splits an
	//     explicit image between them (photo-like drawings, filtered
	//     photos); either one is something to tell the parents about.
	//   - The model's mistakes are mostly confusions between neighbouring
	//     classes (Porn vs Sexy, Hentai vs Drawing). Requiring a majority
	//     keeps those borderline images from alarming the parents, while an
	//     explicit image typically scores Porn well above 0.8, so 0.5 leaves
	//     a wide margin before one is missed. Raising it towards 0.8 would
	//     trade misses for quiet; lowering it below 0.5 lets "Sexy" photos
	//     with a little Porn probability through as alarms.
	nudityThreshold = 0.5
)

var (
	errNudityTooLarge = errors.New("nudity: image too large")
	errNudityFormat   = errors.New("nudity: not a JPEG or PNG image")
)

const (
	opPad = iota
	opConv
	opDWConv
	opAdd
	opAvgPool
	opDense
)

// nudityOp is one step of the network with its weights ready to use
// (dequantized, batch norm folded into kernel and bias).
type nudityOp struct {
	kind       int
	in         [2]int // value ids; in[1] only for add
	out        int
	k, stride  int
	padT, padL int // pad op: top/left; conv: implicit zero padding
	padB, padR int
	relu6      bool
	act        string // dense: relu, softmax or linear
	w, b       []float32
	inH, inW   int
	inC        int
	outH, outW int
	outC       int
}

type nudityModel struct {
	ops     []nudityOp
	size    int   // input side, 224
	valSize []int // floats per value
	slotOf  []int // value id -> workspace slot
	slots   []int // floats per slot
	output  int
	pool    sync.Pool // *nudityWork
}

type nudityWork struct{ slots [][]float32 }

var (
	nudityOnce   sync.Once
	nudityShared *nudityModel
	nudityErr    error
)

// newNudityModel returns the classifier, loading the embedded weights the
// first time. The model is safe for concurrent use.
func newNudityModel() (*nudityModel, error) {
	nudityOnce.Do(func() { nudityShared, nudityErr = loadNudityModel(nudityTopology, nudityWeights) })
	return nudityShared, nudityErr
}

type nudityTopo struct {
	Classes []string `json:"classes"`
	Input   string   `json:"input"`
	Size    int      `json:"size"`
	Output  string   `json:"output"`
	Ops     []struct {
		Op      string   `json:"op"`
		In      []string `json:"in"`
		Out     string   `json:"out"`
		T       int      `json:"t"`
		B       int      `json:"b"`
		L       int      `json:"l"`
		R       int      `json:"r"`
		K       int      `json:"k"`
		Stride  int      `json:"stride"`
		Padding string   `json:"padding"`
		Kernel  string   `json:"kernel"`
		Bias    string   `json:"bias"`
		BN      string   `json:"bn"`
		Eps     float64  `json:"eps"`
		Relu6   bool     `json:"relu6"`
		Act     string   `json:"act"`
	} `json:"ops"`
	Weights map[string]struct {
		Offset int     `json:"offset"`
		Shape  []int   `json:"shape"`
		Min    float64 `json:"min"`
		Scale  float64 `json:"scale"`
	} `json:"weights"`
}

func loadNudityModel(topo, bin []byte) (*nudityModel, error) {
	var t nudityTopo
	if err := json.Unmarshal(topo, &t); err != nil {
		return nil, fmt.Errorf("nudity: topology: %w", err)
	}
	if len(t.Classes) != 5 {
		return nil, errors.New("nudity: topology: expected 5 classes")
	}
	for i, c := range t.Classes {
		if c != nudityClassNames[i] {
			return nil, fmt.Errorf("nudity: topology: class %d is %q", i, c)
		}
	}
	// tensor dequantizes a weight like tfjs does: min + q*scale, as float32.
	tensor := func(name string, shape ...int) ([]float64, error) {
		w, ok := t.Weights[name]
		if !ok {
			return nil, fmt.Errorf("nudity: missing tensor %s", name)
		}
		n := 1
		for _, d := range w.Shape {
			n *= d
		}
		if len(shape) != len(w.Shape) {
			return nil, fmt.Errorf("nudity: tensor %s has shape %v", name, w.Shape)
		}
		for i := range shape {
			if shape[i] >= 0 && shape[i] != w.Shape[i] {
				return nil, fmt.Errorf("nudity: tensor %s has shape %v, want %v", name, w.Shape, shape)
			}
		}
		if w.Offset < 0 || w.Offset+n > len(bin) {
			return nil, fmt.Errorf("nudity: tensor %s out of range", name)
		}
		out := make([]float64, n)
		for i, q := range bin[w.Offset : w.Offset+n] {
			out[i] = float64(float32(w.Min + float64(q)*w.Scale))
		}
		return out, nil
	}

	m := &nudityModel{size: t.Size}
	ids := map[string]int{t.Input: 0}
	type shape struct{ h, w, c int }
	shapes := []shape{{t.Size, t.Size, 3}}
	val := func(name string) (int, error) {
		id, ok := ids[name]
		if !ok {
			return 0, fmt.Errorf("nudity: unknown value %s", name)
		}
		return id, nil
	}
	for _, o := range t.Ops {
		if len(o.In) == 0 {
			return nil, fmt.Errorf("nudity: op %s has no input", o.Out)
		}
		in, err := val(o.In[0])
		if err != nil {
			return nil, err
		}
		s := shapes[in]
		op := nudityOp{in: [2]int{in, -1}, inH: s.h, inW: s.w, inC: s.c, relu6: o.Relu6}
		switch o.Op {
		case "pad":
			op.kind = opPad
			op.padT, op.padB, op.padL, op.padR = o.T, o.B, o.L, o.R
			op.outH, op.outW, op.outC = s.h+o.T+o.B, s.w+o.L+o.R, s.c
		case "conv", "dwconv":
			if o.K < 1 || o.Stride < 1 {
				return nil, fmt.Errorf("nudity: bad conv %s", o.Out)
			}
			op.k, op.stride = o.K, o.Stride
			switch o.Padding {
			case "valid":
				op.outH = (s.h-o.K)/o.Stride + 1
				op.outW = (s.w-o.K)/o.Stride + 1
			case "same":
				op.outH = (s.h + o.Stride - 1) / o.Stride
				op.outW = (s.w + o.Stride - 1) / o.Stride
				op.padT = max((op.outH-1)*o.Stride+o.K-s.h, 0) / 2
				op.padL = max((op.outW-1)*o.Stride+o.K-s.w, 0) / 2
			default:
				return nil, fmt.Errorf("nudity: padding %q", o.Padding)
			}
			var kern []float64
			if o.Op == "conv" {
				op.kind = opConv
				if kern, err = tensor(o.Kernel, o.K, o.K, s.c, -1); err != nil {
					return nil, err
				}
				op.outC = t.Weights[o.Kernel].Shape[3]
			} else {
				op.kind = opDWConv
				if kern, err = tensor(o.Kernel, o.K, o.K, s.c, 1); err != nil {
					return nil, err
				}
				op.outC = s.c
			}
			if o.BN == "" {
				return nil, fmt.Errorf("nudity: conv %s without batch norm", o.Out)
			}
			var bn [4][]float64
			for i, p := range []string{"gamma", "beta", "moving_mean", "moving_variance"} {
				if bn[i], err = tensor(o.BN+"/"+p, op.outC); err != nil {
					return nil, err
				}
			}
			// Fold y = gamma*(x-mean)/sqrt(var+eps) + beta into the kernel.
			op.w = make([]float32, len(kern))
			op.b = make([]float32, op.outC)
			mul := make([]float64, op.outC)
			for c := range op.outC {
				mul[c] = bn[0][c] / math.Sqrt(bn[3][c]+o.Eps)
				op.b[c] = float32(bn[1][c] - bn[2][c]*mul[c])
			}
			for i, v := range kern {
				op.w[i] = float32(v * mul[i%op.outC])
			}
		case "add":
			op.kind = opAdd
			if len(o.In) != 2 {
				return nil, fmt.Errorf("nudity: add %s needs two inputs", o.Out)
			}
			if op.in[1], err = val(o.In[1]); err != nil {
				return nil, err
			}
			if shapes[op.in[1]] != s {
				return nil, fmt.Errorf("nudity: add %s shape mismatch", o.Out)
			}
			op.outH, op.outW, op.outC = s.h, s.w, s.c
		case "avgpool":
			op.kind = opAvgPool
			op.k, op.stride = o.K, o.Stride
			op.outH, op.outW, op.outC = (s.h-o.K)/o.Stride+1, (s.w-o.K)/o.Stride+1, s.c
		case "dense":
			op.kind = opDense
			n := s.h * s.w * s.c
			kern, err := tensor(o.Kernel, n, -1)
			if err != nil {
				return nil, err
			}
			op.outH, op.outW, op.outC = 1, 1, t.Weights[o.Kernel].Shape[1]
			bias, err := tensor(o.Bias, op.outC)
			if err != nil {
				return nil, err
			}
			op.w, op.b = make([]float32, len(kern)), make([]float32, len(bias))
			for i, v := range kern {
				op.w[i] = float32(v)
			}
			for i, v := range bias {
				op.b[i] = float32(v)
			}
			switch o.Act {
			case "relu", "softmax", "linear":
				op.act = o.Act
			default:
				return nil, fmt.Errorf("nudity: activation %q", o.Act)
			}
		default:
			return nil, fmt.Errorf("nudity: unsupported op %q", o.Op)
		}
		if op.outH < 1 || op.outW < 1 || op.outC < 1 {
			return nil, fmt.Errorf("nudity: op %s has empty output", o.Out)
		}
		if _, dup := ids[o.Out]; dup {
			return nil, fmt.Errorf("nudity: value %s defined twice", o.Out)
		}
		op.out = len(shapes)
		ids[o.Out] = op.out
		shapes = append(shapes, shape{op.outH, op.outW, op.outC})
		m.ops = append(m.ops, op)
	}
	out, err := val(t.Output)
	if err != nil {
		return nil, err
	}
	if s := shapes[out]; s.h*s.w*s.c != 5 {
		return nil, errors.New("nudity: output is not 5 classes")
	}
	m.output = out
	for _, s := range shapes {
		m.valSize = append(m.valSize, s.h*s.w*s.c)
	}
	m.planSlots()
	m.pool.New = func() any {
		w := &nudityWork{slots: make([][]float32, len(m.slots))}
		for i, n := range m.slots {
			w.slots[i] = make([]float32, n)
		}
		return w
	}
	return m, nil
}

// planSlots gives every value a workspace buffer, reusing buffers of values
// that are no longer needed, so one inference needs about 15 MB.
func (m *nudityModel) planSlots() {
	last := make([]int, len(m.valSize)) // op index of the last read
	for i, op := range m.ops {
		for _, v := range op.in {
			if v >= 0 {
				last[v] = i
			}
		}
	}
	last[m.output] = len(m.ops)
	m.slotOf = make([]int, len(m.valSize))
	var free []int
	take := func(n int) int {
		best := -1
		for i, s := range free {
			if m.slots[s] >= n && (best < 0 || m.slots[s] < m.slots[free[best]]) {
				best = i
			}
		}
		if best < 0 && len(free) > 0 { // grow the largest free buffer
			best = 0
			for i, s := range free {
				if m.slots[s] > m.slots[free[best]] {
					best = i
				}
			}
		}
		if best < 0 {
			m.slots = append(m.slots, n)
			return len(m.slots) - 1
		}
		s := free[best]
		free = append(free[:best], free[best+1:]...)
		m.slots[s] = max(m.slots[s], n)
		return s
	}
	m.slotOf[0] = take(m.valSize[0])
	for i, op := range m.ops {
		m.slotOf[op.out] = take(m.valSize[op.out])
		for j, v := range op.in {
			if v >= 0 && last[v] == i && (j == 0 || v != op.in[0]) {
				free = append(free, m.slotOf[v])
			}
		}
	}
}

// Classify returns the probabilities of Drawing, Hentai, Neutral, Porn and
// Sexy (indexed by nudityClass) for a JPEG or PNG image.
func (m *nudityModel) Classify(img []byte) ([5]float32, error) {
	var p [5]float32
	if len(img) > nudityMaxBytes {
		return p, errNudityTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(img))
	if err != nil {
		return p, fmt.Errorf("%w: %v", errNudityFormat, err)
	}
	if format != "jpeg" && format != "png" {
		return p, errNudityFormat
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > nudityMaxPixels {
		return p, errNudityTooLarge
	}
	pic, _, err := image.Decode(bytes.NewReader(img))
	if err != nil {
		return p, fmt.Errorf("%w: %v", errNudityFormat, err)
	}
	w := m.pool.Get().(*nudityWork)
	defer m.pool.Put(w)
	m.resizeInto(w.slots[m.slotOf[0]][:m.valSize[0]], pic)
	m.run(w)
	copy(p[:], w.slots[m.slotOf[m.output]][:5])
	return p, nil
}

// LooksNude reports whether the image's explicit classes (Porn + Hentai)
// reach nudityThreshold. "Sexy" alone never does.
func (m *nudityModel) LooksNude(img []byte) (bool, error) {
	p, err := m.Classify(img)
	if err != nil {
		return false, err
	}
	return p[nudityPorn]+p[nudityHentai] >= nudityThreshold, nil
}

// resizeInto writes the image as 224x224x3 floats in [0,1]: pixels / 255,
// then bilinear resize with align_corners=true, computed like tfjs's
// resizeBilinear (float64 arithmetic over float32 inputs). Only the pixels
// the resize needs are read.
func (m *nudityModel) resizeInto(dst []float32, pic image.Image) {
	b := pic.Bounds()
	oldH, oldW, n := b.Dy(), b.Dx(), m.size
	effIn := func(old int) float64 {
		if n > 1 {
			return float64(old - 1)
		}
		return float64(old)
	}
	effOut := float64(n - 1)
	if n <= 1 {
		effOut = float64(n)
	}
	rowRatio, colRatio := effIn(oldH)/effOut, effIn(oldW)/effOut
	px := func(x, y int) [3]float64 {
		var r, g, bl uint8
		switch im := pic.(type) {
		case *image.YCbCr:
			yi, ci := im.YOffset(b.Min.X+x, b.Min.Y+y), im.COffset(b.Min.X+x, b.Min.Y+y)
			r, g, bl = color.YCbCrToRGB(im.Y[yi], im.Cb[ci], im.Cr[ci])
		case *image.Gray:
			r = im.Pix[im.PixOffset(b.Min.X+x, b.Min.Y+y)]
			g, bl = r, r
		case *image.NRGBA:
			i := im.PixOffset(b.Min.X+x, b.Min.Y+y)
			r, g, bl = im.Pix[i], im.Pix[i+1], im.Pix[i+2]
		default:
			c := color.NRGBAModel.Convert(pic.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			r, g, bl = c.R, c.G, c.B
		}
		return [3]float64{
			float64(float32(r) / 255), float64(float32(g) / 255), float64(float32(bl) / 255),
		}
	}
	type src struct {
		lo, hi int
		frac   float64
	}
	cols := make([]src, n)
	for x := range n {
		f := colRatio * float64(x)
		lo := max(0, int(math.Floor(f)))
		cols[x] = src{lo, min(oldW-1, int(math.Ceil(f))), f - float64(lo)}
	}
	for y := range n {
		f := rowRatio * float64(y)
		top := max(0, int(math.Floor(f)))
		bot := min(oldH-1, int(math.Ceil(f)))
		rf := f - float64(top)
		for x, c := range cols {
			tl, tr, bl, br := px(c.lo, top), px(c.hi, top), px(c.lo, bot), px(c.hi, bot)
			o := dst[(y*n+x)*3 : (y*n+x)*3+3]
			for ch := range 3 {
				t := tl[ch] + (tr[ch]-tl[ch])*c.frac
				bo := bl[ch] + (br[ch]-bl[ch])*c.frac
				o[ch] = float32(t + (bo-t)*rf)
			}
		}
	}
}

func (m *nudityModel) run(w *nudityWork) {
	for i := range m.ops {
		op := &m.ops[i]
		in := w.slots[m.slotOf[op.in[0]]][:m.valSize[op.in[0]]]
		out := w.slots[m.slotOf[op.out]][:m.valSize[op.out]]
		switch op.kind {
		case opPad:
			nudityPad(op, in, out)
		case opConv:
			if op.k == 1 && op.stride == 1 {
				nudityPointwise(op, in, out)
			} else {
				nudityConv(op, in, out)
			}
		case opDWConv:
			nudityDepthwise(op, in, out)
		case opAdd:
			in2 := w.slots[m.slotOf[op.in[1]]][:m.valSize[op.in[1]]]
			for j := range out {
				out[j] = in[j] + in2[j]
			}
		case opAvgPool:
			nudityAvgPool(op, in, out)
		case opDense:
			nudityDense(op, in, out)
		}
	}
}

func relu6(v []float32) {
	for i, x := range v {
		if x < 0 {
			v[i] = 0
		} else if x > 6 {
			v[i] = 6
		}
	}
}

func nudityPad(op *nudityOp, in, out []float32) {
	clear(out)
	c := op.inC
	for y := range op.inH {
		src := in[y*op.inW*c : (y+1)*op.inW*c]
		d := ((y+op.padT)*op.outW + op.padL) * c
		copy(out[d:d+len(src)], src)
	}
}

// nudityPointwise is a 1x1 convolution: out[P][Co] = in[P][Ci] x W[Ci][Co]
// + bias, four pixels at a time so each weight row is loaded once per four.
func nudityPointwise(op *nudityOp, in, out []float32) {
	ci, co := op.inC, op.outC
	pixels := op.inH * op.inW
	p := 0
	for ; p+4 <= pixels; p += 4 {
		o0 := out[(p+0)*co : (p+1)*co]
		o1 := out[(p+1)*co : (p+2)*co]
		o2 := out[(p+2)*co : (p+3)*co]
		o3 := out[(p+3)*co : (p+4)*co]
		copy(o0, op.b)
		copy(o1, op.b)
		copy(o2, op.b)
		copy(o3, op.b)
		i0 := in[(p+0)*ci : (p+1)*ci]
		i1 := in[(p+1)*ci : (p+2)*ci]
		i2 := in[(p+2)*ci : (p+3)*ci]
		i3 := in[(p+3)*ci : (p+4)*ci]
		for c := range ci {
			a0, a1, a2, a3 := i0[c], i1[c], i2[c], i3[c]
			if a0 == 0 && a1 == 0 && a2 == 0 && a3 == 0 {
				continue // common after ReLU6
			}
			wr := op.w[c*co : c*co+co]
			o0, o1, o2, o3 := o0[:len(wr)], o1[:len(wr)], o2[:len(wr)], o3[:len(wr)]
			for j, wv := range wr {
				o0[j] += a0 * wv
				o1[j] += a1 * wv
				o2[j] += a2 * wv
				o3[j] += a3 * wv
			}
		}
	}
	for ; p < pixels; p++ {
		o := out[p*co : (p+1)*co]
		copy(o, op.b)
		for c, a := range in[p*ci : (p+1)*ci] {
			if a == 0 {
				continue
			}
			wr := op.w[c*co : c*co+co]
			o := o[:len(wr)]
			for j, wv := range wr {
				o[j] += a * wv
			}
		}
	}
	if op.relu6 {
		relu6(out)
	}
}

// nudityConv is a general KxK convolution (only the first layer uses it).
func nudityConv(op *nudityOp, in, out []float32) {
	ci, co, k := op.inC, op.outC, op.k
	for oy := range op.outH {
		for ox := range op.outW {
			o := out[(oy*op.outW+ox)*co : (oy*op.outW+ox+1)*co]
			copy(o, op.b)
			for ky := range k {
				iy := oy*op.stride + ky - op.padT
				if iy < 0 || iy >= op.inH {
					continue
				}
				for kx := range k {
					ix := ox*op.stride + kx - op.padL
					if ix < 0 || ix >= op.inW {
						continue
					}
					px := in[(iy*op.inW+ix)*ci : (iy*op.inW+ix+1)*ci]
					base := (ky*k + kx) * ci * co
					for c, a := range px {
						wr := op.w[base+c*co : base+c*co+co]
						o := o[:len(wr)]
						for j, wv := range wr {
							o[j] += a * wv
						}
					}
				}
			}
		}
	}
	if op.relu6 {
		relu6(out)
	}
}

// nudityDepthwise is a KxK depthwise convolution (multiplier 1).
func nudityDepthwise(op *nudityOp, in, out []float32) {
	c, k := op.inC, op.k
	for oy := range op.outH {
		for ox := range op.outW {
			o := out[(oy*op.outW+ox)*c : (oy*op.outW+ox+1)*c]
			copy(o, op.b)
			for ky := range k {
				iy := oy*op.stride + ky - op.padT
				if iy < 0 || iy >= op.inH {
					continue
				}
				for kx := range k {
					ix := ox*op.stride + kx - op.padL
					if ix < 0 || ix >= op.inW {
						continue
					}
					px := in[(iy*op.inW+ix)*c : (iy*op.inW+ix+1)*c]
					wr := op.w[(ky*k+kx)*c : (ky*k+kx+1)*c]
					px, o := px[:len(wr)], o[:len(wr)]
					for j, wv := range wr {
						o[j] += px[j] * wv
					}
				}
			}
		}
	}
	if op.relu6 {
		relu6(out)
	}
}

func nudityAvgPool(op *nudityOp, in, out []float32) {
	c, k := op.inC, op.k
	cnt := float32(k * k)
	for oy := range op.outH {
		for ox := range op.outW {
			o := out[(oy*op.outW+ox)*c : (oy*op.outW+ox+1)*c]
			clear(o)
			for ky := range k {
				for kx := range k {
					iy, ix := oy*op.stride+ky, ox*op.stride+kx
					px := in[(iy*op.inW+ix)*c : (iy*op.inW+ix+1)*c]
					for j := range o {
						o[j] += px[j]
					}
				}
			}
			for j := range o {
				o[j] /= cnt
			}
		}
	}
}

func nudityDense(op *nudityOp, in, out []float32) {
	n := len(op.b)
	copy(out, op.b)
	for i, a := range in {
		if a == 0 {
			continue
		}
		wr := op.w[i*n : (i+1)*n]
		o := out[:len(wr)]
		for j, wv := range wr {
			o[j] += a * wv
		}
	}
	switch op.act {
	case "relu":
		for j, v := range out {
			if v < 0 {
				out[j] = 0
			}
		}
	case "softmax":
		mx := out[0]
		for _, v := range out {
			mx = max(mx, v)
		}
		var sum float32
		for j, v := range out {
			out[j] = float32(math.Exp(float64(v - mx)))
			sum += out[j]
		}
		for j := range out {
			out[j] /= sum
		}
	}
}

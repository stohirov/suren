package parity

import (
	"image"
	"math"
)

const (
	ssimRadius = 5
	ssimSigma  = 1.5
)

func gaussKernel() []float64 {
	n := 2*ssimRadius + 1
	k := make([]float64, n)
	sum := 0.0
	for i := range k {
		d := float64(i - ssimRadius)
		k[i] = math.Exp(-(d * d) / (2 * ssimSigma * ssimSigma))
		sum += k[i]
	}
	for i := range k {
		k[i] /= sum
	}
	return k
}

func luma(img *image.RGBA) (w, h int, out []float64) {
	b := img.Rect
	w, h = b.Dx(), b.Dy()
	out = make([]float64, w*h)
	for y := range h {
		for x := range w {
			i := img.PixOffset(b.Min.X+x, b.Min.Y+y)
			r, g, bl := float64(img.Pix[i]), float64(img.Pix[i+1]), float64(img.Pix[i+2])
			out[y*w+x] = 0.2126*r + 0.7152*g + 0.0722*bl
		}
	}
	return w, h, out
}

func blur(src []float64, w, h int, k []float64) []float64 {
	tmp := make([]float64, w*h)
	for y := range h {
		for x := range w {
			s := 0.0
			for i, kv := range k {
				sx := min(max(x+i-ssimRadius, 0), w-1)
				s += kv * src[y*w+sx]
			}
			tmp[y*w+x] = s
		}
	}
	out := make([]float64, w*h)
	for y := range h {
		for x := range w {
			s := 0.0
			for i, kv := range k {
				sy := min(max(y+i-ssimRadius, 0), h-1)
				s += kv * tmp[sy*w+x]
			}
			out[y*w+x] = s
		}
	}
	return out
}

func ssim(a, b *image.RGBA) float64 {
	w, h, la := luma(a)
	_, _, lb := luma(b)

	sqa := make([]float64, len(la))
	sqb := make([]float64, len(lb))
	ab := make([]float64, len(la))
	for i := range la {
		sqa[i] = la[i] * la[i]
		sqb[i] = lb[i] * lb[i]
		ab[i] = la[i] * lb[i]
	}

	k := gaussKernel()
	mua := blur(la, w, h, k)
	mub := blur(lb, w, h, k)
	sa := blur(sqa, w, h, k)
	sb := blur(sqb, w, h, k)
	sab := blur(ab, w, h, k)

	const l = 255.0
	c1 := (0.01 * l) * (0.01 * l)
	c2 := (0.03 * l) * (0.03 * l)

	sum := 0.0
	for i := range mua {
		ma, mb := mua[i], mub[i]
		va := sa[i] - ma*ma
		vb := sb[i] - mb*mb
		cov := sab[i] - ma*mb
		num := (2*ma*mb + c1) * (2*cov + c2)
		den := (ma*ma + mb*mb + c1) * (va + vb + c2)
		sum += num / den
	}
	return sum / float64(len(mua))
}

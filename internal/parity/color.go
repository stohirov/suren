package parity

import "math"

func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func labF(t float64) float64 {
	const d = 6.0 / 29.0
	if t > d*d*d {
		return math.Cbrt(t)
	}
	return t/(3*d*d) + 4.0/29.0
}

func srgb8ToLab(r, g, b uint8) (l, a, bb float64) {
	rl := srgbToLinear(float64(r) / 255)
	gl := srgbToLinear(float64(g) / 255)
	bl := srgbToLinear(float64(b) / 255)

	x := 0.4124564*rl + 0.3575761*gl + 0.1804375*bl
	y := 0.2126729*rl + 0.7151522*gl + 0.0721750*bl
	z := 0.0193339*rl + 0.1191920*gl + 0.9503041*bl

	const xn, yn, zn = 0.95047, 1.0, 1.08883
	fx, fy, fz := labF(x/xn), labF(y/yn), labF(z/zn)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

func deltaE76(r1, g1, b1, r2, g2, b2 uint8) float64 {
	l1, a1, bb1 := srgb8ToLab(r1, g1, b1)
	l2, a2, bb2 := srgb8ToLab(r2, g2, b2)
	dl, da, db := l1-l2, a1-a2, bb1-bb2
	return math.Sqrt(dl*dl + da*da + db*db)
}

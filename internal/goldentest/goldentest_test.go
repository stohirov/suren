package goldentest

import (
	"image"
	"testing"
)

// Golden storage must be lossless for premultiplied data, or every gate is
// measuring the file format instead of the renderer.
func TestGoldenRoundTripIsLosslessForPremultiplied(t *testing.T) {
	for _, tc := range []struct {
		name   string
		opaque bool
	}{
		{"translucent", false},
		{"opaque", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := image.NewRGBA(image.Rect(0, 0, 256, 256))
			for a := range 256 {
				for v := range 256 {
					p := src.PixOffset(v, a)
					alpha := a
					if tc.opaque {
						alpha = 255
					}
					w := min(v, alpha)
					src.Pix[p], src.Pix[p+1], src.Pix[p+2], src.Pix[p+3] = uint8(w), uint8(w), uint8(w), uint8(alpha)
				}
			}

			data, err := encodeGolden(src)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := decodeGolden(data)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Rect != src.Rect {
				t.Fatalf("bounds = %v, want %v", got.Rect, src.Rect)
			}
			for i := range src.Pix {
				if got.Pix[i] != src.Pix[i] {
					t.Fatalf("byte %d: got %d, want %d (round-trip is lossy)", i, got.Pix[i], src.Pix[i])
				}
			}
		})
	}
}

package parity

import "image"

// NonTrivial reports whether an image is worth asserting on at all.
//
// Every gate in this package answers "do these two renders agree", and two blank
// canvases agree perfectly. A generated scene that renders nothing — pushed
// off-canvas by a transform, clipped to empty, or drawn in a color the backdrop
// already is — therefore passes every gate while testing none of them. Any
// harness that renders a scene it did not hand-write must screen for this first,
// so it lives here with the contract rather than being re-invented per suite.
//
// The bar is real coverage plus more than one distinct pixel: a uniformly-filled
// canvas is covered but exercises no edge, and edges are where the backends
// diverge.
func NonTrivial(img *image.RGBA) bool {
	if len(img.Pix) < 4 {
		return false
	}
	covered, distinct := 0, false
	first := [4]uint8{img.Pix[0], img.Pix[1], img.Pix[2], img.Pix[3]}
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i+3] > 0 {
			covered++
		}
		if [4]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]} != first {
			distinct = true
		}
	}
	return distinct && covered*100 > len(img.Pix)/4
}

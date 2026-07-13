package png

import (
	stdpng "image/png"
	"io"

	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/scene"
)

func Encode(w io.Writer, s *scene.Scene, pxW, pxH int) error {
	return stdpng.Encode(w, cpu.Render(s, pxW, pxH))
}

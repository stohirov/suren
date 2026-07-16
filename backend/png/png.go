package png

import (
	stdpng "image/png"
	"io"

	"github.com/stohirov/suren/backend/cpu"
	"github.com/stohirov/suren/scene"
)

func Encode(w io.Writer, s *scene.Scene, pxW, pxH int) error {
	return stdpng.Encode(w, cpu.Render(s, pxW, pxH))
}

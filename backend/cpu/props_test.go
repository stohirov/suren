package cpu

import (
	"testing"

	"github.com/stohirov/suren/internal/parity/props"
)

func TestPropsCPU(t *testing.T) {
	props.CheckAll(t, Render)
}

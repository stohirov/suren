package cpu

import (
	"testing"

	"github.com/stohirov/sukho/internal/parity/props"
)

func TestPropsCPU(t *testing.T) {
	props.CheckAll(t, Render)
}

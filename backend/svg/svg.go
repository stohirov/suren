package svg

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/scene"
)

func Encode(w io.Writer, s *scene.Scene, pxW, pxH int) error {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`+"\n", pxW, pxH, pxW, pxH)
	for _, n := range s.Nodes {
		col, ok := solidColor(n.Paint)
		if !ok {
			continue
		}
		writeNode(&b, n, col)
	}
	b.WriteString("</svg>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func writeNode(b *strings.Builder, n scene.Node, col paint.Color) {
	b.WriteString(`<path d="`)
	writeData(b, n.Path)
	b.WriteString(`"`)
	if m := n.Transform; m != geom.Identity() {
		fmt.Fprintf(b, ` transform="matrix(%s %s %s %s %s %s)"`,
			f(m.A), f(m.B), f(m.C), f(m.D), f(m.E), f(m.F))
	}
	if n.Stroke != nil {
		writeStroke(b, *n.Stroke, col)
	} else {
		writeFill(b, n.FillRule, col)
	}
	b.WriteString("/>\n")
}

func writeFill(b *strings.Builder, rule paint.FillRule, col paint.Color) {
	fmt.Fprintf(b, ` fill="%s"`, hex(col))
	if col.A < 1 {
		fmt.Fprintf(b, ` fill-opacity="%s"`, f(clamp01(col.A)))
	}
	if rule == paint.EvenOdd {
		b.WriteString(` fill-rule="evenodd"`)
	}
}

func writeStroke(b *strings.Builder, s paint.Stroke, col paint.Color) {
	fmt.Fprintf(b, ` fill="none" stroke="%s"`, hex(col))
	if col.A < 1 {
		fmt.Fprintf(b, ` stroke-opacity="%s"`, f(clamp01(col.A)))
	}
	fmt.Fprintf(b, ` stroke-width="%s"`, f(s.Width))
	if cap := capName(s.Cap); cap != "butt" {
		fmt.Fprintf(b, ` stroke-linecap="%s"`, cap)
	}
	if join := joinName(s.Join); join != "miter" {
		fmt.Fprintf(b, ` stroke-linejoin="%s"`, join)
	}
	if s.Join == path.MiterJoin && s.MiterLimit > 0 {
		fmt.Fprintf(b, ` stroke-miterlimit="%s"`, f(s.MiterLimit))
	}
	if len(s.Dashes) > 0 {
		parts := make([]string, len(s.Dashes))
		for i, d := range s.Dashes {
			parts[i] = f(d)
		}
		fmt.Fprintf(b, ` stroke-dasharray="%s"`, strings.Join(parts, ","))
		if s.DashOffset != 0 {
			fmt.Fprintf(b, ` stroke-dashoffset="%s"`, f(s.DashOffset))
		}
	}
}

func writeData(b *strings.Builder, p path.Path) {
	it := p.Iter()
	first := true
	for {
		v, pts, ok := it.Next()
		if !ok {
			break
		}
		if !first {
			b.WriteByte(' ')
		}
		first = false
		switch v {
		case path.MoveTo:
			fmt.Fprintf(b, "M%s %s", f(pts[0].X), f(pts[0].Y))
		case path.LineTo:
			fmt.Fprintf(b, "L%s %s", f(pts[0].X), f(pts[0].Y))
		case path.QuadTo:
			fmt.Fprintf(b, "Q%s %s %s %s", f(pts[0].X), f(pts[0].Y), f(pts[1].X), f(pts[1].Y))
		case path.CubicTo:
			fmt.Fprintf(b, "C%s %s %s %s %s %s",
				f(pts[0].X), f(pts[0].Y), f(pts[1].X), f(pts[1].Y), f(pts[2].X), f(pts[2].Y))
		case path.Close:
			b.WriteByte('Z')
		}
	}
}

func solidColor(p paint.Paint) (paint.Color, bool) {
	if s, ok := p.(paint.Solid); ok {
		return s.Color, true
	}
	return paint.Color{}, false
}

func capName(c path.Cap) string {
	switch c {
	case path.RoundCap:
		return "round"
	case path.SquareCap:
		return "square"
	default:
		return "butt"
	}
}

func joinName(j path.Join) string {
	switch j {
	case path.RoundJoin:
		return "round"
	case path.BevelJoin:
		return "bevel"
	default:
		return "miter"
	}
}

func hex(c paint.Color) string {
	ch := func(v float64) int { return int(clamp01(v)*255 + 0.5) }
	return fmt.Sprintf("#%02x%02x%02x", ch(c.R), ch(c.G), ch(c.B))
}

func clamp01(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	return v
}

func f(v float64) string {
	s := strconv.FormatFloat(v, 'f', 6, 64)
	if strings.ContainsRune(s, '.') {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

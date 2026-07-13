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
	var body, defs strings.Builder
	id := 0
	for _, n := range s.Nodes {
		ref, ok := paintRef(n.Paint, &defs, &id)
		if !ok {
			continue
		}
		clip := clipRef(n, &defs, &id)
		writeNode(&body, n, ref, clip)
	}

	var out strings.Builder
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`+"\n", pxW, pxH, pxW, pxH)
	if defs.Len() > 0 {
		out.WriteString("<defs>\n")
		out.WriteString(defs.String())
		out.WriteString("</defs>\n")
	}
	out.WriteString(body.String())
	out.WriteString("</svg>\n")
	_, err := io.WriteString(w, out.String())
	return err
}

type fillRef struct {
	solid bool
	color paint.Color
	url   string
}

func paintRef(p paint.Paint, defs *strings.Builder, id *int) (fillRef, bool) {
	switch g := p.(type) {
	case paint.Solid:
		return fillRef{solid: true, color: g.Color}, true
	case paint.LinearGradient:
		name := fmt.Sprintf("g%d", *id)
		*id++
		fmt.Fprintf(defs, `<linearGradient id="%s" gradientUnits="userSpaceOnUse" x1="%s" y1="%s" x2="%s" y2="%s">`+"\n",
			name, f(g.P0.X), f(g.P0.Y), f(g.P1.X), f(g.P1.Y))
		writeStops(defs, g.Stops)
		defs.WriteString("</linearGradient>\n")
		return fillRef{url: "url(#" + name + ")"}, true
	case paint.RadialGradient:
		name := fmt.Sprintf("g%d", *id)
		*id++
		fmt.Fprintf(defs, `<radialGradient id="%s" gradientUnits="userSpaceOnUse" cx="%s" cy="%s" r="%s">`+"\n",
			name, f(g.Center.X), f(g.Center.Y), f(g.Radius))
		writeStops(defs, g.Stops)
		defs.WriteString("</radialGradient>\n")
		return fillRef{url: "url(#" + name + ")"}, true
	default:
		return fillRef{}, false
	}
}

func writeStops(b *strings.Builder, stops []paint.Stop) {
	for _, s := range stops {
		fmt.Fprintf(b, `<stop offset="%s" stop-color="%s"`, f(s.Offset), hex(s.Color))
		if s.Color.A < 1 {
			fmt.Fprintf(b, ` stop-opacity="%s"`, f(clamp01(s.Color.A)))
		}
		b.WriteString("/>\n")
	}
}

func clipRef(n scene.Node, defs *strings.Builder, id *int) string {
	if n.Clip == nil {
		return ""
	}
	name := fmt.Sprintf("clip%d", *id)
	*id++
	fmt.Fprintf(defs, `<clipPath id="%s" clipPathUnits="userSpaceOnUse"><rect x="%s" y="%s" width="%s" height="%s"/></clipPath>`+"\n",
		name, f(n.Clip.Min.X), f(n.Clip.Min.Y), f(n.Clip.Width()), f(n.Clip.Height()))
	return "url(#" + name + ")"
}

func writeNode(b *strings.Builder, n scene.Node, ref fillRef, clip string) {
	if clip != "" {
		fmt.Fprintf(b, `<g clip-path="%s">`, clip)
	}
	b.WriteString(`<path d="`)
	writeData(b, n.Path)
	b.WriteString(`"`)
	if m := n.Transform; m != geom.Identity() {
		fmt.Fprintf(b, ` transform="matrix(%s %s %s %s %s %s)"`,
			f(m.A), f(m.B), f(m.C), f(m.D), f(m.E), f(m.F))
	}
	if n.Stroke != nil {
		writeStroke(b, *n.Stroke, ref)
	} else {
		writeFill(b, n.FillRule, ref)
	}
	b.WriteString("/>")
	if clip != "" {
		b.WriteString("</g>")
	}
	b.WriteString("\n")
}

func writeFill(b *strings.Builder, rule paint.FillRule, ref fillRef) {
	writePaint(b, "fill", ref)
	if rule == paint.EvenOdd {
		b.WriteString(` fill-rule="evenodd"`)
	}
}

func writeStroke(b *strings.Builder, s paint.Stroke, ref fillRef) {
	b.WriteString(` fill="none"`)
	writePaint(b, "stroke", ref)
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

func writePaint(b *strings.Builder, attr string, ref fillRef) {
	if !ref.solid {
		fmt.Fprintf(b, ` %s="%s"`, attr, ref.url)
		return
	}
	fmt.Fprintf(b, ` %s="%s"`, attr, hex(ref.color))
	if ref.color.A < 1 {
		fmt.Fprintf(b, ` %s-opacity="%s"`, attr, f(clamp01(ref.color.A)))
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

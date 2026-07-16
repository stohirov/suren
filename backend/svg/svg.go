package svg

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/scene"
)

// Dropped names one feature of one node that did not survive the export.
//
// Node indexes into the scene's own Nodes slice, so a caller can find the thing
// it wrote rather than guess from the description.
type Dropped struct {
	Node    int
	Feature string
}

// Report lists what Encode could not express. It is the answer to the question
// this backend used to leave unasked: SVG cannot represent everything a
// scene.Node can say, and a caller that is not told has no way to find out —
// the output is valid SVG either way, just not the scene it asked for.
//
// An empty report means the export was faithful. See Lossy.
type Report struct {
	Dropped []Dropped
}

// Lossy reports whether anything was dropped. A false answer is the strong
// claim: every field of every node was either emitted or is not a rendering
// property. See TestEverySceneNodeFieldHasAnSVGContract, which is what keeps
// that claim true as scene.Node grows.
func (r Report) Lossy() bool { return len(r.Dropped) > 0 }

func (r *Report) drop(node int, feature string) {
	r.Dropped = append(r.Dropped, Dropped{Node: node, Feature: feature})
}

// Encode writes s as an SVG document and reports what it could not express.
//
// It does not error on an inexpressible node: the document is still written and
// the node is still dropped, exactly as before — the difference is that the
// caller is now told. Errors are reserved for the io.Writer.
func Encode(w io.Writer, s *scene.Scene, pxW, pxH int) (Report, error) {
	var rep Report
	var body, defs strings.Builder
	id := 0
	for i, n := range s.Nodes {
		ref, ok := paintRef(n.Paint, &defs, &id)
		if !ok {
			rep.drop(i, paintName(n.Paint))
			continue
		}
		blend := blendFor(&rep, i, n)
		clips := clipRefs(n, &defs, &id)
		writeNode(&body, n, ref, clips, blend)
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
	return rep, err
}

// Why an image paint is a FORMAT LIMIT and not an omission (Phase 17).
//
// This one looks expressible, which is exactly why it gets a paragraph. SVG has
// <pattern>, a pattern can hold an <image>, and CSS has image-rendering — so the
// obvious reading is that a Repeat-mode image is "expressible and simply not
// emitted", the class Phase 24 put mix-blend-mode in. That reading is wrong, and
// checking it rather than asserting it is that phase's whole lesson.
//
// The load-bearing reason is the FILTER, and it disqualifies every image node
// regardless of edge mode. This phase's entire correctness content is that the
// kernel is pinned in shared code rather than delegated to a driver (see
// paint.Filter). CSS Images 3 declines to pin it, in as many words:
// image-rendering:auto is "UA-dependent"; smooth says only that "scaling
// algorithms that 'smooth' colors are acceptable, such as bilinear interpolation"
// — an example, not a mandate; crisp-edges "may be scaled using nearest neighbor
// or any other UA-chosen algorithm"; and pixelated, the value that sounds like
// Nearest, "allows minor smoothing as necessary". Not one of them names a
// function. Emitting an image would hand the sampling back to the user agent —
// the very thing the shader refuses to hand to a sampler.
//
// The edge modes fail independently, and the detail is worth recording: SVG's
// <pattern> tiles "to infinity in X and Y" with no alternative, so Clamp and
// Mirror have no counterpart. spreadMethod names exactly pad/reflect/repeat —
// this paint's three edge modes, under other names — and it is a GRADIENT
// attribute that patterns do not take. The vocabulary exists in the format and is
// not available where it would be needed.
//
// So an emitted image would be a node that is present, plausible, and wrong,
// which is the failure Phase 24 exists to remove rather than relabel. Reported and
// dropped, beside conic and mesh.
//
// Note the coupling, which is the same shape as that phase's blend/composite
// finding: Repeat alone IS tileable, so a per-row reading of this decision would
// "fix" the repeat case and ship UA-defined filtering under it. The filter and the
// edge mode are not independent rows.

// paintName describes a paint the format cannot express, for the report.
func paintName(p paint.Paint) string {
	switch p.(type) {
	case paint.ConicGradient:
		return "conic paint"
	case paint.MeshGradient:
		return "mesh paint"
	case paint.Image:
		return "image paint"
	default:
		return fmt.Sprintf("paint %T", p)
	}
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

// blendFor decides what goes on the mix-blend-mode axis and reports what cannot.
//
// The two axes are independent in the scene model and COUPLED on the way out,
// which is the whole subtlety of this row and the reason Phase 24's table — one
// row per feature, each independently fixable — could not state it.
//
// mix-blend-mode implies source-over compositing. CSS has no way to say
// "multiply, but XOR-composited". So a node whose Composite is not SrcOver
// cannot have its blend emitted either: mix-blend-mode:multiply under Xor
// renders Multiply-OVER, which is not a partial answer but a different wrong
// one. Both axes drop together and are reported together. Fixing the blend row
// alone — the obvious reading of the table — would have shipped exactly that.
func blendFor(rep *Report, i int, n scene.Node) string {
	name, known := blendName(n.Op)
	if !known {
		// A blend mode with no CSS name would export as Normal, silently. That
		// is the original bug; refuse to reintroduce it for a mode added later.
		rep.drop(i, fmt.Sprintf("blend=%v (no CSS mix-blend-mode name)", n.Op))
		return ""
	}
	if n.Composite != paint.SrcOver {
		f := "composite=" + compositeName(n.Composite)
		if n.Op != paint.Normal {
			f += " (+blend=" + name + ", suppressed: mix-blend-mode implies source-over)"
		}
		rep.drop(i, f)
		return ""
	}
	if n.Op == paint.Normal {
		return ""
	}
	return name
}

// blendName maps the W3C separable set onto its CSS spelling. It is total by
// construction over today's enum and answers false for anything else, because a
// mode this does not know must be reported rather than quietly flattened.
func blendName(m paint.BlendMode) (string, bool) {
	switch m {
	case paint.Normal:
		return "normal", true
	case paint.Multiply:
		return "multiply", true
	case paint.Screen:
		return "screen", true
	case paint.Overlay:
		return "overlay", true
	case paint.Darken:
		return "darken", true
	case paint.Lighten:
		return "lighten", true
	case paint.ColorDodge:
		return "color-dodge", true
	case paint.ColorBurn:
		return "color-burn", true
	case paint.HardLight:
		return "hard-light", true
	case paint.SoftLight:
		return "soft-light", true
	case paint.Difference:
		return "difference", true
	case paint.Exclusion:
		return "exclusion", true
	default:
		return "", false
	}
}

// compositeName is for the REPORT only — SVG cannot apply these, so the name
// exists to tell a caller what it lost. Spellings match the corpus entries.
func compositeName(op paint.CompositeOp) string {
	switch op {
	case paint.SrcOver:
		return "src-over"
	case paint.Clear:
		return "clear"
	case paint.Src:
		return "src"
	case paint.Dst:
		return "dst"
	case paint.DstOver:
		return "dst-over"
	case paint.SrcIn:
		return "src-in"
	case paint.DstIn:
		return "dst-in"
	case paint.SrcOut:
		return "src-out"
	case paint.DstOut:
		return "dst-out"
	case paint.SrcAtop:
		return "src-atop"
	case paint.DstAtop:
		return "dst-atop"
	case paint.Xor:
		return "xor"
	default:
		return fmt.Sprintf("composite(%d)", op)
	}
}

// clipRefs builds one clipPath per clip on the node: the bbox rect (Node.Clip)
// first, then each path clip (Node.Clips) in order.
//
// They are returned separately rather than merged because SVG intersects clips
// by NESTING them. Two children inside a single <clipPath> UNION — the opposite
// of what a clip stack means, and a mistake that would render as too MUCH
// surviving rather than too little.
//
// The geometry is already in device space: render.Canvas.ClipPath stores
// p.Transform(ctm), and ClipRect stores a device bbox. The wrapping <g> carries
// no transform, so its user space is the root's, and clipPathUnits=userSpaceOnUse
// resolves there. The node's own transform must NOT be applied to these — it
// belongs to the <path> inside.
func clipRefs(n scene.Node, defs *strings.Builder, id *int) []string {
	var refs []string
	if n.Clip != nil {
		name := fmt.Sprintf("clip%d", *id)
		*id++
		fmt.Fprintf(defs, `<clipPath id="%s" clipPathUnits="userSpaceOnUse"><rect x="%s" y="%s" width="%s" height="%s"/></clipPath>`+"\n",
			name, f(n.Clip.Min.X), f(n.Clip.Min.Y), f(n.Clip.Width()), f(n.Clip.Height()))
		refs = append(refs, "url(#"+name+")")
	}
	for _, cl := range n.Clips {
		name := fmt.Sprintf("clip%d", *id)
		*id++
		fmt.Fprintf(defs, `<clipPath id="%s" clipPathUnits="userSpaceOnUse"><path d="`, name)
		writeData(defs, cl.Path)
		defs.WriteString(`"`)
		if cl.Rule == paint.EvenOdd {
			// clip-rule, not fill-rule: inside a clipPath the winding rule has its
			// own property name and fill-rule is ignored, so the wrong one here
			// silently changes which region survives.
			defs.WriteString(` clip-rule="evenodd"`)
		}
		defs.WriteString("/></clipPath>\n")
		refs = append(refs, "url(#"+name+")")
	}
	return refs
}

func writeNode(b *strings.Builder, n scene.Node, ref fillRef, clips []string, blend string) {
	for _, c := range clips {
		fmt.Fprintf(b, `<g clip-path="%s">`, c)
	}
	b.WriteString(`<path d="`)
	writeData(b, n.Path)
	b.WriteString(`"`)
	if m := n.Transform; m != geom.Identity() {
		fmt.Fprintf(b, ` transform="matrix(%s %s %s %s %s %s)"`,
			f(m.A), f(m.B), f(m.C), f(m.D), f(m.E), f(m.F))
	}
	if blend != "" {
		// The style form rather than the presentation attribute: mix-blend-mode is
		// only a presentation attribute in SVG 2, while style= works anywhere
		// blending is supported at all.
		fmt.Fprintf(b, ` style="mix-blend-mode:%s"`, blend)
	}
	if n.Stroke != nil {
		writeStroke(b, *n.Stroke, ref)
	} else {
		writeFill(b, n.FillRule, ref)
	}
	b.WriteString("/>")
	for range clips {
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

package path

import "github.com/stohirov/suren/geom"

const kappa = 0.5522847498307936

func Rect(r geom.Rect) Path {
	var p Path
	p.MoveTo(r.Min)
	p.LineTo(geom.Pt(r.Max.X, r.Min.Y))
	p.LineTo(r.Max)
	p.LineTo(geom.Pt(r.Min.X, r.Max.Y))
	p.Close()
	return p
}

func Circle(center geom.Point, r float64) Path {
	return Ellipse(center, r, r)
}

func Ellipse(center geom.Point, rx, ry float64) Path {
	cx, cy := center.X, center.Y
	kx, ky := rx*kappa, ry*kappa
	var p Path
	p.MoveTo(geom.Pt(cx+rx, cy))
	p.CubicTo(geom.Pt(cx+rx, cy+ky), geom.Pt(cx+kx, cy+ry), geom.Pt(cx, cy+ry))
	p.CubicTo(geom.Pt(cx-kx, cy+ry), geom.Pt(cx-rx, cy+ky), geom.Pt(cx-rx, cy))
	p.CubicTo(geom.Pt(cx-rx, cy-ky), geom.Pt(cx-kx, cy-ry), geom.Pt(cx, cy-ry))
	p.CubicTo(geom.Pt(cx+kx, cy-ry), geom.Pt(cx+rx, cy-ky), geom.Pt(cx+rx, cy))
	p.Close()
	return p
}

func RoundedRect(r geom.Rect, rx, ry float64) Path {
	rx = min(rx, r.Width()/2)
	ry = min(ry, r.Height()/2)
	if rx <= 0 || ry <= 0 {
		return Rect(r)
	}
	kx, ky := rx*kappa, ry*kappa
	x0, y0, x1, y1 := r.Min.X, r.Min.Y, r.Max.X, r.Max.Y
	var p Path
	p.MoveTo(geom.Pt(x0+rx, y0))
	p.LineTo(geom.Pt(x1-rx, y0))
	p.CubicTo(geom.Pt(x1-rx+kx, y0), geom.Pt(x1, y0+ry-ky), geom.Pt(x1, y0+ry))
	p.LineTo(geom.Pt(x1, y1-ry))
	p.CubicTo(geom.Pt(x1, y1-ry+ky), geom.Pt(x1-rx+kx, y1), geom.Pt(x1-rx, y1))
	p.LineTo(geom.Pt(x0+rx, y1))
	p.CubicTo(geom.Pt(x0+rx-kx, y1), geom.Pt(x0, y1-ry+ky), geom.Pt(x0, y1-ry))
	p.LineTo(geom.Pt(x0, y0+ry))
	p.CubicTo(geom.Pt(x0, y0+ry-ky), geom.Pt(x0+rx-kx, y0), geom.Pt(x0+rx, y0))
	p.Close()
	return p
}

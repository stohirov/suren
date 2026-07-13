package paint

import "testing"

func TestColorRGBAOpaque(t *testing.T) {
	r, g, b, a := RGB(1, 0.5, 0).RGBA()
	if a != 0xffff {
		t.Fatalf("alpha = %d, want 65535", a)
	}
	if r != 0xffff {
		t.Errorf("r = %d, want 65535", r)
	}
	if g < 0x7f00 || g > 0x8100 {
		t.Errorf("g = %d, want ~32768", g)
	}
	if b != 0 {
		t.Errorf("b = %d, want 0", b)
	}
}

func TestColorRGBAPremultiplied(t *testing.T) {
	r, _, _, a := RGBA(1, 1, 1, 0.5).RGBA()
	if a < 0x7f00 || a > 0x8100 {
		t.Fatalf("alpha = %d, want ~32768", a)
	}
	if r < 0x7f00 || r > 0x8100 {
		t.Errorf("premultiplied r = %d, want ~32768", r)
	}
	if r > a {
		t.Errorf("premultiplied r (%d) must not exceed alpha (%d)", r, a)
	}
}

func TestColorClamps(t *testing.T) {
	r, _, _, a := RGBA(2, 0, 0, 2).RGBA()
	if r != 0xffff || a != 0xffff {
		t.Fatalf("out-of-range color should clamp: r=%d a=%d", r, a)
	}
}

func TestInterp(t *testing.T) {
	stops := []Stop{
		{Offset: 0, Color: RGB(0, 0, 0)},
		{Offset: 0.5, Color: RGB(1, 0, 0)},
		{Offset: 1, Color: RGB(1, 1, 1)},
	}
	eq := func(a, b float64) bool { d := a - b; return d < 1e-9 && d > -1e-9 }

	if c := Interp(stops, 0.5); !eq(c.R, 1) || !eq(c.G, 0) || !eq(c.B, 0) {
		t.Errorf("at stop 0.5 got %+v, want exact red", c)
	}
	if c := Interp(stops, 0.25); !eq(c.R, 0.5) || !eq(c.G, 0) {
		t.Errorf("midpoint of first span got %+v, want R=0.5", c)
	}
	if c := Interp(stops, 0.75); !eq(c.G, 0.5) || !eq(c.B, 0.5) || !eq(c.R, 1) {
		t.Errorf("midpoint of second span got %+v", c)
	}
	if c := Interp(stops, -1); !eq(c.R, 0) || !eq(c.B, 0) {
		t.Errorf("below range should clamp to first stop, got %+v", c)
	}
	if c := Interp(stops, 2); !eq(c.R, 1) || !eq(c.G, 1) || !eq(c.B, 1) {
		t.Errorf("above range should clamp to last stop, got %+v", c)
	}
	if c := Interp(nil, 0.5); c != (Color{}) {
		t.Errorf("empty stops should give zero color, got %+v", c)
	}
}

func TestStrokeDash(t *testing.T) {
	s := Stroke{Width: 2, Dashes: []float64{4, 2}, DashOffset: 1}
	d, ok := s.Dash()
	if !ok {
		t.Fatal("expected a dash")
	}
	if d.Phase != 1 || len(d.Pattern) != 2 {
		t.Errorf("unexpected dash %+v", d)
	}
	if _, ok := (Stroke{Width: 2}).Dash(); ok {
		t.Error("no dash expected when Dashes empty")
	}
}

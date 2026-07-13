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

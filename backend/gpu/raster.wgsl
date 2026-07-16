struct Seg { x0: f32, y0: f32, x1: f32, y1: f32 };

struct Node {
  segStart: u32, segCount: u32, rule: u32, kind: u32,
  cr: f32, cg: f32, cb: f32, ca: f32,
  g0x: f32, g0y: f32, g1x: f32, g1y: f32,
  stopStart: u32, stopCount: u32, hasClip: u32, flags: u32,
  clx0: f32, cly0: f32, clx1: f32, cly1: f32,
  bbx0: f32, bby0: f32, bbx1: f32, bby1: f32,
  m0: f32, m1: f32, m2: f32, m3: f32, m4: f32, m5: f32,
  clipStart: u32, clipCount: u32,
};

struct ClipRec { segStart: u32, segCount: u32, rule: u32, pad: u32 };

struct Dims { w: u32, h: u32, nx: u32, ny: u32 };

// Mirrors encode.go's Stop: a colour at a sample. off is the parameter for a
// gradient (x/y unused); x/y are the point for a mesh vertex (off unused), three
// consecutive records to a triangle. All-scalar, matching the Go struct's tight
// 4-byte packing, so the upload maps straight on.
struct Stop { off: f32, r: f32, g: f32, b: f32, a: f32, x: f32, y: f32 };

const TILE: i32 = 16;

// Mirrors paint.MeshEps.
const MESH_EPS: f32 = 1e-5;

@group(0) @binding(0) var out_tex: texture_storage_2d<rgba8unorm, write>;
@group(0) @binding(1) var<storage, read> segs: array<Seg>;
@group(0) @binding(2) var<storage, read> nodes: array<Node>;
@group(0) @binding(3) var<uniform> dims: Dims;
@group(0) @binding(4) var<storage, read> tileOffsets: array<u32>;
@group(0) @binding(5) var<storage, read> tileNodes: array<u32>;
@group(0) @binding(6) var<storage, read> stops: array<Stop>;
@group(0) @binding(7) var<storage, read> tileSegOff: array<u32>;
@group(0) @binding(8) var<storage, read> tileSegIdx: array<u32>;
@group(0) @binding(9) var<storage, read> clips: array<ClipRec>;

fn interpStops(start: u32, count: u32, t: f32) -> vec4<f32> {
  if (count == 0u) { return vec4<f32>(0.0); }
  let s0 = stops[start];
  if (t <= s0.off) { return vec4<f32>(s0.r, s0.g, s0.b, s0.a); }
  let last = stops[start + count - 1u];
  if (t >= last.off) { return vec4<f32>(last.r, last.g, last.b, last.a); }
  for (var i = 1u; i < count; i = i + 1u) {
    let hi = stops[start + i];
    if (t <= hi.off) {
      let lo = stops[start + i - 1u];
      let span = hi.off - lo.off;
      if (span <= 0.0) { return vec4<f32>(hi.r, hi.g, hi.b, hi.a); }
      let u = (t - lo.off) / span;
      return vec4<f32>(
        lo.r + (hi.r - lo.r) * u,
        lo.g + (hi.g - lo.g) * u,
        lo.b + (hi.b - lo.b) * u,
        lo.a + (hi.a - lo.a) * u,
      );
    }
  }
  return vec4<f32>(last.r, last.g, last.b, last.a);
}

fn gradColor(nd: Node, px: f32, py: f32) -> vec4<f32> {
  let qx = nd.m0 * px + nd.m2 * py + nd.m4;
  let qy = nd.m1 * px + nd.m3 * py + nd.m5;
  var t = 0.0;
  if (nd.kind == 1u) {
    let dx = nd.g1x - nd.g0x;
    let dy = nd.g1y - nd.g0y;
    let len2 = dx * dx + dy * dy;
    if (len2 > 0.0) {
      t = ((qx - nd.g0x) * dx + (qy - nd.g0y) * dy) / len2;
    }
  } else if (nd.kind == 2u) {
    let radius = nd.g1x;
    if (radius > 0.0) {
      let dx = qx - nd.g0x;
      let dy = qy - nd.g0y;
      t = sqrt(dx * dx + dy * dy) / radius;
    }
  } else {
    // Conic. Mirrors backend/cpu's shader.go term for term: the same subtraction
    // order, and a DIVISION by 2π rather than a multiply by its reciprocal, which
    // is a different f32 number.
    //
    // The dx==0 && dy==0 guard is not defensive, it is the contract: atan2(0,0)
    // is 0 in Go and undefined in WGSL, so the centre pixel is pinned to t=0 on
    // both sides instead of resting on a driver's corner case.
    let dx = qx - nd.g0x;
    let dy = qy - nd.g0y;
    if (dx != 0.0 || dy != 0.0) {
      let a = (atan2(dy, dx) - nd.g1x) / (2.0 * 3.141592653589793);
      t = a - floor(a);
    }
  }
  let c = interpStops(nd.stopStart, nd.stopCount, t);
  let ca = clamp(c.w, 0.0, 1.0);
  // Deliberately NOT rounded to 16 bits, though the CPU's gradient is: cpu's
  // gradShader goes through paint.Color.RGBA(), which returns 16-bit
  // premultiplied, then divides by 257 — so the reference never sees a
  // continuous gradient colour. Mirroring that here was measured and reverted.
  // It moved nothing (gradient stayed Δ=1, and a gradient read through
  // ColorDodge's unbounded derivative stayed Δ=0), because 16-bit error sits
  // ~130x below the 8-bit rounding decision. It is also an artifact of reusing
  // Go's color.Color convention rather than a choice the reference made, and
  // matching an artifact costs per-pixel work to enshrine an accident. Recorded
  // as a known semantic difference that lives below the floor.
  return vec4<f32>(clamp(c.x, 0.0, 1.0) * ca, clamp(c.y, 0.0, 1.0) * ca, clamp(c.z, 0.0, 1.0) * ca, ca);
}

// meshColor is a verbatim port of paint.MeshAt — same term order, same
// first-match rule, same skip of a degenerate triangle rather than a division by
// its vanishing area. The two state one function and must not drift; a mismatch
// here is not a rounding artifact but the backends shading different colours.
//
// Premultiplication matches gradColor's: the vertex colours are STRAIGHT, so the
// barycentric combination is taken on straight channels and the result is
// premultiplied once at the end, which is what the reference's Color.RGBA() does.
fn meshColor(nd: Node, px: f32, py: f32) -> vec4<f32> {
  let qx = nd.m0 * px + nd.m2 * py + nd.m4;
  let qy = nd.m1 * px + nd.m3 * py + nd.m5;
  let end = nd.stopStart + nd.stopCount;
  var i = nd.stopStart;
  loop {
    if (i + 3u > end) { break; }
    let a = stops[i];
    let b = stops[i + 1u];
    let c = stops[i + 2u];
    let d = (b.y - c.y) * (a.x - c.x) + (c.x - b.x) * (a.y - c.y);
    if (d != 0.0) {
      let l0 = ((b.y - c.y) * (qx - c.x) + (c.x - b.x) * (qy - c.y)) / d;
      let l1 = ((c.y - a.y) * (qx - c.x) + (a.x - c.x) * (qy - c.y)) / d;
      let l2 = 1.0 - l0 - l1;
      // MESH_EPS mirrors paint.MeshEps. Without it two triangles sharing an edge
      // both reject a point on it and the mesh cracks along every interior
      // diagonal; see that constant for the measurement.
      if (l0 >= -MESH_EPS && l1 >= -MESH_EPS && l2 >= -MESH_EPS) {
        let cr = l0 * a.r + l1 * b.r + l2 * c.r;
        let cg = l0 * a.g + l1 * b.g + l2 * c.g;
        let cb = l0 * a.b + l1 * b.b + l2 * c.b;
        let ca = clamp(l0 * a.a + l1 * b.a + l2 * c.a, 0.0, 1.0);
        return vec4<f32>(clamp(cr, 0.0, 1.0) * ca, clamp(cg, 0.0, 1.0) * ca, clamp(cb, 0.0, 1.0) * ca, ca);
      }
    }
    i = i + 3u;
  }
  // Outside every triangle. The drop to transparent is a DISCONTINUITY at the
  // mesh's silhouette, and it is why a scene must extend its mesh past the path
  // it fills — see paint.MeshGradient.
  return vec4<f32>(0.0);
}

fn coverage(wv: f32, rule: u32) -> f32 {
  var a = abs(wv);
  if (rule == 1u) {
    a = a - 2.0 * floor(a * 0.5);
    if (a > 1.0) { a = 2.0 - a; }
    return a;
  }
  return min(a, 1.0);
}

fn hardLight(cb: f32, cs: f32) -> f32 {
  if (cs <= 0.5) { return 2.0 * cb * cs; }
  return cb + (2.0 * cs - 1.0) - cb * (2.0 * cs - 1.0);
}

fn softLight(cb: f32, cs: f32) -> f32 {
  if (cs <= 0.5) { return cb - (1.0 - 2.0 * cs) * cb * (1.0 - cb); }
  var d = sqrt(cb);
  if (cb <= 0.25) { d = ((16.0 * cb - 12.0) * cb + 4.0) * cb; }
  return cb + (2.0 * cs - 1.0) * (d - cb);
}

fn blendCh(mode: u32, cb: f32, cs: f32) -> f32 {
  switch mode {
    case 1u: { return cb * cs; }
    case 2u: { return cb + cs - cb * cs; }
    case 3u: { return hardLight(cs, cb); }
    case 4u: { return min(cb, cs); }
    case 5u: { return max(cb, cs); }
    case 6u: {
      if (cb <= 0.0) { return 0.0; }
      if (cs >= 1.0) { return 1.0; }
      return min(1.0, cb / (1.0 - cs));
    }
    case 7u: {
      if (cb >= 1.0) { return 1.0; }
      if (cs <= 0.0) { return 0.0; }
      return 1.0 - min(1.0, (1.0 - cb) / cs);
    }
    case 8u: { return hardLight(cb, cs); }
    case 9u: { return softLight(cb, cs); }
    case 10u: { return abs(cb - cs); }
    case 11u: { return cb + cs - 2.0 * cb * cs; }
    default: { return cs; }
  }
}

// quant8 pins the 8-bit quantization to the CPU reference's rule: clamp8 in
// raster/fill.go is uint8(v + 0.5), round-half-up. Leaving the rounding to the
// driver's f32->u8 conversion on the rgba8unorm store would make it
// implementation-defined, and an implementation-defined rounding rule cannot be
// half of a parity contract — Vulkan and DX12 need not round like Metal.
fn quant8(v: vec4<f32>) -> vec4<f32> {
  let c = clamp(v, vec4<f32>(0.0), vec4<f32>(1.0));
  return floor(c * 255.0 + 0.5) * (1.0 / 255.0);
}

// pdCoeff is a verbatim port of raster.Coefficients — Porter-Duff's (Fa, Fb) for
// each operator. The two tables state the same twelve rows and must not drift:
// a mismatch here is not a rounding artifact but the two backends computing
// different functions, which no tolerance in this tree should absorb.
fn pdCoeff(op: u32, sA: f32, bA: f32) -> vec2<f32> {
  switch op {
    case 1u: { return vec2<f32>(0.0, 0.0); }              // Clear
    case 2u: { return vec2<f32>(1.0, 0.0); }              // Src
    case 3u: { return vec2<f32>(0.0, 1.0); }              // Dst
    case 4u: { return vec2<f32>(1.0 - bA, 1.0); }         // DstOver
    case 5u: { return vec2<f32>(bA, 0.0); }               // SrcIn
    case 6u: { return vec2<f32>(0.0, sA); }               // DstIn
    case 7u: { return vec2<f32>(1.0 - bA, 0.0); }         // SrcOut
    case 8u: { return vec2<f32>(0.0, 1.0 - sA); }         // DstOut
    case 9u: { return vec2<f32>(bA, 1.0 - sA); }          // SrcAtop
    case 10u: { return vec2<f32>(1.0 - bA, sA); }         // DstAtop
    case 11u: { return vec2<f32>(1.0 - bA, 1.0 - sA); }   // Xor
    default: { return vec2<f32>(1.0, 1.0 - sA); }         // SrcOver
  }
}

// porterDuff mirrors raster/fill.go's function of the same name, term for term
// and in the same order — the ordering audit Phase 13 ran on the blend path
// applies here too, since f32 addition is not associative and a reordered sum is
// a different number.
//
// cov lerps between the composited result and the untouched backdrop rather than
// scaling sA. See the CPU comment for why: coverage is the fraction of the pixel
// the operator applies to, not the source's alpha, and folding it into sA would
// make a half-covered Clear erase the whole pixel.
fn porterDuff(mode: u32, comp: u32, dst: vec4<f32>, src: vec4<f32>, cov: f32) -> vec4<f32> {
  let sA = src.w;
  let bA = dst.w;
  var cs = vec3<f32>(0.0);
  if (sA > 0.0) { cs = src.xyz / sA; }
  var cb = vec3<f32>(0.0);
  if (bA > 0.0) { cb = dst.xyz / bA; }

  let bl = vec3<f32>(blendCh(mode, cb.x, cs.x),
                     blendCh(mode, cb.y, cs.y),
                     blendCh(mode, cb.z, cs.z));
  let br = (1.0 - bA) * cs + bA * bl;

  let f = pdCoeff(comp, sA, bA);
  let co = sA * f.x * br + bA * f.y * cb;
  let ao = sA * f.x + bA * f.y;

  let inv = 1.0 - cov;
  return vec4<f32>(co * cov + dst.xyz * inv, ao * cov + bA * inv);
}

// composite unpacks both axes from the node's flags word: blend mode in bits
// 0-3, Porter-Duff operator in bits 4-7 (encode.go packFlags).
fn composite(flags: u32, dst: vec4<f32>, src: vec4<f32>, alpha: f32) -> vec4<f32> {
  let mode = flags & 0xFu;
  let comp = (flags >> 4u) & 0xFu;
  if (comp != 0u) {
    return porterDuff(mode, comp, dst, src, alpha);
  }
  // SrcOver keeps its pre-Phase-15 arithmetic, matching the CPU's decision to do
  // the same: porterDuff is algebraically equal for it but not bit-equal, and
  // routing SrcOver through the general form would move every AA edge in the
  // tree by an LSB to no end.
  let fa = src.w * alpha;
  if (mode == 0u) {
    let invc = 1.0 - fa;
    return vec4<f32>(src.x * alpha + dst.x * invc,
                     src.y * alpha + dst.y * invc,
                     src.z * alpha + dst.z * invc,
                     fa + dst.w * invc);
  }
  let sA = fa;
  let bA = dst.w;
  var cs = vec3<f32>(0.0);
  if (src.w > 0.0) { cs = src.xyz / src.w; }
  var cb = vec3<f32>(0.0);
  if (bA > 0.0) { cb = dst.xyz / bA; }
  let bl = vec3<f32>(blendCh(mode, cb.x, cs.x),
                     blendCh(mode, cb.y, cs.y),
                     blendCh(mode, cb.z, cs.z));
  let co = sA * (1.0 - bA) * cs + sA * bA * bl + (1.0 - sA) * bA * cb;
  let ao = sA + bA * (1.0 - sA);
  return vec4<f32>(co, ao);
}

// The backdrop is a SUMMATION-ORDER divergence from the CPU reference, and a
// deliberate one. raster.go's Sweep accumulates one running total left-to-right
// across the whole row; here every contribution left of the tile collapses into
// this scalar in SEGMENT order, and the tile's sweep seeds its accumulator with
// it. The two are equal in exact arithmetic and not in f32/f64: addition is not
// associative. Fixing it would mean sweeping whole rows, which is precisely the
// serialization the 2D-tiling rewrite removed (720 -> ~57,600 threads), so this
// is one of the contributors to the Δ≤1 floor that the contract accepts rather
// than an oversight. Any reduction whose order is load-bearing belongs here.
fn routeCol(col: i32, dcov: f32, dar: f32, X0: i32,
            cov: ptr<function, array<f32, 16>>,
            ar: ptr<function, array<f32, 16>>,
            backdrop: ptr<function, f32>) {
  if (col < X0) {
    *backdrop = *backdrop + dcov;
    return;
  }
  let lx = col - X0;
  if (lx < TILE) {
    (*cov)[lx] = (*cov)[lx] + dcov;
    (*ar)[lx] = (*ar)[lx] + dar;
  }
}

fn routeSeg(s: Seg, yi: i32, X0: i32, W: i32,
            cov: ptr<function, array<f32, 16>>,
            ar: ptr<function, array<f32, 16>>,
            backdrop: ptr<function, f32>) {
  var ax = s.x0; var ay = s.y0; var bx = s.x1; var by = s.y1;
  var dir = 1.0;
  if (ay > by) {
    let tx = ax; let ty = ay;
    ax = bx; ay = by; bx = tx; by = ty;
    dir = -1.0;
  }
  let dy = by - ay;
  if (dy < 1e-12) { return; }
  let dxdy = (bx - ax) / dy;
  let top = max(ay, f32(yi));
  let bot = min(by, f32(yi) + 1.0);
  if (top >= bot) { return; }
  let xa = ax + (top - ay) * dxdy;
  let xb = ax + (bot - ay) * dxdy;
  let dyv = (bot - top) * dir;

  var x0 = min(xa, xb);
  let x1 = max(xa, xb);
  if (x0 >= f32(W)) { return; }
  let xright = X0 + TILE;

  if (x1 - x0 < 1e-12) {
    var col = i32(floor(x0));
    var fx = x0 - floor(x0);
    if (col < 0) { col = 0; fx = 0.0; }
    routeCol(col, dyv, dyv * 2.0 * fx, X0, cov, ar, backdrop);
    return;
  }

  let inv = 1.0 / (x1 - x0);
  if (x0 < 0.0) {
    routeCol(0, dyv * (min(x1, 0.0) - x0) * inv, 0.0, X0, cov, ar, backdrop);
    x0 = 0.0;
  }

  var col = i32(floor(x0));
  loop {
    if (f32(col) >= x1 || col >= W || col >= xright) { break; }
    let xl = max(x0, f32(col));
    let xr = min(x1, f32(col + 1));
    if (xl < xr) {
      let dseg = dyv * (xr - xl) * inv;
      routeCol(col, dseg, dseg * ((xl - f32(col)) + (xr - f32(col))), X0, cov, ar, backdrop);
    }
    col = col + 1;
  }
}

@compute @workgroup_size(1, 64, 1)
fn main(@builtin(global_invocation_id) gid: vec3<u32>) {
  let W = i32(dims.w);
  let H = i32(dims.h);
  let nx = i32(dims.nx);
  let tileX = i32(gid.x);
  let y = i32(gid.y);
  if (y >= H || tileX >= nx) { return; }

  let X0 = tileX * TILE;
  let spanW = min(TILE, W - X0);
  let tile = u32((y / TILE) * nx + tileX);
  let start = tileOffsets[tile];
  let end = tileOffsets[tile + 1u];

  var fbl: array<vec4<f32>, 16>;
  for (var i = 0; i < TILE; i = i + 1) { fbl[i] = vec4<f32>(0.0); }

  var cov: array<f32, 16>;
  var ar: array<f32, 16>;
  var ccov: array<f32, 16>;
  var car: array<f32, 16>;
  var clipf: array<f32, 16>;

  for (var k = start; k < end; k = k + 1u) {
    let nd = nodes[tileNodes[k]];

    let cly0 = i32(floor(nd.cly0));
    let cly1 = i32(ceil(nd.cly1));
    if (y < cly0 || y >= cly1) { continue; }

    for (var i = 0; i < TILE; i = i + 1) { cov[i] = 0.0; ar[i] = 0.0; }
    var backdrop = 0.0;

    let so0 = tileSegOff[k];
    let so1 = tileSegOff[k + 1u];
    for (var j = so0; j < so1; j = j + 1u) {
      routeSeg(segs[tileSegIdx[j]], y, X0, W, &cov, &ar, &backdrop);
    }

    for (var i = 0; i < TILE; i = i + 1) { clipf[i] = 1.0; }
    let cl0 = nd.clipStart;
    let cl1 = nd.clipStart + nd.clipCount;
    for (var ci = cl0; ci < cl1; ci = ci + 1u) {
      let cl = clips[ci];
      for (var i = 0; i < TILE; i = i + 1) { ccov[i] = 0.0; car[i] = 0.0; }
      var cbd = 0.0;
      let cs1 = cl.segStart + cl.segCount;
      for (var si = cl.segStart; si < cs1; si = si + 1u) {
        routeSeg(segs[si], y, X0, W, &ccov, &car, &cbd);
      }
      var cacc = cbd;
      for (var lx = 0; lx < spanW; lx = lx + 1) {
        cacc = cacc + ccov[lx];
        clipf[lx] = clipf[lx] * coverage(cacc - car[lx] * 0.5, cl.rule);
      }
    }

    let clx0 = max(0, i32(floor(nd.clx0)));
    let clx1 = min(W, i32(ceil(nd.clx1)));
    var acc = backdrop;
    for (var lx = 0; lx < spanW; lx = lx + 1) {
      acc = acc + cov[lx];
      let gx = X0 + lx;
      if (gx < clx0 || gx >= clx1) { continue; }
      let alpha = coverage(acc - ar[lx] * 0.5, nd.rule) * clipf[lx];
      if (alpha > 0.0) {
        var src = vec4<f32>(nd.cr, nd.cg, nd.cb, nd.ca);
        if (nd.kind == 4u) {
          src = meshColor(nd, f32(gx) + 0.5, f32(y) + 0.5);
        } else if (nd.kind != 0u) {
          src = gradColor(nd, f32(gx) + 0.5, f32(y) + 0.5);
        }
        // Quantize per node, not once at the end. The CPU reference composites
        // into an 8-bit image.RGBA, so every node there reads a backdrop the
        // previous node rounded to 8 bits. Accumulating the tile in f32 across
        // all nodes and rounding once is a DIFFERENT computation, not a more
        // precise version of the same one, and the difference grows without
        // bound with stack depth: measured Δ=10 at 64 stacked Overlay layers
        // versus Δ=0 with this rounding in place (corpus: blend-stack-*).
        fbl[lx] = quant8(composite(nd.flags, fbl[lx], src, alpha));
      }
    }
  }

  for (var lx = 0; lx < spanW; lx = lx + 1) {
    textureStore(out_tex, vec2<i32>(X0 + lx, y), fbl[lx]);
  }
}

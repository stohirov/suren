struct Seg { x0: f32, y0: f32, x1: f32, y1: f32 };

struct Node {
  segStart: u32, segCount: u32, rule: u32, kind: u32,
  cr: f32, cg: f32, cb: f32, ca: f32,
  g0x: f32, g0y: f32, g1x: f32, g1y: f32,
  stopStart: u32, stopCount: u32, hasClip: u32, flags: u32,
  clx0: f32, cly0: f32, clx1: f32, cly1: f32,
  bbx0: f32, bby0: f32, bbx1: f32, bby1: f32,
  m0: f32, m1: f32, m2: f32, m3: f32, m4: f32, m5: f32,
  pad0: f32, pad1: f32,
};

struct Dims { w: u32, h: u32, nx: u32, ny: u32 };

struct Stop { off: f32, r: f32, g: f32, b: f32, a: f32 };

const TILE: i32 = 16;

@group(0) @binding(0) var out_tex: texture_storage_2d<rgba8unorm, write>;
@group(0) @binding(1) var<storage, read> segs: array<Seg>;
@group(0) @binding(2) var<storage, read> nodes: array<Node>;
@group(0) @binding(3) var<uniform> dims: Dims;
@group(0) @binding(4) var<storage, read> tileOffsets: array<u32>;
@group(0) @binding(5) var<storage, read> tileNodes: array<u32>;
@group(0) @binding(6) var<storage, read> stops: array<Stop>;

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
  } else {
    let radius = nd.g1x;
    if (radius > 0.0) {
      let dx = qx - nd.g0x;
      let dy = qy - nd.g0y;
      t = sqrt(dx * dx + dy * dy) / radius;
    }
  }
  let c = interpStops(nd.stopStart, nd.stopCount, t);
  let ca = clamp(c.w, 0.0, 1.0);
  return vec4<f32>(clamp(c.x, 0.0, 1.0) * ca, clamp(c.y, 0.0, 1.0) * ca, clamp(c.z, 0.0, 1.0) * ca, ca);
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

  for (var k = start; k < end; k = k + 1u) {
    let nd = nodes[tileNodes[k]];

    let cly0 = i32(floor(nd.cly0));
    let cly1 = i32(ceil(nd.cly1));
    if (y < cly0 || y >= cly1) { continue; }

    for (var i = 0; i < TILE; i = i + 1) { cov[i] = 0.0; ar[i] = 0.0; }
    var backdrop = 0.0;

    let s1 = nd.segStart + nd.segCount;
    for (var si = nd.segStart; si < s1; si = si + 1u) {
      routeSeg(segs[si], y, X0, W, &cov, &ar, &backdrop);
    }

    let clx0 = max(0, i32(floor(nd.clx0)));
    let clx1 = min(W, i32(ceil(nd.clx1)));
    var acc = backdrop;
    for (var lx = 0; lx < spanW; lx = lx + 1) {
      acc = acc + cov[lx];
      let gx = X0 + lx;
      if (gx < clx0 || gx >= clx1) { continue; }
      let alpha = coverage(acc - ar[lx] * 0.5, nd.rule);
      if (alpha > 0.0) {
        var src = vec4<f32>(nd.cr, nd.cg, nd.cb, nd.ca);
        if (nd.kind != 0u) {
          src = gradColor(nd, f32(gx) + 0.5, f32(y) + 0.5);
        }
        let dst = fbl[lx];
        let invc = 1.0 - src.w * alpha;
        fbl[lx] = vec4<f32>(
          src.x * alpha + dst.x * invc,
          src.y * alpha + dst.y * invc,
          src.z * alpha + dst.z * invc,
          src.w * alpha + dst.w * invc,
        );
      }
    }
  }

  for (var lx = 0; lx < spanW; lx = lx + 1) {
    textureStore(out_tex, vec2<i32>(X0 + lx, y), fbl[lx]);
  }
}

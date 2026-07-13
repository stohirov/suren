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

struct Dims { w: u32, h: u32, n: u32, pad: u32 };

@group(0) @binding(0) var out_tex: texture_storage_2d<rgba8unorm, write>;
@group(0) @binding(1) var<storage, read> segs: array<Seg>;
@group(0) @binding(2) var<storage, read> nodes: array<Node>;
@group(0) @binding(3) var<uniform> dims: Dims;
@group(0) @binding(4) var<storage, read_write> fb: array<vec4<f32>>;
@group(0) @binding(5) var<storage, read_write> cover: array<f32>;
@group(0) @binding(6) var<storage, read_write> area: array<f32>;
@group(0) @binding(7) var<storage, read> binOffsets: array<u32>;
@group(0) @binding(8) var<storage, read> binNodes: array<u32>;

fn clampi(v: i32, lo: i32, hi: i32) -> i32 {
  return max(lo, min(v, hi));
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

fn accumulate(base: u32, W: i32, xa: f32, xb: f32, dyv: f32) {
  var x0 = min(xa, xb);
  let x1 = max(xa, xb);
  if (x0 >= f32(W)) { return; }

  if (x1 - x0 < 1e-12) {
    var col = i32(floor(x0));
    var fx = x0 - floor(x0);
    if (col < 0) { col = 0; fx = 0.0; }
    let idx = base + u32(col);
    cover[idx] = cover[idx] + dyv;
    area[idx] = area[idx] + dyv * 2.0 * fx;
    return;
  }

  let inv = 1.0 / (x1 - x0);
  if (x0 < 0.0) {
    cover[base] = cover[base] + dyv * (min(x1, 0.0) - x0) * inv;
    x0 = 0.0;
  }

  var col = i32(floor(x0));
  loop {
    if (f32(col) >= x1 || col >= W) { break; }
    let xl = max(x0, f32(col));
    let xr = min(x1, f32(col + 1));
    if (xl < xr) {
      let dseg = dyv * (xr - xl) * inv;
      let idx = base + u32(col);
      cover[idx] = cover[idx] + dseg;
      area[idx] = area[idx] + dseg * ((xl - f32(col)) + (xr - f32(col)));
    }
    col = col + 1;
  }
}

fn addSeg(s: Seg, yi: i32, base: u32, W: i32) {
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
  accumulate(base, W, xa, xb, (bot - top) * dir);
}

@compute @workgroup_size(64)
fn main(@builtin(global_invocation_id) gid: vec3<u32>) {
  let W = i32(dims.w);
  let H = i32(dims.h);
  let y = i32(gid.x);
  if (y >= H) { return; }
  let base = u32(y) * dims.w;

  for (var x = 0; x < W; x = x + 1) {
    fb[base + u32(x)] = vec4<f32>(0.0, 0.0, 0.0, 0.0);
  }

  let start = binOffsets[u32(y)];
  let end = binOffsets[u32(y) + 1u];
  for (var k = start; k < end; k = k + 1u) {
    let nd = nodes[binNodes[k]];

    let bx0 = clampi(i32(floor(nd.bbx0)), 0, W);
    let bx1 = clampi(i32(floor(nd.bbx1)) + 1, 0, W);
    if (bx0 >= bx1) { continue; }

    let cly0 = i32(floor(nd.cly0));
    let cly1 = i32(ceil(nd.cly1));
    if (y < cly0 || y >= cly1) { continue; }
    let clx0 = max(bx0, max(0, i32(floor(nd.clx0))));
    let clx1 = min(bx1, min(W, i32(ceil(nd.clx1))));

    for (var x = bx0; x < bx1; x = x + 1) {
      cover[base + u32(x)] = 0.0;
      area[base + u32(x)] = 0.0;
    }

    let s1 = nd.segStart + nd.segCount;
    for (var si = nd.segStart; si < s1; si = si + 1u) {
      addSeg(segs[si], y, base, W);
    }

    var acc = 0.0;
    for (var x = bx0; x < bx1; x = x + 1) {
      acc = acc + cover[base + u32(x)];
      if (x < clx0 || x >= clx1) { continue; }
      let alpha = coverage(acc - area[base + u32(x)] * 0.5, nd.rule);
      if (alpha > 0.0) {
        let idx = base + u32(x);
        let dst = fb[idx];
        let inv = 1.0 - nd.ca * alpha;
        fb[idx] = vec4<f32>(
          nd.cr * alpha + dst.x * inv,
          nd.cg * alpha + dst.y * inv,
          nd.cb * alpha + dst.z * inv,
          nd.ca * alpha + dst.w * inv,
        );
      }
    }
  }

  for (var x = 0; x < W; x = x + 1) {
    textureStore(out_tex, vec2<i32>(x, y), fb[base + u32(x)]);
  }
}

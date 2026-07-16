// Blit the offscreen raster target onto a swapchain image.
//
// The compute pass writes a storage<rgba8unorm, write> texture. Swapchain images
// are RenderAttachment and never storage-writable, so the frame cannot be
// computed into the surface directly; it reaches the screen through this render
// pass instead. The raster path is unchanged — this only moves finished pixels.

@group(0) @binding(0) var src: texture_2d<f32>;

@vertex
fn vs_main(@builtin(vertex_index) i: u32) -> @builtin(position) vec4<f32> {
    // One oversized triangle — (-1,-1), (3,-1), (-1,3) — covers the clip rect
    // with no diagonal seam through the middle, unlike a two-triangle quad.
    let x = f32((i << 1u) & 2u) * 2.0 - 1.0;
    let y = f32(i & 2u) * 2.0 - 1.0;
    return vec4<f32>(x, y, 0.0, 1.0);
}

// textureLoad rather than a sampler: target and surface are configured to the
// same size, so this is texel-for-texel with no filtering to introduce error.
// @builtin(position) is framebuffer space, origin top-left, which already
// matches the target's row order — no flip.
//
// The clamp covers the frame where a resize has reconfigured the surface but
// the target has not caught up yet; out-of-range loads would otherwise be
// reading past the texture.
fn fetch(pos: vec4<f32>) -> vec4<f32> {
    let last = vec2<i32>(textureDimensions(src)) - vec2<i32>(1, 1);
    return textureLoad(src, clamp(vec2<i32>(pos.xy), vec2<i32>(0, 0), last), 0);
}

@fragment
fn fs_main(@builtin(position) pos: vec4<f32>) -> @location(0) vec4<f32> {
    return fetch(pos);
}

fn to_linear(c: f32) -> f32 {
    if c <= 0.04045 {
        return c / 12.92;
    }
    return pow((c + 0.055) / 1.055, 2.4);
}

// Only for a surface that offers no non-sRGB format. The target already holds
// sRGB-encoded bytes, and an sRGB attachment encodes again on write, so undo
// the encode here to keep the round trip a passthrough. Alpha is never encoded.
@fragment
fn fs_main_srgb(@builtin(position) pos: vec4<f32>) -> @location(0) vec4<f32> {
    let c = fetch(pos);
    return vec4<f32>(to_linear(c.r), to_linear(c.g), to_linear(c.b), c.a);
}

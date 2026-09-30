#version 450
layout(location = 0) in vec4 bounds;
layout(location = 1) in vec4 color;
layout(location = 2) in vec4 clipRect;
layout(location = 3) in vec4 radii;
layout(location = 4) in vec4 uvRect;
layout(location = 5) in vec4 kindLayer;
layout(location = 6) in vec4 widths;
layout(location = 7) in vec4 sideTop;
layout(location = 8) in vec4 sideRight;
layout(location = 9) in vec4 sideBottom;
layout(location = 10) in vec4 sideLeft;
layout(location = 11) in vec4 shape;
layout(location = 12) in vec4 shadow;
layout(push_constant) uniform Screen { vec2 size; } screen;
layout(location = 0) out vec4 fragColor;
layout(location = 1) out vec4 fragClip;
layout(location = 2) out vec4 fragBounds;
layout(location = 3) out vec4 fragRadii;
layout(location = 4) out vec3 fragAtlas;
layout(location = 5) flat out float fragKind;
layout(location = 6) flat out vec4 fragWidths;
layout(location = 7) flat out vec4 fragSideTop;
layout(location = 8) flat out vec4 fragSideRight;
layout(location = 9) flat out vec4 fragSideBottom;
layout(location = 10) flat out vec4 fragSideLeft;
layout(location = 11) flat out vec4 fragShape;
layout(location = 12) flat out vec4 fragShadow;
void main() {
    vec2 pos[6] = vec2[6](
        vec2(0.0, 0.0), vec2(1.0, 0.0), vec2(0.0, 1.0),
        vec2(0.0, 1.0), vec2(1.0, 0.0), vec2(1.0, 1.0));
    vec2 xy = bounds.xy + pos[gl_VertexIndex] * bounds.zw;
    // Vulkan positive-height viewport maps NDC -1 to framebuffer top.
    gl_Position = vec4(xy.x * 2.0 / screen.size.x - 1.0, xy.y * 2.0 / screen.size.y - 1.0, 0.0, 1.0);
    fragColor = color;
    fragClip = clipRect;
    fragBounds = bounds;
    fragRadii = radii;
    fragAtlas = vec3(mix(uvRect.xy, uvRect.zw, pos[gl_VertexIndex]), kindLayer.y);
    fragKind = kindLayer.x;
    fragWidths = widths;
    fragSideTop = sideTop;
    fragSideRight = sideRight;
    fragSideBottom = sideBottom;
    fragSideLeft = sideLeft;
    fragShape = shape;
    fragShadow = shadow;
}

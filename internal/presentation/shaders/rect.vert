#version 450
layout(push_constant) uniform Rect {
    vec4 bounds; // normalized x,y,width,height in framebuffer coordinates
    vec4 color;  // straight input alpha, premultiplied by the fragment stage
} pc;
void main() {
    vec2 pos[6] = vec2[6](
        vec2(0.0, 0.0), vec2(1.0, 0.0), vec2(0.0, 1.0),
        vec2(0.0, 1.0), vec2(1.0, 0.0), vec2(1.0, 1.0));
    vec2 xy = pc.bounds.xy + pos[gl_VertexIndex] * pc.bounds.zw;
    gl_Position = vec4(xy.x * 2.0 - 1.0, 1.0 - xy.y * 2.0, 0.0, 1.0);
}

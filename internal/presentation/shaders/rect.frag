#version 450
layout(push_constant) uniform Rect {
    vec4 bounds;
    vec4 color;
} pc;
layout(location = 0) out vec4 outColor;
void main() {
    outColor = vec4(pc.color.rgb * pc.color.a, pc.color.a);
}

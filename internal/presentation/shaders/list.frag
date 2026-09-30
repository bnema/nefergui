#version 450
layout(location = 0) in vec4 fragColor;
layout(location = 1) in vec4 fragClip;
layout(location = 2) in vec4 fragBounds;
layout(location = 3) in vec4 fragRadii;
layout(location = 4) in vec3 fragAtlas;
layout(location = 5) flat in float fragKind;
layout(location = 6) flat in vec4 fragWidths;
layout(location = 7) flat in vec4 fragSideTop;
layout(location = 8) flat in vec4 fragSideRight;
layout(location = 9) flat in vec4 fragSideBottom;
layout(location = 10) flat in vec4 fragSideLeft;
layout(location = 11) flat in vec4 fragShape;
layout(location = 12) flat in vec4 fragShadow;
// Set 0 holds the glyph atlas; set 1 holds the current image batch.
layout(set = 0, binding = 0) uniform sampler2DArray atlas;
layout(set = 1, binding = 0) uniform sampler2D imageTexture;
layout(location = 0) out vec4 outColor;
// Signed distance to a rounded rectangle with corner order TL/TR/BR/BL.
float roundedDistance(vec2 p, vec4 box, vec4 radii) {
    vec2 halfSize = box.zw * .5;
    vec2 center = box.xy + halfSize;
    float r = p.y < center.y ? (p.x < center.x ? radii.x : radii.y)
                              : (p.x < center.x ? radii.w : radii.z);
    r = clamp(r, 0.0, max(0.0, min(halfSize.x, halfSize.y)));
    vec2 d = abs(p - center) - halfSize + r;
    return length(max(d, 0.0)) + min(max(d.x, d.y), 0.0) - r;
}

float cover(float d) { return clamp(.5 - d, 0.0, 1.0); }

// Gaussian half-plane integral: Abramowitz/Stegun erf approximation. This
// signed-distance approximation also follows the curved rounded-box corners.
float gaussianCover(float d, float sigma) {
    if (sigma <= .01) return cover(d);
    float x = abs(d) / (sigma * 1.41421356);
    float t = 1.0 / (1.0 + .3275911 * x);
    float erfValue = 1.0 - (((((1.061405429 * t - 1.453152027) * t)
                         + 1.421413741) * t - .284496736) * t + .254829592) * t * exp(-x*x);
    return clamp(d < 0.0 ? .5 + .5*erfValue : .5 - .5*erfValue, 0.0, 1.0);
}

void main() {
    vec2 p = gl_FragCoord.xy;
    if (any(lessThan(p, fragClip.xy)) || any(greaterThanEqual(p, fragClip.xy + fragClip.zw))) discard;
    if (fragKind > .5 && fragKind < 1.5) {
        float coverage = texture(atlas, fragAtlas).r;
        outColor = vec4(fragColor.rgb * fragColor.a * coverage, fragColor.a * coverage);
        return;
    }
    float coverage = cover(roundedDistance(p, fragBounds, fragRadii));
    if (fragKind > 5.5 && fragKind < 6.5) {
        // Uploaded texels are straight sRGB RGBA. Match other list colors:
        // premultiply after sampling and applying element opacity.
        vec4 texel = texture(imageTexture, fragAtlas.xy);
        float alpha = texel.a * fragColor.a * coverage;
        outColor = vec4(texel.rgb * alpha, alpha);
        return;
    }
    vec4 color = fragColor;
    if (fragKind > 1.5 && fragKind < 3.5) {
        vec4 inner = fragBounds;
        vec4 outerRadii = fragRadii;
        if (fragKind < 2.5) {
            // Border radii belong to the outer edge; inner corners shrink by
            // their adjacent widths (CSS non-elliptical radius approximation).
            inner.xy += vec2(fragWidths.w, fragWidths.x);
            inner.zw -= vec2(fragWidths.w + fragWidths.y, fragWidths.x + fragWidths.z);
        } else {
            // Outline bounds already include outline width, and layout already
            // applied outline-offset to fragShape.
            outerRadii += vec4(max(fragWidths.x, fragWidths.w), max(fragWidths.x, fragWidths.y),
                               max(fragWidths.z, fragWidths.y), max(fragWidths.z, fragWidths.w));
            inner = fragShape;
            color = fragColor;
            coverage = cover(roundedDistance(p, fragBounds, outerRadii));
        }
        vec4 innerRadii = fragKind < 2.5 ? max(fragRadii - vec4(max(fragWidths.x, fragWidths.w),
            max(fragWidths.x, fragWidths.y), max(fragWidths.z, fragWidths.y), max(fragWidths.z, fragWidths.w)), 0.0) : fragRadii;
        float hole = inner.z > 0.0 && inner.w > 0.0 ? cover(roundedDistance(p, inner, innerRadii)) : 0.0;
        coverage *= 1.0 - hole;
        if (fragKind < 2.5) {
            // Diagonal split: classify by the nearest normalized edge of the
            // outer box. Ties go to top/bottom (stable at 45-degree corners).
            vec2 uv = (p - fragBounds.xy) / fragBounds.zw;
            float h = min(uv.x, 1.0 - uv.x);
            float v = min(uv.y, 1.0 - uv.y);
            color = v <= h ? (uv.y < .5 ? fragSideTop : fragSideBottom)
                           : (uv.x < .5 ? fragSideLeft : fragSideRight);
        }
    } else if (fragKind > 3.5) {
        vec4 box = fragShape;
        if (fragKind < 4.5) {
            box.xy += fragShadow.xy - vec2(fragShadow.w);
            box.zw += vec2(2.0 * fragShadow.w);
            coverage = box.z > 0.0 && box.w > 0.0 ? gaussianCover(roundedDistance(p, box,
                max(fragRadii + vec4(fragShadow.w), 0.0)), max(.0, fragShadow.z * .5)) : 0.0;
        } else {
            // Inset shadow is the complement of a shifted, contracted box,
            // clipped to the original rounded box.
            box.xy += fragShadow.xy + vec2(fragShadow.w);
            box.zw -= vec2(2.0 * fragShadow.w);
            float hole = box.z > 0.0 && box.w > 0.0 ? gaussianCover(roundedDistance(p, box,
                max(fragRadii - vec4(fragShadow.w), 0.0)), max(.0, fragShadow.z * .5)) : 0.0;
            coverage = cover(roundedDistance(p, fragShape, fragRadii)) * (1.0 - hole);
        }
    }
    outColor = vec4(color.rgb * color.a * coverage, color.a * coverage);
}

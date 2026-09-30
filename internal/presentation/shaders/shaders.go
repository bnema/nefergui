// Package shaders owns the committed SPIR-V compiled from its sibling GLSL
// sources. Consumers do not need glslc; maintainers run go generate ./... .
package shaders

import _ "embed"

//go:generate glslc -O -fshader-stage=vertex -o rect.vert.spv rect.vert
//go:generate glslc -O -fshader-stage=fragment -o rect.frag.spv rect.frag
//go:generate glslc -O -fshader-stage=vertex -o list.vert.spv list.vert
//go:generate glslc -O -fshader-stage=fragment -o list.frag.spv list.frag

//go:embed rect.vert.spv
var Vertex []byte

//go:embed rect.frag.spv
var Fragment []byte

//go:embed list.vert.spv
var ListVertex []byte

//go:embed list.frag.spv
var ListFragment []byte

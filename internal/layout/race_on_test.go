//go:build race

package layout

// raceEnabled: the race detector adds allocations, so exact alloc counts skip.
const raceEnabled = true

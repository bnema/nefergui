//go:build race

package text

// raceEnabled: the race detector adds allocations, so alloc guards skip.
const raceEnabled = true

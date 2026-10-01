//go:build race

package ui

// raceEnabled: the race detector adds allocations, so alloc guards skip.
const raceEnabled = true

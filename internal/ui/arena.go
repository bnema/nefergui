package ui

// elementArena hands out the elements of one frame from reusable chunks, so a
// steady frame allocates no elements and keeps each element's slice capacity.
// Chunks never move, so *element pointers stay valid until reset. The runtime
// alternates two arenas: one holds the committed tree, which the next frame
// reads as its previous tree, while the other is being rebuilt.
type elementArena struct {
	chunks [][]element
	n      int // elements handed out since the last reset
}

const (
	arenaChunk     = 32  // elements per chunk
	arenaMaxChunks = 64  // retained chunks per arena (2048 elements)
	elementMaxKeep = 256 // retained capacity of a per-element slice
)

func (a *elementArena) slot(i int) *element { return &a.chunks[i/arenaChunk][i%arenaChunk] }

// newElement returns a zeroed element that is valid until the next reset.
func (a *elementArena) newElement() *element {
	if a == nil { // an arena-less frame, such as a hand-built test Frame
		return &element{}
	}
	if a.n/arenaChunk == len(a.chunks) {
		a.chunks = append(a.chunks, make([]element, arenaChunk))
	}
	e := a.slot(a.n)
	a.n++
	return e
}

// reset recycles every element handed out. It drops references so the old
// tree can be collected, keeps modest slice capacity and releases chunks
// beyond twice what the last frame needed, or the retention cap.
func (a *elementArena) reset() {
	for i := 0; i < a.n; i++ {
		e := a.slot(i)
		children, events, classes, nodes := e.children, e.events, e.classes, e.ln.Children
		clear(children)
		clear(events)
		clear(classes)
		clear(nodes)
		*e = element{children: keep(children), events: keep(events), classes: keep(classes)}
		e.ln.Children = keep(nodes)
	}
	needed := (a.n + arenaChunk - 1) / arenaChunk
	if limit := min(2*needed+1, arenaMaxChunks); len(a.chunks) > limit {
		clear(a.chunks[limit:])
		a.chunks = a.chunks[:limit]
	}
	a.n = 0
}

func keep[T any](s []T) []T {
	if cap(s) > elementMaxKeep {
		return nil
	}
	return s[:0]
}

package ui

import (
	"context"
	"testing"
	"time"
)

func waitEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("view did not start")
	}
}

func TestIdentityAndEvents(t *testing.T) {
	r := newRuntime()
	order := []string{"a", "b"}
	var received []string
	view := func(f *Frame) {
		root := f.Root()
		for _, key := range order {
			if root.Button(key, Key(key)).Activated() {
				received = append(received, key)
			}
		}
	}
	r.Build(view)
	id := r.Target(1)
	order = []string{"b", "a"}
	r.Queue(inputEvent{Target: id, Kind: "activate"})
	r.Queue(inputEvent{Target: id, Kind: "activate"})
	if err := r.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	builds := 0
	r.Build(func(f *Frame) { builds++; view(f) })
	if builds != 1 || len(received) != 1 || received[0] != "b" {
		t.Fatalf("batch: %v builds %d target=%+v first=%+v", received, builds, id, r.Target(0))
	}
	r.Redraw()
	r.Build(view)
	if len(received) != 1 {
		t.Fatalf("expired event: %v", received)
	}
}
func TestImplicitIdentity(t *testing.T) {
	r := newRuntime()
	r.Build(func(f *Frame) { f.Root().Box().Text("x") })
	first := r.Target(0)
	r.Redraw()
	r.Build(func(f *Frame) { f.Root().Box().Text("y") })
	if !first.same(r.Target(0)) {
		t.Fatal("position/type identity changed")
	}
	r.Redraw()
	r.Build(func(f *Frame) {
		f.Root().Row()
		if debugDiagnostics && len(f.Diagnostics()) != 1 {
			t.Fatalf("missing unkeyed change diagnostic: %v", f.Diagnostics())
		}
	})
	if first.same(r.Target(0)) {
		t.Fatal("type omitted from identity")
	}
}
func TestStaleAndDuplicates(t *testing.T) {
	r := newRuntime()
	var old Node
	r.Build(func(f *Frame) {
		old = f.Root()
		old.Box(Key("dup"))
		old.Box(Key("dup"))
		if debugDiagnostics && len(f.Diagnostics()) != 1 {
			t.Fatal(f.Diagnostics())
		}
	})
	if old.valid() {
		t.Fatal("stale node accepted")
	}
	if debugDiagnostics {
		defer func() {
			if recover() == nil {
				t.Error("missing stale panic")
			}
		}()
		old.Box()
	}
}
func TestWake(t *testing.T) {
	r := newRuntime()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.Redraw()
	if err := r.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

// TestCommittedLayoutSurvivesNextBuild guards the layout arena rotation: the
// view reads the committed layout while the next one is built, and a frame
// that does not commit must leave it intact.
func TestCommittedLayoutSurvivesNextBuild(t *testing.T) {
	r := newRuntime()
	view := func(label string) func(*Frame) {
		return func(f *Frame) { f.Root().Box(Key(label), Inline("width:10px;height:10px;background:#fff")) }
	}
	if !r.Build(view("a")) {
		t.Fatal("first build skipped")
	}
	snapshot := func() (string, int) {
		leaf := r.output.Tree
		for len(leaf.Children) > 0 {
			leaf = leaf.Children[0]
		}
		return leaf.ID, len(r.output.Display)
	}
	id, cmds := snapshot()
	// Building the next frame must not write into the previous output, which
	// the view, hit-testing and the renderer read until the commit.
	prevTree, prevDisplay := r.output.Tree, r.output.Display
	first := prevDisplay[0].ID
	r.Redraw()
	r.Build(view("b"))
	leaf := prevTree
	for len(leaf.Children) > 0 {
		leaf = leaf.Children[0]
	}
	if leaf.ID != id || len(prevDisplay) != cmds || prevDisplay[0].ID != first {
		t.Fatalf("next build overwrote the previous layout: leaf %q (want %q), %d commands (want %d), first %q (want %q)", leaf.ID, id, len(prevDisplay), cmds, prevDisplay[0].ID, first)
	}
	idB, _ := snapshot()
	if idB == id {
		t.Fatalf("second build not committed: %q", idB)
	}
	// A panicking view does not commit: the output stays readable and intact.
	func() {
		defer func() { _ = recover() }()
		r.Redraw()
		r.Build(func(f *Frame) { view("c")(f); panic("view failure") })
	}()
	if got, _ := snapshot(); got != idB {
		t.Fatalf("panicked frame replaced the output: %q, want %q", got, idB)
	}
	// Rotation still alternates after the failure: two more builds keep the
	// latest output and never hand out the arena it lives in.
	for _, label := range []string{"d", "e"} {
		before, _ := snapshot()
		r.Redraw()
		r.Build(func(f *Frame) {
			if got, _ := snapshot(); got != before {
				t.Errorf("output overwritten before commit: %q, want %q", got, before)
			}
			view(label)(f)
		})
	}
}

func TestConcurrentBuildDefersCommit(t *testing.T) {
	r := newRuntime()
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan bool, 1)
	secondDone := make(chan bool, 1)
	go func() { firstDone <- r.Build(func(f *Frame) { f.Root().Box(Key("first")); close(entered); <-release }) }()
	waitEntered(t, entered)
	go func() {
		secondDone <- r.Build(func(f *Frame) { t.Error("busy view executed"); f.Root().Box(Key("second")) })
	}()
	select {
	case built := <-secondDone:
		if built {
			t.Fatal("busy build committed")
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent Build blocked")
	}
	if r.Target(0) != nil {
		t.Fatal("uncommitted tree visible")
	}
	close(release)
	select {
	case built := <-firstDone:
		if !built {
			t.Fatal("first build skipped")
		}
	case <-time.After(time.Second):
		t.Fatal("first build did not finish")
	}
	if id := r.Target(0); id == nil || id.key != "first" {
		t.Fatalf("commit out of order: %+v", id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Wait(ctx); err != nil {
		t.Fatalf("lost wake: %v", err)
	}
	if !r.Build(func(f *Frame) { f.Root().Box(Key("second")) }) {
		t.Fatal("redraw skipped")
	}
	if id := r.Target(0); id == nil || id.key != "second" {
		t.Fatalf("follow-up commit missing: %+v", id)
	}
}

func TestReentrantBuildSchedulesQueuedEvents(t *testing.T) {
	r := newRuntime()
	r.Build(func(f *Frame) { f.Root().Button("target", Key("target")) })
	target := r.Target(0)
	r.Redraw()
	finished := make(chan bool, 1)
	go func() {
		finished <- r.Build(func(f *Frame) {
			f.Root().Button("target", Key("target"))
			r.Queue(inputEvent{Target: target, Kind: "activate"})
			if r.Build(func(*Frame) { t.Error("reentrant view executed") }) {
				t.Error("reentrant build committed")
			}
		})
	}()
	select {
	case built := <-finished:
		if !built {
			t.Fatal("outer build skipped")
		}
	case <-time.After(time.Second):
		t.Fatal("reentrant Build deadlocked")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Wait(ctx); err != nil {
		t.Fatalf("follow-up wake lost: %v", err)
	}
	if !r.Build(func(f *Frame) {
		if !f.Root().Button("target", Key("target")).Activated() {
			t.Error("queued event not delivered")
		}
	}) {
		t.Fatal("follow-up skipped")
	}
	assertOneSettleFrame(t, r, func(f *Frame) { f.Root().Button("target", Key("target")) })
}

// assertOneSettleFrame checks that an event batch schedules exactly one
// event-free follow-up build, and that this build schedules nothing more.
func assertOneSettleFrame(t *testing.T, r *runtime, view func(*Frame)) {
	t.Helper()
	settled := false
	if !r.Build(func(f *Frame) {
		settled = true
		if len(f.events) != 0 {
			t.Error("settle frame carried events")
		}
		view(f)
	}) || !settled {
		t.Fatal("no settle frame after events")
	}
	if r.Build(func(*Frame) { t.Error("extra view executed") }) {
		t.Fatal("extra redraw after settle frame")
	}
}

func TestQueueDuringConstruction(t *testing.T) {
	r := newRuntime()
	r.Build(func(f *Frame) { f.Root().Button("target", Key("target")) })
	target := r.Target(0)
	r.Queue(inputEvent{Target: target, Kind: "activate"})
	entered := make(chan struct{})
	release := make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		result <- r.Build(func(f *Frame) {
			root := f.Root()
			if !root.Button("target", Key("target")).Activated() {
				t.Error("first batch missing")
			}
			close(entered)
			<-release
		})
	}()
	waitEntered(t, entered)
	r.Queue(inputEvent{Target: target, Kind: "activate"})
	r.Redraw()
	close(release)
	if !<-result {
		t.Fatal("first batch skipped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Wait(ctx); err != nil {
		t.Fatalf("lost wake during build: %v", err)
	}
	if !r.Build(func(f *Frame) {
		if !f.Root().Button("target", Key("target")).Activated() {
			t.Error("event queued during construction lost")
		}
	}) {
		t.Fatal("second batch skipped")
	}
	assertOneSettleFrame(t, r, func(f *Frame) { f.Root().Button("target", Key("target")) })
}

func TestCheckboxElementAndActivation(t *testing.T) {
	r := newRuntime()
	checked := false
	build := func(f *Frame) ChangeEvent { root := f.Root(); return root.Checkbox("accept", &checked, Key("accept")) }
	r.Build(func(f *Frame) {
		if build(f).Changed() {
			t.Fatal("spurious change")
		}
	})
	if e := r.committed.children[0]; e.typ != "checkbox" || e.role != "checkbox" || e.text != "accept" {
		t.Fatalf("wrong semantics: %+v", e)
	}
	r.Queue(inputEvent{Target: r.Target(0), Kind: "activate"})
	r.Build(func(f *Frame) {
		ev := build(f)
		changed := ev.Changed()
		if !changed || ev.Changed() != changed || !checked {
			t.Fatal("checkbox activation missing")
		}
	})
	if e := r.committed.children[0]; e.typ != "checkbox" || e.role != "checkbox" {
		t.Fatalf("wrong semantics after activation: %+v", e)
	}
	r.Queue(inputEvent{Target: r.Target(0), Kind: "activate"})
	r.Build(func(f *Frame) { f.Root().Checkbox("accept", &checked, Key("accept"), Disabled(true)) })
	if !checked {
		t.Fatal("disabled checkbox mutated model")
	}
}

func TestHeadingLevel(t *testing.T) {
	r := newRuntime()
	r.Build(func(f *Frame) { f.Root().Header().Heading("title", Level(1), Class("logo")) })
	e := r.committed.children[0].children[0]
	if e.typ != "heading" || e.role != "heading" || e.level != 1 || e.text != "title" || len(e.classes) != 1 || e.classes[0] != "logo" {
		t.Fatalf("heading metadata: %+v", e)
	}
	defer func() {
		if recover() == nil {
			t.Error("invalid level accepted")
		}
	}()
	Level(7)
}

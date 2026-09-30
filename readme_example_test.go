package nefergui_test

import (
	"context"
	"fmt"
	"log"

	"github.com/bnema/nefergui"
)

// Compile the README example without opening a window during tests.
func Example_counter() {
	count := 0
	view := func(f *nefergui.Frame, count *int) {
		root := f.Root().Column()
		root.Text("Hello, Wayland!")
		if root.Button("Count", nefergui.Key("count")).Activated() {
			*count++
		}
		root.Text(fmt.Sprintf("Clicked %d times", *count))
	}
	if err := nefergui.Run(context.Background(), &count, view,
		nefergui.Title("Hello"), nefergui.Size(320, 200)); err != nil {
		log.Fatal(err)
	}
}

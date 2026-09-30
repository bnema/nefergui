package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/bnema/nefergui/internal/harness"
)

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: nefergui-harness run [--size WxH --scale 1.5 --layout fr --input script --expect expect.json --out dir --keep --allow-unpinned --timeout 15s] -- <client cmd...>")
		return harness.Usage
	}
	f := flag.NewFlagSet("run", flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	size := f.String("size", "800x600", "headless size")
	scale := f.String("scale", "1", "output scale")
	layout := f.String("layout", "us", "keyboard layout")
	background := f.String("background", "#111111", "background #RRGGBB")
	input := f.String("input", "", "input script")
	expect := f.String("expect", "", "expectations JSON")
	out := f.String("out", "", "new artifact directory")
	keep := f.Bool("keep", false, "keep temporary runtime/config")
	allowUnpinned := f.Bool("allow-unpinned", false, "permit NEFERGUI_NEFERWL override")
	timeout := f.Duration("timeout", 15*time.Second, "maximum session duration")
	ready := f.Duration("ready-timeout", 8*time.Second, "socket readiness deadline")
	if err := f.Parse(args[1:]); err != nil {
		return harness.Usage
	}
	if *out == "" {
		fmt.Fprintln(os.Stderr, "--out required")
		return harness.Usage
	}
	var ex harness.Expectations
	if *expect != "" {
		data, err := os.ReadFile(*expect)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return harness.Usage
		}
		d := json.NewDecoder(strings.NewReader(string(data)))
		d.DisallowUnknownFields()
		if err = d.Decode(&ex); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return harness.Usage
		}
		var extra any
		if err = d.Decode(&extra); !errors.Is(err, io.EOF) {
			fmt.Fprintln(os.Stderr, "unexpected JSON after expectations:", err)
			return harness.Usage
		}
	}
	base := "."
	if *expect != "" {
		base = filepath.Dir(*expect)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, code := harness.Run(ctx, harness.Options{Size: *size, Scale: *scale, Layout: *layout, Background: *background, Input: *input, Out: *out, Keep: *keep, AllowUnpinned: *allowUnpinned, Timeout: *timeout, ReadyTimeout: *ready, Client: f.Args(), Expect: ex, GoldenBase: base})
	b, _ := json.Marshal(res)
	fmt.Println(string(b))
	return code
}

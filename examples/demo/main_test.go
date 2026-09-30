package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestUnexpectedKeepsCloseErrors(t *testing.T) {
	closeErr := errors.New("close failed")
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"done", errDone, nil},
		{"canceled", fmt.Errorf("run: %w", context.Canceled), nil},
		{"done and close", errors.Join(errDone, closeErr), closeErr},
		{"close only", errors.Join(nil, closeErr), closeErr},
	}
	for _, c := range cases {
		got := unexpected(c.in)
		if c.want == nil && got != nil || c.want != nil && !errors.Is(got, c.want) {
			t.Errorf("%s: unexpected(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
	if got := unexpected(errors.Join(errDone, closeErr)); errors.Is(got, errDone) {
		t.Errorf("errDone kept in %v", got)
	}
}

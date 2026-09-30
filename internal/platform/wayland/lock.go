//go:build linux

package wayland

import "fmt"

// LockOptions selects one registry output lifetime returned by Outputs.
// The lock object is deliberately private to Connection, never passed by UI.
type LockOptions struct{ Output uint32 }

func (w *Window) createLockSurface(opts LockOptions) error {
	c := w.connection
	if c == nil || c.lock == nil {
		return fmt.Errorf("lock window requires acquired connection lock")
	}
	out, ok := c.outputs[opts.Output]
	if !ok || out.proxy == nil {
		return fmt.Errorf("lock output %d unavailable", opts.Output)
	}
	role, err := c.lock.GetLockSurface(w.Surface, out.proxy)
	if err != nil {
		return err
	}
	w.LockSurface, w.lockOutput = role, opts.Output
	role.OnConfigure(func(serial, width, height uint32) {
		w.post(Event{Kind: ConfigureLock, Serial: serial, Width: int32(width), Height: int32(height)})
	})
	return nil
}

// LockOutput is the registry lifetime this lock surface covers.
func (w *Window) LockOutput() uint32 { return w.lockOutput }

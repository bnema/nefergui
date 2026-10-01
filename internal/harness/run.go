package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	Pass           = 0
	Failed         = 1
	Usage          = 2
	Unavailable    = 3
	ProcessFailure = 4
)

type Options struct {
	Size          string
	Scale         string
	Layout        string
	Background    string
	Input         string
	Out           string
	Keep          bool
	AllowUnpinned bool
	Timeout       time.Duration
	ReadyTimeout  time.Duration
	Client        []string
	Expect        Expectations
	GoldenBase    string
}
type Compositor struct {
	Pinned bool   `json:"pinned"`
	SHA    string `json:"sha,omitempty"`
	Binary string `json:"binary,omitempty"`
	PID    int    `json:"pid,omitempty"`
}
type Result struct {
	Compositor Compositor        `json:"compositor"`
	Schema     string            `json:"schema"`
	Status     string            `json:"status"`
	Checks     []Check           `json:"checks"`
	Artifacts  map[string]string `json:"artifacts"`
	Timings    map[string]string `json:"timings"`
	Error      string            `json:"error,omitempty"`
}

func validate(o Options) error {
	if _, err := Config(o.Size, o.Scale, o.Layout, o.Background); err != nil {
		return err
	}
	if o.Out == "" || o.Timeout <= 0 || o.ReadyTimeout <= 0 || o.ReadyTimeout >= o.Timeout {
		return fmt.Errorf("out and positive timeouts (ready < total) required")
	}
	if o.Input != "" {
		if fi, err := os.Stat(o.Input); err != nil || !fi.Mode().IsRegular() {
			return fmt.Errorf("input script not readable: %s", o.Input)
		}
	}
	for _, p := range o.Expect.Probes {
		if _, err := ParseColor(p.Color); err != nil {
			return err
		}
	}
	for _, r := range o.Expect.Regions {
		if r.Color != "" {
			if _, err := ParseColor(r.Color); err != nil {
				return err
			}
		}
	}
	for _, g := range o.Expect.Goldens {
		if g.MaxDiffRatio < 0 || g.MaxDiffRatio > 1 || g.Path == "" {
			return fmt.Errorf("invalid golden %q", g.Name)
		}
	}
	return nil
}

// copyRunLog copies the compositor's structured run log into the artifacts.
// A missing log (older NeferWL, early failure) is not an error; it reports
// whether dst was written.
func copyRunLog(src, dst string) (bool, error) {
	data, err := os.ReadFile(src)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err == nil {
		err = os.WriteFile(dst, data, 0600)
	}
	return err == nil, err
}
func newLog(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
}
func waitSocket(ctx context.Context, runtime string, exited <-chan error) (string, error) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		name, err := DiscoverSocket(ctx, runtime)
		if err != nil || name != "" {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return name, err
		}
		select {
		case err := <-exited:
			return "", fmt.Errorf("compositor exited before ready: %v", err)
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// Run writes result.json for every run that has acquired an artifact directory.
// It never owns client-side debug files: the client receives NEFERGUI_DEBUG_DIR and writes them.
func Run(ctx context.Context, o Options) (res Result, code int) {
	res = Result{Schema: "nefergui-harness/v1", Status: "error", Checks: []Check{}, Artifacts: map[string]string{}, Timings: map[string]string{}}
	code = ProcessFailure
	if err := validate(o); err != nil {
		res.Error = err.Error()
		return res, Usage
	}
	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	out, err := filepath.Abs(o.Out)
	if err != nil {
		res.Error = err.Error()
		return res, Usage
	}
	if err = os.Mkdir(out, 0700); err != nil {
		res.Error = fmt.Sprintf("out must be a new directory: %v", err)
		return res, Usage
	}
	res.Artifacts["root"] = out
	defer func() {
		res.Timings["total"] = time.Since(start).String()
		data, e := json.MarshalIndent(res, "", "  ")
		if e == nil {
			e = os.WriteFile(filepath.Join(out, "result.json"), append(data, '\n'), 0600)
		}
		if e != nil {
			writeErr := fmt.Errorf("write result.json: %w", e)
			if res.Error != "" {
				writeErr = errors.Join(errors.New(res.Error), writeErr)
			}
			res.Error = writeErr.Error()
			res.Status = "error"
			code = ProcessFailure
		}
	}()
	fail := func(err error, c int) (Result, int) { res.Error = err.Error(); return res, c }
	for key, dir := range map[string]string{"frames": "frames", "debug": "client-debug", "logs": "logs"} {
		path := filepath.Join(out, dir)
		if err = os.Mkdir(path, 0700); err != nil {
			return fail(err, ProcessFailure)
		}
		res.Artifacts[key] = path
	}
	shots := FramesDir(filepath.Join(out, "frames"), o.Size) // first output's screenshots
	res.Artifacts["frame"] = filepath.Join(shots, "latest.png")
	res.Artifacts["compositor_log"] = filepath.Join(out, "logs", "compositor.log")
	res.Artifacts["client_stdout"] = filepath.Join(out, "logs", "client.stdout")
	res.Artifacts["client_stderr"] = filepath.Join(out, "logs", "client.stderr")
	session, err := os.MkdirTemp("", "nefergui-harness-")
	if err != nil {
		return fail(err, ProcessFailure)
	}
	if !o.Keep {
		defer os.RemoveAll(session)
	} else {
		res.Artifacts["session"] = session
	}
	runtime := filepath.Join(session, "runtime")
	configHome := filepath.Join(session, "config")
	for _, d := range []string{runtime, filepath.Join(configHome, "neferwl")} {
		if err = os.MkdirAll(d, 0700); err != nil {
			return fail(err, ProcessFailure)
		}
	}
	cfg, err := Config(o.Size, o.Scale, o.Layout, o.Background)
	if err != nil {
		return fail(err, Usage)
	}
	if err = os.WriteFile(filepath.Join(configHome, "neferwl", "config"), []byte(cfg), 0600); err != nil {
		return fail(err, ProcessFailure)
	}
	buildStart := time.Now()
	if path := os.Getenv("NEFERGUI_NEFERWL"); path != "" {
		res.Compositor = Compositor{Binary: path}
		if !o.AllowUnpinned {
			return fail(fmt.Errorf("NEFERGUI_NEFERWL requires --allow-unpinned"), Usage)
		}
	} else {
		res.Compositor = Compositor{Pinned: true, SHA: NeferWLSHA}
	}
	buildCtx, buildCancel := context.WithTimeout(runCtx, 3*time.Minute)
	binary, err := NeferWL(buildCtx)
	buildCancel()
	res.Timings["build"] = time.Since(buildStart).String()
	if err != nil {
		return fail(err, Unavailable)
	}
	if err = runCtx.Err(); err != nil {
		return fail(err, Unavailable)
	}
	logFile, err := newLog(res.Artifacts["compositor_log"])
	if err != nil {
		return fail(err, ProcessFailure)
	}
	defer logFile.Close()
	args := []string{"--backend=headless", "--no-terminal", "--no-xwayland", "--screenshot", res.Artifacts["frames"], "--size", o.Size, "--timeout", o.Timeout.String()}
	if o.Input != "" {
		args = append(args, "--input", o.Input)
	}
	cmd := exec.Command(binary, args...)
	// NeferWL writes structured run logs under XDG_STATE_HOME; keep them in
	// the session instead of the host state directory, and copy them out.
	stateHome := filepath.Join(session, "state")
	cmd.Env = withEnv(isolatedEnv(os.Environ()), map[string]string{"XDG_RUNTIME_DIR": runtime, "XDG_CONFIG_HOME": configHome, "XDG_STATE_HOME": stateHome, "WAYLAND_DISPLAY": ""})
	// Registered before the reap defer, so it runs after the compositor exits.
	defer func() {
		dst := filepath.Join(out, "logs", "compositor-run.log")
		ok, e := copyRunLog(filepath.Join(stateHome, "neferwl", "runs", "headless", "latest.log"), dst)
		if ok {
			res.Artifacts["compositor_run_log"] = dst
		}
		if e != nil {
			copyErr := fmt.Errorf("copy compositor run log: %w", e)
			if res.Error != "" {
				copyErr = errors.Join(errors.New(res.Error), copyErr)
			}
			res.Error, res.Status, code = copyErr.Error(), "error", ProcessFailure
		}
	}()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = cmd.Start(); err != nil {
		return fail(err, Unavailable)
	}
	res.Compositor.PID = cmd.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait(); close(exited) }()
	// Always reap the process; killing the group also stops any children.
	defer func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-exited
	}()
	readyCtx, readyCancel := context.WithTimeout(runCtx, o.ReadyTimeout)
	defer readyCancel()
	readyStart := time.Now()
	socket, err := waitSocket(readyCtx, runtime, exited)
	res.Timings["ready"] = time.Since(readyStart).String()
	if err != nil {
		return fail(err, Unavailable)
	}
	// Create empty logs even for compositor-only sessions.
	for _, path := range []string{res.Artifacts["client_stdout"], res.Artifacts["client_stderr"]} {
		f, e := newLog(path)
		if e != nil {
			return fail(e, ProcessFailure)
		}
		_ = f.Close()
	}
	env := withEnv(isolatedEnv(os.Environ()), map[string]string{"XDG_RUNTIME_DIR": runtime, "XDG_CONFIG_HOME": configHome, "WAYLAND_DISPLAY": socket, "NEFERGUI_DEBUG_DIR": res.Artifacts["debug"]})
	if len(o.Client) > 0 {
		stdout, e := os.OpenFile(res.Artifacts["client_stdout"], os.O_WRONLY, 0600)
		if e != nil {
			return fail(e, ProcessFailure)
		}
		defer stdout.Close()
		stderr, e := os.OpenFile(res.Artifacts["client_stderr"], os.O_WRONLY, 0600)
		if e != nil {
			return fail(e, ProcessFailure)
		}
		defer stderr.Close()
		client := exec.Command(o.Client[0], o.Client[1:]...)
		client.Env = env
		client.Stdout = stdout
		client.Stderr = stderr
		client.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err = client.Start(); err != nil {
			return fail(err, ProcessFailure)
		}
		clientExit := make(chan error, 1)
		go func() { clientExit <- client.Wait(); close(clientExit) }()
		defer func() {
			_ = syscall.Kill(-client.Process.Pid, syscall.SIGKILL)
			<-clientExit
		}()
		clientStart := time.Now()
		capturePath := filepath.Join(res.Artifacts["frames"], "capture.png")
		requestPath := filepath.Join(res.Artifacts["debug"], "capture-request")
		requestSeen := false
		var requestedAt time.Time
		captureTick := time.NewTicker(10 * time.Millisecond)
		defer captureTick.Stop()
	clientLoop:
		for {
			select {
			case err = <-clientExit:
				break clientLoop
			case err = <-exited:
				err = fmt.Errorf("compositor exited during client: %v", err)
				break clientLoop
			case <-runCtx.Done():
				err = runCtx.Err()
				break clientLoop
			case <-captureTick.C:
				if !requestSeen {
					requestedAt, err = captureRequestTime(requestPath)
					if errors.Is(err, os.ErrNotExist) {
						err = nil
					} else if err == nil {
						requestSeen = true
					} else {
						break clientLoop
					}
				}
				if requestSeen {
					err = copyNextFrame(shots, requestedAt, capturePath)
					if err == nil {
						res.Artifacts["capture"] = capturePath
						requestSeen = false
						break clientLoop
					}
					if !errors.Is(err, os.ErrNotExist) {
						break clientLoop
					}
					err = nil
				}
			}
		}
		// After capture, still wait for the client to finish normally.
		if res.Artifacts["capture"] != "" && err == nil {
			select {
			case err = <-clientExit:
			case err = <-exited:
				err = fmt.Errorf("compositor exited during client: %v", err)
			case <-runCtx.Done():
				err = runCtx.Err()
			}
		}
		res.Timings["client"] = time.Since(clientStart).String()
		// A request near client exit must not silently fall back to latest.png.
		if err == nil && res.Artifacts["capture"] == "" {
			if !requestSeen {
				requestedAt, err = captureRequestTime(requestPath)
				if errors.Is(err, os.ErrNotExist) {
					err = nil
				} else if err == nil {
					requestSeen = true
				}
			}
			if requestSeen && err == nil {
				for runCtx.Err() == nil {
					err = copyNextFrame(shots, requestedAt, capturePath)
					if err == nil {
						res.Artifacts["capture"] = capturePath
						break
					}
					if !errors.Is(err, os.ErrNotExist) {
						break
					}
					err = nil
					select {
					case <-runCtx.Done():
					case <-captureTick.C:
					}
				}
				if res.Artifacts["capture"] == "" && err == nil {
					err = fmt.Errorf("capture-request: %w", runCtx.Err())
				}
			}
		}
		if err != nil {
			return fail(fmt.Errorf("client/compositor: %w", err), ProcessFailure)
		}
		select {
		case e := <-exited:
			return fail(fmt.Errorf("compositor exited during client: %v", e), ProcessFailure)
		default:
		}
	}
	// Wait briefly for the asynchronous screenshot writer after the client exits.
	frameCtx, frameCancel := context.WithTimeout(runCtx, 3*time.Second)
	defer frameCancel()
	// A compositor crash after readiness is not a successful capture, even if latest.png exists.
	select {
	case e := <-exited:
		return fail(fmt.Errorf("compositor exited before capture: %v", e), ProcessFailure)
	default:
	}
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		_, e := LoadPNG(res.Artifacts["frame"])
		if e == nil {
			break
		}
		select {
		case <-frameCtx.Done():
			return fail(fmt.Errorf("no frame: %w", frameCtx.Err()), ProcessFailure)
		case e := <-exited:
			return fail(fmt.Errorf("compositor exited before frame: %v", e), ProcessFailure)
		case <-tick.C:
		}
	}
	checkFrame := res.Artifacts["frame"]
	if res.Artifacts["capture"] != "" {
		checkFrame = res.Artifacts["capture"]
	}
	checks, err := Evaluate(checkFrame, o.Expect, o.GoldenBase, out)
	if err != nil {
		return fail(err, ProcessFailure)
	}
	res.Checks = checks
	for _, c := range checks {
		if !c.Pass {
			res.Status = "failed"
			return res, Failed
		}
	}
	res.Status = "pass"
	return res, Pass
}

// frameIndex accepts only compositor screenshots, not latest.png or temporary files.
func frameIndex(name string) (int, bool) {
	if !strings.HasPrefix(name, "frame-") || !strings.HasSuffix(name, ".png") {
		return 0, false
	}
	digits := strings.TrimSuffix(strings.TrimPrefix(name, "frame-"), ".png")
	if len(digits) != 6 {
		return 0, false
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(digits)
	return n, err == nil
}
func captureRequestTime(path string) (time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, err
	}
	ns, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil || ns <= 0 {
		return time.Time{}, fmt.Errorf("invalid capture-request timestamp %q", strings.TrimSpace(string(data)))
	}
	return time.Unix(0, ns), nil
}

// nextFrame picks the second screenshot written after the commit token.
// NeferWL renders and writes screenshots one after another, so the first file
// finished after the token may have been rendered before it; the second began
// after the first finished and therefore shows the committed frame.
// An incomplete candidate is retried instead of selecting a later frame.
func nextFrame(dir string, after time.Time) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	first, second := int(^uint(0)>>1), int(^uint(0)>>1)
	names := map[int]string{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		n, ok := frameIndex(entry.Name())
		if !ok {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return "", e
		}
		if !info.ModTime().After(after) {
			continue
		}
		names[n] = entry.Name()
		if n < first {
			first, second = n, first
		} else if n < second {
			second = n
		}
	}
	return names[second], nil
}
func copyNextFrame(dir string, after time.Time, destination string) error {
	name, err := nextFrame(dir, after)
	if err != nil {
		return err
	}
	if name == "" {
		return os.ErrNotExist
	}
	// NeferWL writes frame-N directly. Retry an incomplete PNG on the next tick.
	path := filepath.Join(dir, name)
	if _, err := LoadPNG(path); err != nil {
		return os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0600)
}
func Evaluate(frame string, expect Expectations, base, out string) ([]Check, error) {
	img, err := LoadPNG(frame)
	if err != nil {
		return nil, err
	}
	checks := make([]Check, 0, len(expect.Probes)+len(expect.Regions)+len(expect.Goldens))
	add := func(name string, err error) {
		c := Check{Name: name, Pass: err == nil}
		if err != nil {
			c.Detail = err.Error()
		}
		checks = append(checks, c)
	}
	for _, p := range expect.Probes {
		want, _ := ParseColor(p.Color)
		add(p.Name, PixelProbe(img, p.X, p.Y, want, p.Tolerance))
	}
	for _, r := range expect.Regions {
		err := BoundsCheck(img, r.Rect)
		if err == nil && r.Color != "" {
			want, _ := ParseColor(r.Color)
			err = UniformRegion(img, r.Rect, want, r.Tolerance)
		}
		add(r.Name, err)
	}
	for i, g := range expect.Goldens {
		path := g.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		ref, e := LoadPNG(path)
		if e != nil {
			add(g.Name, e)
			continue
		}
		pass, ratio, diff, e := CompareGolden(img, ref, g.Tolerance, g.MaxDiffRatio)
		if diff != nil {
			e = errors.Join(e, SavePNG(filepath.Join(out, fmt.Sprintf("diff-%d.png", i)), diff))
		}
		if e == nil && !pass {
			e = fmt.Errorf("differing pixel ratio %.6f exceeds %.6f", ratio, g.MaxDiffRatio)
		}
		add(g.Name, e)
	}
	return checks, nil
}

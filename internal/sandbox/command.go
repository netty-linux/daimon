package sandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"
)

type execRunner struct {
	executable, stateDir string
	env                  []string
}
type boundedOutput struct {
	mu       sync.Mutex
	data     []byte
	limit    int
	overflow bool
	cancel   func() error
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	room := b.limit - len(b.data)
	if room < n {
		b.data = append(b.data, p[:max(0, room)]...)
		b.overflow = true
		if b.cancel != nil {
			_ = b.cancel()
		}
		return n, nil
	}
	b.data = append(b.data, p...)
	return n, nil
}
func (r *execRunner) Run(ctx context.Context, args []string) ([]byte, int, error) {
	argv := append([]string{"--embedded", "--json", "--state-dir", r.stateDir}, args...)
	cmd := exec.CommandContext(ctx, r.executable, argv...)
	cmd.Env = append([]string{}, r.env...)
	cmd.WaitDelay = 2 * time.Second
	if e := configureProcess(cmd); e != nil {
		return nil, 0, e
	}
	out := &boundedOutput{limit: MaxCommandOutput, cancel: cmd.Cancel}
	stderr := &boundedOutput{limit: MaxCommandError, cancel: cmd.Cancel}
	cmd.Stdout = out
	cmd.Stderr = stderr
	e := cmd.Run()
	finishProcess(cmd)
	if out.overflow || stderr.overflow {
		return nil, 0, errorOf(Capacity)
	}
	if e != nil {
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			return out.data, exit.ExitCode(), nil
		}
		if errors.Is(e, os.ErrNotExist) {
			return nil, 0, errorOf(ExecutableMissing)
		}
		return nil, 0, errorOf(Transport)
	}
	return append([]byte{}, out.data...), 0, nil
}

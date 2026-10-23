//go:build windows

package harness2bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsResourceLimit(t *testing.T) {
	cfg := integrationConfig(t)
	cfg.Worker = filepath.Join(t.TempDir(), "worker.py")
	// A valid reply after an allocation above the Job ceiling must never arrive.
	code := `import sys
sys.stdin.buffer.read()
bytearray(400*1024*1024)
print('{}')
`
	if err := os.WriteFile(cfg.Worker, []byte(code), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.Health(context.Background()); !errors.Is(err, ErrWorker) {
		t.Fatal("memory ceiling not enforced", err)
	}
}

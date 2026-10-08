package cuatest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// Build compiles a stdlib-only fixture locally, with no downloads or credentials.
func Build(t testing.TB, mode string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	name := "cua-driver"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", binary, "./ui/scripts/cua-fixture")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %v %s", err, out)
	}
	if err := os.WriteFile(binary+".mode", []byte(mode), 0600); err != nil {
		t.Fatal(err)
	}
	return binary
}

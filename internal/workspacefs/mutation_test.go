//go:build linux

package workspacefs

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRootRenamedRemovedAndConcurrentChecks(t *testing.T) {
	for _, action := range []string{"rename", "remove"} {
		t.Run(action, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "root")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			r, b, err := Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range 20 {
						err := b.Check()
						if err != nil && !errors.Is(err, ErrChanged) {
							t.Error(err)
						}
					}
				}()
			}
			if action == "rename" {
				err = os.Rename(root, root+"-moved")
			} else {
				err = os.Remove(root)
			}
			if err != nil {
				t.Fatal(err)
			}
			wg.Wait()
			if !errors.Is(b.Check(), ErrChanged) {
				t.Fatal("mutation not detected")
			}
			if ApplyIsolation().Strong {
				t.Fatal("detection is not isolation")
			}
		})
	}
}

package managedworkspace

import "context"

// Entry is an owned bounded snapshot value, not a live source handle.
type Entry struct {
	Path      string
	Directory bool
	Data      []byte
}

// Snapshot reuses the existing read-only Linux file identity/link checks.
// Callers own returned bytes. This does not grant permission to apply to source.
func Snapshot(ctx context.Context, directory string) ([]Entry, error) {
	entries, _, _, _, _, err := readSnapshot(ctx, directory, false)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(entries))
	for i, e := range entries {
		out[i] = Entry{e.Path, e.Directory, append([]byte{}, e.Data...)}
	}
	return out, nil
}

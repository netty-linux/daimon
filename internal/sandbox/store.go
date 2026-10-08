package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"
)

type record struct {
	Info    Info    `json:"info"`
	Name    string  `json:"name"`
	Ref     string  `json:"ref"`
	Profile Profile `json:"profile"`
}
type journal struct {
	Version int      `json:"version"`
	Records []record `json:"records"`
}

func validateRecord(r record) error {
	i := r.Info
	if ValidateID(i.ID) != nil || ValidateProfile(r.Profile) != nil || r.Name != ownedName(i.ID) || r.Ref != string(r.Profile.EffectivePlacement())+":"+r.Name || !sessionPattern.MatchString(i.OwnerSession) || i.Backend != r.Profile.Backend || i.Kind != Container || i.Placement != r.Profile.EffectivePlacement() || i.Runtime != "gvisor" || i.Image.Alias != "linux" || i.Network != "outbound" || i.CreatedAt.IsZero() || i.CreatedAt.Location().String() != "UTC" || i.Resources.LifetimeSeconds < 1 || i.Resources.LifetimeSeconds > 3600 {
		return errorOf(Persistence)
	}
	if !validExpiry(i.ExpiresAt) || i.Placement == Local && i.ExpiresAt != "" || len(i.Image.Resolved) > 256 {
		return errorOf(Persistence)
	}
	expected := limits(r.Profile, time.Duration(i.Resources.LifetimeSeconds)*time.Second)
	if i.Resources != expected || i.Browser != r.Profile.Browser || i.Network != r.Profile.Network || (i.Status == Deleted) != (i.Cleanup == "complete") || i.Cleanup == "unresolved" && i.Status != Failed {
		return errorOf(Persistence)
	}
	switch i.ErrorCategory {
	case "", ExecutableMissing, SignedOut, CloudUnavailable, Invalid, Unavailable, NotConfigured, NotFound, Capacity, Provision, Cleanup, Transport, Permission, DiskFull, Unsupported, Canceled, Protocol, Persistence:
	default:
		return errorOf(Persistence)
	}
	for _, ch := range i.Image.Resolved {
		if ch < 33 || ch > 126 {
			return errorOf(Persistence)
		}
	}
	if i.ComputerID != "" && i.ComputerID != computerID(i.ID) {
		return errorOf(Persistence)
	}
	switch i.Status {
	case Creating, Running, Deleting, Deleted, Failed:
	default:
		return errorOf(Persistence)
	}
	switch i.Cleanup {
	case "pending", "complete", "unresolved":
	default:
		return errorOf(Persistence)
	}
	return nil
}
func loadJournal(path string) (map[ID]record, error) {
	out := map[ID]record{}
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return nil, errorOf(Persistence)
	}
	defer root.Close()
	name := filepath.Base(path)
	info, e := root.Lstat(name)
	if errors.Is(e, os.ErrNotExist) {
		return out, nil
	}
	if e != nil || !info.Mode().IsRegular() {
		return nil, errorOf(Persistence)
	}
	f, e := root.Open(name)
	if e != nil {
		return nil, errorOf(Persistence)
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, MaxRegistryBytes+1))
	if e != nil || len(raw) > MaxRegistryBytes || strictJSON(raw) != nil || !exactSchema(raw, reflect.TypeFor[journal]()) {
		return nil, errorOf(Persistence)
	}
	var j journal
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&j) != nil || j.Version != 1 || j.Records == nil || len(j.Records) > MaxRecords {
		return nil, errorOf(Persistence)
	}
	owners := map[string]bool{}
	for _, r := range j.Records {
		if validateRecord(r) != nil || out[r.Info.ID].Info.ID != "" || owners[r.Info.OwnerSession] {
			return nil, errorOf(Persistence)
		}
		out[r.Info.ID] = r
		owners[r.Info.OwnerSession] = true
	}
	return out, nil
}

// The caller serializes the controlled single-writer journal. Intent is synced
// before runtime effects; directory fsync/external-writer exclusion are not promised.
func saveJournal(ctx context.Context, path string, records map[ID]record) (err error) {
	if e := ctx.Err(); e != nil {
		return e
	}
	j := journal{Version: 1, Records: []record{}}
	for _, r := range records {
		if validateRecord(r) != nil {
			return errorOf(Persistence)
		}
		j.Records = append(j.Records, r)
	}
	sort.Slice(j.Records, func(a, b int) bool { return j.Records[a].Info.ID < j.Records[b].Info.ID })
	raw, e := json.Marshal(j)
	if e != nil || len(raw)+1 > MaxRegistryBytes {
		return errorOf(Persistence)
	}
	raw = append(raw, '\n')
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return errorOf(Persistence)
	}
	defer root.Close()
	name := filepath.Base(path)
	if info, e := root.Lstat(name); e == nil && !info.Mode().IsRegular() || e != nil && !errors.Is(e, os.ErrNotExist) {
		return errorOf(Persistence)
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".sandboxes-*")
	if e != nil {
		return errorOf(Persistence)
	}
	tmp := filepath.Base(f.Name())
	committed := false
	defer func() {
		f.Close()
		if !committed {
			if e := root.Remove(tmp); e != nil && !errors.Is(e, os.ErrNotExist) {
				err = &Error{Kind: Persistence, Cause: errors.Join(err, e)}
			}
		}
	}()
	if f.Chmod(0600) != nil {
		return errorOf(Persistence)
	}
	for len(raw) > 0 {
		if e := ctx.Err(); e != nil {
			return e
		}
		n := len(raw)
		if n > 16384 {
			n = 16384
		}
		written, e := f.Write(raw[:n])
		if e != nil || written != n {
			return errorOf(Persistence)
		}
		raw = raw[n:]
	}
	if f.Sync() != nil || f.Close() != nil {
		return errorOf(Persistence)
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if root.Rename(tmp, name) != nil {
		return errorOf(Persistence)
	}
	committed = true
	return nil
}

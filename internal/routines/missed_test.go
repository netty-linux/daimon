package routines

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestInitializeMissedOnceNoSessionsAndNextNormalSlot(t *testing.T) {
	store, s, runtime, created := setup(t)
	ctx := context.Background()
	if err := s.Create(ctx, baseInput("routine"), created); err != nil {
		t.Fatal(err)
	}
	detection := created.Add(3*24*time.Hour + 2*time.Hour)
	expected := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	if err := s.Initialize(ctx, detection); err != nil {
		t.Fatal(err)
	}
	first, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || !first[0].MissedAt.Equal(expected) || !first[0].MissedDetectedAt.Equal(detection) || !first[0].LastAttemptAt.IsZero() || len(runtime.calls) != 0 {
		t.Fatal("catch-up or execution metadata", first)
	}
	views, err := s.List()
	if err != nil || views[0].State != "active" || views[0].Missed == nil || views[0].Missed.State != "missed" || views[0].SessionID != "" {
		t.Fatal("wrong activity", views, err)
	}
	views[0].Missed.DetectedAt = created
	for i := 0; i < 2; i++ {
		reopened, err := Open(store.path)
		if err != nil {
			t.Fatal(err)
		}
		restarted, err := New(reopened, runtime, refs{}, botRefs{}, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if err = restarted.Initialize(ctx, detection.Add(time.Duration(i+1)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		items, err := reopened.List()
		if err != nil || len(items) != 1 || !items[0].MissedAt.Equal(expected) || !items[0].MissedDetectedAt.Equal(detection) {
			t.Fatal("duplicate/replaced occurrence", items, err)
		}
		s = restarted
	}
	if len(runtime.calls) != 0 {
		t.Fatal("startup created Session")
	}
	if err = s.Tick(ctx, detection); err != nil {
		t.Fatal(err)
	}
	if len(runtime.calls) != 0 {
		t.Fatal("missed slot executed")
	}
	if err = s.Tick(ctx, first[0].NextAt); err != nil {
		t.Fatal(err)
	}
	if len(runtime.calls) != 1 {
		t.Fatal("next normal slot blocked")
	}
	after, err := store.List()
	if err != nil || !after[0].LastAttemptAt.Equal(first[0].NextAt) {
		t.Fatal("normal attempt missing")
	}
	if err = s.Initialize(ctx, first[0].NextAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	after, err = store.List()
	if err != nil || !after[0].MissedAt.Equal(expected) {
		t.Fatal("executed slot called missed")
	}
}

func TestInitializePausedFutureExactAndCanceled(t *testing.T) {
	for _, kind := range []string{"paused", "future", "exact", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			store, s, runtime, now := setup(t)
			in := baseInput("routine")
			in.Enabled = kind != "paused"
			if err := s.Create(context.Background(), in, now); err != nil {
				t.Fatal(err)
			}
			detection := now.Add(2 * time.Hour)
			ctx := context.Background()
			if kind == "future" {
				detection = now
			}
			if kind == "exact" {
				detection = now.Add(time.Hour)
			}
			if kind == "canceled" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			err := s.Initialize(ctx, detection)
			if kind == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if kind != "canceled" && err != nil {
				t.Fatal(err)
			}
			items, e := store.List()
			if e != nil || !items[0].MissedAt.IsZero() || len(runtime.calls) != 0 {
				t.Fatal("unexpected missed occurrence")
			}
		})
	}
}

func TestInitializeOldSlotAtExactNewSlotDoesNotBlockNormalFiring(t *testing.T) {
	store, s, runtime, now := setup(t)
	if err := s.Create(context.Background(), baseInput("routine"), now); err != nil {
		t.Fatal(err)
	}
	detection := now.Add(48*time.Hour + time.Hour)
	if err := s.Initialize(context.Background(), detection); err != nil {
		t.Fatal(err)
	}
	items, err := store.List()
	if err != nil || !items[0].NextAt.Equal(detection) || !items[0].MissedAt.Equal(detection.Add(-24*time.Hour)) {
		t.Fatal("current slot lost", items, err)
	}
	if err := s.Tick(context.Background(), detection); err != nil {
		t.Fatal(err)
	}
	if len(runtime.calls) != 1 {
		t.Fatal("normal exact slot blocked")
	}
}

func TestMissedTimezoneAndLatestOnlyRetention(t *testing.T) {
	store, s, runtime, now := setup(t)
	in := baseInput("routine")
	in.Timezone = "America/Sao_Paulo"
	if err := s.Create(context.Background(), in, now); err != nil {
		t.Fatal(err)
	}
	for day := 10; day < 50; day++ {
		detection := time.Date(2026, 10, day, 13, 0, 0, 0, time.UTC)
		if err := s.Initialize(context.Background(), detection); err != nil {
			t.Fatal(err)
		}
		items, err := store.List()
		expected := time.Date(2026, 10, day, 12, 0, 0, 0, time.UTC)
		if err != nil || len(items) != 1 || !items[0].MissedAt.Equal(expected) || !items[0].LastAttemptAt.IsZero() {
			t.Fatal("timezone/retention", items, err)
		}
	}
	if len(runtime.calls) != 0 {
		t.Fatal("offline execution")
	}
}

func TestStoreVersionOneCompatibilityAndStrictMissedMetadata(t *testing.T) {
	store, s, _, now := setup(t)
	ctx := context.Background()
	if err := s.Create(ctx, baseInput("routine"), now); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err = json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	value["version"] = float64(1)
	item := value["routines"].([]any)[0].(map[string]any)
	delete(item, "missed_at")
	delete(item, "missed_detected_at")
	legacy, _ := json.Marshal(value)
	if err = os.WriteFile(store.path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(store.path); err != nil {
		t.Fatal("legacy rejected", err)
	}
	if err = s.Initialize(ctx, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(store.path)
	if err = json.Unmarshal(data, &value); err != nil || value["version"] != float64(2) {
		t.Fatal("version 2 not persisted")
	}
	item = value["routines"].([]any)[0].(map[string]any)
	for _, bad := range []string{"missed_detected_at", "missed_at"} {
		old := item[bad]
		delete(item, bad)
		corrupt, _ := json.Marshal(value)
		if strict(corrupt) {
			t.Fatal("missing metadata accepted")
		}
		item[bad] = old
	}
	item["missed_detected_at"] = "2026-10-09T08:00:00Z"
	corrupt, _ := json.Marshal(value)
	if err = os.WriteFile(store.path, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(store.path); err == nil {
		t.Fatal("detection before slot accepted")
	}
}

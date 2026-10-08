package workspacefs

import "testing"

func TestApplyIsolationFailsClosed(t *testing.T) {
	i := ApplyIsolation()
	if i.Strong || i.Platform == "" || i.Reason == "" {
		t.Fatal("unproven isolation enabled")
	}
}

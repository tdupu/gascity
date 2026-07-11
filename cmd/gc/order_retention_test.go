package main

import (
	"io"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/orders"
)

// TestRetentionDefaultMatchesConfigDefault ensures the sentinel value used by
// orders.ValidateRetentionPolicy to detect "not explicitly overridden" matches
// config.DefaultOrderTrackingDeleteAfterClose. The orders package cannot import
// internal/config (cycle), so we verify the sync here.
func TestRetentionDefaultMatchesConfigDefault(t *testing.T) {
	fast := orders.Order{
		Name:     "sync-check",
		Trigger:  "cooldown",
		Interval: "1m",
		Formula:  "sweep",
	}
	// The SDK default must trigger the error (it is the "not overridden" sentinel).
	if err := orders.ValidateRetentionPolicy(fast, config.DefaultOrderTrackingDeleteAfterClose); err == nil {
		t.Fatalf("ValidateRetentionPolicy accepted fast order with SDK default %q — "+
			"orders.sdkDefaultOrderTrackingDeleteAfterClose may be out of sync with "+
			"config.DefaultOrderTrackingDeleteAfterClose",
			config.DefaultOrderTrackingDeleteAfterClose)
	}
	// Any explicit non-default value must pass.
	if err := orders.ValidateRetentionPolicy(fast, "48h"); err != nil {
		t.Fatalf("ValidateRetentionPolicy rejected fast order with explicit city override: %v", err)
	}
}

// TestScanOrderSetSnapshotFSRejectsHighFrequencyOrderWithoutRetentionPolicy
// verifies that a cooldown order firing faster than 15m is dropped during
// scanning when neither the order nor the city declares delete_after_close.
func TestScanOrderSetSnapshotFSRejectsHighFrequencyOrderWithoutRetentionPolicy(t *testing.T) {
	fs := fsys.NewFake()
	fs.Dirs["/city/orders"] = true
	fs.Files["/city/orders/fast-sweep.toml"] = []byte(`[order]
formula = "mol-sweep"
trigger = "cooldown"
interval = "5m"
`)
	// Empty config: no explicit [beads.policies.order_tracking] override.
	cfg := &config.City{}

	snapshot, err := scanOrderSetSnapshotFS(fs, "/city", cfg, io.Discard, "test")
	if err != nil {
		t.Fatalf("scanOrderSetSnapshotFS: %v", err)
	}
	for _, o := range snapshot.Orders {
		if o.Name == "fast-sweep" {
			t.Errorf("fast-sweep order should have been dropped by retention-policy validation")
		}
	}
}

// TestScanOrderSetSnapshotFSAllowsHighFrequencyOrderWithCityOverride verifies
// that a fast cooldown order is accepted when the city has an explicit
// [beads.policies.order_tracking].delete_after_close.
func TestScanOrderSetSnapshotFSAllowsHighFrequencyOrderWithCityOverride(t *testing.T) {
	fs := fsys.NewFake()
	fs.Dirs["/city/orders"] = true
	fs.Files["/city/orders/fast-sweep.toml"] = []byte(`[order]
formula = "mol-sweep"
trigger = "cooldown"
interval = "5m"
`)
	cfg := &config.City{}
	cfg.Beads.Policies = map[string]config.BeadPolicyConfig{
		"order_tracking": {DeleteAfterClose: "48h"},
	}

	snapshot, err := scanOrderSetSnapshotFS(fs, "/city", cfg, io.Discard, "test")
	if err != nil {
		t.Fatalf("scanOrderSetSnapshotFS: %v", err)
	}
	found := false
	for _, o := range snapshot.Orders {
		if o.Name == "fast-sweep" {
			found = true
		}
	}
	if !found {
		t.Errorf("fast-sweep order should be present with city override delete_after_close=48h")
	}
}

// TestScanOrderSetSnapshotFSAllowsHighFrequencyOrderWithOrderLevelPolicy
// verifies that a fast cooldown order is accepted when the order itself
// declares delete_after_close.
func TestScanOrderSetSnapshotFSAllowsHighFrequencyOrderWithOrderLevelPolicy(t *testing.T) {
	fs := fsys.NewFake()
	fs.Dirs["/city/orders"] = true
	fs.Files["/city/orders/fast-sweep.toml"] = []byte(`[order]
formula = "mol-sweep"
trigger = "cooldown"
interval = "5m"
delete_after_close = "48h"
`)
	cfg := &config.City{}

	snapshot, err := scanOrderSetSnapshotFS(fs, "/city", cfg, io.Discard, "test")
	if err != nil {
		t.Fatalf("scanOrderSetSnapshotFS: %v", err)
	}
	found := false
	for _, o := range snapshot.Orders {
		if o.Name == "fast-sweep" {
			found = true
		}
	}
	if !found {
		t.Errorf("fast-sweep order should be present when order declares its own delete_after_close")
	}
}

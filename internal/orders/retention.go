package orders

import (
	"fmt"
	"time"
)

// RetentionRequiredFrequency is the cooldown interval threshold below which an
// order must have an explicit delete_after_close retention policy — either at
// the order level (order.DeleteAfterClose) or at the city level
// ([beads.policies.order_tracking].delete_after_close) — to prevent unbounded
// accumulation of tracking beads. A cooldown order firing faster than this
// with only the SDK default 7d retention will accumulate thousands of closed
// tracking beads per day; see gastownhall/gascity issue gs-a4j.
const RetentionRequiredFrequency = 15 * time.Minute

// sdkDefaultOrderTrackingDeleteAfterClose mirrors
// config.DefaultOrderTrackingDeleteAfterClose. Kept in sync by
// TestRetentionDefaultMatchesConfigDefault in retention_test.go.
// The orders package cannot import internal/config (layering: orders is
// depended upon by config consumers, not the reverse), so we maintain the
// constant here as a sentinel for "not explicitly overridden."
const sdkDefaultOrderTrackingDeleteAfterClose = "7d"

// ValidateRetentionPolicy returns an error when the order fires more frequently
// than RetentionRequiredFrequency and has no explicit delete_after_close policy.
//
// An order satisfies the policy if ANY of the following is true:
//   - Its Trigger is not "cooldown" (cron/condition/event/manual orders are
//     exempt — their frequency is harder to derive statically).
//   - Its cooldown interval is at or above RetentionRequiredFrequency.
//   - The order itself has delete_after_close set (order.DeleteAfterClose != "").
//   - The city has overridden [beads.policies.order_tracking].delete_after_close
//     to something other than the SDK default "7d"
//     (cityOrderTrackingDeleteAfterClose != "" &&
//     cityOrderTrackingDeleteAfterClose != "7d").
//
// cityOrderTrackingDeleteAfterClose should be the city's effective
// [beads.policies.order_tracking].delete_after_close value after config
// composition (i.e., after ApplyBeadPolicyDefaults). When the city has not
// explicitly set the field, ApplyBeadPolicyDefaults fills in "7d", so the
// sentinel comparison catches both the absent case and the default case.
func ValidateRetentionPolicy(a Order, cityOrderTrackingDeleteAfterClose string) error {
	if a.Trigger != "cooldown" {
		return nil
	}
	interval, err := time.ParseDuration(a.Interval)
	if err != nil || interval <= 0 {
		// Malformed or missing interval is caught by Validate; skip here.
		return nil
	}
	if interval >= RetentionRequiredFrequency {
		return nil
	}
	// Order fires faster than the threshold — retention policy is required.
	if a.DeleteAfterClose != "" {
		return nil // order declares its own policy
	}
	cityExplicit := cityOrderTrackingDeleteAfterClose != "" &&
		cityOrderTrackingDeleteAfterClose != sdkDefaultOrderTrackingDeleteAfterClose
	if cityExplicit {
		return nil // city has an explicit override that applies globally
	}
	return fmt.Errorf(
		"order %q: cooldown interval %s is faster than %s but has no explicit "+
			"delete_after_close retention policy; add delete_after_close to "+
			"[beads.policies.order_tracking] in city.toml (e.g. delete_after_close = \"48h\") "+
			"or set delete_after_close on the order itself to prevent unbounded "+
			"tracking-bead accumulation",
		a.Name, a.Interval, RetentionRequiredFrequency,
	)
}

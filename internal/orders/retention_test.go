package orders

import (
	"strings"
	"testing"
)

// sdkDefault is the value ApplyBeadPolicyDefaults fills in when not explicitly
// set; mirrors config.DefaultOrderTrackingDeleteAfterClose. The sync is checked
// by TestRetentionDefaultMatchesConfigDefault in cmd/gc to avoid an import
// cycle (internal/config imports internal/orders).
const sdkDefault = "7d"

func TestValidateRetentionPolicy(t *testing.T) {
	tests := []struct {
		name         string
		order        Order
		cityDAC      string
		wantErr      bool
		errSubstring string
	}{
		{
			name: "cooldown under threshold no policy city default",
			order: Order{
				Name: "fast-sweep", Trigger: "cooldown", Interval: "5m",
				Formula: "sweep",
			},
			cityDAC:      sdkDefault,
			wantErr:      true,
			errSubstring: "delete_after_close",
		},
		{
			name: "cooldown under threshold city explicit override",
			order: Order{
				Name: "fast-sweep", Trigger: "cooldown", Interval: "5m",
				Formula: "sweep",
			},
			cityDAC: "4h",
			wantErr: false,
		},
		{
			name: "cooldown under threshold order-level policy set",
			order: Order{
				Name: "fast-sweep", Trigger: "cooldown", Interval: "5m",
				Formula: "sweep", DeleteAfterClose: "48h",
			},
			cityDAC: sdkDefault,
			wantErr: false,
		},
		{
			name: "cooldown at threshold boundary (exactly 15m) no policy",
			order: Order{
				Name: "boundary", Trigger: "cooldown", Interval: "15m",
				Formula: "sweep",
			},
			cityDAC: sdkDefault,
			wantErr: false, // 15m >= threshold → exempt
		},
		{
			name: "cooldown above threshold no policy",
			order: Order{
				Name: "slow-sweep", Trigger: "cooldown", Interval: "1h",
				Formula: "sweep",
			},
			cityDAC: sdkDefault,
			wantErr: false,
		},
		{
			name: "cooldown 1m no city override no order policy",
			order: Order{
				Name: "very-fast", Trigger: "cooldown", Interval: "1m",
				Formula: "sweep",
			},
			cityDAC:      sdkDefault,
			wantErr:      true,
			errSubstring: "1m",
		},
		{
			name: "cron trigger no policy (exempt)",
			order: Order{
				Name: "cron-order", Trigger: "cron", Schedule: "* * * * *",
				Formula: "sweep",
			},
			cityDAC: sdkDefault,
			wantErr: false,
		},
		{
			name: "manual trigger exempt",
			order: Order{
				Name: "manual-order", Trigger: "manual", Formula: "sweep",
			},
			cityDAC: sdkDefault,
			wantErr: false,
		},
		{
			name: "condition trigger exempt",
			order: Order{
				Name: "cond-order", Trigger: "condition", Check: "true",
				Formula: "sweep",
			},
			cityDAC: sdkDefault,
			wantErr: false,
		},
		{
			name: "city DAC empty (before ApplyBeadPolicyDefaults) with fast order",
			order: Order{
				Name: "fast-sweep", Trigger: "cooldown", Interval: "5m",
				Formula: "sweep",
			},
			cityDAC: "", // empty = no city override at all
			wantErr: true,
		},
		{
			name: "city DAC 48h explicit override",
			order: Order{
				Name: "fast-sweep", Trigger: "cooldown", Interval: "5m",
				Formula: "sweep",
			},
			cityDAC: "48h",
			wantErr: false,
		},
		{
			name: "order under threshold order DAC set to any value",
			order: Order{
				Name: "fast", Trigger: "cooldown", Interval: "30s",
				Formula: "sweep", DeleteAfterClose: "24h",
			},
			cityDAC: sdkDefault,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRetentionPolicy(tt.order, tt.cityDAC)
			if tt.wantErr && err == nil {
				t.Fatalf("ValidateRetentionPolicy() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("ValidateRetentionPolicy() = %v, want nil", err)
			}
			if tt.wantErr && tt.errSubstring != "" && !strings.Contains(err.Error(), tt.errSubstring) {
				t.Errorf("error %q does not contain expected substring %q", err.Error(), tt.errSubstring)
			}
		})
	}
}

// TestValidateRetentionPolicyActionableMessage verifies the error message
// guides the operator to a fix rather than just stating the problem.
func TestValidateRetentionPolicyActionableMessage(t *testing.T) {
	order := Order{
		Name: "fast-order", Trigger: "cooldown", Interval: "1m", Formula: "sweep",
	}
	err := ValidateRetentionPolicy(order, sdkDefault)
	if err == nil {
		t.Fatal("want error for fast order without retention policy")
	}
	msg := err.Error()
	checks := []string{
		"fast-order",
		"1m",
		"city.toml",
		"delete_after_close",
	}
	for _, want := range checks {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q missing expected term %q", msg, want)
		}
	}
}

package orders

import (
	"log"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
)

var runtimeHelpersLogf = log.Printf

// LastRunFuncForStore returns the latest order-run bead time for one store.
func LastRunFuncForStore(store beads.Store) LastRunFunc {
	return func(name string) (time.Time, error) {
		if store == nil {
			return time.Time{}, nil
		}
		label := "order-run:" + name
		// Order-run beads land in either tier: the ephemeral tracking bead
		// (wisps) created by the dispatcher and the molecule root (issues)
		// labeled after instantiation. Both carry the order-run label.
		results, err := store.List(beads.ListQuery{
			Label:         label,
			Limit:         1,
			IncludeClosed: true,
			Sort:          beads.SortCreatedDesc,
			TierMode:      beads.TierBoth,
		})
		if err != nil {
			if len(results) == 0 {
				return time.Time{}, err
			}
			runtimeHelpersLogf("orders: last-run lookup partially failed for %s: %v", name, err)
		}
		if len(results) == 0 {
			return time.Time{}, nil
		}
		return results[0].CreatedAt, nil
	}
}

// LastRunAcrossStores returns the most recent run time across a set of stores
// for a single order name.
func LastRunAcrossStores(stores ...beads.Store) LastRunFunc {
	return func(name string) (time.Time, error) {
		var latest time.Time
		for _, store := range stores {
			if store == nil {
				continue
			}
			last, err := LastRunFuncForStore(store)(name)
			if err != nil {
				return time.Time{}, err
			}
			if last.After(latest) {
				latest = last
			}
		}
		return latest, nil
	}
}

// CursorFuncForStore returns the max order-run seq for one store.
func CursorFuncForStore(store beads.Store) CursorFunc {
	return func(name string) uint64 {
		if store == nil {
			return 0
		}
		label := "order-run:" + name
		results, err := store.List(beads.ListQuery{
			Label:         label,
			Limit:         10,
			IncludeClosed: true,
			Sort:          beads.SortCreatedDesc,
			TierMode:      beads.TierBoth,
		})
		if err != nil {
			if len(results) == 0 {
				runtimeHelpersLogf("orders: cursor lookup failed for %s: %v", name, err)
				return 0
			}
			runtimeHelpersLogf("orders: cursor lookup partially failed for %s: %v", name, err)
		}
		if len(results) == 0 {
			return 0
		}
		labelSets := make([][]string, 0, len(results))
		for _, b := range results {
			labelSets = append(labelSets, b.Labels)
		}
		return MaxSeqFromLabels(labelSets)
	}
}

// IntervalHintFuncForStore returns a function that reads the next_interval_hint
// metadata value from the most recent order-tracking bead for a given scoped
// order name. An empty string is returned when no hint exists or the lookup
// fails; errors are logged and silently swallowed so a hint failure never
// blocks an order from firing.
func IntervalHintFuncForStore(store beads.Store) IntervalHintFunc {
	return func(name string) (string, error) {
		if store == nil {
			return "", nil
		}
		label := "order-run:" + name
		results, err := store.List(beads.ListQuery{
			Label:         label,
			Limit:         1,
			IncludeClosed: true,
			Sort:          beads.SortCreatedDesc,
			TierMode:      beads.TierBoth,
		})
		if err != nil && len(results) == 0 {
			return "", nil
		}
		if len(results) == 0 {
			return "", nil
		}
		return results[0].Metadata["next_interval_hint"], nil
	}
}

// IntervalHintAcrossStores returns the first non-empty next_interval_hint
// found across a set of stores for a given order name.
func IntervalHintAcrossStores(stores ...beads.Store) IntervalHintFunc {
	return func(name string) (string, error) {
		for _, store := range stores {
			if store == nil {
				continue
			}
			hint, err := IntervalHintFuncForStore(store)(name)
			if err != nil || hint == "" {
				continue
			}
			return hint, nil
		}
		return "", nil
	}
}

// CursorAcrossStores merges seq cursors from multiple stores.
func CursorAcrossStores(stores ...beads.Store) CursorFunc {
	fns := make([]CursorFunc, 0, len(stores))
	for _, store := range stores {
		if store != nil {
			fns = append(fns, CursorFuncForStore(store))
		}
	}
	return func(name string) uint64 {
		var latest uint64
		for _, fn := range fns {
			if seq := fn(name); seq > latest {
				latest = seq
			}
		}
		return latest
	}
}

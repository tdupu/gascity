# Phase 4 Kickoff Prompt

Paste the block below to start the next session.

---

Continue the gascity feature-flag rollout (PR-S2a) on branch `worktree-reconciler`
at `/data/projects/gascity/.claude/worktrees/reconciler`. Phases 1–3 are complete,
committed, local/UNPUSHED, inert: `bec9156b1` (S2-T1 interface + typed errors +
`Bead.Revision json:"-"`), `ec0bccd04` (S2-T2 conformance harness + S2-T3 MemStore
CAS), `da0d073a6` (S2-T3 FileStore CAS + out-of-band revision persistence). Read
`engdocs/plans/feature-flags/PHASE4-HANDOFF.md` first — it has the full status, the
verified BdStore surface map (file:line), the three resolved decisions, the
classifier design task, the Phase-5 landmines, gates, and gotchas. Build spec:
`engdocs/plans/feature-flags/PR-S2a-BUILD-SPEC.md` (keep its Progress block current).

Now build **Phase 4 = S2-T4 + S2-T5**: the BdStore conditional-write **result
classifier** and the lazy four-verb **capability probe + latch**, in a new
`internal/beads/bdstore_conditional.go` (+ struct fields on `bdstore.go` +
`internal/beads/bdstore_conditional_internal_test.go`). Per `EXECUTION-PLAN.md`
lines ~108-137 and `DESIGN.md` §8.2/§8.3.

Key constraints (from the resolved decisions — the plan's wording is stale here):
- The classifier is **message-substring matching on `err.Error()`**, NOT a numeric
  exit-code path (Decision 2). Mirror `isBdTransientWriteError`/`isBdAmbiguousWriteError`
  (bdstore.go:1873/1884). Say "exit-9/13 is a misnomer for this codebase" in the PR.
- bd **#4682 is UNLANDED**, so the precondition/unsupported/gate-refusal substrings
  are provisional — design a defensible set; the `//go:build integration` row
  (S2-T12, PR-S2b) against a real #4682 bd is the guard.
- The probe mirrors `bdReadyProjectionEnabled` (bdstore_ready_projection.go:69):
  lazy, memoized, four verbs (`update`/`close`/`assign`/`delete` `--help` grep for
  `--if-revision`), through the existing `s.runner` seam only (no 2nd probe seam).
  The runtime latch is authoritative over the probe.
- Do NOT add `var _ ConditionalWriter = (*BdStore)(nil)` yet — it needs all four
  verbs, which land in Phase 5.

Follow the standing process: **Fable design pass** (model `fable`, BOUNDED ask — a
past design agent stalled on an oversized single output; do the synthesis in the
main loop or ask for a focused critique) → **TDD** (classifier table test first,
red via the scripted runner fake, then green) → **Fable red-team BEFORE the commit**
(read-only on the shared worktree; have it propose mutations, you run them to prove
teeth) → full gates (full `go test ./internal/beads/...` not `-run`, vet,
golangci-lint, gofumpt, the `OpenAPISpecInSync|EventPayload` wire gate) → commit
with trailer `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

Then Phase 5 (S2-T6/T7: the three `*IfMatch` verbs + a dedicated `runConditionalWrite`
retry wrapper that NEVER routes through `runBDTransientWrite`; `CompareAndSetMetadataKey`
bounded emulation + typed `CASRetriesExhaustedError`; the SQL-spike verdict as a dated
note) — and Phase 5 is where **F2 (bd `ga-zj78gu`)** fires: `DoltliteReadStore` embeds
`*BdStore` and will falsely promote a zero-revision CAS writer; read that bead before
writing Phase 5. Then Phase 6 (S2-T8 CachingStore evict-never-patch, the livelock
merge gate). Do NOT push. Do NOT start S3/PR-S2b without checking in — S3 is
outward-facing (deploy-lineage sync + the live maintainer-city flip).

# Phase 4 Handoff — BdStore ConditionalWriter (classifier + probe, then verbs + emulation)

Pick-up doc for the next session. **PR-S2a Phases 1–3 are complete** (S2-T1, T2,
T3-mem, T3-file — the interface, typed errors, conformance harness, and the two
native stores that don't need `bd`). This hands off **Phase 4** (S2-T4/T5: the
BdStore result classifier + capability probe), then **Phase 5** (S2-T6/T7: the
conditional verbs + bounded CAS emulation + the SQL spike verdict) and **Phase 6**
(S2-T8: CachingStore evict-never-patch). All still **INERT** — zero consumers.

The kickoff prompt is in `PHASE4-PROMPT.md` (paste it to start). This doc is the
grounding; the build spec is `PR-S2a-BUILD-SPEC.md` (has a live Progress block).

---

## Status — what's committed (branch `worktree-reconciler`, local/UNPUSHED)

| Commit | Task | Content |
|--------|------|---------|
| `44eb2ab70` | docs | `PR-S2a-BUILD-SPEC.md` (the build plan; keep its Progress block current) |
| `bec9156b1` | S2-T1 | `ConditionalWriter` interface + normative revision/granularity contract; 4 typed errors (`ErrConditionalWriteUnsupported`, `PreconditionFailedError`, `GateRefusalError`, `CASRetriesExhaustedError`) with `IsX` helpers; `Bead.Revision int64 json:"-"`; `ConditionalWriterFor`/`ConditionalWriterHandleProvider`; `bdIssue.Revision` + `toBead` stamping |
| `ec0bccd04` | S2-T2, T3-mem | `beadstest/conditional_writer_conformance.go` harness; MemStore native CAS + `DisableConditionalWrites`; the FileStore **shadow** (returns unsupported, replaced in 3b) |
| `da0d073a6` | S2-T3-file | FileStore native CAS (flock-wrapped) + out-of-band `fileData.Revisions` persistence |

**Do not push** (local integration stack). **Two untracked dirs are NOT mine** —
`engdocs/plans/beads-cas/`, `engdocs/plans/reconciler-redesign/`. Leave them; never `git add` them.

## The three resolved decisions (still authoritative — override stale plan wording)
1. `Bead.Revision` is `json:"-"` (wire byte-untouched). DONE in Phase 1.
2. **The bd classifier is MESSAGE-SUBSTRING matching, not a numeric exit-code path.** BdStore has no exit-code path; every existing classifier (`isBdTransientWriteError` bdstore.go:1873, `isBdNotFound` :812, `isBdAmbiguousWriteError` :1884) matches on `err.Error()` substrings. Do the SAME for CAS. The plan's "exit-9/exit-13 classifier" is a MISNOMER for this codebase — implement it message-based and say so in the PR.
3. Method names mirror `Update`/`Close`/`Delete` → `UpdateIfMatch`/`CloseIfMatch`/`DeleteIfMatch`/`CompareAndSetMetadataKey`. DONE in the interface.

## Remaining task list (PR-S2a)
- **Phase 4 = S2-T4 + S2-T5** — `EXECUTION-PLAN.md` lines ~108-137. Classifier (pure fn) + lazy four-verb probe + latch. New file `internal/beads/bdstore_conditional.go` + struct fields on `bdstore.go` + `internal/beads/bdstore_conditional_internal_test.go`.
- **Phase 5 = S2-T6 + S2-T7** — plan lines ~139-170. The three `*IfMatch` verbs + a dedicated `runConditionalWrite` retry wrapper; `CompareAndSetMetadataKey` bounded emulation + typed exhaustion; the SQL spike verdict (dated note). Add `var _ ConditionalWriter = (*BdStore)(nil)` HERE (needs all four methods). **F2 fires here** (see below).
- **Phase 6 = S2-T8** — plan lines ~172-185. CachingStore forward-and-EVICT (never patch); the livelock regression is a MERGE GATE.
- Then **PR-S2b** (S2-T10..T12: factory mode-stamp + `ResolveConditionalWriter` thin adapter, the `beads.conditional_writes.degraded` event REGISTERED-only, the `//go:build integration` BdStore conformance row) — a separate PR/session. S2-T9 sqlite is deferred out of S2.

DESIGN detail: `DESIGN.md` §8.2 (classifier table + retry policy, bdstore.go:1238-1259), §8.3 (probe, :1261-1306), §8.4 (emulation + SQL spike, :1308-1350), §8.5 (CachingStore evict, :1352-1364).

---

## BdStore surface map (verified file:line — don't re-explore)

- **BdStore struct:** `bdstore.go:294-305` (`dir`, `runner CommandRunner`, `idPrefix`, and the `readyProjectionMu/Checked/Enabled` probe trio). Add here: `condWriteMu sync.Mutex`, `condWriteProbed/condWriteCapable/condWriteLatched bool`.
- **Runner seam:** `CommandRunner func(dir, name string, args ...string) ([]byte, error)` at `bdstore.go:29`. Called as `s.runner(s.dir, "bd", args...)`. This is the ONLY subprocess seam — probe and conditional writes both go through it (no second `WithBDCapabilityProbe` option, per DESIGN §8.3 / §7.4).
- **Probe pattern to MIRROR:** `bdReadyProjectionEnabled` at `bdstore_ready_projection.go:69-88` — mutex, `if s.condWriteProbed { return … }`, lazy on first call, memoized. Your probe loops `for _, verb := range []string{"update","close","assign","delete"}` running `s.runner(s.dir, "bd", verb, "--help")` and greps the output for `--if-revision`; any miss → incapable. Latch (`condWriteLatched`) is authoritative over the probe in `conditionalWritesCapable()`.
- **Classifier inputs:** `s.runner` returns `(out []byte, err error)` where on failure `err.Error()` already carries the bd detail (see `classifyBDExecResult` bdstore.go:179-200 — it wraps `%w: %s` with the stderr/stdout detail). So `classifyConditionalWriteResult(out []byte, err error) error` is pure over exactly what the runner returns.
- **Classifiers to mirror (message-substring style):**
  - `isBdTransientWriteError` (bdstore.go:1873) matches `"Error 1213 (40001): serialization failure"`, `"this transaction conflicts with a committed transaction"`, `"failed to prepare catalog"`, + ambiguous.
  - `isBdAmbiguousWriteError` (bdstore.go:1884) matches `"i/o timeout"`, `"invalid connection"`, `"bad connection"`, `"connection reset"`, `"broken pipe"`, `"timed out after"`, `"deadline exceeded"` — write MAY have committed → return as-is.
  - `isBdNotFound` (bdstore.go:812) → `ErrNotFound`.
  - `extractJSON(out)` idiom (used at bdstore.go:1017/1112) tolerates log-noise-wrapped JSON — use it to parse the precondition body.
- **The blind loop to AVOID:** `runBDTransientWriteOutputWhen` (bdstore.go:1811, budget `bdTransientWriteAttempts=3`, 25ms backoff) via `runBDTransientWrite`/`runBDTransientWriteOutput` (:1793-1800). Conditional writes must NOT route through these — write a NEW `runConditionalWrite` wrapper (DESIGN §8.2 last ¶). The doltlite `--dolt-auto-commit` prefix comes from `s.bdTransientWriteArgs(args)` (called at :1813) — your wrapper must still apply it.
- **bd-sql CAS precedent (for the S2-T7 SQL spike):** `ReleaseIfCurrent` at `bdstore.go:1097` (issues `bd sql --json UPDATE … WHERE … AND status/assignee`, reads rows_affected; embedded-dolt fallback `releaseIfCurrentViaEmbeddedDoltSQL` ~:1141; SQL literal escaping `bdSQLStringLiteral`). The SQL-spike disqualifier: the raw UPDATE must ALSO `revision = revision + 1` atomically or it breaks the contract — if it can't, the emulation loop ships and the SQL path is dropped, not half-adopted. Record the verdict as a dated note in `engdocs/plans/feature-flags/`.
- **`bdIssue.Revision`** (bdstore.go:~617) already decodes `json:"revision,omitempty"` and `toBead()` (:768) stamps it. Key name provisional pending #4682.
- **Test double:** `fakeRunner` at `bdstore_test.go:19-31` — map keyed by `name + " " + strings.Join(args, " ")` → `{out []byte, err error}`. For Phase 4/5 you need per-argv `out`+`err` (works for distinct argv) AND, for the committed-but-ambiguous cell (§7.4), a richer scripted runner whose apply-func mutates fake backing state BEFORE returning the ambiguous err. `bdstore_test.go` is `package beads_test` (black-box); put white-box internal tests in a `package beads` file (`bdstore_conditional_internal_test.go`).

## The classifier design task (Phase 4's crux — read carefully)

`classifyConditionalWriteResult(out, err) error` maps a bd write result to one of:
`nil` (success) · `*PreconditionFailedError{Expected, Current}` · `ErrConditionalWriteUnsupported` (LATCHES) · `*GateRefusalError` (per-write, never latches) · transient/ambiguous (return as-is for the retry wrapper) · else existing classification (`isBdNotFound`→`ErrNotFound`).

DESIGN §8.2 table (translate each row to a message-substring rule):
| Signal | → | Latches? |
|---|---|---|
| precondition failure, body parses `{expected_revision, current_revision}` | `*PreconditionFailedError{Expected, Current}` | no |
| precondition failure, body unparseable | `*PreconditionFailedError` zero Expected/Current, `Raw` set | no |
| unsupported: body code `conditional-write-unsupported` OR usage/unknown-flag mentioning `--if-revision` | `ErrConditionalWriteUnsupported` | **YES** |
| exit-13-class gate refusal, any other body code (e.g. close-authority) | `*GateRefusalError` | no |
| ambiguous class (`isBdAmbiguousWriteError`) | return as-is | no |
| else | existing (`isBdNotFound`→ErrNotFound, …) | no |

Two load-bearing rules: (1) unsupported latch is **body-code / message gated, never a bare exit number** — a policy refusal must not silently degrade every future fenced write; (2) precondition body-parse is **defensive** (tolerate noise via `extractJSON`; misparse → zero-valued `PreconditionFailedError`, never a different class).

**REALITY: bd #4682 is UNLANDED.** bd does not emit `--if-revision`, a revision column, or precondition/unsupported bodies yet (verified: `/data/projects/beads` has none). So the exact substrings are **provisional**, exactly like the `revision` wire key (F6). Design a defensible set now; the `//go:build integration` conformance row against a #4682-capable bd (S2-T12, PR-S2b) is the ultimate guard — say so in the PR. Today's pre-#4682 bd emits a generic usage error for `--if-revision` → that's the `ErrConditionalWriteUnsupported` (latches) row, which the probe (`--help` grep) catches first anyway.

**`PreconditionFailedError.Expected` must be the caller's argument.** The conformance harness now asserts `Expected == stale` UNCONDITIONALLY (only `Current` is gated behind `ConditionalWriterOptions.SuppliesCurrent`). So when the verb wrapper (Phase 5) builds a `PreconditionFailedError` from the classifier, it must set `Expected` to the revision the caller passed, even if bd's body omits it. For the integration row, set `SuppliesCurrent` only if the bd body reliably carries `current_revision`.

## Phase-5 landmines (don't forget)
- **F2 = bd `ga-zj78gu` fires the moment BdStore implements ConditionalWriter.** `DoltliteReadStore` (`internal/beads/doltlite_read_store.go`) embeds a concrete `*BdStore`, so the CAS methods PROMOTE through it — but its SQL `scanBead` (~:1356) never populates `Revision` → `Get`→0 → every CAS `PreconditionFailedError` forever in the `GC_NATIVE_DOLTLITE_BEADS` deployment, and promoted writes bypass its `resetOrderRunCache()` (:523). FIX in Phase 5: EITHER populate `Revision` in doltlite `scanBead` + override the four CAS verbs to invalidate the order-run cache, OR expose `ConditionalWriterHandle()` returning `(nil, false)` so it doesn't falsely claim capability. Read `ga-zj78gu` before writing Phase 5. Secondary: `internal/beads/exec/exec.go:136` (`beadWire.toBead`) is a 2nd bd-JSON envelope that drops revision.
- **Compile assert** `var _ ConditionalWriter = (*BdStore)(nil)` compiles only once all four methods exist → add it in Phase 5, not Phase 4.
- **The `assign` verb** is probed (a consumer uses assign) even though there's no `AssignIfMatch` — keep all four in the probe.

## Process (the user's standing method — non-negotiable)
1. **Fable design pass** (model `fable`) over the phase — resolve the classifier substring set, the scripted-runner shape, the retry policy, the emulation loop, and the SQL-spike verdict. No code. **Keep the ask BOUNDED** — the design agent stalled once on an oversized single-file spec ([workflow-big-generation-stall]); prefer a focused critique/design over a giant generated doc, or do the synthesis in the main loop.
2. **TDD** in the main loop: classifier table test first (red via scripted runner), then impl green. ≤5 files/phase; verify each.
3. **Fable red-team BEFORE every commit** (read-only on the shared worktree — uncommitted changes mean an isolated worktree won't see them; per [redteam-mutation-shared-worktree]). Have it PROPOSE mutations; you run them in the main loop to prove teeth. Fold confirmed findings; document residue.
4. **Full gates**, then commit. Trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

## Gates / verification (run every phase)
- `go build ./internal/beads/...`
- `go test ./internal/beads/ ./internal/beads/beadstest/` — **FULL package, not `-run`** (the pre-commit hook skips package guards; latent failures only show in a full run).
- `go test ./internal/beads/ -run 'Conditional' -race` (the emulation/probe touch goroutine-free paths, but keep the habit).
- `go vet ./internal/beads/...` ; `golangci-lint run ./internal/beads/` (retry on "parallel golangci-lint is running").
- `gofumpt -l <changed>` (binary `/home/ubuntu/go/bin/gofumpt`) → empty.
- **Wire gate:** `go test ./internal/api/ -run 'OpenAPISpecInSync|EventPayload'` green (Phase 4/5 touch no wire type, but confirm).
- Pre-commit hook runs lint-changed + doc-gen + vet + docsync automatically; it works on this worktree.

## Reusable assets from Phases 1–3 (use them; don't reinvent)
- Harness: `beadstest.RunConditionalWriterConformanceWithOptions(t, name, open, ConditionalWriterOptions{SuppliesCurrent, OpenDisabled})`. For BdStore, unit conformance is limited (needs real bd) — S2-T7 says run the harness row over BdStore+scripted fake "where scriptable"; the authoritative row is the S2-T12 integration build.
- Typed errors + `IsPreconditionFailed`/`IsGateRefusal`/`IsCASRetriesExhausted`/`IsConditionalWriteUnsupported` helpers already exist (beads.go).
- **Mutation-testing discipline (proves conformance/test teeth):** back up the file to `$CLAUDE_JOB_DIR/tmp` (`/home/ubuntu/.claude/jobs/<id>/tmp`), mutate with a `python3` string-replace, run the specific subtest (expect FAIL), then `cp -f` the backup back and `diff` to confirm identical. **NEVER `git checkout <file>`** to revert — it wipes ALL uncommitted changes ([S2-HANDOFF gotcha]).

## Gotchas (learned this stack)
- **Dolt is LOCAL-ONLY** — `git push` only; never `bd dolt push/pull/remote`.
- **Red-team on the shared worktree:** read-only prompt + `git diff`, or `isolation:'worktree'` (but that won't have uncommitted changes). Have it PROPOSE mutations, don't let it edit.
- **New test package** needs a generated `testenv_import_test.go` (`go run scripts/add-testenv-import.go`) — but `internal/beads` is not new, so N/A here.
- **`go clean -cache` is BANNED**; cold build via `GOCACHE=$(mktemp -d) go build ...`.
- **Pre-existing unrelated flake:** `TestStreamSessionPeekAcceptsPeekCapability` fails under `-race` in `internal/api` (bd `ga-69hv8k`) — not rollout work; filter it out.
- The revision contract carves derived-projection columns (bd `is_blocked`) OUT of the bump guarantee (F1) — the conformance suite must never assert whether a cross-bead/dep write bumps.

## After PR-S2a
PR-S2b = S2-T10 (factory mode-stamp via a `ModeStamped` optional interface + `ResolveConditionalWriter(store)` thin adapter over the general `rollout` resolver — NO mode branching in `internal/beads` outside the stamp; reuses `ConditionalWriterFor`) + S2-T11 (`beads.conditional_writes.degraded` event REGISTERED-only) + S2-T12 (the `//go:build integration` BdStore conformance row). **Checkpoint with the user before S3** — S3 has outward-facing steps (deploy-lineage sync + the live maintainer-city flip).

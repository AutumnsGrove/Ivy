# G1: generalising the gate (5a.1)

Gate **G1** of `docs/CHUNK5-BRIEF.md` section 5: the design asked for **before generalising the
gate**. It covers the request types, how the one-ledger-row-plus-counter transaction extends to them,
the withheld-mail and ask-selection hooks, and the extended architecture test. Nothing here is
implemented. Read `docs/chunk5/5a-gate-ledger-spend.md` for the decisions this builds on (caps per
account plus one global, pause at the cap, a code-defined model registry, mandatory estimates).

## What the code does today, and what is wrong with it

`llm/gate.go` guards embeddings only. Five properties of it matter, because the design fixes them
rather than copying them:

1. **The caller supplies the policy.** `EmbedRequest` carries `Enabled` and `CapUSD`, which
   `cmd/embed.go` and `search/search.go` fill from `config.Account.LLMEnabled` and
   `cfg.LLM.MonthlyCapUSD`. The gate believes them. A new caller that passes `Enabled: true` has
   bypassed the opt-in with no test failing.
2. **The caller owns the provider.** `cmd/embed.go` calls `llm.NewOpenRouter(...)` and puts the
   `Embedder` in the request. Any package can therefore call `emb.Embed(...)` directly and skip the
   gate. `llm/arch_test.go` only searches for endpoint **strings** outside `llm/`, so it cannot see
   this. The brief's invariant 1 ("one chokepoint") is not actually enforced.
3. **The cap is per endpoint and racy.** `MonthlySpend(account, period, endpoint)` reads one
   endpoint's counter, so Jev, chat and vision would each get a separate cap. It is also
   check-then-call-then-record, so concurrent calls can all pass the check and overshoot.
4. **A refusal has no reason.** The ledger records `outcome = "refused"` but not why (opt-in off, cap,
   withheld, feature off), so the stats panel cannot show "calls the gates blocked, by cause".
5. **A failed ledger write is only logged.** The counter is the cap's only record of spend, so a
   broken ledger silently disables the cap.

## Design

### Providers belong to the gate

`Gate` is built with the provider settings (base URL from config, API key from `data/secrets`, the
Ollama URL) and constructs its own clients, all unexported (`openRouterEmbed`, `jev`, `chat`,
`vision`, `ollama`). `llm.NewOpenRouter`, `llm.NewOllama` and the exported `Embedder` interface go
away. A request names a **provider kind** (`ProviderOpenRouter`, `ProviderOllama`), never a client.
Tests inject fakes through gate options inside package `llm`, and everything else points at the
`internal/mailworld` fake by base URL, as today.

### Four typed entry points, one admission path

```go
func (g *Gate) Embed(ctx, EmbedRequest) ([]Vector, error)
func (g *Gate) Decide(ctx, DecideRequest) (DecideResult, error)     // Jev, /systemone
func (g *Gate) Complete(ctx, CompleteRequest) (CompleteResult, error) // chat
func (g *Gate) See(ctx, SeeRequest) (SeeResult, error)               // vision
```

Each request carries `Feature` (a name from the feature table below), `AccountIDs` (one for every
automated feature, the selected set for Ask), `ContentKeys` (per input, empty for a call with no mail
such as the rule compiler), and the payload. **No request carries `Enabled`, `CapUSD`, a model id's
price or a provider client.** The gate resolves all of those itself.

Every entry point runs the same internal sequence, so a new feature cannot skip a step:

```
admit(ctx, Admission) -> Reservation | refusal
  provider call (deadline, concurrency slot)
settle(ctx, Reservation, result) -> ledger rows + counters, one transaction
```

`admit` is the single policy check and returns a refusal with a **reason** from a closed set:
`feature_off`, `not_enabled`, `vision_off`, `cap_account`, `cap_global`, `withheld`, `too_large`,
`no_provider`, `ledger_unwritable`. Order of checks: provider present, size bounds, feature switch,
account opt-in (read from `store.Account.LLMEnabled` / `VisionEnabled`), vetting (below), caps.

### The feature table

A small table in `llm/features.go` is the only place a feature is described:

| feature | endpoint | needs vetted mail | notes |
|---|---|---|---|
| `search`, `embed` | embeddings | no | existing behaviour, unchanged |
| `injection_tripwire`, `sensitive_content` | systemone | no | they are the check |
| `needs_me`, `classify`, `junk_rescue` | systemone | no | classifiers read clipped state only |
| `needs_me_stage2`, `summary`, `digest`, `extraction`, `compiler` | chat | yes, except `compiler` | `compiler` sees no mail |
| `vision` | vision | yes | also `VisionEnabled` |
| `ask`, `claim_check` | chat / systemone | yes | the selected accounts, all must pass |

An unknown feature name is refused (`feature_off`), so a typo fails closed. The table is data the
architecture test also reads.

### Vetting hook (the withheld rule, fail-closed)

`admit` calls an injected `Vetting` interface for every content key of a feature that needs vetted
mail:

```go
type Vetting interface { Vetted(ctx context.Context, accountID, contentKey string) (bool, error) }
```

"Vetted" means the tripwire and sensitive checks have run **and** did not withhold the message. 5a
ships the default implementation, which returns `false` for everything, so every feature that needs
vetted mail is refused until 5c.0 provides the real one (decision 6). That is the fail-closed rule (5d decision 2)
holding by construction rather than by remembering to check. The error path also fails closed.

### Ask selection

A request with several `AccountIDs` (Ask only; every other feature must pass exactly one, enforced in
`admit`) requires **every** listed account to be opted in and under its cap, or the whole call is
refused naming the account. The ledger attributes the call's cost across the selected accounts in
equal shares (one row per account, shared `call_id`), so per-account caps stay honest. (Open question 3.)

### Caps

Two checks, both summed across endpoints: the account's month-to-date spend against its cap, and the
global month-to-date spend against the global cap. Caps and feature switches live in state.db
settings (`store.GetSetting`), with defaults when absent; `cfg.LLM.MonthlyCapUSD` becomes the default
per-account cap (it is $5 today) and a new `llm.global_cap_usd` the default global one. The cap is
checked against **recorded spend plus the in-flight reservations plus this call's worst-case
estimate**, which closes the race (property 3): the gate keeps an in-memory map of reserved dollars per
account and globally under one mutex (Ivy is a single process; a crash only drops short-lived
reservations). The worst case is `input tokens x registry input price + max output tokens x output
price`, from the model registry (`llm/models.go`).

### The ledger

State migration 18, append-only:

- `api_calls.reason TEXT NOT NULL DEFAULT ''`: the refusal cause above; empty for ok/error/rejected.
- Nothing else changes in `api_calls`. The per-call question set and probability vector (5a decision 6)
  arrive with the Jev layer in 5b as a separate `api_call_detail(call_id, detail_json)` table, so the
  hot table stays small; 5a only reserves the idea.

`settle` writes the rows and the counters in one `RecordAPICalls` transaction exactly as today. New
rule: if that write fails, the gate trips a **breaker** that refuses further calls with
`ledger_unwritable` until a probe write succeeds, so a broken ledger can never disable the cap
(property 5). The failure is logged at error level either way.

Counters already key on (account, period, endpoint); the cap queries add `SUM` over endpoints. No schema
change is needed for that.

### Bounds and concurrency

Per-endpoint concurrency slots in the gate (initial: Jev 8, chat 4, vision 2, embeddings 1, all
constants in `llm/` and shown on the stats panel when saturated; Polaris saw no throttling at 40
concurrent Jev calls, so 8 is conservative). Per-call deadlines: Jev 15 s, chat 90 s, vision 120 s,
embeddings as today. A call over a slot waits on the context, never forever. Size bounds: the existing
`MaxBatchInputs` and `MaxInputBytes` for embeddings; for Jev, state plus question tokens under the 32k
context with margin (the caller clips; the gate refuses as `too_large`); for chat and vision, a
per-request byte bound set with the first user of each in 5c and 5h.

### The extended architecture test

`llm/arch_test.go` gains three assertions, all failing the build:

1. **No endpoint string outside `llm/`** for `/systemone`, `/chat/completions`, `/embeddings` and the
   vision path (the existing check, now also covering the vision URL).
2. **No exported provider constructor or client interface.** A scan of `llm`'s exported identifiers
   (go/parser over the package) fails if it exports `NewOpenRouter`, `NewOllama`, `Embedder`, `Jev`,
   `Chat`, `Vision`, or any func returning a type with a `Call`/`Embed`/`Complete`/`See` method. This is
   the check that closes property 2.
3. **Every feature in a gate call is in the feature table.** A scan for `Feature:` literals in
   production code outside `llm/` fails on a name the table does not contain.

### Migration of the two existing callers

`cmd/embed.go` and `search/search.go` drop `Embedder`, `Enabled` and `CapUSD` from their requests and
pass `Provider` (from the account's `EmbedProvider`) and the feature name. The Ollama path stays
exempt from opt-in and cap (it never leaves the device) and still records zero-cost rows. Behaviour for
embeddings is otherwise unchanged and its existing tests must stay green untouched.

## Tests written first (each seen failing for the right reason)

1. A recording fake provider fails the test on **any** request when the account is off, for each of the
   four entry points, and the ledger shows one refused row with the right `reason`.
2. A caller that never sets an opt-in flag (there is none to set) and an account with
   `LLMEnabled = false` is refused; flipping the stored account setting, not the request, changes it.
3. Cap: account cap reached refuses with `cap_account`; global cap with `cap_global`; both at zero cost;
   a new month resets. **Concurrency:** N goroutines race a cap with room for one call; at most one
   reaches the provider (this is the test that fails on the current code's check-then-call).
4. Reservation release: a failed or cancelled call releases its reservation, so the cap does not leak.
5. Ledger breaker: with `RecordAPICalls` forced to fail, the next call is refused `ledger_unwritable`,
   and recovers after one successful write.
6. Vetting: with the default `Vetting`, `needs_me_stage2`, `summary`, `ask` and `vision` are refused
   `withheld` and send nothing; `injection_tripwire` and `compiler` are not.
7. Ask selection: two accounts, one off, the whole call refused; both on, one row per account with the
   cost shares summing to the provider's number.
8. The three architecture-test assertions, each shown failing against a deliberately violating file.
9. Embeddings regression: the existing `llm`, `search` and `cmd` tests pass unchanged.

## Not in G1

The Jev request/response parsing (5b), the model registry's contents and the settings screens (5a.2 and
5a.3 once this is cleared), the spend API, the per-feature model override UI, the estimate helper for
bulk actions. G1 only fixes the shape so they fit.

## Decisions (operator, 2026-10-07): G1 is cleared

All six questions were answered as recommended. 5a.1 may start, tests first.

1. **The gate owns the providers.** `NewOpenRouter`, `NewOllama` and the `Embedder` interface leave the
   public API; callers name a provider kind. The architecture test's second assertion enforces it.
2. **One process.** Reservations are in memory under a mutex; no database reservation table.
3. **Ask cost is attributed in equal shares** across the selected accounts, one ledger row per account
   with a shared `call_id`.
4. **The global cap default lives in settings with a built-in default;** no new `ivy.yaml` key. The
   existing `llm.monthly_cap_usd` stays as the default per-account cap.
5. **The breaker refuses all calls** with `ledger_unwritable` until a probe write succeeds.
6. **The vetting machinery is built early, as the first step of 5c (5c.0),** not left to 5d: the
   tripwire and sensitive checks, the withheld fact and the real `Vetting` implementation. The rest of
   5d (categories, junk rescue, banners, mail types) stays in 5d. Until 5c.0 lands, the default `Vetting`
   refuses every vetted-mail feature, which is the intended fail-closed state.

## As built (5a.1, 2026-10-08)

The design held. These are the places the code differs from it or fills a gap, so a reviewer reads the
code against the truth and not against the sketch above.

- **The opt-in source is an injected `AccountPolicy`, not `store.Account.LLMEnabled`.** The mirror row's
  `llm_enabled` is never set by sync (`gateway/read.go` says so), so the design's source was wrong in
  production. The policy lives in `llm/policy.go` and is given to the gate at construction (never per
  request): an account in `ivy.yaml` keeps its startup choice, an app-connected account is read live from
  `state.db` `account_configs`, a seeded dev account falls back to its mirror row, an unknown account or a
  read error is off. `cmd.NewEmbedding` builds it; `FromApp` still names which accounts are live. The
  guarantee G1 wanted holds: no request carries an opt-in.
- **Migration 19, not 18** (18 became the account-icon migration while G1 waited): `api_calls.reason`.
- **Features ship dark** (brief section 4). The table has `defaultOn`; only `search` and `embed` are on.
  A feature's per-account switch is the setting `llm.feature.<name>` (`on`/`off`); every later stage
  turns its own on. Order of checks: shape, feature known, provider present, size, feature switch,
  opt-in, vetting, ledger breaker, caps.
- **Caps.** `llm.cap_usd` (per account) and `llm.global_cap_usd` (global scope) in settings; defaults
  `$5` (the operator's `llm.monthly_cap_usd`) and `$10` (`DefaultGlobalCapUSD`, agent-chosen, revisit). A
  setting that does not parse falls back to the default, never to "no cap". **A cap of zero or less now
  allows no hosted spend** (it used to mean unlimited, which broke brief invariant 11). Spend is summed
  across endpoints (`AccountSpend`, `GlobalSpend`), and the check is recorded spend plus in-flight
  reservations plus the call's worst case, under one mutex.
- **Breaker probe.** `store.ProbeLedger` runs the real ledger inserts in a transaction and rolls back, so
  a broken ledger is detected through the same constraints a real write meets, and a probe leaves no row.
- **Vetting for a multi-account call (Ask).** A call names accounts and keys but not which belongs to
  whom, so a key is accepted if any selected account vouches for it; an error, or a vetted-mail feature
  that names no mail, is withheld. 5i (gate G5) should replace this with account-tagged references.
- **`Embed` keeps a singular `AccountID`**; `Decide`, `Complete` and `See` take `AccountIDs`. A request
  naming no account, or several for a feature that is not `multi`, is `ErrBadRequest`: returned, not
  ledgered, because it is a caller bug and not a policy outcome.
- **Test shape.** Other packages can no longer inject a stub provider (that is the point), so the search
  tests that used `stubEmbedder` and `countingRejecter` now point the gate at small `httptest` servers
  speaking the real wire format. Inside `llm`, tests set the unexported client fields directly.
  Test 9 ("existing tests pass untouched") therefore became "pass with the same assertions, ported to the
  new construction"; no assertion was weakened.
- **Test-first caveat.** The gate was written before its tests. Each behaviour test was then shown to
  fail by mutation instead (opt-in check removed, reservation removed, release made a no-op, breaker
  disabled, vetting disabled, Ask checking only the first account): every one turned its test red.

## The questions as asked

1. **Providers owned by the gate** removes `NewOpenRouter`/`NewOllama`/`Embedder` from the public API
   and touches `cmd/embed.go` and `search`. Agreed, or keep the constructors and rely on review?
2. **In-memory reservations** assume one process. Ivy is one binary today; is a second writer
   (a separate CLI that calls the gate) ever planned? If so the reservation has to move into the
   database.
3. **Ask cost attribution:** equal shares across the selected accounts, or all against a synthetic
   `ask` account with its own cap? Equal shares keeps per-account caps honest but means one Ask can
   hit an account's cap through a question mostly about another.
4. **Where the global cap default lives:** a new `llm.global_cap_usd` in `ivy.yaml`, or settings only
   with a built-in default?
5. **Breaker policy:** refuse all calls on a ledger write failure (proposed), or only refuse once the
   unrecorded spend in memory would exceed the cap?
6. **Fail-closed default for vetting** means every vetted-mail feature is off until 5d lands. That is
   intended, but it means 5c's stage 2 cannot be demoed before 5d's tripwire exists. Acceptable, or
   should 5c build the tripwire first?

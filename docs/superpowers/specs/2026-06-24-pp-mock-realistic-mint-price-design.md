# pp-mock realistic mint pricing — design

**Date:** 2026-06-24
**Status:** Approved (brainstorm) — pending implementation plan
**Area:** `pp-mock` (partner-plugin mock), search → validate → mint price flow

## Problem

During **manual** (non-e2e) testing against Base Sepolia, the
search → validate → book(mint) → buy workflow fails on the **distributor** bot
with an "expected price does not match" error.

Root cause: pp-mock's mint handler **ignores** the validated price and returns a
hardcoded constant.

- **Search** (`pp-mock/handlers/accommodation/v5/accommodation_search.go:84`) is
  deterministic, not random: `unitPriceValue = DefaultPricePerNight (10533) × nights`,
  emitted with `decimals: 0` in the requested currency. Stored per `searchId`.
- **Validation** (`pp-mock/handlers/book/v5/validation.go:44`) faithfully passes that
  stored price back as `TotalPrice` and persists it as `VerifiedPrice`.
- **Mint** (`pp-mock/handlers/book/v5/mint.go:57`) returns
  `common.BookingTokenPriceV5 = {value:"1", native token}` → `1e18` wei = **1 native
  coin**, regardless of search/validation. It even attaches an alert saying the mint
  price "does not reflect the verified total price … it's just a minimum value."

The distributor rejects at `internal/messaging/mint_v5.go:90`:

```go
if !proto.Equal(request.ExpectedPrice, successResp.Price) { ... reject ... }
```

The tester sets `ExpectedPrice` from the search/validation price (e.g. `10533 EUR`),
but the mint response price is the fixed `1 native coin` → never `proto.Equal` → reject.

The fixed price exists on purpose: it keeps the on-chain buy in a known, affordable
currency and magnitude, which is fine for e2e but wrong for realistic online testing.

## Goal

When testing online, the full workflow should be **price-consistent** (search =
validate = mint) so the distributor's `ExpectedPrice` check and the on-chain buy both
pass, while:

- **Fiat** searches (USD/EUR) mint with **off-chain** payment (`OFFCHAIN_PAYMENT =
  address(1)`), at a realistic human price (~`$105.33`/night). On-chain magnitude is
  irrelevant because nothing transfers.
- **Native** (ETH/CAM) and **ERC20** searches produce **deliberately tiny base-unit**
  amounts (e.g. `10533 wei`) so buys cost almost nothing and are easy to verify on a
  block explorer.

The e2e suite must remain untouched.

## Key facts that constrain the design

1. **Currency → payment-token mapping already works** in the bot's price handler
   (`internal/price/price.go` / `pkg/booking/booking.go`):
   - Native → `0x00` (`NativePaymentToken`)
   - ISO/fiat → `0x01` (`ISOPaymentToken`) == the contract's `OFFCHAIN_PAYMENT`
   - ERC20 → the token's address
   No change needed here.

2. **`ToBigInt` semantics** (`pkg/price/price.go:30`):
   `on_chain_amount = value × 10^(totalDecimals − decimals)`, where `totalDecimals` is
   the token's real decimals supplied by the **bot** (native → 18, ISO → 6, ERC20 →
   queried from chain). Therefore the emitted `Price.Decimals` is the **scale of the
   value string**, not a parse hint:
   - To get a raw base-unit amount (e.g. `10533 wei`), emit `Price.Decimals = the
     token's total decimals`, so the multiplier collapses to 1 and the integer passes
     through verbatim.
   - `decimals = 0` means the value is in **whole tokens** (`10533 → 10533 ETH`) — the
     current bug.

3. **The price magnitude must be decided at search time and carried unchanged through
   validate → mint.** Because the distributor derives `ExpectedPrice` from what it saw
   in search/validation, mint must not re-scale the price; it must pass through the
   stored `VerifiedPrice`. (`pp-mock/handlers/state` already plumbs this: search stores
   `[]*UnifiedPrice`, validation stores `VerifiedPrice`, and `UnifiedPrice` records
   currency type via `PriceV5ToUnifiedPrice`.)

4. **e2e depends on both the fixed mint price and the default search price:**
   - Mint tests pass `common.BookingTokenPriceV5/V4/V3` as `ExpectedPrice` and assert
     the response equals it (`tests/e2e/tests/book.go`, `test_mint_v5.go`, …).
   - Accommodation tests assert search price is exactly
     `Value: DefaultPricePerNight × nights, Decimals: 0`
     (`tests/e2e/tests/test_accommodation_v5.go:349`, v4 likewise).
   So the **default (flag-off) path must be byte-identical to today**, and realistic
   pricing must be a separate, opt-in branch.

## Design

### Trigger: opt-in env flag (default off)

New env var read in `pp-mock/server/server.go`, matching the existing env-flag pattern
(`CMB_PARTNER_PLUGIN_MOCK_EVENTS`, `..._TEST_MODE`, `..._PORT`):

```
CMB_PARTNER_PLUGIN_MOCK_REALISTIC_PRICE=true   # default false
```

- **false (default):** current behavior exactly — search emits `DefaultPricePerNight ×
  nights @ decimals 0`; mint returns the fixed `BookingTokenPrice{V3,V4,V5}` constant.
  e2e unaffected.
- **true:** currency-aware search prices + mint pass-through (below).

### Per-token decimals map

pp-mock has no chain RPC access, so it cannot query an ERC20's decimals. A new env var
carries an address → decimals map:

```
CMB_PARTNER_PLUGIN_MOCK_TOKEN_DECIMALS="0xabc...:6,0xdef...:18"
```

- Parsed once at startup, keyed by **normalized (lowercased) address**.
- Unknown address → default **18**.
- Native and ISO/fiat never consult it (their decimals are fixed: 18 and "off-chain").

### Magnitude (realistic mode) — normalization, not a per-night helper

**Planning finding:** the services do **not** share a per-night model. Accommodation
synthesizes `DefaultPricePerNight × nights` in the requested currency; **transport** and
**activity** read prices from currency-tagged **mock data**. Furthermore, the entire mock
data tree is **fiat-only** — every transport/activity price is `ISO_CURRENCY_EUR`/`USD`,
with **zero** native or ERC20 entries. Because both services keep only mock entries whose
currency `proto.Equal`s the request, a native/ERC20 search against transport/activity
returns **empty results**. So in practice **accommodation is the only service that ever
produces a native or ERC20 price.**

Therefore the magnitude change is a **per-currency normalization pass** applied to each
result's already-built `searchPrice`, not a per-night helper:

| Search currency | normalized `Price.Value` | `Price.Decimals`             | reads as            | on-chain payment | exercised by |
|-----------------|--------------------------|------------------------------|---------------------|------------------|--------------|
| Fiat (USD/EUR)  | unchanged (as computed)  | unchanged                    | e.g. `$105.33`      | off-chain `0x01` | all services |
| Native (ETH/CAM)| **fixed** `10533`        | `18`                         | `10533 wei`         | native `0x00`    | accommodation |
| ERC20           | **fixed** `10533`        | token decimals (map, def 18) | `10533` base units  | token address    | accommodation |

The native/ERC20 amount is a **fixed small constant** (configurable, default `10533` base
units) — predictable and explorer-friendly, independent of product or nights. Fiat is
left exactly as the handler computed it (the realistic human value), so transport/activity
fiat prices are untouched and only become *consistent* via the mint pass-through below.

### Code changes

1. **Config** (`pp-mock/config/config.go`): add realistic-mode fields/loaders — the flag
   bool, the fixed native/ERC20 base-unit constant (default `10533`), and the parsed
   address→decimals map (keyed by lower-cased address string; no go-ethereum dep needed).
   Populate at startup from the env vars. Keep the existing `SetDefaults`/`SetE2EDefaults`
   shape.

2. **Shared normalizer** (`pp-mock/handlers/state`, on `*UnifiedPrice`): a reusable
   method that rewrites a `UnifiedPrice` in place per the table — fiat unchanged, native →
   `{Price:"10533", Decimals:18}`, ERC20 → `{Price:"10533", Decimals:<map lookup>}`.
   Living on `UnifiedPrice` keeps it version-agnostic (search handlers already convert
   their per-version price into `UnifiedPrice` before storing).

3. **Search handlers** — branch on the realistic flag, in **all** price-producing search
   services: accommodation, transport, and activity, each at `v3/v4/v5`
   (`pp-mock/handlers/{accommodation,transport,activity}/{v3,v4,v5}/*_search.go`). Each
   already builds a `searchPrice`, converts it to `UnifiedPrice`, and calls
   `state.GetStore().AddSearchResult`.
   - flag off → existing inline price (unchanged).
   - flag on → run the new normalizer on the `UnifiedPrice`, then write the normalized
     value **back into the response price proto** so the search response, stored state,
     validation, and mint all agree. (For fiat this is a no-op; for accommodation
     native/ERC20 it substitutes the fixed tiny amount.)
   (`seat_map/v4` produces no price of its own — the transport search carries the price for
   that flow — so it needs no change.)

4. **Mint handlers** (`pp-mock/handlers/book/v3|v4|v5/mint.go`) — branch on the flag:
   - flag off → return `common.BookingTokenPrice{V3,V4,V5}` (unchanged).
   - flag on → return `storedValidateData.Data.VerifiedPrice.ToPrice{V3,V4,V5}()`
     (data already in hand; it is the value used in today's alert message). The
     "does not reflect verified price" alert is dropped in realistic mode since the mint
     price now *does* reflect it.

5. **Distributor mismatch error detail** (`internal/messaging`) — when the distributor
   rejects a mint because the price doesn't match, the log/error currently carries no
   values. `errUnexpectedMintResponsePrice` (`internal/messaging/mint.go:20`) is a static
   string used by `mint_v4.go:91` and `mint_v5.go:91` (v3 has no `ExpectedPrice` check,
   so it's unaffected). Improve both call sites to include the **expected vs actual**
   prices — value, decimals, and currency/payment-token — so the mismatch is diagnosable
   straight from the logs. Prefer a formatted error/log built at the call site (where
   both `request.ExpectedPrice` and `successResp.Price` are in scope) over the bare
   sentinel, e.g. `expected {value,decimals,currency} got {value,decimals,currency}`.

6. **Docs** — update `pp-mock/docs/README.md` (the "Mint" section) and the main
   `README.MD` ("Running partner plugin (pp-mock) example" section) to document the two
   new env vars and the realistic-vs-fixed mint-price behavior.

### Scope

- **In scope:** all price-producing services on the full search → validate → mint path —
  accommodation, transport, activity at `v3/v4/v5` search, plus book `v3/v4/v5` mint —
  the config, the shared normalizer, the distributor mismatch-error improvement, and the
  doc updates.
- **Mint pass-through benefits all services** (it makes transport/activity fiat bookings
  consistent too). **Native/ERC20 normalization is exercised only by accommodation**,
  because transport/activity mock data is fiat-only — this is a data reality, not a
  shortcut. Adding native/ERC20 mock entries to transport/activity is explicitly out of
  scope (no manual-test payoff; accommodation already covers ERC20/native end-to-end).
- **No follow-on services remain.** `seat_map/v4` is excluded only because it emits no
  price of its own.

## Out of scope / non-goals

- No change to the bot's **pricing/payment logic** (`internal/price`, `pkg/booking`,
  `pkg/price`) — the currency→payment-token mapping and `ToBigInt` already do the right
  thing. The only bot-side change is the mismatch-error message detail in
  `internal/messaging` (item 5), which is purely diagnostic.
- No change to e2e tests or their fixed-price expectations.
- No on-chain RPC lookups from pp-mock.

## Risks / notes

- If a user sets the realistic flag but searches in an ERC20 whose decimals are **not**
  in the map and are **not** 18, the bot's `ToBigInt` may error loudly
  (`decimals > totalDecimals`) rather than silently overcharge — a safe failure that
  signals "add this token to the decimals map."
- In realistic mode the search **response** itself displays the tiny native/ERC20
  numbers (e.g. `10533` with 18 decimals). This is unavoidable: the displayed price must
  equal the on-chain value for `ExpectedPrice` to match. This is the desired behavior.

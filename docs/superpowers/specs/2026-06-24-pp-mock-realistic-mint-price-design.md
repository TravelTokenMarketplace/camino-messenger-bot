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

### Per-night magnitude (realistic mode)

A single per-night magnitude, reusing the existing constant value `10533`, with the
currency supplying the decimals:

| Search currency | `Price.Value`            | `Price.Decimals`             | reads as            | on-chain payment |
|-----------------|--------------------------|------------------------------|---------------------|------------------|
| Fiat (USD/EUR)  | `10533 × nights`         | `2`                          | `$105.33 / night`   | off-chain `0x01` |
| Native (ETH/CAM)| `10533 × nights`         | `18`                         | `10533 wei`         | native `0x00`    |
| ERC20           | `10533 × nights`         | token decimals (map, def 18) | `10533` base units  | token address    |

The fiat per-night value `105.33` reuses `DefaultPricePerNightStr = "10533"` /
`DefaultPricePerNightDecimals = 2` — no new magic number. Native/ERC20 are intentionally
tiny (not realistic) for cheap, explorer-verifiable testnet buys.

### Code changes

1. **Config** (`pp-mock/config/config.go`): add realistic-mode fields/loaders
   (the flag bool and the parsed `map[common.Address]uint32` decimals map), populated at
   startup from the two env vars. Keep the existing `SetDefaults`/`SetE2EDefaults` shape.

2. **Shared price helper** (`pp-mock/common`): a reusable function that, given the
   requested currency and `nights`, returns the realistic `Price` (value + decimals per
   the table). Centralizing this keeps search handlers thin and makes extension to other
   services trivial.

3. **Search handlers** — branch on the realistic flag:
   - flag off → existing inline `DefaultPricePerNight × nights @ decimals 0` (unchanged).
   - flag on → call the shared helper.
   The `UnifiedPrice` stored in state then already carries the correct value/decimals
   and currency type.

4. **Mint handlers** (`pp-mock/handlers/book/v3|v4|v5/mint.go`) — branch on the flag:
   - flag off → return `common.BookingTokenPrice{V3,V4,V5}` (unchanged).
   - flag on → return `storedValidateData.Data.VerifiedPrice.ToPrice{V3,V4,V5}()`
     (data already in hand; it is the value used in today's alert message). The
     "does not reflect verified price" alert is dropped in realistic mode since the mint
     price now *does* reflect it.

### Scope

- **In scope:** the full manual-test path the user exercises — accommodation
  `v3/v4/v5` search + book `v3/v4/v5` mint — plus the config and shared helper.
- **Out of scope (follow-on):** wiring the same shared helper into the other search
  services (activity, transport, seat_map). They are unaffected in default mode; in
  realistic mode they remain price-consistent (mint passes through whatever validation
  stored) but would still emit the old magnitude for native/ERC20 until updated. The
  shared helper makes this a mechanical follow-up.

## Out of scope / non-goals

- No change to the bot itself (`internal/`, `pkg/`) — the currency→payment-token
  mapping and `ToBigInt` already do the right thing.
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

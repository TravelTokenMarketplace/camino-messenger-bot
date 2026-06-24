# pp-mock Realistic Mint Pricing Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in pp-mock mode that makes the search → validate → mint price flow consistent so distributor `ExpectedPrice` checks pass during online manual testing, with fiat as off-chain payment and native/ERC20 as tiny base-unit amounts.

**Architecture:** A new env flag gates two behaviors in pp-mock: (1) mint handlers return the stored validated price instead of the fixed `1 native coin` constant, and (2) search handlers normalize each result's price by currency (fiat unchanged; native/ERC20 → a fixed tiny base-unit amount with correct decimals). The bot itself is unchanged except for a diagnostic improvement to the distributor's price-mismatch error.

**Tech Stack:** Go 1.25, gRPC/protobuf (camino-messenger-protocol buf SDK), pp-mock in-memory state store, go test with `-race`.

## Global Constraints

- Module path is versioned: `github.com/chain4travel/camino-messenger-bot/v13`; imports use the `/v13` suffix.
- Every `.go` file requires the Camino license header (copy the 2-line header from any existing file in the same package).
- Lint (`.golangci.yml`): use `require` (not `testify/assert`), `go.uber.org/mock` (not `golang/mock`), `io`/`os` (not `io/ioutil`).
- **Default behavior must be byte-identical to today** — the e2e suite asserts the fixed mint price (`common.BookingTokenPriceV{3,4,5}`) and the default search price (`DefaultPricePerNight × nights`, decimals 0). All new behavior is gated behind the realistic-price flag, default **off**.
- Env var names: `CMB_PARTNER_PLUGIN_MOCK_REALISTIC_PRICE` (bool, default false), `CMB_PARTNER_PLUGIN_MOCK_TOKEN_DECIMALS` (address→decimals map, default empty). Matches the existing `CMB_PARTNER_PLUGIN_MOCK_*` env pattern in `pp-mock/server/server.go`.
- Fixed native/ERC20 base-unit amount default: `10533`. ERC20 unknown-address decimals default: `18`.
- Build with `./scripts/build_partner_plugin_mock.sh`; run unit tests with `go test -race ./pp-mock/... ./internal/messaging/...`.

---

## File Structure

**New files:**
- `pp-mock/config/realistic_price.go` — realistic-mode config: flag, fixed base-unit amount, address→decimals map, and the env parser.
- `pp-mock/config/realistic_price_test.go` — tests for the env-map parser and lookups.
- `pp-mock/handlers/state/realistic_price.go` — `(*UnifiedPrice).NormalizeRealistic(cfg)` normalizer method.
- `pp-mock/handlers/state/realistic_price_test.go` — tests for the normalizer across fiat/native/ERC20.
- `internal/messaging/mint_price_error.go` — helper formatting the expected-vs-actual price mismatch message.
- `internal/messaging/mint_price_error_test.go` — test for the formatter.

**Modified files:**
- `pp-mock/server/server.go` — read the two new env vars at startup, store into config, log them.
- `pp-mock/config/config.go` — leave as-is (new config lives in `realistic_price.go`, same package).
- `pp-mock/handlers/book/v3/mint.go`, `.../v4/mint.go`, `.../v5/mint.go` — flag-gated pass-through of the validated price.
- `pp-mock/handlers/accommodation/{v3,v4,v5}/accommodation_search.go` — flag-gated normalize.
- `pp-mock/handlers/transport/{v3,v4,v5}/transport_search.go` — flag-gated normalize.
- `pp-mock/handlers/activity/{v3,v4,v5}/activity_search.go` — flag-gated normalize.
- `internal/messaging/mint_v4.go`, `internal/messaging/mint_v5.go` — use the new mismatch formatter.
- `pp-mock/docs/README.md`, `README.MD` — document env vars + behavior.

---

## Task 1: Realistic-price config (flag, amount, decimals map, env parser)

**Files:**
- Create: `pp-mock/config/realistic_price.go`
- Create: `pp-mock/config/realistic_price_test.go`

**Interfaces:**
- Consumes: nothing (leaf).
- Produces:
  - package-level vars in `package config`: `RealisticPriceEnabled bool`, `RealisticNativeBaseUnits string`, `RealisticTokenDecimals map[string]uint32`
  - `func SetRealisticPrice(enabled bool, baseUnits string, tokenDecimals map[string]uint32)`
  - `func ParseTokenDecimals(raw string) (map[string]uint32, error)` — parses `"0xAbc:6,0xDef:18"` into `{"0xabc":6,"0xdef":18}` (lower-cased keys)
  - `func TokenDecimalsFor(address string) uint32` — map lookup by lower-cased address, returns `18` if absent
  - `const RealisticNativeBaseUnitsDefault = "10533"`, `const RealisticTokenDecimalsDefault = uint32(18)`

- [ ] **Step 1: Write the failing test**

Create `pp-mock/config/realistic_price_test.go` (copy the license header from `pp-mock/config/config.go`):

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseTokenDecimals(t *testing.T) {
	m, err := ParseTokenDecimals("0xAbC123:6, 0xDEF456:18")
	require.NoError(t, err)
	require.Equal(t, map[string]uint32{"0xabc123": 6, "0xdef456": 18}, m)

	empty, err := ParseTokenDecimals("")
	require.NoError(t, err)
	require.Empty(t, empty)

	_, err = ParseTokenDecimals("0xabc:notanumber")
	require.Error(t, err)

	_, err = ParseTokenDecimals("missingcolon")
	require.Error(t, err)
}

func TestTokenDecimalsFor(t *testing.T) {
	SetRealisticPrice(true, RealisticNativeBaseUnitsDefault, map[string]uint32{"0xabc": 6})
	require.Equal(t, uint32(6), TokenDecimalsFor("0xABC"))
	require.Equal(t, RealisticTokenDecimalsDefault, TokenDecimalsFor("0xnotpresent"))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./pp-mock/config/...`
Expected: FAIL — `undefined: ParseTokenDecimals` / `SetRealisticPrice` / `TokenDecimalsFor`.

- [ ] **Step 3: Write minimal implementation**

Create `pp-mock/config/realistic_price.go` (copy the license header from `config.go`):

```go
package config

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	RealisticNativeBaseUnitsDefault        = "10533"
	RealisticTokenDecimalsDefault   uint32 = 18
)

var (
	RealisticPriceEnabled    bool
	RealisticNativeBaseUnits = RealisticNativeBaseUnitsDefault
	RealisticTokenDecimals   = map[string]uint32{}
)

// SetRealisticPrice stores the realistic-pricing settings for the process.
func SetRealisticPrice(enabled bool, baseUnits string, tokenDecimals map[string]uint32) {
	RealisticPriceEnabled = enabled
	if baseUnits != "" {
		RealisticNativeBaseUnits = baseUnits
	}
	if tokenDecimals == nil {
		tokenDecimals = map[string]uint32{}
	}
	RealisticTokenDecimals = tokenDecimals
}

// ParseTokenDecimals parses "0xAddr:dec,0xAddr:dec" into a lower-cased address->decimals map.
func ParseTokenDecimals(raw string) (map[string]uint32, error) {
	out := map[string]uint32{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out, nil
	}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		addr, decStr, ok := strings.Cut(pair, ":")
		if !ok {
			return nil, fmt.Errorf("invalid token decimals entry %q: expected <address>:<decimals>", pair)
		}
		dec, err := strconv.ParseUint(strings.TrimSpace(decStr), 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid decimals in entry %q: %w", pair, err)
		}
		out[strings.ToLower(strings.TrimSpace(addr))] = uint32(dec)
	}
	return out, nil
}

// TokenDecimalsFor returns the configured decimals for an ERC20 address, or the default 18.
func TokenDecimalsFor(address string) uint32 {
	if dec, ok := RealisticTokenDecimals[strings.ToLower(strings.TrimSpace(address))]; ok {
		return dec
	}
	return RealisticTokenDecimalsDefault
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./pp-mock/config/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pp-mock/config/realistic_price.go pp-mock/config/realistic_price_test.go
git commit -m "feat(pp-mock): add realistic-price config (flag, base units, token decimals map)"
```

---

## Task 2: UnifiedPrice realistic normalizer

**Files:**
- Create: `pp-mock/handlers/state/realistic_price.go`
- Create: `pp-mock/handlers/state/realistic_price_test.go`

**Interfaces:**
- Consumes: `config.RealisticNativeBaseUnits`, `config.TokenDecimalsFor` (Task 1); the existing `state.UnifiedPrice` struct (`Price string`, `Decimals uint32`, `IsNative bool`, `IsoCurrencyEnum int32`, `TokenContractAddress string`).
- Produces: `func (p *UnifiedPrice) NormalizeRealistic()` — mutates the receiver in place. Fiat (IsoCurrencyEnum != 0) → unchanged. Native (`IsNative`) → `Price = config.RealisticNativeBaseUnits`, `Decimals = 18`. ERC20 (`TokenContractAddress != ""`) → `Price = config.RealisticNativeBaseUnits`, `Decimals = config.TokenDecimalsFor(addr)`.

- [ ] **Step 1: Write the failing test**

Create `pp-mock/handlers/state/realistic_price_test.go` (license header from `state.go`):

```go
package state

import (
	"testing"

	"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/config"
	"github.com/stretchr/testify/require"
)

func TestNormalizeRealistic(t *testing.T) {
	config.SetRealisticPrice(true, "10533", map[string]uint32{"0xusdc": 6})

	// Fiat is left untouched.
	fiat := &UnifiedPrice{Price: "10533", Decimals: 2, IsoCurrencyEnum: 1}
	fiat.NormalizeRealistic()
	require.Equal(t, &UnifiedPrice{Price: "10533", Decimals: 2, IsoCurrencyEnum: 1}, fiat)

	// Native becomes a tiny wei amount at 18 decimals.
	native := &UnifiedPrice{Price: "999999", Decimals: 0, IsNative: true}
	native.NormalizeRealistic()
	require.Equal(t, &UnifiedPrice{Price: "10533", Decimals: 18, IsNative: true}, native)

	// Known ERC20 uses its configured decimals.
	usdc := &UnifiedPrice{Price: "999999", Decimals: 0, TokenContractAddress: "0xUSDC"}
	usdc.NormalizeRealistic()
	require.Equal(t, &UnifiedPrice{Price: "10533", Decimals: 6, TokenContractAddress: "0xUSDC"}, usdc)

	// Unknown ERC20 defaults to 18 decimals.
	other := &UnifiedPrice{Price: "999999", Decimals: 0, TokenContractAddress: "0xOTHER"}
	other.NormalizeRealistic()
	require.Equal(t, &UnifiedPrice{Price: "10533", Decimals: 18, TokenContractAddress: "0xOTHER"}, other)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race ./pp-mock/handlers/state/...`
Expected: FAIL — `native.NormalizeRealistic undefined`.

- [ ] **Step 3: Write minimal implementation**

Create `pp-mock/handlers/state/realistic_price.go` (license header from `state.go`):

```go
package state

import "github.com/chain4travel/camino-messenger-bot/v13/pp-mock/config"

// NormalizeRealistic rewrites the price for realistic-pricing mode.
//
// Fiat/ISO prices are left as-is (off-chain payment, realistic human value).
// Native and ERC20 prices are replaced with a fixed tiny base-unit amount so
// on-chain buys are cheap and easy to verify on a block explorer. The decimals
// are set so the value passes through the bot's ToBigInt verbatim (multiplier 1):
// native uses 18, ERC20 uses the configured per-token decimals (default 18).
func (p *UnifiedPrice) NormalizeRealistic() {
	switch {
	case p.IsNative:
		p.Price = config.RealisticNativeBaseUnits
		p.Decimals = 18
	case p.TokenContractAddress != "":
		p.Price = config.RealisticNativeBaseUnits
		p.Decimals = config.TokenDecimalsFor(p.TokenContractAddress)
	default:
		// Fiat/ISO: leave unchanged.
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./pp-mock/handlers/state/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pp-mock/handlers/state/realistic_price.go pp-mock/handlers/state/realistic_price_test.go
git commit -m "feat(pp-mock): add UnifiedPrice realistic-price normalizer"
```

---

## Task 3: Wire env vars in pp-mock server startup

**Files:**
- Modify: `pp-mock/server/server.go`

**Interfaces:**
- Consumes: `config.SetRealisticPrice`, `config.ParseTokenDecimals` (Task 1).
- Produces: at process start, `config.RealisticPriceEnabled` / `RealisticTokenDecimals` reflect the env; startup log shows them.

- [ ] **Step 1: Add the env key constants**

In `pp-mock/server/server.go`, extend the `const` block (currently ending with `DefaultPort = 50051`):

```go
const (
	EnvKeyEventsEnabled    = "CMB_PARTNER_PLUGIN_MOCK_EVENTS"
	EnvKeyPort             = "CMB_PARTNER_PLUGIN_MOCK_PORT"
	EnvE2ETestMode         = "CMB_PARTNER_PLUGIN_MOCK_TEST_MODE"
	EnvKeyRealisticPrice   = "CMB_PARTNER_PLUGIN_MOCK_REALISTIC_PRICE"
	EnvKeyTokenDecimals    = "CMB_PARTNER_PLUGIN_MOCK_TOKEN_DECIMALS"
	DefaultPort            = 50051
)
```

- [ ] **Step 2: Parse and store the new env vars**

In `Run()`, immediately after the existing `config.SetDefaults()` call, add:

```go
	realisticPrice := os.Getenv(EnvKeyRealisticPrice) == "true"
	tokenDecimals, err := config.ParseTokenDecimals(os.Getenv(EnvKeyTokenDecimals))
	if err != nil {
		log.Printf("failed to parse %s: %v", EnvKeyTokenDecimals, err)
		return err
	}
	config.SetRealisticPrice(realisticPrice, "", tokenDecimals)
```

Note: `err` is already declared later via `var err error`; change that later line from `var err error` to a bare reuse, OR move the `var err error` declaration up. Simplest: delete the later `var err error` line (around the port parsing) since `err` is now already in scope from this block.

- [ ] **Step 3: Add startup logging**

In the `log.Printf` startup block (after the `e2e test mode` line), add:

```go
	log.Printf("  realistic price: %t (%s)", realisticPrice, EnvKeyRealisticPrice)
	log.Printf("  token decimals:  %d entries (%s)", len(tokenDecimals), EnvKeyTokenDecimals)
```

- [ ] **Step 4: Build to verify it compiles**

Run: `./scripts/build_partner_plugin_mock.sh`
Expected: build succeeds, produces `./build/pp-mock`. (If `err` redeclaration errors appear, apply the Step 2 note.)

- [ ] **Step 5: Smoke-test the flag is read**

Run: `CMB_PARTNER_PLUGIN_MOCK_REALISTIC_PRICE=true CMB_PARTNER_PLUGIN_MOCK_TOKEN_DECIMALS="0xabc:6" CMB_PARTNER_PLUGIN_MOCK_PORT=50061 ./build/pp-mock`
Expected: startup log shows `realistic price: true` and `token decimals:  1 entries`. Ctrl-C to stop.

- [ ] **Step 6: Commit**

```bash
git add pp-mock/server/server.go
git commit -m "feat(pp-mock): read realistic-price env vars at startup"
```

---

## Task 4: Mint pass-through (book v3/v4/v5)

**Files:**
- Modify: `pp-mock/handlers/book/v5/mint.go`
- Modify: `pp-mock/handlers/book/v4/mint.go`
- Modify: `pp-mock/handlers/book/v3/mint.go`

**Interfaces:**
- Consumes: `config.RealisticPriceEnabled` (Task 1); existing `storedValidateData.Data.VerifiedPrice` (`*state.UnifiedPrice`) and its `ToPriceV3/V4/V5()` methods.
- Produces: when the flag is on, mint responses carry the validated price instead of `common.BookingTokenPrice{V3,V4,V5}`, and the "does not reflect verified price" alert/info is suppressed.

- [ ] **Step 1: V5 — pass through the validated price**

In `pp-mock/handlers/book/v5/mint.go`, replace the success-response construction + alert (lines ~50-65) so the price and alert are flag-aware. Add `"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/config"` to imports (already imported). Change:

```go
	mintPrice := common.BookingTokenPriceV5
	if config.RealisticPriceEnabled {
		mintPrice = storedValidateData.Data.VerifiedPrice.ToPriceV5()
	}

	response := &bookv5.MintResponse{
		Response: &bookv5.MintResponse_SuccessResponse{
			SuccessResponse: &bookv5.MintSuccessResponse{
				Header:          common.SuccessHeaderV4(),
				MintId:          &typesv4.UUID{Value: uuid.New().String()},
				BuyableUntil:    timestamppb.New(time.Now().Add(config.BuyableUntilDefault)),
				ValidationId:    req.ValidationId,
				Price:           mintPrice,
				Cancellable:     true,
				BookingTokenUri: "https://example.com/",
			},
		},
	}

	if !config.RealisticPriceEnabled {
		mintResponseInfoMessage := "Please note that the price given in this mint response does not reflect the verified total price of the product of '" + storedValidateData.Data.VerifiedPrice.Price + "'. The price is just a minimum value to be able to mint the product."
		common.AddHeaderAlertV4(response.GetSuccessResponse().Header, typesv4.AlertCode_ALERT_CODE_INFORMATIONAL, mintResponseInfoMessage)
	}
```

- [ ] **Step 2: V4 — same change**

In `pp-mock/handlers/book/v4/mint.go`, set the price via a `mintPrice` local (`common.BookingTokenPriceV4` default, `storedValidateData.Data.VerifiedPrice.ToPriceV4()` when realistic) and guard the existing `AddHeaderAlertV4` info block (lines ~56-57) with `if !config.RealisticPriceEnabled { ... }`. `config` is already imported.

- [ ] **Step 3: V3 — same change**

In `pp-mock/handlers/book/v3/mint.go`, the success header is built with `common.SuccessHeaderWithInfoV1(mintResponseInfoMessage)`. Make it flag-aware:

```go
	header := common.SuccessHeaderV1()
	mintPrice := common.BookingTokenPriceV3
	if config.RealisticPriceEnabled {
		mintPrice = storedValidateData.Data.VerifiedPrice.ToPriceV3()
	} else {
		mintResponseInfoMessage := "Please note that the price given in this mint response does not reflect the verified total price of the product of '" + storedValidateData.Data.VerifiedPrice.Price + "'. The price is just a minimum value to be able to mint the product."
		header = common.SuccessHeaderWithInfoV1(mintResponseInfoMessage)
	}

	response := bookv3.MintResponse{
		Header:       header,
		MintId:       &typesv1.UUID{Value: uuid.New().String()},
		BuyableUntil: &timestamppb.Timestamp{Seconds: time.Now().Add(config.BuyableUntilDefault).Unix()},
		ValidationId: req.ValidationId,
		Price:        mintPrice,
		Cancellable:  true,
	}
```

`common.SuccessHeaderV1()` exists (`pp-mock/common/common.go:185`) — use it directly.

- [ ] **Step 4: Build to verify it compiles**

Run: `./scripts/build_partner_plugin_mock.sh`
Expected: build succeeds.

- [ ] **Step 5: Verify default-mode unit tests still pass**

Run: `go test -race ./pp-mock/...`
Expected: PASS (default flag off → unchanged behavior).

- [ ] **Step 6: Commit**

```bash
git add pp-mock/handlers/book/v3/mint.go pp-mock/handlers/book/v4/mint.go pp-mock/handlers/book/v5/mint.go
git commit -m "feat(pp-mock): pass validated price through mint in realistic mode"
```

---

## Task 5: Search normalization — accommodation v3/v4/v5

**Files:**
- Modify: `pp-mock/handlers/accommodation/v5/accommodation_search.go`
- Modify: `pp-mock/handlers/accommodation/v4/accommodation_search.go`
- Modify: `pp-mock/handlers/accommodation/v3/accommodation_search.go`

**Interfaces:**
- Consumes: `config.RealisticPriceEnabled` (Task 1); `(*UnifiedPrice).NormalizeRealistic()` (Task 2); `(*UnifiedPrice).ToPriceV3/V4/V5()`.
- Produces: when the flag is on, the search response price, stored `UnifiedPrice`, and (downstream) validation/mint all carry the normalized price.

- [ ] **Step 1: V5 — normalize after building the UnifiedPrice**

In `pp-mock/handlers/accommodation/v5/accommodation_search.go`, find where it builds `validationPrice := state.PriceV5ToUnifiedPrice(unit.PriceDetail.Price)` (~line 199). Replace the surrounding two lines with:

```go
		validationPrice := state.PriceV5ToUnifiedPrice(unit.PriceDetail.Price)
		if config.RealisticPriceEnabled {
			validationPrice.NormalizeRealistic()
			normalized := validationPrice.ToPriceV5()
			unit.PriceDetail.Price = normalized
			searchResults[len(searchResults)-1].TotalPrice.Value = normalized
		}
		validationPrices = append(validationPrices, validationPrice)
```

Add `"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/config"` to the import block.

Note: the search result is appended just above this point, so `searchResults[len(searchResults)-1]` is the result whose `TotalPrice.Value` must match the unit price (both were set to `unit.PriceDetail.Price`). Confirm by reading the few lines above (`searchResults = append(... TotalPrice: ... Value: unit.PriceDetail.Price ...)`); if the append happens *after* this block in the file, instead assign `unit.PriceDetail.Price = normalized` **before** the append and drop the index expression.

- [ ] **Step 2: V4 — same pattern**

In `pp-mock/handlers/accommodation/v4/accommodation_search.go`, locate the `state.PriceV4ToUnifiedPrice(...)` call and the result's `TotalPrice`/`PriceDetail.Price` assignment. Apply the same flag-gated block: normalize the `UnifiedPrice`, convert back via `ToPriceV4()`, and write the normalized proto into both the unit `PriceDetail.Price` and the result `TotalPrice.Value`. Add the `config` import.

- [ ] **Step 3: V3 — same pattern**

In `pp-mock/handlers/accommodation/v3/accommodation_search.go`, the price is built at lines ~143-145 (`searchPrice` with `Value: fmt.Sprintf("%d", common.DefaultPricePerNight*duration)`) and converted at `state.PriceV3ToUnifiedPrice(searchPrice)` (~line 156). Apply the flag-gated block: normalize the unified price, convert back with `ToPriceV3()`, and write it into the result price field(s). Read the surrounding code to find the exact result struct field that holds the price and assign the normalized proto there. Add the `config` import.

- [ ] **Step 4: Build to verify it compiles**

Run: `./scripts/build_partner_plugin_mock.sh`
Expected: build succeeds.

- [ ] **Step 5: Verify default-mode behavior unchanged**

Run: `go test -race ./pp-mock/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pp-mock/handlers/accommodation/
git commit -m "feat(pp-mock): normalize accommodation search prices in realistic mode"
```

---

## Task 6: Search normalization — transport & activity v3/v4/v5

**Files:**
- Modify: `pp-mock/handlers/transport/v5/transport_search.go`
- Modify: `pp-mock/handlers/transport/v4/transport_search.go`
- Modify: `pp-mock/handlers/transport/v3/transport_search.go`
- Modify: `pp-mock/handlers/activity/v5/activity_search.go`
- Modify: `pp-mock/handlers/activity/v4/activity_search.go`
- Modify: `pp-mock/handlers/activity/v3/activity_search.go`

**Interfaces:**
- Consumes: `config.RealisticPriceEnabled` (Task 1); `(*UnifiedPrice).NormalizeRealistic()` (Task 2); the per-version `ToPriceV*` converters.
- Produces: realistic-mode normalization wired uniformly. (For these services the mock data is fiat-only, so the normalizer is a no-op today; this keeps them consistent and future-proof.)

- [ ] **Step 1: Transport v5**

In `pp-mock/handlers/transport/v5/transport_search.go`, the price is built as `searchPrice` (~line 108) and converted at `validationPrice := state.PriceV5ToUnifiedPrice(searchPrice)` (~line 128). Insert the flag-gated normalize block right after the conversion, writing the normalized proto back into the result's `TotalPrice.Value`:

```go
		validationPrice := state.PriceV5ToUnifiedPrice(searchPrice)
		if config.RealisticPriceEnabled {
			validationPrice.NormalizeRealistic()
			normalized := validationPrice.ToPriceV5()
			searchResults[len(searchResults)-1].TotalPrice.Value = normalized
		}
		validationPrices = append(validationPrices, validationPrice)
```

Confirm the result was appended just above (it is — `searchResults = append(...)` then `resultID++` then the conversion). Add the `config` import.

- [ ] **Step 2: Transport v4 and v3**

Apply the identical pattern in `transport/v4/transport_search.go` (`PriceV4ToUnifiedPrice` / `ToPriceV4`) and `transport/v3/transport_search.go` (`PriceV3ToUnifiedPrice` / `ToPriceV3`). In each, read the few lines around the `state.PriceV*ToUnifiedPrice(searchPrice)` call to confirm the result-struct field holding the price, and write the normalized proto there. Add the `config` import to each.

- [ ] **Step 3: Activity v3/v4/v5**

In activity, the price comes straight from mock data: `state.PriceV5ToUnifiedPrice(activity.TotalPrice.Value)` (v5 ~line 65), `activity.TotalPrice.Value` (v4 ~line 45), `activity.Price` (v3 ~line 64). For each, after building the `UnifiedPrice`, apply the flag-gated normalize and write the normalized proto back into the result's price field:

```go
		validationPrice := state.PriceV5ToUnifiedPrice(activity.TotalPrice.Value)
		if config.RealisticPriceEnabled {
			validationPrice.NormalizeRealistic()
			activity.TotalPrice.Value = validationPrice.ToPriceV5()
		}
		validationPrices = append(validationPrices, validationPrice)
```

`activity` here is the per-result element being added to the response (confirm it is the response result, not a throwaway copy — read the loop; activity handlers clone mock data into the response, so mutating `activity.TotalPrice.Value`/`activity.Price` is correct). Use `ToPriceV4()`/`activity.TotalPrice.Value` for v4 and `ToPriceV3()`/`activity.Price` for v3. Add the `config` import to each.

- [ ] **Step 4: Build to verify it compiles**

Run: `./scripts/build_partner_plugin_mock.sh`
Expected: build succeeds.

- [ ] **Step 5: Verify default-mode behavior unchanged**

Run: `go test -race ./pp-mock/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pp-mock/handlers/transport/ pp-mock/handlers/activity/
git commit -m "feat(pp-mock): wire realistic-price normalization into transport & activity search"
```

---

## Task 7: Distributor price-mismatch error detail

**Files:**
- Create: `internal/messaging/mint_price_error.go`
- Create: `internal/messaging/mint_price_error_test.go`
- Modify: `internal/messaging/mint_v5.go`
- Modify: `internal/messaging/mint_v4.go`

**Interfaces:**
- Consumes: the protobuf `*typesv4.Price` / `*typesv5.Price` types already used in mint_v4/v5.
- Produces: `func formatMintPriceMismatchV5(expected, actual *typesv5.Price) string` and `func formatMintPriceMismatchV4(expected, actual *typesv4.Price) string`, returning a human-readable "expected … got …" string including value, decimals, and currency.

- [ ] **Step 1: Write the failing test**

Create `internal/messaging/mint_price_error_test.go` (license header from `mint.go`):

```go
package messaging

import (
	"testing"

	typesv4 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/types/v4"
	typesv5 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/types/v5"
	"github.com/stretchr/testify/require"
)

func TestFormatMintPriceMismatchV5(t *testing.T) {
	expected := &typesv5.Price{Value: "10533", Decimals: 18, Currency: &typesv4.Currency{Currency: &typesv4.Currency_NativeToken{}}}
	actual := &typesv5.Price{Value: "1", Decimals: 0, Currency: &typesv4.Currency{Currency: &typesv4.Currency_NativeToken{}}}
	msg := formatMintPriceMismatchV5(expected, actual)
	require.Contains(t, msg, "10533")
	require.Contains(t, msg, "decimals=18")
	require.Contains(t, msg, "decimals=0")
	require.Contains(t, msg, "expected")
}

func TestFormatMintPriceMismatchV5NilActual(t *testing.T) {
	expected := &typesv5.Price{Value: "10533", Decimals: 18}
	msg := formatMintPriceMismatchV5(expected, nil)
	require.Contains(t, msg, "<nil>")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -race -run TestFormatMintPriceMismatch ./internal/messaging/`
Expected: FAIL — `undefined: formatMintPriceMismatchV5`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/messaging/mint_price_error.go` (license header from `mint.go`):

```go
package messaging

import (
	"fmt"

	typesv4 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/types/v4"
	typesv5 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/types/v5"
)

func formatMintPriceMismatchV5(expected, actual *typesv5.Price) string {
	return fmt.Sprintf("%s: expected %s, got %s",
		errUnexpectedMintResponsePrice.Error(), priceStringV5(expected), priceStringV5(actual))
}

func formatMintPriceMismatchV4(expected, actual *typesv4.Price) string {
	return fmt.Sprintf("%s: expected %s, got %s",
		errUnexpectedMintResponsePrice.Error(), priceStringV4(expected), priceStringV4(actual))
}

func priceStringV5(p *typesv5.Price) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("{value=%s decimals=%d currency=%s}", p.Value, p.Decimals, p.GetCurrency().String())
}

func priceStringV4(p *typesv4.Price) string {
	if p == nil {
		return "<nil>"
	}
	return fmt.Sprintf("{value=%s decimals=%d currency=%s}", p.Value, p.Decimals, p.GetCurrency().String())
}
```

Note: `GetValue()`, `GetDecimals()`, and `GetCurrency()` are confirmed present on both `typesv4.Price` and `typesv5.Price` (generated `price.pb.go`), so the helpers above compile as written.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race -run TestFormatMintPriceMismatch ./internal/messaging/`
Expected: PASS.

- [ ] **Step 5: Use the formatter at the call sites**

In `internal/messaging/mint_v5.go` (lines ~90-93), replace:

```go
	if !proto.Equal(request.ExpectedPrice, successResp.Price) {
		h.logger.Debug(errUnexpectedMintResponsePrice)
		return mintErrResponseV5(typesv4.ErrorCode_ERROR_CODE_BUSINESS_PROCESS_ERROR, errUnexpectedMintResponsePrice.Error())
	}
```

with:

```go
	if !proto.Equal(request.ExpectedPrice, successResp.Price) {
		msg := formatMintPriceMismatchV5(request.ExpectedPrice, successResp.Price)
		h.logger.Debug(msg)
		return mintErrResponseV5(typesv4.ErrorCode_ERROR_CODE_BUSINESS_PROCESS_ERROR, msg)
	}
```

Apply the analogous change in `internal/messaging/mint_v4.go` (lines ~90-93) using `formatMintPriceMismatchV4(request.ExpectedPrice, successResp.Price)` and `mintErrResponseV4`.

- [ ] **Step 6: Build and test the package**

Run: `go test -race ./internal/messaging/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/messaging/mint_price_error.go internal/messaging/mint_price_error_test.go internal/messaging/mint_v4.go internal/messaging/mint_v5.go
git commit -m "feat(messaging): include expected vs actual price in mint mismatch error"
```

---

## Task 8: Documentation

**Files:**
- Modify: `pp-mock/docs/README.md`
- Modify: `README.MD`

**Interfaces:** none (docs only).

- [ ] **Step 1: Update pp-mock docs — Mint section**

In `pp-mock/docs/README.md`, replace the `# Mint` section (lines 63-66) with:

```markdown
# Mint

Used for minting a buyable token.

By default the mint response returns a fixed minimum price of `1` native coin, decoupled
from the validated price (this is what the e2e suite expects).

Set `CMB_PARTNER_PLUGIN_MOCK_REALISTIC_PRICE=true` to make mint return the **validated
price** instead, so the full search → validate → mint flow is price-consistent (useful for
online manual testing). In this mode:

- **Fiat (USD/EUR)** searches mint with off-chain payment at the realistic human value.
- **Native** and **ERC20** searches mint a deliberately tiny base-unit amount
  (default `10533`) so on-chain buys are cheap and easy to verify on a block explorer.

For ERC20 currencies, pp-mock cannot query token decimals on-chain, so provide them via
`CMB_PARTNER_PLUGIN_MOCK_TOKEN_DECIMALS="0xToken:6,0xOther:18"` (default 18 if absent).
Note: only accommodation search supports native/ERC20 currencies; transport and activity
mock data is fiat-only.
```

- [ ] **Step 2: Update main README — pp-mock run section**

In `README.MD`, in the "Running partner plugin (pp-mock) example" section, after the existing code block (line 314), add:

```markdown
Optional environment variables:

- `CMB_PARTNER_PLUGIN_MOCK_REALISTIC_PRICE=true` — make mint return the validated price
  (price-consistent search → validate → mint), instead of the default fixed `1` native
  coin. Fiat stays off-chain at a realistic value; native/ERC20 use a tiny base-unit
  amount for cheap testnet buys.
- `CMB_PARTNER_PLUGIN_MOCK_TOKEN_DECIMALS="0xToken:6,..."` — per-ERC20 decimals used by
  realistic pricing (default 18). Only needed for non-18-decimal test tokens.
```

- [ ] **Step 3: Commit**

```bash
git add pp-mock/docs/README.md README.MD
git commit -m "docs: document pp-mock realistic-price env vars"
```

---

## Task 9: End-to-end manual verification (Base Sepolia loop)

**Files:** none (manual verification using the locally-built binaries and `build/` configs from `CLAUDE.local.md`).

- [ ] **Step 1: Run the full suite with race**

Run: `go test -shuffle=on -race -timeout=120s ./...`
Expected: PASS (default behavior unchanged; new helpers covered).

- [ ] **Step 2: Lint**

Run: `./scripts/lint.sh`
Expected: no findings (license headers present, no banned imports).

- [ ] **Step 3: Manual fiat flow (off-chain payment)**

Start the three local processes with realistic pricing on:
```bash
cd build
CMB_PARTNER_PLUGIN_MOCK_REALISTIC_PRICE=true ./pp-mock
./start_suplier_bot.sh
./start_dist_bot.sh
```
Run a USD/EUR accommodation search → validate → mint(book) from the distributor, using the search/validation price as `ExpectedPrice`.
Expected: no "expected price does not match" error; booking token minted with off-chain payment (`0x..01`); buy succeeds.

- [ ] **Step 4: Manual native flow (tiny on-chain amount)**

Repeat the search with native-token currency.
Expected: search/validation price reads as the tiny `10533`-base-unit amount; mint + buy succeed transferring `10533 wei`, visible on the Base Sepolia explorer.

- [ ] **Step 5: Negative check — mismatch error detail**

Send a mint with an intentionally wrong `ExpectedPrice`.
Expected: distributor logs the new detailed message showing expected vs actual `{value, decimals, currency}`.

- [ ] **Step 6: Final commit (if any verification fixups were needed)**

```bash
git add -A
git commit -m "test: verify pp-mock realistic pricing end-to-end"
```

---

## Self-Review Notes

- **Spec coverage:** Config + env (Tasks 1, 3) ↔ spec code-change 1; normalizer (Task 2) + search wiring (Tasks 5, 6) ↔ spec code-changes 2-3; mint pass-through (Task 4) ↔ spec code-change 4; mismatch error (Task 7) ↔ spec code-change 5; docs (Task 8) ↔ spec code-change 6. Manual verification (Task 9) covers the spec's risks/notes (ERC20 decimals, displayed tiny numbers).
- **Default-path safety:** Every behavioral change is guarded by `config.RealisticPriceEnabled`; default off keeps e2e assertions (`DefaultPricePerNight × nights, decimals 0`; `BookingTokenPriceV*`) intact.
- **Type consistency:** `NormalizeRealistic()` (Task 2) is referenced by that exact name in Tasks 5-6; `formatMintPriceMismatchV4/V5` (Task 7) match their call sites; `config.RealisticPriceEnabled`/`RealisticNativeBaseUnits`/`TokenDecimalsFor` are defined in Task 1 and used as named in Tasks 2-6.
- **Verified during planning:** `require` is the repo's testify package; `common.SuccessHeaderV1()` exists (`common.go:185`); `GetValue/GetDecimals/GetCurrency` exist on v4/v5 `Price`.
- **Remaining confirm-on-implementation point** (each search task flags reading surrounding code): the exact result-struct field that holds the price, and whether the result is appended before or after the normalize block (affects the `searchResults[len-1]` vs pre-append assignment).

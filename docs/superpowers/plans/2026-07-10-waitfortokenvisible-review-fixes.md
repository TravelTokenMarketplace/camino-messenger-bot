# waitForTokenVisible Review Fixes + Configurable Retry + Fail-Fast Mismatch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply the accepted code-review comments on commit `ee0a20b` in `pkg/booking/booking.go` (make the retry loop unit-testable, add tests, skip the wasted final sleep) **and** (a) make the retry budget configurable via the config file with sane, validated defaults (16 attempts, 1000 ms delay), and (b) redesign the wait so a genuine reservation-price mismatch is distinguished from sync lag and **fails fast** instead of timing out.

**Architecture:** Extract the polling loop out of the `*service`-bound `waitForTokenVisible` into a free function `pollTokenVisible` that depends on a narrow `reservationPriceReader` interface plus injectable `attempts int` / `delay time.Duration` parameters — testable with a fake reader and no real sleeps. Because a reservation's `(price, paymentToken)` is **write-once at mint** (only `SafeMintWithReservation` writes it; there is no price setter), a load-balanced RPC backend can only ever differ from its peers in whether it has *synced the token yet*, never in the reservation's *value*. So: `getReservationPrice` returning the `(0, 0x0)` sentinel (or reverting) means "not synced yet" → retry; a **concrete, differing** reservation means a permanent mismatch → return a typed `ErrReservationPriceMismatch` immediately; a match → proceed. Exhausting the budget without the token ever appearing returns a typed `ErrTokenNotVisible`. The two tunables move from hardcoded constants into `config.Config`, validated at parse time and threaded through `booking.NewService`.

**Tech Stack:** Go 1.25, go-ethereum bindings (`accounts/abi/bind`, `common`), `go.uber.org/zap`, spf13 `viper`/`pflag` for config, `testify/require` for tests.

## Global Constraints

- Module path is versioned `github.com/chain4travel/camino-messenger-bot/v13`; imports use the `/v13` suffix.
- Every `.go` file requires the Camino license header (two comment lines, copied verbatim from an existing file in the same package). `lint.sh` enforces it.
- `.golangci.yml` denies `testify/assert` (use `testify/require`), `golang/mock` (use `go.uber.org/mock`), and `io/ioutil`.
- Tests run under `-race -shuffle=on`; deterministic, no wall-clock dependence beyond an injected `delay`.
- Production behavior stays revert-safe: the wait only ever *delays or fails* a buy, never changes what gets bought. Fail-fast on mismatch is safe only because reservation `(price, paymentToken)` is immutable post-mint (verified: no `set/updateReservationPrice` transactor exists; only `SafeMintWithReservation` writes it).
- Config precedent (follow exactly): durations are flat top-level integer flags with units documented in flag help (`response_timeout` = ms, `bot_auth_cache_timeout` = seconds). The config round-trip test (`config/config_reader_test.go`) requires every field emitted by `Config.unparse()` to also be present with the same value in `config/test_config.yaml`.

**Defaults (sane, overridable):** `token_visible_max_attempts = 16`, `token_visible_retry_delay = 1000` (milliseconds). Worst-case wait `(attempts-1) × delay ≈ 15s`.

**Starting point:** The working tree currently has uncommitted edits to `pkg/booking/booking.go` and an untracked `pkg/booking/booking_test.go` from an earlier attempt. **Task 0 resets these** so the plan executes cleanly from committed `HEAD` (`ee0a20b`) via TDD.

---

### Task 0: Reset working tree to clean HEAD

**Files:**
- Restore: `pkg/booking/booking.go`
- Delete: `pkg/booking/booking_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: a clean working tree at `ee0a20b`.

- [ ] **Step 1: Discard the uncommitted changes**

```bash
git restore pkg/booking/booking.go
rm -f pkg/booking/booking_test.go
```

- [ ] **Step 2: Verify clean tree and correct HEAD**

Run: `git status --short && git rev-parse --short HEAD`
Expected: no output from `status --short`, and HEAD prints `ee0a20b`.

---

### Task 1: Add configurable, validated retry settings to `config`

Adds the two tunables end-to-end in the config package. Nothing consumes them yet, so the build stays green and this task is independently testable via the existing config round-trip test plus a new negative case.

**Files:**
- Modify: `config/config.go` (add fields to `Config` and `UnparsedConfig`; extend `unparse`)
- Modify: `config/flags.go` (register two flags with defaults)
- Modify: `config/config_reader.go` (parse + validate in `parseConfig`; add error vars)
- Modify: `config/test_config.yaml` (add the two keys so the round-trip test matches)
- Test: `config/config_reader_test.go` (add one negative case)

**Interfaces:**
- Consumes: nothing from other tasks.
- Produces (relied on by Task 2's app wiring):
  - `config.Config.TokenVisibleMaxAttempts int`
  - `config.Config.TokenVisibleRetryDelay time.Duration`
  - Config keys / flags: `token_visible_max_attempts` (int64, default 16), `token_visible_retry_delay` (int64 ms, default 1000).
  - Validation: `parseConfig` returns an error (wrapped as `errInvalidRawConfig`) when attempts < 1 or delay < 0.

- [ ] **Step 1: Add the config-package error vars**

In `config/config_reader.go`, extend the `var (...)` error block (currently ends at `errInvalidBookingTokenAddress`) by adding:

```go
	errNonPositiveTokenVisibleAttempts = errors.New("token_visible_max_attempts must be >= 1")
	errNegativeTokenVisibleRetryDelay  = errors.New("token_visible_retry_delay must be >= 0")
```

- [ ] **Step 2: Add fields to `Config` and `UnparsedConfig` and extend `unparse`**

In `config/config.go`:

Add to the `Config` struct (after `RecordExpiration bool`):

```go
	TokenVisibleMaxAttempts int
	TokenVisibleRetryDelay  time.Duration
```

Add to the `UnparsedConfig` struct (after the `RecordExpiration bool` mapstructure field):

```go
	TokenVisibleMaxAttempts int64 `mapstructure:"token_visible_max_attempts"`
	TokenVisibleRetryDelay  int64 `mapstructure:"token_visible_retry_delay"` // milliseconds
```

Add to the returned `&UnparsedConfig{...}` literal in `unparse()` (after `RecordExpiration: cfg.RecordExpiration,`):

```go
		TokenVisibleMaxAttempts: int64(cfg.TokenVisibleMaxAttempts),
		TokenVisibleRetryDelay:  int64(cfg.TokenVisibleRetryDelay / time.Millisecond),
```

- [ ] **Step 3: Register the flags with defaults**

In `config/flags.go`, after the `response_timeout` flag line, add:

```go
	flags.Int64("token_visible_max_attempts", 16, "Max attempts to poll getReservationPrice until the minted token is visible on the local RPC node before buying.")
	flags.Int64("token_visible_retry_delay", 1000, "Delay between token-visibility poll attempts (in milliseconds).")
```

- [ ] **Step 4: Parse and validate in `parseConfig`**

In `config/config_reader.go`, inside `parseConfig`, after the `if !common.IsHexAddress(cfg.BookingTokenAddress) { ... }` block and before the `return &Config{...}`, add:

```go
	if cfg.TokenVisibleMaxAttempts < 1 {
		return nil, errNonPositiveTokenVisibleAttempts
	}

	if cfg.TokenVisibleRetryDelay < 0 {
		return nil, errNegativeTokenVisibleRetryDelay
	}
```

Then add to the returned `&Config{...}` literal (after `RecordExpiration: cfg.RecordExpiration,`):

```go
		TokenVisibleMaxAttempts: int(cfg.TokenVisibleMaxAttempts),
		TokenVisibleRetryDelay:  time.Duration(cfg.TokenVisibleRetryDelay) * time.Millisecond,
```

- [ ] **Step 5: Add the two keys to `test_config.yaml`**

In `config/test_config.yaml`, append these two lines (keys stay alphabetically ordered → at the end after `rpc_server:`):

```yaml
token_visible_max_attempts: 16
token_visible_retry_delay: 1000
```

- [ ] **Step 6: Add the negative validation test case**

In `config/config_reader_test.go`, inside the `tests` map (after the `"from flags"` case), add:

```go
		"invalid token_visible_max_attempts": {
			prepare: func(t *testing.T, cr *reader) {
				require.NoError(t, setFlagsFromMap(cr.flags, "", rawMap))
				require.NoError(t, cr.flags.Set("token_visible_max_attempts", "0"))
				cr.viper.Set(flagKeyConfig, nonExistingConfigPath)
			},
			flags:       Flags(),
			expectedErr: errInvalidRawConfig,
		},
```

- [ ] **Step 7: Run the config tests**

Run: `go test -race ./config/ 2>&1 | tail -20`
Expected: PASS — round-trip cases pass (test_config.yaml now includes the two keys), `"default"` still fails on empty bot key, and `"invalid token_visible_max_attempts"` fails with `errInvalidRawConfig`.

- [ ] **Step 8: Commit**

```bash
git add config/config.go config/flags.go config/config_reader.go config/test_config.yaml config/config_reader_test.go
git commit -m "feat(config): add configurable token-visibility retry attempts and delay"
```

---

### Task 2: Single-read fail-fast `pollTokenVisible` with typed errors + config wiring

Redesign the wait: extract a testable `pollTokenVisible` that classifies each read, fails fast on a real mismatch, and times out distinctly on sync lag. Move the tunables from package constants onto the `service` struct, fed by config.

**Files:**
- Modify: `pkg/booking/booking.go` (`service` struct, `NewService`, `waitForTokenVisible`, new typed errors + `pollTokenVisible`)
- Modify: `internal/app/app.go` (`booking.NewService(...)` call at `internal/app/app.go:123-131`)
- Test: `pkg/booking/booking_test.go` (create)

**Interfaces:**
- Consumes: `config.Config.TokenVisibleMaxAttempts` / `TokenVisibleRetryDelay` (Task 1); `*bookingtoken.Bookingtoken`'s `GetReservationPrice(opts *bind.CallOpts, tokenId *big.Int) (struct { Price *big.Int; PaymentToken common.Address }, error)`.
- Produces:
  - Types `ErrReservationPriceMismatch` and `ErrTokenNotVisible` (pointer-receiver `Error()`), for `errors.As` at call sites.
  - `reservationPriceReader` interface (single `GetReservationPrice` method matching the binding's anonymous-struct return).
  - `func pollTokenVisible(ctx context.Context, reader reservationPriceReader, logger *zap.SugaredLogger, tokenID *big.Int, expectedPrice *big.Int, expectedPaymentToken common.Address, attempts int, delay time.Duration) error` — returns `nil` (match), `*ErrReservationPriceMismatch` (fail fast), `*ErrTokenNotVisible` (timeout), or `ctx.Err()`.
  - `booking.NewService` gains trailing params `tokenVisibleMaxAttempts int, tokenVisibleRetryDelay time.Duration`, stored on `service`.
  - Package constants `maxTokenVisibleAttempts` / `retryDelay` are **removed** (defaults now live in `config/flags.go`).

- [ ] **Step 1: Write the failing tests**

Create `pkg/booking/booking_test.go`:

```go
// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package booking

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// reservationResult is one scripted return value for the fake reader.
type reservationResult struct {
	price        *big.Int
	paymentToken common.Address
	err          error
}

// fakeReservationReader returns a scripted sequence of results, one per call,
// and counts how many times it was invoked.
type fakeReservationReader struct {
	results []reservationResult
	calls   int
}

func (f *fakeReservationReader) GetReservationPrice(_ *bind.CallOpts, _ *big.Int) (struct {
	Price        *big.Int
	PaymentToken common.Address
}, error) {
	r := f.results[f.calls]
	f.calls++
	return struct {
		Price        *big.Int
		PaymentToken common.Address
	}{Price: r.price, PaymentToken: r.paymentToken}, r.err
}

func TestPollTokenVisible(t *testing.T) {
	var (
		tokenID = big.NewInt(42)
		price   = big.NewInt(1000)
		token   = common.HexToAddress("0x00000000000000000000000000000000000000aa")
		zero    = common.Address{}
		logger  = zap.NewNop().Sugar()
	)

	visible := reservationResult{price: price, paymentToken: token}
	// The pre-mint / not-yet-synced view: (0, 0x0).
	lagging := reservationResult{price: big.NewInt(0), paymentToken: zero}

	t.Run("visible on first attempt makes a single call", func(t *testing.T) {
		reader := &fakeReservationReader{results: []reservationResult{visible}}
		err := pollTokenVisible(context.Background(), reader, logger, tokenID, price, token, 16, time.Millisecond)
		require.NoError(t, err)
		require.Equal(t, 1, reader.calls)
	})

	t.Run("visible after sync lag retries", func(t *testing.T) {
		reader := &fakeReservationReader{results: []reservationResult{lagging, lagging, visible}}
		err := pollTokenVisible(context.Background(), reader, logger, tokenID, price, token, 16, time.Millisecond)
		require.NoError(t, err)
		require.Equal(t, 3, reader.calls)
	})

	t.Run("read errors are retried like a lagging read", func(t *testing.T) {
		reader := &fakeReservationReader{results: []reservationResult{
			{err: errors.New("execution reverted")},
			visible,
		}}
		err := pollTokenVisible(context.Background(), reader, logger, tokenID, price, token, 16, time.Millisecond)
		require.NoError(t, err)
		require.Equal(t, 2, reader.calls)
	})

	t.Run("present with wrong price fails fast", func(t *testing.T) {
		reader := &fakeReservationReader{results: []reservationResult{
			{price: big.NewInt(999), paymentToken: token},
		}}
		err := pollTokenVisible(context.Background(), reader, logger, tokenID, price, token, 16, time.Second)
		var mismatch *ErrReservationPriceMismatch
		require.ErrorAs(t, err, &mismatch)
		require.Equal(t, 1, reader.calls) // no retries — it's permanent
		require.Equal(t, big.NewInt(999), mismatch.ActualPrice)
		require.Equal(t, price, mismatch.ExpectedPrice)
	})

	t.Run("present with wrong payment token fails fast", func(t *testing.T) {
		otherToken := common.HexToAddress("0x00000000000000000000000000000000000000bb")
		reader := &fakeReservationReader{results: []reservationResult{
			{price: price, paymentToken: otherToken},
		}}
		err := pollTokenVisible(context.Background(), reader, logger, tokenID, price, token, 16, time.Second)
		var mismatch *ErrReservationPriceMismatch
		require.ErrorAs(t, err, &mismatch)
		require.Equal(t, 1, reader.calls)
		require.Equal(t, otherToken, mismatch.ActualPaymentToken)
	})

	t.Run("context cancellation aborts the wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		reader := &fakeReservationReader{results: []reservationResult{lagging, visible}}
		err := pollTokenVisible(ctx, reader, logger, tokenID, price, token, 16, time.Second)
		require.ErrorIs(t, err, context.Canceled)
		require.Equal(t, 1, reader.calls)
	})

	t.Run("never visible times out as ErrTokenNotVisible", func(t *testing.T) {
		reader := &fakeReservationReader{results: []reservationResult{lagging, lagging, lagging, lagging}}
		err := pollTokenVisible(context.Background(), reader, logger, tokenID, price, token, 4, time.Millisecond)
		var notVisible *ErrTokenNotVisible
		require.ErrorAs(t, err, &notVisible)
		require.Equal(t, 4, reader.calls)
		require.Equal(t, 4, notVisible.Attempts)
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail to compile**

Run: `go test ./pkg/booking/ 2>&1 | head -20`
Expected: FAIL — build errors: `undefined: pollTokenVisible`, `undefined: ErrReservationPriceMismatch`, `undefined: ErrTokenNotVisible`.

- [ ] **Step 3: Refactor `booking.go` — struct fields, constructor, typed errors, extracted function**

In `pkg/booking/booking.go`:

(a) Add two fields to the `service` struct (after `ethClient *ethclient.Client`):

```go
	tokenVisibleMaxAttempts int
	tokenVisibleRetryDelay  time.Duration
```

(b) Change the `NewService` signature to append two params (currently ends `... cmAccounts cmaccounts.Service,\n) (Service, error) {`):

```go
func NewService(
	ethClient *ethclient.Client,
	bookingTokenAddress common.Address,
	minterCMAccountAddress common.Address,
	privateKey *ecdsa.PrivateKey,
	chainID *big.Int,
	logger *zap.SugaredLogger,
	cmAccounts cmaccounts.Service,
	tokenVisibleMaxAttempts int,
	tokenVisibleRetryDelay time.Duration,
) (Service, error) {
```

(c) In the returned `&service{...}` literal inside `NewService`, add (after the existing fields):

```go
		tokenVisibleMaxAttempts: tokenVisibleMaxAttempts,
		tokenVisibleRetryDelay:  tokenVisibleRetryDelay,
```

(d) Replace the entire existing `waitForTokenVisible` method (its doc comment, the inner `const ( maxTokenVisibleAttempts = 10; retryDelay = 3 * time.Second )` block, the `for attempt := range maxTokenVisibleAttempts` loop, and the trailing `return fmt.Errorf(...)`) with:

```go
// ErrReservationPriceMismatch is returned when the reserved token is present on
// the local RPC node but its reservation price/paymentToken differ from what we
// expected. A reservation is written once at mint (SafeMintWithReservation) and
// never updated, so this is a permanent condition — we fail fast rather than
// exhausting the retry budget.
type ErrReservationPriceMismatch struct {
	TokenID              *big.Int
	ExpectedPrice        *big.Int
	ExpectedPaymentToken common.Address
	ActualPrice          *big.Int
	ActualPaymentToken   common.Address
}

func (e *ErrReservationPriceMismatch) Error() string {
	return fmt.Sprintf(
		"reserved token %s has price %s (paymentToken %s) but expected %s (paymentToken %s)",
		e.TokenID, e.ActualPrice, e.ActualPaymentToken.Hex(),
		e.ExpectedPrice, e.ExpectedPaymentToken.Hex())
}

// ErrTokenNotVisible is returned when the reserved token never became visible on
// the local RPC node (getReservationPrice kept returning the (0, 0x0) sentinel
// or erroring) within the retry budget — i.e. sync lag that did not resolve.
type ErrTokenNotVisible struct {
	TokenID              *big.Int
	ExpectedPrice        *big.Int
	ExpectedPaymentToken common.Address
	Attempts             int
}

func (e *ErrTokenNotVisible) Error() string {
	return fmt.Sprintf(
		"token %s (price %s, paymentToken %s) not visible on local RPC node after %d attempts",
		e.TokenID, e.ExpectedPrice, e.ExpectedPaymentToken.Hex(), e.Attempts)
}

// reservationPriceReader reads a token's on-chain reservation price. It is
// satisfied by *bookingtoken.Bookingtoken and narrowed to this one method so
// the polling logic can be exercised with a fake in unit tests.
type reservationPriceReader interface {
	GetReservationPrice(opts *bind.CallOpts, tokenId *big.Int) (struct {
		Price        *big.Int
		PaymentToken common.Address
	}, error)
}

// waitForTokenVisible polls getReservationPrice until the reserved token is
// visible on the local RPC node with the expected price and payment token, then
// returns nil. It retries up to tokenVisibleMaxAttempts times with
// tokenVisibleRetryDelay between attempts (both configurable via the config file).
//
// This guards against split-brain reads when the write (mint) and read (buy)
// hit different backends of a load-balanced RPC provider such as drpc.org: the
// node handling the buy may lag behind the one that confirmed the mint, causing
// getReservationPrice to return the (0, 0x0) sentinel and the contract to revert
// with IncorrectPrice.
func (bs *service) waitForTokenVisible(
	ctx context.Context,
	tokenID *big.Int,
	expectedPrice *big.Int,
	expectedPaymentToken common.Address,
) error {
	return pollTokenVisible(
		ctx, bs.bookingToken, bs.logger,
		tokenID, expectedPrice, expectedPaymentToken,
		bs.tokenVisibleMaxAttempts, bs.tokenVisibleRetryDelay,
	)
}

// pollTokenVisible is the injectable core of waitForTokenVisible. attempts and
// delay are parameters so tests can drive it without real sleeps.
//
// Reservations are immutable after mint, so a backend can only differ from its
// peers in whether it has synced the token yet — never in the reservation's
// value. That lets us classify each read unambiguously:
//   - error, or the (0, 0x0) sentinel  -> not synced yet, retry
//   - present and matches expected     -> success
//   - present and differs from expected-> permanent mismatch, fail fast
//
// Note: a genuinely free native-coin reservation would itself be (0, 0x0) and is
// treated as "not synced" here; such an expectation would time out rather than
// match. In practice buy prices are non-zero, so this edge does not arise.
func pollTokenVisible(
	ctx context.Context,
	reader reservationPriceReader,
	logger *zap.SugaredLogger,
	tokenID *big.Int,
	expectedPrice *big.Int,
	expectedPaymentToken common.Address,
	attempts int,
	delay time.Duration,
) error {
	var zeroAddr common.Address
	for attempt := range attempts {
		reservation, err := reader.GetReservationPrice(
			&bind.CallOpts{Context: ctx}, tokenID)
		if err == nil {
			present := reservation.Price.Sign() != 0 || reservation.PaymentToken != zeroAddr
			if present {
				if reservation.Price.Cmp(expectedPrice) == 0 &&
					reservation.PaymentToken == expectedPaymentToken {
					if attempt > 0 {
						logger.Infof("waitForTokenVisible: token %s visible after %d retries", tokenID, attempt)
					}
					return nil
				}
				// Token is present but its immutable reservation differs — retrying
				// cannot change this, so surface a distinct mismatch error now.
				return &ErrReservationPriceMismatch{
					TokenID:              tokenID,
					ExpectedPrice:        expectedPrice,
					ExpectedPaymentToken: expectedPaymentToken,
					ActualPrice:          reservation.Price,
					ActualPaymentToken:   reservation.PaymentToken,
				}
			}
			logger.Debugf("waitForTokenVisible: attempt %d/%d: token %s not yet visible (0, 0x0)",
				attempt+1, attempts, tokenID)
		} else {
			logger.Debugf("waitForTokenVisible: attempt %d/%d: GetReservationPrice error: %v",
				attempt+1, attempts, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	return &ErrTokenNotVisible{
		TokenID:              tokenID,
		ExpectedPrice:        expectedPrice,
		ExpectedPaymentToken: expectedPaymentToken,
		Attempts:             attempts,
	}
}
```

Leave all imports as-is (`time`, `bind`, `common`, `fmt`, `big`, `zap` all remain in use).

- [ ] **Step 4: Wire the config values through `app.go`**

In `internal/app/app.go`, update the `booking.NewService(...)` call (`internal/app/app.go:123`) to pass the two new args after `cmAccounts`:

```go
	bookingService, err := booking.NewService(
		evmClient,
		cfg.BookingTokenAddress,
		cfg.CMAccountAddress,
		cfg.BotKey,
		chainID,
		logger,
		cmAccounts,
		cfg.TokenVisibleMaxAttempts,
		cfg.TokenVisibleRetryDelay,
	)
```

- [ ] **Step 5: Run the booking tests to verify they pass**

Run: `go test -race ./pkg/booking/ 2>&1 | tail -20`
Expected: PASS (`ok  github.com/chain4travel/camino-messenger-bot/v13/pkg/booking`).

- [ ] **Step 6: Verify the whole module builds (app wiring compiles)**

Run: `go build ./... 2>&1 | tail -20`
Expected: no output, exit 0.

- [ ] **Step 7: Commit**

```bash
git add pkg/booking/booking.go pkg/booking/booking_test.go internal/app/app.go
git commit -m "refactor(booking): fail fast on reservation price mismatch, config-driven retries"
```

---

### Task 3: Skip the wasted final sleep (review comment #1)

The timeout path (all-lag reads) still sleeps after the final attempt. Skip it.

**Files:**
- Modify: `pkg/booking/booking.go` (`pollTokenVisible`)
- Test: `pkg/booking/booking_test.go` (add one subtest)

**Interfaces:**
- Consumes: `pollTokenVisible` from Task 2.
- Produces: same signature; a timing-out run does `attempts` reads with only `attempts-1` sleeps.

- [ ] **Step 1: Add the failing timing test**

In `pkg/booking/booking_test.go`, add this subtest inside `TestPollTokenVisible` (after the existing subtests, before the closing brace):

```go
	t.Run("timeout does no trailing sleep after the final attempt", func(t *testing.T) {
		attempts := 4
		reader := &fakeReservationReader{results: []reservationResult{lagging, lagging, lagging, lagging}}
		// A trailing sleep after the final attempt would push elapsed to >= attempts*delay.
		start := time.Now()
		err := pollTokenVisible(context.Background(), reader, logger, tokenID, price, token, attempts, 50*time.Millisecond)
		elapsed := time.Since(start)
		var notVisible *ErrTokenNotVisible
		require.ErrorAs(t, err, &notVisible)
		require.Equal(t, attempts, reader.calls)
		// Only attempts-1 sleeps of 50ms; the final attempt must not sleep.
		require.Less(t, elapsed, time.Duration(attempts)*50*time.Millisecond)
	})
```

- [ ] **Step 2: Run the new subtest to verify it fails**

Run: `go test -race -run 'TestPollTokenVisible/timeout_does_no_trailing_sleep' ./pkg/booking/ -v 2>&1 | tail -20`
Expected: FAIL — `elapsed >= 4*50ms` because the loop currently sleeps after every attempt including the last.

- [ ] **Step 3: Skip the sleep on the final attempt**

In `pollTokenVisible`, insert the early-break immediately before the `select` block:

```go
		// No point sleeping after the final attempt — we're about to give up.
		if attempt == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./pkg/booking/ 2>&1 | tail -20`
Expected: PASS (all subtests, including the new timing one).

- [ ] **Step 5: Commit**

```bash
git add pkg/booking/booking.go pkg/booking/booking_test.go
git commit -m "perf(booking): skip trailing retry sleep in pollTokenVisible"
```

---

### Task 4: Full verification

**Files:** none (verification only).

**Interfaces:**
- Consumes: all prior tasks.
- Produces: confidence that the whole module tests, vets, and lints clean.

- [ ] **Step 1: Run the affected package tests under race + shuffle**

Run: `go test -race -shuffle=on ./config/ ./pkg/booking/ 2>&1 | tail -20`
Expected: PASS for both packages.

- [ ] **Step 2: Build and vet the whole module**

Run: `go build ./... && go vet ./... 2>&1 | tail -20`
Expected: build exit 0; no vet findings in `config` or `pkg/booking`.

- [ ] **Step 3: Lint (golangci-lint + license header)**

Run: `./scripts/lint.sh 2>&1 | tail -30`
Expected: clean (new `booking_test.go` carries the license header; no new findings). If the lint tooling is unavailable in this environment, note it and fall back to `gofmt -l config/ pkg/booking/` (expected: no files listed).

- [ ] **Step 4: Full unit suite sanity (optional, slower)**

Run: `go test -race ./... 2>&1 | tail -30`
Expected: PASS (or unchanged from the pre-existing baseline for any unrelated flaky/e2e-gated packages).

---

## Self-Review

**Spec coverage:**
- Configurable retry attempts + delay, sane defaults, overridable → Task 1 + Task 2 wiring. ✓
- Default attempts 16, delay 1000 ms → `flags.go` + `test_config.yaml`. ✓
- Validation so a bad value can't silently break buys → Task 1 Step 4 (attempts ≥ 1, delay ≥ 0). ✓
- Distinguish price mismatch from timeout, fail fast on mismatch → Task 2 (`ErrReservationPriceMismatch` returned immediately; `ErrTokenNotVisible` on timeout). ✓
- Review comment #1 (skip final sleep) → Task 3. ✓
- Review comment #2 (timeout masked mismatch) → *resolved by design* in Task 2 (no longer just documented). ✓
- Review comment #3 (unit tests) → Task 2 + timing test in Task 3. ✓
- Review comment #4 (hardcoded constants) → resolved by config-driving them; constants removed. ✓

**Placeholder scan:** No TBD/TODO/"handle edge cases"; every code and command step is concrete. ✓

**Type consistency:**
- `reservationPriceReader.GetReservationPrice` matches the binding's anonymous-struct return exactly; used identically in fake, `pollTokenVisible`, `waitForTokenVisible`. ✓
- `pollTokenVisible` signature `(ctx, reader, logger, tokenID, expectedPrice, expectedPaymentToken, attempts, delay)` identical at definition and all call sites/tests. ✓
- `ErrReservationPriceMismatch` fields (`TokenID, ExpectedPrice, ExpectedPaymentToken, ActualPrice, ActualPaymentToken`) and `ErrTokenNotVisible` fields (`TokenID, ExpectedPrice, ExpectedPaymentToken, Attempts`) match their construction sites and the fields asserted in tests (`ActualPrice`, `ActualPaymentToken`, `ExpectedPrice`, `Attempts`). ✓
- `NewService` trailing params `tokenVisibleMaxAttempts int, tokenVisibleRetryDelay time.Duration` match the `service` fields, the `&service{}` literal, and the `app.go` args `cfg.TokenVisibleMaxAttempts` (int) / `cfg.TokenVisibleRetryDelay` (time.Duration). ✓
- Config names `TokenVisibleMaxAttempts`/`TokenVisibleRetryDelay` and keys `token_visible_max_attempts`/`token_visible_retry_delay` consistent across `Config`, `UnparsedConfig`, `unparse`, `parseConfig`, `flags.go`, `test_config.yaml`. ✓
- Units: delay round-trips as **milliseconds** (int64); `Config` holds `time.Duration`; flag help says "milliseconds"; default `1000`. ✓

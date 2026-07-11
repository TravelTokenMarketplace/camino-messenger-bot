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
}, error,
) {
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
}

// Copyright (C) 2022-2026, Chain4Travel AG. All rights reserved.
// See the file LICENSE for licensing terms.

package state

import (
	"testing"

	"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/config"
	"github.com/stretchr/testify/require"
)

func TestNormalizeRealistic(t *testing.T) {
	t.Cleanup(func() { config.SetRealisticPrice(false, config.RealisticNativeBaseUnitsDefault, nil) })
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

// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

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

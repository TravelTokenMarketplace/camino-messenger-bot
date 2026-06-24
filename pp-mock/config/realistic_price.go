// Copyright (C) 2022-2026, Chain4Travel AG. All rights reserved.
// See the file LICENSE for licensing terms.

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

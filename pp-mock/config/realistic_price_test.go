// Copyright (C) 2022-2026, Chain4Travel AG. All rights reserved.
// See the file LICENSE for licensing terms.

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
	t.Cleanup(func() { SetRealisticPrice(false, RealisticNativeBaseUnitsDefault, nil) })
	SetRealisticPrice(true, RealisticNativeBaseUnitsDefault, map[string]uint32{"0xabc": 6})
	require.Equal(t, uint32(6), TokenDecimalsFor("0xABC"))
	require.Equal(t, RealisticTokenDecimalsDefault, TokenDecimalsFor("0xnotpresent"))
}

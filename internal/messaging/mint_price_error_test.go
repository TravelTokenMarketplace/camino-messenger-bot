// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

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

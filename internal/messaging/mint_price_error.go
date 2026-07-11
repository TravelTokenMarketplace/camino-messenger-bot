// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

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

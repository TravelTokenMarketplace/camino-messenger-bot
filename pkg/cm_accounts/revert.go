// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package cmaccounts

import (
	"errors"
	"fmt"
	"strings"

	"github.com/chain4travel/camino-messenger-contracts/go/contracts/bookingtoken"
	"github.com/chain4travel/camino-messenger-contracts/go/contracts/bookingtokenoperator"
	"github.com/chain4travel/camino-messenger-contracts/go/contracts/cmaccount"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rpc"
)

// revertABIs holds the ABIs whose custom errors can bubble up through a
// CMAccount call (the CMAccount delegates booking-token mint/buy/cancel logic to
// the BookingToken and BookingTokenOperator contracts, so their custom errors
// surface from a CMAccount transaction's gas-estimation eth_call).
var revertABIs = func() []abi.ABI {
	metas := []string{
		cmaccount.CmaccountMetaData.ABI,
		bookingtokenoperator.BookingtokenoperatorMetaData.ABI,
		bookingtoken.BookingtokenMetaData.ABI,
	}
	out := make([]abi.ABI, 0, len(metas))
	for _, m := range metas {
		if a, err := abi.JSON(strings.NewReader(m)); err == nil {
			out = append(out, a)
		}
	}
	return out
}()

// decodeRevert turns a raw RPC revert error into a readable custom-error string,
// e.g. `UnexpectedPrice(tokenId: 12, actualPrice: …, expectedPrice: …)`.
//
// go-ethereum only auto-decodes the standard Error(string)/Panic(uint256)
// reverts; custom errors arrive as opaque bytes in the RPC error's data field
// (often rendered as garbage when the node stuffs them into the message string).
// This matches the 4-byte selector against the known contract ABIs and unpacks
// the arguments. Returns ("", false) when the error carries no decodable
// custom-error data.
func decodeRevert(err error) (string, bool) {
	var de rpc.DataError
	if !errors.As(err, &de) {
		return "", false
	}
	hexData, ok := de.ErrorData().(string)
	if !ok {
		return "", false
	}
	data, derr := hexutil.Decode(hexData)
	if derr != nil || len(data) < 4 {
		return "", false
	}
	selector := [4]byte(data[:4])
	for _, a := range revertABIs {
		for _, e := range a.Errors {
			if [4]byte(e.ID[:4]) != selector {
				continue
			}
			args, uerr := e.Unpack(data)
			if uerr != nil {
				return e.Name, true
			}
			return fmt.Sprintf("%s%v", e.Name, args), true
		}
	}
	return "", false
}

// wrapTxErr wraps a contract-call error with the given action description,
// enriching it with the decoded custom-error name and arguments when the
// underlying revert data can be decoded against the known contract ABIs.
func wrapTxErr(action string, err error) error {
	if decoded, ok := decodeRevert(err); ok {
		return fmt.Errorf("%s: %s: %w", action, decoded, err)
	}
	return fmt.Errorf("%s: %w", action, err)
}

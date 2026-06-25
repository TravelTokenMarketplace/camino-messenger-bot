// Copyright (C) 2022-2026, Chain4Travel AG. All rights reserved.
// See the file LICENSE for licensing terms.

package cmaccounts

import (
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/require"
)

// dataErr is a minimal stand-in for the rpc.DataError that go-ethereum returns
// for a reverted eth_call/estimateGas: the human-readable message is opaque,
// the structured revert bytes live in ErrorData().
type dataErr struct {
	msg  string
	data interface{}
}

func (e dataErr) Error() string          { return e.msg }
func (e dataErr) ErrorData() interface{} { return e.data }

func TestDecodeRevert_UnexpectedPrice(t *testing.T) {
	require := require.New(t)

	// Build real revert data: selector + abi-encoded (tokenId, actualPrice, expectedPrice).
	var operatorABI abi.ABI
	for _, a := range revertABIs {
		if _, ok := a.Errors["UnexpectedPrice"]; ok {
			operatorABI = a
			break
		}
	}
	e := operatorABI.Errors["UnexpectedPrice"]
	args, err := e.Inputs.Pack(big.NewInt(12), big.NewInt(1000), big.NewInt(2000))
	require.NoError(err)
	revertData := append(append([]byte{}, e.ID[:4]...), args...)

	decoded, ok := decodeRevert(dataErr{msg: "execution reverted", data: hexutil.Encode(revertData)})
	require.True(ok)
	require.Contains(decoded, "UnexpectedPrice")
	require.Contains(decoded, "12")
	require.Contains(decoded, "1000")
	require.Contains(decoded, "2000")
}

func TestDecodeRevert_NonDataError(t *testing.T) {
	_, ok := decodeRevert(errors.New("plain error"))
	require.False(t, ok)
}

func TestDecodeRevert_UnknownSelector(t *testing.T) {
	_, ok := decodeRevert(dataErr{msg: "execution reverted", data: hexutil.Encode([]byte{0xde, 0xad, 0xbe, 0xef})})
	require.False(t, ok)
}

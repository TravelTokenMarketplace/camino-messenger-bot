// Copyright (C) 2022-2026, Chain4Travel AG. All rights reserved.
// See the file LICENSE for licensing terms.

package v3

import (
	"context"
	"time"

	"buf.build/gen/go/chain4travel/camino-messenger-protocol/grpc/go/cmp/services/book/v3/bookv3grpc"
	bookv3 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/services/book/v3"
	typesv1 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/types/v1"
	"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/common"
	"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/config"
	"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/handlers/state"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ bookv3grpc.MintServiceServer = (*mintServiceV3Server)(nil)

type mintServiceV3Server struct{}

func NewMintServiceServer() bookv3grpc.MintServiceServer {
	return &mintServiceV3Server{}
}

func (s *mintServiceV3Server) Mint(_ context.Context, req *bookv3.MintRequest) (*bookv3.MintResponse, error) {
	storedValidateData, ok := state.GetStore().GetValidationResult(req.ValidationId.Value)
	if !ok {
		return &bookv3.MintResponse{
			Header: common.ErrorHeaderV1("Validation not found in state"),
		}, nil
	}

	header := common.SuccessHeaderV1()
	mintPrice := common.BookingTokenPriceV3
	if config.RealisticPriceEnabled {
		mintPrice = storedValidateData.Data.VerifiedPrice.ToPriceV3()
	} else {
		mintResponseInfoMessage := "Please note that the price given in this mint response does not reflect the verified total price of the product of '" + storedValidateData.Data.VerifiedPrice.Price + "'. The price is just a minimum value to be able to mint the product."
		header = common.SuccessHeaderWithInfoV1(mintResponseInfoMessage)
	}

	response := bookv3.MintResponse{
		Header: header,
		MintId: &typesv1.UUID{Value: uuid.New().String()},
		BuyableUntil: &timestamppb.Timestamp{
			Seconds: time.Now().Add(config.BuyableUntilDefault).Unix(),
		},
		ValidationId: req.ValidationId,
		Price:        mintPrice,
		Cancellable:  true,
	}

	state.GetStore().AddMintResult(response.MintId.Value, storedValidateData.Data.InitialSearchData.SeatMapID)

	return &response, nil
}

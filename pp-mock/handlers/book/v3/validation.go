// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package v3

import (
	"context"
	"log"

	"buf.build/gen/go/chain4travel/camino-messenger-protocol/grpc/go/cmp/services/book/v3/bookv3grpc"
	bookv3 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/services/book/v3"
	typesv1 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/types/v1"
	typesv3 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/types/v3"

	"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/common"
	"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/handlers/state"
	"github.com/google/uuid"
)

var _ bookv3grpc.ValidationServiceServer = (*validationServiceV3Server)(nil)

type validationServiceV3Server struct{}

func NewValidationServiceServer() bookv3grpc.ValidationServiceServer {
	return &validationServiceV3Server{}
}

func (s *validationServiceV3Server) Validation(_ context.Context, req *bookv3.ValidationRequest) (*bookv3.ValidationResponse, error) {
	if req.ValidationObject == nil ||
		req.ValidationObject.SearchIdentifier == nil ||
		req.ValidationObject.SearchIdentifier.ResultId == 0 ||
		req.ValidationObject.SearchIdentifier.SearchId == nil {
		return &bookv3.ValidationResponse{
			Header: common.ErrorHeaderV1("Invalid validation request: missing validation object or search identifier"),
		}, nil
	}

	// Look-up the store if we actually have a search storedSearchData for the given search identifier
	// If we don't have a storedSearchData, return an error
	searchID := req.ValidationObject.SearchIdentifier.SearchId.Value
	resultID := req.ValidationObject.SearchIdentifier.ResultId
	log.Printf("[book.v3.Validation] request searchId=%s resultId=%d", searchID, resultID)

	storedSearchData, found := state.GetStore().GetSearchResult(searchID)
	if !found {
		log.Printf("[book.v3.Validation] rejected: searchId=%s not found in state", searchID)
		return &bookv3.ValidationResponse{
			Header: common.ErrorHeaderV1("Invalid validation request: searchId not found in state"),
		}, nil
	}

	resultIndex := int(resultID - 1)
	if resultIndex < 0 || resultIndex >= len(storedSearchData.Data.Prices) {
		log.Printf("[book.v3.Validation] rejected: resultId=%d out of range (have %d prices) for searchId=%s",
			resultID, len(storedSearchData.Data.Prices), searchID)
		return &bookv3.ValidationResponse{
			Header: common.ErrorHeaderV1("Invalid validation request: resultId out of range"),
		}, nil
	}

	unifiedValidationPrice := storedSearchData.Data.Prices[resultIndex]
	validationPrice := unifiedValidationPrice.ToPriceV3()
	log.Printf("[book.v3.Validation] searchId=%s resultId=%d -> verifiedPrice=%s (selected from %d search prices)",
		searchID, resultID, unifiedValidationPrice, len(storedSearchData.Data.Prices))

	response := bookv3.ValidationResponse{
		Header:           common.SuccessHeaderV1(),
		ValidationId:     &typesv1.UUID{Value: uuid.New().String()},
		ValidationObject: req.ValidationObject,
		PriceDetail: &typesv3.PriceDetail{
			Price:       validationPrice,
			Description: "Validated total price",
		},
	}

	state.GetStore().AddValidationResult(response.ValidationId.Value, state.ValidationData{
		InitialSearchData: storedSearchData.Data,
		VerifiedPrice:     unifiedValidationPrice,
		JSONRequest:       req.String(),
		JSONResponse:      response.String(),
	})
	log.Printf("[book.v3.Validation] issued validationId=%s with verifiedPrice=%s", response.ValidationId.Value, unifiedValidationPrice)

	return &response, nil
}

// Copyright (C) 2022-2026, Chain4Travel AG. All rights reserved.
// See the file LICENSE for licensing terms.

package v4

import (
	"context"
	"log"

	"buf.build/gen/go/chain4travel/camino-messenger-protocol/grpc/go/cmp/services/book/v4/bookv4grpc"
	bookv4 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/services/book/v4"
	typesv4 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/types/v4"

	"github.com/chain4travel/camino-messenger-bot/v13/pkg/conversion"
	"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/common"
	"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/handlers/state"
)

var _ bookv4grpc.ValidationServiceServer = (*validationServiceV4Server)(nil)

type validationServiceV4Server struct{}

func NewValidationServiceServer() bookv4grpc.ValidationServiceServer {
	return &validationServiceV4Server{}
}

func (s *validationServiceV4Server) Validation(_ context.Context, req *bookv4.ValidationRequest) (*bookv4.ValidationResponse, error) {
	if req.ValidationObject == nil ||
		req.ValidationObject.SearchResultIdentifier == nil ||
		req.ValidationObject.SearchResultIdentifier.SearchId == nil ||
		req.ValidationObject.SearchResultIdentifier.SearchId.Value == "" {
		return errValidationResp(typesv4.ErrorCode_ERROR_CODE_INVALID_IDENTIFIERS, "Invalid validation request: missing required validation identifier fields"), nil
	}

	// Look-up the store if we actually have a search storedSearchData for the given search identifier
	// If we don't have a storedSearchData, return an error
	searchID := req.ValidationObject.SearchResultIdentifier.SearchId.Value
	resultID := req.ValidationObject.SearchResultIdentifier.ResultId
	log.Printf("[book.v4.Validation] request searchId=%s resultId=%d", searchID, resultID)

	storedSearchData, found := state.GetStore().GetSearchResult(searchID)
	if !found {
		log.Printf("[book.v4.Validation] rejected: searchId=%s not found in state", searchID)
		return errValidationResp(typesv4.ErrorCode_ERROR_CODE_INVALID_IDENTIFIERS, "Invalid validation request: searchId not found in state"), nil
	}

	if resultID >= conversion.MustIntToUInt32(len(storedSearchData.Data.Prices)) {
		log.Printf("[book.v4.Validation] rejected: resultId=%d out of range (have %d prices) for searchId=%s",
			resultID, len(storedSearchData.Data.Prices), searchID)
		return errValidationResp(typesv4.ErrorCode_ERROR_CODE_INVALID_IDENTIFIERS, "Invalid validation request: resultId out of range"), nil
	}

	unifiedValidationPrice := storedSearchData.Data.Prices[resultID]
	log.Printf("[book.v4.Validation] searchId=%s resultId=%d -> verifiedPrice=%s (selected from %d search prices)",
		searchID, resultID, unifiedValidationPrice, len(storedSearchData.Data.Prices))

	resp := &bookv4.ValidationResponse{
		Response: &bookv4.ValidationResponse_SuccessResponse{
			SuccessResponse: &bookv4.ValidationSuccessResponse{
				Header:           common.SuccessHeaderV4(),
				ValidationId:     common.NewExpiringUUID(),
				ValidationObject: req.ValidationObject,
				TotalPrice: &typesv4.TotalPrice{
					Value: unifiedValidationPrice.ToPriceV4(),
				},
			},
		},
	}

	validationID := resp.GetSuccessResponse().ValidationId.Id.Value
	state.GetStore().AddValidationResult(validationID, state.ValidationData{
		InitialSearchData: storedSearchData.Data,
		VerifiedPrice:     unifiedValidationPrice,
		JSONRequest:       req.String(),
		JSONResponse:      resp.String(),
	})
	log.Printf("[book.v4.Validation] issued validationId=%s with verifiedPrice=%s", validationID, unifiedValidationPrice)

	return resp, nil
}

func errValidationResp(code typesv4.ErrorCode, message string) *bookv4.ValidationResponse {
	return &bookv4.ValidationResponse{
		Response: &bookv4.ValidationResponse_ErrorResponse{
			ErrorResponse: &bookv4.ValidationErrorResponse{
				Header: common.ErrorHeaderV4(code, message),
			},
		},
	}
}

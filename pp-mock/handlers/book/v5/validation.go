// Copyright (C) 2022-2026, Chain4Travel AG. All rights reserved.
// See the file LICENSE for licensing terms.

package v5

import (
	"context"
	"log"

	"buf.build/gen/go/chain4travel/camino-messenger-protocol/grpc/go/cmp/services/book/v5/bookv5grpc"
	bookv5 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/services/book/v5"
	typesv4 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/types/v4"
	typesv5 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/types/v5"

	"github.com/chain4travel/camino-messenger-bot/v13/pkg/conversion"
	"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/common"
	"github.com/chain4travel/camino-messenger-bot/v13/pp-mock/handlers/state"
)

var _ bookv5grpc.ValidationServiceServer = (*validationV5Server)(nil)

type validationV5Server struct{}

func NewValidationServiceServer() bookv5grpc.ValidationServiceServer {
	return &validationV5Server{}
}

func (s *validationV5Server) Validation(_ context.Context, req *bookv5.ValidationRequest) (*bookv5.ValidationResponse, error) {
	if req.ValidationObject == nil ||
		req.ValidationObject.SearchResultIdentifier == nil ||
		req.ValidationObject.SearchResultIdentifier.SearchId == nil ||
		req.ValidationObject.SearchResultIdentifier.SearchId.Value == "" {
		return errValidationResp(typesv4.ErrorCode_ERROR_CODE_INVALID_IDENTIFIERS, "Invalid validation request: missing required validation identifier fields"), nil
	}

	searchID := req.ValidationObject.SearchResultIdentifier.SearchId.Value
	resultID := req.ValidationObject.SearchResultIdentifier.ResultId
	log.Printf("[book.v5.Validation] request searchId=%s resultId=%d", searchID, resultID)

	storedSearchData, found := state.GetStore().GetSearchResult(searchID)
	if !found {
		log.Printf("[book.v5.Validation] rejected: searchId=%s not found in state", searchID)
		return errValidationResp(typesv4.ErrorCode_ERROR_CODE_INVALID_IDENTIFIERS, "Invalid validation request: searchId not found in state"), nil
	}

	if resultID >= conversion.MustIntToUInt32(len(storedSearchData.Data.Prices)) {
		log.Printf("[book.v5.Validation] rejected: resultId=%d out of range (have %d prices) for searchId=%s",
			resultID, len(storedSearchData.Data.Prices), searchID)
		return errValidationResp(typesv4.ErrorCode_ERROR_CODE_INVALID_IDENTIFIERS, "Invalid validation request: resultId out of range"), nil
	}

	unifiedValidationPrice := storedSearchData.Data.Prices[resultID]
	log.Printf("[book.v5.Validation] searchId=%s resultId=%d -> verifiedPrice=%s (selected from %d search prices)",
		searchID, resultID, unifiedValidationPrice, len(storedSearchData.Data.Prices))

	resp := &bookv5.ValidationResponse{
		Response: &bookv5.ValidationResponse_SuccessResponse{
			SuccessResponse: &bookv5.ValidationSuccessResponse{
				Header:           common.SuccessHeaderV4(),
				ValidationId:     common.NewExpiringUUID(),
				ValidationObject: req.ValidationObject,
				TotalPrice: &typesv5.TotalPrice{
					Value: unifiedValidationPrice.ToPriceV5(),
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
	log.Printf("[book.v5.Validation] issued validationId=%s with verifiedPrice=%s", validationID, unifiedValidationPrice)

	return resp, nil
}

func errValidationResp(code typesv4.ErrorCode, message string) *bookv5.ValidationResponse {
	return &bookv5.ValidationResponse{
		Response: &bookv5.ValidationResponse_ErrorResponse{
			ErrorResponse: &bookv5.ValidationErrorResponse{
				Header: common.ErrorHeaderV4(code, message),
			},
		},
	}
}

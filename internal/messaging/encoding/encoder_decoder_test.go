// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package encoding

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"testing"

	"github.com/chain4travel/camino-messenger-bot/v13/internal/messaging/encryption"
	"github.com/chain4travel/camino-messenger-bot/v13/internal/messaging/message"
	"github.com/chain4travel/camino-messenger-bot/v13/internal/rpc/generated"
	"github.com/chain4travel/camino-messenger-bot/v13/pkg/metadata"

	pingv1 "buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go/cmp/services/ping/v1"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type dummySession struct{}

func (d *dummySession) Commit() error {
	return nil
}

func (d *dummySession) Abort() error {
	return nil
}

func TestEncodeDecodeV1(t *testing.T) {
	ctx := context.Background()
	storageSession := &dummySession{}

	// sender

	senderBotKey, err := ecdsa.GenerateKey(crypto.S256(), rand.Reader)
	require.NoError(t, err)
	senderBotAddress := crypto.PubkeyToAddress(senderBotKey.PublicKey)

	senderStorage := NewMockStorage(gomock.NewController(t))
	senderEncoderDecoder, err := NewEncoderDecoder(
		zap.NewNop().Sugar(),
		senderStorage,
		100,          // max chunk size
		senderBotKey, // key,
	)
	require.NoError(t, err)

	// recipient

	recipientBotKey, err := ecdsa.GenerateKey(crypto.S256(), rand.Reader)
	require.NoError(t, err)
	recipientBotAddress := crypto.PubkeyToAddress(recipientBotKey.PublicKey)

	recipientStorage := NewMockStorage(gomock.NewController(t))
	recipientEncoderDecoder, err := NewEncoderDecoder(
		zap.NewNop().Sugar(),
		recipientStorage,
		100,             // max chunk size
		recipientBotKey, // key,
	)
	require.NoError(t, err)

	// messages

	sharedKey, err := encryption.NewKey()
	require.NoError(t, err)

	requestID := "request-id"

	requestMessage := &message.Message{
		Type: generated.PingServiceV1Request,
		Content: &pingv1.PingRequest{
			PingMessage: "ping",
			Timestamp:   timestamppb.Now(),
		},
		RequestID:  requestID,
		Timestamps: metadata.Timestamps{"1": 100, "2": 200},
	}

	responseMessage := &message.Message{
		Type: generated.PingServiceV1Response,
		Content: &pingv1.PingResponse{
			PingMessage: "pong",
			Timestamp:   timestamppb.Now(),
		},
		RequestID:  requestID,
		Timestamps: metadata.Timestamps{"1": 100, "2": 200, "3": 300},
	}

	// sender encode request
	senderCMAccountAddress := crypto.PubkeyToAddress(senderBotKey.PublicKey)
	recipientCMAccountAddress := crypto.PubkeyToAddress(recipientBotKey.PublicKey)

	senderStorage.EXPECT().NewSession(ctx).Return(storageSession, nil)
	senderStorage.EXPECT().GetBotPubKey(ctx, storageSession, recipientBotAddress).Return(&recipientBotKey.PublicKey, nil)
	senderStorage.EXPECT().Abort(storageSession)

	encodedMessage, err := senderEncoderDecoder.EncodeMessage(
		ctx,
		requestMessage,
		recipientBotAddress,
		sharedKey,
		senderCMAccountAddress,
	)
	require.NoError(t, err)

	// recipient decode request

	recipientStorage.EXPECT().NewSession(ctx).Return(storageSession, nil)
	recipientStorage.EXPECT().SetBotPubKey(ctx, storageSession, senderBotAddress, &senderBotKey.PublicKey).Return(nil) // also adds to cache
	recipientStorage.EXPECT().Commit(storageSession).Return(nil)
	recipientStorage.EXPECT().Abort(storageSession)

	decodedMessage, sharedKey, decodedSenderCMAccountAddress, err := recipientEncoderDecoder.DecodeAndVerifyMessage( // we can't verify shared key here, because it is a new key
		ctx,
		encodedMessage,
		senderBotAddress,
	)
	require.NoError(t, err)
	require.Equal(t, senderCMAccountAddress, decodedSenderCMAccountAddress)
	require.True(t, proto.Equal(requestMessage.Content, decodedMessage.Content))
	proto.Reset(requestMessage.Content)
	proto.Reset(decodedMessage.Content)
	require.Equal(t, requestMessage, decodedMessage)

	// recipient encode response (shared key from received request)

	// getting senderBot pubKey from cache, no need to call storage

	encodedMessage, err = recipientEncoderDecoder.EncodeMessage(
		ctx,
		responseMessage,
		senderBotAddress,
		sharedKey,
		recipientCMAccountAddress,
	)
	require.NoError(t, err)

	// sender decode response

	senderStorage.EXPECT().NewSession(ctx).Return(storageSession, nil)
	senderStorage.EXPECT().SetBotPubKey(ctx, storageSession, recipientBotAddress, &recipientBotKey.PublicKey).Return(nil) // also adds to cache
	senderStorage.EXPECT().Commit(storageSession).Return(nil)
	senderStorage.EXPECT().Abort(storageSession)

	decodedMessage, decodedSharedKey, decodedRecipientCMAccountAddress, err := senderEncoderDecoder.DecodeAndVerifyMessage(
		ctx,
		encodedMessage,
		recipientBotAddress,
	)
	require.NoError(t, err)
	require.Equal(t, recipientCMAccountAddress, decodedRecipientCMAccountAddress)
	require.True(t, proto.Equal(responseMessage.Content, decodedMessage.Content))
	proto.Reset(responseMessage.Content)
	proto.Reset(decodedMessage.Content)
	require.Equal(t, responseMessage, decodedMessage)
	require.Equal(t, sharedKey, decodedSharedKey)
}

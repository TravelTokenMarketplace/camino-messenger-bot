// Copyright (C) 2022-2026, Chain4Travel AG. All rights reserved.
// See the file LICENSE for licensing terms.

package booking

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"time"

	cmaccounts "github.com/chain4travel/camino-messenger-bot/v13/pkg/cm_accounts"
	"github.com/chain4travel/camino-messenger-contracts/go/contracts/bookingtoken"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"go.uber.org/zap"
)

var (
	_ Service = (*service)(nil)

	// Special address that indicates BookingToken payment will be in native coin of
	// the network (CAM).
	NativePaymentToken = common.HexToAddress("0x0000000000000000000000000000000000000000")

	// Special address that indicates BookingToken payment will occur off-chain.
	ISOPaymentToken = common.HexToAddress("0x0000000000000000000000000000000000000001")

	errEmptyURI = fmt.Errorf("uri cannot be empty")
)

type Status uint8

const (
	StatusUnspecified Status = iota
	StatusReserved
	StatusReservationExpired
	StatusBought
	StatusCancelled
)

// Service provides minting and buying methods to interact with the CM Account contract.
type Service interface {
	// MintBookingToken mints a new booking token.
	// Parameters:
	// - reservedFor: The CM Account to reserve the token for.
	// - uri: The URI of the token.
	// - expirationTimestamp: Expiration timestamp for the token to be bought.
	// - price: Price of the token.
	// - paymentToken: Address of the payment token (ERC20), if address(0) then native.
	// - offChainPaymentCurrency: The currency to be used for off-chain payments.
	// - isCancellable: Indicates if the booking can be cancelled.
	// Returns the transaction receipt.
	MintBookingToken(
		ctx context.Context,
		reservedFor common.Address,
		uri string,
		expirationTimestamp *big.Int,
		price *big.Int,
		paymentToken common.Address,
		offChainPaymentCurrency *big.Int,
		isCancellable bool,
	) (*types.Receipt, *big.Int, error)

	// BuyBookingToken buys an existing reserved booking token.
	// Parameters:
	// - tokenID: ID of the token to buy.
	// - price: Price of the token.
	// - paymentToken: Address of the payment token (ERC20), if address(0) then native.
	// Returns the transaction receipt.
	BuyBookingToken(
		ctx context.Context,
		tokenID *big.Int,
		price *big.Int,
		paymentToken common.Address,
	) (*types.Receipt, error)

	// RecordExpiration records the expiration of a booking token on-chain.
	// Parameters:
	// - tokenID: ID of the token to record expiration for.
	// Returns the transaction receipt.
	RecordExpiration(
		ctx context.Context,
		tokenID *big.Int,
	) (*types.Receipt, error)

	// GetBookingStatus retrieves the booking status of a token.
	// Parameters:
	// - blockNumber: Block number to query the status at. If nil, the latest block is used.
	// - tokenID: ID of the token to get the status for.
	// Returns the booking status.
	GetBookingStatus(
		ctx context.Context,
		blockNumber *big.Int,
		tokenID *big.Int,
	) (Status, error)

	// IsBookingCancellable checks if a booking is cancellable.
	// Parameters:
	// - blockNumber: Block number to query the status at. If nil, the latest block is used.
	// - tokenID: ID of the token to get the status for.
	// Returns the booking status.
	IsBookingCancellable(
		ctx context.Context,
		blockNumber *big.Int,
		tokenID *big.Int,
	) (bool, error)

	// GetCancellationReasonsByTx retrieves cancellation reasons event from given tx.
	// Parameters:
	// - txHash: Hash of the BookingtokenCancellationReasons event transaction.
	GetCancellationReasonsEvent(
		ctx context.Context,
		txHash common.Hash,
	) (*bookingtoken.BookingtokenCancellationReasons, error)

	// GetCancellationReasons retrieves cancellation reasons from token cancellation proposal.
	// Parameters:
	// - blockNumber: Block number to query the reasons at. If nil, the latest block is used.
	// - tokenID: ID of the token to get the cancellation reasons from.
	GetCancellationReasons(
		ctx context.Context,
		blockNumber *big.Int,
		tokenID *big.Int,
	) (*CancellationReasons, error)

	// GetCancellationProposal retrieves cancellation proposal from token.
	// Parameters:
	// - blockNumber: Block number to query the proposal at. If nil, the latest block is used.
	// - tokenID: ID of the token to get the cancellation proposal from.
	GetCancellationProposal(
		ctx context.Context,
		blockNumber *big.Int,
		tokenID *big.Int,
	) (*CancellationProposal, error)
}

type service struct {
	logger                 *zap.SugaredLogger
	transactOpts           *bind.TransactOpts
	chainID                *big.Int
	minterCMAccountAddress common.Address
	cmAccounts             cmaccounts.Service
	bookingToken           *bookingtoken.Bookingtoken
	ethClient              *ethclient.Client

	tokenVisibleMaxAttempts int
	tokenVisibleRetryDelay  time.Duration
}

// NewService initializes a new Service. It sets up the transactor with the provided
// private key and creates the CMAccount contract.
func NewService(
	ethClient *ethclient.Client,
	bookingTokenAddress common.Address,
	minterCMAccountAddress common.Address,
	privateKey *ecdsa.PrivateKey,
	chainID *big.Int,
	logger *zap.SugaredLogger,
	cmAccounts cmaccounts.Service,
	tokenVisibleMaxAttempts int,
	tokenVisibleRetryDelay time.Duration,
) (Service, error) {
	bookingToken, err := bookingtoken.NewBookingtoken(bookingTokenAddress, ethClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create booking token contract binding: %w", err)
	}

	transactOpts, err := bind.NewKeyedTransactorWithChainID(privateKey, chainID)
	if err != nil {
		return nil, fmt.Errorf("failed to create transactor: %w", err)
	}

	// TODO @havan If we need in future, we can set additional options like gas limit, gas price, nonce, etc.
	// transactOpts.GasLimit = uint64(300000) // example gas limit
	// transactOpts.GasPrice = big.NewInt(20000000000) // example gas price

	return &service{
		logger:                  logger,
		transactOpts:            transactOpts,
		chainID:                 chainID,
		minterCMAccountAddress:  minterCMAccountAddress,
		cmAccounts:              cmAccounts,
		bookingToken:            bookingToken,
		ethClient:               ethClient,
		tokenVisibleMaxAttempts: tokenVisibleMaxAttempts,
		tokenVisibleRetryDelay:  tokenVisibleRetryDelay,
	}, nil
}

func (bs *service) MintBookingToken(
	ctx context.Context,
	reservedFor common.Address,
	uri string,
	expirationTimestamp *big.Int,
	price *big.Int,
	paymentToken common.Address,
	offChainPaymentCurrency *big.Int,
	isCancellable bool,
) (*types.Receipt, *big.Int, error) {
	bs.logger.Infof("📅 Minting BookingToken reservedFor=%s price=%s paymentToken=%s offChainCurrency=%s expiration=%s cmAccount=%s",
		reservedFor.Hex(), price.String(), paymentToken.Hex(), offChainPaymentCurrency.String(), expirationTimestamp.String(), bs.minterCMAccountAddress.Hex())

	// Validate URI
	// TODO: Should we have default tokenURI if no URI is provided?
	if strings.TrimSpace(uri) == "" {
		return nil, nil, errEmptyURI
	}

	receipt, err := bs.cmAccounts.MintBookingToken(
		ctx,
		bs.transactOpts,
		bs.minterCMAccountAddress,
		reservedFor,
		uri,
		expirationTimestamp,
		price,
		paymentToken,
		offChainPaymentCurrency,
		isCancellable,
	)
	if err != nil {
		// cmAccounts.MintBookingToken already prefixes "failed to mint booking token"
		// and decodes the custom revert; add call params instead of re-wrapping.
		return nil, nil, fmt.Errorf("mint reservedFor %s (price %s): %w", reservedFor.Hex(), price.String(), err)
	}

	for _, log := range receipt.Logs {
		if event, err := bs.bookingToken.ParseTokenReserved(*log); err == nil {
			bs.logger.Infof("[TokenReserved] TokenID: %s ReservedFor: %s Price: %s, PaymentToken: %s", event.TokenId, event.ReservedFor, event.Price, event.PaymentToken)
			return receipt, event.TokenId, nil
		}
	}

	return receipt, nil, fmt.Errorf("failed to parse TokenReserved event from tx receipt logs (txID: %s)", receipt.TxHash.Hex())
}

func (bs *service) BuyBookingToken(
	ctx context.Context,
	tokenID *big.Int,
	price *big.Int,
	paymentToken common.Address,
) (*types.Receipt, error) {
	bs.logger.Infof("🛒 Buying BookingToken TokenID=%s price=%s paymentToken=%s cmAccount=%s",
		tokenID.String(), price.String(), paymentToken.Hex(), bs.minterCMAccountAddress.Hex())

	// Validate tokenID
	if tokenID.Sign() < 0 {
		return nil, fmt.Errorf("tokenID must be a positive integer (>= 0)")
	}

	// Wait until the local RPC node has the minted token in its view.
	// Load-balanced RPC providers (e.g. drpc.org) may route the supplier's
	// WaitMined call and the distributor's subsequent eth_call to different
	// backend nodes; the node receiving the buy may not have synced the mint
	// block yet, making getReservationPrice return (0, 0x0) and causing an
	// IncorrectPrice revert.
	if err := bs.waitForTokenVisible(ctx, tokenID, price, paymentToken); err != nil {
		return nil, fmt.Errorf("buy tokenID %s: %w", tokenID, err)
	}

	// Call the BuyBookingToken function from the contract
	receipt, err := bs.cmAccounts.BuyBookingToken(ctx, bs.transactOpts, bs.minterCMAccountAddress, tokenID, price, paymentToken)
	if err != nil {
		// cmAccounts.BuyBookingToken already prefixes "failed to buy booking token"
		// and decodes the custom revert; add the call parameters as context here
		// instead of re-wrapping with the same phrase.
		return nil, fmt.Errorf("buy tokenID %s (price %s, paymentToken %s): %w", tokenID.String(), price.String(), paymentToken.Hex(), err)
	}

	return receipt, nil
}

// ErrReservationPriceMismatch is returned when the reserved token is present on
// the local RPC node but its reservation price/paymentToken differ from what we
// expected. A reservation is written once at mint (SafeMintWithReservation) and
// never updated, so this is a permanent condition — we fail fast rather than
// exhausting the retry budget.
type ErrReservationPriceMismatch struct {
	TokenID              *big.Int
	ExpectedPrice        *big.Int
	ExpectedPaymentToken common.Address
	ActualPrice          *big.Int
	ActualPaymentToken   common.Address
}

func (e *ErrReservationPriceMismatch) Error() string {
	return fmt.Sprintf(
		"reserved token %s has price %s (paymentToken %s) but expected %s (paymentToken %s)",
		e.TokenID, e.ActualPrice, e.ActualPaymentToken.Hex(),
		e.ExpectedPrice, e.ExpectedPaymentToken.Hex())
}

// ErrTokenNotVisible is returned when the reserved token never became visible on
// the local RPC node (getReservationPrice kept returning the (0, 0x0) sentinel
// or erroring) within the retry budget — i.e. sync lag that did not resolve.
type ErrTokenNotVisible struct {
	TokenID              *big.Int
	ExpectedPrice        *big.Int
	ExpectedPaymentToken common.Address
	Attempts             int
}

func (e *ErrTokenNotVisible) Error() string {
	return fmt.Sprintf(
		"token %s (price %s, paymentToken %s) not visible on local RPC node after %d attempts",
		e.TokenID, e.ExpectedPrice, e.ExpectedPaymentToken.Hex(), e.Attempts)
}

// reservationPriceReader reads a token's on-chain reservation price. It is
// satisfied by *bookingtoken.Bookingtoken and narrowed to this one method so
// the polling logic can be exercised with a fake in unit tests.
type reservationPriceReader interface {
	GetReservationPrice(opts *bind.CallOpts, tokenID *big.Int) (struct {
		Price        *big.Int
		PaymentToken common.Address
	}, error)
}

// waitForTokenVisible polls getReservationPrice until the reserved token is
// visible on the local RPC node with the expected price and payment token, then
// returns nil. It retries up to tokenVisibleMaxAttempts times with
// tokenVisibleRetryDelay between attempts (both configurable via the config file).
//
// This guards against split-brain reads when the write (mint) and read (buy)
// hit different backends of a load-balanced RPC provider such as drpc.org: the
// node handling the buy may lag behind the one that confirmed the mint, causing
// getReservationPrice to return the (0, 0x0) sentinel and the contract to revert
// with IncorrectPrice.
func (bs *service) waitForTokenVisible(
	ctx context.Context,
	tokenID *big.Int,
	expectedPrice *big.Int,
	expectedPaymentToken common.Address,
) error {
	return pollTokenVisible(
		ctx, bs.bookingToken, bs.logger,
		tokenID, expectedPrice, expectedPaymentToken,
		bs.tokenVisibleMaxAttempts, bs.tokenVisibleRetryDelay,
	)
}

// pollTokenVisible is the injectable core of waitForTokenVisible. attempts and
// delay are parameters so tests can drive it without real sleeps.
//
// Reservations are immutable after mint, so a backend can only differ from its
// peers in whether it has synced the token yet — never in the reservation's
// value. That lets us classify each read unambiguously:
//   - error, or the (0, 0x0) sentinel  -> not synced yet, retry
//   - present and matches expected     -> success
//   - present and differs from expected-> permanent mismatch, fail fast
//
// Note: a genuinely free native-coin reservation would itself be (0, 0x0) and is
// treated as "not synced" here; such an expectation would time out rather than
// match. In practice buy prices are non-zero, so this edge does not arise.
func pollTokenVisible(
	ctx context.Context,
	reader reservationPriceReader,
	logger *zap.SugaredLogger,
	tokenID *big.Int,
	expectedPrice *big.Int,
	expectedPaymentToken common.Address,
	attempts int,
	delay time.Duration,
) error {
	var zeroAddr common.Address
	for attempt := range attempts {
		reservation, err := reader.GetReservationPrice(
			&bind.CallOpts{Context: ctx}, tokenID)
		if err == nil {
			present := reservation.Price.Sign() != 0 || reservation.PaymentToken != zeroAddr
			if present {
				if reservation.Price.Cmp(expectedPrice) == 0 &&
					reservation.PaymentToken == expectedPaymentToken {
					if attempt > 0 {
						logger.Infof("waitForTokenVisible: token %s visible after %d retries", tokenID, attempt)
					}
					return nil
				}
				// Token is present but its immutable reservation differs — retrying
				// cannot change this, so surface a distinct mismatch error now.
				return &ErrReservationPriceMismatch{
					TokenID:              tokenID,
					ExpectedPrice:        expectedPrice,
					ExpectedPaymentToken: expectedPaymentToken,
					ActualPrice:          reservation.Price,
					ActualPaymentToken:   reservation.PaymentToken,
				}
			}
			logger.Debugf("waitForTokenVisible: attempt %d/%d: token %s not yet visible (0, 0x0)",
				attempt+1, attempts, tokenID)
		} else {
			logger.Debugf("waitForTokenVisible: attempt %d/%d: GetReservationPrice error: %v",
				attempt+1, attempts, err)
		}
		// No point sleeping after the final attempt — we're about to give up.
		if attempt == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	return &ErrTokenNotVisible{
		TokenID:              tokenID,
		ExpectedPrice:        expectedPrice,
		ExpectedPaymentToken: expectedPaymentToken,
		Attempts:             attempts,
	}
}

func (bs *service) RecordExpiration(
	ctx context.Context,
	tokenID *big.Int,
) (*types.Receipt, error) {
	bs.logger.Infof("📝 Recording expiration for BookingToken with TokenID %s", tokenID.String())

	// Validate tokenID
	if tokenID.Sign() < 0 {
		return nil, fmt.Errorf("tokenID must be a positive integer (>= 0)")
	}

	receipt, err := bs.cmAccounts.RecordExpiration(ctx, bs.transactOpts, bs.minterCMAccountAddress, tokenID)
	if err != nil {
		return nil, fmt.Errorf("failed to record token expiration: %w", err)
	}

	return receipt, nil
}

func (bs *service) GetBookingStatus(
	ctx context.Context,
	blockNumber *big.Int,
	tokenID *big.Int,
) (Status, error) {
	status, err := bs.bookingToken.GetBookingStatus(&bind.CallOpts{BlockNumber: blockNumber, Context: ctx}, tokenID)
	if err != nil {
		return StatusUnspecified, fmt.Errorf("failed to get booking status: %w", err)
	}
	return Status(status), nil
}

func (bs *service) IsBookingCancellable(
	ctx context.Context,
	blockNumber *big.Int,
	tokenID *big.Int,
) (bool, error) {
	isCancellable, err := bs.bookingToken.IsCancellable(&bind.CallOpts{BlockNumber: blockNumber, Context: ctx}, tokenID)
	if err != nil {
		return false, fmt.Errorf("failed to get booking cancellable status: %w", err)
	}
	return isCancellable, nil
}

func (bs *service) GetCancellationReasonsEvent(
	ctx context.Context,
	txHash common.Hash,
) (*bookingtoken.BookingtokenCancellationReasons, error) {
	txReceipt, err := bs.ethClient.TransactionReceipt(ctx, txHash)
	switch {
	case err != nil:
		return nil, fmt.Errorf("failed to get transaction receipt: %w", err)
	case txReceipt == nil || txReceipt.Status != types.ReceiptStatusSuccessful:
		return nil, fmt.Errorf("transaction receipt not found or failed for txHash: %s", txHash.Hex())
	}

	for _, log := range txReceipt.Logs {
		if event, err := bs.bookingToken.ParseCancellationReasons(*log); err == nil {
			return event, nil
		}
	}

	return nil, fmt.Errorf("failed to parse CancellationReasons event from tx receipt logs (txID: %s)", txHash.Hex())
}

type CancellationReasons struct {
	CancellationReason  uint16
	CancellationVersion uint16
	RejectionReason     uint16
	RejectionVersion    uint16
	CounterReason       uint16
	CounterVersion      uint16
	WithdrawalReason    uint16
	WithdrawalVersion   uint16
}

func (bs *service) GetCancellationReasons(
	ctx context.Context,
	blockNumber *big.Int,
	tokenID *big.Int,
) (*CancellationReasons, error) {
	reasons, err := bs.bookingToken.GetCancellationReasons(&bind.CallOpts{BlockNumber: blockNumber, Context: ctx}, tokenID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cancellation reasons: %w", err)
	}
	return (*CancellationReasons)(&reasons), nil
}

type CancellationProposalStatus uint8

const (
	CancellationProposalStatusNoProposal CancellationProposalStatus = iota
	CancellationProposalStatusPending
	CancellationProposalStatusRejected
	CancellationProposalStatusWithdrawn
	CancellationProposalStatusFinalized
)

type CancellationProposal struct {
	Status           CancellationProposalStatus
	RefundAmount     *big.Int
	InitialProposer  common.Address
	CurrentProposer  common.Address
	OwnerAccepted    bool
	SupplierAccepted bool
	TimesCountered   uint32
	TimesRejected    uint32
}

func (bs *service) GetCancellationProposal(
	ctx context.Context,
	blockNumber *big.Int,
	tokenID *big.Int,
) (*CancellationProposal, error) {
	status, refundAmount, initialProposer, currentProposer, ownerAccepted, supplierAccepted, timesCountered, timesRejected, err := bs.bookingToken.GetCancellationProposal(
		&bind.CallOpts{BlockNumber: blockNumber, Context: ctx},
		tokenID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get cancellation proposal: %w", err)
	}
	return &CancellationProposal{
		Status:           CancellationProposalStatus(status),
		RefundAmount:     refundAmount,
		InitialProposer:  initialProposer,
		CurrentProposer:  currentProposer,
		OwnerAccepted:    ownerAccepted,
		SupplierAccepted: supplierAccepted,
		TimesCountered:   timesCountered,
		TimesRejected:    timesRejected,
	}, nil
}

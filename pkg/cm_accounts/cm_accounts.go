// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package cmaccounts

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/chain4travel/camino-messenger-contracts/go/contracts/cmaccount"
	"github.com/chain4travel/camino-messenger-contracts/go/contracts/cmaccountmanager"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
)

const (
	// Implementation slot for ERC1967Proxy
	// See: https://eips.ethereum.org/EIPS/eip-1967#logic-contract-address
	managerCMAccountImplementationSlotString = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
	evmExecutionRevertErrorMessage           = "execution reverted"
)

var (
	_ Service = &service{}

	messengerBotRole                   = crypto.Keccak256Hash([]byte("MESSENGER_BOT_ROLE"))
	managerCMAccountImplementationSlot = common.HexToHash(managerCMAccountImplementationSlotString)

	ErrServiceNotSupported = errors.New("service is not supported")
)

type Service interface {
	GetAllMessengerBots(ctx context.Context, cmAccountAddress common.Address) ([]common.Address, error)
	IsBotAllowed(ctx context.Context, cmAccountAddress common.Address, botAddress common.Address) (bool, error)
	IsServiceSupported(ctx context.Context, cmAccountAddress common.Address, serviceFullName string) (bool, error)

	MintBookingToken(
		ctx context.Context,
		transactOpts *bind.TransactOpts,
		cmAccountAddress common.Address,
		reservedFor common.Address,
		uri string,
		expirationTimestamp *big.Int,
		price *big.Int,
		paymentToken common.Address,
		isoCurrency *big.Int,
		isCancellable bool,
	) (*types.Receipt, error)

	BuyBookingToken(
		ctx context.Context,
		transactOpts *bind.TransactOpts,
		cmAccountAddr common.Address,
		tokenID *big.Int,
		price *big.Int,
		paymentToken common.Address,
	) (*types.Receipt, error)

	RecordExpiration(
		ctx context.Context,
		transactOpts *bind.TransactOpts,
		cmAccountAddress common.Address,
		tokenID *big.Int,
	) (*types.Receipt, error)

	IsCMAccountImplementationUpToDate(ctx context.Context, cmAccountAddress common.Address) (bool, error)

	CMAccount(common.Address) (*cmaccount.Cmaccount, error)

	Cancellation
}

type botAuthCacheKey struct {
	cmAccount common.Address
	bot       common.Address
}

type botAuthCacheVal struct {
	allowed   bool
	expiresAt time.Time
}

type service struct {
	ethClient *ethclient.Client
	cache     *lru.Cache[common.Address, *cmaccount.Cmaccount]
	logger    *zap.SugaredLogger
	chainID   *big.Int

	botAuthCacheTimeout time.Duration
	botAuthCacheMu      sync.RWMutex
	botAuthCache        map[botAuthCacheKey]botAuthCacheVal
}

func NewService(
	ctx context.Context,
	logger *zap.SugaredLogger,
	cacheSize int,
	ethClient *ethclient.Client,
	botAuthCacheTimeout time.Duration,
) (Service, error) {
	chainID, err := ethClient.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}

	cache, err := lru.New[common.Address, *cmaccount.Cmaccount](cacheSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create cm account cache: %w", err)
	}

	return &service{
		ethClient:           ethClient,
		cache:               cache,
		logger:              logger,
		chainID:             chainID,
		botAuthCacheTimeout: botAuthCacheTimeout,
		botAuthCache:        make(map[botAuthCacheKey]botAuthCacheVal),
	}, nil
}

func (s *service) GetAllMessengerBots(ctx context.Context, cmAccountAddress common.Address) ([]common.Address, error) {
	cmAccount, err := s.CMAccount(cmAccountAddress)
	if err != nil {
		s.logger.Errorf("Failed to get cm account: %v", err)
		return nil, err
	}

	botsAddresses, err := cmAccount.GetRoleMembers(&bind.CallOpts{Context: ctx}, messengerBotRole)
	if err != nil {
		s.logger.Errorf("Failed to get messenger bots: %v", err)
		return nil, err
	}

	return botsAddresses, nil
}

func (s *service) IsBotAllowed(ctx context.Context, cmAccountAddress common.Address, botAddress common.Address) (bool, error) {
	key := botAuthCacheKey{cmAccount: cmAccountAddress, bot: botAddress}

	s.botAuthCacheMu.RLock()
	cached, found := s.botAuthCache[key]
	s.botAuthCacheMu.RUnlock()

	if found && time.Now().Before(cached.expiresAt) {
		return cached.allowed, nil
	}

	cmAccount, err := s.CMAccount(cmAccountAddress)
	if err != nil {
		s.logger.Errorf("Failed to get cm account: %v", err)
		return false, err
	}

	allowed, err := cmAccount.IsBotAllowed(&bind.CallOpts{Context: ctx}, botAddress)
	if err != nil {
		s.logger.Errorf("Failed to check if bot %s is allowed for CM account %s: %v", botAddress.Hex(), cmAccountAddress.Hex(), err)
		return false, fmt.Errorf("failed to check if bot is allowed: %w", err)
	}

	if s.botAuthCacheTimeout > 0 {
		s.botAuthCacheMu.Lock()
		s.botAuthCache[key] = botAuthCacheVal{
			allowed:   allowed,
			expiresAt: time.Now().Add(s.botAuthCacheTimeout),
		}
		s.botAuthCacheMu.Unlock()
	}

	return allowed, nil
}

func (s *service) IsServiceSupported(ctx context.Context, cmAccountAddress common.Address, serviceFullName string) (bool, error) {
	cmAccount, err := s.CMAccount(cmAccountAddress)
	if err != nil {
		s.logger.Errorf("Failed to get cm account: %v", err)
		return false, err
	}

	return cmAccount.IsServiceSupported(&bind.CallOpts{Context: ctx}, serviceFullName)
}

func (s *service) MintBookingToken(
	ctx context.Context,
	transactOpts *bind.TransactOpts,
	cmAccountAddress common.Address,
	reservedFor common.Address,
	uri string,
	expirationTimestamp *big.Int,
	price *big.Int,
	paymentToken common.Address,
	offChainPaymentCurrency *big.Int,
	isCancellable bool,
) (*types.Receipt, error) {
	cmAccount, err := s.CMAccount(cmAccountAddress)
	if err != nil {
		return nil, err
	}

	tx, err := cmAccount.MintBookingToken(
		transactOpts,
		reservedFor,
		uri,
		expirationTimestamp,
		price,
		paymentToken,
		offChainPaymentCurrency,
		isCancellable,
	)
	if err != nil {
		return nil, wrapTxErr("failed to mint booking token", err)
	}

	s.logger.Debugf("Waiting for MintBookingToken transaction to be mined...")

	receipt, err := bind.WaitMined(ctx, s.ethClient, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for MintBookingToken transaction to be mined: %w", err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("MintBookingToken transaction failed: %v", receipt)
	}

	s.logger.Infof("MintBookingToken transaction is mined. Block Nr: %s Gas used: %d", receipt.BlockNumber, receipt.GasUsed)

	return receipt, nil
}

func (s *service) BuyBookingToken(
	ctx context.Context,
	transactOpts *bind.TransactOpts,
	cmAccountAddress common.Address,
	tokenID *big.Int,
	price *big.Int,
	paymentToken common.Address,
) (*types.Receipt, error) {
	cmAccount, err := s.CMAccount(cmAccountAddress)
	if err != nil {
		return nil, err
	}

	tx, err := cmAccount.BuyBookingToken(
		transactOpts,
		tokenID,
		price,
		paymentToken,
	)
	if err != nil {
		return nil, wrapTxErr("failed to buy booking token", err)
	}

	s.logger.Debugf("Waiting for BuyBookingToken transaction to be mined...")

	receipt, err := bind.WaitMined(ctx, s.ethClient, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for BuyBookingToken transaction to be mined: %w", err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("BuyBookingToken transaction failed: %v", receipt)
	}

	s.logger.Infof("BuyBookingToken transaction is mined. Block Nr: %s Gas used: %d", receipt.BlockNumber, receipt.GasUsed)

	return receipt, nil
}

func (s *service) CMAccount(cmAccountAddr common.Address) (*cmaccount.Cmaccount, error) {
	cmAccount, ok := s.cache.Get(cmAccountAddr)
	if ok {
		return cmAccount, nil
	}

	cmaccount, err := cmaccount.NewCmaccount(cmAccountAddr, s.ethClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create cm account contract binding: %w", err)
	}
	s.cache.Add(cmAccountAddr, cmaccount)

	return cmaccount, nil
}

func (s *service) RecordExpiration(
	ctx context.Context,
	transactOpts *bind.TransactOpts,
	cmAccountAddress common.Address,
	tokenID *big.Int,
) (*types.Receipt, error) {
	cmAccount, err := s.CMAccount(cmAccountAddress)
	if err != nil {
		return nil, err
	}

	tx, err := cmAccount.RecordExpiration(transactOpts, tokenID)
	if err != nil {
		return nil, wrapTxErr("failed to record expiration", err)
	}

	s.logger.Debugf("Waiting for RecordExpiration transaction to be mined...")

	receipt, err := bind.WaitMined(ctx, s.ethClient, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for RecordExpiration transaction to be mined: %w", err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("transaction failed: %v", receipt)
	}

	s.logger.Infof("RecordExpiration transaction is mined. Block Nr: %s Gas used: %d", receipt.BlockNumber, receipt.GasUsed)

	return receipt, nil
}

func (s *service) getLatestCMAccountImplementation(ctx context.Context, cmAccountAddress common.Address) (common.Address, error) {
	cmAccount, err := s.CMAccount(cmAccountAddress)
	if err != nil {
		return common.Address{}, err
	}
	managerAddress, err := cmAccount.GetManagerAddress(&bind.CallOpts{Context: ctx})
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to fetch CM account manager address: %w", err)
	}
	manager, err := cmaccountmanager.NewCmaccountmanager(managerAddress, s.ethClient)
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to create CM account manager contract binding: %w", err)
	}
	currentImplOnManager, err := manager.GetAccountImplementation(&bind.CallOpts{Context: ctx})
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to get account implementation: %w", err)
	}
	return currentImplOnManager, nil
}

func (s *service) getCurrentImplementationOnProxy(ctx context.Context, cmAccountAddress common.Address) (common.Address, error) {
	implAddressSlotValue, err := s.ethClient.StorageAt(ctx, cmAccountAddress, managerCMAccountImplementationSlot, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to get implementation address from proxy: %w", err)
	}
	return slotValueToAddress(implAddressSlotValue)
}

func (s *service) IsCMAccountImplementationUpToDate(ctx context.Context, cmAccountAddress common.Address) (bool, error) {
	currentImplOnManager, err := s.getLatestCMAccountImplementation(ctx, cmAccountAddress)
	if err != nil {
		return false, fmt.Errorf("failed to get latest cm account implementation from cm account manager: %w", err)
	}

	currentImplOnProxy, err := s.getCurrentImplementationOnProxy(ctx, cmAccountAddress)
	if err != nil {
		return false, fmt.Errorf("failed to get current cm account implementation implementation: %w", err)
	}
	s.logger.Info("📜 CM Account Implementation:")
	s.logger.Info("   - Active:  " + currentImplOnProxy.Hex())
	s.logger.Info("   - Latest:  " + currentImplOnManager.Hex())

	return currentImplOnManager == currentImplOnProxy, nil
}

func slotValueToAddress(slotValue []byte) (common.Address, error) {
	if len(slotValue) < 32 {
		return common.Address{}, fmt.Errorf("slot value storage read returned unexpected size: %d", len(slotValue))
	}
	// We take the last 20 bytes because Ethereum addresses are 20 bytes (40 hex chars) but storage slots are 32 bytes
	// The address is right-aligned in the 32 byte slot
	return common.BytesToAddress(slotValue[12:]), nil
}

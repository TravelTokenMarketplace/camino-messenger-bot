// Copyright (C) 2022-2026, Chain4Travel AG. All rights reserved.
// See the file LICENSE for licensing terms.

package config

import (
	"crypto/ecdsa"
	"encoding/hex"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// ******* Parsed config *******
//
//

type Config struct {
	DeveloperMode bool
	E2ETestMode   bool

	BotKey           *ecdsa.PrivateKey
	CMAccountAddress common.Address

	ChainRPCURL         string
	BookingTokenAddress common.Address

	BotAuthCacheTimeout time.Duration

	ResponseTimeout time.Duration

	RecordExpiration bool

	TokenVisibleMaxAttempts int
	TokenVisibleRetryDelay  time.Duration

	RPCServer     RPCServerConfig
	PartnerPlugin PartnerPluginConfig
	DB            SQLiteDBConfig
	Matrix        MatrixConfig
}

type SQLiteDBConfig struct {
	Common                 UnparsedSQLiteDBConfig
	EventListener          UnparsedSQLiteDBConfig
	MessagesEncoderDecoder UnparsedSQLiteDBConfig
	Resolver               UnparsedSQLiteDBConfig
}

// ******* Common *******
//
//

type PartnerPluginConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	Host        string `mapstructure:"host"`
	Unencrypted bool   `mapstructure:"unencrypted"`
	CACertFile  string `mapstructure:"ca_file"`
}

type MatrixConfig struct {
	Host  string `mapstructure:"host"`
	Store string
}

type RPCServerConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	Port           uint64 `mapstructure:"port"`
	Unencrypted    bool   `mapstructure:"unencrypted"`
	ServerCertFile string `mapstructure:"cert_file"`
	ServerKeyFile  string `mapstructure:"key_file"`
}

// ******* Unparsed config *******
//
//

type UnparsedConfig struct {
	DeveloperMode bool `mapstructure:"developer_mode"`
	E2ETestMode   bool `mapstructure:"e2e_test_mode"`

	BotKey           string `mapstructure:"bot_key"`
	CMAccountAddress string `mapstructure:"cm_account_address"`

	ChainRPCURL         string `mapstructure:"chain_rpc_url"`
	BookingTokenAddress string `mapstructure:"booking_token_address"`

	BotAuthCacheTimeout int64 `mapstructure:"bot_auth_cache_timeout"` // seconds

	ResponseTimeout int64 `mapstructure:"response_timeout"` // milliseconds

	RecordExpiration bool `mapstructure:"record_expiration"`

	TokenVisibleMaxAttempts int64 `mapstructure:"token_visible_max_attempts"`
	TokenVisibleRetryDelay  int64 `mapstructure:"token_visible_retry_delay"` // milliseconds

	PartnerPlugin PartnerPluginConfig `mapstructure:"partner_plugin"`
	RPCServer     RPCServerConfig     `mapstructure:"rpc_server"`

	Matrix UnparsedMatrixConfig   `mapstructure:"matrix"`
	DB     UnparsedSQLiteDBConfig `mapstructure:"db"`
}

type UnparsedSQLiteDBConfig struct {
	DBPath string `mapstructure:"path"`
}

type UnparsedMatrixConfig struct {
	Host string `mapstructure:"host"`
}

func (cfg *Config) unparse() *UnparsedConfig {
	return &UnparsedConfig{
		DB:            cfg.DB.Common,
		RPCServer:     cfg.RPCServer,
		PartnerPlugin: cfg.PartnerPlugin,
		Matrix: UnparsedMatrixConfig{
			Host: cfg.Matrix.Host,
		},
		DeveloperMode:       cfg.DeveloperMode,
		E2ETestMode:         cfg.E2ETestMode,
		BotKey:              hex.EncodeToString(crypto.FromECDSA(cfg.BotKey)),
		CMAccountAddress:    cfg.CMAccountAddress.Hex(),
		ChainRPCURL:         cfg.ChainRPCURL,
		BookingTokenAddress: cfg.BookingTokenAddress.Hex(),
		BotAuthCacheTimeout: int64(cfg.BotAuthCacheTimeout / time.Second),
		ResponseTimeout:     int64(cfg.ResponseTimeout / time.Millisecond),
		RecordExpiration:    cfg.RecordExpiration,

		TokenVisibleMaxAttempts: int64(cfg.TokenVisibleMaxAttempts),
		TokenVisibleRetryDelay:  int64(cfg.TokenVisibleRetryDelay / time.Millisecond),
	}
}

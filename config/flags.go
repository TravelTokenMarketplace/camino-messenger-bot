// Copyright (C) 2022-2026, Chain4Travel AG. All rights reserved.
// See the file LICENSE for licensing terms.

package config

import (
	"github.com/spf13/pflag"
)

const (
	flagKeyConfig = "config"

	flagKeyDeveloperMode = "developer_mode"
)

func Flags() *pflag.FlagSet {
	flags := pflag.NewFlagSet("config", pflag.ExitOnError)

	flags.String(flagKeyConfig, "camino-messenger-bot.yaml", "path to config file")

	// Main config flags
	flags.Bool(flagKeyDeveloperMode, false, "Sets developer mode.")
	flags.Bool("e2e_test_mode", false, "Sets e2e test mode adjusting limits and (expiration-)timeouts. DO NOT USE IN PRODUCTION: This mode will fail to work as the deployed contracts are enforcing the restrictions.")
	flags.String("bot_key", "", "Sets bot private key. Used for the Matrix server connection and CM account interaction.")
	flags.String("cm_account_address", "", "Sets bot cm account address.")
	flags.String("chain_rpc_url", "", "chain RPC URL.")
	flags.String("booking_token_address", "0x459EEdD4bE13bD7D1Af27DA5DdA6d69407118C83", "BookingToken address.")
	flags.Int64("bot_auth_cache_timeout", 300, "Duration in seconds to cache bot authorizations.")
	flags.Int64("response_timeout", 3000, "The messenger timeout (in milliseconds).")

	// DB config flags
	flags.String("db.path", "cmb-db", "Path to database dir.")

	// Partner plugin config flags
	flags.Bool("partner_plugin.enabled", false, "Enable or disable the partner plugin rpc client. It must be enabled if bot's cm account supports at least one service.")
	flags.String("partner_plugin.host", "localhost:50051", "partner plugin RPC server host.")
	flags.Bool("partner_plugin.unencrypted", false, "Whether the RPC client should initiate an unencrypted connection with the server.")
	flags.String("partner_plugin.ca_file", "", "The partner plugin RPC server CA certificate file.")

	// RPC server config flags
	flags.Bool("rpc_server.enabled", false, "Enable or disable RPC server. It must be enabled if bot is expecting to receive RPC requests (e.g. its distributor bot).")
	flags.Uint64("rpc_server.port", 9090, "The RPC server port.")
	flags.Bool("rpc_server.unencrypted", false, "Whether the RPC server should be unencrypted.")
	flags.String("rpc_server.cert_file", "", "The server certificate file.")
	flags.String("rpc_server.key_file", "", "The server key file.")

	// Matrix config flags
	flags.String("matrix.host", "", "Sets the matrix host.")

	// Record expiration config flags
	flags.Bool("record_expiration", true, "Whether to record token expiration on chain.")

	return flags
}

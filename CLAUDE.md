# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Camino-Messenger-Bot is a Go service that brokers travel-service requests (search, booking, minting, cancellation) between trading partners. It bridges three worlds:

- **Matrix** — encrypted peer-to-peer transport between bots (suppliers and distributors).
- **gRPC + protobuf** — the request/response API surface, defined by the external [camino-messenger-protocol](https://github.com/TravelTokenMarketplace/camino-messenger-protocol) (currently `release-13`). The bot acts as a gRPC **server** for incoming partner requests and a gRPC **client** to its own "partner-plugin" middleware (which integrates the partner's backend and is NOT part of this repo).
- **EVM blockchain** (Base Sepolia testnet / Camino mainnet) — CM Accounts, ERC20 payments, and the BookingToken NFT contract, accessed via go-ethereum.

A bot runs in one of two roles, selected purely by config: **supplier** (seller) or **distributor** (buyer).

## Prerequisites

- **Go >= 1.25** (see `go.mod`; minimum is duplicated in `scripts/build.sh`, `Dockerfile`, and README — update all four together).
- **libolm** C library is required at build AND runtime (Matrix E2E encryption via the `camino-matrix-go` git submodule). Install via `apt install libolm-dev` (Linux) or `brew install libolm` (macOS Apple Silicon only; Intel Macs and Windows unsupported).
- Initialize the submodule: `git submodule update --init --recursive`.
- On macOS, set CGO paths before building: `set -a && source homebrew.env && set +a` (or export `CGO_CFLAGS`/`CGO_LDFLAGS` per README), and install GNU grep (`brew install grep`) because build scripts need `grep -P`.

## Common Commands

There is no Makefile — all tooling lives in `scripts/`.

```bash
./scripts/build.sh                # build ./build/camino-messenger-bot (use -d for debug symbols)
./scripts/build_test.sh           # build + run full unit test suite with -race and coverage.out
./scripts/lint.sh                 # golangci-lint (pinned v2.7.1) + license-header check
./scripts/fmt.sh                  # gofmt -w over the tree
./scripts/build_partner_plugin_mock.sh   # build ./build/pp-mock
```

Running tests directly (what `build_test.sh` wraps):

```bash
go test -shuffle=on -race -timeout=120s ./...        # all unit tests
go test -race ./internal/messaging/...               # a single package
go test -race -run TestName ./internal/messaging/    # a single test
```

Run the bot (config is mandatory; pick supplier or distributor):

```bash
./build/camino-messenger-bot --config supplier-config.yaml
./build/camino-messenger-bot --developer_mode true --config supplier-config.yaml   # extra dev logging/behavior
# Mock partner plugin (entry point is pp-mock/main.go). Port comes ONLY from the
# env var below (default 50051); pp-mock parses no CLI flags.
CMB_PARTNER_PLUGIN_MOCK_PORT=50051 go run ./pp-mock
```

A typical local Base Sepolia loop runs three processes together: a supplier bot, a distributor bot (each with its own CM Account + config + SQLite db dir), and the pp-mock partner plugin on the port referenced by the supplier's `partner_plugin_port`.

E2E tests are heavyweight — they download/build caminogo, camino-conduit, and the matrix-app-service, then spin up a real network:

```bash
./scripts/e2e.sh                          # full e2e run (see tests/e2e/README.MD)
./scripts/e2e.sh --filter <pattern> --debug --clean
```

## Code Generation

Three categories of generated code; regenerate after touching their sources/templates rather than hand-editing output.

- **gRPC service handlers** (`internal/rpc/generated/`): produced by `./scripts/generate_grpc_service_handlers.sh` from the `templates/` directory + the protocol's buf SDK. This script generates a per-service client/server pair for every protocol service, plus `register_*_services.go` and `unmarshal.go`. A blacklist in the script excludes some services (partner, network, claim, notification, insurance).
- **protobuf** (`proto/pb/`): `./scripts/protobuf_codegen.sh` (requires `buf` 1.47.2, `protoc-gen-go` v1.30.0). The bot's own proto is just `proto/readiness/`; the bulk of message types come from the external protocol via the buf registry.
- **mocks**: `./scripts/mock.gen.sh` (uber `mockgen` v0.4.0) driven by `scripts/mocks.mockgen.txt` (interface mode) and `scripts/mocks.mockgen.source.txt` (source mode). `mock_*.go` files throughout the tree are generated.

## Architecture

`main.go` → `cmd/` (cobra root command, config + logger setup) → `internal/app/app.go`. **`internal/app/app.go` is the composition root** — `NewApp` wires every component together and `App.Run` starts them as a coordinated `errgroup` with explicit startup ordering (event listener + message processor → matrix messenger → gRPC server). Read it first to understand how the pieces connect.

### Request flow

Two directions converge on the **message processor** (`internal/messaging/processor.go`):

1. **Inbound gRPC** (`internal/rpc/server`) — a partner calls this bot's gRPC server. The processor routes via the **service registry** (`internal/messaging/service_registry.go`, which validates that services are actually registered on-chain for the CM Account), encodes/encrypts the request, and forwards it over Matrix to the counterparty bot.
2. **Inbound Matrix** — the matrix messenger (`internal/matrix/messenger`) receives a message, the processor decodes it and dispatches to the **partner plugin** gRPC client (`internal/partnerplugin`, `internal/rpc/client`), which talks to the partner's backend middleware. The **response handler** (`internal/messaging/response_handler.go`) post-processes responses — notably price computation and BookingToken minting.

### Key subsystems

- **Messaging encoding** (`internal/messaging/encoding`): serializes, compresses, encrypts, and **chunks** large messages (Matrix has a max message size — see `matrix_client.MaxChunkSize`). Backed by a SQLite store for reassembly state. `mint.go` / `mint_v3.go` / `mint_v4.go` / `mint_v5.go` handle version-specific mint request handling.
- **Blockchain (`pkg/`)**: `pkg/cm_accounts` (CM Account contract interaction + caching + bot authorization), `pkg/booking` (BookingToken minting), `pkg/erc20` (token metadata/decimals), `pkg/price` & `internal/price` (price conversion using ERC20 decimals). The `internal/eventlistener` subscribes to BookingToken on-chain events and reacts (e.g. buying/minting), persisting cursor state in SQLite.
- **Cancellation** (`internal/cancellation/v1|v2|v3`): version-specific cancellation services, registered separately on the gRPC server.
- **Resolver** (`internal/resolver`): resolves CM Account addresses to Matrix user IDs (a bot's Matrix user ID is derived deterministically from its EVM address — see `pkg/matrix.UserIDFromAddress`).
- **Scheduler** (`pkg/scheduler`): periodic jobs, e.g. the `cash_in` job.

Most stateful subsystems (event listener, encoder/decoder, resolver) persist to **SQLite** at paths configured under `cfg.DB.*`.

### Configuration

`config/` reads layered config via `config_reader.go` (cobra flags in `flags.go` + YAML file). Example configs for each network/role live at repo root (`camino-messenger-bot-{supplier,distributor}-{base-sepolia,camino}.yaml`). Critical fields: `bot_key` (bot wallet private key — must be funded for mint transactions), `cm_account_address`, role-determining settings, and the `partner_plugin` section (disabling it leaves the gRPC client nil). The bot requires a configured **CM Account** with the bot registered and services added on-chain (done via the contracts repo's hardhat tooling — see README "Camino Messenger Account" section). Restart the bot after adding/removing on-chain services.

## Conventions

- Module path is versioned: `github.com/chain4travel/camino-messenger-bot/v13`. Imports use the `/v13` suffix.
- All `.go` files require the Camino license header (`header.yaml`); `lint.sh` enforces it via `camino-license`.
- Lint config `.golangci.yml` denies `io/ioutil` (use `io`/`os`), `testify/assert` (use `require`), and `golang/mock` (use `go.uber.org/mock`). It builds with the `e2e` tag.
- Response metadata carries a `timestamps` tracing map (`pkg/metadata`) recording each hop of a request for latency debugging — see README "Tracing".

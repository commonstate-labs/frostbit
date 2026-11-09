# FrostBit DEX

FrostBit DEX is an experimental Go blockchain node with a DEX execution
engine, checkpoint certificates, FROST vault workflows, an Explorer, and a
browser wallet.

> Security notice: this code has not been represented as production-audited.
> Do not place funds at risk until the protocol, deployment, and key-management
> setup have received an independent review. Never reuse identities from an
> older repository, build artifact, or test network.

## Identity and network model

- `peers.json` is a public manifest containing the immutable genesis trust set
  and initial bootstrap routes. It contains no private-key field, even when the
  value would be empty.
- A node reads its local hot key only from an inherited file descriptor. The
  key derives the node/miner address; there is no user-configured miner index.
- The verified StateDB liveness registry is the current Active/Probation miner
  set. Bootstrap and discovery responses cannot grant consensus authority.
- `miner_address` is the hot signing identity and owns its locked stake.
  `reward_address` is the fixed destination for rewards and the post-slash
  principal released after the exit delay.
- One process runs one identity. Multi-node networks are coordinated as
  separate processes or containers with isolated secret mounts.

## Development

Requirements: Go 1.24 and Node.js 22.

```sh
go test ./...
npm --prefix explorer ci
npm --prefix explorer run type-check
npm --prefix wallet ci
npm --prefix wallet test
```

The legacy private workspace may contain ignored diagnostic programs under
`tmp/`; that directory is deliberately absent from the public export.

## Create fresh network identities

Choose a new chain ID and generate identities into the ignored `.local/`
directory:

```sh
go run ./cmd/network-init \
  --chain-id 10001 \
  --validators 4 \
  --advertise-host 127.0.0.1 \
  --base-port 6000 \
  --output-dir .local/network
```

The command atomically writes a complete operator-local runtime directory:
`config.json`, `genesis.json`, `frost_default.json`, `witness_default.json`,
the official public `peers.json`, and separate `secrets/<address>.key` files.
It never prints key bytes, refuses to overwrite an existing directory, and
validates the complete output before making it visible. FROST and Witness are
disabled until an operator deliberately supplies audited custody settings.

On Linux, start one identity by opening its secret as FD 3:

```sh
exec 3<.local/network/secrets/<address>.key
go run ./cmd/main \
  --bootstrap-mode=genesis \
  --config-dir=.local/network \
  --expected-address=<address> \
  --data-dir=.local/data/<address> \
  --advertise-addr=127.0.0.1:6000 \
  --private-key-fd=3 \
  --no-simulator
```

For containers and production-style secret mounts, see
[`deploy/PUBLICATION.md`](deploy/PUBLICATION.md) and the templates under
`deploy/`.

## Dynamic miner registration

An already synchronized address can submit a complete miner registration with
`cmd/miner-register`. Registration binds its canonical secp256k1 key, BLS key
and proof of possession, endpoint, fixed reward address, and stake in one
transaction. The chain assigns the internal miner slot and starts the miner in
Probation.

For an existing network, the release bundle's `frostbit-node` command provides
the guarded quick path without a source checkout. `up` pulls the
release-published image by immutable digest, validates the five-file network
bundle, derives the miner address, selects an unambiguous public IPv4 endpoint,
and starts late-join synchronization. After manually verifying synchronization,
same-height block ID agreement, and external TCP/UDP reachability, run
`register --confirm-ready`. Registration journals the signed transaction before
submission and only permits exact-raw resubmission after an unknown outcome.

## Verify the public source

Run the public verification entrypoint before submitting a change or building
a release:

```sh
bash scripts/verify-public.sh
```

PowerShell users can run
`powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-public.ps1`.
The checks build the node and Explorer, run the public test and vet gates,
validate generated protobuf output, and reject secret-bearing public config.
Then follow [`deploy/PUBLICATION.md`](deploy/PUBLICATION.md).

## License

Except where a file states otherwise, project-authored source code is licensed
under the [GNU General Public License version 3 only](LICENSE)
(`GPL-3.0-only`). The Solidity sources that carry an MIT SPDX header remain
under the [MIT License](solidity/LICENSE.md). Third-party components retain
their respective licenses; see [NOTICE](NOTICE) for the distribution boundary.

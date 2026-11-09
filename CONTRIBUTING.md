# Contributing

Contributions should keep consensus behavior deterministic and preserve the
public/private boundary.

Before submitting a change:

```sh
bash scripts/verify-public.sh
```

PowerShell users can run
`powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-public.ps1`.
These public checks do not depend on private publication, pre-commit, local
network, or operator automation.

Never commit credentials, deployment inventories, private endpoints, account
seeds, generated node keys, databases, logs, profiles, or build artifacts.
`peers.json` is strict public JSON: example status belongs in `_notice`, while
the official filename remains unchanged and secret-bearing fields are removed
entirely.

Protocol changes that affect protobuf wire data, state keys, transaction
digests, certificate digests, or consensus membership must include migration
or clean-break rationale plus deterministic tests. Generated Go and wallet
protobuf files must be updated together with `pb/data.proto`.

## Contribution license

By submitting a contribution, you confirm that you have the right to submit it
and agree that it is licensed under `GPL-3.0-only`, matching the license of the
project-authored code it modifies. A contribution to a file that carries a
different explicit license notice remains under that file's stated license.

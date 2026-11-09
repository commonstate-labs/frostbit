# Public deployment boundary

The public deployment templates are `docker-compose.*.yml`, `k8s/`,
`container-node-entrypoint.sh`, and `secure-node/`. They must contain public
configuration and secret *paths* only. Runtime key bytes belong in an external
secret manager or a node-specific, read-only mount.

`cmd/network-init` writes generated manifests and node secrets under
`.local/network` by default. The whole `.local/` tree is excluded from both Git
and Docker build contexts; treat it as disposable operator-local state, never
as release input.

Combined Windows node releases are disabled until a named-pipe or inherited-
handle secret launcher exists. Do not restore plaintext argv, environment, or
repository-file fallbacks to make a Windows bundle start. Package a Windows
Explorer separately if one is needed.

`deploy/physical20/` is a private lab inventory, not a distributable template.
It contains machine labels and network topology that identify an operator even
when no cryptographic key is present. The directory is excluded by
`.gitignore`, `.dockerignore`, and recursive `.gitattributes` (`export-ignore`). Do not
copy it into a newly initialized public repository.

These ignore rules do not remove files that are already tracked or erase
earlier commits. They are not a security boundary. Release maintainers must
publish from a clean, reviewed source inventory and rotate any credential that
has ever been committed; deleting the current file is not credential
revocation.

## Published repository boundary

The public checkout is self-verifying and must not depend on private release or
operator tooling. Run:

```sh
bash scripts/verify-public.sh
```

On PowerShell, run
`powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify-public.ps1`.
Both entrypoints build the public executables, run the public test and vet
gates, validate generated protobuf output, and reject secret-bearing public
configuration.

Include:

- Go source and tests under the runtime packages, `go.mod`, and `go.sum`.
- `pb/data.proto` plus its generated Go and wallet JavaScript outputs.
- Explorer and wallet source, manifests, package files, and lockfiles; omit
  `node_modules` and built `dist` directories.
- Audited Solidity contracts, scripts, tests, and lockfile; omit every `.env`,
  Hardhat cache, artifact, deployment-output, and local account file.
- The strict public config profiles, Docker/release files, public deployment
  templates, reviewed build/verification scripts, the public CI workflow, and
  the root public documentation.
- The root `LICENSE` containing GPLv3, the root `NOTICE`, and the MIT license
  retained by the explicitly MIT-licensed Solidity sources.

Exclude:

- The old `.git` directory, tags, remotes, reflogs, release archives, images,
  binaries, databases, caches, logs, profiles, coverage, and temporary tools.
- `.agent`, `.agents`, `.claude`, `.idea`, `.vscode`, `AGENTS.md`, `CLAUDE.md`,
  `$profile`, internal task/status reports, and private design discussions.
- `deploy/physical20`, host inventories, SSH material, real IP/MAC/topology
  data, and every operator-specific configuration.
- Every key/certificate/keystore/seed/environment file and all generated
  material under `.local/`.
- Private source exporters, publication guards, pre-commit gates, local-network
  automation, full lab diagnostics, and operator deployment pipelines.

The root `config/frost_default.json` is deliberately disabled. Operators must
build a reviewed runtime profile before enabling FROST; public placeholder vault
references are not an authorization to run custody services.

Publish only after the reviewed tree passes the public verification entrypoint.
For an anonymous repository, configure a new non-identifying author name/email
and remote; do not import private commits, tags, signing keys, release metadata,
or package provenance.

Every published binary, container image, or archive must preserve `LICENSE`
and `NOTICE` and make the exact Corresponding Source for that build available
under `GPL-3.0-only`. Third-party license notices remain mandatory and are not
replaced by the project license.

# Security policy

## Reporting a vulnerability

Use the repository host's private vulnerability-reporting feature. Do not put
exploit details, private keys, seed phrases, credentials, or identifying
infrastructure data in a public issue.

Until a private reporting channel is enabled on the new anonymous repository,
do not publish the repository as accepting security reports. The project does
not currently advertise a personal email, chat account, or operator identity.

## Funds and key safety

- Example and generated development identities are disposable and must never
  custody valuable funds.
- A key that appeared in an old commit, archive, image, log, or build context
  is compromised even if the current file was later deleted.
- Node secrets must be injected at runtime from a secret manager or isolated
  read-only file descriptor; they must not appear in argv, environment values,
  `peers.json`, release archives, or container layers.
- Reports involving possible key exposure should first move funds and rotate
  the affected identity. Repository cleanup is not revocation.

## Supported versions

Only the current default branch is considered for security fixes while the
project remains pre-release.

# Kubernetes runtime secrets

The manifests reference public configuration and one Secret per node identity;
they deliberately do not define either resource inline. Create them from files
held outside the repository before applying the workload:

```sh
export DEX_MINER_ADDRESS='replace-with-canonical-fbc-miner-address'

kubectl -n dex-prod create configmap dex-prod-public-config \
  --from-file=/secure-import/network-release/config/runtime

kubectl -n dex-prod create secret generic dex-miner-address-secrets \
  --from-file=private_key="/secure-import/miners/$DEX_MINER_ADDRESS/private_key" \
  --from-file=frost_wrap_key="/secure-import/miners/$DEX_MINER_ADDRESS/frost_wrap_key"
```

Use the corresponding `dex-test-public-config` name in the test namespace.
The public ConfigMap must be the network-published `config/runtime` directory,
not the repository's intentionally empty `config/env/prod` template. It must not
contain private-key, wrap-key, mnemonic, or token fields. Replace the
`DEX_MINER_ADDRESS` value in the workload manifest with the same canonical
address before applying it.

The container entrypoint opens the mounted files as inherited descriptors and
adds only `--private-key-fd` / `--frost-wrap-key-fd` to the node argv. Secret
bytes never become environment-variable values or command-line arguments.

Each manifest intentionally runs one node replica. Do not scale a StatefulSet
while it references a single Secret: create a distinct workload, address-bound
Secret name, and address-named persistent data directory for each miner
identity, or use a secrets-store CSI provider that selects a secret by identity.

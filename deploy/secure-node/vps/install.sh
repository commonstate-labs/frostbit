#!/bin/sh
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
umask 077

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

usage() {
    cat >&2 <<'EOF'
Usage:
  sudo ./install.sh --miner-address fbc1... --binary FILE --config-dir DIR \
    --trusted-host HOST --known-hosts-file FILE \
    [--trusted-port 22] [--advertise-addr HOST:PORT] \
    [--bootstrap-mode genesis|latejoin]

The installer creates (but never replaces) one ed25519 secret-fetch key and
prints its public half. It installs the service but does not enable or start it.
EOF
    exit 64
}

die() {
    printf 'VPS install: %s\n' "$1" >&2
    exit 1
}

require_value() {
    [ "$#" -ge 2 ] || usage
}

[ "$(id -u)" -eq 0 ] || die 'must run as root'
[ "$(uname -s)" = Linux ] || die 'this installer targets Linux'

miner_address=
binary_source=
config_source=
trusted_host=
known_hosts_source=
trusted_port=22
advertise_addr=
bootstrap_mode=genesis

while [ "$#" -gt 0 ]; do
    case "$1" in
        --miner-address) require_value "$@"; miner_address=$2; shift 2 ;;
        --binary) require_value "$@"; binary_source=$2; shift 2 ;;
        --config-dir) require_value "$@"; config_source=$2; shift 2 ;;
        --trusted-host) require_value "$@"; trusted_host=$2; shift 2 ;;
        --known-hosts-file) require_value "$@"; known_hosts_source=$2; shift 2 ;;
        --trusted-port) require_value "$@"; trusted_port=$2; shift 2 ;;
        --advertise-addr) require_value "$@"; advertise_addr=$2; shift 2 ;;
        --bootstrap-mode) require_value "$@"; bootstrap_mode=$2; shift 2 ;;
        -h|--help) usage ;;
        *) usage ;;
    esac
done

printf '%s\n' "$miner_address" | grep -Eq '^fbc1[023456789acdefghjklmnpqrstuvwxyz]{39}$' || \
    die '--miner-address must be one canonical lowercase fbc address'
case "$trusted_port" in ''|*[!0-9]*) usage ;; esac
[ "$trusted_port" -ge 1 ] && [ "$trusted_port" -le 65535 ] || usage
case "$bootstrap_mode" in
    genesis|latejoin) ;;
    *) usage ;;
esac
case "$trusted_host" in
    ''|-*|*[!A-Za-z0-9._-]*) die '--trusted-host must be a hostname or IPv4 address without user or port' ;;
esac
if [ -n "$advertise_addr" ]; then
    printf '%s\n' "$advertise_addr" | \
        grep -Eq '^([A-Za-z0-9.-]+|\[[0-9A-Fa-f:]+\]):[0-9]{1,5}$' || \
        die '--advertise-addr must be host:port'
    advertise_port=${advertise_addr##*:}
    [ "$advertise_port" -ge 1 ] && [ "$advertise_port" -le 65535 ] || \
        die '--advertise-addr port must be in 1..65535'
fi

[ -f "$binary_source" ] || die "binary not found: $binary_source"
[ -x "$binary_source" ] || die 'binary source must be executable'
[ -d "$config_source" ] || die "config directory not found: $config_source"
[ -f "$known_hosts_source" ] || die "known_hosts source not found: $known_hosts_source"
[ ! -L "$known_hosts_source" ] || die 'known_hosts source must not be a symlink'

for config_name in config.json genesis.json frost_default.json witness_default.json peers.json; do
    [ -f "$config_source/$config_name" ] || die "missing config file: $config_source/$config_name"
done
for command_name in awk getent grep groupadd install ssh-keygen systemctl useradd; do
    command -v "$command_name" >/dev/null 2>&1 || die "missing command: $command_name"
done
for executable in /usr/bin/bash /usr/bin/jq /usr/bin/ssh /usr/bin/timeout; do
    [ -x "$executable" ] || die "missing required executable: $executable"
done
if grep -Eq 'BEGIN([[:space:]][A-Z0-9]+)*[[:space:]]+PRIVATE KEY' "$known_hosts_source"; then
    die 'known_hosts source appears to contain a private key'
fi
known_host_name=$trusted_host
if [ "$trusted_port" -ne 22 ]; then
    known_host_name="[$trusted_host]:$trusted_port"
fi
known_host_lines=$(awk 'NF && $1 !~ /^#/ { count++ } END { print count + 0 }' "$known_hosts_source")
[ "$known_host_lines" -eq 1 ] || die 'known_hosts must contain exactly one non-comment host key'
pinned_host=$(awk 'NF && $1 !~ /^#/ { print $1 }' "$known_hosts_source")
pinned_type=$(awk 'NF && $1 !~ /^#/ { print $2 }' "$known_hosts_source")
pinned_fields=$(awk 'NF && $1 !~ /^#/ { print NF }' "$known_hosts_source")
[ "$pinned_fields" -eq 3 ] || die 'known_hosts entry must contain exactly host, key type, and public key'
[ "$pinned_host" = "$known_host_name" ] || die "known_hosts pins $pinned_host, expected $known_host_name"
[ "$pinned_type" = ssh-ed25519 ] || die 'known_hosts must pin an ssh-ed25519 host key'
ssh-keygen -lf "$known_hosts_source" >/dev/null 2>&1 || die 'ssh-keygen rejected the pinned known_hosts file'
ssh-keygen -F "$known_host_name" -f "$known_hosts_source" >/dev/null 2>&1 || \
    die "known_hosts has no entry for $known_host_name"

/usr/bin/jq -e --arg miner "$miner_address" --arg mode "$bootstrap_mode" '
    def fbc_address:
        type == "string" and test("^fbc1[023456789acdefghjklmnpqrstuvwxyz]{39}$");
    def public_hex:
        type == "string" and length > 0 and (length % 2 == 0) and test("^[0-9A-Fa-f]+$");
    def secret_field:
        ascii_downcase | gsub("[_ -]"; "") |
        . == "privatekey" or . == "frostwrapkey" or . == "mnemonic" or
        . == "seed" or . == "seedphrase" or . == "secret" or
        . == "secretkey" or . == "password" or . == "passphrase";
    def public_endpoint:
        type == "string" and
        (test("^[A-Za-z0-9.-]+:[0-9]{1,5}$") or test("^\\[[0-9A-Fa-f:]+\\]:[0-9]{1,5}$")) and
        ((capture(":(?<port>[0-9]{1,5})$").port | tonumber) as $port |
            $port >= 1 and $port <= 65535);
    def only_keys($allowed):
        all(keys[]; . as $key | $allowed | index($key) != null);
    (.genesis_validators // [] | map(.address)) as $validator_addresses |
    (
    (.schema_version == 1) and
    (.kind == "public-peer-manifest") and
    ((._notice | type) == "string") and
    (._notice | ascii_upcase | contains("NO SECRETS")) and
    (.chain_id | type == "number" and . > 0) and
    (.genesis_validators | type == "array" and length > 0) and
    (.bootstrap_endpoints | type == "array" and length > 0) and
    (only_keys(["_notice", "schema_version", "kind", "chain_id",
        "genesis_validators", "bootstrap_endpoints"])) and
    ([.. | objects | keys[] | select(secret_field)] | length == 0) and
    (all(.genesis_validators[];
        only_keys(["address", "reward_address", "secp_public_key", "bls_public_key", "is_miner"]) and
        (.address | fbc_address) and
        (.reward_address | fbc_address) and
        (.secp_public_key | type == "string" and test("^(02|03)[0-9A-Fa-f]{64}$")) and
        (.bls_public_key | public_hex) and
        (.is_miner == true))) and
    ([.genesis_validators[].address] | length == (unique | length)) and
    (all(.bootstrap_endpoints[];
        . as $route |
        ($route | only_keys(["address", "endpoint"])) and
        ($route.address | fbc_address) and
        ($route.endpoint | public_endpoint) and
        ($validator_addresses | index($route.address) != null))) and
    ([.bootstrap_endpoints[].address] | length == (unique | length)) and
    ([.bootstrap_endpoints[].endpoint] | length == (unique | length)) and
    ([.bootstrap_endpoints[].address] | sort == ($validator_addresses | sort)) and
    (if $mode == "genesis" then
        ([.genesis_validators[] | select(.address == $miner)] | length == 1)
    else
        ([.bootstrap_endpoints[] | select(.address != $miner)] | length > 0)
    end)
    )
' "$config_source/peers.json" >/dev/null || \
    die 'peers.json must contain complete address/reward/secp/BLS/is_miner public records, valid bootstrap routes, and no secrets; genesis mode must also contain this miner'
manifest_chain=$(/usr/bin/jq -er '.chain_id | select(type == "number" and . > 0 and . == floor)' \
    "$config_source/peers.json") || die 'peers.json chain_id must be a positive integer'
genesis_chain=$(/usr/bin/jq -er '.chain_id | select(type == "number" and . > 0 and . == floor)' \
    "$config_source/genesis.json") || die 'genesis.json chain_id must be a positive integer'
[ "$manifest_chain" = "$genesis_chain" ] || die 'genesis.json and peers.json chain_id values differ'
/usr/bin/jq -e '.enabled == true' "$config_source/frost_default.json" >/dev/null || \
    die 'this secure miner template requires FROST enabled=true'
default_k=$(/usr/bin/jq -er '.vault.defaultK | select(type == "number" and . >= 1 and . == floor)' \
    "$config_source/frost_default.json") || die 'frost vault.defaultK must be a positive integer'
miner_count=$(/usr/bin/jq -er '[.genesis_validators[] | select(.is_miner == true)] | length' \
    "$config_source/peers.json") || die 'cannot count FROST miners in peers.json'
[ "$miner_count" -ge "$default_k" ] || \
    die "peers.json has $miner_count miners but FROST defaultK requires $default_k"
local_is_genesis=$(/usr/bin/jq -er --arg miner "$miner_address" \
    '[.genesis_validators[] | select(.address == $miner)] | length' "$config_source/peers.json") || \
    die 'cannot resolve --miner-address in peers.json'
manifest_endpoint=$(/usr/bin/jq -r --arg miner "$miner_address" \
    '[.bootstrap_endpoints[] | select(.address == $miner) | .endpoint][0] // ""' \
    "$config_source/peers.json") || die 'cannot resolve the local manifest endpoint'
if [ "$local_is_genesis" -eq 1 ] && [ -n "$advertise_addr" ] && \
    [ "$advertise_addr" != "$manifest_endpoint" ]; then
    die '--advertise-addr conflicts with the endpoint bound to --miner-address in peers.json'
fi

fetch_key="/var/lib/dex/ssh/$miner_address.secret_fetch"
for legacy_fetch in /var/lib/dex/ssh/secret_fetch /var/lib/dex/ssh/secret_fetch.pub \
    /var/lib/dex/ssh/node-*; do
    if [ -e "$legacy_fetch" ] || [ -L "$legacy_fetch" ]; then
        die 'legacy unbound or node-N fetch key found; verify its miner address and rename it manually before retrying (automatic identity mapping is forbidden)'
    fi
done
for legacy_runtime in /etc/dex/node /var/lib/dex/node; do
    if [ -e "$legacy_runtime" ] || [ -L "$legacy_runtime" ]; then
        die 'legacy unbound node runtime directory found; back it up, verify its private key address, and migrate it manually to the address-bound config/data path before retrying'
    fi
done

nologin_shell=$(command -v nologin || true)
[ -n "$nologin_shell" ] || die 'nologin shell is required'

if ! getent group dex >/dev/null 2>&1; then
    groupadd --system dex
fi
if ! id dex >/dev/null 2>&1; then
    useradd --system --gid dex --home-dir /var/lib/dex --shell "$nologin_shell" --no-create-home dex
else
    dex_home=$(getent passwd dex | awk -F: '{print $6}')
    dex_shell=$(getent passwd dex | awk -F: '{print $7}')
    [ "$dex_home" = /var/lib/dex ] || die "existing dex user has unexpected home: $dex_home"
    case "$dex_shell" in
        */nologin|*/false) ;;
        *) die "existing dex user has an interactive shell: $dex_shell" ;;
    esac
fi
[ "$(id -u dex)" -ne 0 ] || die 'dex service user must not be UID 0'
[ "$(id -gn dex)" = dex ] || die 'dex must use dex as its primary group'
[ "$(id -Gn dex)" = dex ] || die 'dex service user must not belong to additional groups'
explicit_dex_members=$(getent group dex | awk -F: '{print $4}')
case "$explicit_dex_members" in
    ''|dex) ;;
    *) die "dex group has unexpected explicit members: $explicit_dex_members" ;;
esac
dex_gid=$(getent group dex | awk -F: '{print $3}')
primary_dex_users=$(getent passwd | awk -F: -v gid="$dex_gid" '$4 == gid { print $1 }')
[ "$primary_dex_users" = dex ] || die "dex is the primary group of unexpected users: $primary_dex_users"

install -d -o root -g root -m 0755 /opt/dex /opt/dex/bin
install -d -o root -g dex -m 0750 /etc/dex /etc/dex/miner
install -d -o root -g dex -m 0750 /var/lib/dex /var/lib/dex/ssh /var/lib/dex/miners
data_dir="/var/lib/dex/miners/$miner_address"
install -d -o dex -g dex -m 0700 "$data_dir"
install -d -o dex -g dex -m 0750 /var/log/dex

install -o root -g root -m 0755 "$binary_source" /opt/dex/bin/node
for config_name in config.json genesis.json frost_default.json witness_default.json peers.json; do
    install -o root -g dex -m 0640 "$config_source/$config_name" "/etc/dex/miner/$config_name"
done
install -o root -g root -m 0644 "$known_hosts_source" /etc/dex/trusted-server-known-hosts

if [ ! -e "$fetch_key" ]; then
    ssh-keygen -q -t ed25519 -a 64 -N '' -C "dex-secret-fetch-$miner_address" -f "$fetch_key"
fi
[ -f "$fetch_key" ] && [ -f "$fetch_key.pub" ] || die 'fetch key pair is incomplete'
[ ! -L "$fetch_key" ] && [ ! -L "$fetch_key.pub" ] || die 'fetch key files must not be symlinks'
chown root:dex "$fetch_key"
chmod 0640 "$fetch_key"
chown root:root "$fetch_key.pub"
chmod 0644 "$fetch_key.pub"

install -o root -g root -m 0755 "$SCRIPT_DIR/start-dex-secure" /usr/local/sbin/start-dex-secure
install -o root -g root -m 0644 "$SCRIPT_DIR/dex-node-secure.service" \
    /etc/systemd/system/dex-node-secure.service

env_tmp=$(mktemp /etc/dex/.secure-node.env.XXXXXX)
cleanup_env() {
    [ -n "${env_tmp:-}" ] && rm -f -- "$env_tmp"
}
trap cleanup_env EXIT HUP INT TERM
{
    printf 'DEX_MINER_ADDRESS=%s\n' "$miner_address"
    printf 'DEX_BIN=/opt/dex/bin/node\n'
    printf 'DEX_CONFIG_DIR=/etc/dex/miner\n'
    printf 'DEX_PEERS_FILE=/etc/dex/miner/peers.json\n'
    printf 'DEX_DATA_DIR=%s\n' "$data_dir"
    printf 'DEX_BOOTSTRAP_MODE=%s\n' "$bootstrap_mode"
    printf 'DEX_ADVERTISE_ADDR=%s\n' "$advertise_addr"
    printf 'DEX_SECRET_HOST=%s\n' "$trusted_host"
    printf 'DEX_SECRET_PORT=%s\n' "$trusted_port"
    printf 'DEX_SECRET_IDENTITY_FILE=%s\n' "$fetch_key"
    printf 'DEX_SECRET_KNOWN_HOSTS=/etc/dex/trusted-server-known-hosts\n'
    printf 'DEX_SECRET_FETCH_TIMEOUT_SECONDS=20\n'
    printf 'DEX_LOG_FILE=/var/log/dex/%s.log\n' "$miner_address"
} > "$env_tmp"
chown root:root "$env_tmp"
chmod 0600 "$env_tmp"
mv -f -- "$env_tmp" /etc/dex/secure-node.env
env_tmp=
trap - EXIT HUP INT TERM

systemctl daemon-reload
if command -v systemd-analyze >/dev/null 2>&1; then
    systemd-analyze verify /etc/systemd/system/dex-node-secure.service >/dev/null
fi

printf '%s\n' 'VPS secure-node runtime installed but not started or enabled.'
printf '%s\n' 'Copy this public fetch key to the trusted server (it is not secret):'
cat "$fetch_key.pub"
printf '%s\n' 'After trusted-server provisioning, open a short grant and run:'
printf '%s\n' '  sudo systemctl start dex-node-secure.service'

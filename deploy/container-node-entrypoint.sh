#!/bin/sh
set -eu

umask 077
ulimit -c 0 2>/dev/null || true

node_bin=${DEX_NODE_BIN:-/app/node}
register_bin=${DEX_MINER_REGISTER_BIN:-/app/miner-register}
private_key_file=${DEX_PRIVATE_KEY_FILE:-/run/secrets/dex-miner/private_key}
frost_wrap_key_file=${DEX_FROST_WRAP_KEY_FILE:-/run/secrets/dex-miner/frost_wrap_key}
arbitrage_private_key_file=${DEX_ARBITRAGE_PRIVATE_KEY_FILE:-/run/secrets/dex-arbitrage/private_key}

fail() {
    printf '%s\n' "container-node-entrypoint: $*" >&2
    exit 1
}

[ -x "$node_bin" ] || fail "node binary is unavailable"
[ -x "$register_bin" ] || fail "miner-register binary is unavailable"

mode=node
case "${1:-}" in
    check-config|identity|config|node|register|update-endpoint)
        mode=$1
        shift
        ;;
esac

reject_managed_secret_flags() {
    for managed_arg in "$@"; do
        case "$managed_arg" in
            --private-key|--private-key=*|--private-key-fd|--private-key-fd=*|\
            --arbitrage-private-key|--arbitrage-private-key=*|\
            --arbitrage-private-key-fd|--arbitrage-private-key-fd=*|\
            --frost-wrap-key-fd|--frost-wrap-key-fd=*)
                fail "secret-bearing flags are managed by the container entrypoint"
                ;;
        esac
    done
}

case "$mode" in
    check-config)
        reject_managed_secret_flags "$@"
        exec "$node_bin" --check-config "$@"
        ;;
    identity)
        [ "$#" -eq 0 ] || fail "identity takes no arguments"
        [ -f "$private_key_file" ] && [ -r "$private_key_file" ] || \
            fail "private key secret file is unavailable"
        exec 3<"$private_key_file"
        exec "$register_bin" --private-key-fd=3 --print-address
        ;;
    config)
        config_print_count=0
        for config_arg in "$@"; do
            case "$config_arg" in
                --print-chain-id|--print-node-port|--print-frost-enabled)
                    config_print_count=$((config_print_count + 1))
                    ;;
                --config-dir=*) ;;
                *) fail "config accepts one print mode and --config-dir=<directory>" ;;
            esac
        done
        [ "$config_print_count" -eq 1 ] || fail "config requires exactly one print mode"
        exec "$register_bin" "$@"
        ;;
    register)
        [ "${1:-}" = "--confirm-ready" ] || \
            fail "register requires --confirm-ready as the first argument"
        shift
        reject_managed_secret_flags "$@"
        for register_arg in "$@"; do
            case "$register_arg" in
                --state-file|--state-file=*)
                    fail "register journal path is fixed by the container entrypoint"
                    ;;
            esac
        done
        [ -f "$private_key_file" ] && [ -r "$private_key_file" ] || \
            fail "private key secret file is unavailable"
        exec 3<"$private_key_file"
        exec "$register_bin" --private-key-fd=3 "$@" --state-file=/state/registration.json
        ;;
    update-endpoint)
        [ "${1:-}" = "--confirm-ready" ] || \
            fail "update-endpoint requires --confirm-ready as the first argument"
        shift
        reject_managed_secret_flags "$@"
        for update_arg in "$@"; do
            case "$update_arg" in
                --state-file|--state-file=*)
                    fail "endpoint update journal path is fixed by the container entrypoint"
                    ;;
            esac
        done
        [ -f "$private_key_file" ] && [ -r "$private_key_file" ] || \
            fail "private key secret file is unavailable"
        exec 3<"$private_key_file"
        exec "$register_bin" --private-key-fd=3 --update-endpoint "$@" --state-file=/state/endpoint-update.json
        ;;
esac

reject_managed_secret_flags "$@"
[ -f "$private_key_file" ] && [ -r "$private_key_file" ] || \
    fail "private key secret file is unavailable"

expected_address_seen=false
expected_arbitrage_address_seen=false
for arg in "$@"; do
    case "$arg" in
        --expected-address=*)
            [ -n "${arg#*=}" ] || fail "--expected-address must not be empty"
            [ "$expected_address_seen" = false ] || \
                fail "--expected-address must be provided exactly once"
            expected_address_seen=true
            ;;
        --expected-address)
            fail "use the unambiguous --expected-address=<address> form"
            ;;
        --expected-arbitrage-address=*)
            [ -n "${arg#*=}" ] || fail "--expected-arbitrage-address must not be empty"
            [ "$expected_arbitrage_address_seen" = false ] || \
                fail "--expected-arbitrage-address must be provided at most once"
            expected_arbitrage_address_seen=true
            ;;
        --expected-arbitrage-address)
            fail "use the unambiguous --expected-arbitrage-address=<address> form"
            ;;
    esac
done
[ "$expected_address_seen" = true ] || \
    fail "--expected-address=<address> is required"

                                                                    
                                                                              
exec 3<"$private_key_file"

if [ -e "$arbitrage_private_key_file" ]; then
    [ "$expected_arbitrage_address_seen" = true ] || \
        fail "--expected-arbitrage-address=<address> is required when the arbitrage secret is mounted"
    [ -f "$arbitrage_private_key_file" ] && [ -r "$arbitrage_private_key_file" ] || \
        fail "arbitrage private key secret file is not readable"
    exec 5<"$arbitrage_private_key_file"
    if [ -e "$frost_wrap_key_file" ]; then
        [ -f "$frost_wrap_key_file" ] && [ -r "$frost_wrap_key_file" ] || \
            fail "FROST wrap key secret file is not readable"
        exec 4<"$frost_wrap_key_file"
        exec "$node_bin" "$@" --private-key-fd=3 --frost-wrap-key-fd=4 --arbitrage-private-key-fd=5
    fi
    exec "$node_bin" "$@" --private-key-fd=3 --arbitrage-private-key-fd=5
fi

if [ -e "$frost_wrap_key_file" ]; then
    [ -f "$frost_wrap_key_file" ] && [ -r "$frost_wrap_key_file" ] || \
        fail "FROST wrap key secret file is not readable"
    exec 4<"$frost_wrap_key_file"
    exec "$node_bin" "$@" --private-key-fd=3 --frost-wrap-key-fd=4
fi

exec "$node_bin" "$@" --private-key-fd=3

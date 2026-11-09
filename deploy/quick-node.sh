#!/bin/sh
set -eu

umask 077
ulimit -c 0 2>/dev/null || true

CONTAINER_NAME=frostbit-node
CONFIG_ROOT=/opt/frostbit/config
CONFIG_DEST=$CONFIG_ROOT/runtime
SECRET_ROOT=/opt/frostbit/secrets
STATE_ROOT=/var/lib/frostbit-quick-node

die() {
    printf 'quick-node: %s\n' "$1" >&2
    exit 1
}

usage() {
    cat >&2 <<'EOF'
Usage:
  sudo ./frostbit-node up \
    --image REGISTRY/IMAGE:VERSION@sha256:DIGEST \
    --network-dir DIR --private-key-file FILE \
    [--frost-wrap-key-file FILE] \
    --reward-address fbc1... --explorer-url https://... \
    [--advertise-endpoint PUBLIC_IPV4:PORT] \
    [--container-network publish|host]

  sudo ./frostbit-node register --confirm-ready [--resubmit-unresolved]

The private key and optional FROST wrap key are read from files and passed to
the container only through inherited descriptors. Their values must never be
provided on the command line or in environment variables.
EOF
    exit 64
}

require_root() {
    [ "$(id -u)" -eq 0 ] || die 'must run as root'
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || die "missing command: $1"
}

require_real_file() {
    quick_file=$1
    [ -f "$quick_file" ] || die "not a regular file: $quick_file"
    [ ! -L "$quick_file" ] || die "symbolic links are not allowed: $quick_file"
    [ -r "$quick_file" ] || die "file is not readable: $quick_file"
}

validate_secret_file() {
    quick_secret=$1
    require_real_file "$quick_secret"
    awk '
        BEGIN { valid = 0 }
        /^[0-9A-Fa-f]{64}$/ { if (valid) exit 1; valid = 1; next }
        { exit 1 }
        END { if (!valid) exit 1 }
    ' "$quick_secret" >/dev/null 2>&1 || \
        die "secret file must contain exactly one 64-character hexadecimal token: $quick_secret"
    quick_mode=$(stat -c '%a' "$quick_secret" 2>/dev/null || true)
    case "$quick_mode" in ''|*[!0-7]*) die "cannot validate secret permissions: $quick_secret" ;; esac
    [ $((0$quick_mode & 022)) -eq 0 ] || \
        die "secret file must not be group/world writable: $quick_secret"
}

is_public_ipv4() {
    quick_ip=$1
    quick_old_ifs=$IFS
    IFS=.
    set -- $quick_ip
    IFS=$quick_old_ifs
    [ "$#" -eq 4 ] || return 1
    for quick_octet in "$@"; do
        case "$quick_octet" in ''|*[!0-9]*) return 1 ;; esac
        [ "$quick_octet" -ge 0 ] && [ "$quick_octet" -le 255 ] || return 1
    done
    quick_a=$1 quick_b=$2 quick_c=$3
    [ "$quick_a" -ge 1 ] && [ "$quick_a" -le 223 ] || return 1
    case "$quick_a.$quick_b" in
        10.*|127.*|169.254|192.168|198.18|198.19) return 1 ;;
    esac
    [ "$quick_a" -ne 100 ] || [ "$quick_b" -lt 64 ] || [ "$quick_b" -gt 127 ] || return 1
    [ "$quick_a" -ne 172 ] || [ "$quick_b" -lt 16 ] || [ "$quick_b" -gt 31 ] || return 1
    [ "$quick_a.$quick_b.$quick_c" != 192.0.0 ] || return 1
    [ "$quick_a.$quick_b.$quick_c" != 192.0.2 ] || return 1
    [ "$quick_a.$quick_b.$quick_c" != 192.88.99 ] || return 1
    [ "$quick_a.$quick_b.$quick_c" != 198.51.100 ] || return 1
    [ "$quick_a.$quick_b.$quick_c" != 203.0.113 ] || return 1
    return 0
}

discover_public_ipv4() {
    require_command ip
    quick_candidates=
    for quick_cidr in $(ip -o -4 addr show up scope global 2>/dev/null | awk '{print $4}'); do
        quick_candidate=${quick_cidr%/*}
        if is_public_ipv4 "$quick_candidate"; then
            case " $quick_candidates " in
                *" $quick_candidate "*) ;;
                *) quick_candidates="$quick_candidates $quick_candidate" ;;
            esac
        fi
    done
    set -- $quick_candidates
    [ "$#" -eq 1 ] || die 'could not select exactly one public IPv4 address; use --advertise-endpoint for NAT or multi-address hosts'
    printf '%s\n' "$1"
}

validate_endpoint() {
    quick_endpoint=$1
    case "$quick_endpoint" in
        *:*) ;;
        *) die '--advertise-endpoint must be PUBLIC_IPV4:PORT' ;;
    esac
    quick_host=${quick_endpoint%:*}
    quick_port=${quick_endpoint##*:}
    is_public_ipv4 "$quick_host" || die '--advertise-endpoint must use a public numeric IPv4 address'
    case "$quick_port" in ''|*[!0-9]*) die '--advertise-endpoint port must be numeric' ;; esac
    [ "$quick_port" -ge 1 ] && [ "$quick_port" -le 65535 ] || \
        die '--advertise-endpoint port must be in 1..65535'
}

validate_reward_address() {
    case "$1" in fbc1*) ;; *) die '--reward-address must be a canonical lowercase fbc1 address' ;; esac
    [ "${#1}" -eq 43 ] || die '--reward-address must be a canonical lowercase fbc1 address'
    quick_reward_payload=${1#fbc1}
    case "$quick_reward_payload" in
        *[!023456789acdefghjklmnpqrstuvwxyz]*) die '--reward-address must be a canonical lowercase fbc1 address' ;;
    esac
}

validate_explorer_url() {
    case "$1" in
        https://*|http://127.0.0.1:*|http://localhost:*) ;;
        *) die '--explorer-url must use HTTPS, except for an explicit loopback development URL' ;;
    esac
    case "$1" in *[!A-Za-z0-9:/._?%+=\&-]*) die '--explorer-url contains unsupported characters' ;; esac
}

cleanup_config_stage() {
    if [ -n "${CONFIG_STAGE:-}" ] && [ -d "$CONFIG_STAGE" ]; then
        for quick_name in config.json genesis.json frost_default.json witness_default.json peers.json; do
            rm -f -- "$CONFIG_STAGE/$quick_name"
        done
        rmdir -- "$CONFIG_STAGE" 2>/dev/null || true
    fi
}

install_public_config() {
    quick_source=$1
    for quick_name in config.json genesis.json frost_default.json witness_default.json peers.json; do
        require_real_file "$quick_source/$quick_name"
    done
    install -d -o root -g root -m 0755 "$CONFIG_ROOT"
    if [ -d "$CONFIG_DEST" ]; then
        [ ! -L "$CONFIG_DEST" ] || die "configuration destination is a symbolic link: $CONFIG_DEST"
        for quick_name in config.json genesis.json frost_default.json witness_default.json peers.json; do
            cmp -s -- "$quick_source/$quick_name" "$CONFIG_DEST/$quick_name" || \
                die "installed configuration differs at $quick_name; use the documented upgrade flow"
        done
        return
    fi
    CONFIG_STAGE=$(mktemp -d "$CONFIG_ROOT/.runtime.XXXXXX")
    for quick_name in config.json genesis.json frost_default.json witness_default.json peers.json; do
        install -o root -g root -m 0444 "$quick_source/$quick_name" "$CONFIG_STAGE/$quick_name"
    done
    mv -- "$CONFIG_STAGE" "$CONFIG_DEST"
    CONFIG_STAGE=
}

install_secret_file() {
    quick_source=$1
    quick_destination=$2
    if [ -e "$quick_destination" ]; then
        [ -f "$quick_destination" ] && [ ! -L "$quick_destination" ] || \
            die "installed secret path is not a regular file: $quick_destination"
        cmp -s -- "$quick_source" "$quick_destination" || \
            die "installed secret differs and will not be overwritten: $quick_destination"
        return
    fi
    install -o root -g 65532 -m 0440 "$quick_source" "$quick_destination"
}

docker_config_value() (
    quick_image=$1
    quick_network=$2
    quick_mode=$3
    docker run --rm \
        --user 0:0 \
        --mount "type=bind,src=$quick_network,dst=/runtime,readonly" \
        "$quick_image" config "$quick_mode" --config-dir=/runtime | tail -n 1
)

require_digest_image() {
    quick_digest_image=$1
    case "$quick_digest_image" in
        *@sha256:*) ;;
        *) die '--image must be the digest-pinned reference published with the release' ;;
    esac
    quick_digest=${quick_digest_image##*@sha256:}
    [ "${#quick_digest}" -eq 64 ] || die '--image sha256 digest must contain 64 lowercase hexadecimal characters'
    case "$quick_digest" in *[!0-9a-f]*) die '--image sha256 digest must contain 64 lowercase hexadecimal characters' ;; esac
}

config_digest() (
    quick_digest_dir=$1
    for quick_digest_name in config.json genesis.json frost_default.json witness_default.json peers.json; do
        quick_file_digest=$(sha256sum "$quick_digest_dir/$quick_digest_name" | awk '{print $1}')
        printf '%s  %s\n' "$quick_file_digest" "$quick_digest_name"
    done | sha256sum | awk '{print $1}'
)

write_metadata_value() {
    quick_metadata_path=$1
    quick_metadata_expected=$2
    if [ -e "$quick_metadata_path" ]; then
        require_real_file "$quick_metadata_path"
        IFS= read -r quick_metadata_current < "$quick_metadata_path" || \
            die "cannot read $quick_metadata_path"
        [ "$quick_metadata_current" = "$quick_metadata_expected" ] || \
            die "stored deployment intent differs at $quick_metadata_path"
        return
    fi
    printf '%s\n' "$quick_metadata_expected" > "$quick_metadata_path"
    chmod 0644 "$quick_metadata_path"
}

write_public_metadata() {
    quick_state_dir=$1
    quick_reward=$2
    quick_explorer=$3
    quick_endpoint=$4
    quick_chain=$5
    quick_image=$6
    quick_config_digest=$7
    quick_image_id=$8
    quick_container_network=$9
    install -d -o root -g root -m 0755 "$quick_state_dir"
    write_metadata_value "$quick_state_dir/reward_address" "$quick_reward"
    write_metadata_value "$quick_state_dir/explorer_url" "$quick_explorer"
    write_metadata_value "$quick_state_dir/endpoint" "$quick_endpoint"
    write_metadata_value "$quick_state_dir/chain_id" "$quick_chain"
    write_metadata_value "$quick_state_dir/image" "$quick_image"
    write_metadata_value "$quick_state_dir/image_id" "$quick_image_id"
    write_metadata_value "$quick_state_dir/config_digest" "$quick_config_digest"
    write_metadata_value "$quick_state_dir/container_network" "$quick_container_network"
    install -d -o 65532 -g 65532 -m 0700 "$quick_state_dir/attempt"
}

validate_container_network_intent() {
    quick_expected_network=$1
    quick_actual_network=$(docker inspect --format '{{.HostConfig.NetworkMode}}' "$CONTAINER_NAME")
    quick_actual_bindings=$(docker inspect --format '{{json .HostConfig.PortBindings}}' "$CONTAINER_NAME")
    if [ "$quick_expected_network" = host ]; then
        [ "$quick_actual_network" = host ] && [ "$quick_actual_bindings" = '{}' ] || \
            die 'host-network container unexpectedly has a different network mode or published ports'
    else
        [ "$quick_actual_network" = bridge ] || die 'published-port container unexpectedly has a different network mode'
    fi
}

command_up() {
    quick_network=
    quick_private=
    quick_wrap=
    quick_reward=
    quick_explorer=
    quick_endpoint=
    quick_image=
    quick_container_network=publish
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --network-dir) [ "$#" -ge 2 ] || usage; quick_network=$2; shift 2 ;;
            --private-key-file) [ "$#" -ge 2 ] || usage; quick_private=$2; shift 2 ;;
            --frost-wrap-key-file) [ "$#" -ge 2 ] || usage; quick_wrap=$2; shift 2 ;;
            --reward-address) [ "$#" -ge 2 ] || usage; quick_reward=$2; shift 2 ;;
            --explorer-url) [ "$#" -ge 2 ] || usage; quick_explorer=$2; shift 2 ;;
            --advertise-endpoint) [ "$#" -ge 2 ] || usage; quick_endpoint=$2; shift 2 ;;
            --image) [ "$#" -ge 2 ] || usage; quick_image=$2; shift 2 ;;
            --container-network) [ "$#" -ge 2 ] || usage; quick_container_network=$2; shift 2 ;;
            *) usage ;;
        esac
    done
    [ -n "$quick_image" ] && [ -n "$quick_network" ] && [ -n "$quick_private" ] && [ -n "$quick_reward" ] && [ -n "$quick_explorer" ] || usage
    [ -d "$quick_network" ] && [ ! -L "$quick_network" ] || die '--network-dir must be a real directory'
    case "$quick_network$quick_private$quick_wrap" in *,*) die 'input paths must not contain commas' ;; esac
    validate_secret_file "$quick_private"
    if [ -n "$quick_wrap" ]; then validate_secret_file "$quick_wrap"; fi
    validate_reward_address "$quick_reward"
    validate_explorer_url "$quick_explorer"
    require_digest_image "$quick_image"
    case "$quick_container_network" in
        publish|host) ;;
        *) die '--container-network must be publish or host' ;;
    esac
    docker pull "$quick_image" >/dev/null
    quick_image_id=$(docker image inspect --format '{{.Id}}' "$quick_image")
    [ -n "$quick_image_id" ] || die 'pulled image has no immutable image ID'

    docker run --rm --user 0:0 \
        --mount "type=bind,src=$quick_network,dst=/runtime,readonly" \
        "$quick_image_id" check-config --config-dir=/runtime --peers=/runtime/peers.json

    quick_address=$(docker run --rm --user 0:0 \
        --mount "type=bind,src=$quick_private,dst=/run/secrets/dex-miner/private_key,readonly" \
        "$quick_image_id" identity | tail -n 1)
    case "$quick_address" in fbc1*) ;; *) die 'failed to derive a canonical miner address' ;; esac
    [ "${#quick_address}" -eq 43 ] || die 'derived miner address has an invalid length'

    quick_chain=$(docker_config_value "$quick_image_id" "$quick_network" --print-chain-id)
    quick_port=$(docker_config_value "$quick_image_id" "$quick_network" --print-node-port)
    quick_frost=$(docker_config_value "$quick_image_id" "$quick_network" --print-frost-enabled)
    case "$quick_chain" in ''|*[!0-9]*) die 'candidate image returned an invalid chain ID' ;; esac
    case "$quick_port" in ''|*[!0-9]*) die 'candidate image returned an invalid node port' ;; esac
    if [ "$quick_frost" = true ] && [ -z "$quick_wrap" ]; then
        die 'FROST is enabled; provide the existing independent --frost-wrap-key-file'
    fi
    if [ -z "$quick_endpoint" ]; then
        quick_public_ip=$(discover_public_ipv4)
        quick_endpoint="$quick_public_ip:$quick_port"
    fi
    validate_endpoint "$quick_endpoint"
    quick_config_digest=$(config_digest "$quick_network")

    install_public_config "$quick_network"
    quick_secret_dir="$SECRET_ROOT/$quick_address"
    [ ! -L "$SECRET_ROOT" ] && [ ! -L "$quick_secret_dir" ] || die 'secret directories must not be symbolic links'
    install -d -o root -g 65532 -m 0750 "$SECRET_ROOT" "$quick_secret_dir"
    install_secret_file "$quick_private" "$quick_secret_dir/private_key"
    if [ -n "$quick_wrap" ]; then
        install_secret_file "$quick_wrap" "$quick_secret_dir/frost_wrap_key"
    fi

    quick_state_dir="$STATE_ROOT/$quick_address"
    write_public_metadata "$quick_state_dir" "$quick_reward" "$quick_explorer" "$quick_endpoint" "$quick_chain" "$quick_image" "$quick_config_digest" "$quick_image_id" "$quick_container_network"

    if docker container inspect "$CONTAINER_NAME" >/dev/null 2>&1; then
        quick_existing_address=$(docker inspect --format '{{index .Config.Labels "org.frostbit.miner-address"}}' "$CONTAINER_NAME")
        quick_existing_endpoint=$(docker inspect --format '{{index .Config.Labels "org.frostbit.advertise-endpoint"}}' "$CONTAINER_NAME")
        quick_existing_image=$(docker inspect --format '{{index .Config.Labels "org.frostbit.image-id"}}' "$CONTAINER_NAME")
        quick_existing_config=$(docker inspect --format '{{index .Config.Labels "org.frostbit.config-digest"}}' "$CONTAINER_NAME")
        quick_existing_network=$(docker inspect --format '{{index .Config.Labels "org.frostbit.container-network"}}' "$CONTAINER_NAME")
        [ "$quick_existing_address" = "$quick_address" ] && [ "$quick_existing_endpoint" = "$quick_endpoint" ] && \
            [ "$quick_existing_image" = "$quick_image_id" ] && [ "$quick_existing_config" = "$quick_config_digest" ] && \
            [ "$quick_existing_network" = "$quick_container_network" ] || \
            die 'existing frostbit-node belongs to a different deployment intent; it was not changed'
        validate_container_network_intent "$quick_container_network"
        docker start "$CONTAINER_NAME" >/dev/null
    else
        quick_volume="frostbit-node-data-$quick_address"
        if docker volume inspect "$quick_volume" >/dev/null 2>&1; then
            quick_volume_address=$(docker volume inspect --format '{{index .Labels "org.frostbit.miner-address"}}' "$quick_volume")
            quick_volume_chain=$(docker volume inspect --format '{{index .Labels "org.frostbit.chain-id"}}' "$quick_volume")
            quick_volume_config=$(docker volume inspect --format '{{index .Labels "org.frostbit.config-digest"}}' "$quick_volume")
            [ "$quick_volume_address" = "$quick_address" ] && [ "$quick_volume_chain" = "$quick_chain" ] && \
                [ "$quick_volume_config" = "$quick_config_digest" ] || \
                die 'existing data volume is not bound to this miner, chain, and configuration'
        else
            docker volume create --label "org.frostbit.miner-address=$quick_address" \
                --label "org.frostbit.chain-id=$quick_chain" \
                --label "org.frostbit.config-digest=$quick_config_digest" "$quick_volume" >/dev/null
        fi
        if [ "$quick_container_network" = host ]; then
            set -- --network host
        else
            set -- -p "$quick_port:$quick_port/tcp" -p "$quick_port:$quick_port/udp"
        fi
        docker run -d \
            --name "$CONTAINER_NAME" \
            --restart unless-stopped \
            --label "org.frostbit.miner-address=$quick_address" \
            --label "org.frostbit.advertise-endpoint=$quick_endpoint" \
            --label "org.frostbit.chain-id=$quick_chain" \
            --label "org.frostbit.image-id=$quick_image_id" \
            --label "org.frostbit.image-ref=$quick_image" \
            --label "org.frostbit.config-digest=$quick_config_digest" \
            --label "org.frostbit.container-network=$quick_container_network" \
            --log-driver local --log-opt max-size=20m --log-opt max-file=5 \
            "$@" \
            --mount "type=bind,src=$CONFIG_DEST,dst=/app/config/runtime,readonly" \
            --mount "type=bind,src=$quick_secret_dir,dst=/run/secrets/dex-miner,readonly" \
            --mount "type=volume,src=$quick_volume,dst=/data" \
            "$quick_image_id" node \
            --bootstrap-mode=latejoin \
            --config-dir=/app/config/runtime \
            --peers=/app/config/runtime/peers.json \
            "--expected-address=$quick_address" \
            "--data-dir=/data/$quick_address" \
            --resume=true \
            "--listen-addr=0.0.0.0:$quick_port" \
            "--advertise-addr=$quick_endpoint" \
            --no-simulator >/dev/null
        validate_container_network_intent "$quick_container_network"
    fi

    printf 'Node started: address=%s endpoint=%s chain_id=%s\n' "$quick_address" "$quick_endpoint" "$quick_chain"
    printf '%s\n' 'Next: wait for synchronization, verify the same-height block ID and external TCP/UDP reachability, then run:'
    printf '  sudo ./frostbit-node register --confirm-ready\n'
}

read_metadata() {
    quick_metadata_file=$1
    require_real_file "$quick_metadata_file"
    IFS= read -r quick_metadata_value < "$quick_metadata_file" || die "cannot read $quick_metadata_file"
    [ -n "$quick_metadata_value" ] || die "empty metadata file: $quick_metadata_file"
    printf '%s\n' "$quick_metadata_value"
}

command_register() {
    quick_confirm=0
    quick_resubmit=0
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --confirm-ready) quick_confirm=1; shift ;;
            --resubmit-unresolved) quick_resubmit=1; shift ;;
            *) usage ;;
        esac
    done
    [ "$quick_confirm" -eq 1 ] || \
        die 'registration is blocked until --confirm-ready acknowledges sync, same-height block ID, and external TCP/UDP reachability'
    docker container inspect "$CONTAINER_NAME" >/dev/null 2>&1 || die 'frostbit-node container does not exist'
    [ "$(docker inspect --format '{{.State.Status}}' "$CONTAINER_NAME")" = running ] || die 'frostbit-node is not running'
    quick_address=$(docker inspect --format '{{index .Config.Labels "org.frostbit.miner-address"}}' "$CONTAINER_NAME")
    quick_endpoint=$(docker inspect --format '{{index .Config.Labels "org.frostbit.advertise-endpoint"}}' "$CONTAINER_NAME")
    quick_chain=$(docker inspect --format '{{index .Config.Labels "org.frostbit.chain-id"}}' "$CONTAINER_NAME")
    quick_state_dir="$STATE_ROOT/$quick_address"
    quick_reward=$(read_metadata "$quick_state_dir/reward_address")
    quick_explorer=$(read_metadata "$quick_state_dir/explorer_url")
    quick_image_ref=$(read_metadata "$quick_state_dir/image")
    quick_recorded_image_id=$(read_metadata "$quick_state_dir/image_id")
    quick_config_digest=$(read_metadata "$quick_state_dir/config_digest")
    quick_container_network=$(read_metadata "$quick_state_dir/container_network")
    quick_image_id=$(docker inspect --format '{{index .Config.Labels "org.frostbit.image-id"}}' "$CONTAINER_NAME")
    quick_container_image_id=$(docker inspect --format '{{.Image}}' "$CONTAINER_NAME")
    quick_ref_image_id=$(docker image inspect --format '{{.Id}}' "$quick_image_ref" 2>/dev/null || true)
    [ -n "$quick_image_id" ] && [ "$quick_image_id" = "$quick_container_image_id" ] && \
        [ "$quick_image_id" = "$quick_ref_image_id" ] && [ "$quick_image_id" = "$quick_recorded_image_id" ] || \
        die 'published image digest or container image no longer matches the immutable deployment intent'
    [ "$(docker inspect --format '{{index .Config.Labels "org.frostbit.config-digest"}}' "$CONTAINER_NAME")" = "$quick_config_digest" ] || \
        die 'container configuration digest no longer matches the immutable deployment intent'
    [ "$(docker inspect --format '{{index .Config.Labels "org.frostbit.container-network"}}' "$CONTAINER_NAME")" = "$quick_container_network" ] || \
        die 'container network mode no longer matches the immutable deployment intent'
    validate_container_network_intent "$quick_container_network"
    quick_image=$quick_image_id
    quick_secret_dir="$SECRET_ROOT/$quick_address"
    validate_secret_file "$quick_secret_dir/private_key"
    quick_resubmit_arg=
    if [ "$quick_resubmit" -eq 1 ]; then quick_resubmit_arg=--resubmit-unresolved; fi

    docker run --rm --name "frostbit-miner-register-$quick_address" \
        --mount "type=bind,src=$quick_secret_dir/private_key,dst=/run/secrets/dex-miner/private_key,readonly" \
        --mount "type=bind,src=$quick_state_dir/attempt,dst=/state" \
        "$quick_image" register --confirm-ready \
        "--explorer-url=$quick_explorer" \
        "--endpoint=$quick_endpoint" \
        "--reward-address=$quick_reward" \
        "--expected-chain-id=$quick_chain" \
        --amount=3200000000 --fee=1000 \
        ${quick_resubmit_arg:+$quick_resubmit_arg}
}

require_root
require_command docker
require_command awk
require_command cmp
require_command install
require_command mktemp
require_command stat
require_command tail
require_command sha256sum
docker info >/dev/null 2>&1 || die 'Docker daemon is unavailable'
CONFIG_STAGE=
trap cleanup_config_stage EXIT HUP INT TERM

[ "$#" -ge 1 ] || usage
quick_command=$1
shift
case "$quick_command" in
    up) command_up "$@" ;;
    register) command_register "$@" ;;
    -h|--help|help) usage ;;
    *) usage ;;
esac

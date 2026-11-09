#!/bin/sh
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH
umask 077

SERVICE_USER=dex-secrets
SERVICE_GROUP=dex-secrets
SERVICE_HOME=/var/lib/dex-secret-ssh
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SSHD_CONFIG=/etc/ssh/sshd_config
SSHD_BEGIN='# BEGIN DEX SECRET BROKER (managed by deploy/secure-node)'
SSHD_END='# END DEX SECRET BROKER'

die() {
    printf 'trusted-server install: %s\n' "$1" >&2
    exit 1
}

[ "$(id -u)" -eq 0 ] || die 'must run as root'
[ "$(uname -s)" = Linux ] || die 'this installer targets Linux'

for command_name in awk cat chpasswd cp flock getent grep groupadd install mktemp openssl ssh-keygen sshd useradd; do
    command -v "$command_name" >/dev/null 2>&1 || die "missing command: $command_name"
done

if ! getent group "$SERVICE_GROUP" >/dev/null 2>&1; then
    groupadd --system "$SERVICE_GROUP"
fi

if id "$SERVICE_USER" >/dev/null 2>&1; then
    actual_home=$(getent passwd "$SERVICE_USER" | awk -F: '{print $6}')
    actual_shell=$(getent passwd "$SERVICE_USER" | awk -F: '{print $7}')
    [ "$actual_home" = "$SERVICE_HOME" ] || die "$SERVICE_USER already exists with home $actual_home"
    [ "$actual_shell" = /bin/sh ] || die "$SERVICE_USER already exists with shell $actual_shell"
else
    useradd --system --gid "$SERVICE_GROUP" --home-dir "$SERVICE_HOME" \
        --shell /bin/sh --no-create-home "$SERVICE_USER"
fi
service_uid=$(id -u "$SERVICE_USER")
[ "$service_uid" -ne 0 ] || die "$SERVICE_USER must not be UID 0"
[ "$(getent passwd | awk -F: -v uid="$service_uid" '$3 == uid { print $1 }')" = "$SERVICE_USER" ] || \
    die "$SERVICE_USER UID is shared by another account"
[ "$(id -gn "$SERVICE_USER")" = "$SERVICE_GROUP" ] || die "$SERVICE_USER must use $SERVICE_GROUP as its primary group"
[ "$(id -Gn "$SERVICE_USER")" = "$SERVICE_GROUP" ] || die "$SERVICE_USER must not belong to additional groups"
explicit_group_members=$(getent group "$SERVICE_GROUP" | awk -F: '{print $4}')
case "$explicit_group_members" in
    ''|"$SERVICE_USER") ;;
    *) die "$SERVICE_GROUP has unexpected explicit members: $explicit_group_members" ;;
esac
service_gid=$(getent group "$SERVICE_GROUP" | awk -F: '{print $3}')
primary_gid_users=$(getent passwd | awk -F: -v gid="$service_gid" '$4 == gid { print $1 }')
[ "$primary_gid_users" = "$SERVICE_USER" ] || \
    die "$SERVICE_GROUP is the primary group of unexpected users: $primary_gid_users"

[ ! -L "$SERVICE_HOME" ] || die "$SERVICE_HOME must not be a symlink"
[ ! -e "$SERVICE_HOME" ] || [ -d "$SERVICE_HOME" ] || die "$SERVICE_HOME must be a directory"
install -d -o root -g root -m 0755 "$SERVICE_HOME"
[ ! -L "$SERVICE_HOME/.ssh" ] || die "$SERVICE_HOME/.ssh must not be a symlink"
[ ! -e "$SERVICE_HOME/.ssh" ] || [ -d "$SERVICE_HOME/.ssh" ] || die "$SERVICE_HOME/.ssh must be a directory"
install -d -o root -g "$SERVICE_GROUP" -m 0750 "$SERVICE_HOME/.ssh"
if [ ! -e "$SERVICE_HOME/.ssh/authorized_keys" ]; then
    install -o root -g "$SERVICE_GROUP" -m 0640 /dev/null "$SERVICE_HOME/.ssh/authorized_keys"
else
    [ ! -L "$SERVICE_HOME/.ssh/authorized_keys" ] || die 'authorized_keys must not be a symlink'
    [ -f "$SERVICE_HOME/.ssh/authorized_keys" ] || die 'authorized_keys must be a regular file'
    chown root:"$SERVICE_GROUP" "$SERVICE_HOME/.ssh/authorized_keys"
    chmod 0640 "$SERVICE_HOME/.ssh/authorized_keys"
fi

for state_dir in /srv/dex-secrets /run/dex-secret-grants; do
    [ ! -L "$state_dir" ] || die "$state_dir must not be a symlink"
    [ ! -e "$state_dir" ] || [ -d "$state_dir" ] || die "$state_dir must be a directory"
done
install -d -o root -g "$SERVICE_GROUP" -m 0750 /srv/dex-secrets
install -d -o root -g "$SERVICE_GROUP" -m 0750 /run/dex-secret-grants
install -d -o root -g root -m 0755 /usr/local/sbin

install -o root -g "$SERVICE_GROUP" -m 0750 "$SCRIPT_DIR/dex-secretctl" \
    /usr/local/sbin/dex-secretctl

                                                                            
                                                                            
                                                                    
/usr/local/sbin/dex-secretctl validate-install-state
rm -f -- /usr/local/libexec/dex-secret-broker \
    /usr/local/sbin/dex-secret-allow \
    /usr/local/sbin/dex-secret-provision

[ -f "$SSHD_CONFIG" ] || die "missing sshd configuration: $SSHD_CONFIG"
candidate=$(mktemp /etc/ssh/.sshd_config.dex-secrets.XXXXXX)
backup=$(mktemp /etc/ssh/.sshd_config.dex-secrets.backup.XXXXXX)
cleanup_sshd_files() {
    [ -n "${candidate:-}" ] && rm -f -- "$candidate"
    [ -n "${backup:-}" ] && rm -f -- "$backup"
}
trap cleanup_sshd_files EXIT HUP INT TERM

if ! awk -v begin="$SSHD_BEGIN" -v end="$SSHD_END" '
    $0 == begin { if (managed) exit 2; managed = 1; next }
    $0 == end { if (!managed) exit 3; managed = 0; next }
    !managed { print }
    END { if (managed) exit 4 }
' "$SSHD_CONFIG" > "$candidate"; then
    die 'existing managed sshd block is malformed; refusing to edit sshd_config'
fi
{
    printf '\n%s\n' "$SSHD_BEGIN"
    cat "$SCRIPT_DIR/sshd-dex-secrets.conf"
    printf '%s\n' "$SSHD_END"
} >> "$candidate"

sshd -t -f "$candidate" || die 'sshd rejected the candidate configuration; live configuration was not changed'
effective=$(sshd -T -f "$candidate" -C user=dex-secrets,host=localhost,addr=127.0.0.1)
printf '%s\n' "$effective" | grep -qx 'authenticationmethods publickey' || die 'candidate sshd configuration does not require public-key authentication'
printf '%s\n' "$effective" | grep -qx 'passwordauthentication no' || die 'candidate sshd configuration still permits password authentication'
printf '%s\n' "$effective" | grep -qx 'authorizedkeysfile /var/lib/dex-secret-ssh/.ssh/authorized_keys' || die 'candidate sshd configuration does not use the managed authorized_keys file'
printf '%s\n' "$effective" | grep -qx 'authorizedkeyscommand none' || die 'candidate sshd configuration permits an alternate AuthorizedKeysCommand'
printf '%s\n' "$effective" | grep -qx 'trustedusercakeys none' || die 'candidate sshd configuration permits trusted user CAs'
printf '%s\n' "$effective" | grep -qx 'forcecommand none' || die 'candidate sshd configuration has a global ForceCommand'

cp -p -- "$SSHD_CONFIG" "$backup"
chown --reference="$SSHD_CONFIG" "$candidate"
chmod --reference="$SSHD_CONFIG" "$candidate"
mv -f -- "$candidate" "$SSHD_CONFIG"
candidate=
if ! sshd -t; then
    mv -f -- "$backup" "$SSHD_CONFIG"
    backup=
    die 'live sshd validation failed; previous configuration restored'
fi

if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet sshd; then
        if ! systemctl reload sshd; then
            mv -f -- "$backup" "$SSHD_CONFIG"
            backup=
            systemctl reload sshd >/dev/null 2>&1 || true
            die 'failed to reload sshd; previous configuration restored'
        fi
    elif systemctl is-active --quiet ssh; then
        if ! systemctl reload ssh; then
            mv -f -- "$backup" "$SSHD_CONFIG"
            backup=
            systemctl reload ssh >/dev/null 2>&1 || true
            die 'failed to reload ssh; previous configuration restored'
        fi
    else
        printf '%s\n' 'warning: no active ssh/sshd systemd unit; valid config will apply on next daemon start' >&2
    fi
fi

                                                                             
                                                                               
                                                                                 
random_password=$(openssl rand -base64 48)
printf '%s:%s\n' "$SERVICE_USER" "$random_password" | chpasswd
random_password=

cleanup_sshd_files
candidate=
backup=
trap - EXIT HUP INT TERM

printf '%s\n' 'trusted-server secret control installed'
printf '%s\n' 'next: run dex-secretctl provision for each canonical miner address'

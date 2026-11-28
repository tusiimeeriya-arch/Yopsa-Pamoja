#!/bin/sh
# Harden a fresh Ubuntu 24.04 Vultr instance for Pamoja.
#
# Run once, as root, over SSH:
#   sudo DEPLOY_USER=pamoja-deploy sh scripts/harden-server.sh
#
# The script never disables root or password login until you confirm that a
# SECOND SSH session works as the new deploy user. Keep the first session open
# until the end. If anything goes wrong, the Vultr web console is the recovery
# path.
#
# Staging deliberately allows SSH from any address (mobile data changes IP).
# Production must set SSH_ALLOW_FROM to named admin addresses or a VPN CIDR.
set -eu

DEPLOY_USER="${DEPLOY_USER:-pamoja-deploy}"
SSH_ALLOW_FROM="${SSH_ALLOW_FROM:-any}"
SSH_PORT="${SSH_PORT:-22}"
SWAP_SIZE="${SWAP_SIZE:-2G}"

log() { printf '\n== %s\n' "$*"; }
die() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "run as root"
[ -r /etc/os-release ] || die "cannot read /etc/os-release"
. /etc/os-release
[ "${ID:-}" = "ubuntu" ] || die "this script targets Ubuntu"
[ "${VERSION_ID:-}" = "24.04" ] || die "this script targets Ubuntu 24.04 LTS (found ${VERSION_ID:-unknown})"

log "1/10 base packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq ca-certificates curl gnupg ufw fail2ban unattended-upgrades chrony jq openssh-server sudo

log "2/10 deploy user ${DEPLOY_USER}"
if ! id "$DEPLOY_USER" >/dev/null 2>&1; then
  adduser --disabled-password --gecos "" "$DEPLOY_USER"
fi
usermod -aG sudo "$DEPLOY_USER"
install -d -m 700 -o "$DEPLOY_USER" -g "$DEPLOY_USER" "/home/${DEPLOY_USER}/.ssh"
# The key that already logs in as root is the operator's public key. Copy it;
# no private key ever leaves the operator's machine.
if [ ! -s "/home/${DEPLOY_USER}/.ssh/authorized_keys" ]; then
  if [ -s /root/.ssh/authorized_keys ]; then
    install -m 600 -o "$DEPLOY_USER" -g "$DEPLOY_USER" /root/.ssh/authorized_keys "/home/${DEPLOY_USER}/.ssh/authorized_keys"
  else
    die "no public key found; set up /root/.ssh/authorized_keys first"
  fi
fi
cat >/etc/sudoers.d/90-pamoja-deploy <<EOF
# Pamoja deploy user: full sudo, no password prompt (key-only SSH).
${DEPLOY_USER} ALL=(ALL) NOPASSWD:ALL
EOF
chmod 440 /etc/sudoers.d/90-pamoja-deploy
visudo -cf /etc/sudoers.d/90-pamoja-deploy >/dev/null || die "sudoers file is invalid"

log "3/10 unattended security upgrades"
cat >/etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
EOF
cat >/etc/apt/apt.conf.d/52-pamoja-unattended <<'EOF'
// Security updates only; reboot in a chosen window, not unattended.
Unattended-Upgrade::Allowed-Origins {
  "${distro_id}:${distro_codename}-security";
  "${distro_id}ESMApps:${distro_codename}-apps-security";
  "${distro_id}ESM:${distro_codename}-infra-security";
};
Unattended-Upgrade::Automatic-Reboot "false";
Unattended-Upgrade::Remove-Unused-Dependencies "true";
EOF
systemctl enable --now unattended-upgrades >/dev/null

log "4/10 SSH: keys only, no root, no password"
install -d -m 755 /etc/ssh/sshd_config.d
cat >/etc/ssh/sshd_config.d/99-pamoja-hardening.conf <<EOF
# Pamoja: key-only SSH. Password and root login are disabled after the
# second-session check below. Do not edit while a session is at risk.
Port ${SSH_PORT}
PermitRootLogin no
PasswordAuthentication no
KbdInteractiveAuthentication no
ChallengeResponseAuthentication no
PubkeyAuthentication yes
PermitEmptyPasswords no
MaxAuthTries 3
LoginGraceTime 30
ClientAliveInterval 300
ClientAliveCountMax 2
X11Forwarding no
AllowAgentForwarding no
AllowTcpForwarding no
AllowUsers ${DEPLOY_USER}
EOF
sshd -t || die "sshd configuration is invalid; nothing was reloaded"

log "5/10 firewall (ufw)"
ufw --force reset >/dev/null
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
if [ "$SSH_ALLOW_FROM" = "any" ]; then
  ufw allow "${SSH_PORT}/tcp" comment "SSH (staging: any address)" >/dev/null
else
  ufw allow from "$SSH_ALLOW_FROM" to any port "$SSH_PORT" proto tcp comment "SSH admin only" >/dev/null
fi
ufw allow 80/tcp comment "HTTP (ACME + redirect)" >/dev/null
ufw allow 443/tcp comment "HTTPS" >/dev/null
ufw allow 443/udp comment "HTTP/3" >/dev/null
ufw --force enable >/dev/null
ufw status verbose

log "6/10 fail2ban"
cat >/etc/fail2ban/jail.d/pamoja.local <<'EOF'
[DEFAULT]
bantime = 1h
findtime = 10m
maxretry = 5

[sshd]
enabled = true
port = ssh
backend = systemd
EOF
systemctl enable --now fail2ban >/dev/null
fail2ban-client status sshd || true

log "7/10 Docker from the official repository"
if ! command -v docker >/dev/null 2>&1; then
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  cat >/etc/apt/sources.list.d/docker.list <<EOF
deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable
EOF
  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
fi
usermod -aG docker "$DEPLOY_USER"
install -d -m 755 /etc/docker
cat >/etc/docker/daemon.json <<'EOF'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "live-restore": true,
  "no-new-privileges": true
}
EOF
systemctl enable --now docker >/dev/null
systemctl restart docker
docker version --format '{{.Server.Version}}' | sed 's/^/docker server /'

log "8/10 journald and log hygiene"
install -d -m 755 /etc/systemd/journald.conf.d
cat >/etc/systemd/journald.conf.d/99-pamoja.conf <<'EOF'
[Journal]
SystemMaxUse=200M
SystemKeepFree=500M
MaxRetentionSec=30day
EOF
systemctl restart systemd-journald
# Application logs are JSON with ids only (see server/src/audit.js and the
# request logger); this is the reminder to keep it that way.
printf 'Logs must not contain full names, phone numbers, national ids, addresses, passwords or tokens.\n'

log "9/10 time sync"
systemctl disable --now systemd-timesyncd >/dev/null 2>&1 || true
cat >/etc/chrony/chrony.conf <<'EOF'
pool 2.ubuntu.pool.ntp.org iburst
pool ntp.ubuntu.com iburst
driftfile /var/lib/chrony/chrony.drift
makestep 1.0 3
rtcsync
EOF
systemctl enable --now chrony >/dev/null
chronyc tracking | sed -n '1,4p'

log "10/10 limited swap and kernel tuning"
if [ ! -f /swapfile ]; then
  fallocate -l "$SWAP_SIZE" /swapfile || dd if=/dev/zero of=/swapfile bs=1M count=2048 status=none
  chmod 600 /swapfile
  mkswap /swapfile >/dev/null
fi
if ! swapon --show=NAME --noheadings | grep -qx '/swapfile'; then
  swapon /swapfile
fi
grep -q '^/swapfile ' /etc/fstab || printf '/swapfile none swap sw 0 0\n' >>/etc/fstab
cat >/etc/sysctl.d/99-pamoja.conf <<'EOF'
vm.swappiness = 10
vm.vfs_cache_pressure = 50
EOF
sysctl --system >/dev/null
swapon --show

cat <<EOF

============================================================
 SECOND SESSION CHECK - do this before closing anything
============================================================
Open a NEW terminal and run:

  ssh ${DEPLOY_USER}@<server-ip> 'sudo -n true && echo SECOND_SESSION_OK'

You must see SECOND_SESSION_OK. Keep this first session open as the recovery
path. Then run:

  sudo systemctl reload ssh

and confirm the second session still works. If it does not, the first session
is still root: revert with

  rm /etc/ssh/sshd_config.d/99-pamoja-hardening.conf && systemctl reload ssh

Paste the output of these back to the deployment record:

  sudo ufw status verbose
  sudo sshd -T | grep -E '^(permitrootlogin|passwordauthentication|pubkeyauthentication|allowusers|maxauthtries) '
  docker version
  timedatectl | sed -n '1,6p'
  swapon --show
  sudo fail2ban-client status sshd
============================================================
EOF

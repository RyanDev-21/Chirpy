#!/usr/bin/env bash
# One-time setup of a fresh Ubuntu 24.04 server for Chirpy. Run as the
# admin user (Azure: azureuser) with sudo:   bash setup.sh
# Safe to run again. Afterwards: README.md, "Start Chirpy".
set -euo pipefail

if [ "$(id -u)" -eq 0 ]; then echo "run as your normal user (it uses sudo)"; exit 1; fi
if ! ls ~/.ssh/authorized_keys >/dev/null 2>&1; then
  echo "no SSH key in ~/.ssh/authorized_keys: password logins are about to be turned off, so stop here"; exit 1
fi

echo "== system updates"
sudo apt-get update -q
sudo DEBIAN_FRONTEND=noninteractive apt-get -y -q upgrade
sudo DEBIAN_FRONTEND=noninteractive apt-get -y -q install \
  docker.io docker-compose-v2 git ufw fail2ban unattended-upgrades gnupg

echo "== automatic security updates (reboots at 04:00 Bangkok when needed)"
sudo tee /etc/apt/apt.conf.d/20auto-upgrades >/dev/null <<'CONF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
CONF
sudo tee /etc/apt/apt.conf.d/52chirpy-reboot >/dev/null <<'CONF'
Unattended-Upgrade::Automatic-Reboot "true";
Unattended-Upgrade::Automatic-Reboot-Time "21:00";
CONF

echo "== SSH: keys only, no root"
sudo tee /etc/ssh/sshd_config.d/50-chirpy.conf >/dev/null <<'CONF'
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
CONF
sudo systemctl reload ssh

echo "== firewall: SSH, HTTP, HTTPS only"
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 443/udp
sudo ufw --force enable

echo "== fail2ban (blocks repeated failed SSH logins)"
sudo systemctl enable --now fail2ban

echo "== 2 GB swap (a safety net on 1 GB of RAM)"
if [ ! -f /swapfile ]; then
  sudo fallocate -l 2G /swapfile
  sudo chmod 600 /swapfile
  sudo mkswap /swapfile >/dev/null
  sudo swapon /swapfile
  echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab >/dev/null
fi
echo 'vm.swappiness=10' | sudo tee /etc/sysctl.d/90-chirpy.conf >/dev/null
sudo sysctl -q --system

echo "== Docker"
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER"

echo "== nightly backup (02:30 Bangkok)"
dir=$(cd "$(dirname "$0")" && pwd)
sudo tee /etc/systemd/system/chirpy-backup.service >/dev/null <<UNIT
[Unit]
Description=Chirpy database backup to R2
[Service]
Type=oneshot
User=$USER
ExecStart=$dir/backup.sh
UNIT
sudo tee /etc/systemd/system/chirpy-backup.timer >/dev/null <<'UNIT'
[Unit]
Description=Nightly Chirpy database backup
[Timer]
OnCalendar=*-*-* 19:30:00 UTC
Persistent=true
[Install]
WantedBy=timers.target
UNIT
sudo systemctl daemon-reload
sudo systemctl enable --now chirpy-backup.timer

echo
echo "Done. Log out and back in (for the docker group), then follow README.md."
echo "Fresh passwords for .env, if you need them:"
echo "  POSTGRES_PASSWORD=$(openssl rand -hex 24)"
echo "  REDIS_PASSWORD=$(openssl rand -hex 24)"

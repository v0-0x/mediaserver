#!/usr/bin/env bash
# Installs mediaserver on a Raspberry Pi (Pi 4 or 5, Raspberry Pi OS 64-bit
# best; 32-bit works too) or any other Linux computer, as a service that
# starts with it. Run it as the user it is to run as (not root: it asks for
# sudo when it needs it):
#
#   curl -fsSL https://raw.githubusercontent.com/v0-0x/mediaserver/HEAD/packaging/install.sh | bash
#
# or, in the folder of a mediaserver-<version>-linux-<arch>.tar.gz you
# unpacked: ./install.sh (it installs the mediaserver next to it).
#
#   --beta         the newest beta too (and its updates follow the betas)
#   --preview      the newest preview of the next version (mediaserver 2.0)
#                  too, and its updates follow them
#   --no-ffmpeg    don't install ffmpeg (needed to convert the videos a
#                  device can't play as they are, and for downloads)
#   --no-ani-cli   don't install ani-cli (for the episodes the Aniko apps
#                  download onto the server)
#   --uninstall    remove the program and its service (its data stays)
#   --purge        the same, and its data (accounts, history, settings)
#
# The program goes in ~/.local/bin/mediaserver (it replaces it itself when
# it updates), its data in ~/.local/share/mediaserver (anime/ for the Aniko
# apps' side, movies/ for the Moviko apps'), its service in
# /etc/systemd/system/mediaserver.service. A computer that ran Aniko Hub
# moves to mediaserver: its service is replaced, its data moves into
# anime/ (its library, AniList logins, devices and password stay).
set -euo pipefail

# All in a function, run at the end: piped from curl, a download cut short
# runs nothing.
main() {
REPO=v0-0x/mediaserver
beta=0 preview=0 ffmpeg=1 anicli=1 uninstall=0 purge=0
for arg in "$@"; do
  case $arg in
    --beta) beta=1 ;;
    --preview) beta=1 preview=1 ;;
    --no-ffmpeg) ffmpeg=0 ;;
    --no-ani-cli) anicli=0 ;;
    --uninstall) uninstall=1 ;;
    --purge) uninstall=1 purge=1 ;;
    -h | --help)
      sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "install.sh: unknown option $arg (see --help)" >&2
      exit 2
      ;;
  esac
done

say() { printf '\033[1m%s\033[0m\n' "$*"; }
die() {
  printf '\033[31m%s\033[0m\n' "$*" >&2
  exit 1
}

if [ "$(id -u)" -eq 0 ]; then
  die "Run this as the user mediaserver is to run as (pi, say), not as root or with sudo: it asks for sudo when it needs it."
fi
[ "$(uname -s)" = Linux ] || die "mediaserver is for Linux (a Raspberry Pi, say). On Windows and macOS, install Aniko and Moviko themselves."
command -v curl > /dev/null || die "This needs curl: sudo apt-get install -y curl"

user=$(id -un)
share="${XDG_DATA_HOME:-$HOME/.local/share}"
bin="$HOME/.local/bin/mediaserver"
data="$share/mediaserver"
unit=/etc/systemd/system/mediaserver.service
has_systemd=0
if command -v systemctl > /dev/null && [ -d /run/systemd/system ]; then
  has_systemd=1
fi

# Aniko Hub's service and program (kumo-hub: its name before Aniko's
# rename), which mediaserver replaces.
remove_old_hub() {
  for name in aniko-hub kumo-hub; do
    if [ "$has_systemd" = 1 ] && [ -f "/etc/systemd/system/$name.service" ]; then
      say "Removing Aniko Hub's service ($name): mediaserver takes its place…"
      sudo systemctl disable --now "$name" || true
      sudo rm -f "/etc/systemd/system/$name.service"
      sudo systemctl daemon-reload
    fi
    rm -f "$HOME/.local/bin/$name" "$HOME/.local/bin/$name.new"
  done
}

if [ "$uninstall" = 1 ]; then
  if [ "$has_systemd" = 1 ] && [ -f "$unit" ]; then
    say "Stopping mediaserver's service…"
    sudo systemctl disable --now mediaserver || true
    sudo rm -f "$unit"
    sudo systemctl daemon-reload
  fi
  remove_old_hub
  rm -f "$bin" "$bin.new"
  if [ "$purge" = 1 ]; then
    rm -rf "$data"
    # Links left where Aniko Hub's data was.
    for name in aniko-hub kumo-hub; do
      if [ -L "$share/$name" ]; then rm -f "$share/$name"; fi
    done
    say "mediaserver is removed, with its data."
  else
    say "mediaserver is removed. Its data stays in $data (--purge removes it)."
  fi
  exit 0
fi

# The program for this processor. A static program: on a 64-bit kernel the
# 64-bit one runs even with a 32-bit system around it.
case $(uname -m) in
  aarch64 | arm64) arch=arm64 ;;
  armv7l | armv8l) arch=arm ;;
  x86_64 | amd64) arch=amd64 ;;
  armv6l) die "This Raspberry Pi (ARMv6: a Pi 1 or Zero) is too slow for mediaserver: use a Pi 3, 4 or 5." ;;
  *) die "mediaserver isn't made for this processor ($(uname -m))." ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
here=$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2> /dev/null && pwd || true)
prerelease=0
if [ -n "$here" ] && [ -x "$here/mediaserver" ] && [ -f "$here/mediaserver.service" ]; then
  # Run from an unpacked archive.
  src=$here
  version=$("$src/mediaserver" --version | awk '{ print $NF }')
  say "Installing mediaserver $version from $src"
else
  command -v python3 > /dev/null || die "This needs python3 (to read GitHub's list of releases): sudo apt-get install -y python3"
  say "Looking for the newest mediaserver…"
  curl -fsSL -H "Accept: application/vnd.github+json" \
    "https://api.github.com/repos/$REPO/releases?per_page=100" -o "$tmp/releases.json" ||
    die "Couldn't read mediaserver's releases on GitHub (no internet?)."
  # <version> <1 if a beta> <address> <sha256:… or empty>
  pick=$(python3 -I - "$tmp/releases.json" "$beta" "$arch" "$preview" << 'PY'
import json, re, sys
releases = json.load(open(sys.argv[1]))
beta, arch = sys.argv[2] == "1", sys.argv[3]
# The previews of the next version are tagged apart (server-next-v…).
tag = r"server-(next-)?v\d+(\.\d+)*" if sys.argv[4] == "1" else r"server-v\d+(\.\d+)*"
servers = [r for r in releases if not r.get("draft") and re.fullmatch(tag, r["tag_name"])]
stable = [r for r in servers if not r.get("prerelease")]
# Betas too when asked, or when there's no stable release yet.
pool = servers if beta or not stable else stable
if not pool:
    sys.exit("no mediaserver release yet")
ver = lambda r: r["tag_name"].rsplit("v", 1)[1]
best = max(pool, key=lambda r: tuple(int(n) for n in ver(r).split(".")))
version = ver(best)
name = "mediaserver-%s-linux-%s.tar.gz" % (version, arch)
asset = next((a for a in best.get("assets", []) if a["name"] == name), None)
if asset is None:
    sys.exit("mediaserver %s has no %s" % (version, name))
print(version, "1" if best.get("prerelease") else "0", asset["browser_download_url"], asset.get("digest") or "")
PY
  ) || die "Couldn't find a mediaserver to install."
  read -r version prerelease url digest <<< "$pick"
  if [ "$prerelease" = 1 ]; then
    say "Downloading mediaserver $version (beta) for $arch…"
  else
    say "Downloading mediaserver $version for $arch…"
  fi
  curl -fL --progress-bar -o "$tmp/mediaserver.tar.gz" "$url" || die "The download failed."
  if [[ $digest == sha256:* ]]; then
    echo "${digest#sha256:}  $tmp/mediaserver.tar.gz" | sha256sum -c --quiet - || die "The download is damaged (its checksum doesn't match): try again."
  fi
  mkdir "$tmp/x"
  tar -xzf "$tmp/mediaserver.tar.gz" -C "$tmp/x"
  src=$(find "$tmp/x" -mindepth 1 -maxdepth 1 -type d | head -1)
  [ -x "$src/mediaserver" ] || die "The archive has no mediaserver program."
fi

# ffmpeg converts the videos a device can't play as they are (on a Pi with
# its video encoder, h264_v4l2m2m) and saves downloads; ani-cli finds the
# episodes the Aniko apps download onto the server.
want=()
if [ "$ffmpeg" = 1 ] && ! command -v ffmpeg > /dev/null; then want+=(ffmpeg); fi
if [ ${#want[@]} -gt 0 ]; then
  if command -v apt-get > /dev/null; then
    say "Installing ${want[*]}…"
    sudo apt-get update -qq && sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${want[@]}" < /dev/null ||
      echo "${want[*]} didn't install: mediaserver still shares its libraries, without converting or downloading." >&2
  else
    echo "Install ${want[*]} with your system's packages: mediaserver converts videos and takes downloads with them." >&2
  fi
fi
if [ "$anicli" = 1 ] && ! command -v ani-cli > /dev/null && [ ! -x "$HOME/.local/bin/ani-cli" ]; then
  say "Installing ani-cli…"
  mkdir -p "$HOME/.local/bin"
  if curl -fsSL -o "$tmp/ani-cli" https://raw.githubusercontent.com/pystardust/ani-cli/master/ani-cli && head -1 "$tmp/ani-cli" | grep -q '^#!'; then
    install -m 755 "$tmp/ani-cli" "$HOME/.local/bin/ani-cli"
  else
    echo "ani-cli didn't download: the Aniko apps can't download episodes onto the server until it's installed." >&2
  fi
fi

was_running=0
if [ "$has_systemd" = 1 ] && systemctl is-active --quiet mediaserver 2> /dev/null; then
  was_running=1
fi
# Aniko Hub stops before mediaserver starts: they answer on the same port.
remove_old_hub

mkdir -p "$(dirname "$bin")" "$data"
chmod 700 "$data" # AniList logins are in it
# Next to the old one, then over it: a running mediaserver goes on until it
# restarts.
cp "$src/mediaserver" "$bin.new"
chmod 755 "$bin.new"
mv -f "$bin.new" "$bin"
# Its first run moves Aniko Hub's data into anime/ (this one, or the
# service's).
if [ "$prerelease" = 1 ] || [ "$beta" = 1 ]; then
  "$bin" --data-dir "$data" --set-channel beta > /dev/null
fi

host=$(hostname)
[[ $host == *.* ]] || host="$host.local"
if [ "$has_systemd" = 1 ]; then
  say "Installing the service (mediaserver)…"
  path="$HOME/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
  sed -e "s|@USER@|$user|g" -e "s|@BIN@|$bin|g" -e "s|@DATA@|$data|g" -e "s|@PATH@|$path|g" "$src/mediaserver.service" > "$tmp/mediaserver.service"
  sudo install -m 644 "$tmp/mediaserver.service" "$unit"
  sudo systemctl daemon-reload
  sudo systemctl enable mediaserver > /dev/null 2>&1
  if [ "$was_running" = 1 ]; then
    sudo systemctl restart mediaserver
  else
    sudo systemctl start mediaserver
  fi
  sleep 2
  systemctl is-active --quiet mediaserver || die "mediaserver didn't start: journalctl -u mediaserver -n 50 says why."
else
  echo "No systemd here: start it with  $bin" >&2
fi

echo
say "mediaserver $version is installed."
echo "  Dashboard:   http://$host:43211  (or this computer's address, port 43211)"
echo "  Movies & TV: http://$host:43311  (after signing in to the dashboard)"
echo "  Music:       on the dashboard (the Utako apps find it at port 43411)"
code=$("$bin" --data-dir "$data" --setup-code || true)
if [[ $code == "Setup code: "* ]]; then
  echo "  ${code}  (to set the dashboard's password; ~/.local/bin/mediaserver --setup-code shows it again)"
fi
if [ "$has_systemd" = 1 ]; then
  echo "  Logs:        journalctl -u mediaserver -f"
fi
echo
echo "On your computers, TVs and phones, turn on Settings › Other devices (Aniko) and Settings › Devices (Moviko): mediaserver shows up there."
}

main "$@"

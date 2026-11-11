# Docker Secrets Engine Setup

Guide to installing the official [Docker Secrets Engine](https://github.com/docker/secrets-engine)
on Docker Engine (CE) and connecting it to the MCP Gateway.

Docker Desktop ships the Secrets Engine already. On plain Docker Engine you
install it yourself. This guide uses the signed standalone Linux release and was
tested with:

- Arch Linux, Docker Engine 29.9.3
- Secrets Engine v0.11.2
- `docker-mcp` built from this repository

## How the pieces fit together

| Component | Purpose |
| --- | --- |
| `/usr/bin/docker-secrets-engine` | Daemon and CLI, shipped as one binary |
| `docker-pass` symlinks | Exposes `docker pass`, which `docker mcp secret` shells out to |
| `docker-secrets-engine.service` | Per-user systemd service that runs the daemon |
| NRI plugin | Lets `dockerd` resolve `se://` references at container start |
| `docker mcp` | Reads and writes secrets through the engine client |

The standalone daemon listens on the abstract Unix socket
`@docker-secrets-engine/<uid>/daemon.sock`. Docker Desktop instead exposes
`~/.cache/docker-secrets-engine/engine.sock`.

`docker mcp` used to pin the Desktop socket, which made `docker mcp secret ls`,
OAuth token reads, and gateway secret verification fail on Docker CE. This
repository now dials the standalone socket first and falls back to the Desktop
socket (see `fix(secret): support standalone secrets engine socket`). Build the
plugin from this repository before following the MCP section below.

## Prerequisites

- Docker Engine **29.2.0 or newer** (needed for NRI secret injection)
- A systemd **user** session
- D-Bus plus a Secret Service provider (GNOME Keyring or KWallet). Secrets are
  stored through `org.freedesktop.secrets`, and the daemon exits if none is
  available.
- `sudo` for the system-wide steps

On Arch Linux, `secret-tool` comes from `libsecret`.

Check the keyring before installing:

```bash
secret-tool store --label=probe probe key <<< value \
  && secret-tool clear probe key \
  && echo "Secret Service OK"
```

The keyring must be unlocked in your user session.

## Step 1 — Download and verify the release

The `.sha256` file covers the **extracted binary** (`secrets-engine`), not the
tarball, so extract before checking. Replace `amd64` with `arm64` on ARM hosts.

```bash
mkdir -p ~/src/docker-secrets-engine
cd ~/src/docker-secrets-engine

BASE=https://github.com/docker/secrets-engine/releases/download/v0.11.2
curl -fsSLO "$BASE/secrets-engine-linux_amd64.tar.gz"
curl -fsSLO "$BASE/secrets-engine-linux_amd64.sha256"
curl -fsSLO "$BASE/secrets-engine-linux_amd64.sigstore.json"

tar xzf secrets-engine-linux_amd64.tar.gz
sha256sum --check secrets-engine-linux_amd64.sha256
```

Expected output:

```text
secrets-engine: OK
```

The tarball contains a single binary named `secrets-engine`.

Optionally verify the sigstore signature. On Arch Linux `cosign` is in the
`extra` repository:

```bash
sudo pacman -S --needed cosign

cosign verify-blob secrets-engine \
  --bundle secrets-engine-linux_amd64.sigstore.json \
  --certificate-identity "https://github.com/docker/secrets-engine/.github/workflows/sign-release.yml@refs/tags/v0.11.2" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Step 2 — Install the engine binary

```bash
sudo install -D -m 0755 secrets-engine /usr/bin/docker-secrets-engine
sudo install -d -m 0755 /usr/libexec/docker-secrets-engine/plugins

/usr/bin/docker-secrets-engine --help
```

The binary should report `Usage: secrets-engine [flags]` and a `setup`
subcommand. Keep the plugin directory owned by root and not writable by others.

## Step 3 — Expose `docker-pass`

`docker mcp secret set` and `docker mcp secret rm` shell out to `docker pass`.
Install the binary as a CLI plugin, and as `docker-pass` for direct use:

```bash
sudo install -d -m 0755 /usr/libexec/docker/cli-plugins
sudo ln -sf /usr/bin/docker-secrets-engine /usr/libexec/docker/cli-plugins/docker-pass
sudo ln -sf /usr/bin/docker-secrets-engine /usr/bin/docker-pass

docker pass version
```

Expected output:

```text
Version: v0.11.2
```

## Step 4 — Run the daemon

The official packages install a per-user systemd unit. This is the same unit:

```bash
sudo tee /usr/lib/systemd/user/docker-secrets-engine.service >/dev/null <<'EOF'
[Unit]
Description=Docker secrets engine daemon
Documentation=https://docs.docker.com/

[Service]
Type=simple
ExecStart=/usr/bin/docker-secrets-engine
Restart=on-failure
RestartSec=2s

# Ensure a crash never leaves a core dump on disk for a same-UID peer to read.
LimitCORE=0

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now docker-secrets-engine
systemctl --user status docker-secrets-engine --no-pager
```

Confirm the daemon is listening:

```bash
ss -xl | grep docker-secrets
```

Expected, where `1000` is your UID:

```text
u_str LISTEN 0 4096 @docker-secrets-engine/1000/daemon.sock
```

If the service must run before you log in (for example, to serve containers
started at boot), enable lingering:

```bash
loginctl enable-linger "$USER"
```

## Step 5 — Wire the NRI plugin into dockerd

NRI is what lets a container resolve `se://` references at start time.

```bash
sudo install -d -m 0755 \
  /usr/libexec/docker/nri-plugins \
  /var/log/docker-secrets-engine-plugins \
  /etc/docker/nri/conf.d

# Register the plugin with dockerd.
sudo ln -sf /usr/bin/docker-secrets-engine \
  /usr/libexec/docker/nri-plugins/10-secrets-engine

# The root-run plugin needs to know which user's daemon to contact.
printf 'uid: %s\n' "$(id -u)" | sudo tee /etc/docker/nri/conf.d/10-secrets-engine.conf
```

Enable NRI in `/etc/docker/daemon.json`. The `setup nri-config` helper only sets
`nri-opts.enable` and preserves every other key, but back up the file first:

```bash
sudo touch /etc/docker/daemon.json
sudo cp -p /etc/docker/daemon.json /etc/docker/daemon.json.bak

docker-secrets-engine setup nri-config enable \
  < /etc/docker/daemon.json > /tmp/daemon.json
sudo install -m 0644 /tmp/daemon.json /etc/docker/daemon.json

sudo systemctl restart docker
```

If `/etc/docker/daemon.json` is not readable by your user, feed it through
`sudo`:

```bash
sudo cat /etc/docker/daemon.json \
  | docker-secrets-engine setup nri-config enable \
  | sudo tee /tmp/daemon.json >/dev/null
```

Docker's default NRI plugin directory is `/usr/libexec/docker/nri-plugins`, so
no `plugin-path` is needed. If the daemon does not load the plugin, set it
explicitly:

```json
{
  "nri-opts": {
    "enable": true,
    "plugin-path": "/usr/libexec/docker/nri-plugins"
  }
}
```

The plugin targets one user's daemon per host. That is why the `uid` file
matters, and why multi-user hosts are not supported by the experimental CE
integration.

## Verify the installation

```bash
docker pass set probe=s3cr3t
docker pass get probe
docker pass ls

docker run --rm -e P=se://probe alpine sh -c 'echo "$P"'
# expected: s3cr3t

docker pass rm probe
```

Check the daemon and plugin logs:

```bash
journalctl --user -u docker-secrets-engine --since "5 min ago" --no-pager
sudo journalctl -u docker --since "5 min ago" | grep -i -E 'nri|secrets'
```

The NRI plugin writes to `/var/log/docker-secrets-engine-plugins/nri.log`.

## Use with the MCP Gateway

Build and install the plugin from this repository so it dials the standalone
socket:

```bash
go build -o /tmp/docker-mcp ./cmd/docker-mcp
install -m 0755 /tmp/docker-mcp ~/.docker/cli-plugins/docker-mcp
docker mcp version
```

`docker mcp secret set` reads the value from stdin:

```bash
docker mcp secret set github.personal_access_token
# paste the token, then press Ctrl-D
```

Then confirm the engine is reachable:

```bash
docker mcp secret ls
```

Import a profile and start the gateway:

```bash
docker mcp profile import ./output.yaml
docker mcp profile show dev_profile
docker mcp gateway run --profile dev_profile
```

The profile's `secrets` entries must exist in the engine. With the patched
plugin, the gateway verifies them through the standalone socket and emits
`se://docker/mcp/<name>` references to the servers.

## Troubleshooting

### `docker mcp secret ls` reports `secrets engine is not available`

The installed `docker-mcp` still points at Docker Desktop's `engine.sock`, or
the daemon is not running.

```bash
systemctl --user status docker-secrets-engine --no-pager
ss -xl | grep docker-secrets
```

Rebuild `docker-mcp` from this repository (see above) if the socket is present
but the client still fails.

### `docker pass` says `has not been installed`

Docker cannot see the CLI plugin. Check the symlink and the resolved version:

```bash
ls -l /usr/libexec/docker/cli-plugins/docker-pass
docker pass version
```

### The daemon exits immediately or reports a keyring error

The daemon needs D-Bus and an unlocked Secret Service provider. Verify the
session bus and the keyring:

```bash
echo "$DBUS_SESSION_BUS_ADDRESS"
secret-tool search --all service probe
```

On KWallet, make sure the wallet is unlocked. Headless hosts without a keyring
are not supported.

### Containers do not resolve `se://` references

Check each link in the chain:

1. `nri-opts.enable` is `true` in `/etc/docker/daemon.json`.
2. The plugin symlink exists:
   `ls -l /usr/libexec/docker/nri-plugins/10-secrets-engine`
3. The uid file matches the user running the daemon:
   `cat /etc/docker/nri/conf.d/10-secrets-engine.conf`
4. dockerd was restarted after the change:
   `sudo systemctl restart docker`
5. The logs show no plugin errors:
   `sudo journalctl -u docker --since "5 min ago" | grep -i nri`

### The service stops after logging out

Enable lingering for the user that owns the daemon:

```bash
loginctl enable-linger "$USER"
```

## Uninstall

```bash
systemctl --user disable --now docker-secrets-engine
sudo rm -f /usr/lib/systemd/user/docker-secrets-engine.service

docker-secrets-engine setup nri-config disable \
  < /etc/docker/daemon.json > /tmp/daemon.json
sudo install -m 0644 /tmp/daemon.json /etc/docker/daemon.json

sudo rm -f /usr/libexec/docker/cli-plugins/docker-pass \
           /usr/libexec/docker/nri-plugins/10-secrets-engine \
           /etc/docker/nri/conf.d/10-secrets-engine.conf \
           /usr/bin/docker-pass \
           /usr/bin/docker-secrets-engine

sudo rm -rf /var/log/docker-secrets-engine-plugins \
            /usr/libexec/docker-secrets-engine

sudo systemctl restart docker
```

Removing the binary does not delete secrets already stored in the keyring.

## References

- [docker/secrets-engine](https://github.com/docker/secrets-engine)
- [`docker pass` reference](https://docs.docker.com/reference/cli/docker/pass/)
- [`dockerd` reference](https://docs.docker.com/reference/cli/dockerd/)
- [NRI (Node Resource Interface)](https://github.com/containerd/nri)
- [OAuth DCR with Docker CE](oauth-ce-mode.md)

# Codex Inspection Timer

Standalone server-side Codex account inspection for CLIProxyAPI. It does not use Usage Service.

Safety rule: the script never deletes accounts. It only auto-disables quota-exhausted accounts and auto-enables disabled accounts whose weekly quota is available again. Invalid accounts are logged as delete suggestions.

## Install

Copy files to the server:

```bash
mkdir -p /root/cliproxyapi/tools/codex-inspection
cp tools/codex-inspection/codex_inspect.py /root/cliproxyapi/tools/codex-inspection/codex_inspect.py
chmod +x /root/cliproxyapi/tools/codex-inspection/codex_inspect.py

mkdir -p /etc/cliproxyapi
cp tools/codex-inspection/codex-inspection.env.example /etc/cliproxyapi/codex-inspection.env
nano /etc/cliproxyapi/codex-inspection.env

cp tools/codex-inspection/systemd/codex-inspection.service /etc/systemd/system/codex-inspection.service
cp tools/codex-inspection/systemd/codex-inspection.timer /etc/systemd/system/codex-inspection.timer
systemctl daemon-reload
systemctl enable --now codex-inspection.timer
```

Set `CPA_MANAGEMENT_KEY` to the plaintext management key used to login to the Web UI, not the bcrypt hash stored in `config.yaml`.

## Change interval

Edit `/etc/systemd/system/codex-inspection.timer`:

```ini
OnUnitActiveSec=30min
```

Then reload:

```bash
systemctl daemon-reload
systemctl restart codex-inspection.timer
```

## Run once

```bash
systemctl start codex-inspection.service
journalctl -u codex-inspection.service --no-pager -n 100
```

## Dry run

Set this in `/etc/cliproxyapi/codex-inspection.env`:

```bash
CODEX_INSPECTION_DRY_RUN=true
```


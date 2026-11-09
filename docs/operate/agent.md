# On-site agent

The agent runs on a Raspberry-Pi-class box (or the guard PC) on the site LAN. It hears plate cameras and readers, opens the gate or door, and **keeps deciding when the internet is down**. Source and full reference: the `openbekci-agent` repository.

- Outbound connections only: a WebSocket to your server (HTTPS long-poll if a proxy blocks upgrades). No inbound port from outside the LAN.
- Offline-first: it holds an encrypted copy of the allow-list, decides locally, logs every decision and syncs when the server is reachable again.
- One static binary, no CGO: `linux/arm64`, `linux/amd64`, `windows/amd64`.
- The server must be reachable over valid HTTPS (see [No public certificate](../install/compose.md#no-public-certificate)). For a private CA, point the agent at it with `ca_file` or `OPENBEKCI_AGENT_CA_FILE`.

## Install and enrol

Download the archive for your platform, install the binary, the example config (`agent.yaml`) and the systemd unit, run as a dedicated service user (add it to `dialout` for USB/serial relay boards).

In the web admin (site access settings) create an agent: you get a one-time token valid for 15 minutes. Then, as the service user:

```sh
openbekci-agent enrol --server https://bekci.example.org --token obk_enr_XXXX
systemctl enable --now openbekci-agent
```

The long-lived credential is stored in the agent's state directory (mode 0600), never in `agent.yaml`. The token rotates by itself well before its expiry.

## Everyday commands

```sh
openbekci-agent status           # enrolment, allow-list version, queue depth, drivers, last contact
openbekci-agent config check     # validates agent.yaml and builds every driver without touching hardware
openbekci-agent rotate-token
openbekci-agent run --mock       # try it with a scriptable fake camera and relay, no hardware
```

Updating: replace the binary and restart the service; the state directory is compatible within a protocol version.

## What happens offline

- Decisions come from the last allow-list, in the agent's own clock and the site timezone; every decision (allow and deny) is logged to a local queue that survives power loss.
- A credential that **expires** while offline is denied as it would be online. A credential that is **revoked** while offline keeps working until contact returns, up to the grace period: the accepted trade-off of an offline-first gate.
- **Grace period:** the site setting `offline_grace_hours` (default 72 hours) counts from the last contact with the server. After it the agent stops: it opens nothing and logs `deny / agent_stopped`.
- When the connection returns, configuration and allow-list sync first, then the queued events in order. Duplicates are harmless.
- Guard "open" commands only work on the live connection and expire within seconds.
- Revoking the agent in the web admin wipes its state; it exits and refuses to start until enrolled again.
- Only plates and hashes are stored (plates encrypted at rest); logs contain ids, never plates.

## Hardware

Drivers are declared in `agent.yaml`. Today: `relay` (a pulse on a relay board, always switched off again, with a minimum interval between pulses) and `platecam`. Camera brand adapters are added when a site's first camera is known; adding a driver is one line plus a package in the agent repository.

# On-site access agent fleet

The agent (`openbekci-agent` repository) runs on a small box on the site LAN and drives gates and doors. It only makes outbound connections to the server, decides at the gate from a locally stored allow-list, and keeps working when the internet is down.

## Rolling out

1. In the site's access settings create an agent; you get a one-time enrolment code (valid 15 minutes).
2. On the box, install the agent (systemd unit with its own user in the device groups it needs, such as `dialout`, or the Docker image with device passthrough) and run its enrol command with the code and your server URL. The agent stores a long-lived credential in a mode-600 file.
3. The server must be reachable over valid HTTPS from the box; for a private CA, install the CA on the box.
4. Test from the guard console: the "open" button must work within seconds, and the opening must appear in the log.

Behind a proxy set `OPENBEKCI_TRUSTED_PROXIES`, otherwise every enrolment shares one rate-limit budget (10 per minute).

## Day to day

- **Silent agents.** `last_seen` is shown in the site's access settings and platform admin (heartbeat every 30 s). Alert when an agent is silent for more than 15 minutes; the server also notifies the site manager after the site's own threshold.
- **Offline.** The agent keeps opening for the grace period (default 72 h, a site setting), then stops automatic opening. The guard's remote open fails visibly while it is offline. Logs sync with their original timestamps when it reconnects.
- **Revoking** a stolen or retired box: revoke it in the site's access settings. The credential dies at once; the agent wipes its allow-list when it next connects. A box that never reconnects holds at most a grace-period-old encrypted allow-list.
- **Updates.** Auto-update is opt-in (signature-verified, applied in the site's maintenance hour, previous binary kept). The server shows "update available" either way. Update a fleet in waves: one site, a day of observation, then the rest. Agents work with servers up to two minors apart; upgrade the server first.
- **Logs** carry event ids and access point ids, not plates (plates are personal data with their own retention).
- **Replacing a box:** create a new agent, enrol, then revoke the old one.

## Security checklist

No inbound ports from outside the LAN; camera push endpoints bound to the LAN interface with a shared secret; credential file mode 600; box disk encrypted where the hardware allows; time sync (NTP) working, since offline decisions use validity windows.

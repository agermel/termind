# TODO

## OpenClaw Auth Schemes

- Add a guarded expert-only fallback for Gateway shared token auth when an
  OpenClaw deployment explicitly requires `auth.token`.
- Add a guarded expert-only fallback for Gateway password auth when a deployment
  explicitly requires `auth.password`.
- Improve setup-code diagnostics for expired bootstrap tokens and mismatched
  OpenClaw Gateway URLs.
- Add a native OpenClaw termind plugin gateway method registered with
  `operator.write` for streaming diagnostics. Until then, use OpenClaw's
  built-in `agent` / `agent.wait` / `sessions.get` methods because unknown
  gateway methods default to `operator.admin`.
- Support TLS fingerprint pinning for self-hosted `wss://` gateways.
- Add clearer diagnostics for Tailscale / reverse-proxy auth failures once the
  OpenClaw side exposes those requirements in connect error details.

## OpenClaw Terminal Control

- Add a future `termind node` mode that connects to OpenClaw Gateway as a
  capability host, so OpenClaw can invoke this machine's terminal abilities via
  the standard node transport instead of a custom plugin-owned WebSocket.
- Start with conservative node capabilities such as `system.which` and guarded
  `system.run`, then evaluate richer terminal-specific abilities like shell
  snapshot, send input, and PTY session control.
- Keep a Termind OpenClaw plugin optional: use it only to register friendly tool
  schemas, commands, or OpenClaw-side UX around Termind. Do not make the plugin
  responsible for the primary Gateway connection unless OpenClaw's standard node
  path proves insufficient.

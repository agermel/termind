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

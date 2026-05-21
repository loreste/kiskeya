# Kiskeya

A lightweight, cross‑platform desktop **SIP softphone** built with [Wails v2](https://wails.io)
(Go backend + a dependency‑free HTML/CSS/JS frontend).

Kiskeya registers to a SIP/VoIP server (PBX, ITSP, Asterisk/FreeSWITCH, hosted
platforms, etc.) and lets you place and receive voice calls, with the NAT
handling a softphone behind a home/office router actually needs.

## Why Kiskeya?

Most usable SIP softphones are either closed‑source, ad‑supported, or heavyweight
Electron apps. Kiskeya is a small, fast, **MIT‑licensed** native app you can read,
audit, modify, and ship:

- **Self‑hostable & vendor‑neutral** — point it at any standards‑compliant SIP server.
- **Privacy‑respecting** — credentials live in the OS keychain; contacts and call
  history are stored locally (`~/.kiskeya`), never in the cloud.
- **Real NAT traversal** — registers from, and listens on, the same socket; learns
  its public address from the registrar (`rport`/`received`) and keeps the NAT
  pinhole open with periodic keepalives, so **inbound calls work from behind a
  home router** (validated against a live PBX).
- **Secure by option** — TLS signaling and SRTP (SDES) media encryption.

## Features

- SIP over **UDP, TCP, and TLS**; digest authentication; automatic re‑registration
  (honors the server‑granted `Expires`, handles `423 Interval Too Brief`, backs off
  on permanent failures).
- **Outbound and inbound** calls (dialog‑correct ACK/BYE/CANCEL).
- **Audio:** G.711 (PCMU/PCMA) over RTP, an adaptive jitter buffer with packet‑loss
  concealment, microphone mute, device selection, and live level meters.
- **DTMF:** in‑band RFC 4733 (`telephone-event`) sent from the keypad during a call.
- **Media encryption:** SRTP (SDES, `AES_CM_128_HMAC_SHA1_80`), negotiated per call.
- **NAT:** `rport` (RFC 3581), STUN, source‑port alignment, and a CRLF keepalive.
- **Local data:** contacts and call history (`~/.kiskeya`); SIP password stored in
  the OS keychain (macOS Keychain / Windows Credential Manager / Linux Secret Service).
- **UX:** dial pad, contacts, call history, diagnostics/SIP log, themes, and
  keyboard/screen‑reader accessibility.

## Platform support

| Platform | Status | Notes |
|----------|--------|-------|
| **macOS** | ✅ Supported | Universal binary (Apple Silicon + Intel). Currently the only platform we build and test on. |
| **Windows** | 🚧 Planned | Cross‑compiles today (MinGW), but not yet officially tested/supported. |
| **Linux** | 🚧 Planned | Builds via the GitHub Actions workflow / `build_linux_packages.sh`; not yet officially tested/supported. |

> Kiskeya runs on macOS today. Windows and Linux are on the roadmap — the build
> tooling already targets them, but they haven't been validated end‑to‑end yet.

## Requirements

- [Go](https://go.dev) 1.24+
- [Node.js](https://nodejs.org) 18+ (for the frontend build)
- The [Wails CLI](https://wails.io/docs/gettingstarted/installation):
  `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

## Building

```bash
# macOS (universal)
wails build -platform darwin/universal
# → build/bin/kiskeya.app

# Windows (cross-compiled from macOS with MinGW: brew install mingw-w64)
CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc GOOS=windows GOARCH=amd64 \
  wails build -platform windows/amd64
# → build/bin/kiskeya.exe
```

Linux must be built natively (CGO links GTK3/WebKit2GTK/ALSA — Wails cannot
cross‑compile Linux). Use `build_linux_packages.sh` on a Linux host, or push to
GitHub to let `.github/workflows/build.yml` build all three platforms.

## Development

```bash
wails dev
```

Runs a Vite dev server with hot reload and a Go bridge at `http://localhost:34115`.

## License

Kiskeya is released under the **MIT License** — see [`LICENSE`](LICENSE).

**Why a license at all?** Without an explicit license, code published online is
still "all rights reserved" by default copyright law: nobody may legally use,
copy, modify, or distribute it. Adding a license is what makes the project
genuinely open source. We chose **MIT** because it's short, permissive, and
widely understood — it lets anyone use Kiskeya (including commercially), modify
it, and redistribute it, as long as the copyright and license notice are kept.
That maximizes adoption and contribution while limiting our liability.

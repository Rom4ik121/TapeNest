# ADR 0001 — Background audio in Telegram WebView

- **Status:** Accepted (2026-09-25)
- **Context:** spec §3.1 #13 (MVP audio = `HTMLAudioElement`), §6, §19.5

## Context

WavePlayer runs inside Telegram's in-app WebView (WKWebView on iOS, Android
System WebView, Chromium/QtWebEngine on desktop). Web apps cannot declare
background-audio capabilities; the host app decides. Observed platform
behaviour:

| Client | Minimised Mini App (collapsed sheet) | Telegram in background / screen locked |
|---|---|---|
| iOS | usually continues | WKWebView is suspended → audio stops after a few seconds (host app has no audio background mode for web content) |
| Android | continues while the process lives | may continue; OS may kill the WebView under memory pressure |
| Desktop / Web | continues | continues |

There is no Telegram Mini Apps API for background playback or lock-screen
controls; Media Session API support in these WebViews is partial.

## Decision

1. Keep a **single `HTMLAudioElement` owned by a module-level engine** outside
   React, so navigation never interrupts playback (`src/entities/player/audioEngine.ts`).
2. **Don't fight the platform**: no silent-audio hacks, no keep-alive loops
   (fragile, battery-hostile, review risk).
3. **Make interruption cheap**: persist playback position with a 5 s debounce
   *and* forced saves on pause, track switch, `visibilitychange: hidden` and
   `pagehide` (`keepalive` fetch); the last session (queue, index, position) is
   stored locally and on the server (`PUT /tracks/:id/position`) and restored
   paused on next open (spec §6).
4. **Honest UI state**: `isPlaying` is driven by real media events; if
   `play()` is rejected (autoplay policy after resume) the player shows
   "tap to continue" instead of a fake playing state.
5. Post-MVP: Media Session metadata/actions where supported; Web Audio API for
   gapless playback (spec §4.2 trigger: complaints about gaps).

## Consequences

- On iOS, audio stops when Telegram goes to background — **documented platform
  limitation**; users resume from the exact position.
- Between tracks there is a micro-gap (HTMLAudioElement), accepted for MVP.

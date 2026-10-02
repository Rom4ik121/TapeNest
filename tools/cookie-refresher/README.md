# cookie-refresher (contract; sidecar not implemented yet)

Spec §5.3 describes a Playwright sidecar that logs in to each source every 6 h and stores fresh
cookies for yt-dlp. The consumer side in download-service is already implemented; the sidecar
itself is still to do (ADR 0008).

## Contract (what download-worker reads)

| Redis key | Value |
|---|---|
| `cookies:<source>` | Netscape `cookies.txt` content (`youtube`, `vk`, `rutube`) |
| `cookies:<source>:updated_at` | unix seconds of the refresh |

- The worker writes the jar to a private temp file (0600) for each attempt that needs cookies
  (escalation ladder) and deletes it afterwards.
- A jar older than `COOKIES_MAX_AGE` (default 12 h) is still used, but a warning is logged.
- A missing key means the worker downloads without cookies.

## Manual stand-in

```bash
# export cookies.txt from a browser session of a dedicated (non-personal) account, then:
download-worker -import-cookies youtube /path/to/cookies.txt
```

Use throwaway accounts only. Never commit cookie files.

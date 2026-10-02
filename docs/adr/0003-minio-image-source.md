# ADR 0003 — MinIO container image source

- **Status:** Accepted (2026-09-25)
- **Context:** spec §3.1 #10 (MinIO), §3.2 (pin by digest)

`minio/minio` and `minio/mc` were removed from Docker Hub (≈2026-09-12); the
upstream repository is archived (2026-04-25) and Community Edition is
source-only. Checked on 2026-09-25: Docker Hub and anonymous Quay pulls fail.

**Decision:** keep MinIO (the S3 API and minio-go stay as fixed in §3.1) but use
`pgsty/minio` — a maintained, MinIO-compatible community fork, multi-arch —
pinned by digest (`RELEASE.2026-08-04T00-00-00Z@sha256:b6bfe723…`). Buckets are
created via the console (:9001) in dev (spec §11); services create them at
startup in stage 2.

**Revisit** if the fork stalls: alternatives are building MinIO from source,
`cgr.dev/chainguard/minio`, or an S3-compatible replacement (Garage) — the
latter would be a §3.1 change and needs the owner's approval.

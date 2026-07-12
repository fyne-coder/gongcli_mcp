# Release Versioning

`gongctl` uses SemVer-style release tags.

- Version source for the next release: `VERSION`
- Git tag format: `vX.Y.Z`
- Release-candidate tag format: `vX.Y.Z-rcN`
- Pre-1.0 minor releases may still change CLI or MCP contracts.
- Release builds inject `version`, `commit`, and `date` into both `gongctl` and `gongmcp`.
- Docker images should be published with immutable version tags and pinned by digest for customer deployments.

## Local Checks

```bash
make build
bin/gongctl version
bin/gongmcp --db /path/to/gong.db
```

For production-readiness checks:

```bash
go version  # must be go1.26.5 or newer
go test -count=1 ./...
go vet ./...
make secret-scan
make sbom
make checksums
docker build -t gongctl:local .
docker build --target mcp -t gongctl:mcp-local .
docker build --target mcp-gateway -t gongctl:mcp-gateway-local .
scripts/postgres-backup-restore-smoke.sh
kubectl kustomize deploy/kubernetes/postgres-pilot
```

CI also runs static analysis, Go vulnerability checks, normal image builds, and
MCP-only image builds. If a release is built outside CI, reproduce those checks
and archive `dist/checksums.txt`, `dist/sbom-go-modules.json`, and
`dist/build-env.txt` with the release artifacts.

## Release Candidate Flow

1. Update `VERSION`.
2. Update `CHANGELOG.md`.
3. Confirm `go version` is `go1.26.5` or newer. Do not build release
   artifacts with an older Go toolchain.
4. Run `go test -count=1 ./...`.
5. Run `go vet ./...`.
6. Run `make secret-scan`.
7. Run `make sbom`.
8. Run `make checksums`.
9. Run `make postgres-backup-restore-smoke`.
10. Render the Kubernetes Postgres pilot starter:
   `kubectl kustomize deploy/kubernetes/postgres-pilot`.
11. Run `make docker-build`.
12. Run `make docker-build-mcp`.
13. Run `make docker-build-mcp-gateway`.
14. Run `make docker-build-ghcr`.
15. Run `make docker-build-ghcr-mcp`.
16. Run `make docker-build-ghcr-mcp-gateway`.
17. Tag the release as `v$(cat VERSION)`.
18. Push the tag; `.github/workflows/publish-images.yml` reruns Postgres-backed
    Go tests, the synthetic Postgres backup/restore smoke, vet, secret scan,
    Docker smoke builds, and image vulnerability scans before it
    publishes:
    - `ghcr.io/fyne-coder/gongcli_mcp/gongctl:vX.Y.Z`
    - `ghcr.io/fyne-coder/gongcli_mcp/gongmcp:vX.Y.Z`
    - `ghcr.io/fyne-coder/gongcli_mcp/gongmcp-gateway:vX.Y.Z`
19. After the first publish, confirm the GHCR packages are public if the GitHub
    repository is public and external consumption is intended.
20. Run GoReleaser from the tag with Go 1.26.5 or newer.

For pre-GA validation, push a release-candidate tag such as `v0.4.0-rc1`.
Release-candidate tags publish immutable candidate tags and SHA tags only; they
do not publish `latest` or the moving `X.Y` alias. Stable `vX.Y.Z` tag pushes
publish the immutable version tag, the `X.Y` alias, and `latest`.

The GHCR workflow can also be run manually from GitHub Actions from the default
branch. Manual runs publish SHA-tagged images only; release version tags come
from protected `vX.Y.Z` or `vX.Y.Z-rcN` tag pushes.

Public docs may be prepared for the next `VERSION`, but a Docker image tag is
not public until the corresponding protected tag workflow completes and
`docker buildx imagetools inspect ghcr.io/fyne-coder/gongcli_mcp/gongmcp:vX.Y.Z`
and `docker buildx imagetools inspect ghcr.io/fyne-coder/gongcli_mcp/gongctl:vX.Y.Z`
and
`docker buildx imagetools inspect ghcr.io/fyne-coder/gongcli_mcp/gongmcp-gateway:vX.Y.Z`
all succeed.

## Supply-Chain Notes

- Publish immutable version tags and prefer digest-pinned Docker references in
  company MCP host configs.
- GitHub Actions in this repo are pinned to commit SHAs with the upstream tag
  noted in comments. When bumping an action, resolve the tag SHA with
  `git ls-remote --tags https://github.com/OWNER/REPO.git refs/tags/TAG`,
  update the workflow, and review the upstream changelog before merging.
- Publish the full `gongctl` image and the MCP-only `gongmcp` image as separate
  GHCR packages. Publish the remote MCP OAuth gateway as
  `gongmcp-gateway`. Business-user MCP host configs should point at the
  MCP-only or gateway package required by the deployment shape.
- Run image vulnerability scanning in the publishing registry or company
  container pipeline before promoting an image digest. Local CI proves the image
  builds, but registry scanners usually have the right policy exceptions and
  base-image feeds.
- Keep `dist/checksums.txt`, `dist/sbom-go-modules.json`, and
  `dist/build-env.txt` with binary artifacts.
- Generate SBOMs and signatures in the company release pipeline when required.
  This repo's CI covers tests, vet, secret-pattern scanning, staticcheck
  (`U1000` and `ST1000` disabled until existing internal dead-code and package
  comment cleanup are scheduled),
  govulncheck, Go module inventory, checksums, and Docker builds;
  signing/provenance can be layered on top by the publishing environment.

Digest verification commands:

```bash
docker buildx imagetools inspect ghcr.io/fyne-coder/gongcli_mcp/gongmcp:vX.Y.Z
docker buildx imagetools inspect ghcr.io/fyne-coder/gongcli_mcp/gongctl:vX.Y.Z
docker buildx imagetools inspect ghcr.io/fyne-coder/gongcli_mcp/gongmcp-gateway:vX.Y.Z
```

Record and deploy the returned `sha256:` digest:

```bash
docker pull ghcr.io/fyne-coder/gongcli_mcp/gongmcp@sha256:REPLACE_WITH_DIGEST
docker run --rm ghcr.io/fyne-coder/gongcli_mcp/gongmcp@sha256:REPLACE_WITH_DIGEST --list-tool-presets
```

For environments that require signatures, add keyless signing in the customer
or publisher release pipeline and verify the signed digest before promotion.
Digest pinning is the minimum supported customer-hosted verification path in
this repo.

## Upgrade And Rollback

For customer-hosted deployments:

The consolidated operator sequence lives in the
[Customer upgrade runbook](customer-upgrade-runbook.md). Keep this section as
the release-facing summary and use the runbook for customer promotion steps,
schema/read-model handling, gateway smoke, and evidence collection.

1. Pin the current working image digest and record the current cache backup.
2. Pull the candidate `gongctl` and `gongmcp` images by immutable tag or digest.
3. Back up SQLite or Postgres, transcript files, profile YAML, AI governance
   YAML, and MCP host config before changing binaries.
4. For SQLite, run `gongctl sync status --db PATH` with the candidate operator
   image against a protected copy before touching production.
5. For Postgres, restore the backup into an isolated database, run
   `gongctl sync read-model --rebuild` with the candidate operator image, then
   run a read-only MCP smoke against the restored database.
6. Run `gongmcp` `tools/list` against the candidate MCP image using a read-only
   cache mount or read-only Postgres role.
7. Promote the MCP image only after the target host, allowlist, auth mode, and
   support-bundle smoke pass.
8. Roll back by restoring the prior image digest and prior cache/config backup.

Do not use read-only `gongmcp` as a migration path. If the cache needs a schema
repair or migration, run the writable `gongctl` operator path first, validate
`sync status`, then restart MCP.

The repo-level Postgres release drill is synthetic only:

```bash
scripts/postgres-backup-restore-smoke.sh
```

It proves dump/restore mechanics, migration/read-model repair, restored MCP
startup, and read-only-role denials without using customer data. Customer
production backup policy, PITR, replica rewind, encryption, and retention of
backup media remain deployment-owned controls.

For a controlled shared Postgres client pilot, use the
[Postgres client pilot release packet](postgres-client-pilot-release-packet.md)
as the handoff checklist. It narrows the supported pilot surface, evidence
bundle, digest-pinning expectations, and non-GA limitations before business
users connect.

The shorter operator sequence is the
[Postgres client onboarding checklist](postgres-client-onboarding-checklist.md).
Completing that checklist is publish-readiness evidence for a controlled pilot;
it is not by itself a tag, image publication, or GA certification.

# Release Versioning

`gongctl` uses SemVer-style release tags.

- Version source for the next release: `VERSION`
- Git tag format: `vX.Y.Z`
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
go test -count=1 ./...
go vet ./...
make secret-scan
make sbom
make checksums
docker build -t gongctl:local .
docker build --target mcp -t gongctl:mcp-local .
```

CI also runs static analysis, Go vulnerability checks, normal image builds, and
MCP-only image builds. If a release is built outside CI, reproduce those checks
and archive `dist/checksums.txt`, `dist/sbom-go-modules.json`, and
`dist/build-env.txt` with the release artifacts.

## Release Candidate Flow

1. Update `VERSION`.
2. Update `CHANGELOG.md`.
3. Run `go test -count=1 ./...`.
4. Run `go vet ./...`.
5. Run `make secret-scan`.
6. Run `make sbom`.
7. Run `make checksums`.
8. Run `make docker-build`.
9. Run `make docker-build-mcp`.
10. Run `make docker-build-ghcr`.
11. Run `make docker-build-ghcr-mcp`.
12. Tag the release as `v$(cat VERSION)`.
13. Push the tag; `.github/workflows/publish-images.yml` publishes:
    - `ghcr.io/fyne-coder/gongcli_mcp/gongctl:vX.Y.Z`
    - `ghcr.io/fyne-coder/gongcli_mcp/gongmcp:vX.Y.Z`
14. After the first publish, confirm the GHCR packages are public if the GitHub
    repository is public and external consumption is intended.
15. Run GoReleaser from the tag.

The GHCR workflow can also be run manually from GitHub Actions. Manual runs use
the current `VERSION` file for `vX.Y.Z` and `X.Y.Z` image tags.

## Supply-Chain Notes

- Publish immutable version tags and prefer digest-pinned Docker references in
  company MCP host configs.
- Publish the full `gongctl` image and the MCP-only `gongmcp` image as separate
  GHCR packages. Business-user MCP host configs should point at the MCP-only
  package.
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

## Upgrade And Rollback

For customer-hosted deployments:

1. Pin the current working image digest and record the current cache backup.
2. Pull the candidate `gongctl` and `gongmcp` images by immutable tag or digest.
3. Back up SQLite, transcript files, profile YAML, AI governance YAML, and MCP
   host config before changing binaries.
4. Run `gongctl sync status --db PATH` with the candidate operator image.
5. Run `gongmcp` `tools/list` against the candidate MCP image using a read-only
   cache mount.
6. Promote the MCP image only after the target host, allowlist, auth mode, and
   support-bundle smoke pass.
7. Roll back by restoring the prior image digest and prior cache/config backup.

Do not use read-only `gongmcp` as a migration path. If the cache needs a schema
repair or migration, run the writable `gongctl` operator path first, validate
`sync status`, then restart MCP.

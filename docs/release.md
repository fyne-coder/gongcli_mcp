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
10. Tag the release as `v$(cat VERSION)`.
11. Run GoReleaser from the tag.

## Supply-Chain Notes

- Publish immutable version tags and prefer digest-pinned Docker references in
  company MCP host configs.
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

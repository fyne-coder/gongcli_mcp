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

## Release Candidate Flow

1. Update `VERSION`.
2. Update `CHANGELOG.md`.
3. Run `go test -count=1 ./...`.
4. Run `make docker-build`.
5. Tag the release as `v$(cat VERSION)`.
6. Run GoReleaser from the tag.

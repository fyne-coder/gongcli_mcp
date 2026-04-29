#!/usr/bin/env sh
set -eu

mkdir -p dist
go list -m -json all > dist/sbom-go-modules.json
{
	echo "go_version=$(go env GOVERSION)"
	echo "go_os=$(go env GOOS)"
	echo "go_arch=$(go env GOARCH)"
	echo "module=$(go list -m)"
} > dist/build-env.txt

echo "sbom: wrote dist/sbom-go-modules.json and dist/build-env.txt"

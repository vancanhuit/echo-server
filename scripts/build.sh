#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || printf dev)}
COMMIT=${COMMIT:-$(git rev-parse HEAD 2>/dev/null || printf unknown)}
BUILD_DATE=${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}

case "${1:-binary}" in
binary)
	package="$(go list -m)/internal/buildinfo"
	exec go build -trimpath -buildvcs=false -ldflags "-X $package.Version=$VERSION -X $package.Commit=$COMMIT -X $package.Date=$BUILD_DATE" -o bin/echo-server ./cmd/echo-server
	;;
docker)
	exec docker build --build-arg "VERSION=$VERSION" --build-arg "COMMIT=$COMMIT" --build-arg "BUILD_DATE=$BUILD_DATE" -t echo-server:local .
	;;
*)
	printf 'Usage: %s [binary|docker]\n' "$0" >&2
	exit 2
	;;
esac

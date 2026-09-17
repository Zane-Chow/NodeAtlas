#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
asset_dir="$(cd "$project_root/internal/webassets/dist" && pwd -P)"
expected_asset_dir="$project_root/internal/webassets/dist"
if [[ "$asset_dir" != "$expected_asset_dir" ]]; then
  echo "refusing to replace unexpected asset directory: $asset_dir" >&2
  exit 1
fi

npm --prefix "$project_root/web" ci
npm --prefix "$project_root/web" test -- --run
npm --prefix "$project_root/web" run build
find "$asset_dir" -mindepth 1 ! -name .gitkeep -delete
cp -R "$project_root/web/dist/." "$asset_dir/"

export GOCACHE="${GOCACHE:-/tmp/controlpanel-gocache}"
go test ./...
mkdir -p "$project_root/dist"
version="${VERSION:-dev}"
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$version" -o "$project_root/dist/controlpanel" ./cmd/controlpanel

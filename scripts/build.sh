#!/bin/sh
set -eu
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
mkdir -p "$project_dir/dist"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go -C "$project_dir" build -trimpath -ldflags=-H=windowsgui -o "$project_dir/dist/coach.exe" ./cmd/coach
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go -C "$project_dir" vet ./...
shasum -a 256 "$project_dir/dist/coach.exe"

#!/bin/sh
# goenv.sh — обёртка go-команд с workspace-local окружением (чанк 35+).
# На этой машине sandbox запрещает запись в ~/go и ~/Library/Caches —
# перенаправляем GOPATH/GOCACHE/GOTMPDIR в .tools/ (в git не попадает).
# Использование: ./goenv.sh build ./...  |  ./goenv.sh test ./internal/...
ROOT="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$ROOT/.tools/tmp" "$ROOT/.tools/gocache" "$ROOT/.tools/gopath"
export GOTMPDIR="$ROOT/.tools/tmp"
export GOCACHE="$ROOT/.tools/gocache"
export GOPATH="$ROOT/.tools/gopath"
export GOFLAGS="-mod=mod"
exec go "$@"

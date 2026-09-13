#!/usr/bin/env bash
# ============================================================================
# Генерация Go-кода из proto-контрактов SuriFleet (api/proto/agent/v1).
#
# Окружение (однократная установка плагинов):
#   export GOBIN="$PWD/.tools/bin"
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
#
# Инструменты не коммитятся: protoc — .tools/protoc, плагины — .tools/bin.
# Сгенерированный код КОММИТИТСЯ в internal/gen (воспроизводимость без protoc).
# ============================================================================
set -euo pipefail
cd "$(dirname "$0")/.."

export PATH="$PWD/.tools/bin:$PATH"

PROTOC="$PWD/.tools/protoc/bin/protoc.exe"

"$PROTOC" \
  --proto_path=api/proto \
  --proto_path=.tools/protoc/include \
  --go_out=internal/gen --go_opt=paths=source_relative \
  --go-grpc_out=internal/gen --go-grpc_opt=paths=source_relative \
  api/proto/agent/v1/agent.proto \
  api/proto/agent/v1/enrollment.proto

echo "OK: сгенерировано в internal/gen/agent/v1"

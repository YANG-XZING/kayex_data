#!/usr/bin/env bash
# 构建前端并交叉编译 Linux 版，打包后可直接拷到 CentOS 7 安装（服务器上不需要 Node）
# 用法：./scripts/build.sh [版本号]，Windows 上用 Git Bash 执行
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
VERSION="${1:-$(date +%Y%m%d%H%M)}"
NAME="kx-opc-web-${VERSION}-linux-amd64"
OUT="$ROOT/dist/$NAME"

# 前端打包到 internal/web/dist，随后由 go:embed 嵌入程序；先清掉旧的打包结果，否则旧文件也会被嵌进程序
rm -rf "$ROOT/internal/web/dist/assets"
(cd "$ROOT/web" && npm ci --no-audit --no-fund && npm run build)
touch "$ROOT/internal/web/dist/.gitkeep"

(cd "$ROOT" && go vet ./...)

rm -rf "$OUT" && mkdir -p "$OUT"
# CGO_ENABLED=0：纯静态二进制，不依赖 glibc 版本，CentOS 7 可直接运行
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" \
  -o "$OUT/kx-opc-web" ./cmd/kx-opc-web)

cp "$ROOT/configs/config.yaml" "$OUT/"
cp -r "$ROOT/deploy" "$ROOT/sql" "$OUT/"

tar -C "$ROOT/dist" -czf "$ROOT/dist/$NAME.tar.gz" "$NAME"
echo "已生成 dist/$NAME.tar.gz"
echo "目标机上：tar xzf $NAME.tar.gz && cd $NAME && sudo ./deploy/install.sh"

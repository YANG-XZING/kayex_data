#!/usr/bin/env bash
# 在目标机上以 root 执行，安装或升级服务。配置一律用安装包里的 config.yaml，
# 现场原来的配置备份为 config.yaml.bak.时间（按用户要求：部署以最新的配置为准）
# 用法：在解压后的目录中执行 sudo ./deploy/install.sh
set -euo pipefail

SRC="$(cd "$(dirname "$0")/.." && pwd)"
APP_DIR=/opt/kx_opc_web
SERVICE=kx_opc_web
LEGACY_SERVICE=kx-opc-web
LEGACY_DIR=/opt/kx-opc-web
if systemctl cat "$LEGACY_SERVICE.service" >/dev/null 2>&1; then
  systemctl stop "$LEGACY_SERVICE.service"
  systemctl disable "$LEGACY_SERVICE.service" 2>/dev/null || true
fi

id kxopc >/dev/null 2>&1 || useradd --system --no-create-home --shell /sbin/nologin kxopc
mkdir -p "$APP_DIR/logs"
for old_config in config.yaml furnaces.json; do
  if [[ ! -f "$APP_DIR/$old_config" && -f "$LEGACY_DIR/$old_config" ]]; then
    cp -p "$LEGACY_DIR/$old_config" "$APP_DIR/$old_config"
  fi
done
systemctl stop "$SERVICE" 2>/dev/null || true

install -m 0755 "$SRC/kx_opc_web" "$APP_DIR/kx_opc_web"
if [[ -f "$APP_DIR/config.yaml" ]]; then
  BAK="$APP_DIR/config.yaml.bak.$(date +%Y%m%d%H%M%S)"
  cp -p "$APP_DIR/config.yaml" "$BAK"
  echo "原来的配置已备份为 $BAK，改用安装包里的 config.yaml"
fi
install -m 0600 "$SRC/config.yaml" "$APP_DIR/config.yaml"
cp -r "$SRC/sql" "$APP_DIR/"
chown -R kxopc:kxopc "$APP_DIR"

install -m 0644 "$SRC/deploy/$SERVICE.service" "/etc/systemd/system/$SERVICE.service"
systemctl daemon-reload
systemctl enable "$SERVICE"

# 放行 8700 端口（firewalld 未运行时跳过）
if systemctl is-active --quiet firewalld; then
  firewall-cmd --permanent --add-port=8700/tcp >/dev/null && firewall-cmd --reload >/dev/null
fi

echo
echo "安装完成。下一步："
echo "  1. 检查配置：$APP_DIR/config.yaml"
echo "  2. 启动服务：systemctl start $SERVICE"
echo "  3. 浏览器打开：http://<本机IP>:8700"
echo "  4. 查看日志：journalctl -u $SERVICE -f"

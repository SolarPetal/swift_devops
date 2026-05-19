#!/usr/bin/env bash
#
# swift-devops · 卸载脚本
#
# 用法:
#   sudo bash uninstall.sh             # 保留数据
#   sudo bash uninstall.sh --purge     # 同时删除数据/配置/日志
#
set -euo pipefail

APP_NAME="swift-devops"
APP_USER="swiftops"
INSTALL_DIR="/opt/${APP_NAME}"
DATA_DIR="/var/lib/${APP_NAME}"
LOG_DIR="/var/log/${APP_NAME}"
CONFIG_DIR="/etc/${APP_NAME}"
SERVICE_FILE="/etc/systemd/system/${APP_NAME}.service"

PURGE=0
[[ "${1:-}" == "--purge" ]] && PURGE=1

[[ $EUID -eq 0 ]] || { echo "请用 root 或 sudo 执行"; exit 1; }

echo "[*] 停止并禁用服务..."
systemctl stop "${APP_NAME}.service" 2>/dev/null || true
systemctl disable "${APP_NAME}.service" 2>/dev/null || true
rm -f "$SERVICE_FILE"
systemctl daemon-reload

echo "[*] 删除二进制..."
rm -rf "$INSTALL_DIR"

if [[ $PURGE -eq 1 ]]; then
    echo "[*] --purge 模式：清理数据/配置/日志..."
    rm -rf "$DATA_DIR" "$LOG_DIR" "$CONFIG_DIR"
    rm -f "/root/.${APP_NAME}-initial-password"
    id -u "$APP_USER" &>/dev/null && userdel "$APP_USER" 2>/dev/null || true
    echo "[✓] 清理完毕"
else
    echo "[i] 数据保留在: $DATA_DIR"
    echo "[i] 配置保留在: $CONFIG_DIR"
    echo "[i] 日志保留在: $LOG_DIR"
    echo "[i] 如需彻底清理: sudo bash uninstall.sh --purge"
fi

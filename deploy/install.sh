#!/usr/bin/env bash
#
# swift-devops · 一键安装脚本（Linux only）
#
# 用法:
#   sudo bash install.sh                    # 默认安装 latest
#   sudo bash install.sh --port 9090        # 指定端口
#   sudo bash install.sh --version v0.1.0   # 指定版本
#   sudo bash install.sh --upgrade          # 升级（保留配置与数据）
#   sudo bash install.sh --local ./pkg.tar.gz   # 离线安装本地包
#
set -euo pipefail

# ============ 默认变量 ============
APP_NAME="swift-devops"
APP_USER="swiftops"
INSTALL_DIR="/opt/${APP_NAME}"
DATA_DIR="/var/lib/${APP_NAME}"
LOG_DIR="/var/log/${APP_NAME}"
CONFIG_DIR="/etc/${APP_NAME}"
CONFIG_FILE="${CONFIG_DIR}/config.yaml"
SERVICE_FILE="/etc/systemd/system/${APP_NAME}.service"
PORT="${PORT:-8088}"
VERSION="${VERSION:-latest}"
REPO="${REPO:-SolarPetal/swift_devops}"
RELEASES_BASE="${RELEASES_BASE:-https://github.com/${REPO}/releases}"
LOCAL_PKG=""
UPGRADE=0

# ============ 颜色 ============
if [[ -t 1 ]]; then
    C_G='\033[0;32m'; C_R='\033[0;31m'; C_Y='\033[0;33m'; C_C='\033[0;36m'; C_0='\033[0m'
else
    C_G=''; C_R=''; C_Y=''; C_C=''; C_0=''
fi
info()  { echo -e "${C_G}[✓]${C_0} $*"; }
warn()  { echo -e "${C_Y}[!]${C_0} $*"; }
fatal() { echo -e "${C_R}[✗]${C_0} $*" >&2; exit 1; }

# ============ 前置检查 ============
preflight() {
    [[ $EUID -eq 0 ]] || fatal "请用 root 或 sudo 执行"
    [[ "$(uname -s)" == "Linux" ]] || fatal "仅支持 Linux"

    command -v systemctl >/dev/null || fatal "需要 systemd"
    command -v curl >/dev/null || fatal "需要 curl"
    command -v tar >/dev/null || fatal "需要 tar"

    if ! command -v docker >/dev/null; then
        warn "未检测到 Docker：local-docker / remote-docker / Docker 运行时将不可用；local-jar 与 systemd/nohup 不受影响"
    fi

    case "$(uname -m)" in
        x86_64)  ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) fatal "不支持的架构: $(uname -m)" ;;
    esac
    info "检测到 Linux/${ARCH}"
}

# ============ 参数解析 ============
parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --port)      PORT="$2"; shift 2 ;;
            --version)   VERSION="$2"; shift 2 ;;
            --data-dir)  DATA_DIR="$2"; shift 2 ;;
            --local)     LOCAL_PKG="$2"; shift 2 ;;
            --upgrade)   UPGRADE=1; shift ;;
            -h|--help)   usage; exit 0 ;;
            *) fatal "未知参数: $1" ;;
        esac
    done
}

usage() {
    sed -n '2,12p' "$0"
}

# ============ 用户与目录 ============
create_user_and_dirs() {
    if ! id -u "$APP_USER" &>/dev/null; then
        useradd --system --no-create-home --shell /usr/sbin/nologin "$APP_USER"
        info "创建系统用户: $APP_USER"
    fi

    if getent group docker >/dev/null 2>&1; then
        usermod -aG docker "$APP_USER" || true
        info "已将 $APP_USER 加入 docker 组"
    fi

    for d in "$INSTALL_DIR" "$DATA_DIR" "$DATA_DIR/artifacts" "$DATA_DIR/build" "$DATA_DIR/.m2" "$LOG_DIR" "$CONFIG_DIR"; do
        mkdir -p "$d"
        chown "$APP_USER:$APP_USER" "$d"
    done
}

# ============ 下载或拷贝二进制 ============
resolve_version() {
    if [[ "$VERSION" != "latest" ]]; then
        return
    fi

    local latest_url
    latest_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "${RELEASES_BASE}/latest")"         || fatal "无法解析 latest release：${RELEASES_BASE}/latest"
    VERSION="${latest_url##*/}"
    [[ "$VERSION" == v* ]] || fatal "latest release 解析异常: ${latest_url}"
    info "解析 latest release: ${VERSION}"
}

fetch_binary() {
    local tmp; tmp="$(mktemp -d)"
    trap "rm -rf $tmp" EXIT

    if [[ -n "$LOCAL_PKG" ]]; then
        [[ -f "$LOCAL_PKG" ]] || fatal "本地包不存在: $LOCAL_PKG"
        info "使用本地包: $LOCAL_PKG"
        tar -xzf "$LOCAL_PKG" -C "$tmp"
    else
        resolve_version
        local asset="${APP_NAME}-linux-${ARCH}-${VERSION}.tar.gz"
        local url="${RELEASES_BASE}/download/${VERSION}/${asset}"
        info "下载: $url"
        curl -fsSL "$url" -o "$tmp/pkg.tar.gz" || fatal "下载失败，可用 --local 指定本地包"
        tar -xzf "$tmp/pkg.tar.gz" -C "$tmp"
    fi

    # 兼容包内文件名 swift-devops 或 swift-devops-<arch>
    local bin
    bin="$(find "$tmp" -maxdepth 2 -type f -name "${APP_NAME}*" | head -n1)"
    [[ -n "$bin" ]] || fatal "包内未找到二进制"

    # 升级时停服再替换
    if [[ $UPGRADE -eq 1 ]] && systemctl is-active --quiet "$APP_NAME" 2>/dev/null; then
        systemctl stop "$APP_NAME"
        info "已停止旧服务"
    fi

    install -m 0755 -o "$APP_USER" -g "$APP_USER" "$bin" "${INSTALL_DIR}/${APP_NAME}"
    info "二进制已安装: ${INSTALL_DIR}/${APP_NAME}"
}

# ============ 生成配置 + 初始密码 ============
gen_config() {
    if [[ -f "$CONFIG_FILE" ]]; then
        warn "配置已存在，跳过生成: $CONFIG_FILE"
        return
    fi

    local master_key jwt_secret admin_pwd hashed
    master_key="$(head -c 32 /dev/urandom | base64 -w0)"
    jwt_secret="$(head -c 32 /dev/urandom | base64 -w0)"
    admin_pwd="$(head -c 9 /dev/urandom | base64 | tr -d '/+=' | cut -c1-12)"
    hashed="$(${INSTALL_DIR}/${APP_NAME} hash-pwd "${admin_pwd}")"

    cat > "$CONFIG_FILE" <<EOF
server:
  port: ${PORT}
  mode: release

database:
  # 支持 sqlite / mysql / postgres；安装脚本默认生成 sqlite 配置
  driver: sqlite
  dsn: ${DATA_DIR}/swift-devops.db

security:
  master_key: "${master_key}"
  jwt_secret: "${jwt_secret}"
  jwt_ttl: 24h

admin:
  username: admin
  password_bcrypt: "${hashed}"

storage:
  artifact_dir: ${DATA_DIR}/artifacts
  build_workspace: ${DATA_DIR}/build
  max_history: 30
  max_upload_mb: 256

builder:
  docker_enabled: false

ssh:
  pool_size_per_host: 2
  idle_timeout: 5m
  connect_timeout: 10s

monitor:
  interval: 5s
  retention: 24h

log:
  level: info
  dir: ${LOG_DIR}
EOF
    chmod 0640 "$CONFIG_FILE"
    chown "root:${APP_USER}" "$CONFIG_FILE"

    echo "$admin_pwd" > "/root/.${APP_NAME}-initial-password"
    chmod 0600 "/root/.${APP_NAME}-initial-password"
    info "初始管理员密码: /root/.${APP_NAME}-initial-password"
}

# ============ systemd unit ============
write_service() {
    cat > "$SERVICE_FILE" <<EOF
[Unit]
Description=Swift DevOps - Java Service Automation Platform
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
User=${APP_USER}
Group=${APP_USER}
WorkingDirectory=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/${APP_NAME} serve --config ${CONFIG_FILE}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
StandardOutput=append:${LOG_DIR}/stdout.log
StandardError=append:${LOG_DIR}/stderr.log

NoNewPrivileges=true
ProtectSystem=full
ProtectHome=true
PrivateTmp=true
ReadWritePaths=${DATA_DIR} ${LOG_DIR}

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable "${APP_NAME}.service" >/dev/null 2>&1
    info "systemd 服务已注册"
}

# ============ 启动 + 自检 ============
start_and_verify() {
    systemctl restart "${APP_NAME}.service"

    local i
    for i in $(seq 1 30); do
        if curl --noproxy '*' -fsS "http://127.0.0.1:${PORT}/api/v1/health" >/dev/null 2>&1; then
            info "服务已启动"
            return
        fi
        sleep 1
    done
    fatal "服务 30 秒内未响应，请查看: journalctl -u ${APP_NAME} -e"
}

# ============ 收口 ============
print_summary() {
    local ip
    ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
    local pwd_file="/root/.${APP_NAME}-initial-password"
    cat <<EOF

${C_G}══════════════════════════════════════════════════${C_0}
  ${C_C}swift-devops 已就绪 ✨${C_0}

  访问地址 : http://${ip:-127.0.0.1}:${PORT}
  管理账号 : admin
  初始密码 : $( [[ -f "$pwd_file" ]] && cat "$pwd_file" || echo "(已有配置)" )

  常用命令:
    systemctl status  ${APP_NAME}
    systemctl restart ${APP_NAME}
    journalctl -u ${APP_NAME} -f

  配置文件 : ${CONFIG_FILE}
  数据目录 : ${DATA_DIR}
  日志目录 : ${LOG_DIR}
${C_G}══════════════════════════════════════════════════${C_0}
EOF
}

# ============ main ============
main() {
    parse_args "$@"
    preflight
    create_user_and_dirs
    fetch_binary
    gen_config
    write_service
    start_and_verify
    print_summary
}

main "$@"

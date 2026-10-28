#!/usr/bin/env bash
# ============================================================
# JaySQL 一键部署脚本 (sshpass 密码登录版本)
# ============================================================
# 流程: 构建前端 → 同步 dist → 编译 Linux 二进制 → 上传 →
#       备份旧版 → 替换 → 重启 → 验证 → 失败自动回滚
#
# 用法:
#   # 1. 密码从环境变量传入(不进 history)
#   export DEPLOY_PASSWORD='你的密码'
#   ./scripts/deploy.sh --host 192.168.1.99 --user root
#
#   # 2. 或者从文件读(权限 600)
#   echo '你的密码' > ~/.deploy_pwd && chmod 600 ~/.deploy_pwd
#   ./scripts/deploy.sh --host 192.168.1.99 --password-file ~/.deploy_pwd
#
#   # 3. 先演练一遍,不真改
#   ./scripts/deploy.sh --host 192.168.1.99 --dry-run
#
# 依赖: sshpass (brew install sshpass)
# ============================================================

set -euo pipefail

# ==========================
# 默认配置
# ==========================
REMOTE_HOST=""
REMOTE_USER="root"
REMOTE_DIR="/opt/goinsight"
REMOTE_PORT="22"
REMOTE_SERVICE="goinsight"
REMOTE_LISTEN_PORT="9000"
PASSWORD_FILE=""
DRY_RUN=false
SKIP_BACKUP=false
SKIP_FRONTEND=false

# ==========================
# 颜色
# ==========================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log()  { echo -e "${BLUE}[INFO]${NC} $*"; }
ok()   { echo -e "${GREEN}[ OK ]${NC} $*"; }
warn() { echo -e "${YELLOW}[WARN]${NC} $*"; }
err()  { echo -e "${RED}[FAIL]${NC} $*" >&2; }

# ==========================
# 参数解析
# ==========================
usage() {
  cat <<EOF
Usage: $0 [options]

必填:
  --host <ip|host>          远端主机 (例: 192.168.1.99)

可选:
  --user <name>             远端用户 (默认 root)
  --dir <path>              部署目录 (默认 /opt/goinsight)
  --port <n>                SSH 端口 (默认 22)
  --service <name>          systemd 服务名 (默认 goinsight)
  --listen-port <n>         应用监听端口 (默认 9000)
  --password-file <path>    密码文件路径(权限 600,推荐)
  --skip-frontend           跳过前端构建(只用现有 dist)
  --skip-backup             不备份旧二进制
  --dry-run                 只打印将执行的命令,不真跑
  -h, --help                显示帮助

密码提供方式(二选一):
  1. 环境变量 DEPLOY_PASSWORD
  2. --password-file <path>(推荐,chmod 600)
EOF
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)         REMOTE_HOST="$2"; shift 2 ;;
    --user)         REMOTE_USER="$2"; shift 2 ;;
    --dir)          REMOTE_DIR="$2"; shift 2 ;;
    --port)         REMOTE_PORT="$2"; shift 2 ;;
    --service)      REMOTE_SERVICE="$2"; shift 2 ;;
    --listen-port)  REMOTE_LISTEN_PORT="$2"; shift 2 ;;
    --password-file) PASSWORD_FILE="$2"; shift 2 ;;
    --skip-frontend) SKIP_FRONTEND=true; shift ;;
    --skip-backup)   SKIP_BACKUP=true; shift ;;
    --dry-run)       DRY_RUN=true; shift ;;
    -h|--help)       usage ;;
    *) err "未知参数: $1"; usage ;;
  esac
done

if [[ -z "$REMOTE_HOST" ]]; then
  err "缺少 --host"
  usage
fi

# ==========================
# 密码加载
# ==========================
load_password() {
  if [[ -n "$PASSWORD_FILE" ]]; then
    if [[ ! -f "$PASSWORD_FILE" ]]; then
      err "密码文件不存在: $PASSWORD_FILE"
      exit 1
    fi
    local perms
    perms=$(stat -f "%Lp" "$PASSWORD_FILE" 2>/dev/null || stat -c "%a" "$PASSWORD_FILE" 2>/dev/null)
    if [[ "$perms" != "600" && "$perms" != "400" ]]; then
      warn "密码文件权限是 $perms,建议 600"
    fi
    DEPLOY_PASSWORD=$(cat "$PASSWORD_FILE")
    SSHPASS_OPT="-f"
  elif [[ -n "${DEPLOY_PASSWORD:-}" ]]; then
    SSHPASS_OPT="-e"
  else
    err "必须设置 DEPLOY_PASSWORD 环境变量 或 --password-file"
    exit 1
  fi
}

# ==========================
# sshpass 包装
# ==========================
run_remote() {
  local desc="$1"; shift
  if $DRY_RUN; then
    log "[DRY-RUN] [$desc] sshpass $SSHPASS_OPT ssh -p $REMOTE_PORT $REMOTE_USER@$REMOTE_HOST -- $*"
    return 0
  fi
  log "[$desc] $*"
  sshpass $SSHPASS_OPT ssh -p "$REMOTE_PORT" \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    -o ConnectTimeout=10 \
    "$REMOTE_USER@$REMOTE_HOST" -- "$@"
}

scp_to_remote() {
  local src="$1" dst="$2"
  if $DRY_RUN; then
    log "[DRY-RUN] sshpass scp $src → $REMOTE_USER@$REMOTE_HOST:$dst"
    return 0
  fi
  log "scp $src → $REMOTE_USER@$REMOTE_HOST:$dst"
  sshpass $SSHPASS_OPT scp -P "$REMOTE_PORT" \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    "$src" "$REMOTE_USER@$REMOTE_HOST:$dst"
}

# ==========================
# 前置检查
# ==========================
check_deps() {
  for cmd in sshpass ssh scp go npm; do
    if ! command -v "$cmd" &>/dev/null; then
      err "缺少依赖: $cmd"
      if [[ "$cmd" == "sshpass" ]]; then
        err "  macOS: brew install sshpass"
        err "  Linux: sudo apt install sshpass / yum install sshpass"
      fi
      exit 1
    fi
  done
}

# ==========================
# 路径
# ==========================
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
FRONTEND_DIR="$PROJECT_ROOT/www"
BACKEND_DIR="$PROJECT_ROOT/backend"
BINARY_NAME="goinsight-linux-amd64"

# ==========================
# 流程
# ==========================
banner() {
  echo ""
  echo "============================================================"
  echo "  JaySQL 一键部署"
  echo "============================================================"
  echo "  目标主机:  $REMOTE_USER@$REMOTE_HOST:$REMOTE_PORT"
  echo "  部署目录:  $REMOTE_DIR"
  echo "  systemd:   $REMOTE_SERVICE.service"
  echo "  监听端口:  $REMOTE_LISTEN_PORT"
  echo "  Dry-Run:   $DRY_RUN"
  echo "============================================================"
  echo ""
}

build_frontend() {
  if $SKIP_FRONTEND; then
    warn "跳过前端构建,使用现有 dist"
    return 0
  fi
  log "构建前端..."
  cd "$FRONTEND_DIR"
  rm -rf dist
  npm install --silent
  npm run build
  ok "前端构建完成"
}

sync_dist() {
  log "同步 dist 到 backend/web/"
  rm -rf "$BACKEND_DIR/web/dist"
  mkdir -p "$BACKEND_DIR/web"
  cp -r "$FRONTEND_DIR/dist" "$BACKEND_DIR/web/"
  ok "dist 已同步"
}

build_backend() {
  log "编译 Linux amd64 二进制..."
  cd "$BACKEND_DIR"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" \
    -o "$BINARY_NAME" ./cmd
  ok "二进制已编译: $(ls -lh $BINARY_NAME | awk '{print $5}')"
  ok "MD5:       $(md5sum $BINARY_NAME 2>/dev/null || md5 -q $BINARY_NAME)"
}

upload() {
  local remote_tmp="$REMOTE_DIR/.deploy.new"
  scp_to_remote "$BACKEND_DIR/$BINARY_NAME" "$remote_tmp"
}

backup_remote() {
  if $SKIP_BACKUP; then
    warn "跳过备份"
    return 0
  fi
  local ts
  ts=$(date +%Y%m%d-%H%M%S)
  run_remote "备份旧二进制" bash -c "
    cd '$REMOTE_DIR' || exit 1
    if [[ -f goinsight ]]; then
      cp goinsight goinsight.bak-$ts && echo '备份为 goinsight.bak-$ts'
    else
      echo '未发现旧二进制,跳过备份'
    fi
  "
}

restart_remote() {
  run_remote "停止服务" "systemctl stop $REMOTE_SERVICE 2>&1 || true"
  run_remote "替换二进制" bash -c "
    cd '$REMOTE_DIR' || exit 1
    mv .deploy.new goinsight && chmod +x goinsight
  "
  run_remote "启动服务" "systemctl start $REMOTE_SERVICE"
  sleep 2
  run_remote "查看状态" "systemctl status $REMOTE_SERVICE --no-pager | head -15"
}

verify_remote() {
  sleep 1
  run_remote "健康检查" bash -c "
    curl -sI -m 5 http://127.0.0.1:$REMOTE_LISTEN_PORT/ | head -5 || echo 'HTTP 请求失败'
  "
  run_remote "进程信息" bash -c "
    ps -eo pid,etime,args | grep -E '[g]oinsight' | head -3
  "
}

rollback_remote() {
  err "启动失败,执行回滚"
  run_remote "回滚" bash -c "
    cd '$REMOTE_DIR' || exit 1
    if ls goinsight.bak-* 2>/dev/null | head -1; then
      latest_bak=\$(ls -t goinsight.bak-* 2>/dev/null | head -1)
      if [[ -n \"\$latest_bak\" ]]; then
        systemctl stop $REMOTE_SERVICE 2>&1 || true
        mv goinsight goinsight.failed-\$(date +%s)
        cp \$latest_bak goinsight && chmod +x goinsight
        systemctl start $REMOTE_SERVICE
        echo \"已回滚到 \$latest_bak\"
      fi
    else
      echo '没有可用备份,无法回滚'
    fi
  "
}

# ==========================
# 主流程
# ==========================
main() {
  banner
  check_deps
  load_password

  build_frontend
  sync_dist
  build_backend
  upload
  backup_remote
  restart_remote

  # 验证 + 自动回滚
  if $DRY_RUN; then
    warn "DRY-RUN 模式,跳过验证和回滚"
  else
    if verify_remote 2>&1 | grep -qi "200 OK\|301\|302"; then
      ok "部署成功!"
    else
      err "健康检查异常,准备回滚"
      rollback_remote
      exit 1
    fi
  fi
}

main "$@"
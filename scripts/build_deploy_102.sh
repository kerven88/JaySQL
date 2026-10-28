#!/usr/bin/env bash
set -Eeuo pipefail

# JaySQL build + deploy script
# - Build frontend on local macOS
# - Sync frontend dist into backend/web/dist for go:embed
# - Build Linux amd64 service binary on local machine
# - Optionally build/deploy backup-agent
# - Upload artifacts to 192.168.53.102 and restart service

usage() {
  cat <<'EOF'
用法:
  bash scripts/build_deploy_102.sh --version 2026.07.10.1

  bash scripts/build_deploy_102.sh \
    --version 2026.07.10.1 \
    --deploy-agent \
    --agent-token 'your-agent-token' \
    --agent-node-id 1

说明:
  1. 默认会执行:
     - backend/go build ./...
     - www npm 安装依赖并构建 dist
     - rsync www/dist -> backend/web/dist
     - 构建 linux/amd64 的 goinsight-linux-amd64
     - 上传到 192.168.53.102
     - 备份远端旧二进制
     - 替换并重启 JaySQL 服务
     - 做基础验证
  2. 如果你没有改备份执行链路, 不需要传 --deploy-agent
  3. 如果你传了 --deploy-agent, 必须同时提供 --agent-token

可选参数:
  --version <ver>             发布版本号, 会写入服务二进制版本信息
  --skip-frontend             跳过前端依赖安装和构建
  --skip-backend-check        跳过 backend/go build ./...
  --skip-verify               跳过远端和本地 curl 校验
  --deploy-agent              一并构建/发布/重启 backup-agent
  --agent-token <token>       backup-agent 所需 token
  --agent-node-id <id>        backup node id, 默认 1
  --agent-server <url>        agent 回连服务地址, 默认 http://127.0.0.1:8083
  --remote-host <host>        远端主机, 默认 192.168.53.102
  --remote-user <user>        远端用户, 默认 hs-ops
  --ssh-password <password>   SSH 密码, 默认内置当前环境密码
  --help                      查看帮助

环境依赖:
  本地需要安装: go, npm, rsync, sshpass, ssh, scp, curl, tar

示例:
  bash scripts/build_deploy_102.sh --version 2026.07.10.1

  bash scripts/build_deploy_102.sh \
    --version 2026.07.10.1 \
    --deploy-agent \
    --agent-token 'xxxxxx'
EOF
}

log() {
  printf '[INFO] %s\n' "$*"
}

warn() {
  printf '[WARN] %s\n' "$*" >&2
}

die() {
  printf '[ERROR] %s\n' "$*" >&2
  exit 1
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1"
}

PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRONTEND_DIR="$PROJECT_ROOT/www"
BACKEND_DIR="$PROJECT_ROOT/backend"
BACKEND_WEB_DIST="$BACKEND_DIR/web/dist"
SERVICE_ENTRY="$BACKEND_DIR/cmd/main.go"
AGENT_ENTRY_DIR="$BACKEND_DIR/internal/backup/agentcmd"
ARTIFACT_ROOT="$PROJECT_ROOT/.deploy-artifacts"

REMOTE_HOST="192.168.53.102"
REMOTE_USER="hs-ops"
SSH_PASSWORD="Hsmap@2026"
REMOTE_SERVICE_DIR="/opt/goInsight/backend/goinsight"
REMOTE_SERVICE_BINARY="$REMOTE_SERVICE_DIR/goinsight-linux-amd64"
REMOTE_CONFIG="$REMOTE_SERVICE_DIR/config.yaml"
REMOTE_LOG_DIR="/home/hs-ops/goinsight-logs"
REMOTE_SERVICE_LOG="$REMOTE_LOG_DIR/nohup.out"
REMOTE_AGENT_BINARY="/usr/local/bin/backup-agent"
REMOTE_AGENT_LOG="$REMOTE_LOG_DIR/backup-agent.log"
REMOTE_TMP_DIR="/tmp"
REMOTE_BACKUP_DIR="/tmp/goinsight-backup"

DEPLOY_AGENT=0
AGENT_NODE_ID="1"
AGENT_TOKEN=""
AGENT_SERVER="http://127.0.0.1:8083"

SKIP_FRONTEND=0
SKIP_BACKEND_CHECK=0
SKIP_VERIFY=0
VERSION=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --skip-frontend)
      SKIP_FRONTEND=1
      shift
      ;;
    --skip-backend-check)
      SKIP_BACKEND_CHECK=1
      shift
      ;;
    --skip-verify)
      SKIP_VERIFY=1
      shift
      ;;
    --deploy-agent)
      DEPLOY_AGENT=1
      shift
      ;;
    --agent-token)
      AGENT_TOKEN="${2:-}"
      shift 2
      ;;
    --agent-node-id)
      AGENT_NODE_ID="${2:-}"
      shift 2
      ;;
    --agent-server)
      AGENT_SERVER="${2:-}"
      shift 2
      ;;
    --remote-host)
      REMOTE_HOST="${2:-}"
      shift 2
      ;;
    --remote-user)
      REMOTE_USER="${2:-}"
      shift 2
      ;;
    --ssh-password)
      SSH_PASSWORD="${2:-}"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      die "未知参数: $1"
      ;;
  esac
done

[[ -n "$VERSION" ]] || die "必须提供 --version"
if [[ "$DEPLOY_AGENT" -eq 1 && -z "$AGENT_TOKEN" ]]; then
  die "使用 --deploy-agent 时必须同时提供 --agent-token"
fi

need_cmd go
need_cmd ssh
need_cmd scp
need_cmd sshpass
need_cmd rsync
need_cmd curl
need_cmd tar

if [[ "$SKIP_FRONTEND" -eq 0 ]]; then
  need_cmd npm
fi

BUILD_TIME="$(date '+%Y-%m-%d %H:%M:%S %z')"
GIT_COMMIT="$(git -C "$PROJECT_ROOT" rev-parse --short HEAD 2>/dev/null || true)"
GIT_BRANCH="$(git -C "$PROJECT_ROOT" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
LDFLAGS="-X main.Version=$VERSION -X 'main.BuildTime=$BUILD_TIME' -X main.GitCommit=$GIT_COMMIT -X main.GitBranch=$GIT_BRANCH"

RELEASE_DIR="$ARTIFACT_ROOT/$VERSION"
LOCAL_SERVICE_BINARY="$RELEASE_DIR/goinsight-linux-amd64"
LOCAL_AGENT_BINARY="$RELEASE_DIR/backup-agent"

mkdir -p "$RELEASE_DIR"

SSH_OPTS=(
  -o StrictHostKeyChecking=no
  -o UserKnownHostsFile=/dev/null
)

run_ssh() {
  sshpass -p "$SSH_PASSWORD" ssh "${SSH_OPTS[@]}" "$REMOTE_USER@$REMOTE_HOST" "$@"
}

run_scp() {
  sshpass -p "$SSH_PASSWORD" scp "${SSH_OPTS[@]}" "$@"
}

log "项目目录: $PROJECT_ROOT"
log "发布版本: $VERSION"
log "远端主机: $REMOTE_USER@$REMOTE_HOST"
log "制品目录: $RELEASE_DIR"

if [[ "$SKIP_BACKEND_CHECK" -eq 0 ]]; then
  log "开始后端构建校验: go build ./..."
  (
    cd "$BACKEND_DIR"
    go build ./...
  )
fi

if [[ "$SKIP_FRONTEND" -eq 0 ]]; then
  log "开始前端依赖安装与构建"
  (
    cd "$FRONTEND_DIR"
    if [[ -f package-lock.json ]]; then
      npm ci
    else
      npm install
    fi
    npm run build
  )

  log "同步前端 dist 到 backend/web/dist"
  mkdir -p "$BACKEND_WEB_DIST"
  rsync -a --delete "$FRONTEND_DIR/dist/" "$BACKEND_WEB_DIST/"
else
  warn "已跳过前端构建, 将直接使用当前 backend/web/dist 内容参与 embed"
fi

log "构建 Linux 服务二进制"
(
  cd "$BACKEND_DIR"
  GOOS=linux GOARCH=amd64 go build -ldflags "$LDFLAGS" -o "$LOCAL_SERVICE_BINARY" "$SERVICE_ENTRY"
)
chmod +x "$LOCAL_SERVICE_BINARY"

if [[ "$DEPLOY_AGENT" -eq 1 ]]; then
  log "构建 Linux backup-agent 二进制"
  (
    cd "$BACKEND_DIR"
    GOOS=linux GOARCH=amd64 go build -o "$LOCAL_AGENT_BINARY" "$AGENT_ENTRY_DIR"
  )
  chmod +x "$LOCAL_AGENT_BINARY"
fi

log "上传制品到远端临时目录"
run_scp "$LOCAL_SERVICE_BINARY" "$REMOTE_USER@$REMOTE_HOST:$REMOTE_TMP_DIR/"
if [[ "$DEPLOY_AGENT" -eq 1 ]]; then
  run_scp "$LOCAL_AGENT_BINARY" "$REMOTE_USER@$REMOTE_HOST:$REMOTE_TMP_DIR/"
fi

log "远端备份旧二进制"
run_ssh "mkdir -p '$REMOTE_BACKUP_DIR' '$REMOTE_LOG_DIR';
ts=\$(date +%Y%m%d%H%M%S);
if [[ -f '$REMOTE_SERVICE_BINARY' ]]; then
  cp '$REMOTE_SERVICE_BINARY' '$REMOTE_BACKUP_DIR/goinsight-linux-amd64.'\"\$ts\";
fi
if [[ '$DEPLOY_AGENT' = '1' ]] && [[ -f '$REMOTE_AGENT_BINARY' ]]; then
  echo '$SSH_PASSWORD' | sudo -S cp '$REMOTE_AGENT_BINARY' '$REMOTE_BACKUP_DIR/backup-agent.'\"\$ts\";
fi"

log "停止旧 JaySQL 服务"
run_ssh "pids=\$(ps -eo pid,args | awk '/[g]oinsight-linux-amd64 -config .*config.yaml/ {print \$1}');
if [[ -n \"\$pids\" ]]; then
  kill \$pids || true;
  sleep 2;
fi"

if [[ "$DEPLOY_AGENT" -eq 1 ]]; then
  log "停止旧 backup-agent"
  run_ssh "pids=\$(ps -eo pid,args | awk '/[b]ackup-agent -server .* -node-id '"$AGENT_NODE_ID"'/ {print \$1}');
if [[ -n \"\$pids\" ]]; then
  kill \$pids || true;
  sleep 2;
fi"
fi

log "替换远端服务二进制"
run_ssh "mv '$REMOTE_TMP_DIR/$(basename "$LOCAL_SERVICE_BINARY")' '$REMOTE_SERVICE_BINARY' && chmod +x '$REMOTE_SERVICE_BINARY'"

if [[ "$DEPLOY_AGENT" -eq 1 ]]; then
  log "替换远端 backup-agent 二进制"
  run_ssh "echo '$SSH_PASSWORD' | sudo -S mv '$REMOTE_TMP_DIR/$(basename "$LOCAL_AGENT_BINARY")' '$REMOTE_AGENT_BINARY' && echo '$SSH_PASSWORD' | sudo -S chmod +x '$REMOTE_AGENT_BINARY'"
fi

log "启动 JaySQL 服务"
run_ssh "mkdir -p '$REMOTE_LOG_DIR';
truncate -s 0 '$REMOTE_SERVICE_LOG';
if command -v tmux >/dev/null 2>&1; then
  tmux kill-session -t goinsight >/dev/null 2>&1 || true;
  tmux new-session -d -s goinsight \"$REMOTE_SERVICE_BINARY -config $REMOTE_CONFIG >$REMOTE_SERVICE_LOG 2>&1\";
else
  setsid '$REMOTE_SERVICE_BINARY' -config '$REMOTE_CONFIG' >'$REMOTE_SERVICE_LOG' 2>&1 </dev/null &
fi
sleep 5"

if [[ "$DEPLOY_AGENT" -eq 1 ]]; then
  log "启动 backup-agent"
  run_ssh "truncate -s 0 '$REMOTE_AGENT_LOG';
setsid '$REMOTE_AGENT_BINARY' -server '$AGENT_SERVER' -node-id '$AGENT_NODE_ID' -token '$AGENT_TOKEN' >'$REMOTE_AGENT_LOG' 2>&1 </dev/null &
sleep 5"
fi

if [[ "$SKIP_VERIFY" -eq 0 ]]; then
  log "检查远端服务进程、端口和日志"
  run_ssh "set -e;
echo '--- service process ---';
ps -ef | grep 'goinsight-linux-amd64' | grep -v grep || true;
echo '--- service port ---';
if command -v ss >/dev/null 2>&1; then
  ss -lntp | grep ':8083' || true;
else
  netstat -lntp 2>/dev/null | grep ':8083' || true;
fi
echo '--- service log tail ---';
tail -n 50 '$REMOTE_SERVICE_LOG' || true"

  if [[ "$DEPLOY_AGENT" -eq 1 ]]; then
    log "检查远端 backup-agent 进程和日志"
    run_ssh "echo '--- agent process ---';
ps -ef | grep 'backup-agent' | grep -v grep || true;
echo '--- agent log tail ---';
tail -n 50 '$REMOTE_AGENT_LOG' || true"
  fi

  log "本地访问 102 页面做基础校验"
  curl -I --max-time 10 "http://$REMOTE_HOST:8083/"
  curl -s --max-time 10 "http://$REMOTE_HOST:8083/" | head -n 5
fi

cat <<EOF

[DONE] 构建与发布完成
  版本号:          $VERSION
  本地服务包:      $LOCAL_SERVICE_BINARY
  远端服务包:      $REMOTE_SERVICE_BINARY
  远端配置文件:    $REMOTE_CONFIG
  远端服务日志:    $REMOTE_SERVICE_LOG
  是否发布 Agent:  $DEPLOY_AGENT
EOF

if [[ "$DEPLOY_AGENT" -eq 1 ]]; then
  cat <<EOF
  Agent 二进制:    $REMOTE_AGENT_BINARY
  Agent 日志:      $REMOTE_AGENT_LOG
  Agent Node ID:   $AGENT_NODE_ID
EOF
fi

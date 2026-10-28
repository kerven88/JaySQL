#!/bin/bash
# ============================================================================
# Prometheus + node_exporter 部署脚本（幂等，可重复执行）
#
# 用法：
#   ./deploy.sh node-exporter            # 在被监控主机上部署 node_exporter(:9100)
#   ./deploy.sh prometheus <tar.gz路径>  # 在 Prometheus 主机上部署 Prometheus(127.0.0.1:9090)
#
# 目录布局（沿用 /data/prometheus 下既有的"版本目录 + 软链"风格）：
#   /data/prometheus/prometheus-<ver>.linux-amd64/   程序本体
#   /data/prometheus/prometheus -> 上述目录            软链
#   /data/prometheus/prometheus.yml                   配置
#   /data/prometheus/data/                            TSDB（保留 15 天）
#   /data/prometheus/node_exporter -> node_exporter-<ver>.linux-amd64
#
# 【为什么不能只配 JaySQL】
# JaySQL 的 PromQL 用 instance=~"^<hostname>(?::[0-9]+)?$" 过滤，
# target 地址必须与 insight_instances.hostname 登记值一致，否则资源列永远为空。
#
# 【受限网络下如何获取二进制】
# centos01/02 无法直连 github 与国内镜像站 HTTPS；由可出网的机器下载后 scp：
#   curl -sL -o prometheus.tgz \
#     "https://gh-proxy.com/https://github.com/prometheus/prometheus/releases/download/v2.55.1/prometheus-2.55.1.linux-amd64.tar.gz"
# 若某代理限速，可换 gh-proxy.com / ghproxy.net / ghfast.top（实测 gh-proxy 最快）。
# ============================================================================
set -euo pipefail

BASE=/data/prometheus
PROM_VERSION=prometheus-2.55.1.linux-amd64
NODE_VERSION=node_exporter-1.9.1.linux-amd64
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
  exit 1
}

ensure_user() {
  local user=$1
  if ! id "$user" >/dev/null 2>&1; then
    useradd -r -s /sbin/nologin -d "/home/$user" -M "$user"
    echo "    已创建系统用户 $user"
  fi
}

deploy_node_exporter() {
  echo "==> 部署 node_exporter（监听 :9100，供 Prometheus 抓取）"

  local bin_dir="$BASE/$NODE_VERSION"
  if [ ! -x "$bin_dir/node_exporter" ]; then
    if [ -f "$BASE/$NODE_VERSION.tar.gz" ]; then
      echo "    从 $BASE/$NODE_VERSION.tar.gz 解压"
      tar -xzf "$BASE/$NODE_VERSION.tar.gz" -C "$BASE"
    else
      echo "!!  未找到 $bin_dir/node_exporter 或 $BASE/$NODE_VERSION.tar.gz" >&2
      echo "    请先放置 node_exporter 安装包（见脚本头部获取方式）" >&2
      exit 1
    fi
  fi

  ensure_user node_exporter
  chown root:root "$bin_dir/node_exporter"
  chmod 755 "$bin_dir/node_exporter"
  ln -sfn "$NODE_VERSION" "$BASE/node_exporter"

  install -m 644 "$SCRIPT_DIR/node_exporter.service" /etc/systemd/system/node_exporter.service
  systemctl daemon-reload
  systemctl enable node_exporter >/dev/null 2>&1 || true
  systemctl restart node_exporter
  sleep 3

  systemctl is-active node_exporter >/dev/null || {
    echo "!!  node_exporter 启动失败，journalctl -u node_exporter -n 30 查看" >&2
    exit 1
  }
  echo "    node_exporter $("$BASE/node_exporter/node_exporter" --version 2>&1 | head -1 | awk '{print $3}') 已启动"
  ss -lntp 2>/dev/null | grep -q ':9100' && echo "    :9100 监听正常" || echo "    !! :9100 未监听"
}

deploy_prometheus() {
  local tarball=${1:-}
  if [ -z "$tarball" ] || [ ! -f "$tarball" ]; then
    echo "用法: $0 prometheus <prometheus-<ver>.linux-amd64.tar.gz 路径>" >&2
    exit 1
  fi

  echo "==> 部署 Prometheus（监听 127.0.0.1:9090）"

  if [ ! -d "$BASE/$PROM_VERSION" ]; then
    tar -xzf "$tarball" -C "$BASE"
  fi
  chown -R root:root "$BASE/$PROM_VERSION"
  chmod 755 "$BASE/$PROM_VERSION/prometheus" "$BASE/$PROM_VERSION/promtool"
  ln -sfn "$PROM_VERSION" "$BASE/prometheus"

  # 配置：仓库里的 prometheus.yml 是基线，服务器上已有则保留（避免覆盖现场调整）
  if [ ! -f "$BASE/prometheus.yml" ]; then
    install -m 644 "$SCRIPT_DIR/prometheus.yml" "$BASE/prometheus.yml"
    echo "    已安装配置 $BASE/prometheus.yml（记得按实例登记值核对 targets）"
  else
    echo "    沿用已有配置 $BASE/prometheus.yml（未覆盖）"
  fi

  ensure_user prometheus
  mkdir -p "$BASE/data"
  chown -R prometheus:prometheus "$BASE/data"
  chmod 750 "$BASE/data"

  install -m 644 "$SCRIPT_DIR/prometheus.service" /etc/systemd/system/prometheus.service
  systemctl daemon-reload

  echo "    校验配置语法"
  "$BASE/prometheus/promtool" check config "$BASE/prometheus.yml"

  systemctl enable prometheus >/dev/null 2>&1 || true
  systemctl restart prometheus
  sleep 5
  systemctl is-active prometheus >/dev/null || {
    echo "!!  Prometheus 启动失败，journalctl -u prometheus -n 30 查看" >&2
    exit 1
  }
  curl -sf --max-time 5 http://127.0.0.1:9090/-/ready >/dev/null \
    && echo "    /-/ready 就绪" || echo "    !! /-/ready 未就绪"
  echo ""
  echo "    抓取目标状态：curl -s localhost:9090/api/v1/targets | python -m json.tool | grep -E 'health|instance'"
}

case "${1:-}" in
  node-exporter) deploy_node_exporter ;;
  prometheus)    deploy_prometheus "${2:-}" ;;
  *)             usage ;;
esac

#!/usr/bin/env python
# -*- coding: utf-8 -*-
r"""用 goInsight 修复后的 PromQL 逐条实测 Prometheus，确认 instance 正则命中且有值。

修复点：不能再用 regexp.QuoteMeta 产出的 `\.`（PromQL 字面量非法转义），
改用字符类 `[.]`。本脚本与 Go 侧 prometheusInstancePattern 的行为保持一致。
兼容 python2（CentOS 7 默认）。"""
import json
import sys

try:
    from urllib import urlencode
    from urllib2 import urlopen, HTTPError, URLError
except ImportError:
    from urllib.parse import urlencode
    from urllib.request import urlopen
    from urllib.error import HTTPError, URLError

BASE = "http://127.0.0.1:9090/api/v1/query"


def instance_pattern(host):
    """与 Go 侧 prometheusInstancePattern 等价：点号 -> 字符类，剔除反斜杠。"""
    return host.replace("\\", "").replace(".", "[.]")


QUERIES = [
    ("cpu%", 'clamp_min(100 - (avg(irate(node_cpu_seconds_total{instance=~"^HOST(?::[0-9]+)?$",mode="idle"}[5m])) * 100), 0)'),
    ("memory%", 'clamp_min((1 - (avg(node_memory_MemAvailable_bytes{instance=~"^HOST(?::[0-9]+)?$"}) / avg(node_memory_MemTotal_bytes{instance=~"^HOST(?::[0-9]+)?$"}))) * 100, 0)'),
    ("disk%", 'max(100 * (1 - (node_filesystem_avail_bytes{instance=~"^HOST(?::[0-9]+)?$",fstype!~"tmpfs|overlay|squashfs|ramfs|nsfs"} / node_filesystem_size_bytes{instance=~"^HOST(?::[0-9]+)?$",fstype!~"tmpfs|overlay|squashfs|ramfs|nsfs"})))'),
    ("io%", 'sum(irate(node_disk_io_time_seconds_total{instance=~"^HOST(?::[0-9]+)?$",device!~"loop.*|ram.*"}[5m])) * 100'),
    ("net_in", 'sum(irate(node_network_receive_bytes_total{instance=~"^HOST(?::[0-9]+)?$",device!~"lo|docker.*|veth.*|br.*|cni.*"}[5m]))'),
    ("net_out", 'sum(irate(node_network_transmit_bytes_total{instance=~"^HOST(?::[0-9]+)?$",device!~"lo|docker.*|veth.*|br.*|cni.*"}[5m]))'),
]


def query(expr):
    url = BASE + "?" + urlencode({"query": expr})
    try:
        raw = urlopen(url, timeout=10).read().decode("utf-8")
    except HTTPError as exc:
        return "HTTP-%s %s" % (exc.code, exc.read()[:160])
    except URLError as exc:
        return "HTTP-ERR %s" % exc
    try:
        payload = json.loads(raw)
    except ValueError:
        return "BAD-JSON"
    if payload.get("status") != "success":
        return "API-ERR %s" % payload.get("error")
    result = payload["data"]["result"]
    if not result:
        return "EMPTY  <- 匹配失败"
    return "value=%-22s (series=%d)" % (result[0]["value"][1], len(result))


def main():
    hosts = sys.argv[1:] or ["192.168.1.99", "192.168.1.100"]
    failed = 0
    for host in hosts:
        print("--- 实例登记值: %s ---" % host)
        for label, tpl in QUERIES:
            out = query(tpl.replace("HOST", instance_pattern(host)))
            if out.startswith("EMPTY") or "ERR" in out or out.startswith("HTTP"):
                failed += 1
            print("  %-9s %s" % (label, out))
        print("")
    print("结论: %s" % ("6 项全部命中" if failed == 0 else "有 %d 项未命中" % failed))
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())

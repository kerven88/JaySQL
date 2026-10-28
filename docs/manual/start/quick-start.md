---
title: 快速开始
group: start
order: 40
icon: thunderbolt
desc: 从零部署到跑通第一个备份任务
updated: 2026-10-09
reading: 8 分钟
tags: [入门, 实操]
keywords: [部署, 安装, 启动, config.yaml, 元数据库, 初始化, 纳管实例, 第一个备份]
---

按以下步骤，30 分钟内完成 JaySQL 部署并跑通第一个备份任务。

## 准备

- 一台 Linux 服务器（服务端），2C4G 起步，能访问所有被管数据库
- 一个 MySQL 实例作为**元数据库**（建议 5.7 / 8.0，字符集 utf8mb4）
- 被管数据库主机需要部署 **backup-agent**（仅备份功能需要）

## 部署服务端

### 创建元数据库与账号

```sql
CREATE DATABASE jaysql DEFAULT CHARACTER SET utf8mb4;
CREATE USER 'jaysql'@'%' IDENTIFIED BY 'YourPassword';
GRANT ALL PRIVILEGES ON jaysql.* TO 'jaysql'@'%';
```

### 配置 config.yaml

```yaml
listen_address: "0.0.0.0:8083"
database:
  host: "192.168.1.99"
  port: 3306
  user: "jaysql"
  password: "YourPassword"
  dbname: "jaysql"
log:
  root_dir: "/opt/app/goinsight/logs"
```

日志目录建议写**绝对路径**。相对路径会相对进程工作目录解析，进程重启方式一变，日志就可能落到别处。

### 启动服务

```bash
./goinsight -config /opt/app/goinsight/config.yaml
# 建议用 systemd 托管，日志在 root_dir 下的 app.log 与 *-request.log
```

首次启动会自动建表并写入种子数据（用户、角色、权限点、监控指标字典等）。

### 登录

浏览器打开 `http://<服务器IP>:8083`，使用初始管理员账号登录，**登录后立即修改密码**。

## 纳管第一个实例

1. 进入**系统管理 → 环境管理**，创建环境（如「生产」「测试」）
2. 进入实例管理，填写连接信息（主机、端口、引擎、账号密码），保存后做一次连通性测试
3. 在 **SQL工作台 → SQL查询** 试跑一条 `SELECT 1` 验证链路

:::info 看不到某个库
是数据访问授权未包含该 Schema，不是实例没纳管。到「系统管理 → 数据访问」按用户或角色授权。
:::

## 跑通第一个备份任务

1. **备份恢复 → 存储目标**：新增目标
   - 本地服务器：需完成 **SSH 指纹确认**
   - 对象存储（OSS / COS / OBS / MinIO）：需通过**连通测试**
2. 在目标主机安装 **backup-agent**，确认日志中 poll / heartbeat 正常
3. **数据备份 → 新建任务**：选择实例与库表 → 选择存储目标 → 配置计划与保留策略 → 保存
4. 手动触发一次执行，在任务详情的执行记录里查看产物路径与日志
5. 到**数据恢复**做一次 **PLAN** 演练（不真正写数据），确认预检通过

:::danger 别跳过第 5 步
备份的价值在于可恢复。第一次配置完成后，务必在测试库做一次真实恢复验证，并把它记入值班手册。
:::

### agent 上线的判断标准

tail 执行节点日志：

```bash
tail -f /usr/local/goinsight/backup-agent/backup-agent.log
```

- 正常：周期性出现 poll / heartbeat 成功记录
- 异常：`context deadline exceeded` / `i/o timeout`——这是 **TCP 握手没完成**，与接口参数、token 无关

排查顺序：

```bash
# 1) 看代理（最常见、最容易忽略）
P=$(pgrep -f backup-agent | head -1); tr '\0' '\n' < /proc/$P/environ | grep -i proxy

# 2) 看路由
ip route get 192.168.1.99

# 3) 看握手
nc -vz -w 3 192.168.1.99 8083 || echo "TCP FAIL"
curl -s --noproxy '*' -o /dev/null -w 'http=%{http_code}\n' http://192.168.1.99:8083/
```

命中代理问题的解法：在 service 里加 `Environment="NO_PROXY=192.168.1.99,127.0.0.1,localhost"` 后重启 agent。

## 下一步

- 配置[告警渠道与策略](../guide/monitor-alert.md)（监控告警 → 告警配置）
- 配置[数据访问授权](../guide/system-admin.md)，让研发自助查询
- 接入[大模型](../guide/ai-assistant.md)启用 AI 助手（系统管理 → 系统配置 → AI 能力）

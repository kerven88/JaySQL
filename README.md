# JaySQL

<p align="center">
  <b>面向数据库运维与研发协作的一体化管理平台</b>
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/static/v1?label=License&message=MIT&color=2ea44f&style=flat-square" /></a>
  <img src="https://img.shields.io/static/v1?label=Go&message=1.23&color=00ADD8&style=flat-square&logo=go" />
  <img src="https://img.shields.io/static/v1?label=Vue&message=3&color=4FC08D&style=flat-square&logo=vue.js" />
  <a href="https://github.com/simajiong/JaySQL/releases"><img src="https://img.shields.io/github/v/release/simajiong/JaySQL.svg?style=flat-square&color=orange" /></a>
  <img src="https://img.shields.io/github/stars/simajiong/JaySQL?style=flat-square&logo=github" />
</p>

JaySQL 把「查询与变更、备份与恢复、性能与监控、审计与权限」收敛到同一个控制台：纳管多引擎实例，
SQL 查询受数据授权约束，变更走工单 + 规范预审 + 审批流，备份可定时可恢复，慢 SQL 与指标有统一视图，所有动作留痕可回放。

> 平台只保存**元数据、任务记录与审计**，不代持业务数据 —— 业务数据始终停留在被管实例与存储目标上。

---

## 功能模块

| 模块 | 关键能力 | 入口 |
| :--- | :------- | :--- |
| 首页 | 实例总览、待办与风险、快捷入口 | `/home` |
| AI 助手 | 服务商接入、模型管理、SQL 生成与优化建议、文档问答 | `/ai` |
| SQL 工作台 | 在线查询（受数据授权约束）、新建工单、工单列表与详情 | `/sql-workbench` |
| 备份恢复 | 备份任务、备份集、存储目标、恢复向导（含预检与二次确认） | `/backup` |
| 性能分析 | 性能概览、慢 SQL 采集与治理、负载快照 | `/performance` |
| 监控告警 | 监控大屏、实例明细、健康分、告警中心与多渠道通知 | `/monitor` |
| 安全审计 | 操作日志、策略配置 | `/audit` |
| 系统管理 | 用户 / 角色 / 权限点、数据访问授权、环境、系统配置 | `/admin` |
| 文档中心 | 产品文档（随代码构建）、AI 文档问答 | `/docs` |

一次典型路径：纳管实例 → 授权研发可查哪些库 → 自助查询 / 提交变更工单 → DBA 审批执行 → 定时备份与恢复演练 → 慢了查慢 SQL → 出事看监控告警 → 事后查审计。

---

## 支持的数据库

能力随引擎逐步落地，下表为**当前已实现**的口径：

| 引擎 | SQL 查询 | SQL 审核 | 变更工单 | 备份恢复 | 监控采集 | 慢SQL |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: |
| **MySQL** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| **PostgreSQL** | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| **Oracle** | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ |
| **SQL Server** | ✅ | ❌ | ❌ | ❌ | ✅ | ❌ |
| **TiDB** | ✅ | ✅ | ✅ | ✅ | ✅ | ❌ |
| **ClickHouse** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **Doris** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| **OceanBase** | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |

SQL 审核内置 **279 条标准规则**：MySQL 82、TiDB 82、PostgreSQL 58、Oracle 57，
支持全局与实例级参数配置、规则开关与级别调整（级别唯一数据源是 `insight_sql_standard_rules.level`）。

---

## 架构

![JaySQL 分层架构](docs/manual/assets/architecture.svg)

| 层 | 组成 | 说明 |
| :-- | :--- | :--- |
| 接入层 | Vue 3 + Ant Design Vue 控制台、REST API、WebSocket 日志推送 | 唯一用户入口 |
| 应用层 | Go + Gin + GORM，含工作台 / 工单 / 备份 / 监控 / 性能 / 审计与定时 cron | 业务编排与调度 |
| 数据层 | 元数据库（MySQL）、指标库（Prometheus）、存储目标 | 平台自身数据都在这里 |
| 执行层 | backup-agent + mysqldump / pg_dump / xtrabackup | 跑在被管主机上 |
| 采集层 | node_exporter + 数据库采集 SQL + 慢日志采集器 | 指标与慢日志来源 |

---

## 部署依赖

| 依赖 | 是否必须 | 说明 |
| :--- | :------- | :--- |
| **MySQL**（元数据库） | ✅ 必须 | 存放平台自身的用户、工单、监控、审计数据，≥ 5.7，字符集 utf8mb4 |
| **Prometheus + node_exporter** | ⭕ 可选 | 仅在需要主机资源层指标（CPU / 内存 / 磁盘 / IO / 网络）时需要；引擎层指标由平台直连数据库采集 |

---

## 快速开始

### 方式一：下载二进制

前往 [Releases](https://github.com/simajiong/JaySQL/releases) 获取对应平台的包，解压后：

```bash
cp config.yaml.template config.yaml   # 修改 secret_key 与数据库连接
./goinsight -config config.yaml
```

> 二进制名沿用 `goinsight`（沿用上游历史命名，与 JaySQL 同物）；systemd 部署时服务名与安装目录同理。

### 方式二：从源码构建

```bash
# 1. 构建前端（会先执行 docs:build 生成文档中心产物）
cd www && npm install && npm run build

# 2. 同步前端产物到后端 embed 目录
#    backend/web/web.go 里有 //go:embed dist，产物必须落在这里，否则编译失败
rm -rf ../backend/web/dist && cp -r dist ../backend/web/dist

# 3. 编译后端
cd ../backend
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o goinsight ./cmd

# 4. 准备配置并启动
cp config.yaml.template config.yaml
./goinsight -config config.yaml
```

也可以直接用打包脚本（产出 linux-amd64 与 darwin-arm64 两个包 + SHA256）：

```bash
./scripts/build.sh --version v1.0.0
```

⚠️ 构建前确认 `backend/internal/orders/api/mysql/embedded/{linux-amd64,linux-arm64}/gh-ost` 存在 ——
它被 `go:embed` 引用，缺失会直接编译失败。

### 初始化与登录

首次启动会自动建表并写入种子数据（管理员、角色、权限点、审核规则、监控指标字典等）。

* 访问：<http://your-ip:8083>
* 初始管理员：`admin` / `Admin@2026`

⚠️ 安全建议：

* 登录后**立即修改密码**
* `config.yaml` 里的 `secret_key` 必须替换为自己的 32 位随机字符串（它与 JWT 签名同源，泄露即等于可伪造登录凭证）
* 生产环境前置 Nginx 并配置 HTTPS

---

## 目录结构

```
jaysql/
├── backend/                 # 服务端（Go + Gin + GORM）
│   ├── cmd/                 # 入口：main.go 及两个运维工具
│   ├── internal/            # 业务模块：orders / das / backup / monitor / performance / inspect / ai / users ...
│   ├── pkg/                 # 可复用基础设施：eventbus / lock / notifier / parser / secret ...
│   ├── sql/                 # 演进式 SQL 脚本（按日期命名，人工执行并留审计）
│   ├── config.yaml.template # 配置模板（不含任何真实凭据）
│   └── go.mod               # module github.com/simajiong/jaysql
├── www/                     # 前端（Vue 3 + Vite + Ant Design Vue）
│   ├── src/views/           # 按模块划分的页面
│   └── scripts/             # build-docs.mjs：把 docs/manual 转成文档中心产物
├── docs/
│   ├── manual/              # 文档中心内容源（随代码走，构建期转 HTML）
│   └── plans/               # 内部设计与方案文档（不进产品界面）
├── scripts/                 # 构建与部署脚本
├── THIRD_PARTY_LICENSES/    # 上游许可存档
└── LICENSE
```

---

## 配置要点

配置文件为单个 `config.yaml`，主要段落：

| 段落 | 作用 |
| :--- | :--- |
| `app` | 监听地址、环境（dev/prod）、`secret_key`（32 位，用于 JWT 与密码加密） |
| `database` | 元数据库连接：`username` / `password` / `database` / `host` / `port` |
| `das` | 查询最大执行时间、默认与最大返回行数 |
| `monitor` | Prometheus 地址（仅资源层指标用）；可用环境变量 `GOINSIGHT_PROMETHEUS_URL` / `PROMETHEUS_URL` 覆盖 |
| `ghost` | gh-ost 工具路径与固定参数 |
| `notify` | 钉钉 / 企业微信 / 邮件渠道开关与 webhook |
| `ai` | AI 能力总开关、故障切换与降级策略 |

两个约定，改之前建议先知道：

* **大模型凭据（API Key / 端点）不写配置文件**，存在 `insight_llm_providers` 表里并做 AES-256-GCM 加密
* **实例密码同样加密入库**，不放明文配置；`config.yaml` 已在 `.gitignore` 中，请勿提交

---

## 文档中心

`docs/manual/**` 是产品文档的**唯一内容源**，随代码走、构建期转 HTML，运行时零解析：

* 入门：什么是 JaySQL / 功能概览 / 产品架构 / 快速开始
* 指南：AI 助手 / SQL 工作台 / 备份恢复 / 性能分析 / 监控告警 / 安全审计 / 系统管理

本地预览文档改动：`cd www && npm run docs:build`（`predev` / `prebuild` 钩子会自动执行）。
写作规范见 [`docs/manual/README.md`](docs/manual/README.md)。

---

## 参与开发

```bash
# 前端
cd www && npm install && npm run dev      # 启动开发服务（vite 代理指向 8083）
npm run lint                              # ESLint
npm run build                             # 构建（会先跑 docs:build）

# 后端
cd backend
go build ./...
go vet ./...
go test ./...
```

测试约定（避免把真实环境信息写进代码）：

* 需要连真实 MySQL 的用例读环境变量 `MATRIX_MYSQL_PASS`，未设置则自动 skip
* 备份链路调试用的 SSH 密码走 `BACKUP_DEBUG_SSH_PASS`
* **不要在测试或配置里硬编码真实密码、内网 IP**

改动文档站内容时，同步更新 front-matter 的 `updated` 字段。

---

## 反馈与贡献

* 🐛 Bug 与需求：提交 [Issue](https://github.com/simajiong/JaySQL/issues)
* 🔧 代码贡献：欢迎 Pull Request，请保证 `go build ./...`、`go vet ./...` 与 `npm run lint` 通过

---

## Credits

本项目衍生自 [goInsight](https://github.com/lazzyfu/goInsight)（Copyright (c) 2024 xff），在其工单与数据查询内核之上做了大量扩展。
上游以 MIT 协议发布，其原始版权声明与许可条款全文保存在 [`THIRD_PARTY_LICENSES/goInsight-MIT.txt`](THIRD_PARTY_LICENSES/goInsight-MIT.txt)。

同时感谢：

* [gh-ost](https://github.com/github/gh-ost) —— GitHub 的 MySQL 在线表结构变更工具
* [Gin](https://github.com/gin-gonic/gin) / [GORM](https://gorm.io) —— 后端框架与 ORM
* [Vue 3](https://vuejs.org) / [Ant Design Vue](https://antdv.com) —— 前端框架与组件库
* [Prometheus](https://prometheus.io) + node_exporter —— 主机资源层指标采集

## License

本项目采用 [MIT](LICENSE) 协议。

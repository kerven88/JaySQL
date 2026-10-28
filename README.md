# 🚀 JaySQL

<p align="center">
  <img src="https://img.shields.io/static/v1?label=License&message=MIT&color=2ea44f&style=flat-square" />
  <a href="https://github.com/simajiong/JaySQL/releases">
    <img src="https://img.shields.io/github/v/release/simajiong/JaySQL.svg?style=flat-square&color=orange" />
  </a>
  <a href="https://github.com/simajiong/JaySQL/releases">
    <img src="https://img.shields.io/github/downloads/simajiong/JaySQL/total.svg?style=flat-square&color=blue" />
  </a>
  <img src="https://img.shields.io/github/stars/simajiong/JaySQL?style=flat-square&logo=github" />
</p>

<p align="center">
  <b> 企业级数据库工单与数据查询平台 </b>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Backend-Gin-00ADD8?style=for-the-badge&logo=go" />
  <img src="https://img.shields.io/badge/Frontend-Vue3-4FC08D?style=for-the-badge&logo=vue.js" />
  <img src="https://img.shields.io/badge/Database-MySQL%20%7C%20TiDB-4479A1?style=for-the-badge&logo=mysql" />
</p>

## 📖 项目简介

**JaySQL** 是一个基于 **Gin + Vue3** 的企业级数据库 WEB 管理平台，提供**数据库工单**和**数据查询**能力，解决 DBA 与开发人员之间协作低效、操作高危、审计缺失等痛点。

## 🗺️ 文档与预览

* 📌 使用手册见 [`docs/manual/`](docs/manual/)（架构说明、快速开始、各模块指南）
* 📌 功能预览截图待补充

## 🛠️ 核心功能模块

| 功能模块       | 功能说明                                                                        |
| :------------- | :------------------------------------------------------------------------------ |
| **📝 工单管理** | 支持DDL/DML/数据导出（CSV / XLSX）工单；支持审批、执行、回滚及一键复制工单 |
| **⚖️ 审批流**   | 支持会签/或签，可按组织架构自定义审批流程与层级                          |
| **🔍 SQL 审计** | 内置高性能语法审核器，支持68种审核规则；支持全局及实例级参数配置         |
| **🔓 数据查询** | 支持库表级访问权限控制与查询审计           |
| **👥 权限组织** | 基于组织架构的用户角色管理；DB实例按组织绑定，实现访问隔离               |
| **🌐 环境隔离** | 支持自定义多个工单环境（测试、预发、生产），确保变更发布流程规范化              |
| **🔔 消息通知** | 多实例通知渠道（钉钉/飞书/企业微信/邮件）+ 通知策略，按告警路由到指定渠道与接收人                      |
| **🛡️ 其它** | 支持双因素认证                       |

## 📦 部署依赖

| 依赖 | 是否必须 | 说明 |
| :--- | :------- | :--- |
| **MySQL / TiDB**（元数据库） | ✅ 必须 | 存放平台自身的用户、工单、监控等数据，≥ 5.7 |
| **Redis** | ❌ **不需要** | 自 2026-10 起已移除该依赖：工单执行日志推送走进程内事件总线，执行锁走进程内带租约锁。旧 `config.yaml` 里的 `redis` 段可整段删除 |
| **Prometheus + node_exporter** | ⭕ 可选 | 仅在需要采集主机资源层指标（CPU / 内存 / 磁盘 / IO / 网络）时才需要；引擎层指标由平台直连数据库采集 |

> ⚠️ **部署形态**：JaySQL 的单实例部署（单二进制 + systemd）是受支持的形态。
> 工单执行锁与实时消息总线都是**进程内**实现，因此**暂不支持多实例横向扩展**——
> 多副本同时运行时，工单并发保护与日志推送不会跨进程生效。

## 🗄️ 支持数据库

| 数据库引擎         | 版本要求 | 核心能力说明                                                        |
| :----------------- | :------- | :------------------------------------------------------------------ |
| **MySQL / 云服务** | ≥ 5.7    | DML工单（含回滚）；Online DDL (gh-ost)；数据加密导出；数据查询库表级权限控制 |
| **TiDB**           | ≥ 4.0    | DML工单（不支持生成回滚SQL）；原生Online DDL；数据加密导出；数据查询库表级权限控制     |

> 📝 待补充：项目已扩展 PostgreSQL / Oracle / SQL Server / MongoDB 的支持，此处表格尚未同步，请按实际能力补全。

## 🚀 快速开始

### 方式一：下载二进制包

前往 [Releases](https://github.com/simajiong/JaySQL/releases) 页面获取最新的安装包。

```bash
./goinsight-linux-amd64 -config config.yaml
```

### 方式二：从源码构建

```bash
# 1. 构建前端
cd www && npm install && npm run build

# 2. 把前端产物同步到后端 embed 目录
#    （backend/web/web.go 里 //go:embed dist，构建产物必须落在这里）
rm -rf ../backend/web/dist && cp -r dist ../backend/web/dist

# 3. 编译后端（Linux 部署）
cd ../backend
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o goinsight ./cmd

# 4. 准备配置并启动
cp config.yaml.template config.yaml   # 修改其中的 secret_key 与数据库连接
./goinsight -config config.yaml
```

⚠️ 构建前请确认 `backend/internal/orders/api/mysql/embedded/{linux-amd64,linux-arm64}/gh-ost` 存在 —— 它被 `go:embed` 引用，缺失会导致编译失败。

### 访问系统

在浏览器打开：<http://your-ip:8083>

* 默认管理员： admin
* 默认密码： 1234.Com!

⚠️ 安全建议： 首次登录请立即修改密码；`config.yaml` 里的 `secret_key` 必须替换为自己的 32 位随机字符串（它与登录凭证签名同源）。生产环境强烈建议前置 Nginx 并配置 HTTPS 反向代理。

## 🤝 联系与支持

如果您在使用过程中发现 Bug 或有功能建议，欢迎提交 Issue 或 Pull Request。

* 🐛 Bug 反馈与功能建议：请提交 [Issue](https://github.com/simajiong/JaySQL/issues)
* 🔧 代码贡献：欢迎提交 Pull Request

## 🙏 Credits

本项目衍生自 [goInsight](https://github.com/lazzyfu/goInsight)（Copyright (c) 2024 xff），在其工单与数据查询内核之上进行了大量扩展。

goInsight 以 MIT 协议发布，其原始版权声明与许可条款全文保存在 [`THIRD_PARTY_LICENSES/goInsight-MIT.txt`](THIRD_PARTY_LICENSES/goInsight-MIT.txt)。

同时感谢以下开源项目：

* [gh-ost](https://github.com/github/gh-ost) —— GitHub 的 MySQL 在线表结构变更工具
* [Gin](https://github.com/gin-gonic/gin) / [GORM](https://gorm.io) —— 后端框架与 ORM
* [Vue 3](https://vuejs.org) / [Ant Design Vue](https://antdv.com) —— 前端框架与组件库
* [Prometheus](https://prometheus.io) + node_exporter —— 主机资源层指标采集

## 📄 License

本项目采用 [MIT](LICENSE) 协议。

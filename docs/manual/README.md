# JaySQL 文档站 · 源码规范

本目录是【文档中心】模块的内容源，**随代码走、构建期转 HTML**。改动走 PR review，发布随版本一起上线。

> 与 `docs/` 根目录下其他 Markdown 的区别：
>
> - `docs/order-ddl-support.md`、`docs/plans/*.md` —— **内部设计/方案文档**，面向开发，不进产品界面
> - `docs/manual/**` —— **产品文档站源码**，面向用户，会被构建进前端产物

落地实施方案（构建脚本、一级菜单入口、AI 文档问答）见
[`docs/plans/2026-10-09-docs-center-implementation.md`](../plans/2026-10-09-docs-center-implementation.md)，
界面样式参照 `jay-docs-module-demo.html`（工作区内的设计 Demo）。

---

## 目录约定

```
docs/manual/
├── README.md          # 本文件：规范与约定
├── nav.json           # 导航顺序（唯一排序源，改顺序只动这里）
├── assets/            # 图片、SVG 等静态资源
├── start/             # 分组：入门
│   ├── what-is-jaysql.md
│   ├── overview.md
│   ├── architecture.md
│   └── quick-start.md
└── guide/             # 分组：功能指南
    ├── home.md
    ├── ai-assistant.md
    ├── sql-workbench.md
    ├── backup-restore.md
    ├── performance.md
    ├── monitor-alert.md
    ├── audit.md
    └── system-admin.md
```

规则：

1. **分组 = 一级目录**，一个子目录一组，目录名即分组 ID（当前只有 `start`、`guide`）。
2. **一篇文档 = 一个 `.md` 文件**，文件名即 URL slug（小写、连字符），**不要在文件名里带序号**。
3. 顺序、图标、描述一律写在 **front-matter**，由 `nav.json` 决定组内排列。
4. 图片放 `assets/`，用相对路径引用：`![产品架构](./assets/architecture.svg)`。

---

## front-matter 字段

每篇必须以 YAML front-matter 开头：

```yaml
---
title: 备份恢复
group: guide
order: 40
icon: safety
desc: 备份任务、存储目标、备份集与恢复向导
updated: 2026-10-09
reading: 10 分钟
tags: [功能, 核心]
keywords: [备份, 恢复, 存储目标, 备份集, xtrabackup]
---
```

| 字段 | 必填 | 用途 |
| --- | --- | --- |
| `title` | 是 | 页面标题与导航标题 |
| `group` | 是 | 所属分组（子目录名） |
| `order` | 是 | 组内排序，步长 10 便于插入 |
| `icon` | 是 | 图标名（映射到 antd 图标） |
| `desc` | 是 | 导航副标题、搜索摘要、卡片描述 |
| `updated` | 是 | 最后更新日期，展示在页面元信息 |
| `reading` | 否 | 预估阅读时长 |
| `tags` | 否 | 页面右上角标签 |
| `keywords` | 是 | **AI 检索用**，补全称、别名、常见问法 |

> `keywords` 是给 AI 助手用的，写得越全检索越准。例如「恢复」这篇要写：`恢复、还原、找回、误删、rollback、restore`。

---

## 正文写作约定

### 3.1 结构

```markdown
导语：一句话说清这篇解决什么问题（会自动渲染为页面副标题）

## 概念 / 它是什么

### 它解决什么问题

## 怎么用（步骤）

## 配置参考（表格）

## 常见问题 / 排查
```

统一的收尾节奏：**概念 → 用法 → 配置 → 排查**。读者无论是"第一次用"还是"出事了来查"，都能在同一篇里找到。

**标题层级（硬约束）：**

- **标题里不要写序号**——既不要 `## 一、概念` 这种中文序号，也不要 `### 1. 步骤` 这种阿拉伯数字。
  层级本身就是顺序，写了序号反而在插入/调整章节时要全篇改号（历史上已经踩过：12 篇里 60 处 h2 全带序号）。
- **层级从 `##` 开始**：`#` 留给 front-matter 的 `title`（页面渲染为大标题），正文第一条标题必须是 `##`。
- **不跳级**：不能出现 `##` 直接接 `####`；`###` 必须挂在最近的 `##` 之下。侧边栏目录按 h2 → h3 组装成两级树，
  跳级会让小节挂错父节点。层级要更深时优先考虑拆成两篇，而不是用到 h4。

### 3.2 提示块

统一用容器语法（`:::type 标题` … `:::`），构建期映射为对应样式的 callout：

```markdown
:::tip 端点怎么写
多数国内厂商提供 OpenAI 兼容端点，填到 `/v1` 即可。
:::

:::warn 别跳过这一步
备份的价值在于可恢复，务必在测试库做一次真实恢复验证。
:::
```

| 类型 | 用途 | 颜色 |
| --- | --- | --- |
| `info` | 补充事实、口径说明 | 蓝 |
| `tip` | 最佳实践、省事技巧 | 主色（青） |
| `warn` | 容易踩的坑、必须注意 | 橙 |
| `danger` | 不可逆操作、硬约束 | 红 |

### 3.3 其他元素

- 表格用 GFM 语法，首行为表头
- 代码块标注语言：` ```bash `、` ```sql `、` ```yaml `、` ```go `
- 步骤用有序列表；跨步骤强顺序用 `1.` 开头并每步加粗小标题
- 截图统一走 `assets/`，**不要外链**；临时占位用 `<!-- screenshot: 描述 -->` 注释标记，构建期渲染为占位框
- 正文里引用界面路径用「模块 → 页面」格式，如 `系统管理 → 系统配置 → AI 能力`

### 3.4 内容红线

- 不写「待补充」「TBD」——宁可不写这一节
- 涉及 destructive 操作（恢复、DROP、清理）必须带 `danger` 提示块
- 涉及配置项的，写清「改完是否需要重启 / 是否要重发 agent」（这是本项目最高频的疑问）
- 排查类内容一律给**可执行的判断命令或 SQL**，不给空泛建议

---

## nav.json

```json
[
  { "id": "start", "title": "入门", "order": 10 },
  { "id": "guide", "title": "功能指南", "order": 20 }
]
```

新增分组 = 加一个子目录 + 在 `nav.json` 加一条。**组内文档顺序由 front-matter 的 `order` 决定。**

---

## 构建期转 HTML（方案）

流水线（`www` 构建时执行，不进运行时）：

```
docs/manual/**/*.md
   ↓ ① 扫描 + 解析 front-matter（gray-matter）
   ↓ ② Markdown → HTML（markdown-it + 容器插件 + highlight.js）
   ↓ ③ 生成 docs-manifest.json（slug/title/desc/order/anchors/纯文本）
   ↓ ④ 产物随前端一起打包（或写入 backend/web/dist/docs/）
```

关键约束：

- **构建期完成，运行时零解析**——不要在浏览器里引 markdown 解析库
- `highlight.js` 项目已有（`www/package.json`），直接复用
- 每篇生成**锚点列表**（从 `##`/`###` 提取），右侧「本页目录」用它渲染
- 每篇生成**纯文本副本**（去 HTML 标签），供全文搜索与 AI 检索使用
- 构建失败必须让 `npm run build` 失败——文档写坏要能第一时间发现

产出物示例：

```json
{
  "slug": "backup-restore",
  "title": "备份恢复",
  "group": "guide",
  "order": 40,
  "icon": "safety",
  "desc": "备份任务、存储目标、备份集与恢复向导",
  "updated": "2026-10-09",
  "keywords": ["备份", "恢复", "存储目标", "备份集"],
  "anchors": [{ "id": "concepts", "text": "核心概念", "level": 2 }],
  "html": "<h2 id=\"concepts\">…</h2>",
  "text": "备份恢复 核心概念 备份任务 …"
}
```

---

## AI 助手挂载（方案）

文档站接 AI 不是"把整站丢给模型"，而是 **检索增强 + 引用回跳**：

```
用户提问
  ↓ ① 本地检索：用 docs-manifest.json 的 text/keywords 做关键词+向量打分，取 top-5 片段
  ↓ ② 组装 Prompt：系统提示（你是 JaySQL 文档助手）+ 片段（带 slug 与标题）+ 用户问题
  ↓ ③ 调用已接入的大模型（复用系统管理 → AI 能力的现有配置）
  ↓ ④ 渲染答案：正文 + 「参考文档」链接组，点击跳转到文档中心对应锚点
```

要点：

- **未接入大模型时自动降级**为关键词搜索结果列表（不是报错、不是灰按钮）
- 检索在前、模型在后——模型只负责组织语言，事实必须来自文档片段，减少幻觉
- 答案必须带**引用来源**，没有命中的片段时明确回答「文档中未找到，建议…」
- 入口位置：文档页右下角悬浮按钮 → 右侧抽屉；也支持在 AI 助手模块里加「文档问答」模式

---

## 改一篇文档的流程

1. 在对应分组目录改 `.md`
2. 更新 front-matter 的 `updated`
3. 本地跑构建，看渲染效果
4. 提 PR——**文档变更和代码变更一样需要 review**

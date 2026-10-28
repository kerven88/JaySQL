# 文档中心模块 · 实施方案

> 状态：**未实施**。本文是落地计划，代码改动待确认后再执行。
> 内容源已就绪：`docs/manual/**`（12 篇 + 规范），构建与前端部分按本文实施。

---

## 一、目标与已定决策

| 决策项 | 结论 | 理由 |
| --- | --- | --- |
| 正文存放 | `docs/manual/**/*.md`，随代码走 | 可 review、可 diff、版本可追溯 |
| 渲染时机 | **构建期**转 HTML，运行时零解析 | 浏览器不引 markdown 库，首屏快、产物可控 |
| 展示位置 | 主应用内嵌页面（一级菜单） | 风格统一、可鉴权、可挂 AI 助手 |
| AI 助手 | 检索增强式文档问答（RAG-lite） | 复用已有 AI 能力配置，答案带引用回跳 |
| 入口 | 一级菜单【文档中心】 | 与首页、AI 助手等同级 |

---

## 二、任务分解

| # | 任务 | 产物 | 复杂度 |
| --- | --- | --- | --- |
| T1 | 内容源与规范 | `docs/manual/**`（**已完成**） | — |
| T2 | 构建脚本：md → manifest | `www/scripts/build-docs.mjs` | 中 |
| T3 | 接入 vite 构建 | `www/vite.config.js` + npm script | 低 |
| T4 | 一级菜单入口 | `views/docs/route.js`、`router/ark.js` | 低 |
| T5 | 文档页 UI | `views/docs/index.vue` + 子组件 | 高 |
| T6 | AI 文档问答 | 索引 + 接口 + 抽屉组件 | 中 |

---

## 三、T2 构建脚本（md → manifest）

### 3.1 新增文件

```
www/scripts/build-docs.mjs          # 构建脚本，node 直接跑
www/src/views/docs/manifest.json    # 生成物（建议 gitignore，构建时生成）
```

### 3.2 依赖

当前 `www/package.json` **没有** markdown 相关依赖（只有 `highlight.js`）。需要新增：

```bash
cd www && npm i -D markdown-it gray-matter markdown-it-container
```

> 零依赖替代方案：front-matter 用 20 行正则解析、容器的 `:::tip` 用 30 行替换函数。
> 但 markdown-it 更稳（表格、代码高亮、转义都现成），建议直接装。

### 3.3 脚本职责

```text
1. 读 docs/manual/nav.json → 分组与顺序
2. 递归读 docs/manual/**/*.md（跳过 README.md）
3. 解析 front-matter（gray-matter）
4. md → html（markdown-it + container 插件 + highlight.js）
5. 从 html 抽取 h2/h3 生成 anchors（id + 文本 + 层级）
6. 生成纯文本副本 text（去标签，供搜索与 AI 检索）
7. 校验：title/group/order/desc/keywords 必填；缺失直接 throw，让 build 失败
8. 输出 manifest.json
```

### 3.4 产出结构

```json
{
  "generatedAt": "2026-10-09T18:40:00+08:00",
  "groups": [{ "id": "start", "title": "入门", "order": 10 }],
  "docs": [
    {
      "slug": "backup-restore",
      "title": "备份恢复",
      "group": "guide",
      "order": 40,
      "icon": "safety",
      "desc": "备份任务、存储目标、备份集与恢复向导",
      "updated": "2026-10-09",
      "reading": "10 分钟",
      "tags": ["功能", "核心"],
      "keywords": ["备份", "恢复", "误删", "xtrabackup"],
      "anchors": [{ "id": "concepts", "text": "核心概念", "level": 2 }],
      "html": "<h2 id=\"concepts\">…</h2>",
      "text": "备份恢复 核心概念 备份任务 …"
    }
  ]
}
```

### 3.5 容器语法映射

`:::tip 标题` → `<div class="doc-callout tip"><div class="c-title">标题</div>…</div>`

| Markdown | class |
| --- | --- |
| `:::info` | `doc-callout info` |
| `:::tip` | `doc-callout tip` |
| `:::warn` | `doc-callout warn` |
| `:::danger` | `doc-callout danger` |

截图占位：`<!-- screenshot: 描述 -->` → `<div class="doc-shot">描述</div>`

---

## 四、T3 接入构建

`www/vite.config.js` 增加一条 build 前置（或在 npm script 里串起来）：

```json
"scripts": {
  "build:docs": "node scripts/build-docs.mjs",
  "build": "npm run build:docs && vite build"
}
```

> 注意：顺序必须是**先生成 manifest 再 vite build**，否则页面拿到旧内容。
> `npm run build` 是发布链路的唯一入口，串在这里最稳。

---

## 五、T4 一级菜单【文档中心】

### 5.1 新建 `www/src/views/docs/route.js`

```js
import lazyImport from '@/utils/lazyImport'

const route = {
  name: 'docs',
  path: '/docs',
  component: lazyImport(() => import('./index.vue')),
  meta: {
    title: '文档中心',
    icon: 'ReadOutlined',
    keepAlive: true,
    // 文档对全员开放：不设 permission（与首页一致）
  },
}

export default route
```

要点：

- **不设 `permission`** —— 参照 `home/route.js`，无 permission 即全员可见。
  若将来要控权限，用**单个字符串** `docs.view`（数组会导致静默跳 403，这是既有坑）
- `icon` 用已注册的 antd 图标名 `ReadOutlined`
- 单页无 children（文档内部导航由页面自己实现）

### 5.2 修改 `www/src/router/ark.js`

两处：

```js
import DOCS from '@/views/docs/route'      // ① 与其他模块一起 import
...
children: [
  HOME, HOME_TODOS, HOME_RISKS, AI, DAS, ACCOUNT, ORDERS, BACKUP,
  PERFORMANCE, MONITOR, AUDIT, ADMIN,
  DOCS,                                     // ② 加到末尾（放最后，不打断现有顺序）
],
```

### 5.3 位置建议

放在**最后一位**：文档是查阅型入口，不应该挤掉高频操作模块的位置。

---

## 六、T5 文档页 UI

参照 Demo（`jay-docs-module-demo.html`）的呈现，落地为 Vue 组件：

```
views/docs/
├── index.vue                  # 三栏布局 + 路由参数（#/docs/<slug>）
├── components/
│   ├── DocsNav.vue            # 左侧分组导航（含搜索过滤）
│   ├── DocsContent.vue        # 正文（v-html 渲染 manifest.html）
│   ├── DocsToc.vue            # 右侧锚点目录（IntersectionObserver 高亮）
│   ├── DocsSearch.vue         # 顶栏搜索（命中高亮 + 跳转锚点）
│   └── DocsAiPanel.vue        # AI 文档问答抽屉（T6）
└── manifest.json              # 构建生成
```

### 关键实现点

| 项 | 做法 |
| --- | --- |
| 路由 | 用 hash 或 query：`/docs?p=backup-restore`，支持深链与分享 |
| 正文渲染 | `v-html`，样式用 `:deep()` 包裹（**不要用 scoped 直接写**，v-html 内容拿不到 scoped 属性） |
| 代码高亮 | 复用已有 `highlight.js` |
| 锚点跳转 | `scroll-margin-top` + `scrollIntoView`，顶栏吸顶要预留 56px |
| 搜索 | 前端对 `text` 做 `indexOf`，命中处包 `<mark>`；**只在前端**，不引后端 |
| 响应式 | ≤1180px 隐藏右侧 TOC；≤900px 侧栏改抽屉 |
| 空态 | 无权限 / 无内容时给明确文案，不要白屏 |

### 安全

`v-html` 渲染的是**构建期生成的本地 md**，内容可控；但仍需确保 md 源只来自仓库（不接受用户输入）。

---

## 七、T6 AI 文档问答

### 7.1 检索索引

复用 manifest：每篇的 `text` + `keywords` + `title` + `desc`。

打分（简单加权即可，无需向量库）：

```text
score = 标题命中 ×5 + keywords 命中 ×3 + 正文命中 ×1
```

取 top-5 片段（按命中位置截取 ±200 字）。

### 7.2 接口

| 接口 | 方法 | 说明 |
| --- | --- | --- |
| `/api/v1/docs/search` | GET | `?q=` 关键词检索，返回 slug + 标题 + 片段（**不依赖大模型**） |
| `/api/v1/docs/ask` | POST | `{ q }` 检索 + 调大模型作答，返回答案 + 引用列表 |

`ask` 内部复用已有的 AI 调用封装（系统管理 → AI 能力里配置的默认服务商/模型），**不要另建一套大模型接入**。

### 7.3 Prompt 原则

```text
你是 JaySQL 产品文档助手。
只能依据下面提供的文档片段回答，不得编造。
回答用中文，简洁，步骤化。
每段结论后标注来源 [文档标题](slug#anchor)。
若片段中没有答案，直接说「文档中未找到」，并给出建议的排查方向。
```

### 7.4 降级

| 条件 | 行为 |
| --- | --- |
| 未接入任何服务商 | 隐藏 AI 入口，搜索仍可用（不灰按钮，直接不显示） |
| 调用失败 / 超时 | 回落到关键词搜索结果列表，提示「AI 暂不可用，已为你列出相关文档」 |
| 无命中片段 | 返回「文档中未找到」+ 建议，不硬编答案 |

### 7.5 UI

- 入口：文档页右下角悬浮按钮「问文档」→ 右侧抽屉
- 答案区下方固定「参考文档」链接组，点击跳转 `/docs?p=<slug>#<anchor>`
- 答案区顶部标注「由 AI 生成，请以文档原文为准」

---

## 八、验收清单

- [ ] `npm run build` 能生成 `manifest.json`，12 篇全部在内
- [ ] 故意删掉一篇 md 的 `keywords` → **构建失败**（校验生效）
- [ ] 一级菜单出现【文档中心】，图标与位置正确，无权限用户也能进
- [ ] 深链 `/docs?p=backup-restore#concepts` 能直达锚点
- [ ] 搜索「备份」命中 8 篇以上，点击跳转并高亮
- [ ] 右侧 TOC 随滚动高亮；≤1180px 自动隐藏
- [ ] `:::danger` 块渲染为红色提示框；代码块有复制按钮
- [ ] 未配 AI 时不显示 AI 入口；配了之后能问答且引用可跳转
- [ ] eslint 0 error（新增文件），控制台 0 报错

---

## 九、风险与注意

1. **发布链路别漏**：本项目前端是 `//go:embed dist`，改完必须 `npm run build` → 覆盖 `backend/web/dist` → `go build` → 重启，只跑前端构建页面不会变
2. **manifest 体积**：当前 12 篇约 100KB（含 HTML + 纯文本），可接受；超过 500KB 再考虑按篇懒加载
3. **文档与代码不同步**：文档说"某功能在哪"，代码改了菜单名就失真。建议在 CI 加一条检查：文档里出现的菜单名与 `router/ark.js` 的 title 做比对
4. **AI 回答的准确性**：检索在前、模型在后是硬要求，不要让模型凭空作答

---

## 十、落地顺序建议

```text
T2 + T3（构建跑通，能产出 manifest）
   → T4（菜单入口先上去，页面可先放占位）
   → T5（UI 完整化）
   → T6（AI 问答，最后做，依赖 AI 能力已配置）
```

T2~T5 可以一次发版；T6 单独发，避免阻塞文档上线。

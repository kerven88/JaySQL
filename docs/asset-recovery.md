# 缺失资源恢复说明

上游 `simajiong/JaySQL` 的 main 分支缺少 `www/src/assets` 和 `docs/manual/assets`；原忽略规则 `assets/` 会匹配任意层级目录，现已收窄为 `/assets/`。

## 恢复来源

图片、数据库及大模型品牌 Logo 来自 README 指向的官方演示站 <https://www.jaysql.top/> 的公开静态资源，内联 data URI 已解码为原格式文件。逐文件 URL 与 SHA256 见 [asset-recovery-sources.json](asset-recovery-sources.json)。沿用项目许可及相应品牌权利。

全局样式从演示站 `index-CxIvpXD1.css` 中的 `:root{--gi-color-primary:...}` 开始，提取到第三方 `.epic-tree` 样式之前，保存在 `www/src/assets/scss/recovered.css`。未复制 Vue scoped 样式或第三方组件库样式；这些仍由源码和 npm 依赖构建。`index.scss` 保留原入口并引入该 CSS。这是发布产物的恢复，无法还原原始 SCSS 的变量、混入及文件结构；演示站版本可能与当前源码存在视觉差异。

404 页面原来的 `original.png` 未找到，改为复用已恢复的 `original2.png` 插图。文档 `architecture.svg` 未找到原件，根据现有 README 的架构表补绘。

恢复文件均随 Git 提交，Docker 构建不需要访问演示站。将来取得原始 SCSS 后，应以原始源文件替换恢复样式。

## 审计页面源码

`www/src/views/audit/logs/AuditLogIndex.vue` 同样未被上游提交，根目录及 `www/.gitignore` 的宽泛 logs 规则均可能将它排除，Docker 排除规则也已收窄。该页面根据官方演示站 `AuditLogIndex-Dfn5hVnG.js` 的公开行为和本地 `api/audit.js`、服务端 forms/models 重建为可维护的 Vue 源码，保留日期快捷筛选、用户/等级/模块/结果/关键字筛选、分页和审计详情，并防止慢响应覆盖新查询。不是找回的原始 Vue 文件。

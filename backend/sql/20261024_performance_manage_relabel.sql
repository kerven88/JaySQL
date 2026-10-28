-- 2026-10-24 权限点文案调整：performance.manage 语义扩围
--
-- 背景：性能分析模块在「编辑用户 → 权限分配」中新增「性能分析」分组后，
--       performance.manage 的职责从「维护负载快照报告参数」扩展为
--       「管理性能」= 查看性能分析 + 生成负载快照 + 维护快照报告参数。
--       code 不变（performance.manage），仅调整展示文案与描述。
--
-- 种子逻辑（bootstrap/db.go initializeRBACPermissions）只在 code 不存在时插入，
-- 对存量库不会回写新文案，因此这里做幂等 UPDATE。可重复执行。

UPDATE `insight_permissions`
   SET `name`        = '管理性能',
       `description` = '在查看性能分析基础上，额外允许生成负载快照并维护快照报告参数'
 WHERE `code` = 'performance.manage'
   AND (`name` <> '管理性能' OR `description` <> '在查看性能分析基础上，额外允许生成负载快照并维护快照报告参数');

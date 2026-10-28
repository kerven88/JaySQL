-- 备份执行记录：落库真实触发来源 + 备份类型决策说明
--
-- 背景：物理备份任务需要区分两条执行路径
--   MANUAL    手动点击「立即备份」→ 一律全量，不做基线判断
--   SCHEDULED 定时调度触发      → 先探测物理基线，有基线走增量、无基线降级全量
-- 旧实现靠 created_by 是否为 'system' 反推触发方式，不可靠（手动执行但取不到用户名时也会写成 system）。
-- 相关代码：internal/backup/services/backup_trigger.go
--
-- ⚠️ 发布顺序：必须先执行本脚本，再部署新二进制。
--    新版本的执行记录列表会 SELECT 这两列，列不存在会导致整个接口报错。

ALTER TABLE `insight_backup_runs`
  ADD COLUMN `trigger_source` varchar(16) NOT NULL DEFAULT '' COMMENT '触发来源(MANUAL=手动立即备份,SCHEDULED=定时调度)' AFTER `created_by`,
  ADD COLUMN `backup_type_reason` varchar(1024) NOT NULL DEFAULT '' COMMENT '备份类型决策说明（全量/增量判定依据）' AFTER `trigger_source`,
  ADD INDEX `idx_run_trigger_source` (`trigger_source`);

-- 历史记录回填：旧逻辑没有真实来源，按 created_by 近似归类
UPDATE `insight_backup_runs` SET `trigger_source` = 'SCHEDULED' WHERE `trigger_source` = '' AND `created_by` = 'system';
UPDATE `insight_backup_runs` SET `trigger_source` = 'MANUAL'    WHERE `trigger_source` = '' AND `created_by` <> 'system';

-- backup_type_reason 不回填：旧记录没有基线判定链路，留空比编造文案可靠。
-- 历史记录的触发方式仍由代码回落到 created_by 推断，回填后即自动生效。

-- ============================================================
-- 2026-09-29 工单规范预审快照增加「规则版本」列
-- ============================================================
-- 背景：规范预审此前没有版本标记，无法判断一份快照是按哪一版规则算出来的。
--       撤回工单后重新提交时，若 SQL 内容未变化，页面会直接复用撤回前的旧结论，
--       导致「规则已经关闭，预审仍按旧规则报错」。
-- 变更：insight_order_pre_inspections 增加 rule_version 列，记录本次预审所用的
--       规则版本指纹；规则变动（启用/停用/改级别/改参数）会让版本变化，
--       快照版本与当前版本不一致即按最新规则全量重跑。
-- 说明：服务启动的 AutoMigrate 会自动补列（模型已加入迁移列表），
--       本脚本供需要先行手动执行的存量环境使用，重复执行无副作用。
-- 回滚：ALTER TABLE insight_order_pre_inspections DROP COLUMN rule_version;
-- ============================================================

SET @cnt = (
  SELECT COUNT(*) FROM information_schema.columns
  WHERE table_schema = DATABASE()
    AND table_name = 'insight_order_pre_inspections'
    AND column_name = 'rule_version'
);
SET @sql = IF(@cnt = 0,
  "ALTER TABLE insight_order_pre_inspections ADD COLUMN rule_version varchar(32) NOT NULL DEFAULT '' COMMENT '预审所用规则版本指纹' AFTER result",
  'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

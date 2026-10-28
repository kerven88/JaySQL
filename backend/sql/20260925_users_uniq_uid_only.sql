-- ============================================================
-- 2026-09-25 用户唯一性改为仅依据 uid
-- ============================================================
-- 背景：用户唯一性原先由 username/email/mobile 三个唯一索引保证，
--       但软删除（is_deleted=1）的用户仍占用这三个唯一索引——
--       用户列表不显示他们，新建同名用户却在 INSERT 时撞 1062，
--       提示"用户名`xxx`已存在"，且无法自愈。
-- 变更：用户唯一性只由 uid（自增主键）决定，删除三个唯一索引；
--       应用层（internal/users/services/users.go）同步移除字段查重逻辑。
-- 说明：AutoMigrate 不会删除存量库中已存在的索引，因此本脚本需对
--       每个存量环境执行一次；新环境不再创建这些索引。
-- 回滚：如需恢复唯一约束，须先清理/改名软删除用户的重复值再加回索引。
-- ============================================================

SET @cnt = (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'insight_users' AND index_name = 'uniq_username');
SET @sql = IF(@cnt > 0, 'DROP INDEX `uniq_username` ON `insight_users`', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @cnt = (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'insight_users' AND index_name = 'uniq_email');
SET @sql = IF(@cnt > 0, 'DROP INDEX `uniq_email` ON `insight_users`', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @cnt = (SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = 'insight_users' AND index_name = 'uniq_mobile');
SET @sql = IF(@cnt > 0, 'DROP INDEX `uniq_mobile` ON `insight_users`', 'SELECT 1');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

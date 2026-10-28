-- ============================================================================
-- A 类回滚：去掉密文前缀（与 20261014_secret_ciphertext_prefix.sql 配套）
--
-- 什么时候需要它：
--   迁移后线上跑的仍是**旧版二进制**时，旧版 utils.Decrypt 认不出
--   "enc:v1:aesgcm:" 前缀，所有依赖实例密码的功能会报"密码解密失败"
--   （监控采集、SQL 查询、工单执行、备份）。
--   这时执行本文件即可让旧版恢复工作 —— 纯字符串操作，秒级生效，
--   不需要回退二进制。
--
-- 幂等：带 LIKE 'enc:v1:aesgcm:%' 条件，重复执行无副作用。
--
-- ⚠️ 完整回滚顺序（B 类在前，A 类在后）：
--   1) go run ./cmd/secret_migrate -dsn "..." -rollback -batch-tag <批次号>
--   2) mysql ... < 本文件
--   反过来做也能恢复，但按这个顺序能保证每一步之后数据都自洽。
--
-- 用法：mysql --default-character-set=utf8mb4 -u<user> -p<pass> <db> < 本文件
-- ============================================================================

UPDATE insight_instances
   SET password = REPLACE(password, 'enc:v1:aesgcm:', '')
 WHERE password LIKE 'enc:v1:aesgcm:%';

UPDATE insight_notify_settings
   SET mail_password = REPLACE(mail_password, 'enc:v1:aesgcm:', '')
 WHERE mail_password LIKE 'enc:v1:aesgcm:%';

UPDATE insight_llm_providers
   SET api_key_enc = REPLACE(api_key_enc, 'enc:v1:aesgcm:', '')
 WHERE api_key_enc LIKE 'enc:v1:aesgcm:%';

UPDATE insight_backup_storage_targets
   SET ssh_password    = REPLACE(ssh_password, 'enc:v1:aesgcm:', ''),
       ssh_private_key = REPLACE(ssh_private_key, 'enc:v1:aesgcm:', ''),
       s3_access_key   = REPLACE(s3_access_key, 'enc:v1:aesgcm:', ''),
       s3_secret_key   = REPLACE(s3_secret_key, 'enc:v1:aesgcm:', '');

UPDATE insight_backup_nodes
   SET ssh_password    = REPLACE(ssh_password, 'enc:v1:aesgcm:', ''),
       ssh_private_key = REPLACE(ssh_private_key, 'enc:v1:aesgcm:', '');

-- ---------------------------------------------------------------------------
-- 回滚后断言：三列合计必须为 0
-- ---------------------------------------------------------------------------
SELECT 'insight_instances.password'             AS 列,
       SUM(password LIKE 'enc:v1:aesgcm:%')     AS 仍带前缀_应为0
FROM insight_instances;

SELECT 'insight_notify_settings.mail_password'   AS 列,
       SUM(mail_password LIKE 'enc:v1:aesgcm:%') AS 仍带前缀_应为0
FROM insight_notify_settings;

SELECT 'insight_llm_providers.api_key_enc'      AS 列,
       SUM(api_key_enc LIKE 'enc:v1:aesgcm:%')  AS 仍带前缀_应为0
FROM insight_llm_providers;

SELECT 'insight_backup_storage_targets (4 列)'  AS 列,
       SUM(ssh_password LIKE 'enc:v1:aesgcm:%')
       + SUM(ssh_private_key LIKE 'enc:v1:aesgcm:%')
       + SUM(s3_access_key LIKE 'enc:v1:aesgcm:%')
       + SUM(s3_secret_key LIKE 'enc:v1:aesgcm:%') AS 仍带前缀_应为0
FROM insight_backup_storage_targets;

SELECT 'insight_backup_nodes (2 列)'            AS 列,
       SUM(ssh_password LIKE 'enc:v1:aesgcm:%')
       + SUM(ssh_private_key LIKE 'enc:v1:aesgcm:%') AS 仍带前缀_应为0
FROM insight_backup_nodes;

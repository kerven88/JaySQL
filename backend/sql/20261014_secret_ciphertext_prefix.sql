-- ============================================================================
-- 凭据密文补前缀（A 类迁移）
--
-- 背景：pkg/secret 把密文格式统一为带版本前缀的形态
--         enc:v1:aesgcm:<base64(nonce 12B | ciphertext | tag 16B)>
--       而历史数据由 utils.Encrypt 产出，是**不带前缀的纯 base64**。
--       新代码靠前缀判断"这是不是密文"（IsEncrypted），不补前缀的话存量密文
--       会被当成明文原样返回 → 全站解密失败。
--
-- 为什么可以这么简单：存量密文本身就是完整的 base64(nonce+cipher+tag)，
--       只是少了前缀。补前缀是纯字符串拼接 —— 不需要密钥参与、不需要重新加密、
--       不依赖应用代码，风险极低。
--
-- 幂等：带 NOT LIKE 'enc:%' 条件，重复执行不会二次拼接。
-- 可逆：见文末回滚段，用 REPLACE 去掉前缀即可。
--
-- ⚠️⚠️ 执行顺序（血泪教训，顺序错了会直接打挂生产）：
--
--   0) 全量备份元库
--   1) **先部署含 pkg/secret 的新版本二进制**（必须！见下方说明）
--   2) 再执行本脚本（A 类，补前缀）
--   3) 再用 cmd/secret_migrate 处理 B 类（历史明文 → 密文）
--
--   为什么必须先部署后迁移 —— 四个组合的兼容性：
--
--   库里的值形态            新版 secret.Decrypt     旧版 utils.Decrypt
--   ─────────────────────────────────────────────────────────────
--   明文                    ✅ 原样返回              ✅ 原样返回
--   无前缀旧密文            ✅ 有回退，能解开        ✅ 能解开
--   带前缀密文（迁移后）    ✅ 能解开                ❌ 报"密码解密失败"
--   ─────────────────────────────────────────────────────────────
--
--   结论：**新版能读三种形态，旧版只能读前两种**。
--   所以「先迁移后部署」会让旧二进制在迁移完成的那一刻起就连不上所有数据库 ——
--   2026-10-14 在生产上真踩过：4 个实例全部"实例密码解密失败"，监控采集、
--   SQL 查询、工单执行全线中断，靠本文件末尾的回滚段才恢复。
--
--   同理，回滚顺序是反过来的：先回滚 B 类 → 再回滚 A 类 → 服务即恢复，
--   不需要回退二进制。
--
-- 用法：mysql --default-character-set=utf8mb4 -u<user> -p<pass> <db> < 本文件
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 第 0 步：迁移前形态统计（只看形态，不回显任何内容）
--  待补前缀 = 非空、无 enc: 前缀、且"看起来像密文"（长度 ≥ 40 且是合法 base64）
--  疑似明文 = 非空、无 enc: 前缀、但长度/字符集不像密文 —— 交给 B 类工具处理
-- ---------------------------------------------------------------------------
SELECT 'insight_instances.password' AS 列,
       SUM(password = '')                                                              AS 空值,
       SUM(password <> '' AND password NOT LIKE 'enc:%'
           AND CHAR_LENGTH(password) >= 40
           AND password REGEXP '^[A-Za-z0-9+/]+={0,2}$')                               AS 待补前缀,
       SUM(password <> '' AND password NOT LIKE 'enc:%'
           AND NOT (CHAR_LENGTH(password) >= 40
                    AND password REGEXP '^[A-Za-z0-9+/]+={0,2}$'))                     AS 疑似明文,
       SUM(password LIKE 'enc:%')                                                      AS 已带前缀
FROM insight_instances;

SELECT 'insight_notify_settings.mail_password' AS 列,
       SUM(mail_password = '')                                                          AS 空值,
       SUM(mail_password <> '' AND mail_password NOT LIKE 'enc:%'
           AND CHAR_LENGTH(mail_password) >= 40
           AND mail_password REGEXP '^[A-Za-z0-9+/]+={0,2}$')                           AS 待补前缀,
       SUM(mail_password <> '' AND mail_password NOT LIKE 'enc:%'
           AND NOT (CHAR_LENGTH(mail_password) >= 40
                    AND mail_password REGEXP '^[A-Za-z0-9+/]+={0,2}$'))                 AS 疑似明文,
       SUM(mail_password LIKE 'enc:%')                                                  AS 已带前缀
FROM insight_notify_settings;

SELECT 'insight_llm_providers.api_key_enc' AS 列,
       SUM(api_key_enc = '')                                                            AS 空值,
       SUM(api_key_enc <> '' AND api_key_enc NOT LIKE 'enc:%'
           AND CHAR_LENGTH(api_key_enc) >= 40
           AND api_key_enc REGEXP '^[A-Za-z0-9+/]+={0,2}$')                             AS 待补前缀,
       SUM(api_key_enc <> '' AND api_key_enc NOT LIKE 'enc:%'
           AND NOT (CHAR_LENGTH(api_key_enc) >= 40
                    AND api_key_enc REGEXP '^[A-Za-z0-9+/]+={0,2}$'))                   AS 疑似明文,
       SUM(api_key_enc LIKE 'enc:%')                                                    AS 已带前缀
FROM insight_llm_providers;

SELECT 'insight_backup_storage_targets.ssh_password' AS 列,
       SUM(ssh_password = '')                                                           AS 空值,
       SUM(ssh_password <> '' AND ssh_password NOT LIKE 'enc:%'
           AND CHAR_LENGTH(ssh_password) >= 40
           AND ssh_password REGEXP '^[A-Za-z0-9+/]+={0,2}$')                            AS 待补前缀,
       SUM(ssh_password <> '' AND ssh_password NOT LIKE 'enc:%'
           AND NOT (CHAR_LENGTH(ssh_password) >= 40
                    AND ssh_password REGEXP '^[A-Za-z0-9+/]+={0,2}$'))                  AS 疑似明文,
       SUM(ssh_password LIKE 'enc:%')                                                   AS 已带前缀
FROM insight_backup_storage_targets;

SELECT 'insight_backup_storage_targets.ssh_private_key' AS 列,
       SUM(ssh_private_key = '')                                                        AS 空值,
       SUM(ssh_private_key <> '' AND ssh_private_key NOT LIKE 'enc:%'
           AND CHAR_LENGTH(ssh_private_key) >= 40
           AND ssh_private_key REGEXP '^[A-Za-z0-9+/]+={0,2}$')                         AS 待补前缀,
       SUM(ssh_private_key <> '' AND ssh_private_key NOT LIKE 'enc:%'
           AND NOT (CHAR_LENGTH(ssh_private_key) >= 40
                    AND ssh_private_key REGEXP '^[A-Za-z0-9+/]+={0,2}$'))                AS 疑似明文,
       SUM(ssh_private_key LIKE 'enc:%')                                                AS 已带前缀
FROM insight_backup_storage_targets;

SELECT 'insight_backup_storage_targets.s3_access_key' AS 列,
       SUM(s3_access_key = '')                                                          AS 空值,
       SUM(s3_access_key <> '' AND s3_access_key NOT LIKE 'enc:%'
           AND CHAR_LENGTH(s3_access_key) >= 40
           AND s3_access_key REGEXP '^[A-Za-z0-9+/]+={0,2}$')                           AS 待补前缀,
       SUM(s3_access_key <> '' AND s3_access_key NOT LIKE 'enc:%'
           AND NOT (CHAR_LENGTH(s3_access_key) >= 40
                    AND s3_access_key REGEXP '^[A-Za-z0-9+/]+={0,2}$'))                 AS 疑似明文,
       SUM(s3_access_key LIKE 'enc:%')                                                  AS 已带前缀
FROM insight_backup_storage_targets;

SELECT 'insight_backup_storage_targets.s3_secret_key' AS 列,
       SUM(s3_secret_key = '')                                                          AS 空值,
       SUM(s3_secret_key <> '' AND s3_secret_key NOT LIKE 'enc:%'
           AND CHAR_LENGTH(s3_secret_key) >= 40
           AND s3_secret_key REGEXP '^[A-Za-z0-9+/]+={0,2}$')                           AS 待补前缀,
       SUM(s3_secret_key <> '' AND s3_secret_key NOT LIKE 'enc:%'
           AND NOT (CHAR_LENGTH(s3_secret_key) >= 40
                    AND s3_secret_key REGEXP '^[A-Za-z0-9+/]+={0,2}$'))                 AS 疑似明文,
       SUM(s3_secret_key LIKE 'enc:%')                                                  AS 已带前缀
FROM insight_backup_storage_targets;

SELECT 'insight_backup_nodes.ssh_password' AS 列,
       SUM(ssh_password = '')                                                           AS 空值,
       SUM(ssh_password <> '' AND ssh_password NOT LIKE 'enc:%'
           AND CHAR_LENGTH(ssh_password) >= 40
           AND ssh_password REGEXP '^[A-Za-z0-9+/]+={0,2}$')                            AS 待补前缀,
       SUM(ssh_password <> '' AND ssh_password NOT LIKE 'enc:%'
           AND NOT (CHAR_LENGTH(ssh_password) >= 40
                    AND ssh_password REGEXP '^[A-Za-z0-9+/]+={0,2}$'))                  AS 疑似明文,
       SUM(ssh_password LIKE 'enc:%')                                                   AS 已带前缀
FROM insight_backup_nodes;

SELECT 'insight_backup_nodes.ssh_private_key' AS 列,
       SUM(ssh_private_key = '')                                                        AS 空值,
       SUM(ssh_private_key <> '' AND ssh_private_key NOT LIKE 'enc:%'
           AND CHAR_LENGTH(ssh_private_key) >= 40
           AND ssh_private_key REGEXP '^[A-Za-z0-9+/]+={0,2}$')                         AS 待补前缀,
       SUM(ssh_private_key <> '' AND ssh_private_key NOT LIKE 'enc:%'
           AND NOT (CHAR_LENGTH(ssh_private_key) >= 40
                    AND ssh_private_key REGEXP '^[A-Za-z0-9+/]+={0,2}$'))               AS 疑似明文,
       SUM(ssh_private_key LIKE 'enc:%')                                                AS 已带前缀
FROM insight_backup_nodes;

-- ---------------------------------------------------------------------------
-- 第 1 步：补前缀（幂等，可重复执行）
--
-- 只处理「非空 + 无 enc: 前缀 + 形态像密文」的行：
--   形态不像密文的行是历史明文，补了前缀反而会让它被误判为密文、解密必失败，
--   留给 cmd/secret_migrate 走真正的加密（B 类）。
-- ---------------------------------------------------------------------------
UPDATE insight_instances
   SET password = CONCAT('enc:v1:aesgcm:', password)
 WHERE password <> ''
   AND password NOT LIKE 'enc:%'
   AND CHAR_LENGTH(password) >= 40
   AND password REGEXP '^[A-Za-z0-9+/]+={0,2}$';

UPDATE insight_notify_settings
   SET mail_password = CONCAT('enc:v1:aesgcm:', mail_password)
 WHERE mail_password <> ''
   AND mail_password NOT LIKE 'enc:%'
   AND CHAR_LENGTH(mail_password) >= 40
   AND mail_password REGEXP '^[A-Za-z0-9+/]+={0,2}$';

UPDATE insight_llm_providers
   SET api_key_enc = CONCAT('enc:v1:aesgcm:', api_key_enc)
 WHERE api_key_enc <> ''
   AND api_key_enc NOT LIKE 'enc:%'
   AND CHAR_LENGTH(api_key_enc) >= 40
   AND api_key_enc REGEXP '^[A-Za-z0-9+/]+={0,2}$';

UPDATE insight_backup_storage_targets
   SET ssh_password = CONCAT('enc:v1:aesgcm:', ssh_password)
 WHERE ssh_password <> ''
   AND ssh_password NOT LIKE 'enc:%'
   AND CHAR_LENGTH(ssh_password) >= 40
   AND ssh_password REGEXP '^[A-Za-z0-9+/]+={0,2}$';

UPDATE insight_backup_storage_targets
   SET ssh_private_key = CONCAT('enc:v1:aesgcm:', ssh_private_key)
 WHERE ssh_private_key <> ''
   AND ssh_private_key NOT LIKE 'enc:%'
   AND CHAR_LENGTH(ssh_private_key) >= 40
   AND ssh_private_key REGEXP '^[A-Za-z0-9+/]+={0,2}$';

UPDATE insight_backup_storage_targets
   SET s3_access_key = CONCAT('enc:v1:aesgcm:', s3_access_key)
 WHERE s3_access_key <> ''
   AND s3_access_key NOT LIKE 'enc:%'
   AND CHAR_LENGTH(s3_access_key) >= 40
   AND s3_access_key REGEXP '^[A-Za-z0-9+/]+={0,2}$';

UPDATE insight_backup_storage_targets
   SET s3_secret_key = CONCAT('enc:v1:aesgcm:', s3_secret_key)
 WHERE s3_secret_key <> ''
   AND s3_secret_key NOT LIKE 'enc:%'
   AND CHAR_LENGTH(s3_secret_key) >= 40
   AND s3_secret_key REGEXP '^[A-Za-z0-9+/]+={0,2}$';

UPDATE insight_backup_nodes
   SET ssh_password = CONCAT('enc:v1:aesgcm:', ssh_password)
 WHERE ssh_password <> ''
   AND ssh_password NOT LIKE 'enc:%'
   AND CHAR_LENGTH(ssh_password) >= 40
   AND ssh_password REGEXP '^[A-Za-z0-9+/]+={0,2}$';

UPDATE insight_backup_nodes
   SET ssh_private_key = CONCAT('enc:v1:aesgcm:', ssh_private_key)
 WHERE ssh_private_key <> ''
   AND ssh_private_key NOT LIKE 'enc:%'
   AND CHAR_LENGTH(ssh_private_key) >= 40
   AND ssh_private_key REGEXP '^[A-Za-z0-9+/]+={0,2}$';

-- ---------------------------------------------------------------------------
-- 第 2 步：迁移后断言（「待补前缀」必须全部为 0）
-- ---------------------------------------------------------------------------
SELECT 'insight_instances.password' AS 列,
       SUM(password <> '' AND password NOT LIKE 'enc:%'
           AND CHAR_LENGTH(password) >= 40
           AND password REGEXP '^[A-Za-z0-9+/]+={0,2}$')                               AS 待补前缀_应为0
FROM insight_instances;

SELECT 'insight_notify_settings.mail_password' AS 列,
       SUM(mail_password <> '' AND mail_password NOT LIKE 'enc:%'
           AND CHAR_LENGTH(mail_password) >= 40
           AND mail_password REGEXP '^[A-Za-z0-9+/]+={0,2}$')                           AS 待补前缀_应为0
FROM insight_notify_settings;

SELECT 'insight_llm_providers.api_key_enc' AS 列,
       SUM(api_key_enc <> '' AND api_key_enc NOT LIKE 'enc:%'
           AND CHAR_LENGTH(api_key_enc) >= 40
           AND api_key_enc REGEXP '^[A-Za-z0-9+/]+={0,2}$')                             AS 待补前缀_应为0
FROM insight_llm_providers;

SELECT 'insight_backup_storage_targets (4 列合计)' AS 列,
       SUM(ssh_password <> '' AND ssh_password NOT LIKE 'enc:%'
           AND CHAR_LENGTH(ssh_password) >= 40 AND ssh_password REGEXP '^[A-Za-z0-9+/]+={0,2}$')
       + SUM(ssh_private_key <> '' AND ssh_private_key NOT LIKE 'enc:%'
           AND CHAR_LENGTH(ssh_private_key) >= 40 AND ssh_private_key REGEXP '^[A-Za-z0-9+/]+={0,2}$')
       + SUM(s3_access_key <> '' AND s3_access_key NOT LIKE 'enc:%'
           AND CHAR_LENGTH(s3_access_key) >= 40 AND s3_access_key REGEXP '^[A-Za-z0-9+/]+={0,2}$')
       + SUM(s3_secret_key <> '' AND s3_secret_key NOT LIKE 'enc:%'
           AND CHAR_LENGTH(s3_secret_key) >= 40 AND s3_secret_key REGEXP '^[A-Za-z0-9+/]+={0,2}$') AS 待补前缀_应为0
FROM insight_backup_storage_targets;

SELECT 'insight_backup_nodes (2 列合计)' AS 列,
       SUM(ssh_password <> '' AND ssh_password NOT LIKE 'enc:%'
           AND CHAR_LENGTH(ssh_password) >= 40 AND ssh_password REGEXP '^[A-Za-z0-9+/]+={0,2}$')
       + SUM(ssh_private_key <> '' AND ssh_private_key NOT LIKE 'enc:%'
           AND CHAR_LENGTH(ssh_private_key) >= 40 AND ssh_private_key REGEXP '^[A-Za-z0-9+/]+={0,2}$') AS 待补前缀_应为0
FROM insight_backup_nodes;

-- ---------------------------------------------------------------------------
-- 回滚（仅在需要回退到旧版本二进制时执行；纯字符串操作，可逆）
-- ---------------------------------------------------------------------------
-- UPDATE insight_instances
--    SET password = REPLACE(password, 'enc:v1:aesgcm:', '')
--  WHERE password LIKE 'enc:v1:aesgcm:%';
--
-- UPDATE insight_notify_settings
--    SET mail_password = REPLACE(mail_password, 'enc:v1:aesgcm:', '')
--  WHERE mail_password LIKE 'enc:v1:aesgcm:%';
--
-- UPDATE insight_llm_providers
--    SET api_key_enc = REPLACE(api_key_enc, 'enc:v1:aesgcm:', '')
--  WHERE api_key_enc LIKE 'enc:v1:aesgcm:%';
--
-- UPDATE insight_backup_storage_targets
--    SET ssh_password     = REPLACE(ssh_password, 'enc:v1:aesgcm:', ''),
--        ssh_private_key  = REPLACE(ssh_private_key, 'enc:v1:aesgcm:', ''),
--        s3_access_key    = REPLACE(s3_access_key, 'enc:v1:aesgcm:', ''),
--        s3_secret_key    = REPLACE(s3_secret_key, 'enc:v1:aesgcm:', '')
--  WHERE ssh_password LIKE 'enc:v1:aesgcm:%'
--     OR ssh_private_key LIKE 'enc:v1:aesgcm:%'
--     OR s3_access_key LIKE 'enc:v1:aesgcm:%'
--     OR s3_secret_key LIKE 'enc:v1:aesgcm:%';
--
-- UPDATE insight_backup_nodes
--    SET ssh_password    = REPLACE(ssh_password, 'enc:v1:aesgcm:', ''),
--        ssh_private_key = REPLACE(ssh_private_key, 'enc:v1:aesgcm:', '')
--  WHERE ssh_password LIKE 'enc:v1:aesgcm:%'
--     OR ssh_private_key LIKE 'enc:v1:aesgcm:%';

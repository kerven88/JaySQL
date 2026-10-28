-- 移除内置厂商模板「Azure OpenAI」（vendor = azure_openai）
--
-- 背景：种子清单（internal/ai/seeds/vendor_templates.go）已删除该定义，
-- 但播种逻辑按「定义存在才播种」，不会主动删除库里已有行，故需本脚本清存量。
-- 幂等：重复执行无副作用。适配器 azure_openai 后端仍保留，既有 provider 不受影响。
-- 执行时机：随发版执行一次即可。

DELETE FROM insight_llm_vendor_templates WHERE vendor = 'azure_openai';

# Telegram and Markdown alert templates implementation plan

Goal: Add a Telegram bot channel and editable per-channel Markdown alert messages with variables and sample preview.
Architecture: Keep templates in existing channel config JSON (no schema migration). Validate and render a restricted variable syntax on the backend. Render into task payload before enqueue for repeatable retries. Use CommonMark parsing for Telegram HTML and plaintext fallback; never pass raw HTML through. Existing empty templates preserve defaults.
Tech stack: Go notifier registry, Goldmark parser, Gin endpoint, Vue/Ant Design channel editor, existing MarkdownIt preview.

- [x] Add failing notifier tests for Telegram registration, credentials, payload/error handling and Markdown conversion; template tests for variable validation, escaping and no-op defaults.
- [x] Implement Telegram adapter and shared Markdown conversion, template compilation and metadata.
- [x] Integrate template validation, enqueue snapshot, test-send and backend sample preview with actual alert variables.
- [x] Add Telegram UI option and channel template editor with insertable variables, preview and reset/default sample.
- [x] Verify notifier/service tests, frontend build and focused UI interactions; document setup and formatting limits.

Constraints: Preserve unrelated README changes. No real Telegram sends during verification. Token encrypted/masked by existing channel secret handling. Unknown variables rejected, raw HTML disabled, errors never expose bot token. Preview is read-only and requires monitor.manage. Telegram messages exceeding 4096 UTF-16 units fail explicitly rather than truncate.

Validation: notifier, monitor services, preview views and routers tests passed; production frontend build passed; Go 1.23.12 Linux amd64 build passed. Mocked-browser tests covered Telegram fields, variable insertion, preview, save/reopen, masked token and reset at desktop/mobile sizes. Independent review findings (code-context interpolation and visible link length counting) were fixed with regression tests. No real Telegram requests were made. Aggregated summaries intentionally retain system content.

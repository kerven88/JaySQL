# 工单模块 DDL 支持范围说明

## 文档目的

本文档用于说明当前工单模块在 `MySQL`、`PostgreSQL` 两类数据库上的 DDL 支持范围。这里的“支持”分为三层：

1. 创建层：系统是否会把 SQL 识别为 `DDL` 工单并允许创建
2. 审核层：规范预审/审核器是否对该语句有专门规则
3. 执行层：工单执行时是否真的可以执行

这三层并不完全一致，因此判断“是否支持”时必须分层看。

## 总体限制

- 当前工单仅支持 `MySQL`、`PostgreSQL` 两类数据库
- 当前工单仅支持 `DDL`、`DML`
- 当前版本不支持 `EXPORT` 工单

对应代码：

- `backend/internal/orders/services/db_support.go`
- `backend/internal/orders/services/commit.go`

## 一、创建层：哪些语句会被识别为 DDL

创建工单时，系统通过 `inferOrderSQLType()` 判断 SQL 类型。

### 1. 按前缀直接识别为 DDL 的语句

以下前缀会直接被归类为 `DDL`：

- `create ...`
- `alter ...`
- `drop ...`
- `truncate ...`
- `rename ...`
- `comment ...`
- `grant ...`
- `revoke ...`

对应代码：

- `backend/internal/orders/services/commit.go`

### 2. 按 AST 识别为 DDL 的语句类型

解析 SQL AST 后，以下语句类型会被归类为 `DDL`：

- `AlterTable`
- `AlterSequence`
- `AlterPlacementPolicy`
- `CreateDatabase`
- `CreateIndex`
- `CreateTable`
- `CreateView`
- `CreateSequence`
- `CreatePlacementPolicy`
- `DropDatabase`
- `DropIndex`
- `DropTable`
- `DropSequence`
- `DropPlacementPolicy`
- `RenameTable`
- `TruncateTable`

### 3. 这一层的含义

这一层只表示：

- 系统会把这些 SQL 当作 `DDL` 工单接收

但不表示：

- 审核器一定支持
- 执行器一定支持

## 二、审核层：哪些 DDL 语句有专门审核规则

规范预审入口在：

- `backend/internal/inspect/checker/check.go`

当前审核器明确做了专门规则处理的 DDL 语句有：

- `CREATE TABLE`
- `CREATE VIEW`
- `ALTER TABLE`
- `DROP TABLE`
- `TRUNCATE TABLE`
- `RENAME TABLE`
- `ANALYZE TABLE`
- `CREATE DATABASE`

### 1. `CREATE TABLE` 的审核最完整

`CREATE TABLE` 当前有最完整的审核规则，包含但不限于：

- 检查表是否存在
- 检查 `CREATE TABLE AS`
- 检查 `CREATE TABLE LIKE`
- 检查表级 `Options`
- 检查主键
- 检查约束
- 检查审计字段
- 检查列级 `Options`
- 检查重复列定义
- 检查列字符集
- 检查索引前缀
- 检查索引数量
- 检查索引重复定义
- 检查冗余索引
- 检查 `BLOB/TEXT` 禁用索引
- 检查 `InnoDB large prefix`
- 检查 `InnoDB row size`
- 检查 `InnoDB row format`

对应代码：

- `backend/internal/inspect/controllers/rules/create.go`

### 2. `CREATE VIEW` 和 `CREATE DATABASE`

当前规则相对简单：

- `CREATE VIEW`：检查视图是否已存在
- `CREATE DATABASE`：检查数据库是否已存在

对应代码：

- `backend/internal/inspect/controllers/rules/view.go`
- `backend/internal/inspect/controllers/rules/database.go`

### 3. 审核层未专门支持的 DDL

以下语句即使在创建层能被识别成 `DDL`，审核层也没有专门规则，会落到：

- “未识别或禁止的审核语句，请联系数据库管理员”

典型包括：

- `CREATE INDEX`
- `DROP INDEX`
- `DROP DATABASE`
- `CREATE FUNCTION`
- `CREATE TRIGGER`
- `CREATE PROCEDURE`
- `CREATE USER`
- `GRANT`
- `REVOKE`
- `COMMENT ON ...`

## 三、MySQL：执行层实际支持哪些 DDL

MySQL 的 DDL 执行器在：

- `backend/internal/orders/api/mysql/ddl.go`

MySQL 不是把所有 DDL 原样执行，而是采用明确分支处理，因此它的执行层支持范围是最严格、最清晰的。

### 1. 明确支持执行的 MySQL DDL

当前明确支持：

- `CREATE DATABASE`
- `CREATE TABLE`
- `CREATE VIEW`
- `DROP TABLE`
- `DROP INDEX`
- `TRUNCATE TABLE`
- `ALTER TABLE`

其中对应分支如下：

- `CreateDatabase`、`CreateTable`、`CreateView` -> 允许执行
- `DropTable`、`DropIndex` -> 允许执行
- `TruncateTable` -> 允许执行
- `AlterTable` -> 走 `gh-ost`

### 2. MySQL 的 `ALTER TABLE` 不是全支持

`ALTER TABLE` 当前不是直接执行，而是先做正则匹配，只支持以下子类型：

- `ADD`
- `CHANGE`
- `RENAME`
- `MODIFY`
- `DROP`
- `ENGINE`
- `CONVERT`

也就是说，当前真正支持的 `ALTER TABLE` 语法更准确是：

- `ALTER TABLE ... ADD ...`
- `ALTER TABLE ... CHANGE ...`
- `ALTER TABLE ... RENAME ...`
- `ALTER TABLE ... MODIFY ...`
- `ALTER TABLE ... DROP ...`
- `ALTER TABLE ... ENGINE=...`
- `ALTER TABLE ... CONVERT ...`

对应代码：

- `backend/internal/orders/api/mysql/ddl.go`

### 3. 明确不支持或被禁止的 MySQL DDL

当前明确不支持或禁止：

- `RENAME TABLE ...`
- `CREATE INDEX ...`
- `DROP DATABASE ...`
- 未命中正则规则的其他 `ALTER TABLE` 子语法

系统行为如下：

- `RENAME TABLE`：提示“请更正为 alter table ... rename 语法”
- `CREATE INDEX`：提示“请更正为 alter table ... add 语法”
- `DROP DATABASE`：直接拒绝并提示高风险禁止执行
- 其他未匹配类型：报“当前SQL未匹配到规则，执行失败”

### 4. 已知不支持示例

例如以下语句当前在 MySQL 执行层不支持：

```sql
ALTER TABLE t_01 COMMENT '备注说明';
```

原因是 `COMMENT` 不在当前 `ALTER TABLE` 正则支持的子语法范围内。

## 四、PostgreSQL：执行层实际支持哪些 DDL

PostgreSQL 执行器在：

- `backend/internal/orders/api/execute/extended.go`

和 MySQL 不同，PostgreSQL 当前没有单独的 DDL 白名单分支，而是：

1. 拆分 `database.schema`
2. 建立连接
3. `set search_path`
4. 直接执行 `conn.Exec(ctx, m.SQL)`

### 1. PostgreSQL 执行层的特征

因此 PostgreSQL 执行层本质上是“通用执行”：

- 不像 MySQL 那样单独列出 `CreateTable`、`AlterTable`、`DropTable`
- 只要语句能被工单创建层接受
- SQL 本身符合 PostgreSQL 语法
- 当前账号有权限
- 资源路径 `database.schema` 合法

那么执行器原则上就可以执行

### 2. PostgreSQL 执行层理论上可执行的 DDL

例如原则上包括但不限于：

- `CREATE TABLE`
- `ALTER TABLE`
- `DROP TABLE`
- `TRUNCATE TABLE`
- `CREATE VIEW`
- `CREATE SCHEMA`
- `DROP SCHEMA`
- `CREATE INDEX`
- `DROP INDEX`
- `COMMENT ON ...`
- `ALTER INDEX`
- `ALTER VIEW`
- `CREATE SEQUENCE`
- `DROP SEQUENCE`

### 3. PostgreSQL 的关键限制

虽然执行层相对宽松，但审核层并没有全部覆盖，所以 PostgreSQL 当前是：

- 执行层支持范围更大
- 审核层支持范围更窄

也就是说：

- 有些 PG DDL 可以执行
- 但规范审核不一定有专门规则

## 五、MySQL 与 PostgreSQL 的差异总结

### MySQL

- 创建层：DDL 识别范围较大
- 审核层：对常见 DDL 有规则
- 执行层：最严格，采用明确白名单
- 尤其 `ALTER TABLE` 只支持部分子语法

### PostgreSQL

- 创建层：DDL 识别范围较大
- 审核层：只覆盖部分常见 DDL
- 执行层：最宽松，基本直接交给 PostgreSQL 原生执行

## 六、当前最准确的支持清单

### 1. MySQL 当前实际可执行的 DDL

- `CREATE DATABASE`
- `CREATE TABLE`
- `CREATE VIEW`
- `DROP TABLE`
- `DROP INDEX`
- `TRUNCATE TABLE`
- `ALTER TABLE ... ADD`
- `ALTER TABLE ... CHANGE`
- `ALTER TABLE ... RENAME`
- `ALTER TABLE ... MODIFY`
- `ALTER TABLE ... DROP`
- `ALTER TABLE ... ENGINE`
- `ALTER TABLE ... CONVERT`

### 2. MySQL 当前明确不支持/禁止

- `RENAME TABLE`
- `CREATE INDEX`
- `DROP DATABASE`
- `ALTER TABLE ... COMMENT`
- 其他未被当前正则识别的 `ALTER TABLE` 子语法

### 3. PostgreSQL 当前执行层支持口径

PostgreSQL 当前没有像 MySQL 一样的执行层白名单。只要满足：

- 被工单识别为 `DDL`
- 当前数据库账号有权限
- `database.schema` 资源路径正确
- SQL 本身符合 PostgreSQL 语法

则通常可以执行。

### 4. PostgreSQL 当前审核层明确覆盖的 DDL

- `CREATE TABLE`
- `CREATE VIEW`
- `ALTER TABLE`
- `DROP TABLE`
- `TRUNCATE TABLE`
- `RENAME TABLE`
- `ANALYZE TABLE`
- `CREATE DATABASE`

## 七、关键代码文件

### 工单创建与类型识别

- `backend/internal/orders/services/commit.go`
- `backend/internal/orders/services/db_support.go`

### 执行器

- `backend/internal/orders/api/execute/execute.go`
- `backend/internal/orders/api/execute/extended.go`
- `backend/internal/orders/api/mysql/ddl.go`

### 规范审核

- `backend/internal/inspect/checker/check.go`
- `backend/internal/inspect/controllers/rules/create.go`
- `backend/internal/inspect/controllers/rules/view.go`
- `backend/internal/inspect/controllers/rules/database.go`

## 八、一句话总结

- `MySQL`：真正能执行的 DDL 是明确白名单，尤其 `ALTER TABLE` 只支持部分子语法
- `PostgreSQL`：执行层基本走原生 `Exec`，可执行范围更大，但审核层只覆盖部分常见 DDL

// 凭据加密迁移工具（B 类：历史明文 → 密文）。
//
// 背景：系统里曾有三处凭据以明文落库 —— 数据源 extra_config 的 ssh_password/
// ssh_private_key、备份节点的 ssh_password/ssh_private_key。代码改造后新写入
// 一律加密，但**存量明文不会自己变成密文**：编辑界面留空表示"不修改"，
// 旧值会被原样保留，所以必须跑一次批量迁移。
//
// 与 A 类（sql/20261014_secret_ciphertext_prefix.sql，补前缀）的分工：
//
//	A 类：已经是密文、只是没有版本前缀 → 纯字符串拼接，零风险
//	B 类：真的是明文           → 读出 → 加密 → 写回，需要密钥，不可逆
//
// 安全设计：
//  1. 默认 dry-run，必须显式 -apply 才写库；
//  2. 乐观锁：UPDATE ... WHERE id=? AND col=<原值>，并发修改不会覆盖；
//  3. 日志只打 id 与条数，**绝不打印任何凭据内容**；
//  4. 失败行只记录 id，不中断整体流程，便于人工兜底。
//
// 两道额外保险（B 类原本"不可逆"，这两条让它变成可验证、可回退）：
//
//  5. 行级备份：apply 前把每行的原值加密后写入 _secret_migrate_backup 表。
//     备份表本身不含明文（用主密钥再加密一次），全库备份之外多一份精确到行的
//     回滚依据 —— 全库恢复会丢掉迁移之后产生的业务数据，行级备份不会。
//     备份写失败的行直接跳过、不加密，保证"没有备份的行不被加密"。
//  6. 逐行往返校验：加密后立刻用同一密钥解密，比对与原值是否一致。
//     不一致就跳过该行 —— 宁可不加密，也不能产出解不开的密文。
//
// 用法：
//
//	export GOINSIGHT_SECRET_KEY='<32字节密钥，必须与线上 app.secret_key 一致>'
//	go run ./cmd/secret_migrate -dsn "user:pass@tcp(127.0.0.1:3306)/db?charset=utf8mb4"            # 只统计
//	go run ./cmd/secret_migrate -dsn "..." -check-key                                              # 校验密钥是否匹配
//	go run ./cmd/secret_migrate -dsn "..." -apply                                                  # 真正加密
//	go run ./cmd/secret_migrate -dsn "..." -rollback -batch-tag 20261014-153000                    # 按批次回滚
//
// ⚠️ 执行前必须：① 全量备份元库 ② 验证备份可恢复 ③ 用 -check-key 确认密钥匹配。
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/simajiong/jaysql/pkg/secret"
)

// 待迁移目标。与 pkg/secret 的默认清单保持一致。
type target struct {
	Table    string
	Column   string
	JSONPath []string // 非空表示这是 JSON 列，按路径处理内部敏感值
}

var targets = []target{
	{Table: "insight_instances", Column: "extra_config", JSONPath: []string{"$.ssh_password", "$.ssh_private_key"}},
	{Table: "insight_backup_nodes", Column: "ssh_password"},
	{Table: "insight_backup_nodes", Column: "ssh_private_key"},
}

type stats struct {
	total     int
	empty     int
	encrypted int
	plaintext int
	updated   int
	backedUp  int
	skipped   int // 往返校验未通过 / 备份失败，主动放弃加密的行
	failed    []string
}

const backupTable = "_secret_migrate_backup"

func (s *stats) summary(name string) string {
	return fmt.Sprintf("%-45s 总行数=%-6d 空=%-6d 已加密=%-6d 明文=%-6d 已入库=%-6d 已备份=%-6d 跳过=%-6d 失败=%d",
		name, s.total, s.empty, s.encrypted, s.plaintext, s.updated, s.backedUp, s.skipped, len(s.failed))
}

func main() {
	dsn := flag.String("dsn", "", "MySQL DSN，例如 user:pass@tcp(127.0.0.1:3306)/goinsight?charset=utf8mb4")
	apply := flag.Bool("apply", false, "真正写库；不加该参数只统计不修改（dry-run）")
	checkKey := flag.Bool("check-key", false, "只校验当前密钥能否解开该库里的存量密文，不做任何迁移")
	rollback := flag.Bool("rollback", false, "按 -batch-tag 回滚某一批次的加密，恢复为备份里的原值")
	noBackup := flag.Bool("no-backup", false, "apply 时不写行级备份表（不推荐：会让本批次无法精确回滚）")
	batchTag := flag.String("batch-tag", "", "批次标识，回滚时用来定位；为空则自动生成时间戳")
	batch := flag.Int("batch", 500, "每批处理的行数")
	flag.Parse()

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "必须通过 -dsn 指定数据库连接")
		flag.Usage()
		os.Exit(1)
	}

	// 密钥：优先环境变量，其次 config.yaml 的 app.secret_key。
	// 必须与线上正在用的密钥一致，否则迁移后的密文解不开。
	if err := secret.SelfCheck(); err != nil {
		fmt.Fprintf(os.Stderr, "密钥自检失败，拒绝执行: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("密钥来源=%s 指纹=%s\n\n", secret.KeySource(), secret.KeyFingerprint())

	db, err := sql.Open("mysql", *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "连接数据库失败: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()
	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "数据库不可用: %v\n", err)
		os.Exit(1)
	}

	// -check-key 是 B 类的前置闸门：密钥不对就别往下走。
	// 用错密钥加密 = 把全站凭据锁死，这是本次迁移唯一真正不可逆的风险点。
	if *checkKey {
		os.Exit(runKeyCheck(db))
	}

	tag := *batchTag
	if tag == "" {
		tag = time.Now().Format("20060102-150405")
	}

	if *rollback {
		os.Exit(runRollback(db, tag))
	}

	wantBackup := *apply && !*noBackup
	var bw *backupWriter
	if wantBackup {
		bw = newBackupWriter(db, tag)
		if err := bw.ensureTable(); err != nil {
			fmt.Fprintf(os.Stderr, "创建行级备份表失败，拒绝执行: %v\n", err)
			os.Exit(1)
		}
	}

	mode := "DRY-RUN（只统计，不写库）"
	if *apply {
		mode = "APPLY（真正加密并写库）"
	}
	fmt.Printf("执行模式: %s\n", mode)
	fmt.Printf("批次标识: %s\n", tag)
	fmt.Printf("行级备份: %s\n", map[bool]string{true: "开启 -> " + backupTable, false: "关闭"}[wantBackup])
	fmt.Printf("批次大小: %d\n\n", *batch)

	for _, t := range targets {
		st, err := migrateTarget(db, t, *apply, *batch, bw)
		if err != nil {
			fmt.Printf("%-45s 处理失败: %v\n", t.Table+"."+t.Column, err)
			continue
		}
		fmt.Println(st.summary(t.Table + "." + t.Column))
		if len(st.failed) > 0 && len(st.failed) <= 20 {
			fmt.Printf("    失败行 id: %s\n", strings.Join(st.failed, ","))
		} else if len(st.failed) > 20 {
			fmt.Printf("    失败行 id（前 20）: %s ... 共 %d 行\n",
				strings.Join(st.failed[:20], ","), len(st.failed))
		}
	}

	if *apply && wantBackup {
		fmt.Printf("\n行级备份已写入 %s（batch_tag=%s），如需回滚：\n", backupTable, tag)
		fmt.Printf("  go run ./cmd/secret_migrate -dsn \"...\" -rollback -batch-tag %s\n", tag)
	}

	fmt.Println("\n提示：迁移完成后请用 password-storage-audit.sql 复跑校验，")
	fmt.Println("      并抽样对实例发起一次真实连接测试（只解密成功不算通过，能连上才算）。")
}

func migrateTarget(db *sql.DB, t target, apply bool, batchSize int, bw *backupWriter) (*stats, error) {
	st := &stats{failed: []string{}}
	if !tableExists(db, t.Table) {
		return st, fmt.Errorf("表不存在，已跳过")
	}

	offset := 0
	for {
		rows, err := db.Query(
			fmt.Sprintf("SELECT id, `%s` FROM `%s` ORDER BY id LIMIT %d OFFSET %d", t.Column, t.Table, batchSize, offset))
		if err != nil {
			return st, fmt.Errorf("查询失败: %w", err)
		}

		fetched := 0
		for rows.Next() {
			fetched++
			var id uint64
			var value sql.NullString
			if err := rows.Scan(&id, &value); err != nil {
				st.failed = append(st.failed, fmt.Sprintf("%d(scan)", id))
				continue
			}
			st.total++
			raw := value.String
			if strings.TrimSpace(raw) == "" {
				st.empty++
				continue
			}

			var next string
			var changed bool
			if len(t.JSONPath) > 0 {
				next, changed, err = encryptJSONValue(raw, t.JSONPath)
			} else {
				next, changed, err = encryptPlainValue(raw)
			}
			if err != nil {
				st.failed = append(st.failed, fmt.Sprintf("%d(encrypt)", id))
				continue
			}
			if !changed {
				st.encrypted++
				continue
			}
			st.plaintext++

			if !apply {
				continue
			}
			// 第 1 道保险：往返校验。加密后立刻解密，不一致就不写库 ——
			// 宁可留着明文（读取侧兼容），也不能产出解不开的密文（直接瘫痪）。
			if !roundTripOK(raw, next, t.JSONPath) {
				st.skipped++
				st.failed = append(st.failed, fmt.Sprintf("%d(往返校验未过)", id))
				continue
			}
			// 第 2 道保险：先备份原值（加密后存储，备份表自身不含明文），
			// 备份写失败就放弃这一行，保证"没有备份的行不会被加密"。
			if bw != nil {
				if err := bw.begin(t.Table, t.Column, id, raw); err != nil {
					st.skipped++
					st.failed = append(st.failed, fmt.Sprintf("%d(备份失败)", id))
					continue
				}
				st.backedUp++
			}
			// 乐观锁：值在读取后被改过就跳过，避免覆盖别人的写入。
			//
			// ⚠️ JSON 列必须 CAST 成 CHAR 再比较：MySQL 在**预编译（二进制）协议**下
			// 不会把字符串参数隐式转换成 JSON，直接写 `json_col = ?` 永远匹配 0 行
			// （文本协议下却是等价的 —— 这个差异只在 prepared statement 里出现）。
			// 表现是"所有行都冲突"，很容易被误判成并发冲突而白白排查半天。
			compareCol := fmt.Sprintf("`%s`", t.Column)
			if len(t.JSONPath) > 0 {
				compareCol = fmt.Sprintf("CAST(`%s` AS CHAR)", t.Column)
			}
			result, err := db.Exec(
				fmt.Sprintf("UPDATE `%s` SET `%s` = ? WHERE id = ? AND %s = ?", t.Table, t.Column, compareCol),
				next, id, raw)
			if err != nil {
				st.failed = append(st.failed, fmt.Sprintf("%d(update)", id))
				continue
			}
			affected, _ := result.RowsAffected()
			if affected == 1 {
				st.updated++
				// 回读一次库里真正存下的值，补进备份行当回滚基准。
				//
				// 为什么不能直接把 next 存进去：JSON 列会把写入的文本规范化
				// （键顺序、空格、转义），库里存的与写进去的不是字面相等。
				// 用它当乐观锁基准，回滚时 100% 判成"值已被业务修改"。
				if bw != nil {
					var stored string
					err := db.QueryRow(
						fmt.Sprintf("SELECT %s FROM `%s` WHERE id = ?", readExprOf(db, t.Table, t.Column), t.Table),
						id).Scan(&stored)
					if err != nil {
						st.failed = append(st.failed, fmt.Sprintf("%d(回读失败,原值已备份)", id))
					} else if err := bw.finish(t.Table, t.Column, id, stored); err != nil {
						st.failed = append(st.failed, fmt.Sprintf("%d(补写备份失败,原值已备份)", id))
					}
				}
			} else {
				st.failed = append(st.failed, fmt.Sprintf("%d(冲突)", id))
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return st, fmt.Errorf("遍历结果失败: %w", err)
		}
		_ = rows.Close()

		if fetched < batchSize {
			break
		}
		offset += batchSize
	}
	return st, nil
}

// encryptPlainValue 加密普通列；已是密文则返回 changed=false（幂等）。
func encryptPlainValue(raw string) (string, bool, error) {
	if secret.IsEncrypted(raw) {
		return raw, false, nil
	}
	encrypted, err := secret.Encrypt(raw)
	if err != nil {
		return "", false, err
	}
	return encrypted, true, nil
}

// encryptJSONValue 按路径加密 JSON 列内部的值。
//
// changed 用「敏感值里有没有未加密项」判断，而不是比较整个 JSON 字符串：
// 序列化会把缩进紧凑化，整串比较会把"本来就全是密文"的行误报成待迁移。
func encryptJSONValue(raw string, paths []string) (string, bool, error) {
	need, err := secret.NeedsEncryption([]byte(raw), paths)
	if err != nil {
		return "", false, err
	}
	if !need {
		return raw, false, nil
	}
	encrypted, err := secret.EncryptJSON([]byte(raw), paths)
	if err != nil {
		return "", false, err
	}
	return string(encrypted), true, nil
}

// roundTripOK 验证「加密后的值能被解回原值」。
//
// 这是 apply 前的最后一道闸：GCM 的 tag 校验只能保证"解出来的东西没被篡改"，
// 而这里直接比对内容，能挡住 JSON 路径处理出错、字符集被截断、列被超长截断等
// 一切"能加密但解不回去"的情况。
func roundTripOK(raw, encrypted string, paths []string) bool {
	if len(paths) > 0 {
		back, err := secret.DecryptJSON([]byte(encrypted), paths)
		if err != nil {
			return false
		}
		return jsonEqualSensitiveFields(raw, string(back), paths)
	}
	back, err := secret.Decrypt(encrypted)
	return err == nil && back == raw
}

// jsonEqualSensitiveFields 只比对敏感路径上的值。
//
// 不能直接比整串：序列化会把 JSON 紧凑化/缩进变化，差异与凭据无关，
// 用它当判据会把所有行误判成失败。
func jsonEqualSensitiveFields(rawA, rawB string, paths []string) bool {
	var a, b map[string]any
	if err := json.Unmarshal([]byte(rawA), &a); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(rawB), &b); err != nil {
		return false
	}
	for _, p := range paths {
		key := strings.TrimPrefix(strings.TrimPrefix(p, "$"), ".")
		va, okA := a[key]
		vb, okB := b[key]
		if okA != okB {
			return false
		}
		if !okA {
			continue
		}
		if va != vb {
			return false
		}
	}
	return true
}

// backupWriter 行级备份。
//
// 备份表里的 old/new 值都用主密钥加密后再落库 —— 备份表自身绝不能成为明文泄露源，
// 否则为了"能回滚"反而制造了一个新的明文存储点，与本次改造目标自相矛盾。
type backupWriter struct {
	db  *sql.DB
	tag string
}

func newBackupWriter(db *sql.DB, tag string) *backupWriter {
	return &backupWriter{db: db, tag: tag}
}

func (w *backupWriter) ensureTable() error {
	_, err := w.db.Exec(fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS `%s` (\n"+
			"  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,\n"+
			"  batch_tag     VARCHAR(64)  NOT NULL COMMENT '批次标识，回滚时用',\n"+
			"  table_name    VARCHAR(128) NOT NULL,\n"+
			"  column_name   VARCHAR(128) NOT NULL,\n"+
			"  row_id        BIGINT UNSIGNED NOT NULL,\n"+
			"  old_value_enc LONGTEXT     COMMENT '迁移前原值（已用主密钥加密）',\n"+
			"  new_value_enc LONGTEXT     COMMENT '迁移后密文（已用主密钥加密），回滚时用作乐观锁',\n"+
			"  created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),\n"+
			"  PRIMARY KEY (id),\n"+
			"  KEY idx_batch (batch_tag),\n"+
			"  KEY idx_row (table_name, column_name, row_id)\n"+
			") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='secret_migrate 行级备份，确认无误后可 DROP'",
		backupTable))
	return err
}

// begin 在加密之前落一行备份，只写原值。
//
// 顺序必须是「先备份，再改数据」：备份写失败就不加密这一行，
// 保证库里不会存在"被加密过但没有备份"的行。
func (w *backupWriter) begin(table, column string, rowID uint64, oldValue string) error {
	// ⚠️ 用 SealRaw 而不是 Encrypt：Encrypt 幂等，若原值本身就是密文会原样存下，
	// 回滚时 Decrypt 解出的就不是当初的原值了。SealRaw 始终加密一层，
	// 配 OpenRaw 才能原样还回来。
	oldEnc, err := secret.SealRaw(oldValue)
	if err != nil {
		return err
	}
	_, err = w.db.Exec(
		fmt.Sprintf("INSERT INTO `%s` (batch_tag, table_name, column_name, row_id, old_value_enc) VALUES (?,?,?,?,?)", backupTable),
		w.tag, table, column, rowID, oldEnc)
	return err
}

// finish 在加密成功、回读到实际存储值后，补写回滚基准值。
func (w *backupWriter) finish(table, column string, rowID uint64, storedValue string) error {
	newEnc, err := secret.SealRaw(storedValue)
	if err != nil {
		return err
	}
	_, err = w.db.Exec(
		fmt.Sprintf("UPDATE `%s` SET new_value_enc = ? WHERE batch_tag = ? AND table_name = ? AND column_name = ? AND row_id = ? AND new_value_enc IS NULL", backupTable),
		newEnc, w.tag, table, column, rowID)
	return err
}

// runRollback 按批次把列值恢复为备份里的原值。
//
// 乐观锁：WHERE id=? AND col=<迁移后的值>，如果这行在迁移之后被业务改过，
// 说明它不是我们当初写进去的那个值了，跳过以免覆盖新数据。
func runRollback(db *sql.DB, tag string) int {
	fmt.Printf("=== 回滚批次 %s ===\n\n", tag)

	rows, err := db.Query(fmt.Sprintf(
		"SELECT table_name, column_name, row_id, old_value_enc, new_value_enc FROM `%s` WHERE batch_tag = ? ORDER BY id", backupTable), tag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取备份表失败: %v\n", err)
		return 1
	}
	defer func() { _ = rows.Close() }()

	type item struct {
		table, column  string
		rowID          uint64
		oldEnc, newEnc sql.NullString
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.table, &it.column, &it.rowID, &it.oldEnc, &it.newEnc); err != nil {
			fmt.Fprintf(os.Stderr, "解析备份行失败: %v\n", err)
			return 1
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "遍历备份行失败: %v\n", err)
		return 1
	}
	if len(items) == 0 {
		fmt.Println("该批次没有备份记录，无需回滚。")
		return 0
	}

	var restored, unchanged, skipped int
	for _, it := range items {
		oldVal, err := secret.OpenRaw(it.oldEnc.String)
		if err != nil {
			fmt.Printf("  %s.%s id=%d 解密备份值失败，跳过\n", it.table, it.column, it.rowID)
			skipped++
			continue
		}
		if !it.newEnc.Valid || it.newEnc.String == "" {
			fmt.Printf("  %s.%s id=%d 缺少回滚基准值（当时回读失败），跳过\n", it.table, it.column, it.rowID)
			skipped++
			continue
		}
		newVal, err := secret.OpenRaw(it.newEnc.String)
		if err != nil {
			fmt.Printf("  %s.%s id=%d 解密新值失败，跳过\n", it.table, it.column, it.rowID)
			skipped++
			continue
		}

		// 先读当前值再决定动不动手，这样"行被改过"与"本来就一样"能区分开 ——
		// 只看 RowsAffected 的话这两种情况都返回 0，会把正常情况误报成冲突。
		var current string
		err = db.QueryRow(
			fmt.Sprintf("SELECT %s FROM `%s` WHERE id = ?", readExprOf(db, it.table, it.column), it.table),
			it.rowID).Scan(&current)
		if err != nil {
			fmt.Printf("  %s.%s id=%d 读取当前值失败，跳过: %v\n", it.table, it.column, it.rowID, err)
			skipped++
			continue
		}
		if current != newVal {
			fmt.Printf("  %s.%s id=%d 值已被业务修改，跳过（未覆盖新数据）\n", it.table, it.column, it.rowID)
			skipped++
			continue
		}
		if current == oldVal {
			unchanged++
			continue
		}

		result, err := db.Exec(
			fmt.Sprintf("UPDATE `%s` SET `%s` = ? WHERE id = ?", it.table, it.column),
			oldVal, it.rowID)
		if err != nil {
			fmt.Printf("  %s.%s id=%d 回滚失败: %v\n", it.table, it.column, it.rowID, err)
			skipped++
			continue
		}
		affected, _ := result.RowsAffected()
		if affected == 1 {
			restored++
		} else {
			fmt.Printf("  %s.%s id=%d 回滚未生效，请人工确认\n", it.table, it.column, it.rowID)
			skipped++
		}
	}

	fmt.Printf("\n回滚完成：恢复=%d 本来就一致=%d 跳过=%d\n", restored, unchanged, skipped)
	if skipped > 0 {
		fmt.Println("跳过的行需要人工确认：它们的当前值已不是本次迁移写入的值。")
		return 2
	}
	return 0
}

// keyProbe 密钥校验样本列。
//
// 原则：只挑「代码里一直走加密、存量必然是密文」的列，用它们反推当前密钥对不对。
// 刻意排除 insight_users.password —— 那是 bcrypt 不可逆哈希，不是本模块的密文。
var keyProbe = []struct {
	Table  string
	Column string
}{
	{"insight_instances", "password"},
	{"insight_notify_settings", "mail_password"},
	{"insight_llm_providers", "api_key_enc"},
	{"insight_monitor_notify_channels", "config"},
	{"insight_backup_storage_targets", "s3_access_key"},
	{"insight_backup_storage_targets", "s3_secret_key"},
	{"insight_backup_storage_targets", "ssh_password"},
	{"insight_backup_storage_targets", "ssh_private_key"},
}

// runKeyCheck 用存量密文反推密钥是否匹配，只读，返回进程退出码。
//
// 判据是 AES-GCM 的 tag 校验：密钥对 → 全部解开；密钥错 → 一定解不开（不会"碰巧解开"）。
// 所以「有样本且零失败」= 密钥正确，这是一个强判据，不是启发式猜测。
func runKeyCheck(db *sql.DB) int {
	fmt.Println("=== 密钥匹配校验（只读，不修改任何数据）===")
	fmt.Println()

	var checked, ok, failed int
	for _, p := range keyProbe {
		if !tableExists(db, p.Table) || !columnExists(db, p.Table, p.Column) {
			continue
		}
		values, err := sampleColumn(db, p.Table, p.Column, 200)
		if err != nil {
			fmt.Printf("%-45s 读取失败: %v\n", p.Table+"."+p.Column, err)
			continue
		}
		var c, o, f int
		for _, v := range values {
			if !secret.IsEncrypted(v) && !secret.LooksLikeCiphertext(v) {
				continue
			}
			c++
			if _, err := secret.Decrypt(v); err != nil {
				f++
			} else {
				o++
			}
		}
		checked += c
		ok += o
		failed += f
		fmt.Printf("%-45s 样本=%-5d 密文=%-5d 解开=%-5d 失败=%d\n",
			p.Table+"."+p.Column, len(values), c, o, f)
	}

	fmt.Println()
	switch {
	case checked == 0:
		fmt.Println("⚠️ 该库里没有找到任何存量密文样本，无法判定密钥是否匹配。")
		fmt.Println("   可能是一个全新环境。若确认无历史凭据，可直接执行迁移；否则请先确认 DSN 是否正确。")
		return 2
	case failed > 0:
		fmt.Printf("❌ 密钥不匹配：%d/%d 条存量密文解不开。\n", failed, checked)
		fmt.Println("   当前密钥与这套库正在使用的密钥不是同一把。")
		fmt.Println("   请核对目标环境 config.yaml 的 app.secret_key，或换用 GOINSIGHT_SECRET_KEY 指定后重试。")
		fmt.Println("   ⚠️ 密钥不对时绝对不要带 -apply 执行，否则会把所有凭据加密成解不开的密文。")
		return 1
	default:
		fmt.Printf("✅ 密钥匹配：%d 条存量密文全部解开，可以执行迁移。\n", ok)
		return 0
	}
}

// sampleColumn 取某列前 limit 个非空值。
func sampleColumn(db *sql.DB, table, column string, limit int) ([]string, error) {
	rows, err := db.Query(fmt.Sprintf(
		"SELECT `%s` FROM `%s` WHERE `%s` IS NOT NULL AND `%s` <> '' LIMIT %d",
		column, table, column, column, limit))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if v.Valid && v.String != "" {
			out = append(out, v.String)
		}
	}
	return out, rows.Err()
}

// readExprOf 生成"按文本读出"的列表达式。
//
// JSON 列必须 CAST 成 CHAR：预编译（二进制）协议下 MySQL 不会把字符串参数
// 隐式转成 JSON，直接比较永远匹配 0 行；读出来也一样，需要显式 CAST 成文本。
func readExprOf(db *sql.DB, table, column string) string {
	if isJSONColumn(db, table, column) {
		return fmt.Sprintf("CAST(`%s` AS CHAR)", column)
	}
	return fmt.Sprintf("`%s`", column)
}

// isJSONColumn 判断列是否为 JSON 类型。回滚时用它决定要不要 CAST。
func isJSONColumn(db *sql.DB, table, column string) bool {
	var dataType string
	err := db.QueryRow(
		"SELECT DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?",
		table, column).Scan(&dataType)
	return err == nil && strings.EqualFold(dataType, "json")
}

func columnExists(db *sql.DB, table, column string) bool {
	count := 0
	err := db.QueryRow(
		"SELECT COUNT(1) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?",
		table, column).Scan(&count)
	return err == nil && count > 0
}

func tableExists(db *sql.DB, table string) bool {
	count := 0
	err := db.QueryRow(
		"SELECT COUNT(1) FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?",
		table).Scan(&count)
	return err == nil && count > 0
}

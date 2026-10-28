package services

import (
	"strings"
	"testing"
)

func TestMonitorDisplayLayoutTabsByEngine(t *testing.T) {
	mysql := MonitorDisplayLayoutFor("MySQL", "")
	if mysql == nil {
		t.Fatal("MySQL 展示目录不应为空")
	}
	if len(mysql.Tabs) != 3 {
		t.Fatalf("MySQL 应保持三个 Tab，实际 %d 个", len(mysql.Tabs))
	}
	want := []string{LayerHost, LayerEngine, LayerKernel}
	for i, key := range want {
		if mysql.Tabs[i].Key != key {
			t.Fatalf("MySQL 第 %d 个 Tab 应为 %s，实际 %s", i+1, key, mysql.Tabs[i].Key)
		}
	}

	pg := MonitorDisplayLayoutFor("postgresql", "")
	if pg == nil {
		t.Fatal("PostgreSQL 展示目录不应为空")
	}
	// PostgreSQL 两个 Tab 都有监控项：基础指标（主机层 res.*）+ 数据库指标（pg_stat_*）。
	// 主机层由 Prometheus 统一采集、不区分数据库类型，PG 与 MySQL 展示同一批主机指标。
	if len(pg.Tabs) != 2 {
		t.Fatalf("PostgreSQL 应下发 2 个 Tab，实际 %d 个", len(pg.Tabs))
	}
	if pg.Tabs[0].Key != LayerHost || pg.Tabs[1].Key != LayerEngine {
		t.Fatalf("PostgreSQL Tab 顺序应为基础指标 → 数据库指标，实际 %+v", pg.Tabs)
	}
	// Oracle：基础指标（推荐告警集 res.*）+ 数据库指标（v$ / dba_* 直连）
	oracle := MonitorDisplayLayoutFor("Oracle", "")
	if oracle == nil {
		t.Fatal("Oracle 展示目录不应为空")
	}
	if len(oracle.Tabs) != 2 {
		t.Fatalf("Oracle 应下发 2 个 Tab，实际 %d 个", len(oracle.Tabs))
	}
	if oracle.Tabs[0].Key != LayerHost || oracle.Tabs[1].Key != LayerEngine {
		t.Fatalf("Oracle Tab 顺序应为基础指标 → 数据库指标，实际 %+v", oracle.Tabs)
	}
	// SQL Server：基础指标（与 PG 同源同口径的 res.*）+ 数据库指标（sys.dm_* 直连）
	sqlserver := MonitorDisplayLayoutFor("sqlserver", "")
	if sqlserver == nil {
		t.Fatal("SQL Server 展示目录不应为空")
	}
	if len(sqlserver.Tabs) != 2 {
		t.Fatalf("SQL Server 应下发 2 个 Tab，实际 %d 个", len(sqlserver.Tabs))
	}
	if sqlserver.Tabs[0].Key != LayerHost || sqlserver.Tabs[1].Key != LayerEngine {
		t.Fatalf("SQL Server Tab 顺序应为基础指标 → 数据库指标，实际 %+v", sqlserver.Tabs)
	}
	// 未收录类型回落 nil，由前端走旧的字典推导
	if MonitorDisplayLayoutFor("mongodb", "") != nil {
		t.Fatal("未收录类型应返回 nil")
	}
}

func TestMonitorDisplayLayoutMySQLItemOrderAndSeries(t *testing.T) {
	layout := MonitorDisplayLayoutFor("mysql", "")
	if layout == nil {
		t.Fatal("MySQL 展示目录不应为空")
	}
	byTab := map[string][]DisplayItem{}
	for _, item := range layout.Items {
		byTab[item.Tab] = append(byTab[item.Tab], item)
	}
	// 基础指标：11 个监控项（临时表已迁移到数据库指标末位；「主机网络流量」为整机网卡视角，
	// 紧随 MySQL 客户端协议视角的「网络流量」之后），顺序与附件一致且 order 连续。
	// 8-11 为主机层扩展（IO 与磁盘延迟 / Swap / 网络错误 / 进程与时钟），
	// 与默认告警规则对齐补入，明细 ⊇ 告警规则集。
	host := byTab[LayerHost]
	if len(host) != 11 {
		t.Fatalf("基础指标应有 11 个监控项，实际 %d", len(host))
	}
	wantHost := []string{
		"CPU 使用率", "内存使用率", "磁盘使用率", "磁盘使用量", "数据库网络流量", "主机网络流量", "IOPS",
		"IO 与磁盘延迟", "Swap 使用率", "网络错误/丢包", "进程与时钟",
	}
	for i, name := range wantHost {
		if host[i].Name != name {
			t.Fatalf("基础指标第 %d 项应为 %s，实际 %s", i+1, name, host[i].Name)
		}
		if host[i].Order != i+1 {
			t.Fatalf("基础指标第 %d 项 order 应为 %d，实际 %d", i+1, i+1, host[i].Order)
		}
	}

	// 网络流量：两根曲线，顺序 input_traffic → output_traffic，且各带单位与定义
	traffic := host[4]
	if len(traffic.Series) != 2 {
		t.Fatalf("网络流量应有两根曲线，实际 %d", len(traffic.Series))
	}
	if traffic.Series[0].Key != "input_traffic" || traffic.Series[1].Key != "output_traffic" {
		t.Fatalf("网络流量曲线顺序应为 input_traffic / output_traffic，实际 %+v", traffic.Series)
	}
	for _, s := range traffic.Series {
		if s.Unit != "KB" || s.Definition == "" {
			t.Fatalf("网络流量曲线缺少单位或定义：%+v", s)
		}
		if s.Scale != scaleByteToKB {
			t.Fatalf("网络流量应按 KB 换算（scale=%v），实际 %v", scaleByteToKB, s.Scale)
		}
		if s.MetricKey == "" {
			t.Fatal("网络流量应映射到平台现有采集键")
		}
	}

	// 主机网络流量：整机网卡视角，必须落到 res.network_*（Prometheus node_network_*）。
	// 这一项存在的意义就是避免把「MySQL 客户端收发字节」误读成整机流量 ——
	// scp / SSH / 应用外网流量只在这里体现，两者数量级差几百倍是常态。
	hostNet := host[5]
	if hostNet.Key != "mysql.host.net_traffic_host" {
		t.Fatalf("基础指标第 6 项应为主机网络流量，实际 %+v", hostNet)
	}
	if len(hostNet.Series) != 2 {
		t.Fatalf("主机网络流量应有两根曲线，实际 %d", len(hostNet.Series))
	}
	if hostNet.Series[0].MetricKey != MetricResNetworkIn || hostNet.Series[1].MetricKey != MetricResNetworkOut {
		t.Fatalf("主机网络流量应映射到 %s / %s，实际 %+v", MetricResNetworkIn, MetricResNetworkOut, hostNet.Series)
	}
	for _, s := range hostNet.Series {
		// 采集值就是 B/s，Scale 必须为 1；单位是 B/s 而非 B，前端才会按速率自动换算
		if s.Unit != "B/s" || s.Scale != scaleNoop || s.Definition == "" {
			t.Fatalf("主机网络流量单位/换算应为 B/s + scaleNoop，实际 %+v", s)
		}
	}

	// 复制延迟：秒级数值，不能与 0/1 布尔的复制线程状态共用一套 Y 轴，独立成项
	lag := byTab[LayerEngine][7]
	if lag.Name != "复制延迟" || lag.Order != 8 {
		t.Fatalf("数据库指标第 8 项应为复制延迟(order 8)，实际 %s/%d", lag.Name, lag.Order)
	}
	if len(lag.Series) != 1 || lag.Series[0].MetricKey != MetricReplLagSeconds || lag.Series[0].Unit != "s" {
		t.Fatalf("复制延迟应映射为 %s 且单位为 s，实际 %+v", MetricReplLagSeconds, lag.Series)
	}

	// 临时表：已从「基础指标」迁移到「数据库指标」，4 行指标顺序与附件一致
	tmp := byTab[LayerEngine][8]
	if tmp.Name != "临时表" {
		t.Fatalf("数据库指标第 9 项应为临时表，实际 %s", tmp.Name)
	}
	if tmp.Order != 9 {
		t.Fatalf("临时表 order 应为 9（复制延迟独立成项后顺延），实际 %d", tmp.Order)
	}
	wantTmp := []struct {
		key       string
		metricKey string
	}{
		{"created_tmp_disk_tables", MetricEngTmpDiskTables},
		{"Innodb_num_open_files", MetricEngInnodbOpenFiles},
		{"open_files", MetricEngOpenFiles},
		{"open_tables", MetricEngOpenTables},
		{"tmp_disk_pct", MetricCapTmpDiskPct},
	}
	if len(tmp.Series) != len(wantTmp) {
		t.Fatalf("临时表应有 %d 行指标，实际 %d", len(wantTmp), len(tmp.Series))
	}
	for i, s := range tmp.Series {
		if s.Key != wantTmp[i].key || s.MetricKey != wantTmp[i].metricKey {
			t.Fatalf("临时表第 %d 行应为 %s → %s，实际 %+v", i+1, wantTmp[i].key, wantTmp[i].metricKey, s)
		}
		if s.Order != i+1 {
			t.Fatalf("临时表第 %d 行 order 应为 %d，实际 %d", i+1, i+1, s.Order)
		}
	}

	// 数据库指标 / 引擎指标 的监控项数量与顺序（第 8 项为复制延迟独立卡片；
	// 10-13 为与默认告警规则对齐补入的扩展卡片）
	engine := byTab[LayerEngine]
	if len(engine) != 13 || engine[0].Name != "会话连接" || engine[6].Name != "节点复制线程状态" ||
		engine[7].Name != "复制延迟" || engine[8].Name != "临时表" {
		t.Fatalf("数据库指标监控项顺序不符：%+v", engine)
	}
	// 迁移后 order 必须连续，不留空位
	for i, item := range engine {
		if item.Order != i+1 {
			t.Fatalf("数据库指标第 %d 项 order 应为 %d，实际 %d", i+1, i+1, item.Order)
		}
	}
	kernel := byTab[LayerKernel]
	if len(kernel) != 11 || kernel[0].Name != "InnoDB Buffer Pool 命中率" || kernel[10].Name != "InnoDB Redo 写次数" {
		t.Fatalf("引擎指标监控项顺序不符：共 %d 项", len(kernel))
	}
	// 执行次数：7 行，顺序与附件一致
	if got := len(engine[2].Series); got != 7 {
		t.Fatalf("执行次数应有 7 行指标，实际 %d", got)
	}
}

func TestMonitorDisplayLayoutPostgreSQLContent(t *testing.T) {
	layout := MonitorDisplayLayoutFor("PostgreSQL", "")
	if layout == nil {
		t.Fatal("PostgreSQL 展示目录不应为空")
	}
	// 基础指标：主机层 10 项，与 MySQL 同源同采集（MySQL 的「网络流量」除外 —— PG 无客户端
	// 收发字节统计，perf.write_bytes 恒为 unsupported，补进去只会得到一条恒空的曲线；
	// 「主机网络流量」是整机网卡视角、与数据库类型无关，PG 也同样下发）。
	// 7-10 为主机层扩展（IO 与磁盘延迟 / Swap / 网络错误 / 进程与时钟），与 MySQL 对齐。
	wantHost := []string{"CPU 使用率", "内存使用率", "磁盘使用率", "磁盘使用量", "主机网络流量", "IOPS", "IO 与磁盘延迟", "Swap 使用率", "网络错误/丢包", "进程与时钟"}
	host := make([]DisplayItem, 0, len(wantHost))
	engine := make([]DisplayItem, 0, len(layout.Items))
	for _, item := range layout.Items {
		if item.Tab == LayerHost {
			host = append(host, item)
			continue
		}
		engine = append(engine, item)
	}
	if len(host) != len(wantHost) {
		t.Fatalf("PostgreSQL 基础指标应有 %d 个监控项，实际 %d", len(wantHost), len(host))
	}
	for i, name := range wantHost {
		if host[i].Name != name {
			t.Fatalf("PostgreSQL 基础指标第 %d 项应为 %s，实际 %s", i+1, name, host[i].Name)
		}
		if host[i].Order != i+1 {
			t.Fatalf("PostgreSQL 基础指标第 %d 项 order 应为 %d，实际 %d", i+1, i+1, host[i].Order)
		}
	}
	// 主机层必须落到 res.* 采集键，否则目录有项、页面仍然是空的
	for i, key := range []string{MetricResCPU, MetricResMemory, MetricResDisk, MetricResDiskUsed} {
		if len(host[i].Series) == 0 || host[i].Series[0].MetricKey != key {
			t.Fatalf("PostgreSQL 基础指标第 %d 项应映射到 %s，实际 %+v", i+1, key, host[i].Series)
		}
	}

	// 13-17 为与默认告警规则对齐补入的扩展卡片
	want := []string{"逻辑复制延迟", "复制槽堆积", "备库 APPLY 延迟", "备库发送延迟", "复制延迟",
		"空闲事务", "慢查询", "膨胀点", "每秒事务数", "操作行数", "连接数", "数据库最大年龄",
		"采集与可用性", "缓存命中率", "锁等待", "长事务", "无主键表"}
	if len(engine) != len(want) {
		t.Fatalf("PostgreSQL 数据库指标应有 %d 个监控项，实际 %d", len(want), len(engine))
	}
	for i, name := range want {
		if engine[i].Name != name {
			t.Fatalf("PostgreSQL 第 %d 项应为 %s，实际 %s", i+1, name, engine[i].Name)
		}
	}
	// 连接数：6 行（含对齐补入的活跃连接占比 / 连接使用率）
	conn := engine[10]
	if len(conn.Series) != 6 {
		t.Fatalf("连接数应有 6 行指标，实际 %d", len(conn.Series))
	}
	if conn.Series[0].MetricKey != MetricPerfActiveConn || conn.Series[1].MetricKey != MetricPerfConnCount {
		t.Fatalf("连接数映射不符：%+v", conn.Series)
	}
	// 数据库最大年龄：附件第十二项，落到事务 ID 回卷风险键
	if len(engine[11].Series) != 1 || engine[11].Series[0].MetricKey != MetricRiskXIDAge {
		t.Fatalf("数据库最大年龄应映射到 %s，实际 %+v", MetricRiskXIDAge, engine[11].Series)
	}
	// 扩展卡片首项「采集与可用性」：avail.up / avail.collect_ms
	if avail := engine[12]; len(avail.Series) != 2 || avail.Series[0].MetricKey != MetricAvailUp || avail.Series[1].MetricKey != MetricAvailCollectMs {
		t.Fatalf("采集与可用性应映射到 %s / %s，实际 %+v", MetricAvailUp, MetricAvailCollectMs, avail.Series)
	}
	// 复制相关指标：拆分为 5 个独立监控项，命名/单位与指标字典（默认模板）保持一致
	if lag := engine[4]; len(lag.Series) != 1 || lag.Series[0].MetricKey != MetricReplLagSeconds {
		t.Fatalf("复制延迟应独立成项并映射 %s，实际 %+v", MetricReplLagSeconds, lag.Series)
	}
	if logicalRep := engine[0]; len(logicalRep.Series) != 1 || logicalRep.Series[0].MetricKey != MetricEngLogicalRepLagBytes {
		t.Fatalf("逻辑复制延迟应独立成项并映射 %s，实际 %+v", MetricEngLogicalRepLagBytes, logicalRep.Series)
	}
	if slotLag := engine[1]; len(slotLag.Series) != 1 || slotLag.Series[0].MetricKey != MetricEngReplSlotLag {
		t.Fatalf("复制槽堆积应独立成项并映射 %s，实际 %+v", MetricEngReplSlotLag, slotLag.Series)
	}
	if replayLag := engine[2]; len(replayLag.Series) != 1 || replayLag.Series[0].MetricKey != MetricEngReplayLagSeconds {
		t.Fatalf("备库 APPLY 延迟应独立成项并映射 %s，实际 %+v", MetricEngReplayLagSeconds, replayLag.Series)
	}
	if sentLag := engine[3]; len(sentLag.Series) != 1 || sentLag.Series[0].MetricKey != MetricEngSentLagBytes {
		t.Fatalf("备库发送延迟应独立成项并映射 %s，实际 %+v", MetricEngSentLagBytes, sentLag.Series)
	}
	// 附件定义的每一行都必须有采集源：目录里不该再留下"展示但没数据"的空行
	for _, item := range engine {
		for _, s := range item.Series {
			if s.MetricKey == "" {
				t.Fatalf("PostgreSQL 监控项 %s 的指标行 %s 缺少采集键", item.Name, s.Key)
			}
		}
	}
}

// Oracle 基础指标必须与 MySQL 逐项对齐（主机层是同一份 node_exporter 数据），
// 数据库指标保持 v$ / dba_* 口径，且「数据库网络流量」只出现一次（不重复建卡）。
func TestMonitorDisplayLayoutOracleHostParityWithMySQL(t *testing.T) {
	oracle := MonitorDisplayLayoutFor("Oracle", "")
	mysql := MonitorDisplayLayoutFor("MySQL", "")
	if oracle == nil || mysql == nil {
		t.Fatal("Oracle / MySQL 展示目录都不应为空")
	}
	hostItems := func(layout *MonitorDisplayLayout) []DisplayItem {
		items := make([]DisplayItem, 0, len(layout.Items))
		for _, item := range layout.Items {
			if item.Tab == LayerHost {
				items = append(items, item)
			}
		}
		return items
	}
	oh, mh := hostItems(oracle), hostItems(mysql)
	if len(oh) != len(mh) {
		t.Fatalf("Oracle 基础指标监控项数应与 MySQL 一致（%d），实际 %d", len(mh), len(oh))
	}
	seriesRows := 0
	for i := range mh {
		if oh[i].Name != mh[i].Name {
			t.Fatalf("Oracle 基础指标第 %d 项应为 %s（与 MySQL 对齐），实际 %s", i+1, mh[i].Name, oh[i].Name)
		}
		if len(oh[i].Series) != len(mh[i].Series) {
			t.Fatalf("Oracle 监控项 %s 的曲线数应与 MySQL 一致（%d），实际 %d", mh[i].Name, len(mh[i].Series), len(oh[i].Series))
		}
		for j := range mh[i].Series {
			if oh[i].Series[j].MetricKey != mh[i].Series[j].MetricKey {
				t.Fatalf("Oracle 监控项 %s 第 %d 行应映射 %s，实际 %s", mh[i].Name, j+1,
					mh[i].Series[j].MetricKey, oh[i].Series[j].MetricKey)
			}
			seriesRows++
		}
	}
	// 与 MySQL 同为 11 个监控项 / 16 行曲线（模板勾选界面显示 16 项）
	if len(oh) != 11 || seriesRows != 16 {
		t.Fatalf("Oracle 基础指标应为 11 项 / 16 行，实际 %d 项 / %d 行", len(oh), seriesRows)
	}
	// 协议流量只能出现在基础指标 Tab，引擎层不得重复
	for _, item := range oracle.Items {
		if item.Tab == LayerEngine {
			for _, s := range item.Series {
				if s.MetricKey == MetricPerfWriteBytes || s.MetricKey == MetricPerfReadBytes {
					t.Fatalf("Oracle 数据库指标 %s 重复展示了协议流量 %s", item.Name, s.MetricKey)
				}
			}
		}
	}
}

// 默认模板指标集合必须与展示目录中非空采集键完全一致
func TestDisplayLayoutMetricKeys(t *testing.T) {
	cases := map[string][]string{
		"mysql": {
			"perf.active_conn_count",
			"perf.conn_count",
			"perf.qps",
			"perf.tps",
			"perf.read_rows",
			"perf.read_bytes",
			"perf.write_bytes",
			"eng.slow_queries",
			"repl.io_running",
			"repl.sql_running",
			"kern.buffer_dirty_pct",
			"perf.cache_hit_ratio",
			"perf.locks",
			"kern.flush_rate",
			"kern.redo_write_rate",
			// 基础指标 Tab 的主机层指标：明细页展示了就必须进模板
			"res.cpu",             // cpu_usage
			"res.memory",          // mem_usage
			"res.disk",            // disk_usage
			"res.disk_used",       // disk_used
			"res.disk_throughput", // data_io_bytes
			"res.disk_iops",       // data_io_ps
			"res.network_in",      // net_in（主机网卡）
			"res.network_out",     // net_out（主机网卡）
			// 缓冲池利用率：复用既有容量指标口径（已用页 / 总页）
			"cap.buffer_pool_used_pct", // Innodb_buffer_use_ratio
			// 执行次数（Com_* 系列）与全表扫描
			"eng.com_delete",
			"eng.com_insert",
			"eng.com_insert_select",
			"eng.com_replace",
			"eng.com_replace_select",
			"eng.com_select",
			"eng.com_update",
			"eng.select_scan",
			// 临时表与文件 / 表缓存（SHOW GLOBAL STATUS 同源）
			"eng.tmp_disk_tables",   // created_tmp_disk_tables
			"eng.innodb_open_files", // Innodb_num_open_files
			"eng.open_files",        // open_files
			"eng.open_tables",       // open_tables
			// InnoDB Buffer Pool / Data 读写吞吐量
			"kern.buffer_dirty_pages",
			"kern.buffer_read_req_rate",
			"kern.buffer_write_req_rate",
			"kern.innodb_data_read_rate",
			"kern.innodb_data_written_rate",
			// InnoDB 行锁
			"kern.row_lock_time_ms",
			"kern.row_lock_time_avg_ms",
			"kern.row_lock_time_rate",
			"kern.row_lock_waits",
			"kern.row_lock_waits_rate",
			// InnoDB 行操作 / 刷盘 / 排序
			"kern.rows_deleted_rate",
			"kern.rows_inserted_rate",
			"kern.rows_updated_rate",
			"kern.data_fsyncs_rate",
			"kern.sort_rows_rate",
			// InnoDB Redo 写次数
			"kern.log_write_req_rate",
			"kern.log_writes_rate",
			"kern.os_log_fsyncs_rate",
			// 与默认告警规则对齐补入的指标（明细 ⊇ 告警规则集，见 TestDefaultAlertRulesCoveredByDisplayLayout）
			"avail.up",                 // 采集与可用性
			"avail.collect_ms",         // 采集耗时
			"repl.lag_seconds",         // 复制延迟（节点复制线程状态卡片）
			"cap.conn_used_pct",        // 活跃连接占比（会话连接卡片）
			"cap.conn_thread_used_pct", // 连接使用率（会话连接卡片）
			"cap.tmp_disk_pct",         // 临时表落盘率（临时表卡片）
			"perf.long_trx_count",      // 长事务
			"eng.semisync_status",      // 半同步复制
			"risk.no_pk_tables",        // 无主键表
			"res.io",                   // IO 使用率（IO 与磁盘延迟卡片）
			"res.disk_await_ms",        // 磁盘延迟
			"res.swap_used_pct",        // Swap 使用率
			"res.net_errors",           // 网络错误/丢包
			"res.fd_used_pct",          // 文件句柄使用率（进程与时钟卡片）
			"res.time_offset_s",        // 时钟偏移
		},
		"postgresql": {
			"eng.repl_slot_lag_bytes",
			"eng.deadlocks",
			"perf.tps",
			"perf.active_conn_count",
			"perf.conn_count",
			// 附件十二类指标（逻辑复制延迟 / 复制槽堆积 / 备库 APPLY 延迟 / 备库发送延迟 / 复制延迟 / 长事务 / 慢查询 / 膨胀点 / 事务 / 元组 / 连接 / XID）
			"eng.logical_rep_lag_bytes", // 逻辑复制延迟
			"eng.replay_lag_seconds",    // 备库 APPLY 延迟
			"eng.sent_lag_bytes",        // 备库发送延迟
			"eng.idle_in_trx_1s",        // long_idle_in_transaction_1s
			"eng.idle_in_trx_3s",        // long_idle_in_transaction_3s
			"eng.idle_in_trx_5s",        // long_idle_in_transaction_5s
			"eng.long_query_1s",         // long_query_1s
			"eng.long_query_3s",         // long_query_3s
			"eng.long_query_5s",         // long_query_5s
			"eng.swell_time_seconds",    // swell_time
			"eng.xact_commit_rate",      // xact_commit
			"eng.xact_rollback_rate",    // xact_rollback
			"eng.tup_inserted_rate",     // tup_inserted
			"eng.tup_updated_rate",      // tup_updated
			"eng.tup_deleted_rate",      // tup_deleted
			"eng.tup_fetched_rate",      // tup_fetched
			"eng.tup_returned_rate",     // tup_returned
			"perf.idle_in_trx_count",    // idle_in_transaction
			"perf.waiting_conn_count",   // waiting_connection
			"risk.xid_age",              // max_age（数据库最大年龄）
			// 基础指标 Tab 的主机层指标：与 MySQL 同源，明细页展示了就必须进模板
			"res.cpu",             // cpu_usage
			"res.memory",          // mem_usage
			"res.disk",            // disk_usage
			"res.disk_used",       // disk_used
			"res.disk_throughput", // data_io_bytes
			"res.disk_iops",       // data_io_ps
			"res.network_in",      // net_in（主机网卡）
			"res.network_out",     // net_out（主机网卡）
			// 与默认告警规则对齐补入的指标（明细 ⊇ 告警规则集，见 TestDefaultAlertRulesCoveredByDisplayLayout）
			"avail.up",                 // 采集与可用性
			"avail.collect_ms",         // 采集耗时
			"repl.lag_seconds",         // 复制延迟（备库视角）
			"cap.conn_used_pct",        // 活跃连接占比（连接数卡片）
			"cap.conn_thread_used_pct", // 连接使用率
			"perf.cache_hit_ratio",     // 缓存命中率
			"perf.locks",               // 锁等待
			"perf.long_trx_count",      // 长事务
			"risk.no_pk_tables",        // 无主键表
			"res.io",                   // IO 使用率（IO 与磁盘延迟卡片）
			"res.disk_await_ms",        // 磁盘延迟
			"res.swap_used_pct",        // Swap 使用率
			"res.net_errors",           // 网络错误/丢包
			"res.fd_used_pct",          // 文件句柄使用率（进程与时钟卡片）
			"res.time_offset_s",        // 时钟偏移
		},
		// Oracle：基础指标（主机层 16 行，与 MySQL 逐项对齐）+ 数据库指标（v$ / dba_* 14 项）
		"oracle": {
			// 主机层：node_exporter 口径，与数据库类型无关
			"res.cpu",
			"res.memory",
			"res.disk",
			"res.disk_used",
			"res.network_in",
			"res.network_out",
			"res.disk_iops",
			"res.disk_throughput",
			"res.io",
			"res.disk_await_ms",
			"res.swap_used_pct",
			"res.net_errors",
			"res.fd_used_pct",
			"res.time_offset_s",
			// 数据库网络流量（v$sysstat SQL*Net）：基础指标 Tab 内的协议层流量
			"perf.write_bytes",
			"perf.read_bytes",
			// 数据库指标
			"avail.up",
			"avail.collect_ms",
			"perf.qps",
			"perf.tps",
			"perf.conn_count",
			"perf.active_conn_count",
			"cap.process_used_pct",
			"cap.conn_thread_used_pct",
			"cap.conn_used_pct",
			"perf.cache_hit_ratio",
			"perf.locks",
			"perf.long_trx_count",
			"kern.redo_write_rate",
			"cap.tablespace_used_pct",
		},
	}
	for dbType, want := range cases {
		got := displayLayoutMetricKeys(dbType)
		if len(got) != len(want) {
			t.Fatalf("%s 展示目录采集键数量应为 %d，实际 %d: %v", dbType, len(want), len(got), got)
		}
		gotSet := map[string]bool{}
		for _, k := range got {
			gotSet[k] = true
		}
		for _, k := range want {
			if !gotSet[k] {
				t.Fatalf("%s 展示目录缺少采集键 %s，实际 %v", dbType, k, got)
			}
		}
		// 同时校验这些 key 能通过模板校验（scope/引擎范围均合法）
		if _, err := validateTemplateMetricKeys(dbType, got); err != nil {
			t.Fatalf("%s 展示目录采集键未通过模板校验: %v", dbType, err)
		}
	}
}

// 主机层指标：MySQL / PostgreSQL / Oracle / TiDB 明细页都有「基础指标」Tab，模板必须能勾选；
// Doris 与其余类型仍拒绝（明细不做模板裁剪，勾了也没有展示位）。
func TestTemplateHostLayerPolicy(t *testing.T) {
	mysqlKeys := displayLayoutMetricKeys("mysql")
	if !containsString(mysqlKeys, MetricResCPU) {
		t.Fatalf("MySQL 模板应包含主机层 CPU 指标，实际 %v", mysqlKeys)
	}
	// MySQL 允许勾选主机层
	if _, err := validateTemplateMetricKeys("mysql", []string{MetricResCPU, MetricResMemory, MetricPerfQPS}); err != nil {
		t.Fatalf("MySQL 模板应允许勾选主机层指标: %v", err)
	}
	// Oracle 与 MySQL 同待遇：主机层纳入模板治理
	if _, err := validateTemplateMetricKeys("oracle", []string{MetricResCPU, MetricCapTablespaceUsedPct}); err != nil {
		t.Fatalf("Oracle 模板应允许勾选主机层指标: %v", err)
	}
	// TiDB 拥有专属展示目录，与 MySQL 同一套模板治理：允许勾选主机层
	if _, err := validateTemplateMetricKeys("tidb", []string{MetricResCPU}); err != nil {
		t.Fatalf("TiDB 模板应允许勾选主机层指标: %v", err)
	}
	// 勾选界面：MySQL / Oracle / TiDB 可见主机层，Doris 不可见
	if !hostMetricsInTemplate("MySQL") || !hostMetricsInTemplate("oracle") || !hostMetricsInTemplate("tidb") || hostMetricsInTemplate("doris") {
		t.Fatal("hostMetricsInTemplate 应对 MySQL / PostgreSQL / Oracle / TiDB 生效，Doris 不生效")
	}
	for _, engineKey := range []string{"mysql", "oracle"} {
		applicable := map[string]bool{}
		for _, item := range templateApplicableMetricsWithHost(engineKey) {
			applicable[item.MetricKey] = true
		}
		for _, key := range []string{MetricResCPU, MetricResMemory, MetricResDisk, MetricResDiskUsed, MetricResDiskIOPS, MetricResDiskThroughput} {
			if !applicable[key] {
				t.Fatalf("%s 勾选界面应能看到主机层指标 %s", engineKey, key)
			}
		}
	}
	tidbHostSeen := false
	for _, item := range templateApplicableMetricsWithHost("tidb") {
		if item.Scope == "host" && item.MetricKey == MetricResCPU {
			tidbHostSeen = true
		}
	}
	if !tidbHostSeen {
		t.Fatal("tidb 勾选界面应能看到主机层指标 res.cpu")
	}
}

func containsString(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

// 展示目录里引用到的采集键必须在指标字典中存在，否则卡片永远画不出曲线
func TestMonitorDisplayLayoutMetricKeysExistInCatalog(t *testing.T) {
	known := map[string]bool{}
	for _, item := range builtinMetricCatalog {
		known[item.MetricKey] = true
	}
	for _, dbType := range []string{"mysql", "postgresql", "oracle"} {
		layout := MonitorDisplayLayoutFor(dbType, "")
		if layout == nil {
			t.Fatalf("%s 展示目录不应为空", dbType)
		}
		for _, item := range layout.Items {
			for _, s := range item.Series {
				if s.MetricKey == "" {
					continue
				}
				if !known[s.MetricKey] {
					t.Fatalf("%s 监控项 %s 引用了字典中不存在的采集键 %s", dbType, item.Name, s.MetricKey)
				}
			}
		}
	}
}

// 明细口径展开清单：多值指标逐行展开、顺序与目录一致、无采集源行 MetricKey 为空。
// 顺带用 -v 输出完整清单，作为「模板指标项清单」文档的对账来源。
func TestDisplayLayoutTemplateItemsDump(t *testing.T) {
	for _, dbType := range []string{"mysql", "postgresql", "oracle"} {
		rows := displayLayoutTemplateItems(dbType)
		if len(rows) == 0 {
			t.Fatalf("%s 明细口径展开清单不应为空", dbType)
		}
		available, noSource := 0, 0
		for i, row := range rows {
			if row.SeriesKey == "" {
				t.Fatalf("%s 第 %d 行 SeriesKey 为空（明细口径要求每行都有原始指标名）", dbType, i)
			}
			if row.MetricKey != "" {
				available++
			} else {
				noSource++
			}
			t.Logf("%s|%s|%s|%s|%s|采集=%s", dbType, row.Tab, row.ItemName, row.SeriesKey, row.Unit, row.MetricKey)
		}
		t.Logf("%s 合计 %d 行：有采集源 %d，无采集源 %d", dbType, len(rows), available, noSource)
		// 与模板同步口径对账：displayLayoutMetricKeys（去重后的采集键集合）
		// 必须与展开清单中的采集键集合完全一致
		keys := displayLayoutMetricKeys(dbType)
		seen := map[string]bool{}
		for _, row := range rows {
			if row.MetricKey != "" {
				seen[row.MetricKey] = true
			}
		}
		if len(keys) != len(seen) {
			t.Fatalf("%s 采集键数量不一致：同步口径 %d，展开口径 %d", dbType, len(keys), len(seen))
		}
		for _, key := range keys {
			if !seen[key] {
				t.Fatalf("%s 采集键 %s 在展开清单中缺失", dbType, key)
			}
		}
	}
}

// TestRecommendedAlertLevelsCoveredByDisplayLayout 推荐档位涉及的指标必须被监控明细覆盖。
//
// 明细页是指标的展示事实来源：配置页「恢复推荐值」会把这些指标写进规则，
// 若明细里查不到曲线，用户面对一条「查无此指标」的规则无从理解。
//
// 注：原先这里校验的是 defaultRuleMetrics（自动播种的默认规则集），播种已随
// 指标字典下线，改为校验 recommendedAlertLevels —— 它是配置页推荐值的唯一来源。
func TestRecommendedAlertLevelsCoveredByDisplayLayout(t *testing.T) {
	for engineKey := range displayLayouts {
		layoutKeys := map[string]struct{}{}
		for _, key := range displayLayoutMetricKeys(engineKey) {
			layoutKeys[key] = struct{}{}
		}
		for metricKey := range recommendedAlertLevels {
			desc, ok := metricDescriptorOf(metricKey)
			if !ok {
				continue
			}
			if !engineInScope(engineKey, desc.EngineScope) {
				continue
			}
			// 已退役指标是刻意的例外：它在这一引擎上根本没有数据源，
			// 目录里不收它是为了让用户不再看到恒空的卡片与永远不响的告警规则。
			if metricKeyRetiredForEngine(engineKey, canonicalMetricKey(metricKey)) {
				continue
			}
			if _, ok := layoutKeys[canonicalMetricKey(metricKey)]; !ok {
				t.Errorf("引擎 %s 的推荐告警指标 %s（%s）不在监控明细展示目录中，请在 monitor_display_layout.go 补齐",
					engineKey, metricKey, desc.DisplayName)
			}
		}
	}
}

// TestSQLServerDisplayLayoutHostParityWithPG 校验 SQL Server 的主机层展示与 PostgreSQL 逐项一致。
//
// 主机层 res.* 由 Prometheus + node_exporter 统一采集，采集侧不区分数据库类型 ——
// 同一台机器上的 PG 与 SQL Server 拿到的是同一份数据。若两边的监控项、单位或顺序
// 出现分歧，同一个主机指标会在两种实例的明细页上给出不同读数，运维无法判断哪个是对的。
func TestSQLServerDisplayLayoutHostParityWithPG(t *testing.T) {
	pg := MonitorDisplayLayoutFor("postgresql", "")
	sqlserver := MonitorDisplayLayoutFor("sqlserver", "")
	if pg == nil || sqlserver == nil {
		t.Fatal("PG / SQL Server 展示目录不应为空")
	}
	hostOf := func(layout *MonitorDisplayLayout) []DisplayItem {
		items := make([]DisplayItem, 0)
		for _, item := range layout.Items {
			if item.Tab == LayerHost {
				items = append(items, item)
			}
		}
		return items
	}
	pgHost, ssHost := hostOf(pg), hostOf(sqlserver)
	if len(pgHost) != len(ssHost) {
		t.Fatalf("SQL Server 与 PG 的基础指标监控项数量应一致，PG %d 项 / SQL Server %d 项", len(pgHost), len(ssHost))
	}
	for i := range pgHost {
		if pgHost[i].Name != ssHost[i].Name {
			t.Fatalf("基础指标第 %d 项名称应一致，PG=%s / SQL Server=%s", i+1, pgHost[i].Name, ssHost[i].Name)
		}
		if pgHost[i].Order != ssHost[i].Order {
			t.Fatalf("基础指标第 %d 项 order 应一致，PG=%d / SQL Server=%d", i+1, pgHost[i].Order, ssHost[i].Order)
		}
		if len(pgHost[i].Series) != len(ssHost[i].Series) {
			t.Fatalf("基础指标「%s」曲线数量应一致，PG %d / SQL Server %d",
				pgHost[i].Name, len(pgHost[i].Series), len(ssHost[i].Series))
		}
		for j := range pgHost[i].Series {
			if pgHost[i].Series[j].MetricKey != ssHost[i].Series[j].MetricKey {
				t.Fatalf("基础指标「%s」第 %d 根曲线的采集键应一致，PG=%s / SQL Server=%s",
					pgHost[i].Name, j+1, pgHost[i].Series[j].MetricKey, ssHost[i].Series[j].MetricKey)
			}
			if pgHost[i].Series[j].Unit != ssHost[i].Series[j].Unit {
				t.Fatalf("基础指标「%s」第 %d 根曲线的单位应一致，PG=%s / SQL Server=%s",
					pgHost[i].Name, j+1, pgHost[i].Series[j].Unit, ssHost[i].Series[j].Unit)
			}
		}
	}
}

// TestSQLServerDisplayLayoutKeysCollectable 校验 SQL Server 展示目录里的每一行：
//  1. 采集键在指标字典里存在（拼错 key 会静默变成"永远无数据"的卡片）；
//  2. 该键对 SQL Server 适用（engine_scope 命中），否则模板勾了也采不到；
//  3. 除框架指标（avail.*，由采集框架产出）外，键必须落在 loadEnabledMetricKeys 里。
func TestSQLServerDisplayLayoutKeysCollectable(t *testing.T) {
	layout := MonitorDisplayLayoutFor("sqlserver", "")
	if layout == nil {
		t.Fatal("SQL Server 展示目录不应为空")
	}
	engineKeys := map[string]struct{}{}
	for _, key := range loadEnabledMetricKeys("sqlserver") {
		engineKeys[key] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, item := range layout.Items {
		for _, s := range item.Series {
			key := strings.TrimSpace(s.MetricKey)
			if key == "" {
				t.Errorf("监控项「%s」存在无采集源的行（明细口径有、平台未采），确认是否符合预期", item.Name)
				continue
			}
			desc, ok := metricDescriptorOf(key)
			if !ok {
				t.Errorf("监控项「%s」的采集键 %s 不在指标字典中，请核对 monitor_metrics.go", item.Name, key)
				continue
			}
			if !engineInScope("sqlserver", desc.EngineScope) {
				t.Errorf("监控项「%s」的采集键 %s 不适用于 SQL Server（engine_scope=%q）", item.Name, key, desc.EngineScope)
				continue
			}
			// 主机层 res.* 走 Prometheus 通道，不在直连采集范围（collectScopeEnabled 只看
			// engine / kernel）内，只要 hostMetricsInTemplate 放行即可进模板。
			if isFrameworkMetric(key) || desc.Scope == "host" {
				continue
			}
			if _, ok := engineKeys[key]; !ok {
				t.Errorf("监控项「%s」的采集键 %s 不在直连采集范围内（scope=%s）", item.Name, key, desc.Scope)
			}
			seen[key] = struct{}{}
		}
	}
	// SQL Server 数据库层覆盖用户点名的核心运行指标，缺一项就等于该维度无处可看
	required := []string{
		MetricPerfConnCount, MetricPerfActiveConn, MetricPerfLongTrx,
		MetricEngBatchReqRate, MetricPerfTPS,
		MetricPerfLocks, MetricEngLockWaitRate, MetricEngDeadlocks,
		MetricPerfCacheHitRatio, MetricEngPageLifeExpectancy,
		MetricEngLogGrowths, MetricEngLogFlushWaitRate, MetricEngLogUsedPct,
		MetricEngTempdbUsedBytes, MetricEngDataFileBytes, MetricEngLogFileBytes,
		MetricEngIOReadBytesRate, MetricEngIOWriteBytesRate,
		MetricEngIOReadLatencyMs, MetricEngIOWriteLatencyMs,
		MetricEngWaitTimeMsRate, MetricEngSQLCPUPct, MetricEngSQLMemoryBytes,
	}
	for _, key := range required {
		if _, ok := seen[key]; !ok {
			t.Errorf("SQL Server 展示目录缺少核心指标 %s", key)
		}
	}
}

// TestTIDBDisplayLayoutStructure 锁定 TiDB 专属展示目录的结构契约：
//  1. 三个 Tab（基础指标 / 数据库指标 / 集群指标），与 MySQL 模板结构一致；
//  2. 基础指标与 MySQL 逐项一致（res.* 由 Prometheus 统一采集，同机同源，口径不能分叉）；
//  3. 不再包含任何 InnoDB 方言键（旧目录整目录复用 MySQL，TiDB 上全是恒空卡）；
//  4. TiDB 特有键（tidb_trx / data_lock_waits / cluster_info / tikv_store_status /
//     tidb_hot_regions）全部在指标字典中登记且 EngineScope 覆盖 TiDB，
//     并且能通过模板勾选校验（保证「目录有 = 模板可勾 = 采集可跑」三层对齐）。
func TestTIDBDisplayLayoutStructure(t *testing.T) {
	layout := MonitorDisplayLayoutFor("tidb", "")
	if layout == nil {
		t.Fatal("tidb 展示目录不应为空")
	}
	tabKeys := make([]string, 0, len(layout.Tabs))
	for _, tab := range layout.Tabs {
		tabKeys = append(tabKeys, tab.Key)
	}
	if len(tabKeys) != 3 || tabKeys[0] != LayerHost || tabKeys[1] != LayerEngine || tabKeys[2] != LayerKernel {
		t.Fatalf("tidb 应有三个 Tab（host/engine/kernel），实际 %v", tabKeys)
	}

	// 采集键全集
	keys := displayLayoutMetricKeys("tidb")
	keySet := map[string]bool{}
	for _, key := range keys {
		keySet[key] = true
	}

	// 基础指标与 MySQL 逐项对齐（res.* 同源同采集，单位与定义不能分叉）
	for _, key := range displayLayoutMetricKeys("mysql") {
		if !strings.HasPrefix(key, "res.") {
			continue
		}
		if !keySet[key] {
			t.Errorf("tidb 基础指标应与 MySQL 同源同口径，缺少主机层指标 %s", key)
		}
	}

	// InnoDB 方言键不得出现在 TiDB 目录（SHOW GLOBAL STATUS 没有这些计数器）
	forbidden := []string{
		MetricKernBufferDirtyPct, MetricKernBufferReadReq, MetricKernBufferWriteReq,
		MetricKernDataReadRate, MetricKernDataWrittenRate, MetricKernRowLockWaitsRate,
		MetricKernRedoWriteRate, MetricKernFlushRate, MetricKernSortRowsRate,
		MetricCapBufferPoolPct, MetricPerfCacheHitRatio, MetricEngSemiSyncStatus,
		MetricReplIORunning, MetricReplLagSeconds,
	}
	for _, key := range forbidden {
		if keySet[key] {
			t.Errorf("InnoDB/MySQL 方言键 %s 不应出现在 TiDB 展示目录中（采集不到，勾了也是恒空卡）", key)
		}
	}

	// TiDB 特有键：字典登记 + EngineScope 覆盖 TiDB + 模板勾选校验通过
	tidbOnly := []string{
		MetricEngTIDBTrxActive, MetricEngTIDBLockWaits,
		MetricKernTIDBNodeTotal, MetricKernTIDBNodeAbnormal,
		MetricKernTiKVStoreTotal, MetricKernTiKVStoreDown,
		MetricKernTiKVRegionTotal, MetricKernTiKVStoreUsedPct, MetricKernTiKVHotRegions,
	}
	for _, key := range tidbOnly {
		if !keySet[key] {
			t.Errorf("TiDB 特有指标 %s 应进入展示目录（进模板才能采到）", key)
		}
		desc, ok := metricDescriptorOf(key)
		if !ok {
			t.Errorf("TiDB 特有指标 %s 未在指标字典中登记", key)
			continue
		}
		if !engineInScope("tidb", desc.EngineScope) {
			t.Errorf("TiDB 特有指标 %s 的 EngineScope=%s 未覆盖 tidb", key, desc.EngineScope)
		}
	}
	if _, err := validateTemplateMetricKeys("tidb", tidbOnly); err != nil {
		t.Fatalf("TiDB 模板应接受全部 TiDB 特有指标: %v", err)
	}

	// 兼容 MySQL 协议的通用键：QPS / TPS / 连接 / 慢查询必须保留
	general := []string{MetricPerfQPS, MetricPerfTPS, MetricPerfConnCount, MetricPerfActiveConn,
		MetricEngSlowQueries, MetricAvailUp, MetricPerfLongTrx, MetricRiskNoPKTables}
	for _, key := range general {
		if !keySet[key] {
			t.Errorf("TiDB 兼容 MySQL 协议，通用指标 %s 不应从目录中移除", key)
		}
	}

	// 已退役键：TiDB 上结构性取不到数（见 unavailableMetricKeysByEngine），
	// 必须**不**在目录里 —— 它们是"勾了也永远是空卡"的那一类。
	for _, key := range unavailableMetricKeysByEngine["tidb"] {
		if keySet[key] {
			t.Errorf("已退役指标 %s 不应再出现在 TiDB 展示目录中（采集侧恒定 unsupported）", key)
		}
	}
}

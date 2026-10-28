package services

import "testing"

// 附件口径补齐的采集逻辑单测：不需要连库，直接验证采样点的 quality 与取值。
//
// 关注的边界：
//  1. 状态变量存在 → ok（值为 0 也是 ok，代表"真的很闲"而不是"没采到"）；
//  2. 状态变量缺失（低版本 / 非 InnoDB）→ unsupported 且带可读原因；
//  3. 指标不在 keys 里 → 不产出采样点（模板门控不被绕过）。
func TestMySQLStatusDerivedSamples(t *testing.T) {
	values := map[string]float64{
		"Com_select":                        1000,
		"Com_insert":                        0, // 真实为 0：必须标 ok 而不是 unsupported
		"Com_delete":                        20,
		"Com_update":                        300,
		"Com_replace":                       0,
		"Com_insert_select":                 5,
		"Com_replace_select":                0,
		"Select_scan":                       120,
		"Innodb_buffer_pool_pages_dirty":    42,
		"Innodb_buffer_pool_read_requests":  9000,
		"Innodb_buffer_pool_write_requests": 700,
		"Innodb_data_read":                  2048,
		"Innodb_data_written":               4096,
		"Innodb_row_lock_time":              1500,
		"Innodb_row_lock_time_avg":          25,
		"Innodb_row_lock_waits":             60,
		"Innodb_data_fsyncs":                330,
		"Sort_rows":                         880,
		"Innodb_rows_deleted":               10,
		"Innodb_rows_inserted":              20,
		"Innodb_rows_updated":               30,
		"Innodb_log_write_requests":         500,
		"Innodb_log_writes":                 120,
		"Innodb_os_log_fsyncs":              80,
		// 临时表与文件 / 表缓存：瞬时存量，直接上报原值
		"Created_tmp_disk_tables": 12,
		"Innodb_num_open_files":   45,
		"Open_files":              30,
		"Open_tables":             128,
	}
	rates := map[string]float64{}
	for key, value := range values {
		rates[key] = value
	}
	// rates 的键是指标键不是状态变量名，这里按指标键显式给一份速率
	rates = map[string]float64{
		MetricEngComSelect:         10,
		MetricEngComInsert:         0,
		MetricEngComDelete:         1,
		MetricEngComUpdate:         3,
		MetricEngComReplace:        0,
		MetricEngComInsertSelect:   0.5,
		MetricEngComReplaceSelect:  0,
		MetricEngSelectScan:        2,
		MetricKernBufferReadReq:    90,
		MetricKernBufferWriteReq:   7,
		MetricKernDataReadRate:     204.8,
		MetricKernDataWrittenRate:  409.6,
		MetricKernRowLockTimeRate:  15,
		MetricKernRowLockWaitsRate: 3,
		MetricKernDataFsyncsRate:   3.3,
		MetricKernSortRowsRate:     8.8,
		MetricKernRowsDeletedRate:  1,
		MetricKernRowsInsertedRate: 2,
		MetricKernRowsUpdatedRate:  3,
		MetricKernLogWriteReqRate:  5,
		MetricKernLogWritesRate:    1.2,
		MetricKernOsLogFsyncsRate:  0.8,
	}

	allKeys := []string{
		MetricEngComDelete, MetricEngComInsert, MetricEngComInsertSelect, MetricEngComReplace,
		MetricEngComReplaceSelect, MetricEngComSelect, MetricEngComUpdate, MetricEngSelectScan,
		MetricKernBufferDirtyPages, MetricKernBufferReadReq, MetricKernBufferWriteReq,
		MetricKernDataReadRate, MetricKernDataWrittenRate,
		MetricKernRowLockTimeMs, MetricKernRowLockTimeAvgMs, MetricKernRowLockTimeRate,
		MetricKernRowLockWaits, MetricKernRowLockWaitsRate,
		MetricKernRowsDeletedRate, MetricKernRowsInsertedRate, MetricKernRowsUpdatedRate,
		MetricKernDataFsyncsRate, MetricKernSortRowsRate,
		MetricKernLogWriteReqRate, MetricKernLogWritesRate, MetricKernOsLogFsyncsRate,
		MetricEngTmpDiskTables, MetricEngInnodbOpenFiles, MetricEngOpenFiles, MetricEngOpenTables,
	}

	// 1) 变量齐备：全部 ok
	builder := newSampleBuilder("direct")
	appendMySQLStatusDerivedSamples(builder, values, rates, allKeys, false)
	samples := builder.list()
	if len(samples) != len(allKeys) {
		t.Fatalf("采样点数量应等于指标数 %d，实际 %d", len(allKeys), len(samples))
	}
	for _, sample := range samples {
		if sample.Quality != QualityOK {
			t.Fatalf("%s 变量存在时应为 ok，实际 %s（%s）", sample.Key, sample.Quality, sample.Reason)
		}
	}
	byKey := map[string]MetricSample{}
	for _, sample := range samples {
		byKey[sample.Key] = sample
	}
	// 累计型上报原值，速率型上报 Δ/Δt，两者不能串味
	if byKey[MetricKernBufferDirtyPages].Value != 42 {
		t.Fatalf("脏页数应上报原值 42，实际 %v", byKey[MetricKernBufferDirtyPages].Value)
	}
	if byKey[MetricEngComSelect].Value != 10 {
		t.Fatalf("Com_select 应上报速率 10/s，实际 %v", byKey[MetricEngComSelect].Value)
	}
	if byKey[MetricEngComInsert].Value != 0 || byKey[MetricEngComInsert].Quality != QualityOK {
		t.Fatalf("值为 0 的计数器仍应标 ok（不是没采到），实际 %v/%s",
			byKey[MetricEngComInsert].Value, byKey[MetricEngComInsert].Quality)
	}
	if byKey[MetricKernRowLockTimeAvgMs].Value != 25 {
		t.Fatalf("平均等待时长应取状态变量 25ms，实际 %v", byKey[MetricKernRowLockTimeAvgMs].Value)
	}
	// 临时表 / 文件 / 表缓存：存量值原样上报，不做速率换算
	for key, want := range map[string]float64{
		MetricEngTmpDiskTables:   12,
		MetricEngInnodbOpenFiles: 45,
		MetricEngOpenFiles:       30,
		MetricEngOpenTables:      128,
	} {
		if byKey[key].Value != want {
			t.Fatalf("%s 应上报原值 %v，实际 %v", key, want, byKey[key].Value)
		}
	}

	// 2) 变量缺失：全部 unsupported，且不出现"值为 0 却标 ok"的假数据
	emptyValues := map[string]float64{}
	builder2 := newSampleBuilder("direct")
	appendMySQLStatusDerivedSamples(builder2, emptyValues, rates, allKeys, false)
	for _, sample := range builder2.list() {
		if sample.Quality != QualityUnsupported {
			t.Fatalf("%s 变量缺失时应为 unsupported，实际 %s（%s）", sample.Key, sample.Quality, sample.Reason)
		}
		if sample.Reason == "" {
			t.Fatalf("%s unsupported 必须给出可读原因", sample.Key)
		}
	}

	// 3) 模板门控：keys 里没有的指标不产出采样点
	builder3 := newSampleBuilder("direct")
	appendMySQLStatusDerivedSamples(builder3, values, rates, []string{MetricEngComSelect}, false)
	if len(builder3.list()) != 1 || builder3.list()[0].Key != MetricEngComSelect {
		t.Fatalf("未按模板门控裁剪采集范围，实际 %+v", builder3.list())
	}
}

// 平均等待时长在「无 Innodb_row_lock_time_avg 变量」时按 avg = time / waits 推导。
func TestMySQLRowLockTimeAvgFallback(t *testing.T) {
	values := map[string]float64{
		"Innodb_row_lock_time":  1500,
		"Innodb_row_lock_waits": 60,
	}
	builder := newSampleBuilder("direct")
	appendMySQLStatusDerivedSamples(builder, values, map[string]float64{},
		[]string{MetricKernRowLockTimeAvgMs}, false)
	samples := builder.list()
	if len(samples) != 1 || samples[0].Value != 25 {
		t.Fatalf("应按 time/waits=25 推导平均等待时长，实际 %+v", samples)
	}

	// waits 为 0 时无法计算：no_data 而不是除零得 NaN / 标 0
	noWait := map[string]float64{"Innodb_row_lock_time": 0, "Innodb_row_lock_waits": 0}
	builder2 := newSampleBuilder("direct")
	appendMySQLStatusDerivedSamples(builder2, noWait, map[string]float64{},
		[]string{MetricKernRowLockTimeAvgMs}, false)
	samples2 := builder2.list()
	if len(samples2) != 1 || samples2[0].Quality != QualityNoData {
		t.Fatalf("无等待记录时应为 no_data，实际 %+v", samples2)
	}
}

// 速率计算口径：(后值 - 前值) / 间隔秒数；计数器回绕（重启后归零）不应产生负值。
func TestMySQLCounterRateComputation(t *testing.T) {
	first := map[string]float64{MetricEngComSelect: 1000}
	directRateCache.compute("test-instance-rate", first, 100)
	second := map[string]float64{MetricEngComSelect: 1060}
	rates := directRateCache.compute("test-instance-rate", second, 100)
	if rates[MetricEngComSelect] <= 0 {
		t.Fatalf("速率应为正数（累计增长），实际 %v", rates[MetricEngComSelect])
	}
	// 实例重启后计数器归零：本轮不产出速率（回退到 当前值/uptime），不能出现负速率
	afterRestart := map[string]float64{MetricEngComSelect: 10}
	rates2 := directRateCache.compute("test-instance-rate", afterRestart, 100)
	if rates2[MetricEngComSelect] < 0 {
		t.Fatalf("计数器回绕不应产生负速率，实际 %v", rates2[MetricEngComSelect])
	}
}

package services

import (
	"testing"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
)

// 生产环境实测变量（centos01/02 均为此值），作为回归基准
func productionMySQLVars() map[string]string {
	return map[string]string{
		"version":             "8.0.43",
		"hostname":            "centos02",
		"datadir":             "/data/app/mysql8.0/data/",
		"log_output":          "FILE",
		"slow_query_log":      "ON",
		"slow_query_log_file": "/data/app/mysql8.0/logs/slow.log",
		"long_query_time":     "1.000000",
	}
}

func TestBuildSlowLogCapabilityProductionCase(t *testing.T) {
	capability := buildSlowLogCapability("ds-5btnl2r6h51f", productionMySQLVars())

	if capability.State != capabilityEnabledFile {
		t.Fatalf("期望 ENABLED_FILE，实际 %s", capability.State)
	}
	if !capability.Collectable() {
		t.Fatal("生产实例应判定为可采集")
	}
	// 这正是原实现猜错的路径：真实路径在 datadir 同级 logs 目录，不是 /var/log/mysql/slow.log
	if capability.ResolvedPath != "/data/app/mysql8.0/logs/slow.log" {
		t.Fatalf("路径解析错误：%s", capability.ResolvedPath)
	}
	if capability.LongQueryTimeMS != 1000 {
		t.Fatalf("long_query_time 换算错误：%d", capability.LongQueryTimeMS)
	}
	if !capability.FileCapable || capability.TableCapable {
		t.Fatalf("FILE 模式判定错误：file=%v table=%v", capability.FileCapable, capability.TableCapable)
	}
}

func TestBuildSlowLogCapabilityStates(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(map[string]string)
		wantState string
		file      bool
		table     bool
	}{
		{
			name:      "log_output=FILE 可采集",
			mutate:    func(v map[string]string) {},
			wantState: capabilityEnabledFile, file: true,
		},
		{
			name:      "log_output=TABLE 仅表格",
			mutate:    func(v map[string]string) { v["log_output"] = "TABLE" },
			wantState: capabilityEnabledTable, table: true,
		},
		{
			name:      "log_output=FILE,TABLE 按文件采集",
			mutate:    func(v map[string]string) { v["log_output"] = "FILE,TABLE" },
			wantState: capabilityEnabledFile, file: true, table: true,
		},
		{
			name:      "log_output=TABLE,FILE 顺序颠倒同样可采集",
			mutate:    func(v map[string]string) { v["log_output"] = "TABLE,FILE" },
			wantState: capabilityEnabledFile, file: true, table: true,
		},
		{
			name:      "log_output=NONE 无文件无表格",
			mutate:    func(v map[string]string) { v["log_output"] = "NONE" },
			wantState: capabilityEnabledTable,
		},
		{
			// 注意：log_output 与开关正交——慢日志关掉后文件方式配置仍在，
			// 所以 FileCapable 依然为 true，只是「不可采集」由开关决定。
			name:      "slow_query_log=OFF 未开启",
			mutate:    func(v map[string]string) { v["slow_query_log"] = "OFF" },
			wantState: capabilityDisabled, file: true,
		},
		{
			name:      "开关值小写也识别",
			mutate:    func(v map[string]string) { v["slow_query_log"] = "on" },
			wantState: capabilityEnabledFile, file: true,
		},
		{
			name: "MariaDB 用 log_slow_queries 变量名",
			mutate: func(v map[string]string) {
				delete(v, "slow_query_log")
				v["log_slow_queries"] = "ON"
				v["version"] = "10.6.12-MariaDB"
			},
			wantState: capabilityEnabledFile, file: true,
		},
		{
			name:      "缺少开关变量时判定为不可探测",
			mutate:    func(v map[string]string) { delete(v, "slow_query_log") },
			wantState: capabilityProbeFailed,
		},
		{
			name:      "开关值不可识别时判定为不可探测",
			mutate:    func(v map[string]string) { v["slow_query_log"] = "MAYBE" },
			wantState: capabilityProbeFailed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vars := productionMySQLVars()
			tc.mutate(vars)
			capability := buildSlowLogCapability("ds-test", vars)

			if capability.State != tc.wantState {
				t.Fatalf("状态期望 %s，实际 %s（probe_error=%s）", tc.wantState, capability.State, capability.ProbeError)
			}
			if capability.FileCapable != tc.file {
				t.Fatalf("FileCapable 期望 %v，实际 %v", tc.file, capability.FileCapable)
			}
			if capability.TableCapable != tc.table {
				t.Fatalf("TableCapable 期望 %v，实际 %v", tc.table, capability.TableCapable)
			}
		})
	}
}

func TestResolveSlowLogPath(t *testing.T) {
	cases := []struct {
		name     string
		hostname string
		rawFile  string
		datadir  string
		want     string
	}{
		{
			name:    "绝对路径原样使用",
			rawFile: "/data/app/mysql8.0/logs/slow.log",
			datadir: "/data/app/mysql8.0/data/",
			want:    "/data/app/mysql8.0/logs/slow.log",
		},
		{
			name:    "相对路径以 datadir 为基准（MySQL 自身的解析规则）",
			rawFile: "../logs/slow.log",
			datadir: "/data/app/mysql8.0/data",
			want:    "/data/app/mysql8.0/logs/slow.log",
		},
		{
			name:    "相对路径指向 datadir 内部",
			rawFile: "slow.log",
			datadir: "/var/lib/mysql",
			want:    "/var/lib/mysql/slow.log",
		},
		{
			name:     "变量为空时回退默认命名 datadir/hostname-slow.log",
			hostname: "centos02",
			rawFile:  "",
			datadir:  "/var/lib/mysql",
			want:     "/var/lib/mysql/centos02-slow.log",
		},
		{
			name:    "路径含冗余 .. 时归一化",
			rawFile: "/data/app/mysql8.0/data/../logs/slow.log",
			datadir: "/data/app/mysql8.0/data",
			want:    "/data/app/mysql8.0/logs/slow.log",
		},
		{
			name:    "datadir 缺失时保留相对路径本身",
			rawFile: "logs/slow.log",
			datadir: "",
			want:    "logs/slow.log",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSlowLogPath(tc.hostname, tc.rawFile, tc.datadir)
			if got != tc.want {
				t.Fatalf("期望 %s，实际 %s", tc.want, got)
			}
		})
	}
}

func TestDecideSlowLogCapabilityState(t *testing.T) {
	if got := decideSlowLogCapabilityState(&slowLogCapability{ProbeStatus: probeStatusConnectFailed, SlowLogEnabled: true, FileCapable: true}); got != capabilityProbeFailed {
		t.Fatalf("探测失败应判定为 PROBE_FAILED，实际 %s", got)
	}
	if got := decideSlowLogCapabilityState(&slowLogCapability{ProbeStatus: probeStatusOK, SlowLogEnabled: true, FileCapable: true}); got != capabilityEnabledFile {
		t.Fatalf("期望 ENABLED_FILE，实际 %s", got)
	}
}

func TestSlowLogCollectableGate(t *testing.T) {
	cases := []struct {
		name      string
		collector *performanceModels.InsightPerformanceSlowLogCollectors
		want      bool
	}{
		{
			name:      "nil 采集器不可采集",
			collector: nil,
			want:      false,
		},
		{
			name:      "人工停用优先",
			collector: &performanceModels.InsightPerformanceSlowLogCollectors{Enabled: false, SlowQueryLogEnabled: true, FileCapable: true},
			want:      false,
		},
		{
			name:      "未开启慢日志不可采集",
			collector: &performanceModels.InsightPerformanceSlowLogCollectors{Enabled: true, SlowQueryLogEnabled: false, FileCapable: true},
			want:      false,
		},
		{
			name:      "仅表格方式不可采集",
			collector: &performanceModels.InsightPerformanceSlowLogCollectors{Enabled: true, SlowQueryLogEnabled: true, FileCapable: false},
			want:      false,
		},
		{
			name:      "开启且文件方式可采集",
			collector: &performanceModels.InsightPerformanceSlowLogCollectors{Enabled: true, SlowQueryLogEnabled: true, FileCapable: true},
			want:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := slowLogCollectable(tc.collector); got != tc.want {
				t.Fatalf("期望 %v，实际 %v", tc.want, got)
			}
		})
	}
}

func TestSkippedCollectStatus(t *testing.T) {
	cases := []struct {
		state string
		want  string
	}{
		{capabilityDisabled, slowLogCollectStatusSkipped},
		{capabilityEnabledTable, slowLogCollectStatusSkipped},
		{capabilityProbeFailed, slowLogCollectStatusProbeFail},
		{capabilityUnknown, slowLogCollectStatusProbeFail},
		{"", slowLogCollectStatusProbeFail},
		{capabilityEnabledFile, ""},
	}
	for _, tc := range cases {
		got := skippedCollectStatus(&performanceModels.InsightPerformanceSlowLogCollectors{CapabilityState: tc.state})
		if got != tc.want {
			t.Fatalf("状态 %q 期望 %q，实际 %q", tc.state, tc.want, got)
		}
	}
}

func TestSlowLogProbeDue(t *testing.T) {
	now := time.Now()

	if !slowLogProbeDue(capabilityEnabledFile, commonModels.LocalTime(time.Time{})) {
		t.Fatal("从未探测过应立即探测")
	}
	if slowLogProbeDue(capabilityEnabledFile, commonModels.LocalTime(now.Add(-1*time.Minute))) {
		t.Fatal("可采集状态在 1 分钟内不应重复探测")
	}
	if !slowLogProbeDue(capabilityEnabledFile, commonModels.LocalTime(now.Add(-11*time.Minute))) {
		t.Fatal("可采集状态超过 10 分钟应重新探测")
	}
	// 不可采集状态用更短 TTL，便于用户改完配置后快速自愈
	if !slowLogProbeDue(capabilityDisabled, commonModels.LocalTime(now.Add(-3*time.Minute))) {
		t.Fatal("不可采集状态超过 2 分钟应重新探测")
	}
}

func TestApplySlowLogCapabilityKeepsLastConclusionOnProbeFailure(t *testing.T) {
	collector := &performanceModels.InsightPerformanceSlowLogCollectors{
		CapabilityState:     capabilityEnabledFile,
		SlowQueryLogEnabled: true,
		FileCapable:         true,
		ResolvedLogPath:     "/data/app/mysql8.0/logs/slow.log",
	}

	applySlowLogCapability(collector, &slowLogCapability{
		ProbeStatus: probeStatusConnectFailed,
		ProbeError:  "connection refused",
		State:       capabilityProbeFailed,
		ProbedAt:    time.Now(),
	})

	if collector.CapabilityState != capabilityEnabledFile {
		t.Fatalf("探测失败不应覆盖上次成功结论，实际 %s", collector.CapabilityState)
	}
	if !collector.FileCapable || !collector.SlowQueryLogEnabled {
		t.Fatal("探测失败不应清空可采集标记")
	}
	if collector.ProbeStatus != probeStatusConnectFailed {
		t.Fatal("探测失败状态应被记录")
	}
}

func TestApplySlowLogCapabilityMarksProbeFailedWhenNoHistory(t *testing.T) {
	collector := &performanceModels.InsightPerformanceSlowLogCollectors{CapabilityState: capabilityUnknown}

	applySlowLogCapability(collector, &slowLogCapability{
		ProbeStatus: probeStatusConnectFailed,
		ProbeError:  "connection refused",
		ProbedAt:    time.Now(),
	})

	if collector.CapabilityState != capabilityProbeFailed {
		t.Fatalf("无历史结论时探测失败应判定 PROBE_FAILED，实际 %s", collector.CapabilityState)
	}
}

func TestTruncateNotifySignature(t *testing.T) {
	short := "instance=ds-1 err=test"
	if got := truncateNotifySignature(short); got != short {
		t.Fatalf("短消息不应被截断，实际 %q", got)
	}

	long := ""
	for i := 0; i < 400; i++ {
		long += "错"
	}
	got := truncateNotifySignature(long)
	if n := len([]rune(got)); n != slowLogNotifySignatureMax {
		t.Fatalf("超长消息应截断到 %d 个字符，实际 %d", slowLogNotifySignatureMax, n)
	}
	// 必须按 rune 截断，否则多字节中文会被切坏
	if got == "" || !isValidUTF8(got) {
		t.Fatal("截断结果应是合法 UTF-8")
	}
}

func isValidUTF8(text string) bool {
	for _, r := range text {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// TestParseMySQLSlowLogTimeMySQL80 钉住本次线上发现的 bug：
// MySQL 8.0 的 `# Time: 2026-09-15T11:23:44.894523+08:00` 带数值时区偏移，
// Go 的 layout 必须写 Z07:00；写成字面量 Z 会全部解析失败并让 event_time 落成 NULL。
// 断言用绝对值（UTC）比较，避免依赖跑测试机器的本地时区。
func TestParseMySQLSlowLogTimeMySQL80(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantUTC string
	}{
		{"带微秒与时区偏移", "2026-09-15T11:23:44.894523+08:00", "2026-09-15T03:23:44Z"},
		{"带小数无偏移（UTC Z 写法）", "2026-09-15T03:23:44.894523Z", "2026-09-15T03:23:44Z"},
		{"无微秒带偏移", "2026-09-15T11:23:44+08:00", "2026-09-15T03:23:44Z"},
		{"负偏移", "2026-09-15T11:23:44.100000-05:00", "2026-09-15T16:23:44Z"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, err := parseMySQLSlowLogTime(tc.raw)
			if err != nil {
				t.Fatalf("解析 %q 失败：%v", tc.raw, err)
			}
			want, err := time.Parse(time.RFC3339, tc.wantUTC)
			if err != nil {
				t.Fatalf("测试用例期望值非法：%v", err)
			}
			got := ts.UTC().Truncate(time.Second)
			if !got.Equal(want) {
				t.Fatalf("解析 %q 得到绝对时刻 %s，期望 %s",
					tc.raw, got.Format(time.RFC3339), want.Format(time.RFC3339))
			}
		})
	}
}

// 旧格式不带时区，按本地时区解析——只校验日期时间分量。
func TestParseMySQLSlowLogTimeLegacyFormats(t *testing.T) {
	for _, raw := range []string{"260915 11:23:44", "2026-09-15 11:23:44"} {
		ts, err := parseMySQLSlowLogTime(raw)
		if err != nil {
			t.Fatalf("解析 %q 失败：%v", raw, err)
		}
		if ts.Year() != 2026 || ts.Month() != time.September || ts.Day() != 15 || ts.Hour() != 11 {
			t.Fatalf("解析 %q 结果错误：%v", raw, ts)
		}
	}
}

func TestParseMySQLSlowLogTimeRejectsGarbage(t *testing.T) {
	if _, err := parseMySQLSlowLogTime("not a timestamp"); err == nil {
		t.Fatal("非法时间应返回错误")
	}
}

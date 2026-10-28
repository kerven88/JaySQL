package services

// MySQL 采集会话化的行为测试。
//
// 为什么放在 monitor/services 而不是 das/dao：dao 包既有的 db_test.go 在 init 阶段
// 就要连一套真实测试库（缺库直接 panic），新测试放进去会被连带拖挂、根本跑不起来。
// 这里只覆盖**不需要真实数据库**就能断言的部分：
//   1. 连接类错误的判定口径（哪些错要作废整条会话、哪些只影响单条查询）；
//   2. 会话失效后的短路行为 —— 这是本次改造防"一条坏连接吃光整轮超时预算"的关键；
//   3. Close 幂等、nil 安全。
// "一轮只建一次连接"这一条必须在真机上用 Connections 状态变量的增量来验证，
// 见实现说明文档里的验证清单。

import (
	"context"
	"testing"
	"time"

	"github.com/simajiong/jaysql/internal/das/dao"
)

func TestIsConnectionErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil 不算", nil, false},
		{"连接被拒", errOf("dial tcp 127.0.0.1:3306: connect: connection refused"), true},
		{"管道破裂", errOf("write tcp 127.0.0.1:5->127.0.0.1:3306: broken pipe"), true},
		{"连接重置", errOf("read tcp: connection reset by peer"), true},
		{"无效连接", errOf("invalid connection"), true},
		{"IO 超时", errOf("i/o timeout"), true},
		{"EOF", errOf("unexpected EOF"), true},
		// 下面这些**不能**作废会话：它们只说明这一条 SQL 有问题，连接本身还是好的，
		// 误判会让同一轮剩下的指标全部变成 error，把"局部不支持"放大成"整体不可用"。
		{"SQL 语法错误", errOf("Error 1064: You have an error in your SQL syntax"), false},
		{"权限不足", errOf("Error 1142: SELECT command denied to user"), false},
		{"表不存在", errOf("Error 1146: Table 'x.y' doesn't exist"), false},
		{"未知列", errOf("Error 1054: Unknown column 'z' in 'field list'"), false},
		{"上下文取消", errOf("context canceled"), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dao.IsConnectionError(tc.err); got != tc.want {
				t.Fatalf("判定不符：期望 %v，实际 %v（err=%v）", tc.want, got, tc.err)
			}
		})
	}
}

// TestMySQLSessionShortCircuit 验证：一旦出现连接类错误，会话作废，
// 后续查询立刻返回缓存错误，而不是再等一次拨号/查询超时。
func TestMySQLSessionShortCircuit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 127.0.0.1:1 没有监听，拨号会立即被拒 —— 不需要任何真实数据库
	session, err := dao.OpenMySQLSession(dao.SessionConfig{
		User:     "monitor_probe",
		Password: "not-used",
		Host:     "127.0.0.1",
		Port:     1,
		Ctx:      ctx,
	})
	if err != nil {
		t.Fatalf("OpenMySQLSession 不应因地址不可达而失败（sql.Open 是惰性的）: %v", err)
	}
	defer func() {
		// Close 必须可重复调用且都不报错
		if err := session.Close(); err != nil {
			t.Fatalf("Close 失败: %v", err)
		}
		if err := session.Close(); err != nil {
			t.Fatalf("Close 应幂等，第二次返回: %v", err)
		}
	}()

	_, _, firstErr := session.Query("SELECT 1")
	if firstErr == nil {
		t.Fatal("不可达地址的查询必须失败")
	}
	if !dao.IsConnectionError(firstErr) {
		t.Fatalf("该错误应判定为连接类错误: %v", firstErr)
	}
	if session.BrokenErr() == nil {
		t.Fatal("出现连接类错误后，会话必须被标记为失效")
	}

	// 第二次查询应当短路：错误与第一次一致，且不再产生拨号/等待
	started := time.Now()
	_, _, secondErr := session.Query("SELECT 1")
	elapsed := time.Since(started)
	if secondErr == nil {
		t.Fatal("失效会话上的查询必须失败")
	}
	if secondErr.Error() != firstErr.Error() {
		t.Fatalf("短路应返回缓存的错误，实际: %v（首次: %v）", secondErr, firstErr)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("短路未生效，第二次查询耗时 %s（应远快于一次拨号超时）", elapsed)
	}
}

func TestMySQLSessionNilSafe(t *testing.T) {
	var session *dao.MySQLSession
	if _, _, err := session.Query("SELECT 1"); err == nil {
		t.Fatal("nil 会话查询必须报错，不能 panic")
	}
	if err := session.Close(); err != nil {
		t.Fatalf("nil 会话 Close 应安全返回 nil，实际: %v", err)
	}
	if session.BrokenErr() != nil {
		t.Fatal("nil 会话 BrokenErr 应为 nil")
	}
}

// TestQueryInnoDBMetricCountersBoundaries 边界：空名单与 nil 会话都不查询、不 panic。
func TestQueryInnoDBMetricCountersBoundaries(t *testing.T) {
	if got := queryInnoDBMetricCounters(nil, []string{"lock_deadlocks"}); len(got) != 0 {
		t.Fatalf("nil 会话应返回空结果，实际: %+v", got)
	}
	if got := queryInnoDBMetricCounters(nil, nil); len(got) != 0 {
		t.Fatalf("空名单应返回空结果，实际: %+v", got)
	}
}

func errOf(message string) error {
	return &fakeError{message: message}
}

type fakeError struct{ message string }

func (e *fakeError) Error() string { return e.message }

package services

// ============================================================================
// 用 map 更新列名的一致性回归测试
//
// 背景：GORM 对 Updates(map[string]any{...}) 的 key **不套用命名策略**，会原样拼进
// SQL；而结构体字段是按命名策略落列的。两者一旦不一致就报 Unknown column ——
// 偏偏"回填钉钉账号"这条路只吞 Warn，所以线上表现是"功能正常"，只是每次都白调一次
// 钉钉接口、绑定时间永远为空，极难被发现。
//
// 这里用 AutoMigrate 出来的真实表结构跑一遍生产用的更新 map，把这种不一致钉死在测试里。
// ============================================================================

import (
	"testing"
	"time"

	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/orders/models"
	usersModels "github.com/simajiong/jaysql/internal/users/models"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupUserSchemaTestDB(t *testing.T, migrateTargets ...any) *gorm.DB {
	t.Helper()
	dsn := "file:user_schema_" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if len(migrateTargets) == 0 {
		migrateTargets = []any{&usersModels.InsightUsers{}}
	}
	// 表结构由模型自己生成，测的就是"模型默认落列名"与"map key"是否一致。
	// 注意每次只迁移一个模型：这些模型都声明了 idx_updated_at，sqlite 的索引名是全库唯一的，
	// 同库迁移多个会报 already exists。
	for _, target := range migrateTargets {
		if err := db.AutoMigrate(target); err != nil {
			t.Fatalf("automigrate: %v", err)
		}
	}
	prevDB, prevLog := global.App.DB, global.App.Log
	global.App.DB = db
	global.App.Log = logrus.New()
	t.Cleanup(func() {
		global.App.DB = prevDB
		global.App.Log = prevLog
	})
	return db
}

// 钉钉绑定回填的 map key 必须与 AutoMigrate 出来的列名一致，且能真正写进去。
func TestDingTalkBindingUpdatesMatchRealSchema(t *testing.T) {
	db := setupUserSchemaTestDB(t)

	// 先确认列确实叫这个名字（防止有人改字段名却没同步 map key）
	for _, column := range []string{"ding_talk_user_id", "ding_talk_bound_at"} {
		if !db.Migrator().HasColumn(&usersModels.InsightUsers{}, column) {
			t.Fatalf("insight_users 缺少列 %q：字段命名或命名策略变了，请同步 dingTalkBindingUpdates", column)
		}
	}

	if err := db.Exec(`INSERT INTO insight_users (uid, username, password, email, mobile, is_superuser, is_active, is_staff, is_deleted)
		VALUES (1, 'alice', 'x', 'a@example.com', '13800000000', 0, 1, 0, 0)`).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := db.Model(&usersModels.InsightUsers{}).Where("uid = ?", 1).
		Updates(dingTalkBindingUpdates("ding-alice-01")).Error; err != nil {
		t.Fatalf("回填失败（列名与 map key 不一致）: %v", err)
	}

	var stored usersModels.InsightUsers
	if err := db.Where("uid = ?", 1).Take(&stored).Error; err != nil {
		t.Fatalf("回查失败: %v", err)
	}
	if stored.DingTalkUserID != "ding-alice-01" {
		t.Errorf("ding_talk_user_id=%q，期望 ding-alice-01", stored.DingTalkUserID)
	}
	if time.Time(stored.DingTalkBoundAt).IsZero() {
		t.Error("ding_talk_bound_at 应当被写入")
	}
}

// 审批令牌表的列名同样要能被 map 更新命中（回调路径写 status/consumed_at）。
func TestApprovalTokenUpdatesMatchRealSchema(t *testing.T) {
	// 单独一个库：与 InsightUsers 共用会撞 idx_updated_at
	db := setupUserSchemaTestDB(t, &models.InsightOrderApprovalTokens{})
	for _, column := range []string{"out_track_id", "status", "consumed_at", "expires_at", "approver_ding_talk_id"} {
		if !db.Migrator().HasColumn(&models.InsightOrderApprovalTokens{}, column) {
			t.Fatalf("insight_order_approval_tokens 缺少列 %q：请同步 dingtalk_callback.go 里的 map key", column)
		}
	}
}

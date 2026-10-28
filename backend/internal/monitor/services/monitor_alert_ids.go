package services

import (
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"gorm.io/gorm"
)

// ============================================================================
// 告警对外 ID（alm-xxxxxxxxxxxx）
//
// 为什么落库而不是前端现算：ID 的价值在于"可引用"——值班在群里贴一个 ID，
// 任何人都能在历史里精确检索到同一条告警。前端按 fingerprint 哈希现算的 ID
// 会随指纹口径变化而漂移，等于没有 ID。
//
// 为什么用普通索引而非唯一索引：AutoMigrate 加列时存量行全是空值，多行 ''
// 会让唯一索引直接创建失败。唯一性由生成侧保证：newAlertEventID 查库排重 +
// 重试；插入侧若真撞车（概率 ~36^-12，可忽略）会收到索引冲突之外的重复告警，
// 不影响告警主链路（upsertAlertEvent 已有独立错误处理）。
// ============================================================================

const (
	alertIDPrefix   = "alm-"
	alertIDRandLen  = 12
	alertIDMaxTries = 5
	alertIDAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// generateAlertIDRaw 生成 "alm-" + 12 位随机小写字母数字。
// 用 crypto/rand 而非 math/rand：ID 承担"对外可引用"的语义，可预测性哪怕只是观感问题也应避免。
func generateAlertIDRaw() string {
	buf := make([]byte, alertIDRandLen)
	max := big.NewInt(int64(len(alertIDAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand 失败极罕见；退化为确定值会让 ID 撞车，宁可报错上抛由调用方重试
			panic("generate alert id: " + err.Error())
		}
		buf[i] = alertIDAlphabet[n.Int64()]
	}
	return alertIDPrefix + string(buf)
}

// newAlertEventID 生成一个库内不存在的告警 ID（带排重与重试）。
func newAlertEventID(db *gorm.DB) string {
	for i := 0; i < alertIDMaxTries; i++ {
		id := generateAlertIDRaw()
		var count int64
		if err := db.Model(&monitorModels.InsightMonitorAlertEvents{}).
			Where("alert_id = ?", id).Count(&count).Error; err != nil {
			// 查重失败不阻塞告警主链路：直接返回新 ID，靠极低的碰撞概率兜底
			return id
		}
		if count == 0 {
			return id
		}
	}
	// 连续撞车 5 次在现实中不会发生；返回空串让事件不带 ID（页面显示 —），也不该 panic
	return ""
}

// backfillAlertEventIDs 给存量事件补 alert_id。幂等：只处理空值行。
// 在 EnsureMonitorSchema 里 AutoMigrate 加列之后调用。
func backfillAlertEventIDs(db *gorm.DB) {
	if db == nil {
		return
	}
	type idRow struct {
		ID uint64
	}
	rows := make([]idRow, 0, 64)
	if err := db.Model(&monitorModels.InsightMonitorAlertEvents{}).
		Where("alert_id = '' OR alert_id IS NULL").
		Order("id asc").
		Limit(10000). // 单次上限：存量回填是启动期动作，超大批量分轮消化
		Find(&rows).Error; err != nil {
		global.App.Log.Warnf("load alert events for id backfill failed: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	// 一次性载入现有 ID 做内存排重，避免逐行 Count（1 万次 Count 会拖慢启动）
	existing := make(map[string]struct{})
	ids := make([]string, 0, 128)
	if err := db.Model(&monitorModels.InsightMonitorAlertEvents{}).
		Where("alert_id <> ''").
		Pluck("alert_id", &ids).Error; err == nil {
		for _, v := range ids {
			existing[strings.TrimSpace(v)] = struct{}{}
		}
	}
	updated := 0
	for _, row := range rows {
		var alertID string
		for i := 0; i < alertIDMaxTries; i++ {
			candidate := generateAlertIDRaw()
			if _, taken := existing[candidate]; !taken {
				alertID = candidate
				existing[candidate] = struct{}{}
				break
			}
		}
		if alertID == "" {
			continue
		}
		if err := db.Model(&monitorModels.InsightMonitorAlertEvents{}).
			Where("id = ? AND (alert_id = '' OR alert_id IS NULL)", row.ID).
			Update("alert_id", alertID).Error; err != nil {
			global.App.Log.Warnf("backfill alert_id for event %d failed: %v", row.ID, err)
			continue
		}
		updated++
	}
	if updated > 0 {
		global.App.Log.Infof("backfilled alert_id for %d monitor alert events", updated)
	}
}

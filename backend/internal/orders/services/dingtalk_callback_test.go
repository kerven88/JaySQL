package services

// ============================================================================
// 钉钉审批集成的回归测试
//
// 分两部分：
//   - 纯逻辑：回调解析、卡片提示构造、日志打码、通知模式判定、令牌随机性；
//   - 带库：令牌的原子消费（这是安全关键路径，四道闸里的第三、四道）。
//
// 为什么令牌消费值得单测：它同时承担「防重放」「防冒用」「防过期」三件事，
// 任何一条失效都不会在界面上表现出来（审批人只会觉得"点了没反应"或
// "别人也能点我的卡片"），只能靠测试兜住。
// ============================================================================

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/orders/models"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ---------------------------------------------------------------- 纯逻辑部分

// 真实形态的卡片回调：content 是**嵌套的 JSON 字符串**（钉钉就是这么发的），
// 解析器必须能穿透两层才拿得到 actionIds。
func TestDecodeCardCallbackParsesNestedContent(t *testing.T) {
	data := `{
		"outTrackId": "track-abc-001",
		"userId": "manager8901",
		"corpId": "dingcorp123",
		"spaceType": "IM_ROBOT",
		"content": "{\"cardPrivateData\":{\"actionIds\":[\"approve\"],\"params\":{\"order_id\":\"bk-36x\",\"new_claimer\":\"\"}}}"
	}`

	callback, action, param, err := decodeCardCallback(data)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if callback.OutTrackID != "track-abc-001" {
		t.Errorf("outTrackId=%q，期望 track-abc-001", callback.OutTrackID)
	}
	if callback.UserID != "manager8901" {
		t.Errorf("userId=%q，期望 manager8901", callback.UserID)
	}
	if action != cardActionApprove {
		t.Errorf("action=%q，期望 %q", action, cardActionApprove)
	}
	if got := param("order_id"); got != "bk-36x" {
		t.Errorf("param(order_id)=%q，期望 bk-36x", got)
	}
	if got := param("new_claimer"); got != "" {
		t.Errorf("param(new_claimer)=%q，期望空", got)
	}
	// 不存在的 key 必须返回空串而不是 panic
	if got := param("not_exist"); got != "" {
		t.Errorf("param(not_exist)=%q，期望空", got)
	}
}

// 转交按钮：动作名有时被卡片模板放在 params.action 里而非 actionIds，
// 解析器要有兜底，否则转交在部分模板下点不动。
func TestDecodeCardCallbackFallsBackToParamsAction(t *testing.T) {
	data := `{
		"outTrackId": "track-abc-002",
		"userId": "manager8901",
		"content": "{\"cardPrivateData\":{\"actionIds\":[],\"params\":{\"action\":\"transfer\",\"new_claimer\":\"dba2\"}}}"
	}`
	_, action, param, err := decodeCardCallback(data)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if action != cardActionTransfer {
		t.Errorf("action=%q，期望 %q（应从 params.action 兜底）", action, cardActionTransfer)
	}
	if got := param(newClaimerParamKey); got != "dba2" {
		t.Errorf("new_claimer=%q，期望 dba2", got)
	}
}

// 缺 outTrackId 的回调必须被拒 —— 没有它就定位不到令牌，
// 放过去等于允许无凭据操作。
func TestDecodeCardCallbackRejectsMissingOutTrackID(t *testing.T) {
	if _, _, _, err := decodeCardCallback(`{"userId":"u1","content":"{}"}`); err == nil {
		t.Fatal("缺少 outTrackId 时应当报错")
	}
	if _, _, _, err := decodeCardCallback(`{ not json`); err == nil {
		t.Fatal("非法 JSON 时应当报错")
	}
}

// 卡片提示必须包含钉钉约定的 response.cardUpdateOptions，否则点了按钮卡片不会就地刷新。
func TestBuildCardNoticeShape(t *testing.T) {
	raw, err := buildCardNotice("已通过")
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	var payload struct {
		Response struct {
			CardUpdateOptions struct {
				UpdateCardDataByKey    bool `json:"updateCardDataByKey"`
				UpdatePrivateDataByKey bool `json:"updatePrivateDataByKey"`
			} `json:"cardUpdateOptions"`
			CardData struct {
				CardParamMap map[string]string `json:"cardParamMap"`
			} `json:"cardData"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("回包不是合法 JSON: %v", err)
	}
	if !payload.Response.CardUpdateOptions.UpdateCardDataByKey {
		t.Error("updateCardDataByKey 应为 true，否则卡片不会刷新")
	}
	if got := payload.Response.CardData.CardParamMap["status"]; got != "已通过" {
		t.Errorf("cardParamMap.status=%q，期望 已通过", got)
	}
}

// 日志里不得出现完整钉钉 userid（员工标识属于个人信息）。
func TestMaskDingTalkID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"manager8901", "man***901"},
		{"123456", "***"},
		{"1234567", "123***567"},
		{"", "***"},
	}
	for _, c := range cases {
		if got := maskDingTalkID(c.in); got != c.want {
			t.Errorf("maskDingTalkID(%q)=%q，期望 %q", c.in, got, c.want)
		}
	}
}

// 配了卡片模板才走卡片；否则降级为工作通知（点链接回平台）。
func TestNotifyModeOf(t *testing.T) {
	if got := notifyModeOf(map[string]any{}); got != "work_notice" {
		t.Errorf("无模板时 mode=%q，期望 work_notice", got)
	}
	if got := notifyModeOf(map[string]any{"card_template_id": ""}); got != "work_notice" {
		t.Errorf("模板为空串时 mode=%q，期望 work_notice", got)
	}
	if got := notifyModeOf(map[string]any{"card_template_id": "tpl-123"}); got != "card" {
		t.Errorf("有模板时 mode=%q，期望 card", got)
	}
	if got := notifyModeOf(map[string]any{"card_template_id": nil}); got != "work_notice" {
		t.Errorf("模板为 nil 时 mode=%q，期望 work_notice", got)
	}
}

// 令牌是防重放的唯一凭据，必须足够长且不可预测。
func TestNewApprovalTokenIsLongAndUnique(t *testing.T) {
	seen := make(map[string]struct{}, 256)
	for i := 0; i < 256; i++ {
		token := newApprovalToken()
		if len(token) != 64 {
			t.Fatalf("令牌长度=%d，期望 64（32 字节 hex）", len(token))
		}
		if _, dup := seen[token]; dup {
			t.Fatalf("令牌重复：%s", token)
		}
		seen[token] = struct{}{}
	}
}

// ---------------------------------------------------------------- 带库部分

const createApprovalTokenTestSchemaSQL = `
DROP TABLE IF EXISTS insight_order_approval_tokens;
CREATE TABLE insight_order_approval_tokens (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at            datetime,
    updated_at            datetime,
    token                 varchar(64) NOT NULL DEFAULT '',
    order_id              varchar(15) NOT NULL DEFAULT '',
    stage                 INTEGER NOT NULL DEFAULT 1,
    approver              varchar(32) NOT NULL DEFAULT '',
    approver_ding_talk_id varchar(64) NOT NULL DEFAULT '',
    channel               varchar(16) NOT NULL DEFAULT 'dingtalk',
    out_track_id          varchar(64) NOT NULL DEFAULT '',
    status                varchar(16) NOT NULL DEFAULT 'ISSUED',
    consumed_at           datetime,
    expires_at            datetime,
    remark                varchar(255) NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX uniq_approval_token ON insight_order_approval_tokens (token);`

func setupApprovalTokenTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:approval_token_" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.Exec(createApprovalTokenTestSchemaSQL).Error; err != nil {
		t.Fatalf("create schema: %v", err)
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

func seedApprovalToken(t *testing.T, db *gorm.DB, outTrackID, approver, dingID, status string, expiresAt time.Time) models.InsightOrderApprovalTokens {
	t.Helper()
	row := models.InsightOrderApprovalTokens{
		Token:              newApprovalToken(),
		OrderID:            "bk-36xqrhky0r5a",
		Stage:              1,
		Approver:           approver,
		ApproverDingTalkID: dingID,
		Channel:            "dingtalk",
		OutTrackID:         outTrackID,
		Status:             status,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if !expiresAt.IsZero() {
		if err := db.Model(&models.InsightOrderApprovalTokens{}).Where("id = ?", row.ID).
			Update("expires_at", expiresAt).Error; err != nil {
			t.Fatalf("set expires_at: %v", err)
		}
		row.ExpiresAt = commonModels.LocalTime(expiresAt)
	}
	return row
}

// happy path：卡片主人点自己那张卡片 → 令牌被消费，库里落到 CONSUMED。
func TestConsumeApprovalTokenSuccess(t *testing.T) {
	db := setupApprovalTokenTestDB(t)
	seedApprovalToken(t, db, "track-ok", "alice", "ding-alice", "ISSUED", time.Now().Add(time.Hour))

	row, err := consumeApprovalToken("track-ok", "ding-alice")
	if err != nil {
		t.Fatalf("应消费成功，实际报错: %v", err)
	}
	if row.Approver != "alice" || row.OrderID != "bk-36xqrhky0r5a" {
		t.Errorf("返回的令牌记录不对: %+v", row)
	}
	var stored models.InsightOrderApprovalTokens
	if err := db.Where("id = ?", row.ID).Take(&stored).Error; err != nil {
		t.Fatalf("回查失败: %v", err)
	}
	if stored.Status != "CONSUMED" {
		t.Errorf("库内 status=%q，期望 CONSUMED", stored.Status)
	}
	if time.Time(stored.ConsumedAt).IsZero() {
		t.Error("consumed_at 应当被写入")
	}
}

// 第三道闸：点击人必须是令牌绑定的那个人。卡片被转发/ID 泄露都不能用。
func TestConsumeApprovalTokenRejectsOtherClicker(t *testing.T) {
	db := setupApprovalTokenTestDB(t)
	seedApprovalToken(t, db, "track-other", "alice", "ding-alice", "ISSUED", time.Now().Add(time.Hour))

	if _, err := consumeApprovalToken("track-other", "ding-bob"); err == nil {
		t.Fatal("非绑定审批人点击时应当被拒绝")
	}
	var stored models.InsightOrderApprovalTokens
	if err := db.Where("out_track_id = ?", "track-other").Take(&stored).Error; err != nil {
		t.Fatalf("回查失败: %v", err)
	}
	if stored.Status != "ISSUED" {
		t.Errorf("被拒绝时不应消费令牌，库内 status=%q", stored.Status)
	}
}

// 第四道闸：重复点击（钉钉重推 / 用户狂点）必须幂等拒绝。
func TestConsumeApprovalTokenRejectsDoubleConsume(t *testing.T) {
	db := setupApprovalTokenTestDB(t)
	seedApprovalToken(t, db, "track-dup", "alice", "ding-alice", "ISSUED", time.Now().Add(time.Hour))

	if _, err := consumeApprovalToken("track-dup", "ding-alice"); err != nil {
		t.Fatalf("首次消费应当成功: %v", err)
	}
	_, err := consumeApprovalToken("track-dup", "ding-alice")
	if err == nil {
		t.Fatal("二次消费应当被拒绝")
	}
	if !strings.Contains(err.Error(), "重复") {
		t.Errorf("期望提示「重复操作」，实际: %v", err)
	}
}

// 已作废的令牌（工单被转交/重新提交）不能再用。
func TestConsumeApprovalTokenRejectsRevoked(t *testing.T) {
	db := setupApprovalTokenTestDB(t)
	seedApprovalToken(t, db, "track-revoked", "alice", "ding-alice", "REVOKED", time.Now().Add(time.Hour))

	if _, err := consumeApprovalToken("track-revoked", "ding-alice"); err == nil {
		t.Fatal("已作废令牌应当被拒绝")
	}
}

// 过期令牌不能再用 —— 否则一张早已飞走的卡片还能改变工单状态。
func TestConsumeApprovalTokenRejectsExpired(t *testing.T) {
	db := setupApprovalTokenTestDB(t)
	seedApprovalToken(t, db, "track-expired", "alice", "ding-alice", "ISSUED", time.Now().Add(-time.Minute))

	if _, err := consumeApprovalToken("track-expired", "ding-alice"); err == nil {
		t.Fatal("过期令牌应当被拒绝")
	}
}

// 找不到对应卡片时要有可读提示，而不是把 gorm 的原始错误抛给用户。
func TestConsumeApprovalTokenUnknownCard(t *testing.T) {
	setupApprovalTokenTestDB(t)
	_, err := consumeApprovalToken("track-not-exist", "ding-alice")
	if err == nil {
		t.Fatal("未知卡片应当报错")
	}
	if !strings.Contains(err.Error(), "找不到") {
		t.Errorf("期望可读提示，实际: %v", err)
	}
}

// 失败后把令牌放回 ISSUED，审批人才能重试（否则一次失败就永久锁死）。
func TestReleaseApprovalTokenRestoresIssued(t *testing.T) {
	db := setupApprovalTokenTestDB(t)
	row := seedApprovalToken(t, db, "track-release", "alice", "ding-alice", "ISSUED", time.Now().Add(time.Hour))

	consumed, err := consumeApprovalToken("track-release", "ding-alice")
	if err != nil {
		t.Fatalf("消费应当成功: %v", err)
	}
	if err := releaseApprovalToken(consumed); err != nil {
		t.Fatalf("放回令牌失败: %v", err)
	}
	var stored models.InsightOrderApprovalTokens
	if err := db.Where("id = ?", row.ID).Take(&stored).Error; err != nil {
		t.Fatalf("回查失败: %v", err)
	}
	if stored.Status != "ISSUED" {
		t.Errorf("放回后 status=%q，期望 ISSUED", stored.Status)
	}
	if !time.Time(stored.ConsumedAt).IsZero() {
		t.Error("放回后 consumed_at 应当清空")
	}
	// 放回后可以再次正常消费
	if _, err := consumeApprovalToken("track-release", "ding-alice"); err != nil {
		t.Errorf("放回后应可再次消费，实际报错: %v", err)
	}
}

// 回链基址可达性判定：0.0.0.0 这类通配地址派生出来的链接点不开，必须被识别出来，
// 否则一张卡片上的「查看详情」会直接跳到一个无法访问的地址。
func TestIsUnroutableHost(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"http://0.0.0.0:8083", true},
		{"http://[::]:8083", true},
		{"http://*:8083", true},
		{"/sql-workbench/orders", true},
		{"", true},
		{"http://127.0.0.1:8083", false},
		{"http://192.168.1.99:8083", false},
		{"https://goinsight.example.com", false},
	}
	for _, c := range cases {
		if got := isUnroutableHost(c.raw); got != c.want {
			t.Errorf("isUnroutableHost(%q)=%v，期望 %v", c.raw, got, c.want)
		}
	}
}

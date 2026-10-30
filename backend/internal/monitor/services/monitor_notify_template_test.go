package services

import (
	"strings"
	"testing"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"github.com/simajiong/jaysql/pkg/notifier"
	"github.com/simajiong/jaysql/pkg/secret"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNotifyMessageTemplate(t *testing.T) {
	payload := notifyTaskPayload{Title: "CPU alert", Markdown: "default", PlainText: "default", Meta: map[string]string{"instance_name": "db_01 **fake**", "threshold": "80", "observed_value": "92"}}
	got, err := applyNotifyMessageTemplate(payload, "**{{instance_name}}**: {{observed_value}} / {{threshold}}")
	if err != nil {
		t.Fatal(err)
	}
	if got.Markdown == "default" || !strings.Contains(got.PlainText, "db_01 **fake**") || !strings.Contains(got.PlainText, "92 / 80") {
		t.Fatalf("%+v", got)
	}
	if strings.Contains(got.Markdown, "**fake**") {
		t.Fatal("variable injected Markdown")
	}
	same, err := applyNotifyMessageTemplate(payload, "")
	if err != nil || same.Markdown != "default" {
		t.Fatal(same, err)
	}
	for _, bad := range []string{"{{unknown}}", "{{threshold", "{{.Threshold}}", strings.Repeat("x", 17000)} {
		if validateNotifyMessageTemplate(bad) == nil {
			t.Fatalf("accepted invalid template %q", bad[:min(len(bad), 40)])
		}
	}
}
func TestNotifyTemplatePreviewMatchesSend(t *testing.T) {
	template := "## {{title}}\n\n实例：{{instance_name}}\n\n当前值 {{observed_value}}，阈值 {{threshold}}\n\n[详情]({{url}})"
	preview, err := PreviewNotifyMessageTemplate(template, "firing")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.PlainText, "dev-mysql") || preview.PlainText == "" || len(preview.Variables) == 0 {
		t.Fatalf("%+v", preview)
	}
	if strings.Contains(preview.Markdown, "{{") {
		t.Fatal(preview.Markdown)
	}
	for _, trigger := range []string{"recovered", "acknowledged"} {
		if _, err := PreviewNotifyMessageTemplate(template, trigger); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := PreviewNotifyMessageTemplate(template, "oops"); err == nil {
		t.Fatal("invalid trigger accepted")
	}
}

func TestNotifyTemplatePersistenceAndTaskSnapshot(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&monitorModels.InsightMonitorNotifyChannels{}, &monitorModels.InsightMonitorNotifyPolicies{}, &monitorModels.InsightMonitorNotifyTasks{}} {
		if err := db.AutoMigrate(model); err != nil {
			t.Fatal(err)
		}
		db.Exec("DROP INDEX IF EXISTS idx_created_at")
		db.Exec("DROP INDEX IF EXISTS idx_updated_at")
	}
	oldDB, oldLog := global.App.DB, global.App.Log
	global.App.DB = db
	global.App.Log = logrus.New()
	t.Cleanup(func() { global.App.DB = oldDB; global.App.Log = oldLog })
	t.Setenv(secret.EnvKey, "12345678901234567890123456789012")
	if err := secret.LoadKey(); err != nil {
		t.Fatal(err)
	}
	form := NotifyChannelForm{Name: "Telegram test", Kind: "telegram", Config: map[string]any{"bot_token": "123:secret", "chat_id": "-1001", "message_template": "**{{instance_name}}**: {{threshold}}"}}
	id, err := SaveNotifyChannel(form, "tester")
	if err != nil {
		t.Fatal(err)
	}
	var stored monitorModels.InsightMonitorNotifyChannels
	if err := db.First(&stored, id).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Config), "123:secret") {
		t.Fatal("token stored in plaintext")
	}
	item := buildNotifyChannelItem(stored, nil)
	if item.Config["bot_token"] != MaskSentinel || item.Config["message_template"] != form.Config["message_template"] {
		t.Fatal(item.Config)
	}
	form.ID = id
	form.Config = item.Config
	if _, err := SaveNotifyChannel(form, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&stored, id).Error; err != nil {
		t.Fatal(err)
	}
	cfg, err := notifier.DecodeChannelConfig(stored.Config)
	if err != nil || cfg["bot_token"] != "123:secret" {
		t.Fatal(cfg, err)
	}
	payload := notifyTemplateSample("firing")
	event := monitorModels.InsightMonitorAlertEvents{Model: &commonModels.Model{ID: 1}}
	policy := monitorModels.InsightMonitorNotifyPolicies{Model: &commonModels.Model{ID: 1}}
	if !createNotifyTask(event, policy, stored, "firing", "group", "", "test", payload, "") {
		t.Fatal("task not created")
	}
	var task monitorModels.InsightMonitorNotifyTasks
	if err := db.First(&task).Error; err != nil {
		t.Fatal(err)
	}
	result, err := decodeNotifyPayload(task.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.PlainText, "dev-mysql") || !strings.Contains(result.PlainText, "80.00") {
		t.Fatal(result.PlainText)
	}
	form.Config["message_template"] = "changed {{instance_name}}"
	if _, err := SaveNotifyChannel(form, "tester"); err != nil {
		t.Fatal(err)
	}
	var unchanged monitorModels.InsightMonitorNotifyTasks
	db.First(&unchanged, task.ID)
	if string(unchanged.Payload) != string(task.Payload) {
		t.Fatal("queued snapshot changed")
	}
	form.Config["message_template"] = "{{unknown}}"
	if _, err := SaveNotifyChannel(form, "tester"); err == nil {
		t.Fatal("invalid template saved")
	}
}

func TestNotifyTemplateVariablesInCode(t *testing.T) {
	value := "db_01 `raw` **literal**"
	payload := notifyTaskPayload{Meta: map[string]string{"instance_name": value}}
	for _, template := range []string{"`{{instance_name}}`", "```text\n{{instance_name}}\n```", "~~~text\n{{instance_name}}\n~~~", "    {{instance_name}}", "> ```text\n> {{instance_name}}\n> ```", "- test\n\n  ```text\n  {{instance_name}}\n  ```"} {
		got, err := applyNotifyMessageTemplate(payload, template)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got.PlainText, value) {
			t.Fatalf("code variable changed: %q", got.PlainText)
		}
		rich, _ := notifier.RenderMessageMarkdown(got.Markdown)
		if strings.Contains(rich, "<b>literal</b>") {
			t.Fatal("code variable escaped its code context")
		}
	}
	payload.Meta["instance_name"] = "db\n```\n**literal**"
	got, err := applyNotifyMessageTemplate(payload, "```text\n{{instance_name}}\n```")
	if err != nil {
		t.Fatal(err)
	}
	rich, _ := notifier.RenderMessageMarkdown(got.Markdown)
	if strings.Contains(rich, "<b>literal</b>") {
		t.Fatal("variable closed code fence")
	}
}

package notifier

import (
	"errors"
	"fmt"
	"strings"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
	"gorm.io/gorm"
)

const runtimeNotifyConfigKeyDefault = "default"

// RuntimeNotifyConfig 平台默认通知配置（单例）。
//
// 只保留两类用途：
//  1. NoticeURL —— 站内链接前缀，用于在通知正文里拼出「工单/告警详情」的可点击地址；
//  2. Mail —— 邮件渠道的默认 SMTP 凭据，渠道实例留空时继承这里。
//
// 旧的两代 webhook 直发链路（企业微信/钉钉群机器人）已删除，不再从这里读取。
type RuntimeNotifyConfig struct {
	NoticeURL string
	Mail      struct {
		Enable   bool
		Username string
		Password string
		Host     string
		Port     int
	}
}

// LoadRuntimeNotifyConfig 读取平台默认通知配置（邮件密码自动解密）。
func LoadRuntimeNotifyConfig() (*RuntimeNotifyConfig, error) {
	if global.App.DB == nil {
		return nil, errors.New("database not initialized")
	}

	var settings commonModels.InsightNotifySettings
	if err := global.App.DB.Where("config_key = ?", runtimeNotifyConfigKeyDefault).Take(&settings).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("notify settings (%s) not found", runtimeNotifyConfigKeyDefault)
		}
		return nil, err
	}

	decryptedMailPassword := ""
	if strings.TrimSpace(settings.MailPassword) != "" {
		password, err := secret.Decrypt(settings.MailPassword)
		if err != nil {
			return nil, fmt.Errorf("decrypt notify mail password failed: %w", err)
		}
		decryptedMailPassword = password
	}

	cfg := &RuntimeNotifyConfig{
		NoticeURL: normalizeNoticeURL(settings.NoticeURL),
	}
	cfg.Mail.Enable = settings.MailEnable
	cfg.Mail.Username = strings.TrimSpace(settings.MailUsername)
	cfg.Mail.Password = decryptedMailPassword
	cfg.Mail.Host = strings.TrimSpace(settings.MailHost)
	cfg.Mail.Port = settings.MailPort

	return cfg, nil
}

// LoadNoticeURL 仅读取站内链接前缀，读不到时返回空串（调用方自行兜底）。
func LoadNoticeURL() (string, error) {
	cfg, err := LoadRuntimeNotifyConfig()
	if err != nil {
		return "", err
	}
	return cfg.NoticeURL, nil
}

func normalizeNoticeURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

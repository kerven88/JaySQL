package services

import (
	"errors"
	"fmt"
	netmail "net/mail"
	"net/url"
	"strings"

	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
	"gorm.io/gorm"
)

// ============================================================================
// 平台默认通知配置（insight_notify_settings 单例）
//
// 这里只保留两项「全局默认值」，供通知链路复用：
//  1. 通知地址前缀（notice_url）—— 告警/工单通知正文里拼「详情」链接用；
//  2. 邮件默认 SMTP 凭据 —— 邮件渠道实例把 host/port/username/password 留空时继承这里。
//
// 旧的企业微信 / 钉钉群机器人单例配置与直发链路已删除；渠道级的收发策略
// 一律在「通知渠道实例 + 通知策略」里配置，本页只提供全局兜底值。
// ============================================================================

const notifyDefaultsConfigKey = "default"

// NotifyDefaultsForm 平台默认通知配置保存表单。
type NotifyDefaultsForm struct {
	NoticeURL    string `json:"notice_url"`
	MailEnable   *bool  `json:"mail_enable"`
	MailUsername string `json:"mail_username"`
	// MailPassword 留空表示保持原值不变（接口从不回显明文/密文）
	MailPassword string `json:"mail_password"`
	MailHost     string `json:"mail_host"`
	MailPort     int    `json:"mail_port"`
}

// NotifyDefaultsItem 平台默认通知配置视图。
type NotifyDefaultsItem struct {
	NoticeURL    string `json:"notice_url"`
	MailEnable   bool   `json:"mail_enable"`
	MailUsername string `json:"mail_username"`
	MailHost     string `json:"mail_host"`
	MailPort     int    `json:"mail_port"`
	HasPassword  bool   `json:"has_password"`
}

// GetNotifyDefaults 读取平台默认通知配置（不存在时先播种默认行）。
func GetNotifyDefaults() (*NotifyDefaultsItem, error) {
	if global.App.DB == nil {
		return nil, errors.New("数据库未初始化")
	}
	record, err := loadOrSeedNotifyDefaults(global.App.DB)
	if err != nil {
		return nil, err
	}
	return &NotifyDefaultsItem{
		NoticeURL:    normalizeNotifyURL(record.NoticeURL),
		MailEnable:   record.MailEnable,
		MailUsername: strings.TrimSpace(record.MailUsername),
		MailHost:     strings.TrimSpace(record.MailHost),
		MailPort:     record.MailPort,
		HasPassword:  strings.TrimSpace(record.MailPassword) != "",
	}, nil
}

// SaveNotifyDefaults 保存平台默认通知配置。
func SaveNotifyDefaults(form NotifyDefaultsForm) error {
	if global.App.DB == nil {
		return errors.New("数据库未初始化")
	}
	record, err := loadOrSeedNotifyDefaults(global.App.DB)
	if err != nil {
		return err
	}

	form.NoticeURL = normalizeNotifyURL(form.NoticeURL)
	form.MailUsername = strings.TrimSpace(form.MailUsername)
	form.MailPassword = strings.TrimSpace(form.MailPassword)
	form.MailHost = strings.TrimSpace(form.MailHost)

	mailEnable := false
	if form.MailEnable != nil {
		mailEnable = *form.MailEnable
	}
	hasStoredPassword := strings.TrimSpace(record.MailPassword) != ""
	if err := validateNotifyDefaultsForSave(form, mailEnable, hasStoredPassword); err != nil {
		return err
	}

	encryptedPassword := record.MailPassword
	if form.MailPassword != "" {
		encryptedPassword, err = secret.Encrypt(form.MailPassword)
		if err != nil {
			return fmt.Errorf("邮件密码加密失败: %w", err)
		}
	}

	return global.App.DB.Model(&commonModels.InsightNotifySettings{}).
		Where("id = ?", record.ID).
		Updates(map[string]any{
			"notice_url":    form.NoticeURL,
			"mail_enable":   mailEnable,
			"mail_username": form.MailUsername,
			"mail_password": encryptedPassword,
			"mail_host":     form.MailHost,
			"mail_port":     form.MailPort,
		}).Error
}

func validateNotifyDefaultsForSave(form NotifyDefaultsForm, mailEnable bool, hasStoredPassword bool) error {
	if form.NoticeURL != "" && !isValidNotifyURL(form.NoticeURL) {
		return errors.New("通知地址格式不正确，仅支持 http/https")
	}
	if !mailEnable {
		return nil
	}
	if form.MailUsername == "" {
		return errors.New("启用邮件默认凭据时，发件账号不能为空")
	}
	if _, err := netmail.ParseAddress(form.MailUsername); err != nil {
		return errors.New("启用邮件默认凭据时，发件账号必须是合法邮箱")
	}
	if form.MailHost == "" {
		return errors.New("启用邮件默认凭据时，SMTP 主机不能为空")
	}
	if form.MailPort < 1 || form.MailPort > 65535 {
		return errors.New("SMTP 端口范围必须为 1-65535")
	}
	if form.MailPassword == "" && !hasStoredPassword {
		return errors.New("启用邮件默认凭据时，SMTP 密码不能为空")
	}
	return nil
}

// loadOrSeedNotifyDefaults 读取单例配置；缺失时按当前监听地址播种一行。
//
// 注意：播种只走 GORM 的 Create，不覆盖已有行；同时只有「查不到记录」才建新行，
// 其他错误（连接异常等）一律上抛，避免把数据库故障伪装成空配置。
func loadOrSeedNotifyDefaults(db *gorm.DB) (*commonModels.InsightNotifySettings, error) {
	var record commonModels.InsightNotifySettings
	err := db.Where("config_key = ?", notifyDefaultsConfigKey).First(&record).Error
	if err == nil {
		return &record, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	seed := &commonModels.InsightNotifySettings{
		ConfigKey: notifyDefaultsConfigKey,
		NoticeURL: defaultNoticeURLFromListenAddress(),
		MailPort:  465,
	}
	if err := db.Create(seed).Error; err != nil {
		return nil, err
	}
	return seed, nil
}

func normalizeNotifyURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func isValidNotifyURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return strings.TrimSpace(u.Host) != ""
}

func defaultNoticeURLFromListenAddress() string {
	address := strings.TrimSpace(global.App.Config.App.ListenAddress)
	if address == "" {
		return ""
	}
	if strings.HasPrefix(address, "http://") || strings.HasPrefix(address, "https://") {
		return strings.TrimRight(address, "/")
	}
	return "http://" + strings.TrimRight(address, "/")
}

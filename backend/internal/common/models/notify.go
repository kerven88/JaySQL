package models

// 平台默认通知配置（单例）。
//
// 现行用途：
//   - NoticeURL：站内链接前缀，用于把通知正文里的「详情」拼成可点击地址；
//   - Mail*：邮件渠道的默认 SMTP 凭据，渠道实例留空时继承这里。
//
// Wechat* / DingTalk*（Legacy* 语义）遗留字段：旧的「两代 webhook 直发链路」已删除
// （pkg/notifier 旧实现、消息通知页面与接口均不再存在），这两个字段只剩一个用途 ——
// 监控通知渠道的一次性迁移桥（internal/monitor/services/monitor_notify_bootstrap.go
// 在渠道表为空时，把历史上已启用的钉钉/企微群机器人配置搬迁成渠道实例）。
// 新代码不得再写入它们；后续版本确认无存量数据可迁移后，可连同 DB 列一并清理。
type InsightNotifySettings struct {
	*Model
	ConfigKey string `gorm:"type:varchar(32);not null;default:default;uniqueIndex:uniq_notify_config_key;comment:配置唯一键" json:"config_key"`

	NoticeURL string `gorm:"type:varchar(512);not null;default:'';comment:通知链接前缀" json:"notice_url"`

	// 遗留：仅供渠道迁移桥读取
	WechatEnable  bool   `gorm:"type:boolean;not null;default:false;comment:【遗留】是否启用企业微信通知" json:"-"`
	WechatWebhook string `gorm:"type:varchar(512);not null;default:'';comment:【遗留】企业微信Webhook" json:"-"`

	// 遗留：仅供渠道迁移桥读取
	DingTalkEnable   bool   `gorm:"type:boolean;not null;default:false;comment:【遗留】是否启用钉钉通知" json:"-"`
	DingTalkWebhook  string `gorm:"type:varchar(512);not null;default:'';comment:【遗留】钉钉Webhook" json:"-"`
	DingTalkKeywords string `gorm:"type:varchar(256);not null;default:'';comment:【遗留】钉钉关键字" json:"-"`

	MailEnable   bool   `gorm:"type:boolean;not null;default:false;comment:是否启用邮件通知" json:"mail_enable"`
	MailUsername string `gorm:"type:varchar(254);not null;default:'';comment:邮件用户名" json:"mail_username"`
	// 邮件密码是密文，不出接口：前端只回显"已配置"标记，修改时留空表示保持原值
	MailPassword string `gorm:"type:varchar(1024);not null;default:'';comment:邮件密码(加密)" json:"-"`
	MailHost     string `gorm:"type:varchar(255);not null;default:'';comment:邮件主机" json:"mail_host"`
	MailPort     int    `gorm:"type:int;not null;default:0;comment:邮件端口" json:"mail_port"`
}

func (InsightNotifySettings) TableName() string {
	return "insight_notify_settings"
}

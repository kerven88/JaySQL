package models

import (
	"github.com/simajiong/jaysql/pkg/utils"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type InsightCloudAccounts struct {
	*Model
	AccountID            string         `gorm:"type:char(15);not null;default:'';uniqueIndex:uniq_cloud_account_id;comment:云账号业务ID" json:"account_id"`
	Vendor               string         `gorm:"type:varchar(16);not null;default:'aliyun';index:idx_cloud_vendor;comment:云厂商 aliyun/huawei/tencent" json:"vendor"`
	DisplayName          string         `gorm:"type:varchar(128);not null;default:'';comment:显示名" json:"display_name"`
	AccessKeyID          string         `gorm:"type:varchar(256);not null;default:'';comment:AK ID (AES-256-GCM)" json:"-"`
	AccessKeySecret      string         `gorm:"type:varchar(512);not null;default:'';comment:AK Secret (AES-256-GCM)" json:"-"`
	ExtraConfig          datatypes.JSON `gorm:"type:json;null;default:null;comment:扩展配置:华为云projectId映射等" json:"extra_config"`
	Owner                string         `gorm:"type:varchar(64);not null;default:'';index:idx_cloud_owner;comment:创建人" json:"owner"`
	IsShared             bool           `gorm:"type:boolean;not null;default:false;comment:是否共享" json:"is_shared"`
	LastValidatedAt      LocalTime      `gorm:"comment:最近验证通过时间" json:"last_validated_at"`
	LastValidationStatus string         `gorm:"type:varchar(16);not null;default:'unknown';comment:验证状态 success/error/unknown" json:"last_validation_status"`
	LastValidationMsg    string         `gorm:"type:varchar(512);not null;default:'';comment:最近验证结果" json:"last_validation_msg"`
	Remark               string         `gorm:"type:varchar(256);not null;default:'';comment:备注" json:"remark"`
}

func (InsightCloudAccounts) TableName() string {
	return "insight_cloud_accounts"
}

func (u *InsightCloudAccounts) BeforeCreate(tx *gorm.DB) (err error) {
	if u.AccountID == "" {
		u.AccountID = utils.GenerateCloudAccountID()
	}
	return
}

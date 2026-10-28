package services

import (
	"errors"
	"fmt"
	"strings"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/pagination"
	"github.com/simajiong/jaysql/pkg/secret"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type phase2Error struct {
	Type string
}

func (e *phase2Error) Error() string {
	return fmt.Sprintf("不支持的存储目标类型 %s：当前版本仅支持 LOCAL_SERVER / OSS_ALIYUN / COS_TENCENT / OBS_HUAWEI / S3_MINIO。", e.Type)
}

const (
	ErrCodeStoragePhase2 = "STORAGE_TYPE_PHASE_2"
)

// isPhase2StorageType 判断存储目标类型是否为"当前版本不支持"。
// OSS/COS/OBS/MinIO 全部是 S3 兼容端点，统一由 S3Client 提供上传能力，
// 因此四种对象存储一并解除限制，不再区分"二期"。
func isPhase2StorageType(t string) bool {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case backupModels.StorageTargetTypeLocalServer,
		backupModels.StorageTargetTypeOSSAliyun,
		backupModels.StorageTargetTypeCOSTencent,
		backupModels.StorageTargetTypeS3Minio,
		backupModels.StorageTargetTypeOBSHuawei:
		return false
	}
	return true
}

func normalizeStorageTargetType(t string) (string, error) {
	up := strings.ToUpper(strings.TrimSpace(t))
	switch up {
	case backupModels.StorageTargetTypeLocalServer,
		backupModels.StorageTargetTypeOSSAliyun,
		backupModels.StorageTargetTypeS3Minio,
		backupModels.StorageTargetTypeCOSTencent,
		backupModels.StorageTargetTypeOBSHuawei:
		return up, nil
	}
	return "", fmt.Errorf("非法的存储目标类型：%s", t)
}

type GetStorageTargetsService struct {
	*backupForms.GetStorageTargetsForm
	C *gin.Context
}

func (s *GetStorageTargetsService) Run() (data any, total int64, err error) {
	type row struct {
		ID                    uint64                 `json:"id"`
		Name                  string                 `json:"name"`
		Type                  string                 `json:"type"`
		Host                  string                 `json:"host"`
		Port                  int                    `json:"port"`
		BasePath              string                 `json:"base_path"`
		SSHUser               string                 `json:"ssh_user"`
		SSHPasswordConfigured bool                   `json:"ssh_password_configured"`
		SSHPrivateKeyConfigured bool                 `json:"ssh_private_key_configured"`
		S3Bucket              string                 `json:"s3_bucket"`
		S3Endpoint            string                 `json:"s3_endpoint"`
		S3Region              string                 `json:"s3_region"`
		S3AccessKeyConfigured bool                   `json:"s3_access_key_configured"`
		S3SecretKeyConfigured bool                   `json:"s3_secret_key_configured"`
		S3ForcePathStyle      bool                   `json:"s3_force_path_style"`
		FingerprintConfirmed  bool                   `json:"fingerprint_confirmed"`
		ConnectionStatus      string                 `json:"connection_status"`
		LastPrecheckAt        commonModels.LocalTime `json:"last_precheck_at"`
		LastPrecheckResult    any                    `json:"last_precheck_result"`
		Remark                string                 `json:"remark"`
		CreatedAt             commonModels.LocalTime `json:"created_at"`
		UpdatedAt             commonModels.LocalTime `json:"updated_at"`
		CreatedBy             string                 `json:"created_by"`
		UpdatedBy             string                 `json:"updated_by"`
	}
	var list []row
	tx := global.App.DB.Table("insight_backup_storage_targets").Select(`
		id, name, type, host, port, base_path, ssh_user,
		case when ssh_password <> '' then true else false end as ssh_password_configured,
		case when ssh_private_key <> '' then true else false end as ssh_private_key_configured,
		s3_bucket, s3_endpoint, s3_region, s3_force_path_style,
		case when s3_access_key <> '' then true else false end as s3_access_key_configured,
		case when s3_secret_key <> '' then true else false end as s3_secret_key_configured,
		fingerprint_confirmed, connection_status, last_precheck_at, last_precheck_result,
		remark, created_at, updated_at, created_by, updated_by`).
		Where("deleted_at is null").
		Order("id desc")
	if s.Search != "" {
		like := "%" + s.Search + "%"
		tx = tx.Where("name like ? or host like ? or base_path like ? or s3_bucket like ? or remark like ?", like, like, like, like, like)
	}
	if s.Type != "" {
		tx = tx.Where("type = ?", s.Type)
	}
	if s.Status != "" {
		tx = tx.Where("connection_status = ?", s.Status)
	}
	total = pagination.Pager(&s.PaginationQ, tx, &list)
	return &list, total, nil
}

type GetStorageTargetDetailService struct {
	ID uint64
	C  *gin.Context
}

func (s *GetStorageTargetDetailService) Run() (any, error) {
	target, err := loadStorageTargetByID(s.ID)
	if err != nil {
		return nil, err
	}
	row := struct {
		ID                      uint64                 `json:"id"`
		Name                    string                 `json:"name"`
		Type                    string                 `json:"type"`
		Host                    string                 `json:"host"`
		Port                    int                    `json:"port"`
		BasePath                string                 `json:"base_path"`
		SSHUser                 string                 `json:"ssh_user"`
		SSHPasswordConfigured   bool                   `json:"ssh_password_configured"`
		SSHPrivateKeyConfigured bool                   `json:"ssh_private_key_configured"`
		SSHHostKeyFingerprint   string                 `json:"ssh_host_key_fingerprint"`
		FingerprintConfirmed    bool                   `json:"fingerprint_confirmed"`
		S3Endpoint              string                 `json:"s3_endpoint"`
		S3Region                string                 `json:"s3_region"`
		S3Bucket                string                 `json:"s3_bucket"`
		S3ForcePathStyle        bool                   `json:"s3_force_path_style"`
		S3AccessKeyConfigured   bool                   `json:"s3_access_key_configured"`
		S3SecretKeyConfigured   bool                   `json:"s3_secret_key_configured"`
		ConnectionStatus        string                 `json:"connection_status"`
		LastPrecheckAt          commonModels.LocalTime `json:"last_precheck_at"`
		LastPrecheckResult      any                    `json:"last_precheck_result"`
		Remark                  string                 `json:"remark"`
		CreatedAt               commonModels.LocalTime `json:"created_at"`
		UpdatedAt               commonModels.LocalTime `json:"updated_at"`
		CreatedBy               string                 `json:"created_by"`
		UpdatedBy               string                 `json:"updated_by"`
	}{
		ID:                      target.ID,
		Name:                    target.Name,
		Type:                    string(target.Type),
		Host:                    target.Host,
		Port:                    target.Port,
		BasePath:                target.BasePath,
		SSHUser:                 target.SSHUser,
		SSHPasswordConfigured:   strings.TrimSpace(target.SSHPassword) != "",
		SSHPrivateKeyConfigured: strings.TrimSpace(target.SSHPrivateKey) != "",
		SSHHostKeyFingerprint:   target.SSHHostKeyFingerprint,
		FingerprintConfirmed:    target.FingerprintConfirmed,
		S3Endpoint:              target.S3Endpoint,
		S3Region:                target.S3Region,
		S3Bucket:                target.S3Bucket,
		S3ForcePathStyle:        target.S3ForcePathStyle,
		S3AccessKeyConfigured:   strings.TrimSpace(target.S3AccessKey) != "",
		S3SecretKeyConfigured:   strings.TrimSpace(target.S3SecretKey) != "",
		ConnectionStatus:        string(target.ConnectionStatus),
		LastPrecheckAt:          target.LastPrecheckAt,
		LastPrecheckResult:      target.LastPrecheckResult,
		Remark:                  target.Remark,
		CreatedAt:               target.CreatedAt,
		UpdatedAt:               target.UpdatedAt,
		CreatedBy:               target.CreatedBy,
		UpdatedBy:               target.UpdatedBy,
	}
	return row, nil
}

type CreateStorageTargetService struct {
	*backupForms.CreateStorageTargetForm
	C *gin.Context
}

func (s *CreateStorageTargetService) Run() (*backupModels.InsightBackupStorageTargets, error) {
	tp, err := normalizeStorageTargetType(s.Type)
	if err != nil {
		return nil, err
	}
	if isPhase2StorageType(tp) {
		return nil, &phase2Error{Type: tp}
	}
	if err := validateStorageTargetForm(tp, true, s.Host, s.Port, s.BasePath, s.SSHUser, s.SSHPassword, s.SSHPrivateKey,
		s.S3Endpoint, s.S3Region, s.S3Bucket, s.S3AccessKey, s.S3SecretKey); err != nil {
		return nil, err
	}
	target := backupModels.InsightBackupStorageTargets{
		Name:             strings.TrimSpace(s.Name),
		Type:             commonModels.EnumType(tp),
		Host:             strings.TrimSpace(s.Host),
		Port:             s.Port,
		BasePath:         normalizeBasePath(s.BasePath),
		SSHUser:          strings.TrimSpace(s.SSHUser),
		SSHPassword:      "",
		SSHPrivateKey:    "",
		S3Endpoint:       strings.TrimSpace(s.S3Endpoint),
		S3Region:         strings.TrimSpace(s.S3Region),
		S3Bucket:         strings.TrimSpace(s.S3Bucket),
		S3AccessKey:      "",
		S3SecretKey:      "",
		ConnectionStatus: commonModels.EnumType(backupModels.StorageTargetConnStatusUnknown),
		FingerprintConfirmed: false,
		Remark:           strings.TrimSpace(s.Remark),
		CreatedBy:        currentUsername(s.C),
		UpdatedBy:        currentUsername(s.C),
	}
	if s.S3ForcePathStyle != nil {
		target.S3ForcePathStyle = *s.S3ForcePathStyle
	}
	if pw := strings.TrimSpace(s.SSHPassword); pw != "" {
		if enc, err := secret.Encrypt(pw); err != nil {
			return nil, fmt.Errorf("加密 SSH 密码失败: %w", err)
		} else {
			target.SSHPassword = enc
		}
	}
	if pk := strings.TrimSpace(s.SSHPrivateKey); pk != "" {
		if enc, err := secret.Encrypt(pk); err != nil {
			return nil, fmt.Errorf("加密 SSH 私钥失败: %w", err)
		} else {
			target.SSHPrivateKey = enc
		}
	}
	if ak := strings.TrimSpace(s.S3AccessKey); ak != "" {
		if enc, err := secret.Encrypt(ak); err != nil {
			return nil, fmt.Errorf("加密 S3 AccessKey 失败: %w", err)
		} else {
			target.S3AccessKey = enc
		}
	}
	if sk := strings.TrimSpace(s.S3SecretKey); sk != "" {
		if enc, err := secret.Encrypt(sk); err != nil {
			return nil, fmt.Errorf("加密 S3 SecretKey 失败: %w", err)
		} else {
			target.S3SecretKey = enc
		}
	}
	if err := global.App.DB.Create(&target).Error; err != nil {
		return nil, err
	}
	return &target, nil
}

type UpdateStorageTargetService struct {
	*backupForms.UpdateStorageTargetForm
	C  *gin.Context
	ID uint64
}

func (s *UpdateStorageTargetService) Run() error {
	tp, err := normalizeStorageTargetType(s.Type)
	if err != nil {
		return err
	}
	if isPhase2StorageType(tp) {
		return &phase2Error{Type: tp}
	}
	if err := validateStorageTargetForm(tp, false, s.Host, s.Port, s.BasePath, s.SSHUser, "", "",
		s.S3Endpoint, s.S3Region, s.S3Bucket, "", ""); err != nil {
		return err
	}
	target, err := loadStorageTargetByID(s.ID)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"name":         strings.TrimSpace(s.Name),
		"type":         tp,
		"host":         strings.TrimSpace(s.Host),
		"port":         s.Port,
		"base_path":    normalizeBasePath(s.BasePath),
		"ssh_user":     strings.TrimSpace(s.SSHUser),
		"s3_endpoint":  strings.TrimSpace(s.S3Endpoint),
		"s3_region":    strings.TrimSpace(s.S3Region),
		"s3_bucket":    strings.TrimSpace(s.S3Bucket),
		"remark":       strings.TrimSpace(s.Remark),
		"updated_by":   currentUsername(s.C),
		"updated_at":   commonModels.LocalTime(nowLocalTime()),
	}
	if s.S3ForcePathStyle != nil {
		updates["s3_force_path_style"] = *s.S3ForcePathStyle
	}
	if pw := strings.TrimSpace(s.SSHPassword); pw != "" {
		if enc, err := secret.Encrypt(pw); err != nil {
			return fmt.Errorf("加密 SSH 密码失败: %w", err)
		} else {
			updates["ssh_password"] = enc
		}
	}
	if pk := strings.TrimSpace(s.SSHPrivateKey); pk != "" {
		if enc, err := secret.Encrypt(pk); err != nil {
			return fmt.Errorf("加密 SSH 私钥失败: %w", err)
		} else {
			updates["ssh_private_key"] = enc
		}
	}
	if ak := strings.TrimSpace(s.S3AccessKey); ak != "" {
		if enc, err := secret.Encrypt(ak); err != nil {
			return fmt.Errorf("加密 S3 AccessKey 失败: %w", err)
		} else {
			updates["s3_access_key"] = enc
		}
	}
	if sk := strings.TrimSpace(s.S3SecretKey); sk != "" {
		if enc, err := secret.Encrypt(sk); err != nil {
			return fmt.Errorf("加密 S3 SecretKey 失败: %w", err)
		} else {
			updates["s3_secret_key"] = enc
		}
	}
	// 如果连接信息变更，重置指纹确认，强制重新连通测试
	if target.Host != updates["host"] || target.Port != updates["port"] ||
		target.SSHUser != updates["ssh_user"] ||
		(strings.TrimSpace(s.SSHPassword) != "" && pwDiffer(target.SSHPassword, updates["ssh_password"])) {
		updates["fingerprint_confirmed"] = false
		updates["ssh_host_key_fingerprint"] = ""
		updates["connection_status"] = backupModels.StorageTargetConnStatusUnknown
	}
	return global.App.DB.Model(&backupModels.InsightBackupStorageTargets{}).
		Where("id = ?", s.ID).
		Updates(updates).Error
}

func pwDiffer(aRaw, bOrNil any) bool {
	b, ok := bOrNil.(string)
	if !ok {
		return false
	}
	return aRaw != b
}

type DeleteStorageTargetService struct {
	C  *gin.Context
	ID uint64
}

func (s *DeleteStorageTargetService) Run() error {
	target, err := loadStorageTargetByID(s.ID)
	if err != nil {
		return err
	}
	var cnt int64
	if err := global.App.DB.Table("insight_backup_tasks").
		Where("storage_target_id = ? and deleted_at is null", target.ID).
		Count(&cnt).Error; err != nil {
		return err
	}
	if cnt > 0 {
		return fmt.Errorf("该存储目标关联 %d 个备份任务，无法删除，请先修改或删除对应任务后再操作", cnt)
	}
	return global.App.DB.Delete(target).Error
}

func loadStorageTargetByID(id uint64) (*backupModels.InsightBackupStorageTargets, error) {
	var t backupModels.InsightBackupStorageTargets
	if err := global.App.DB.Where("id = ?", id).First(&t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("存储目标不存在")
		}
		return nil, err
	}
	return &t, nil
}

func decryptStorageTargetCredentials(t *backupModels.InsightBackupStorageTargets) (sshPassword, sshPrivateKey, s3AK, s3SK string, err error) {
	if t == nil {
		return
	}
	if strings.TrimSpace(t.SSHPassword) != "" {
		if v, e := secret.Decrypt(t.SSHPassword); e == nil {
			sshPassword = v
		} else {
			err = fmt.Errorf("解密 SSH 密码失败: %w", e)
			return
		}
	}
	if strings.TrimSpace(t.SSHPrivateKey) != "" {
		if v, e := secret.Decrypt(t.SSHPrivateKey); e == nil {
			sshPrivateKey = v
		} else {
			err = fmt.Errorf("解密 SSH 私钥失败: %w", e)
			return
		}
	}
	if strings.TrimSpace(t.S3AccessKey) != "" {
		if v, e := secret.Decrypt(t.S3AccessKey); e == nil {
			s3AK = v
		} else {
			err = fmt.Errorf("解密 S3 AccessKey 失败: %w", e)
			return
		}
	}
	if strings.TrimSpace(t.S3SecretKey) != "" {
		if v, e := secret.Decrypt(t.S3SecretKey); e == nil {
			s3SK = v
		} else {
			err = fmt.Errorf("解密 S3 SecretKey 失败: %w", e)
			return
		}
	}
	return
}

func validateStorageTargetForm(tp string, isCreate bool, host string, port int, basePath, sshUser, sshPassword, sshPrivateKey,
	s3Endpoint, s3Region, s3Bucket, s3AK, s3SK string) error {
	switch tp {
	case backupModels.StorageTargetTypeLocalServer:
		if strings.TrimSpace(host) == "" {
			return fmt.Errorf("本地备份服务器地址不能为空")
		}
		if port <= 0 || port > 65535 {
			return fmt.Errorf("SSH 端口必须在 1~65535 之间")
		}
		if strings.TrimSpace(basePath) == "" {
			return fmt.Errorf("备份根路径不能为空")
		}
		if !strings.HasPrefix(strings.TrimSpace(basePath), "/") {
			return fmt.Errorf("备份根路径必须为绝对路径（以 / 开头）")
		}
		if strings.TrimSpace(sshUser) == "" {
			return fmt.Errorf("SSH 用户名不能为空")
		}
		// 编辑时允许凭据留空，表示保持原值不变；新增时必须填写至少一种凭据
		if isCreate && strings.TrimSpace(sshPassword) == "" && strings.TrimSpace(sshPrivateKey) == "" {
			return fmt.Errorf("SSH 密码或 SSH 私钥至少填写一项")
		}
		return nil
	case backupModels.StorageTargetTypeOSSAliyun,
		backupModels.StorageTargetTypeS3Minio,
		backupModels.StorageTargetTypeCOSTencent,
		backupModels.StorageTargetTypeOBSHuawei:
		if strings.TrimSpace(s3Endpoint) == "" {
			return fmt.Errorf("Endpoint 不能为空（阿里云示例：oss-cn-hangzhou.aliyuncs.com；腾讯云示例：cos.ap-beijing.myqcloud.com）")
		}
		if strings.TrimSpace(s3Region) == "" {
			return fmt.Errorf("Region 不能为空（阿里云示例：cn-hangzhou；腾讯云示例：ap-beijing）")
		}
		if strings.TrimSpace(s3Bucket) == "" {
			return fmt.Errorf("Bucket 名称不能为空")
		}
		// 编辑时允许 AK/SK 留空，表示保持原值不变；新增时必须填写
		if isCreate && (strings.TrimSpace(s3AK) == "" || strings.TrimSpace(s3SK) == "") {
			return fmt.Errorf("AccessKey / SecretKey 不能为空")
		}
		return nil
	}
	return fmt.Errorf("未知存储类型：%s", tp)
}

func normalizeBasePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	p = strings.ReplaceAll(p, "\\", "/")
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	p = strings.TrimRight(p, "/")
	if p == "" {
		return "/"
	}
	return p
}

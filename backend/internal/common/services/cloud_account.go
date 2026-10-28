package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/common/forms"
	"github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/common/services/cloud"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/secret"
	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ============ Validate ============

type AdminValidateCloudAccountService struct {
	*forms.AdminValidateCloudAccountForm
	C *gin.Context
}

type ValidateResult struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message"`
	Vendor  string `json:"vendor"`
}

func (s *AdminValidateCloudAccountService) Run() (*ValidateResult, error) {
	provider, err := cloud.NewProvider(s.Vendor, s.AkId, s.AkSecret, s.ExtraCfg)
	if err != nil {
		return &ValidateResult{Valid: false, Message: err.Error(), Vendor: s.Vendor}, nil
	}
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := provider.Validate(ctx); err != nil {
		return &ValidateResult{Valid: false, Message: err.Error(), Vendor: s.Vendor}, nil
	}
	return &ValidateResult{
		Valid:   true,
		Message: "凭据验证通过",
		Vendor:  s.Vendor,
	}, nil
}

// ============ Discover ============

type AdminDiscoverCloudInstancesService struct {
	*forms.AdminDiscoverCloudInstancesForm
	C *gin.Context
}

type DiscoverResult struct {
	Vendor    string               `json:"vendor"`
	Regions   []string             `json:"regions"`
	Instances []cloud.CloudInstance `json:"instances"`
	Total     int                  `json:"total"`
	Running   int                  `json:"running"`
}

func (s *AdminDiscoverCloudInstancesService) Run() (*DiscoverResult, error) {
	provider, err := cloud.NewProvider(s.Vendor, s.AkId, s.AkSecret, s.ExtraCfg)
	if err != nil {
		return nil, err
	}
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req := cloud.ListInstancesReq{
		Regions:     s.Regions,
		OnlyRunning: s.OnlyRunning,
	}
	instances, err := provider.ListInstances(ctx, req)
	if err != nil {
		return nil, err
	}
	runningCount := 0
	for _, it := range instances {
		if it.IsRunning {
			runningCount++
		}
	}
	return &DiscoverResult{
		Vendor:    s.Vendor,
		Regions:   s.Regions,
		Instances: instances,
		Total:     len(instances),
		Running:   runningCount,
	}, nil
}

// ============ Import ============

type AdminImportCloudInstancesService struct {
	*forms.AdminImportCloudInstancesForm
	C *gin.Context
}

type ImportItemResult struct {
	CloudInstanceId string `json:"cloud_instance_id"`
	DisplayName     string `json:"display_name"`
	Hostname        string `json:"hostname"`
	Port            int    `json:"port"`
	Status          string `json:"status"` // success / failed / skipped
	Message         string `json:"message"`
	InstanceId      string `json:"instance_id,omitempty"` // 成功时返回系统内 ds-xxx id
	ID              uint64 `json:"id,omitempty"`
}

type ImportResult struct {
	Total    int                `json:"total"`
	Success  int                `json:"success"`
	Failed   int                `json:"failed"`
	Skipped  int                `json:"skipped"`
	Items    []ImportItemResult `json:"items"`
}

func (s *AdminImportCloudInstancesService) Run() (*ImportResult, error) {
	if len(s.Instances) == 0 {
		return nil, fmt.Errorf("请选择至少一个待导入实例")
	}
	if s.SavedAccountId != "" && (s.AkId == "" || s.AkSecret == "") {
		_ = s.loadSavedCredentials()
	}
	result := &ImportResult{
		Total: len(s.Instances),
		Items: make([]ImportItemResult, 0, len(s.Instances)),
	}
	for i, inst := range s.Instances {
		_ = i
		user := strings.TrimSpace(inst.OverrideUser)
		pwd := inst.OverridePwd
		if user == "" {
			user = strings.TrimSpace(s.DefaultUser)
		}
		if pwd == "" {
			pwd = s.DefaultPwd
		}
		itemRes := ImportItemResult{
			CloudInstanceId: inst.CloudInstanceId,
			DisplayName:     inst.DisplayName,
			Hostname:        inst.Hostname,
			Port:            inst.Port,
		}
		mergedExtra := s.mergeExtraConfigFor(inst.DbType, s.ExtraConfig)
		existing, existErr := s.findExistingInstance(inst.Hostname, inst.Port)
		if existErr != nil && !errors.Is(existErr, gorm.ErrRecordNotFound) {
			itemRes.Status = "failed"
			itemRes.Message = "检查重复失败: " + existErr.Error()
			result.Failed++
			result.Items = append(result.Items, itemRes)
			continue
		}
		if existErr == nil {
			switch s.DuplicatePolicy {
			case "skip":
				itemRes.Status = "skipped"
				itemRes.Message = "实例已存在(hostname+port重复)，已跳过"
				itemRes.InstanceId = existing.InstanceID
				itemRes.ID = existing.ID
				result.Skipped++
				result.Items = append(result.Items, itemRes)
				continue
			case "abort":
				itemRes.Status = "failed"
				itemRes.Message = fmt.Sprintf("发现重复实例 %s:%d，导入已中止", inst.Hostname, inst.Port)
				result.Failed++
				result.Items = append(result.Items, itemRes)
				return result, fmt.Errorf(itemRes.Message)
			case "update":
				updated, updErr := s.updateExistingInstance(existing, inst, user, pwd, mergedExtra)
				if updErr != nil {
					itemRes.Status = "failed"
					itemRes.Message = "更新失败: " + updErr.Error()
					result.Failed++
				} else {
					itemRes.Status = "success"
					itemRes.InstanceId = updated.InstanceID
					itemRes.ID = updated.ID
					itemRes.Message = "已更新存在的实例配置"
					result.Success++
				}
				result.Items = append(result.Items, itemRes)
				continue
			}
		}
		created, createErr := s.createNewInstance(inst, user, pwd, mergedExtra)
		if createErr != nil {
			itemRes.Status = "failed"
			itemRes.Message = "创建失败: " + createErr.Error()
			result.Failed++
		} else {
			itemRes.Status = "success"
			itemRes.InstanceId = created.InstanceID
			itemRes.ID = created.ID
			itemRes.Message = "导入成功"
			result.Success++
		}
		result.Items = append(result.Items, itemRes)
	}
	return result, nil
}

func (s *AdminImportCloudInstancesService) loadSavedCredentials() error {
	var acct models.InsightCloudAccounts
	err := global.App.DB.Where("account_id = ?", s.SavedAccountId).Limit(1).Take(&acct).Error
	if err != nil {
		return err
	}
	akId, e1 := secret.Decrypt(acct.AccessKeyID)
	akSec, e2 := secret.Decrypt(acct.AccessKeySecret)
	if e1 != nil || e2 != nil {
		return fmt.Errorf("解密云账号凭据失败")
	}
	s.AkId = akId
	s.AkSecret = akSec
	return nil
}

func (s *AdminImportCloudInstancesService) findExistingInstance(hostname string, port int) (*models.InsightInstances, error) {
	var existing models.InsightInstances
	err := global.App.DB.Table("insight_instances").Where("hostname=? and port=?", hostname, port).Limit(1).Take(&existing).Error
	if err != nil {
		return nil, err
	}
	return &existing, nil
}

func (s *AdminImportCloudInstancesService) createNewInstance(inst forms.CloudInstanceToImport, user, pwd string, extra map[string]any) (*models.InsightInstances, error) {
	encryptPwd, err := secret.Encrypt(pwd)
	if err != nil {
		return nil, fmt.Errorf("密码加密失败: %w", err)
	}
	extraJson, err := normalizeInstanceExtraConfig(inst.DbType, extra, "create")
	if err != nil {
		return nil, err
	}
	db := &models.InsightInstances{
		InstanceID:      utils.GenerateInstanceID(),
		Hostname:        strings.TrimSpace(inst.Hostname),
		Port:            inst.Port,
		User:            user,
		Password:        encryptPwd,
		UseType:         models.EnumType("查询"),
		DbType:          models.EnumType(inst.DbType),
		ExtraConfig:     extraJson,
		Environment:     s.Environment,
		Remark:          strings.TrimSpace(inst.DisplayName),
		CreatedBy:       currentAuditUsername(s.C),
		LastCheckStatus: "unknown",
		// 云导入是部署形态与云厂商唯一可靠的来源：云 API 明确知道实例跑在谁家，
		// 而数据库侧永远探测不出来。此前这些信息取到了但落库时丢弃，概览只能显示 "—"。
		DeployProvider: "cloud",
		CloudVendor:    cloudVendorLabel(s.Vendor),
		// 引擎版本同样来自云 API，先落库作为初值，之后由采集回写持续校正。
		DbVersion: strings.TrimSpace(inst.EngineVersion),
	}
	if err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		// 【2026-10-15 收口】不再写默认数据授权：导入的实例同样需要显式授权才可访问。
		return tx.Create(db).Error
	}); err != nil {
		return nil, err
	}
	return db, nil
}

// cloudVendorLabel 云厂商标识 → 展示名。
// 存展示名而不是枚举值：概览直接把这个值显示给用户，再映射一次只会多一处漂移。
func cloudVendorLabel(vendor string) string {
	switch strings.ToLower(strings.TrimSpace(vendor)) {
	case "aliyun":
		return "阿里云"
	case "huawei":
		return "华为云"
	case "tencent":
		return "腾讯云"
	default:
		return strings.TrimSpace(vendor)
	}
}

func (s *AdminImportCloudInstancesService) updateExistingInstance(existing *models.InsightInstances, inst forms.CloudInstanceToImport, user, pwd string, extra map[string]any) (*models.InsightInstances, error) {
	updates := map[string]any{
		"hostname":    strings.TrimSpace(inst.Hostname),
		"port":        inst.Port,
		"user":        user,
		"db_type":     inst.DbType,
		"environment": s.Environment,
		"remark":      strings.TrimSpace(inst.DisplayName),
		// 与新建一致：云实例的三项画像事实随导入一并落库
		"deploy_provider": "cloud",
		"cloud_vendor":    cloudVendorLabel(s.Vendor),
	}
	// 版本只在云 API 确实给了的时候覆盖，避免用空串把已采到的版本清掉
	if version := strings.TrimSpace(inst.EngineVersion); version != "" {
		updates["db_version"] = version
	}
	if strings.TrimSpace(pwd) != "" {
		enc, err := secret.Encrypt(pwd)
		if err != nil {
			return nil, err
		}
		updates["password"] = enc
	}
	extraJson, err := normalizeInstanceExtraConfig(inst.DbType, extra, "update")
	if err != nil {
		return nil, err
	}
	updates["extra_config"] = extraJson
	if err := global.App.DB.Model(&models.InsightInstances{}).Where("id=?", existing.ID).Updates(updates).Error; err != nil {
		return nil, err
	}
	var updated models.InsightInstances
	_ = global.App.DB.Where("id=?", existing.ID).Take(&updated).Error
	return &updated, nil
}

func (s *AdminImportCloudInstancesService) mergeExtraConfigFor(dbType string, batchExtra map[string]any) map[string]any {
	out := make(map[string]any)
	for k, v := range batchExtra {
		out[k] = v
	}
	switch dbType {
	case "PostgreSQL":
		if _, ok := out["database"]; !ok {
			out["database"] = "postgres"
		}
		if _, ok := out["sslmode"]; !ok {
			out["sslmode"] = "disable"
		}
	case "MongoDB":
		if _, ok := out["auth_source"]; !ok {
			out["auth_source"] = "admin"
		}
	case "SQLServer":
		if _, ok := out["database"]; !ok {
			out["database"] = "master"
		}
	}
	return out
}

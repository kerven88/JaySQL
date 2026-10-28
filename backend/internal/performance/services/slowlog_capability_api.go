package services

import (
	"errors"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/performance/forms"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
	"gorm.io/gorm"
)

// GetSlowLogCapabilityListService 返回「支持范围内实例」的慢日志采集能力清单。
// 概览页与参数管理页共用这一份数据，避免各自拼装导致口径不一致。
type GetSlowLogCapabilityListService struct {
	Form     *forms.GetInstanceOptionsForm
	C        *gin.Context
	Username string
}

func (s *GetSlowLogCapabilityListService) Run() (map[string]any, error) {
	list, err := queryPerformanceInstances(s.Form.Search)
	if err != nil {
		return nil, err
	}

	rows := make([]instanceOptionRow, 0, len(list))
	for _, item := range list {
		if !item.Supported {
			continue
		}
		rows = append(rows, item)
	}

	counts, attention := splitSlowLogAttention(rows)
	return map[string]any{
		"instances": rows,
		"summary": map[string]any{
			"collectable":     counts[capabilityEnabledFile],
			"table_only":      counts[capabilityEnabledTable],
			"disabled":        counts[capabilityDisabled],
			"probe_failed":    counts[capabilityProbeFailed],
			"file_missing":    counts[capabilityFileMissing],
			"attention_total": len(attention),
		},
		"attention_instances": attention,
	}, nil
}

// ReprobeSlowLogCapabilityService 手动触发一次能力重探。
// 用户改完 MySQL 配置（开启慢日志 / 切到 FILE）后不必等 10 分钟 TTL，点一下即刻生效。
type ReprobeSlowLogCapabilityService struct {
	InstanceID string
	C          *gin.Context
	Username   string
}

func (s *ReprobeSlowLogCapabilityService) Run() (map[string]any, error) {
	instanceID := strings.TrimSpace(s.InstanceID)
	if instanceID == "" {
		return nil, fmt.Errorf("实例ID不能为空")
	}

	var instance commonModels.InsightInstances
	if err := global.App.DB.Where("instance_id = ?", instanceID).Take(&instance).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("实例不存在")
		}
		return nil, err
	}

	// 【2026-10-24 对齐工单口径】这里刻意不做数据访问授权校验。
	//
	// 与「提交工单」同一口径：orders 的 CreateOrder 只认功能权限 order.submit，
	// 有权限即可对任意实例提交工单，不叠加 insight_das_data_grants 判断。
	// 数据授权管的是「能不能碰这个库的数据」，功能权限管的是「能不能用这个能力」。
	// 性能分析同属平台级能力，沿用同一套：持有 performance.view 即视为可操作全部实例。
	//
	// 此前这里保留过一次 CheckDataAccess(DataActionDML)，理由是「重探会连目标库执行
	// SHOW VARIABLES，属实际访问」。但该理由自相矛盾：提交工单同样会连目标库执行
	// 用户 SQL 且同样不校验数据授权，只对重探设卡等于给出两套互相矛盾的规则。
	// 已按工单口径移除。

	// 把 last_probe_at 置空，让 ensureSlowLogCollector 的 TTL 判定必然通过，
	// 从而复用同一套探测 + 落库逻辑（避免出现第二份实现）。
	var collector performanceModels.InsightPerformanceSlowLogCollectors
	loadErr := global.App.DB.Where("instance_id = ?", instanceID).Take(&collector).Error
	if loadErr == nil {
		if err := global.App.DB.Model(&collector).Updates(map[string]any{"last_probe_at": nil}).Error; err != nil {
			return nil, err
		}
	} else if !errors.Is(loadErr, gorm.ErrRecordNotFound) {
		return nil, loadErr
	}

	refreshed, err := ensureSlowLogCollector(&instance)
	if err != nil {
		return nil, err
	}

	// 重置告警去重签名，让「重新检测」后的新状态能立即被记录一次
	_ = global.App.DB.Model(refreshed).Updates(map[string]any{"last_notified_error": ""}).Error
	refreshed.LastNotifiedError = ""

	state := strings.TrimSpace(refreshed.CapabilityState)
	return map[string]any{
		"instance_id":            refreshed.InstanceID,
		"capability_state":       state,
		"capability_label":       capabilityStateLabel(state),
		"collectable":            slowLogCollectable(refreshed),
		"slow_query_log_enabled": refreshed.SlowQueryLogEnabled,
		"log_output_mode":        refreshed.LogOutputMode,
		"resolved_log_path":      refreshed.ResolvedLogPath,
		"server_log_file":        refreshed.ServerLogFile,
		"long_query_time_ms":     refreshed.LongQueryTimeMS,
		"server_version":         refreshed.ServerVersion,
		"probe_status":           refreshed.ProbeStatus,
		"probe_error":            refreshed.ProbeError,
		"collect_status":         refreshed.CollectStatus,
		"cause":                  capabilityCause(state, refreshed.ProbeError),
		"advice":                 capabilityAdvice(state),
	}, nil
}

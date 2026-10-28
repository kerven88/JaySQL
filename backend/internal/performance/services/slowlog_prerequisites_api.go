package services

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	dbfamily "github.com/simajiong/jaysql/internal/common/dbfamily"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	performanceModels "github.com/simajiong/jaysql/internal/performance/models"
)

// ============================================================================
// 前提条件对外接口
//
// 前端「采集前提条件」面板的数据全部由后端下发，前端不硬编码文案：
//  1. 按引擎分的前提项清单（静态声明，见 slowlog_prerequisites.go）
//  2. 每个实例当前的探测结论（capability_state / probe_error / failure_code）
//  3. 失败码 → 前提项 Key 的关联关系（MatchPrerequisiteKeys）
//
// 这样以后新增引擎或调整开启方式，只改后端声明，前端自动跟着变。
// ============================================================================

type slowLogPrerequisiteItem struct {
	Key         string   `json:"key"`
	Title       string   `json:"title"`
	Kind        string   `json:"kind"`
	Requirement string   `json:"requirement"`
	HowTo       string   `json:"how_to"`
	Consequence string   `json:"consequence"`
	Verify      string   `json:"verify"`
	Codes       []string `json:"codes"`
}

type slowLogEnginePrerequisites struct {
	Family    string                    `json:"family"`
	DBType    string                    `json:"db_type"`
	Supported bool                      `json:"supported"`
	Reason    string                    `json:"reason,omitempty"`
	Items     []slowLogPrerequisiteItem `json:"items"`
}

// buildEnginePrerequisites 把声明库里的前提项转成可序列化的结构。
func buildEnginePrerequisites(dbType string, extraConfig []byte) slowLogEnginePrerequisites {
	family := dbfamily.EffectiveFamily(dbType, extraConfig)
	supported := slowLogEngineSupported(commonModels.EnumType(dbType), extraConfig)
	reason := ""
	if !supported {
		reason = buildUnsupportedCause(dbType)
	}

	items := SlowLogPrerequisites(dbType, extraConfig)
	out := make([]slowLogPrerequisiteItem, 0, len(items))
	for _, item := range items {
		out = append(out, slowLogPrerequisiteItem{
			Key:         item.Key,
			Title:       item.Title,
			Kind:        item.Kind,
			Requirement: item.Requirement,
			HowTo:       item.HowTo,
			Consequence: item.Consequence,
			Verify:      item.Verify,
			Codes:       item.Codes,
		})
	}
	return slowLogEnginePrerequisites{
		Family:    family,
		DBType:    dbType,
		Supported: supported,
		Reason:    reason,
		Items:     out,
	}
}

// GetSlowLogPrerequisitesService 返回慢 SQL 采集的前提条件清单。
//
// db_type 为空时返回三种引擎的全量清单（用于「采集说明」总览面板）；
// 指定了 db_type 时只返回该类型对应的清单（用于实例详情里的精准指引）。
type GetSlowLogPrerequisitesService struct {
	DBType string
	C      *gin.Context
}

func (s *GetSlowLogPrerequisitesService) Run() (map[string]any, error) {
	requested := strings.TrimSpace(s.DBType)

	engines := make([]slowLogEnginePrerequisites, 0, 3)
	if requested == "" {
		for _, dbType := range supportedDBTypes {
			engines = append(engines, buildEnginePrerequisites(dbType, nil))
		}
		return map[string]any{
			"supported_db_types": supportedDBTypes,
			"engines":            engines,
		}, nil
	}

	engine := buildEnginePrerequisites(requested, nil)
	if !engine.Supported {
		// 未知引擎：仍然返回 200 + supported=false，让前端展示"暂不支持"及原因，
		// 而不是抛一个 400——这只是"不在支持列表里"，不是用户请求出错。
		return map[string]any{
			"supported_db_types": supportedDBTypes,
			"engines":            []slowLogEnginePrerequisites{engine},
			"supported":          false,
		}, nil
	}
	return map[string]any{
		"supported_db_types": supportedDBTypes,
		"engines":            []slowLogEnginePrerequisites{engine},
		"supported":          true,
	}, nil
}

// slowLogInstanceDiagnosis 单个实例的诊断结论，供列表页状态列直接渲染。
type slowLogInstanceDiagnosis struct {
	InstanceID       string   `json:"instance_id"`
	DBType           string   `json:"db_type"`
	DBFamily         string   `json:"db_family"`
	DbVersion        string   `json:"db_version,omitempty"`
	CapabilityState  string   `json:"capability_state"`
	CapabilityLabel  string   `json:"capability_label"`
	Source           string   `json:"source,omitempty"`
	FailureCode      string   `json:"failure_code,omitempty"`
	ErrorText        string   `json:"error_text,omitempty"`
	Cause            string   `json:"cause,omitempty"`
	Advice           string   `json:"advice,omitempty"`
	PrerequisiteKeys []string `json:"prerequisite_keys"`
	Supported        bool     `json:"supported"`
	SupportedReason  string   `json:"supported_reason,omitempty"`
}

// GetSlowLogDiagnosisService 返回所有可见实例的采集诊断结论。
//
// 与 /slowlog-capability 的区别：那边是「能力枚举」，这边是「带前提项关联的诊断」，
// 前端状态列直接拿 PrerequisiteKeys 去高亮对应的前提项。
type GetSlowLogDiagnosisService struct {
	Username string
}

func (s *GetSlowLogDiagnosisService) Run() (map[string]any, error) {
	instances, err := queryPerformanceInstances("")
	if err != nil {
		return nil, err
	}

	rows := make([]slowLogInstanceDiagnosis, 0, len(instances))
	for _, item := range instances {
		state := strings.TrimSpace(item.CapabilityState)
		if state == "" {
			state = capabilityUnknown
		}
		code := strings.TrimSpace(item.FailureCode)
		keys := MatchPrerequisiteKeys(item.DBType, []byte(item.ExtraConfig), code)
		if keys == nil {
			keys = []string{}
		}
		supported := item.Supported
		reason := ""
		if !supported {
			reason = buildUnsupportedCause(item.DBType)
		}
		rows = append(rows, slowLogInstanceDiagnosis{
			InstanceID:       item.InstanceID,
			DBType:           item.DBType,
			DBFamily:         dbfamily.EffectiveFamily(item.DBType, []byte(item.ExtraConfig)),
			DbVersion:        item.DbVersion,
			CapabilityState:  state,
			CapabilityLabel:  capabilityStateLabel(state),
			Source:           item.Source,
			FailureCode:      code,
			ErrorText:        strings.TrimSpace(item.ProbeError),
			Cause:            capabilityCause(state, item.ProbeError),
			Advice:           capabilityAdvice(state),
			PrerequisiteKeys: keys,
			Supported:        supported,
			SupportedReason:  reason,
		})
	}

	return map[string]any{
		"supported_db_types": supportedDBTypes,
		"instances":          rows,
	}, nil
}

// GetSlowLogInstancePrerequisitesService 返回某个实例所属引擎的前提清单 + 它自己的失败关联。
//
// 用于实例详情抽屉：既能看到「这类库到底要开什么」，也能看到「这台具体卡在哪一项」。
type GetSlowLogInstancePrerequisitesService struct {
	InstanceID string
	Username   string
}

func (s *GetSlowLogInstancePrerequisitesService) Run() (map[string]any, error) {
	instanceID := strings.TrimSpace(s.InstanceID)
	if instanceID == "" {
		return nil, fmt.Errorf("实例ID不能为空")
	}

	var instance commonModels.InsightInstances
	if err := global.App.DB.Where("instance_id = ?", instanceID).Take(&instance).Error; err != nil {
		return nil, err
	}

	var collector performanceModels.InsightPerformanceSlowLogCollectors
	state, code, errorText, source := "", "", "", ""
	if err := global.App.DB.Where("instance_id = ?", instanceID).Take(&collector).Error; err == nil {
		state = strings.TrimSpace(collector.CapabilityState)
		code = strings.TrimSpace(collector.FailureCode)
		errorText = strings.TrimSpace(collector.ProbeError)
		source = strings.TrimSpace(collector.Source)
	}
	if state == "" {
		state = capabilityUnknown
	}

	keys := MatchPrerequisiteKeys(string(instance.DbType), instance.ExtraConfig, code)
	if keys == nil {
		keys = []string{}
	}

	engine := buildEnginePrerequisites(string(instance.DbType), instance.ExtraConfig)
	return map[string]any{
		"instance_id":      instanceID,
		"db_type":          strings.TrimSpace(string(instance.DbType)),
		"db_family":        engine.Family,
		"db_version":       strings.TrimSpace(instance.DbVersion),
		"capability_state": state,
		"capability_label": capabilityStateLabel(state),
		"source":           source,
		"failure_code":     code,
		"error_text":       errorText,
		"cause":            capabilityCause(state, collector.ProbeError),
		"advice":           capabilityAdvice(state),
		"prereq_keys":      keys,
		"supported":        engine.Supported,
		"prerequisites":    engine.Items,
	}, nil
}

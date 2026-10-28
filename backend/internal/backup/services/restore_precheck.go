package services

import (
	"encoding/json"
	"fmt"
	"strings"

	backupForms "github.com/simajiong/jaysql/internal/backup/forms"
	backupModels "github.com/simajiong/jaysql/internal/backup/models"

	"github.com/gin-gonic/gin"
)

// PrecheckRestoreRunService 恢复向导第 3 步的统一预检入口。
//
// 设计要点：
//  1. 纯计算、零副作用 —— 不写 insight_restore_runs。向导此前靠
//     create(mode=plan) + delete 做预检，会在列表留下幽灵任务，本接口即为替代。
//  2. 按备份集驱动类型分流到既有的三条预检链路（MySQL XtraBackup / PG pgBackRest / 逻辑），
//     前端始终只发一份 payload，不必自己判断走哪个接口。
//  3. 逻辑分支恒按 PLAN 模式计算：不校验 execute_confirmed，
//     这两项属于「确认并执行」步骤的提交态校验，不该在预检查阶段拦人。
//  4. 只读输出：预检结果是向导第 3 步「恢复预检查」的唯一数据源，前端不再做二次判定。
type PrecheckRestoreRunService struct {
	*backupForms.PrecheckRestoreRunForm
	C *gin.Context
}

func (s *PrecheckRestoreRunService) Run() (any, error) {
	if s == nil || s.PrecheckRestoreRunForm == nil {
		return nil, fmt.Errorf("恢复预检请求体为空")
	}
	set, err := LoadBackupSet(s.BackupSetID)
	if err != nil {
		return nil, err
	}
	switch {
	case isMySQLPhysicalDriverType(string(set.DriverType)):
		return s.precheckMySQLPhysical(set)
	case isPostgresPhysicalDriverType(string(set.DriverType)):
		return s.precheckPGPhysical(set)
	}
	return s.precheckLogical(set)
}

func (s *PrecheckRestoreRunService) precheckLogical(set *backupModels.InsightBackupSets) (any, error) {
	input := buildLogicalRestorePlanInputFromCreateForm(&backupForms.CreateRestoreRunForm{
		BackupSetID:          s.BackupSetID,
		TargetInstanceID:     s.TargetInstanceID,
		TargetDBName:         s.TargetDBName,
		TargetDBSuffix:       s.TargetDBSuffix,
		RestoreMode:          s.RestoreMode,
		SelectedDatabases:    s.SelectedDatabases,
		SelectedSchemas:      s.SelectedSchemas,
		SelectedTables:       s.SelectedTables,
		CloseBinlog:          s.CloseBinlog,
		CleanIfExists:        s.CleanIfExists,
		ForeignKeyChecks:     s.ForeignKeyChecks,
		ExitOnError:          s.ExitOnError,
		ObjectScope:          s.ObjectScope,
		DBMappings:           s.DBMappings,
		SchemaConflictPolicy: s.SchemaConflictPolicy,
		DataConflictPolicy:   s.DataConflictPolicy,
		RestoreSchema:        s.RestoreSchema,
		RestoreData:          s.RestoreData,
		Mode:                 restoreExecutionModePlan,
	})
	// 双保险：预检一律按计划模式，避免前端误传 execute 导致预检阶段就报「缺 execute_confirmed」。
	input.Mode = restoreExecutionModePlan

	plan, err := prepareLogicalRestorePlan(input, s.C)
	if err != nil {
		return nil, err
	}
	var pv struct {
		Summary   map[string]any `json:"summary"`
		Items     []any          `json:"items"`
		Resolved  map[string]any `json:"resolved"`
		Plan      map[string]any `json:"plan"`
		RiskLevel string         `json:"risk_level"`
	}
	if len(plan.PrecheckJSON) > 0 {
		_ = json.Unmarshal(plan.PrecheckJSON, &pv)
	}
	passed := true
	if pv.Summary != nil {
		if v, ok := pv.Summary["passed_all"].(bool); ok {
			passed = v
		}
	}
	return gin.H{
		"backup_set_id":              set.ID,
		"db_type":                    strings.TrimSpace(string(set.DBType)),
		"driver_type":                strings.TrimSpace(string(set.DriverType)),
		"restore_kind":               restoreKindLogical,
		"restore_mode":               plan.Options.RestoreMode,
		"schema_conflict_policy":     plan.Options.SchemaConflictPolicy,
		"data_conflict_policy":       plan.Options.DataConflictPolicy,
		"passed":                     passed,
		"summary":                    pv.Summary,
		"items":                      pv.Items,
		"resolved":                   pv.Resolved,
		"plan":                       pv.Plan,
		"risk_level":                 firstNonEmpty(pv.RiskLevel, plan.RiskLevel),
		"requires_confirm_text":      false,
		"no_side_effect":             true,
	}, nil
}

func (s *PrecheckRestoreRunService) precheckMySQLPhysical(set *backupModels.InsightBackupSets) (any, error) {
	// MySQL 物理恢复仅支持 EXISTING_INSTANCE（会覆盖整个实例数据目录），
	// 未显式传值时按该模式预检，由预检项把风险提示出来。
	mode := strings.ToUpper(strings.TrimSpace(s.RestoreMode))
	if mode == "" {
		mode = physicalRestoreModeExistingInstance
	}
	form := &backupForms.PrecheckPhysicalRestoreForm{
		BackupSetID:      s.BackupSetID,
		RestoreMode:      mode,
		TargetInstanceID: s.TargetInstanceID,
		TargetNodeID:     s.TargetNodeID,
		TargetDataDir:    s.TargetDataDir,
		TargetPort:       s.TargetPort,
		AutoStart:        s.AutoStart,
	}
	ctx, err := buildMySQLPhysicalRestoreContext(form, s.C)
	if err != nil {
		return nil, err
	}
	return physicalPrecheckPayload(set, mode, ctx.Passed, ctx.PrecheckItems, ctx.Resolved), nil
}

func (s *PrecheckRestoreRunService) precheckPGPhysical(set *backupModels.InsightBackupSets) (any, error) {
	mode := strings.ToUpper(strings.TrimSpace(s.RestoreMode))
	if mode == "" {
		return nil, fmt.Errorf("物理恢复必须选择恢复方式：新建实例或覆盖已有实例")
	}
	form := &backupForms.PrecheckPhysicalRestoreForm{
		BackupSetID:      s.BackupSetID,
		RestoreMode:      mode,
		TargetInstanceID: s.TargetInstanceID,
		TargetNodeID:     s.TargetNodeID,
		TargetDataDir:    s.TargetDataDir,
		TargetPort:       s.TargetPort,
		AutoStart:        s.AutoStart,
	}
	ctx, err := buildPhysicalRestoreContext(form, s.C)
	if err != nil {
		return nil, err
	}
	return physicalPrecheckPayload(set, mode, ctx.Passed, ctx.PrecheckItems, ctx.Resolved), nil
}

// physicalPrecheckPayload 把两条物理链路的预检结果收敛成与逻辑侧一致的输出结构。
func physicalPrecheckPayload(set *backupModels.InsightBackupSets, mode string, passed bool, items any, resolved any) gin.H {
	var summary any
	switch v := items.(type) {
	case []physicalRestorePrecheckItem:
		summary = summarizePhysicalRestorePrecheck(v)
	default:
		summary = gin.H{}
	}
	return gin.H{
		"backup_set_id":              set.ID,
		"db_type":                    strings.TrimSpace(string(set.DBType)),
		"driver_type":                strings.TrimSpace(string(set.DriverType)),
		"restore_kind":               restoreKindPhysical,
		"restore_mode":               mode,
		"schema_conflict_policy":     "",
		"data_conflict_policy":       "",
		"passed":                     passed,
		"summary":                    summary,
		"items":                      items,
		"resolved":                   resolved,
		"plan":                       gin.H{},
		"risk_level":                 restorePhysicalRiskLevel(mode),
		"requires_confirm_text":      true,
		"no_side_effect":             true,
	}
}

// restorePhysicalRiskLevel 物理恢复恒为高危：会重写整个实例数据目录。
func restorePhysicalRiskLevel(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), "NEW_INSTANCE") {
		return "medium"
	}
	return "high"
}

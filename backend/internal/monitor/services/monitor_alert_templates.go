package services

// ---------------------------------------------------------------------------
// 告警策略模板
//
// 与采集策略模板（monitor_strategy_templates.go）同构的"模板-实例"两级配置：
//   - 每个数据库类型一个内置默认模板，未挂载 / 挂载失效的实例自动回落到它，
//     保证任何时刻实例都有告警规则在跑（不存在"裸奔实例"）；
//   - 实例与模板坚持 1:1：多对多会让"同一指标两条阈值都命中"的告警语义无法裁决；
//   - 模板内规则按 (template_id, metric_key) 覆盖式存储：保存时先查同键规则，
//     有则原行更新（rule_id 保持稳定，告警事件的外链与指纹去重不断），无则插入，
//     不追加重复记录；保存集里没有的同键旧规则删除；
//   - 规则引用指标字典（metric_key），模板内只存覆盖值（比较符/阈值/级别/开关）。
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/internal/monitor/forms"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 缓存：模板行 15 秒 TTL（规则集不进缓存，评估循环按模板分组查库）
// ---------------------------------------------------------------------------

const alertTemplateCacheTTL = 15 * time.Second

type alertTemplate struct {
	ID          uint64
	Name        string
	DisplayName string
	DBType      string
	Description string
	Builtin     bool
	IsDefault   bool
	Enabled     bool
	RuleCount   int
	UpdatedAt   string
}

var (
	alertTemplateMu       sync.RWMutex
	alertTemplateLoadedAt time.Time
	alertTemplatesByID    = map[uint64]*alertTemplate{}
	alertTemplatesByName  = map[string]*alertTemplate{}
	alertTemplateList     = []*alertTemplate{}
)

func invalidateAlertTemplateCache() {
	alertTemplateMu.Lock()
	alertTemplateLoadedAt = time.Time{}
	alertTemplateMu.Unlock()
}

func loadAlertTemplateCache() (map[uint64]*alertTemplate, map[string]*alertTemplate, []*alertTemplate) {
	alertTemplateMu.RLock()
	if !alertTemplateLoadedAt.IsZero() && time.Since(alertTemplateLoadedAt) < alertTemplateCacheTTL {
		byID, byName, list := alertTemplatesByID, alertTemplatesByName, alertTemplateList
		alertTemplateMu.RUnlock()
		return byID, byName, list
	}
	alertTemplateMu.RUnlock()

	byID := map[uint64]*alertTemplate{}
	byName := map[string]*alertTemplate{}
	list := make([]*alertTemplate, 0)
	if global.App != nil && global.App.DB != nil {
		records := make([]monitorModels.InsightMonitorAlertTemplates, 0)
		if err := global.App.DB.Order("db_type ASC, is_default DESC, name ASC").Find(&records).Error; err == nil {
			counts := alertRuleCountsByTemplate()
			for i := range records {
				record := records[i]
				tmpl := &alertTemplate{
					ID:          record.ID,
					Name:        record.Name,
					DisplayName: record.DisplayName,
					DBType:      string(record.DBType),
					Description: record.Description,
					Builtin:     record.Builtin,
					IsDefault:   record.IsDefault,
					Enabled:     record.Enabled,
					RuleCount:   counts[record.ID],
				}
				if record.Model != nil {
					tmpl.UpdatedAt = time.Time(record.UpdatedAt).Format(time.DateTime)
				}
				byID[record.ID] = tmpl
				byName[strings.ToLower(record.Name)] = tmpl
				list = append(list, tmpl)
			}
		}
	}

	alertTemplateMu.Lock()
	alertTemplatesByID, alertTemplatesByName, alertTemplateList = byID, byName, list
	alertTemplateLoadedAt = time.Now()
	alertTemplateMu.Unlock()
	return byID, byName, list
}

func alertRuleCountsByTemplate() map[uint64]int {
	counts := map[uint64]int{}
	if global.App == nil || global.App.DB == nil {
		return counts
	}
	type row struct {
		TemplateID uint64
		Cnt        int64
	}
	rows := make([]row, 0)
	if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertRules{}).
		Select("template_id, COUNT(*) as cnt").
		Where("template_id > 0").
		Group("template_id").Scan(&rows).Error; err != nil {
		return counts
	}
	for _, item := range rows {
		counts[item.TemplateID] = int(item.Cnt)
	}
	return counts
}

func alertTemplateByID(id uint64) *alertTemplate {
	byID, _, _ := loadAlertTemplateCache()
	return byID[id]
}

func alertTemplateByName(name string) *alertTemplate {
	_, byName, _ := loadAlertTemplateCache()
	return byName[strings.ToLower(strings.TrimSpace(name))]
}

// defaultAlertTemplate 取某个数据库类型的默认告警模板（兜底用）。
func defaultAlertTemplate(dbType string) *alertTemplate {
	_, _, list := loadAlertTemplateCache()
	engine := resolveEngine(dbType)
	for _, tmpl := range list {
		if tmpl.DBType == engine && tmpl.IsDefault && tmpl.Enabled {
			return tmpl
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 播种：默认模板 + 历史规则归位
// ---------------------------------------------------------------------------

// AlertDefaultTemplateName 内置默认告警模板的标识，播种与展示共用同一规则。
func AlertDefaultTemplateName(engine string) string {
	return "default-alert-" + resolveEngine(engine)
}

// EnsureAlertTemplates 幂等播种：
//  1. 每个已实现引擎（+ 历史规则里出现过的类型）一个内置默认模板；
//  2. 把 template_id=0 的历史全局规则按 db_type 归入对应默认模板
//     （就地 UPDATE，rule_id 不变 —— 告警事件按 rule_id 外链、指纹按 rule_id 去重，重建会断链）。
func EnsureAlertTemplates(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("数据库未就绪")
	}
	engines := map[string]struct{}{}
	for _, engine := range monitorableEngineKeys() {
		engines[resolveEngine(engine)] = struct{}{}
	}
	// 历史规则可能属于未在册的类型，同样要给它一个默认模板，否则规则会变成孤儿
	ruleTypes := make([]string, 0)
	if err := db.Table("insight_monitor_alert_rules").
		Distinct().Pluck("db_type", &ruleTypes).Error; err == nil {
		for _, item := range ruleTypes {
			key := resolveEngine(item)
			if key == "" {
				continue
			}
			engines[key] = struct{}{}
		}
	}
	// 兜底：规则表/实例表都还空着的新环境，至少保证两大主流类型有默认模板
	engines["mysql"] = struct{}{}
	engines["postgresql"] = struct{}{}

	keys := make([]string, 0, len(engines))
	for key := range engines {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, engine := range keys {
		if err := ensureAlertTemplateForEngine(db, engine); err != nil {
			return err
		}
		// 历史规则归位：按 db_type 挂到该类型默认模板上，rule_id 保持不变
		if err := db.Exec(
			"UPDATE insight_monitor_alert_rules r "+
				"JOIN insight_monitor_alert_templates t ON t.is_default = 1 AND t.db_type = ? "+
				"SET r.template_id = t.id "+
				"WHERE r.template_id = 0 AND LOWER(r.db_type) = LOWER(?)", engine, engine).Error; err != nil {
			global.App.Log.Warnf("migrate legacy alert rules to default template failed: %v", err)
		}
	}
	// 归位之后统一规范化：裸名键改写 + 同(模板,指标,等级)去重 + 布尔类指标只留最高档。
	// 必须在归位之后跑 —— 否则 template_id=0 的历史规则不参与去重，归位后会互相撞唯一索引。
	if err := normalizeAlertRuleStorage(db); err != nil {
		global.App.Log.Warnf("normalize alert rule storage failed: %v", err)
	}
	// 开箱规则播种：零规则的默认模板补一套推荐档位（详见函数注释）。
	// 放在归位/规范化之后：只认"模板内一行规则都没有"的模板，历史模板零触碰。
	if err := seedDefaultAlertRulesForEmptyTemplates(db); err != nil {
		global.App.Log.Warnf("seed default alert rules failed: %v", err)
	}
	invalidateAlertTemplateCache()
	return nil
}

// ---------------------------------------------------------------------------
// 开箱规则播种（零规则默认模板）
// ---------------------------------------------------------------------------

// defaultAlertRuleSpecsForEngine 纯函数：某引擎默认模板的"开箱规则集"。
//
// 清单 = 该引擎监控明细目录中可采集的指标 ∩ 推荐档位表（recommendedAlertLevels），
// 与配置页「恢复推荐值」按钮同一数据源 —— 页面推荐什么，开箱就播什么，口径永不漂移。
// 资源层（res.*，主机指标全引擎同源）+ 连接/缓存/锁等引擎直连指标命中推荐表的都会进；
// 引擎采不到的键（目录 Available=false）与推荐表未登记的指标一律不播（默认集克制）。
func defaultAlertRuleSpecsForEngine(engine string) []metricLevelsToSave {
	dict := make(map[string]MetricDescriptor, len(builtinMetricCatalog))
	for _, item := range builtinMetricCatalog {
		dict[canonicalMetricKey(item.MetricKey)] = item
	}
	result := make([]metricLevelsToSave, 0, 8)
	for _, row := range alertRuleCatalogRows(engine) {
		key := canonicalMetricKey(row.MetricKey)
		if !row.Available {
			continue
		}
		item, ok := dict[key]
		if !ok {
			continue
		}
		levels := recommendedLevelsFor(item)
		if len(levels) == 0 {
			continue
		}
		result = append(result, metricLevelsToSave{
			Key:      key,
			Item:     item,
			Operator: defaultString(strings.TrimSpace(item.DefaultOperator), ">="),
			Levels:   levels,
		})
	}
	return result
}

// seedDefaultAlertRulesForEmptyTemplates 给"一行规则都没有的默认模板"播种开箱规则集。
//
// 边界（与"不覆盖用户配置"的底线对齐）：
//   - 只处理规则数为 0 的默认模板：MySQL/PostgreSQL 的默认模板已有历史规则，
//     一行都不会动；用户改过阈值/删过规则的模板更不可能触碰；
//   - 墓碑在册的指标跳过（防御式：零规则模板理论上没有墓碑，但删除语义必须优先）；
//   - 评估链的"只删不补"（pruneStaleAlertRules）保持不变 —— 播种只发生一次
//     （模板从空到非空），之后该模板的规则集完全归用户管，平台永不再写。
func seedDefaultAlertRulesForEmptyTemplates(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	_, _, list := loadAlertTemplateCache()
	for _, tmpl := range list {
		if !tmpl.IsDefault || !tmpl.Enabled {
			continue
		}
		var count int64
		if err := db.Model(&monitorModels.InsightMonitorAlertRules{}).
			Where("template_id = ?", tmpl.ID).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		engine := tmpl.DBType
		specs := defaultAlertRuleSpecsForEngine(engine)
		if len(specs) == 0 {
			continue
		}
		tombstones := make(map[string]struct{})
		tombRows := make([]monitorModels.InsightMonitorAlertRuleTombstone, 0)
		if err := db.Where("template_id = ?", tmpl.ID).Find(&tombRows).Error; err != nil {
			return err
		}
		for _, row := range tombRows {
			tombstones[canonicalMetricKey(row.MetricKey)] = struct{}{}
		}
		created := 0
		for _, spec := range specs {
			if _, skip := tombstones[spec.Key]; skip {
				continue
			}
			description := defaultString(strings.TrimSpace(spec.Item.Advice), spec.Item.DisplayName)
			for _, level := range spec.Levels {
				rule := monitorModels.InsightMonitorAlertRules{
					Model:           &commonModels.Model{},
					Name:            fmt.Sprintf("%s %s %s %s %s", labelOfEngine(engine), spec.Item.DisplayName,
						level.Level, spec.Operator, formatRuleThreshold(level.Threshold, spec.Item.Unit)),
					DBType:          commonModels.EnumType(engine),
					TemplateID:      tmpl.ID,
					RuleType:        "threshold",
					ConditionType:   "threshold",
					MetricKey:       spec.Key,
					Operator:        spec.Operator,
					Threshold:       level.Threshold,
					ForSeconds:      level.ForSeconds,
					HoldMinutes:     level.ForSeconds,
					RecoverySeconds: defaultRecoverySeconds,
					NoDataBehavior:  "ignore",
					AlertLevel:      level.Level,
					Severity:        severityFromLevel(level.Level),
					SilenceMinutes:  level.SilenceMinutes,
					ScopeType:       "global",
					Description:     description,
					Enabled:         level.Enabled,
				}
				if err := db.Create(&rule).Error; err != nil {
					return err
				}
				created++
			}
		}
		if created > 0 && global.App != nil && global.App.Log != nil {
			global.App.Log.Infof("seeded %d default alert rules into empty default template %s (%s)", created, tmpl.Name, engine)
		}
	}
	return nil
}

func ensureAlertTemplateForEngine(db *gorm.DB, engine string) error {
	name := AlertDefaultTemplateName(engine)
	label := engine
	if capability, ok := engineCapabilityIndex[engine]; ok {
		label = capability.Label
	}
	var record monitorModels.InsightMonitorAlertTemplates
	err := db.Where("name = ?", name).Take(&record).Error
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			return err
		}
		record = monitorModels.InsightMonitorAlertTemplates{
			Model:       &commonModels.Model{},
			Name:        name,
			DisplayName: "默认告警策略",
			DBType:      commonModels.EnumType(engine),
			Description: fmt.Sprintf("%s 的内置默认告警策略；未挂载模板的实例自动套用", label),
			Builtin:     true,
			IsDefault:   true,
			Enabled:     true,
		}
		if err := db.Create(&record).Error; err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 解析：显式挂载 → 挂载失效 → 类型默认（三级回退，与采集策略同款）
// ---------------------------------------------------------------------------

// resolveAlertTemplateForInstance 决定实例实际套用哪个告警模板。
// 返回 nil 表示连默认模板都没有（该引擎未播种，属部署异常；调用方跳过评估并记日志）。
func resolveAlertTemplateForInstance(dbType string, templateID *uint64) (*alertTemplate, bool, string) {
	engine := resolveEngine(dbType)
	if templateID != nil && *templateID > 0 {
		tmpl := alertTemplateByID(*templateID)
		switch {
		case tmpl == nil:
			return defaultAlertTemplate(engine), true, "挂载的告警策略模板已不存在，已回退到默认模板"
		case !tmpl.Enabled:
			return defaultAlertTemplate(engine), true, "挂载的告警策略模板已停用，已回退到默认模板"
		case tmpl.DBType != engine:
			return defaultAlertTemplate(engine), true, fmt.Sprintf("挂载的告警策略模板属于 %s，与实例类型 %s 不一致，已回退到默认模板",
				tmpl.DBType, engine)
		default:
			return tmpl, false, ""
		}
	}
	fallback := defaultAlertTemplate(engine)
	if fallback == nil {
		return nil, true, "该数据库类型暂无可用的默认告警策略模板"
	}
	return fallback, true, "未挂载告警策略模板，套用默认模板"
}

// loadAlertTemplateRules 取模板下启用的规则集（评估链按模板分组调用，避免逐实例查库）。
func loadAlertTemplateRules(templateID uint64) []monitorModels.InsightMonitorAlertRules {
	rules := make([]monitorModels.InsightMonitorAlertRules, 0)
	if global.App == nil || global.App.DB == nil {
		return rules
	}
	global.App.DB.Where("template_id = ? AND enabled = ?", templateID, true).
		Order("id asc").Find(&rules)
	return rules
}

// alertBindingCounts 统计告警模板挂载情况（口径与采集策略一致）：
// bound = 字面挂载数（删除保护看这个）；effective = 解析后真正套用数（含默认兜底）。
func alertBindingCounts() (map[uint64]int, map[uint64]int) {
	bound := map[uint64]int{}
	effective := map[uint64]int{}
	if global.App == nil || global.App.DB == nil {
		return bound, effective
	}
	type row struct {
		InstanceID      string
		DbType          string
		AlertTemplateID *uint64
	}
	rows := make([]row, 0)
	if err := global.App.DB.Table("insight_monitor_target_configs").
		Select("instance_id, db_type, alert_template_id").Find(&rows).Error; err != nil {
		return bound, effective
	}
	for _, item := range rows {
		if item.AlertTemplateID != nil && *item.AlertTemplateID > 0 {
			bound[*item.AlertTemplateID]++
		}
		tmpl, _, _ := resolveAlertTemplateForInstance(item.DbType, item.AlertTemplateID)
		if tmpl != nil {
			effective[tmpl.ID]++
		}
	}
	return bound, effective
}

// ---------------------------------------------------------------------------
// 列表 / 详情 / CRUD
// ---------------------------------------------------------------------------

type AlertTemplateItem struct {
	ID           uint64 `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	DBType       string `json:"db_type"`
	DBTypeLabel  string `json:"db_type_label"`
	Description  string `json:"description"`
	Builtin      bool   `json:"builtin"`
	IsDefault    bool   `json:"is_default"`
	Enabled      bool   `json:"enabled"`
	RuleCount    int    `json:"rule_count"`
	BoundCount   int    `json:"bound_instances"`
	EffectiveNum int    `json:"effective_instances"`
	UpdatedAt    string `json:"updated_at"`
}

type AlertTemplateListData struct {
	GeneratedAt string              `json:"generated_at"`
	Engines     []EngineCapability  `json:"engines"`
	DBTypes     []FilterOption      `json:"db_types"`
	Templates   []AlertTemplateItem `json:"templates"`
}

func alertTemplateItemOf(tmpl *alertTemplate, bound, effective int) AlertTemplateItem {
	label := tmpl.DBType
	if capability, ok := engineCapabilityIndex[tmpl.DBType]; ok {
		label = capability.Label
	}
	return AlertTemplateItem{
		ID: tmpl.ID, Name: tmpl.Name, DisplayName: tmpl.DisplayName,
		DBType: tmpl.DBType, DBTypeLabel: label,
		Description: tmpl.Description,
		Builtin:     tmpl.Builtin, IsDefault: tmpl.IsDefault, Enabled: tmpl.Enabled,
		RuleCount: tmpl.RuleCount, BoundCount: bound, EffectiveNum: effective,
		UpdatedAt: tmpl.UpdatedAt,
	}
}

type GetAlertTemplateListService struct {
	DBType string
	C      *gin.Context
}

func (s *GetAlertTemplateListService) Run() (*AlertTemplateListData, error) {
	if global.App == nil || global.App.DB == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	if err := EnsureAlertTemplates(global.App.DB); err != nil {
		global.App.Log.Warnf("seed alert templates failed: %v", err)
	}
	_, _, list := loadAlertTemplateCache()
	bound, effective := alertBindingCounts()
	engineFilter := resolveEngine(s.DBType)

	data := &AlertTemplateListData{
		GeneratedAt: time.Now().Format(time.DateTime),
		Engines:     EngineCapabilityList(),
		Templates:   make([]AlertTemplateItem, 0, len(list)),
	}
	seen := map[string]struct{}{}
	for _, tmpl := range list {
		if engineFilter != "" && tmpl.DBType != engineFilter {
			continue
		}
		item := alertTemplateItemOf(tmpl, bound[tmpl.ID], effective[tmpl.ID])
		if tmpl.Builtin {
			item.UpdatedAt = ""
		}
		data.Templates = append(data.Templates, item)
		if _, ok := seen[tmpl.DBType]; !ok {
			seen[tmpl.DBType] = struct{}{}
			data.DBTypes = append(data.DBTypes, FilterOption{Value: tmpl.DBType, Label: labelOfEngine(tmpl.DBType)})
		}
	}
	sort.SliceStable(data.Templates, func(i, j int) bool {
		left, right := data.Templates[i], data.Templates[j]
		if left.DBType != right.DBType {
			return left.DBType < right.DBType
		}
		if left.IsDefault != right.IsDefault {
			return left.IsDefault
		}
		return left.Name < right.Name
	})
	sort.SliceStable(data.DBTypes, func(i, j int) bool { return data.DBTypes[i].Value < data.DBTypes[j].Value })
	return data, nil
}

func labelOfEngine(engine string) string {
	if capability, ok := engineCapabilityIndex[engine]; ok {
		return capability.Label
	}
	return engine
}

type AlertRuleItem struct {
	ID              uint64   `json:"id"`
	Name            string   `json:"name"`
	MetricKey       string   `json:"metric_key"`
	Operator        string   `json:"operator"`
	Threshold       float64  `json:"threshold"`
	AlertLevel      string   `json:"alert_level"`
	Severity        string   `json:"severity"`
	ForSeconds      int      `json:"for_seconds"`
	RecoverySeconds int      `json:"recovery_seconds"`
	NoDataBehavior  string   `json:"no_data_behavior"`
	SilenceMinutes  int      `json:"silence_minutes"`
	ScopeType       string   `json:"scope_type"`
	ScopeValue      string   `json:"scope_value"`
	NotifyChannels  []string `json:"notify_channels"`
	SilencePolicy   []string `json:"silence_policy"`
	Description     string   `json:"description"`
	Enabled         bool     `json:"enabled"`
}

type AlertTemplateDetailData struct {
	Template AlertTemplateItem `json:"template"`
	Rules    []AlertRuleItem   `json:"rules"`
	// Metrics 按监控项分组的规则视图：规则配置抽屉的唯一数据源。
	// Rules 是平铺视图（保留给"通知与静默"等按规则操作的页面），两者同源同口径。
	Metrics []AlertRuleGroupItem `json:"metrics"`
	// Warnings 软校验提示（保存后回传，供前端黄色提示区展示）
	Warnings []string `json:"warnings"`
}

type GetAlertTemplateDetailService struct {
	ID uint64
	C  *gin.Context
}

func (s *GetAlertTemplateDetailService) Run() (*AlertTemplateDetailData, error) {
	tmpl := alertTemplateByID(s.ID)
	if tmpl == nil {
		return nil, fmt.Errorf("告警策略模板不存在")
	}
	bound, effective := alertBindingCounts()
	data := &AlertTemplateDetailData{
		Template: alertTemplateItemOf(tmpl, bound[tmpl.ID], effective[tmpl.ID]),
		Rules:    make([]AlertRuleItem, 0),
	}
	rules := make([]monitorModels.InsightMonitorAlertRules, 0)
	if global.App != nil && global.App.DB != nil {
		global.App.DB.Where("template_id = ?", s.ID).Order("metric_key asc, id asc").Find(&rules)
	}
	for _, row := range rules {
		data.Rules = append(data.Rules, AlertRuleItem{
			ID: row.ID, Name: row.Name, MetricKey: row.MetricKey,
			Operator: row.Operator, Threshold: row.Threshold,
			AlertLevel: row.AlertLevel, Severity: row.Severity,
			ForSeconds: row.ForSeconds, RecoverySeconds: row.RecoverySeconds,
			NoDataBehavior: row.NoDataBehavior, SilenceMinutes: row.SilenceMinutes,
			ScopeType: row.ScopeType, ScopeValue: row.ScopeValue,
			NotifyChannels: decodeJSONStringSlice(row.NotifyChannels),
			SilencePolicy:  decodeJSONStringSlice(row.SilencePolicy),
			Description:    row.Description, Enabled: row.Enabled,
		})
	}
	data.Metrics = alertRuleGroupsForTemplate(s.ID, resolveEngine(tmpl.DBType))
	return data, nil
}

func decodeJSONStringSlice(raw datatypes.JSON) []string {
	result := make([]string, 0)
	if len(raw) == 0 {
		return result
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return result
	}
	return arr
}

type CreateAlertTemplateService struct {
	*forms.SaveAlertTemplateForm
	C *gin.Context
}

func (s *CreateAlertTemplateService) Run() (*AlertTemplateItem, error) {
	if global.App == nil || global.App.DB == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	if err := EnsureAlertTemplates(global.App.DB); err != nil {
		global.App.Log.Warnf("seed alert templates failed: %v", err)
	}
	name := strings.ToLower(strings.TrimSpace(s.Name))
	if name == "" {
		return nil, fmt.Errorf("模板标识不能为空")
	}
	if len(name) > 64 {
		return nil, fmt.Errorf("模板标识最长 64 个字符")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return nil, fmt.Errorf("模板标识只能包含小写字母、数字、中划线与下划线")
	}
	engineKey := resolveEngine(s.DBType)
	if !engineImplemented(engineKey) {
		return nil, fmt.Errorf("暂不支持为 %s 创建告警策略模板（该引擎尚未实现监控采集）", s.DBType)
	}
	displayName := strings.TrimSpace(s.DisplayName)
	if displayName == "" {
		displayName = name
	}
	var count int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertTemplates{}).
		Where("name = ?", name).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, fmt.Errorf("模板标识 %s 已存在，请换一个", name)
	}

	record := monitorModels.InsightMonitorAlertTemplates{
		Model:       &commonModels.Model{},
		Name:        name,
		DisplayName: displayName,
		DBType:      commonModels.EnumType(engineKey),
		Description: strings.TrimSpace(s.Description),
		Builtin:     false,
		IsDefault:   false,
		Enabled:     s.Enabled == nil || *s.Enabled,
	}
	err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		// 没给规则集时从 copy_from 指定的模板（默认取该类型默认模板）复制一份：
		// "以默认模板为基础改几个阈值"是最高频的用法。INSERT SELECT 原样复制，
		// 保留通知/静默等全部字段；rule_id 重新生成（模板私有规则无跨模板引用）。
		source := alertTemplateByName(strings.TrimSpace(s.CopyFrom))
		if source == nil {
			source = defaultAlertTemplate(engineKey)
		}
		if source == nil {
			return nil
		}
		return tx.Exec(
			"INSERT INTO insight_monitor_alert_rules "+
				"(created_at, updated_at, name, db_type, template_id, rule_type, metric_key, operator, threshold, "+
				"hold_minutes, alert_level, scope_type, scope_value, notify_channels, silence_policy, description, "+
				"enabled, condition_type, for_seconds, recovery_seconds, no_data_behavior, severity, silence_minutes, group_key) "+
				"SELECT NOW(), NOW(), name, db_type, ?, rule_type, metric_key, operator, threshold, "+
				"hold_minutes, alert_level, scope_type, scope_value, notify_channels, silence_policy, description, "+
				"enabled, condition_type, for_seconds, recovery_seconds, no_data_behavior, severity, silence_minutes, group_key "+
				"FROM insight_monitor_alert_rules WHERE template_id = ?", record.ID, source.ID).Error
	})
	if err != nil {
		return nil, fmt.Errorf("创建告警策略模板失败：%w", err)
	}
	invalidateAlertTemplateCache()
	tmpl := alertTemplateByID(record.ID)
	if tmpl == nil {
		return nil, fmt.Errorf("模板读取失败")
	}
	item := alertTemplateItemOf(tmpl, 0, 0)
	item.UpdatedAt = time.Now().Format(time.DateTime)
	return &item, nil
}

type UpdateAlertTemplateService struct {
	ID uint64
	*forms.UpdateAlertTemplateForm
	C *gin.Context
}

func (s *UpdateAlertTemplateService) Run() (*AlertTemplateItem, error) {
	if global.App == nil || global.App.DB == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	var record monitorModels.InsightMonitorAlertTemplates
	if err := global.App.DB.Where("id = ?", s.ID).Take(&record).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("告警策略模板不存在")
		}
		return nil, err
	}
	updates := map[string]any{}
	if value := strings.TrimSpace(s.DisplayName); value != "" {
		updates["display_name"] = value
	}
	if s.Description != nil {
		updates["description"] = strings.TrimSpace(*s.Description)
	}
	if s.Enabled != nil {
		if (record.Builtin || record.IsDefault) && !*s.Enabled {
			return nil, fmt.Errorf("内置默认模板不可停用；它是未挂载实例的告警兜底")
		}
		updates["enabled"] = *s.Enabled
	}
	if len(updates) > 0 {
		if err := global.App.DB.Model(&monitorModels.InsightMonitorAlertTemplates{}).
			Where("id = ?", record.ID).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	invalidateAlertTemplateCache()
	tmpl := alertTemplateByID(record.ID)
	if tmpl == nil {
		return nil, fmt.Errorf("模板读取失败")
	}
	bound, effective := alertBindingCounts()
	item := alertTemplateItemOf(tmpl, bound[tmpl.ID], effective[tmpl.ID])
	if !tmpl.Builtin {
		item.UpdatedAt = time.Now().Format(time.DateTime)
	}
	return &item, nil
}

type DeleteAlertTemplateService struct {
	ID uint64
	C  *gin.Context
}

func (s *DeleteAlertTemplateService) Run() error {
	if global.App == nil || global.App.DB == nil {
		return fmt.Errorf("数据库未就绪")
	}
	var record monitorModels.InsightMonitorAlertTemplates
	if err := global.App.DB.Where("id = ?", s.ID).Take(&record).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("告警策略模板不存在")
		}
		return err
	}
	if record.Builtin || record.IsDefault {
		return fmt.Errorf("内置默认模板不可删除：它是未挂载实例的告警兜底，删掉会出现告警盲区")
	}
	var bound int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorTargetConfigs{}).
		Where("alert_template_id = ?", record.ID).Count(&bound).Error; err != nil {
		return err
	}
	if bound > 0 {
		return fmt.Errorf("该模板仍挂载在 %d 个实例上，请先切换到其他模板再删除", bound)
	}
	err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("template_id = ?", record.ID).
			Delete(&monitorModels.InsightMonitorAlertRules{}).Error; err != nil {
			return err
		}
		// 模板没了，墓碑一并清掉：墓碑只对默认模板的补档有意义，避免残留垃圾行
		if err := tx.Where("template_id = ?", record.ID).
			Delete(&monitorModels.InsightMonitorAlertRuleTombstone{}).Error; err != nil {
			return err
		}
		return tx.Delete(&monitorModels.InsightMonitorAlertTemplates{}, record.ID).Error
	})
	if err != nil {
		return err
	}
	invalidateAlertTemplateCache()
	return nil
}

// ---------------------------------------------------------------------------
// 挂载 / 解绑（与采集策略同款语义：绑定即覆盖挂载，解绑即回落默认）
// ---------------------------------------------------------------------------

type BindAlertInstancesService struct {
	ID uint64
	*forms.BindAlertInstancesForm
	C *gin.Context
}

func (s *BindAlertInstancesService) Run() (*StrategyBindResult, error) {
	if global.App == nil || global.App.DB == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	tmpl := alertTemplateByID(s.ID)
	if tmpl == nil {
		return nil, fmt.Errorf("告警策略模板不存在")
	}
	if !tmpl.Enabled {
		return nil, fmt.Errorf("模板 %s 已停用，不能挂载；请先启用它", tmpl.DisplayName)
	}
	ids := make([]string, 0, len(s.InstanceIDs))
	seen := map[string]struct{}{}
	for _, raw := range s.InstanceIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("请先选择要挂载的实例")
	}

	result := &StrategyBindResult{
		TemplateID: tmpl.ID, TemplateName: tmpl.Name,
		Mode: "mount", Notes: make([]string, 0),
	}
	err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			var record monitorModels.InsightMonitorTargetConfigs
			if err := tx.Where("instance_id = ?", id).Take(&record).Error; err != nil {
				result.Skipped++
				result.Notes = append(result.Notes, fmt.Sprintf("%s：实例不存在", id))
				continue
			}
			// 跨类型挂载直接拒绝：告警模板按库类型分类，挂错类型解析时只会回退，
			// 页面还显示"已挂载"，等于给了一个假的成功反馈。
			if resolveEngine(string(record.DBType)) != tmpl.DBType {
				result.Skipped++
				result.Notes = append(result.Notes, fmt.Sprintf("%s：实例类型 %s 与模板类型 %s 不一致",
					id, resolveEngine(string(record.DBType)), tmpl.DBType))
				continue
			}
			if err := tx.Model(&monitorModels.InsightMonitorTargetConfigs{}).
				Where("instance_id = ?", id).
				Updates(map[string]any{"alert_template_id": tmpl.ID, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
			result.Bound++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	invalidateAlertTemplateCache()
	return result, nil
}

type UnbindAlertInstancesService struct {
	*forms.UnbindAlertInstancesForm
	C *gin.Context
}

func (s *UnbindAlertInstancesService) Run() (*StrategyBindResult, error) {
	if global.App == nil || global.App.DB == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	ids := make([]string, 0, len(s.InstanceIDs))
	seen := map[string]struct{}{}
	for _, raw := range s.InstanceIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("请先选择要解绑的实例")
	}
	result := &StrategyBindResult{Mode: "unmount", Notes: make([]string, 0)}
	err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		for _, id := range ids {
			var record monitorModels.InsightMonitorTargetConfigs
			if err := tx.Where("instance_id = ?", id).Take(&record).Error; err != nil {
				result.Skipped++
				result.Notes = append(result.Notes, fmt.Sprintf("%s：实例不存在", id))
				continue
			}
			// 解绑不是"无告警"：置 NULL 后评估链自动按该类型默认模板跑
			if err := tx.Model(&monitorModels.InsightMonitorTargetConfigs{}).
				Where("instance_id = ?", id).
				Updates(map[string]any{"alert_template_id": nil, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
			fallback := defaultAlertTemplate(string(record.DBType))
			if fallback != nil {
				result.TemplateName = fallback.Name
			}
			result.Bound++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	invalidateAlertTemplateCache()
	return result, nil
}

// ---------------------------------------------------------------------------
// 规则集覆盖式保存：引用字典 + 按 (template_id, metric_key, alert_level) 覆盖
// ---------------------------------------------------------------------------

// AlertRuleGroupItem 规则配置页的"监控项"视角：一个指标 = 分组，组内含 1~4 个档位。
//
// 分组数据是配置界面的唯一数据源：既给已配置档位（Levels），也给推荐档位
// （RecommendedLevels，「恢复推荐值」按钮用），还给层级（Layer，前端据此分
// 主机/数据库/引擎三个 Tab）。层级口径与监控明细页完全一致（displayLayoutCatalogItems）。
type AlertRuleGroupItem struct {
	MetricKey   string `json:"metric_key"`
	DisplayName string `json:"display_name"`
	// Layer 层级：host / engine / kernel（前端三 Tab 的划分依据）
	Layer      string `json:"layer"`
	LayerLabel string `json:"layer_label"`
	// DetailItem 该指标在监控明细中所属的监控项名（如 网络流量）
	DetailItem string `json:"detail_item"`
	Unit       string `json:"unit"`
	Category   string `json:"category"`
	// Available 是否有采集源。false 时前端禁用配置（采不到就没有告警可谈），
	// 但已存配置照常返回，不丢用户数据。
	Available bool `json:"available"`
	// Operator 该指标唯一的比较方向（由指标语义决定），前端只读展示
	Operator string `json:"operator"`
	// OperatorLocked true 表示方向由平台锁定（>= / <= 方向明确）；布尔类（!= / =）为 false
	OperatorLocked bool `json:"operator_locked"`
	// Description 指标说明/建议（来自字典 advice）
	Description string `json:"description"`
	// 指标级策略：恢复判定与无数据行为（对所有档位生效）
	RecoverySeconds int    `json:"recovery_seconds"`
	NoDataBehavior  string `json:"no_data_behavior"`
	// Levels 已配置的档位（按 P0→P3 排序，未配置为空数组）
	Levels []AlertLevelSpec `json:"levels"`
	// RecommendedLevels 平台推荐档位（默认集克制：只有值得开箱即告警的指标才有值）
	RecommendedLevels []AlertLevelSpec `json:"recommended_levels"`
}

// alertRuleGroupsForTemplate 组装某模板的"监控项分组"视图。
//
// 清单来源是监控明细页目录（displayLayoutCatalogItems），因此"明细页能看到什么，
// 这里就能配什么"；目录里没有但规则表里存在的键（目录与字典漂移）会兜底追加，
// 绝不静默丢掉用户已配置的规则。
// alertRuleCatalogRows 提供"配置清单用"的指标行，并按 metric_key 收敛为一份。
// 归并规则见 dedupeCatalogRowsByMetric。
func alertRuleCatalogRows(engine string) []MetricCatalogItem {
	return dedupeCatalogRowsByMetric(displayLayoutCatalogItems(engine))
}

// dedupeCatalogRowsByMetric 按 metric_key 把指标行收敛为一份，保住"一指标一组档位"的契约。
//
// 监控明细目录里同一个指标可能挂在多个明细项下——例如 perf.conn_count 同时属于
// 「会话连接」和「线程」，perf.active_conn_count 同理。这在明细页是对的（同一个人口
// 从两个视角看），但告警阈值是按**指标**存的，一个指标只能有一组档位。
// 如果原样逐条出清单，前端一次提交全部监控项时会出现两条同 metric_key 的记录，
// 被保存接口判为"重复提交"，整个配置页直接存不下去。
//
// 归并口径：首次出现的那条决定展示信息（明细项/单位/分类），
// 但如果后来那条可采集而先出现的不可采集，则用可采集的顶替 —— 不能让排序
// 把本来能配的指标标成"无采集源"。
//
// 明细项归属做**合并**而非丢弃：首行的 DetailItem 保留，后续同键行的不同明细项
// 以「、」追加（如「会话连接、线程」）。否则共享键在规则配置里只剩首个归属，
// 用户在明细页看到「线程」卡片、到规则配置里搜「线程」却一无所得（2026-09-25 现场问题）。
// 指标说明（Advice）同理：首行为空而后行有值时用后行补齐，不覆盖已有文案。
func dedupeCatalogRowsByMetric(rows []MetricCatalogItem) []MetricCatalogItem {
	merged := make([]MetricCatalogItem, 0, len(rows))
	position := make(map[string]int, len(rows))
	for _, row := range rows {
		key := canonicalMetricKey(row.MetricKey)
		if key == "" {
			continue
		}
		if pos, ok := position[key]; ok {
			// 先合并元信息、再决定顶替：否则顶替会把已累积的归属/说明丢掉。
			// 归属明细项合并：共享键在规则配置里保留它在明细页的全部出入口
			detailItem := merged[pos].DetailItem
			if row.DetailItem != "" && !strings.Contains(detailItem, row.DetailItem) {
				detailItem = detailItem + "、" + row.DetailItem
			}
			// 指标说明兜底：首行没有而后行有（如明细页 definition）则补齐
			advice := merged[pos].Advice
			if strings.TrimSpace(advice) == "" && strings.TrimSpace(row.Advice) != "" {
				advice = row.Advice
			}
			if !merged[pos].Available && row.Available {
				merged[pos] = row
			}
			merged[pos].DetailItem = detailItem
			if strings.TrimSpace(merged[pos].Advice) == "" {
				merged[pos].Advice = advice
			}
			continue
		}
		position[key] = len(merged)
		merged = append(merged, row)
	}
	return merged
}

func alertRuleGroupsForTemplate(templateID uint64, engine string) []AlertRuleGroupItem {
	dictIndex := make(map[string]MetricDescriptor, len(builtinMetricCatalog))
	for _, item := range builtinMetricCatalog {
		dictIndex[canonicalMetricKey(item.MetricKey)] = item
	}

	rulesByKey := map[string][]monitorModels.InsightMonitorAlertRules{}
	for _, row := range loadAlertTemplateRules(templateID) {
		key := canonicalMetricKey(row.MetricKey)
		rulesByKey[key] = append(rulesByKey[key], row)
	}

	result := make([]AlertRuleGroupItem, 0, 40)
	covered := map[string]bool{}
	for _, row := range alertRuleCatalogRows(engine) {
		key := canonicalMetricKey(row.MetricKey)
		covered[key] = true
		dict, inDict := dictIndex[key]
		group := AlertRuleGroupItem{
			MetricKey:      key,
			DisplayName:    row.DisplayName,
			Layer:          row.Layer,
			LayerLabel:     layerLabel(row.Layer),
			DetailItem:     row.DetailItem,
			Unit:           row.Unit,
			Category:       row.Category,
			Available:      row.Available && inDict,
			Description:    row.Advice,
			Operator:       ">=",
			NoDataBehavior: "ignore",
		}
		if inDict {
			group.Operator = defaultString(strings.TrimSpace(dict.DefaultOperator), ">=")
			group.Description = defaultString(dict.Advice, row.Advice)
			group.RecommendedLevels = recommendedLevelsFor(dict)
		}
		group.OperatorLocked = isGreaterDirection(group.Operator) || isLesserDirection(group.Operator)
		if !group.Available {
			// 采不到就不推荐开告警："恢复推荐值"不该把不可采集的指标塞进配置
			group.RecommendedLevels = nil
		}
		for _, rule := range rulesByKey[key] {
			group.Levels = append(group.Levels, AlertLevelSpec{
				Level:          defaultString(normalizeAlertLevelStrict(rule.AlertLevel), rule.AlertLevel),
				Threshold:      rule.Threshold,
				ForSeconds:     rule.ForSeconds,
				SilenceMinutes: rule.SilenceMinutes,
				Enabled:        rule.Enabled,
			})
			group.RecoverySeconds = rule.RecoverySeconds
			group.NoDataBehavior = defaultString(strings.TrimSpace(rule.NoDataBehavior), "ignore")
			if strings.TrimSpace(rule.Description) != "" {
				group.Description = rule.Description
			}
			group.Operator = defaultString(strings.TrimSpace(rule.Operator), group.Operator)
		}
		sortByAlertLevel(group.Levels)
		result = append(result, group)
	}

	// 兜底：规则引用了目录外的指标（指标改名/目录收窄）也要展示，否则用户看不到、
	// 改不掉，却一直在评估。标 available=false，前端提示"该指标已不在监控明细中"。
	extraKeys := make([]string, 0)
	for key := range rulesByKey {
		if covered[key] {
			continue
		}
		extraKeys = append(extraKeys, key)
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		rows := rulesByKey[key]
		group := AlertRuleGroupItem{
			MetricKey:       key,
			DisplayName:     metricLabel(key),
			Layer:           metricLayer(key),
			LayerLabel:      layerLabel(metricLayer(key)),
			Available:       false,
			Operator:        defaultString(strings.TrimSpace(rows[0].Operator), ">="),
			NoDataBehavior:  defaultString(strings.TrimSpace(rows[0].NoDataBehavior), "ignore"),
			RecoverySeconds: rows[0].RecoverySeconds,
			Description:     rows[0].Description,
		}
		group.OperatorLocked = isGreaterDirection(group.Operator) || isLesserDirection(group.Operator)
		for _, rule := range rows {
			group.Levels = append(group.Levels, AlertLevelSpec{
				Level:          defaultString(normalizeAlertLevelStrict(rule.AlertLevel), rule.AlertLevel),
				Threshold:      rule.Threshold,
				ForSeconds:     rule.ForSeconds,
				SilenceMinutes: rule.SilenceMinutes,
				Enabled:        rule.Enabled,
			})
		}
		sortByAlertLevel(group.Levels)
		result = append(result, group)
	}
	return result
}

// metricLevelsToSave 归一化后的"待保存指标"：一个指标 + 它的全部档位。
type metricLevelsToSave struct {
	Key             string
	Item            MetricDescriptor
	Operator        string
	Levels          []AlertLevelSpec
	RecoverySeconds int
	NoDataBehavior  string
	Description     string
}

// AlertTemplateRulesSaveResult 保存结果：完整规则集 + 软校验提示。
type AlertTemplateRulesSaveResult struct {
	Rules    []AlertRuleItem `json:"rules"`
	Warnings []string        `json:"warnings"`
}

// SaveAlertTemplateRulesService 保存模板的规则集。
//
// 同名 (指标, 等级) 已有规则原行覆盖（保留 rule_id 与通知/静默/作用域配置），新档插入。
// 不做追加式写入，模板内同指标同等级永远只有一行。
//
// 删除范围由 ReplaceAll 决定，默认（false）**只作用于提交里出现的指标**：
//   - ReplaceAll=false（默认，增量口径）：只替换提交的指标，未提交的指标原样保留。
//     这样一次"只改了 CPU"的部分提交不会把模板里其余 28 个指标的规则一并抹掉。
//   - ReplaceAll=true（全量口径）：提交集即该模板的完整期望状态，未提交的档位全部删除。
//     配置页一次提交全部监控项，走这条路。
//
// 之所以不默认全量：本接口是"覆盖式"的，一旦调用方漏提交某个指标就是静默数据丢失，
// 而调用方往往无法察觉。宁可让想清空的人显式声明，也不让想局部改的人误伤。
//
// 两种入参：
//   - Metrics（新，分组口径）：一个监控项一次给全它的档位，推荐前端使用；
//   - Rules（旧，平铺口径）：老客户端仍可提交，服务端按指标聚合后走同一套校验与 diff，
//     并强制 ReplaceAll（旧接口本就是覆盖式语义，保持向后兼容）。
type SaveAlertTemplateRulesService struct {
	ID              uint64
	Metrics         []forms.AlertMetricRulesInput
	MetricsProvided bool
	ReplaceAll      bool
	Rules           []forms.AlertRuleInput
	C               *gin.Context
}

func (s *SaveAlertTemplateRulesService) Run() (*AlertTemplateRulesSaveResult, error) {
	if global.App == nil || global.App.DB == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	var record monitorModels.InsightMonitorAlertTemplates
	if err := global.App.DB.Where("id = ?", s.ID).Take(&record).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("告警策略模板不存在")
		}
		return nil, err
	}
	engine := resolveEngine(string(record.DBType))

	// 旧平铺口径（Rules）历史上就是覆盖式：老客户端靠"提交全集"表达期望状态，
	// 这里必须替它们保持 ReplaceAll，否则老调用方的删除操作会失效。
	replaceAll := s.ReplaceAll || !s.MetricsProvided

	// 规则引用指标字典：metric_key 必须是字典在册指标，防手滑写出永远采不到的死键
	catalogKeys := map[string]MetricDescriptor{}
	for _, item := range builtinMetricCatalog {
		catalogKeys[canonicalMetricKey(item.MetricKey)] = item
	}

	inputs, err := s.collectInputs(catalogKeys)
	if err != nil {
		return nil, err
	}

	// 硬校验：任一指标档位集合不合法就整体拒绝（写一半的配置比不写更危险）
	warnings := make([]string, 0)
	errors := make([]string, 0)
	validated := make([]metricLevelsToSave, 0, len(inputs))
	p0Count := 0
	for _, input := range inputs {
		item, ok := catalogKeys[input.Key]
		if !ok {
			errors = append(errors, fmt.Sprintf("指标 %s 不是平台在册指标，请从列表中选择", input.Key))
			continue
		}
		validation := ValidateMetricAlertLevels(item.DisplayName, input.Operator, item.DefaultOperator, input.Levels)
		errors = append(errors, validation.Errors...)
		warnings = append(warnings, validation.Warnings...)
		for _, level := range validation.Levels {
			if level.Level == AlertLevelP0 {
				p0Count++
			}
		}
		input.Item = item
		input.Operator = validation.Operator
		input.Levels = validation.Levels
		validated = append(validated, input)
	}
	if len(errors) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(errors, "；"))
	}
	if p0Count > maxP0LevelsPerTemplate {
		// 软提示：P0 是"电话呼"级别，配太多等于没有等级
		warnings = append(warnings, fmt.Sprintf(
			"该模板已配置 %d 个 P0 档位（建议不超过 %d 个）：P0 通常用于实例不可用等必须立即处置的场景，过多会削弱分级的意义",
			p0Count, maxP0LevelsPerTemplate))
	}

	existing := make([]monitorModels.InsightMonitorAlertRules, 0)
	if err := global.App.DB.Where("template_id = ?", s.ID).Find(&existing).Error; err != nil {
		return nil, err
	}
	existingByLevel := map[string]monitorModels.InsightMonitorAlertRules{}
	for _, row := range existing {
		existingByLevel[ruleLevelIdentity(row.MetricKey, row.AlertLevel)] = row
	}

	keepIDs := make([]uint64, 0, len(existing))
	err = global.App.DB.Transaction(func(tx *gorm.DB) error {
		for _, input := range validated {
			key := canonicalMetricKey(input.Key)
			description := strings.TrimSpace(input.Description)
			if description == "" {
				description = defaultString(input.Item.Advice, input.Item.DisplayName)
			}
			noData := defaultString(strings.TrimSpace(input.NoDataBehavior), "ignore")
			recovery := defaultInt(input.RecoverySeconds, defaultRecoverySeconds)
			for _, level := range input.Levels {
				identity := ruleLevelIdentity(key, level.Level)
				severity := severityFromLevel(level.Level)
				name := fmt.Sprintf("%s %s %s %s %s", labelOfEngine(engine), input.Item.DisplayName,
					level.Level, input.Operator, formatRuleThreshold(level.Threshold, input.Item.Unit))
				if old, ok := existingByLevel[identity]; ok {
					// 覆盖：原行更新。通知渠道/静默策略/作用域不在规则编辑范围内，原样保留，
					// 保证"通知与静默"页签里的配置不会被规则保存冲掉。
					keepIDs = append(keepIDs, old.ID)
					if err := tx.Model(&monitorModels.InsightMonitorAlertRules{}).
						Where("id = ?", old.ID).
						Updates(map[string]any{
							"name":             name,
							"operator":         input.Operator,
							"threshold":        level.Threshold,
							"alert_level":      level.Level,
							"severity":         severity,
							"for_seconds":      level.ForSeconds,
							"hold_minutes":     level.ForSeconds,
							"recovery_seconds": recovery,
							"no_data_behavior": noData,
							"description":      description,
							"enabled":          level.Enabled,
							"updated_at":       time.Now(),
						}).Error; err != nil {
						return err
					}
					continue
				}
				created := monitorModels.InsightMonitorAlertRules{
					Model:           &commonModels.Model{},
					Name:            name,
					DBType:          record.DBType,
					TemplateID:      record.ID,
					RuleType:        "threshold",
					ConditionType:   "threshold",
					MetricKey:       key,
					Operator:        input.Operator,
					Threshold:       level.Threshold,
					ForSeconds:      level.ForSeconds,
					HoldMinutes:     level.ForSeconds,
					RecoverySeconds: recovery,
					NoDataBehavior:  noData,
					AlertLevel:      level.Level,
					Severity:        severity,
					SilenceMinutes:  level.SilenceMinutes,
					ScopeType:       "global",
					ScopeValue:      "",
					Description:     description,
					Enabled:         level.Enabled,
				}
				if err := tx.Create(&created).Error; err != nil {
					return err
				}
				keepIDs = append(keepIDs, created.ID)
			}
		}
		// 提交集里没有的档位删除（覆盖式语义的另一半）。
		// 增量口径（ReplaceAll=false）下，只对"本次提交里出现过的指标"做删除：
		// 未提交的指标一律不动，避免部分提交把别的指标整片抹掉。
		submittedKeys := make(map[string]struct{}, len(validated))
		for _, input := range validated {
			submittedKeys[canonicalMetricKey(input.Key)] = struct{}{}
		}
		stale := make([]uint64, 0)
		kept := make(map[uint64]struct{}, len(keepIDs))
		for _, id := range keepIDs {
			kept[id] = struct{}{}
		}
		for _, row := range existing {
			if _, ok := kept[row.ID]; ok {
				continue
			}
			if !ruleOutOfScope(replaceAll, submittedKeys, row.MetricKey) {
				continue
			}
			stale = append(stale, row.ID)
		}
		if len(stale) > 0 {
			return tx.Where("id IN ?", stale).Delete(&monitorModels.InsightMonitorAlertRules{}).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	invalidateAlertTemplateCache()

	// 返回保存后的完整规则集（含从旧规则保留的通知/静默字段）
	saved := make([]monitorModels.InsightMonitorAlertRules, 0)
	global.App.DB.Where("template_id = ?", s.ID).Order("metric_key asc, id asc").Find(&saved)

	// 墓碑维护（仅内置/默认模板）：保存前有档、保存后没档 = 用户显式删除了该指标的
	// 告警 → 立碑，补档不再自动加回（默认模板的规则由平台播种，没有墓碑删除无法持久化）；
	// 保存后有档（重新配置或"恢复推荐值"）→ 撤碑，恢复默认补档资格。
	// 只对字典在册指标立碑：字典收窄/改名导致的规则清理是平台行为，不是用户意图。
	if record.Builtin || record.IsDefault {
		beforeKeys := make([]string, 0, len(existing))
		for _, row := range existing {
			beforeKeys = append(beforeKeys, canonicalMetricKey(row.MetricKey))
		}
		afterKeys := make([]string, 0, len(saved))
		for _, row := range saved {
			afterKeys = append(afterKeys, canonicalMetricKey(row.MetricKey))
		}
		insertKeys, removeKeys := tombstoneDiffForSave(catalogKeys, beforeKeys, afterKeys)
		if len(insertKeys) > 0 || len(removeKeys) > 0 {
			if err := maintainAlertRuleTombstones(global.App.DB, record.ID, insertKeys, removeKeys); err != nil {
				global.App.Log.Warnf("maintain alert rule tombstones failed: %v", err)
			}
		}
	}

	result := &AlertTemplateRulesSaveResult{Rules: make([]AlertRuleItem, 0, len(saved)), Warnings: warnings}
	for _, row := range saved {
		result.Rules = append(result.Rules, AlertRuleItem{
			ID: row.ID, Name: row.Name, MetricKey: row.MetricKey,
			Operator: row.Operator, Threshold: row.Threshold,
			AlertLevel: row.AlertLevel, Severity: row.Severity,
			ForSeconds: row.ForSeconds, RecoverySeconds: row.RecoverySeconds,
			NoDataBehavior: row.NoDataBehavior, SilenceMinutes: row.SilenceMinutes,
			ScopeType: row.ScopeType, ScopeValue: row.ScopeValue,
			NotifyChannels: decodeJSONStringSlice(row.NotifyChannels),
			SilencePolicy:  decodeJSONStringSlice(row.SilencePolicy),
			Description:    row.Description, Enabled: row.Enabled,
		})
	}
	return result, nil
}

// tombstoneDiffForSave 纯函数：对比保存前后"有档位的指标集合"，算出该立/该撤的墓碑。
//
// insert（立碑）：保存前有档、保存后没档 —— 用户显式删除了该指标的告警。
// 只认"保存前就有档位"的指标：本来就没配置的指标不立碑，保证平台后续版本
// 新增的默认规则仍能自动播种到存量环境。
// remove（撤碑）：保存后仍有档的全部键 —— 涵盖保留与新增两类（"删除后重新添加"
// 的指标不在 before 里，必须从 after 侧找回来）。撤碑对不存在的墓碑是幂等空操作。
// catalogKeys 之外的键跳过：字典收窄触发的规则清理是平台行为，不该记成用户删除。
func tombstoneDiffForSave(catalogKeys map[string]MetricDescriptor, before, after []string) (insert, remove []string) {
	afterSet := make(map[string]struct{}, len(after))
	for _, key := range after {
		afterSet[key] = struct{}{}
		if _, inDict := catalogKeys[key]; inDict {
			remove = append(remove, key)
		}
	}
	seen := make(map[string]struct{}, len(before))
	for _, key := range before {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if _, inDict := catalogKeys[key]; !inDict {
			continue
		}
		if _, kept := afterSet[key]; !kept {
			insert = append(insert, key)
		}
	}
	return insert, remove
}

// maintainAlertRuleTombstones 把一组立碑/撤碑动作落到库（幂等：重复立碑跳过）。
func maintainAlertRuleTombstones(db *gorm.DB, templateID uint64, insertKeys, removeKeys []string) error {
	if db == nil {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if len(removeKeys) > 0 {
			if err := tx.Where("template_id = ? AND metric_key IN ?", templateID, removeKeys).
				Delete(&monitorModels.InsightMonitorAlertRuleTombstone{}).Error; err != nil {
				return err
			}
		}
		for _, key := range insertKeys {
			var count int64
			if err := tx.Model(&monitorModels.InsightMonitorAlertRuleTombstone{}).
				Where("template_id = ? AND metric_key = ?", templateID, key).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			if err := tx.Create(&monitorModels.InsightMonitorAlertRuleTombstone{
				Model:      &commonModels.Model{},
				TemplateID: templateID,
				MetricKey:  key,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// collectInputs 把两种入参（分组 / 平铺）归一化成同一份"待保存指标"列表。
func (s *SaveAlertTemplateRulesService) collectInputs(catalogKeys map[string]MetricDescriptor) ([]metricLevelsToSave, error) {
	if s.MetricsProvided {
		result := make([]metricLevelsToSave, 0, len(s.Metrics))
		seen := map[string]struct{}{}
		for _, metric := range s.Metrics {
			key := canonicalMetricKey(metric.MetricKey)
			if key == "" {
				return nil, fmt.Errorf("指标键不能为空")
			}
			if _, dup := seen[key]; dup {
				return nil, fmt.Errorf("指标 %s 重复提交，请合并后再保存", metric.MetricKey)
			}
			seen[key] = struct{}{}
			item := catalogKeys[key]
			// 明细页有、但平台没有采集源的行（detail-only 占位键）不是字典指标，
			// 无法配置也无法保存：没有档位时直接跳过（前端会把它们一并提交），
			// 有档位才报错——那种情况说明配置来源异常，必须让人看到。
			if _, ok := catalogKeys[key]; !ok && len(metric.Levels) == 0 {
				continue
			}
			levels := make([]AlertLevelSpec, 0, len(metric.Levels))
			for _, level := range metric.Levels {
				levels = append(levels, AlertLevelSpec{
					Level:          level.Level,
					Threshold:      level.Threshold,
					ForSeconds:     level.ForSeconds,
					SilenceMinutes: defaultInt(level.SilenceMinutes, recommendedSilenceMinutes(level.Level)),
					Enabled:        level.Enabled,
				})
			}
			result = append(result, metricLevelsToSave{
				Key:             key,
				Item:            item,
				Operator:        defaultString(strings.TrimSpace(metric.Operator), defaultString(item.DefaultOperator, ">=")),
				Levels:          levels,
				RecoverySeconds: metric.RecoverySeconds,
				NoDataBehavior:  metric.NoDataBehavior,
				Description:     metric.Description,
			})
		}
		return result, nil
	}

	// 兼容分支：老客户端提交平铺规则集，按指标聚合、同等级去重
	index := map[string]*metricLevelsToSave{}
	order := make([]string, 0, len(s.Rules))
	for _, raw := range s.Rules {
		key := canonicalMetricKey(raw.MetricKey)
		if key == "" {
			return nil, fmt.Errorf("指标键不能为空")
		}
		item, ok := catalogKeys[key]
		if !ok {
			return nil, fmt.Errorf("指标 %s 不是平台在册指标，请从列表中选择", raw.MetricKey)
		}
		entry, ok := index[key]
		if !ok {
			entry = &metricLevelsToSave{
				Key:             key,
				Item:            item,
				Operator:        defaultString(strings.TrimSpace(raw.Operator), defaultString(item.DefaultOperator, ">=")),
				RecoverySeconds: raw.RecoverySeconds,
				NoDataBehavior:  raw.NoDataBehavior,
				Description:     raw.Description,
			}
			index[key] = entry
			order = append(order, key)
		}
		level, ok := normalizeAlertLevel(raw.AlertLevel)
		if !ok {
			level = AlertLevelOfDefaultLevel(item.DefaultLevel)
		}
		entry.Levels = append(entry.Levels, AlertLevelSpec{
			Level:          level,
			Threshold:      raw.Threshold,
			ForSeconds:     raw.ForSeconds,
			SilenceMinutes: defaultInt(raw.SilenceMinutes, recommendedSilenceMinutes(level)),
			Enabled:        raw.Enabled,
		})
	}
	result := make([]metricLevelsToSave, 0, len(order))
	for _, key := range order {
		result = append(result, *index[key])
	}
	return result, nil
}

// ruleLevelIdentity diff 的身份键：模板内"同指标同等级"只允许一行。
func ruleLevelIdentity(metricKey string, alertLevel string) string {
	return canonicalMetricKey(metricKey) + "|" + alertLevelIdentity(alertLevel)
}

// ruleOutOfScope 判断一条既有规则是否落在"本次应删除"的范围内。
//
// 全量口径（replaceAll=true）：提交集即完整期望状态，没被提交保留下来的都是待删；
// 增量口径（false）：只有本次提交里出现过的指标才参与删除，其余指标一概不动。
//
// 抽成函数是为了能被单测直接覆盖——这条判断一旦写错就是静默数据丢失，
// 靠集成测试"少删了一条"是发现不了的。
func ruleOutOfScope(replaceAll bool, submittedKeys map[string]struct{}, metricKey string) bool {
	if replaceAll {
		return true
	}
	_, touched := submittedKeys[canonicalMetricKey(metricKey)]
	return touched
}

// maxP0LevelsPerTemplate 单模板 P0 档位软上限：超过只提示不阻断。
const maxP0LevelsPerTemplate = 3

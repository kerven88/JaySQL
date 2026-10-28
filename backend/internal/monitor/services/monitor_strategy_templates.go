package services

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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

// ============================================================================
// 采集策略：模板 - 实例 两级配置
//
// 形态：
//   模板（insight_monitor_scrape_strategy_templates）= 一组采集策略项的集合
//   实例（insight_monitor_target_configs.strategy_template_id）= 只挂一个模板
//   实例不直接配策略，挂了就继承模板的全部策略项；个别实例可用 strategy_overrides 覆盖。
//
// 与「指标模板」的分工（两个都叫模板，务必分清）：
//   指标模板    = 采**哪些指标**        （insight_monitor_metric_templates）
//   采集策略模板 = **怎么采**              （本文件）
//   采集策略模板里的 metric_template 项指向指标模板，两级由此串成一条链。
//
// 生效方式：实时继承，不做快照固化。
// 理由：快照会让"改了模板为什么没生效"变成一个必须靠"批量刷新"按钮解决的问题，
// 而那个按钮一旦漏点，线上就长期处于"配置页说 A、实际跑 B"的状态。实时继承的代价
// 只是每次解析多读一次模板（有 15 秒缓存），换来的是配置与运行永不脱节。
// ============================================================================

// ---------------------------------------------------------------------------
// 策略项类型注册表
//
// 新增一种策略项的全部工作量就是在这里加一条：表结构不动（值存 JSON），
// 前端表单按 ValueKind 自动渲染、校验按 Spec 走、解析按 Default 兜底。
// 这是「策略类型可平滑扩展」承诺的落点。
// ---------------------------------------------------------------------------

const (
	StrategyItemInterval       = "scrape_interval" // 采集周期（秒，0=跟随平台）
	StrategyItemRetention      = "retention"       // 保留时长（小时）
	StrategyItemTimeout        = "timeout"         // 单次采集超时（秒）
	StrategyItemRetry          = "retry"           // 失败重试次数
	StrategyItemResourceSource = "resource_source" // 基础资源采集来源
	StrategyItemEngineSource   = "engine_source"   // 引擎指标采集来源
	StrategyItemMetricTemplate = "metric_template" // 关联的指标模板（空=用类型默认指标模板）
)

// 策略项值的类型。前端按它决定渲染成数字框、下拉还是文本框。
const (
	StrategyValueInt  = "int"
	StrategyValueEnum = "enum"
	StrategyValueText = "text"
)

// StrategyItemTypeSpec 一种策略项的定义。
type StrategyItemTypeSpec struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Kind string `json:"value_kind"`
	Unit string `json:"unit"`
	Min  int    `json:"min,omitempty"`
	Max  int    `json:"max,omitempty"`
	// Options 枚举可选项（Kind=enum 时必填）
	Options     []FilterOption `json:"options,omitempty"`
	Default     any            `json:"default"`
	Multiple    bool           `json:"multiple"`
	Description string         `json:"description"`
}

var strategyItemTypeSpecs = []StrategyItemTypeSpec{
	{
		Type: StrategyItemInterval, Name: "采集周期", Kind: StrategyValueInt, Unit: "秒",
		Min: 0, Max: 3600, Default: 0,
		Description: "0 表示跟随平台周期（insight_monitor_scrape_settings）；大于 0 为实例级周期，只能放慢、须为平台周期整数倍",
	},
	{
		Type: StrategyItemRetention, Name: "保留时长", Kind: StrategyValueInt, Unit: "小时",
		Min: 24, Max: 2160, Default: 168,
		Description: "原始采集点的保留时长，过期数据由清理任务按天分批删除",
	},
	{
		Type: StrategyItemTimeout, Name: "采集超时", Kind: StrategyValueInt, Unit: "秒",
		Min: 1, Max: 60, Default: 5,
		Description: "单次采集的超时上限；超时计为失败并累计连续失败次数",
	},
	{
		Type: StrategyItemRetry, Name: "失败重试", Kind: StrategyValueInt, Unit: "次",
		Min: 0, Max: 10, Default: 2,
		Description: "采集失败后的重试次数，0 表示不重试",
	},
	{
		Type: StrategyItemResourceSource, Name: "基础资源来源", Kind: StrategyValueEnum,
		Options: []FilterOption{
			{Value: "prometheus", Label: "Prometheus"},
			{Value: "direct", Label: "直连采集"},
			{Value: "none", Label: "不采集"},
		},
		Default:     "prometheus",
		Description: "CPU / 内存 / 磁盘 / 网络等主机层指标的采集来源",
	},
	{
		Type: StrategyItemEngineSource, Name: "引擎指标来源", Kind: StrategyValueEnum,
		Options: []FilterOption{
			{Value: "direct", Label: "直连采集"},
			{Value: "prometheus", Label: "Prometheus"},
			{Value: "none", Label: "不采集"},
		},
		Default:     "direct",
		Description: "数据库引擎层指标的采集来源",
	},
	{
		Type: StrategyItemMetricTemplate, Name: "指标模板", Kind: StrategyValueText, Unit: "",
		Default:     "",
		Description: "该策略模板配套的指标模板标识；留空表示沿用实例当前的指标模板绑定",
	},
}

var strategyItemSpecIndex = func() map[string]StrategyItemTypeSpec {
	index := make(map[string]StrategyItemTypeSpec, len(strategyItemTypeSpecs))
	for _, spec := range strategyItemTypeSpecs {
		index[spec.Type] = spec
	}
	return index
}()

// StrategyItemTypeSpecs 暴露注册表（前端据此动态渲染策略项表单）。
func StrategyItemTypeSpecs() []StrategyItemTypeSpec {
	out := make([]StrategyItemTypeSpec, len(strategyItemTypeSpecs))
	copy(out, strategyItemTypeSpecs)
	return out
}

func strategyItemSpec(itemType string) (StrategyItemTypeSpec, bool) {
	spec, ok := strategyItemSpecIndex[strings.ToLower(strings.TrimSpace(itemType))]
	return spec, ok
}

// ---------------------------------------------------------------------------
// 策略项值的解析 / 校验 / 展示
//
// 值统一存 JSON（int 存数字、enum/text 存字符串）。没有列级类型约束，
// 所以校验只能放在这里 —— 这是"通用行存储"换扩展性必须付的代价。
// ---------------------------------------------------------------------------

// parseStrategyValue 把任意输入（JSON 解出的 float64 / string / bool，或表单里的原生类型）
// 归一化成该策略项的规范值。非法值直接报错，不做静默纠正：
// 静默纠正会让"我以为配了 300 秒"和"实际跑 60 秒"长期共存。
func parseStrategyValue(spec StrategyItemTypeSpec, raw any) (any, error) {
	switch spec.Kind {
	case StrategyValueInt:
		value, err := toInt(raw)
		if err != nil {
			return nil, fmt.Errorf("%s 需要数字", spec.Name)
		}
		if value < spec.Min || value > spec.Max {
			return nil, fmt.Errorf("%s 需在 %d ~ %d 之间", spec.Name, spec.Min, spec.Max)
		}
		return value, nil
	case StrategyValueEnum:
		value := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
		for _, option := range spec.Options {
			if option.Value == value {
				return value, nil
			}
		}
		labels := make([]string, 0, len(spec.Options))
		for _, option := range spec.Options {
			labels = append(labels, option.Value)
		}
		return nil, fmt.Errorf("%s 只能取 %s", spec.Name, strings.Join(labels, " / "))
	case StrategyValueText:
		value := strings.TrimSpace(fmt.Sprint(raw))
		if len(value) > 64 {
			return nil, fmt.Errorf("%s 最长 64 个字符", spec.Name)
		}
		return value, nil
	}
	return nil, fmt.Errorf("未知的策略项类型 %s", spec.Type)
}

func toInt(raw any) (int, error) {
	switch value := raw.(type) {
	case int:
		return value, nil
	case int64:
		return int(value), nil
	case float64:
		return int(value), nil
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0, err
		}
		return int(parsed), nil
	case string:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return 0, fmt.Errorf("空值")
		}
		parsed, err := strconv.Atoi(trimmed)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	case nil:
		return 0, fmt.Errorf("空值")
	}
	return 0, fmt.Errorf("无法转换为数字")
}

// strategyValueText 把值渲染成人能读的一行（列表/详情直接展示，前端不再自己拼）。
func strategyValueText(spec StrategyItemTypeSpec, value any) string {
	switch spec.Kind {
	case StrategyValueInt:
		number, err := toInt(value)
		if err != nil {
			return "—"
		}
		if spec.Type == StrategyItemInterval && number == 0 {
			return "跟随平台"
		}
		if spec.Type == StrategyItemMetricTemplate && number == 0 {
			return "—"
		}
		if spec.Unit != "" {
			return fmt.Sprintf("%d %s", number, spec.Unit)
		}
		return strconv.Itoa(number)
	case StrategyValueEnum:
		text := strings.TrimSpace(fmt.Sprint(value))
		for _, option := range spec.Options {
			if option.Value == text {
				return option.Label
			}
		}
		if text == "" {
			return "—"
		}
		return text
	case StrategyValueText:
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			return "—"
		}
		return text
	}
	return fmt.Sprint(value)
}

func strategyValueJSON(value any) (datatypes.JSON, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return datatypes.JSON(raw), nil
}

// decodeStrategyValue 把库里的 JSON 解成 Go 值。解不出来时按该类型的默认值兜底，
// 并调用方感知不到 —— 脏数据不该让整个策略解析崩掉。
func decodeStrategyValue(spec StrategyItemTypeSpec, raw datatypes.JSON) (any, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false
	}
	normalized, err := parseStrategyValue(spec, value)
	if err != nil {
		return nil, false
	}
	return normalized, true
}

// ---------------------------------------------------------------------------
// 模板缓存
// ---------------------------------------------------------------------------

const strategyTemplateCacheTTL = 15 * time.Second

type strategyTemplateItem struct {
	Type   string `json:"type"`
	Key    string `json:"key"`
	Value  any    `json:"value"`
	Text   string `json:"text"`
	Unit   string `json:"unit"`
	Name   string `json:"name"`
	SortNo int    `json:"sort_no"`
}

type strategyTemplate struct {
	ID           uint64
	Name         string
	DisplayName  string
	DBType       string
	Description  string
	Builtin      bool
	IsDefault    bool
	Enabled      bool
	Items        map[string]strategyTemplateItem
	BoundCount   int
	UpdatedAt    string
	DisplayOrder []string
}

var (
	strategyTemplateMu       sync.RWMutex
	strategyTemplateLoadedAt time.Time
	strategyTemplatesByID    = map[uint64]*strategyTemplate{}
	strategyTemplatesByName  = map[string]*strategyTemplate{}
	strategyTemplateList     = []*strategyTemplate{}
)

func invalidateStrategyTemplateCache() {
	strategyTemplateMu.Lock()
	strategyTemplateLoadedAt = time.Time{}
	strategyTemplateMu.Unlock()
}

func itemIdentityKey(itemType, itemKey string) string {
	itemType = strings.ToLower(strings.TrimSpace(itemType))
	itemKey = strings.TrimSpace(itemKey)
	if itemKey == "" {
		return itemType
	}
	return itemType + "/" + itemKey
}

func loadStrategyTemplateCache() (map[uint64]*strategyTemplate, map[string]*strategyTemplate, []*strategyTemplate) {
	strategyTemplateMu.RLock()
	if !strategyTemplateLoadedAt.IsZero() && time.Since(strategyTemplateLoadedAt) < strategyTemplateCacheTTL {
		byID, byName, list := strategyTemplatesByID, strategyTemplatesByName, strategyTemplateList
		strategyTemplateMu.RUnlock()
		return byID, byName, list
	}
	strategyTemplateMu.RUnlock()

	byID := map[uint64]*strategyTemplate{}
	byName := map[string]*strategyTemplate{}
	list := make([]*strategyTemplate, 0)
	if global.App != nil && global.App.DB != nil {
		records := make([]monitorModels.InsightMonitorScrapeStrategyTemplates, 0)
		if err := global.App.DB.Order("db_type ASC, is_default DESC, name ASC").Find(&records).Error; err == nil {
			itemsByTemplate := loadStrategyItemsByTemplate()
			for i := range records {
				record := records[i]
				tmpl := &strategyTemplate{
					ID:          record.ID,
					Name:        record.Name,
					DisplayName: record.DisplayName,
					DBType:      record.DBType,
					Description: record.Description,
					Builtin:     record.Builtin,
					IsDefault:   record.IsDefault,
					Enabled:     record.Enabled,
					Items:       map[string]strategyTemplateItem{},
				}
				if record.Model != nil {
					tmpl.UpdatedAt = time.Time(record.UpdatedAt).Format(time.DateTime)
				}
				for _, row := range itemsByTemplate[record.ID] {
					spec, ok := strategyItemSpec(row.ItemType)
					if !ok {
						// 库里躺着注册表里已经没有的类型（版本回退 / 手工写入）：
						// 跳过而不是崩掉，否则一个脏行会让所有模板都读不出来
						continue
					}
					if !row.Enabled {
						continue
					}
					value, ok := decodeStrategyValue(spec, row.ValueJSON)
					if !ok {
						continue
					}
					tmpl.Items[itemIdentityKey(row.ItemType, row.ItemKey)] = strategyTemplateItem{
						Type: row.ItemType, Key: row.ItemKey, Value: value,
						Text: strategyValueText(spec, value), Unit: spec.Unit,
						Name: spec.Name, SortNo: row.SortOrder,
					}
				}
				byID[record.ID] = tmpl
				byName[strings.ToLower(record.Name)] = tmpl
				list = append(list, tmpl)
			}
		}
	}

	strategyTemplateMu.Lock()
	strategyTemplatesByID, strategyTemplatesByName, strategyTemplateList = byID, byName, list
	strategyTemplateLoadedAt = time.Now()
	strategyTemplateMu.Unlock()
	return byID, byName, list
}

func loadStrategyItemsByTemplate() map[uint64][]monitorModels.InsightMonitorScrapeStrategyItems {
	result := map[uint64][]monitorModels.InsightMonitorScrapeStrategyItems{}
	if global.App == nil || global.App.DB == nil {
		return result
	}
	rows := make([]monitorModels.InsightMonitorScrapeStrategyItems, 0)
	if err := global.App.DB.Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
		return result
	}
	for _, row := range rows {
		result[row.TemplateID] = append(result[row.TemplateID], row)
	}
	return result
}

func strategyTemplateByID(id uint64) *strategyTemplate {
	byID, _, _ := loadStrategyTemplateCache()
	return byID[id]
}

func strategyTemplateByName(name string) *strategyTemplate {
	_, byName, _ := loadStrategyTemplateCache()
	return byName[strings.ToLower(strings.TrimSpace(name))]
}

// defaultStrategyTemplate 取某个数据库类型的默认模板（兜底用）。
func defaultStrategyTemplate(dbType string) *strategyTemplate {
	_, _, list := loadStrategyTemplateCache()
	engine := resolveEngine(dbType)
	for _, tmpl := range list {
		if tmpl.DBType == engine && tmpl.IsDefault && tmpl.Enabled {
			return tmpl
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 播种：每个已实现引擎一个内置默认模板
//
// 与指标模板同一个道理：默认模板是"未挂载 / 挂载失效"的兜底，
// 缺了它就会出现"实例没配策略 → 不采集"的静默盲区。
// ---------------------------------------------------------------------------

// EnsureScrapeStrategyTemplates 幂等播种。已存在的模板只补齐缺失的策略项，
// 不覆盖已有值 —— 用户改过的配置不能被发版悄悄改回去。
func EnsureScrapeStrategyTemplates(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("数据库未就绪")
	}
	for _, engine := range monitorableEngineKeys() {
		if err := ensureStrategyTemplateForEngine(db, engine); err != nil {
			return err
		}
	}
	invalidateStrategyTemplateCache()
	return nil
}

func ensureStrategyTemplateForEngine(db *gorm.DB, engine string) error {
	name := StrategyDefaultTemplateName(engine)
	label := engine
	if capability, ok := engineCapabilityIndex[engine]; ok {
		label = capability.Label
	}
	var record monitorModels.InsightMonitorScrapeStrategyTemplates
	err := db.Where("name = ?", name).Take(&record).Error
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			return err
		}
		// 展示名不再带「（类型）」后缀：列表已有独立的「数据库类型」列，
		// 重复一遍类型名只会让模板列变长、两个默认模板看起来几乎一样。
		record = monitorModels.InsightMonitorScrapeStrategyTemplates{
			Model:       &commonModels.Model{},
			Name:        name,
			DisplayName: strategyDefaultTemplateDisplayName,
			DBType:      engine,
			Description: fmt.Sprintf("%s 的内置默认采集策略；未挂载模板的实例自动套用", label),
			Builtin:     true,
			IsDefault:   true,
			Enabled:     true,
		}
		if err := db.Create(&record).Error; err != nil {
			return err
		}
	}
	// 存量数据兼容：早期版本播种的是「默认采集策略（MySQL）」，这里把它收敛成不带括号的统一展示名。
	// 只处理内置默认模板，用户自建模板的展示名一个字都不动。
	if record.Builtin && record.IsDefault && record.DisplayName != strategyDefaultTemplateDisplayName {
		if strings.HasPrefix(record.DisplayName, strategyDefaultTemplateDisplayName) {
			db.Model(&monitorModels.InsightMonitorScrapeStrategyTemplates{}).
				Where("id = ?", record.ID).
				Update("display_name", strategyDefaultTemplateDisplayName)
			record.DisplayName = strategyDefaultTemplateDisplayName
		}
	}
	return seedStrategyItems(db, record.ID)
}

// seedStrategyItems 补齐模板缺失的策略项。只插入不存在的类型，已有值原样保留。
func seedStrategyItems(db *gorm.DB, templateID uint64) error {
	existing := map[string]struct{}{}
	rows := make([]monitorModels.InsightMonitorScrapeStrategyItems, 0)
	if err := db.Select("item_type, item_key").Where("template_id = ?", templateID).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		existing[itemIdentityKey(row.ItemType, row.ItemKey)] = struct{}{}
	}
	now := commonModels.LocalTime(time.Now())
	missing := make([]monitorModels.InsightMonitorScrapeStrategyItems, 0, len(strategyItemTypeSpecs))
	for i, spec := range strategyItemTypeSpecs {
		if _, ok := existing[itemIdentityKey(spec.Type, "")]; ok {
			continue
		}
		raw, err := strategyValueJSON(spec.Default)
		if err != nil {
			continue
		}
		missing = append(missing, monitorModels.InsightMonitorScrapeStrategyItems{
			TemplateID: templateID, ItemType: spec.Type, ItemKey: "",
			ValueJSON: raw, Enabled: true, SortOrder: i * 10,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	if len(missing) == 0 {
		return nil
	}
	return db.CreateInBatches(&missing, 50).Error
}

// StrategyDefaultTemplateName 内置默认模板的标识。播种与展示共用同一个规则，
// 避免"播种写一个名字、别处拼另一个名字"导致兜底找不到模板。
func StrategyDefaultTemplateName(engine string) string {
	return "default-" + resolveEngine(engine)
}

// strategyDefaultTemplateDisplayName 内置默认模板的统一展示名。
// 不带「（类型）」后缀：列表已有独立的「数据库类型」列，模板列再写一遍类型名是冗余，
// 而且会让 MySQL / PostgreSQL 两个默认模板看起来几乎一样。
const strategyDefaultTemplateDisplayName = "默认采集策略"

// ---------------------------------------------------------------------------
// 生效策略解析（Resolve）
//
// 优先级：实例覆盖 > 挂载模板的启用项 > 平台默认。
// 回退顺序：挂载模板不可用（未挂 / 被删 / 停用 / 跨类型）→ 该 db_type 的默认模板。
// ---------------------------------------------------------------------------

const (
	// 策略项取值的来源，页面用它回答"这个数字是哪来的"
	StrategySourceOverride = "override"
	StrategySourceTemplate = "template"
	StrategySourceDefault  = "default"
	// 模板归属的来源
	StrategySourceFallback = "fallback"
)

type ResolvedStrategyItem struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Value     any    `json:"value"`
	ValueText string `json:"value_text"`
	Unit      string `json:"unit"`
	Source    string `json:"source"`
}

type ResolvedInstanceStrategy struct {
	InstanceID     string                 `json:"instance_id"`
	DBType         string                 `json:"db_type"`
	TemplateID     uint64                 `json:"template_id"`
	TemplateName   string                 `json:"template_name"`
	TemplateLabel  string                 `json:"template_label"`
	TemplateState  string                 `json:"template_state"`
	Fallback       bool                   `json:"fallback"`
	FallbackReason string                 `json:"fallback_reason"`
	Items          []ResolvedStrategyItem `json:"items"`
	OverrideTypes  []string               `json:"override_types"`
}

// resolveTemplateForInstanceRow 决定实例实际套用哪个模板。
// 判定顺序即回退链：显式挂载 → 挂载失效 → 类型默认。返回 nil 表示连默认模板都没有
// （该引擎未播种，属于部署异常，调用方按平台默认跑，不静默停采）。
func resolveTemplateForInstanceRow(dbType string, templateID *uint64) (*strategyTemplate, bool, string) {
	engine := resolveEngine(dbType)
	if templateID != nil && *templateID > 0 {
		tmpl := strategyTemplateByID(*templateID)
		switch {
		case tmpl == nil:
			return defaultStrategyTemplate(engine), true, "挂载的采集策略模板已不存在，已回退到默认模板"
		case !tmpl.Enabled:
			return defaultStrategyTemplate(engine), true, "挂载的采集策略模板已停用，已回退到默认模板"
		case tmpl.DBType != engine:
			return defaultStrategyTemplate(engine), true, fmt.Sprintf("挂载的采集策略模板属于 %s，与实例类型 %s 不一致，已回退到默认模板",
				tmpl.DBType, engine)
		default:
			return tmpl, false, ""
		}
	}
	fallback := defaultStrategyTemplate(engine)
	if fallback == nil {
		return nil, true, "该数据库类型暂无可用的默认采集策略模板，按平台默认值运行"
	}
	return fallback, true, "未挂载采集策略模板，套用默认模板"
}

// ResolveInstanceStrategy 计算一个实例当前生效的完整策略。
// 这是"模板 + 覆盖"合流的唯一入口：挂载、切换、解绑、查看都走它，
// 保证页面上看到的和实际采集用的永远是同一份结果。
func ResolveInstanceStrategy(instanceID, dbType string, templateID *uint64, overrides datatypes.JSON) ResolvedInstanceStrategy {
	tmpl, fallback, reason := resolveTemplateForInstanceRow(dbType, templateID)
	parsedOverrides, overrideTypes := parseStrategyOverrides(overrides)

	resolved := ResolvedInstanceStrategy{
		InstanceID:     instanceID,
		DBType:         resolveEngine(dbType),
		Fallback:       fallback,
		FallbackReason: reason,
		Items:          make([]ResolvedStrategyItem, 0, len(strategyItemTypeSpecs)),
		OverrideTypes:  overrideTypes,
	}
	if tmpl != nil {
		resolved.TemplateID = tmpl.ID
		resolved.TemplateName = tmpl.Name
		resolved.TemplateLabel = tmpl.DisplayName
		if tmpl.DisplayName == "" {
			resolved.TemplateLabel = tmpl.Name
		}
		resolved.TemplateState = "bound"
		if fallback {
			resolved.TemplateState = "fallback"
		}
	} else {
		resolved.TemplateState = "unbound"
	}

	for _, spec := range strategyItemTypeSpecs {
		identity := itemIdentityKey(spec.Type, "")
		item := ResolvedStrategyItem{
			Type: spec.Type, Name: spec.Name, Unit: spec.Unit,
			Value: spec.Default, Source: StrategySourceDefault,
		}
		if tmpl != nil {
			if owned, ok := tmpl.Items[identity]; ok {
				item.Value = owned.Value
				item.Source = StrategySourceTemplate
			}
		}
		if override, ok := parsedOverrides[spec.Type]; ok {
			item.Value = override
			item.Source = StrategySourceOverride
		}
		item.ValueText = strategyValueText(spec, item.Value)
		resolved.Items = append(resolved.Items, item)
	}
	return resolved
}

// strategyItemValue 从解析结果里取某个策略项的整数值（配置项几乎都是整数，
// 单独封装是为了避免调用方到处做类型断言）。
func strategyItemValue(resolved ResolvedInstanceStrategy, itemType string) (int, bool) {
	for _, item := range resolved.Items {
		if item.Type != itemType {
			continue
		}
		value, err := toInt(item.Value)
		if err != nil {
			return 0, false
		}
		return value, true
	}
	return 0, false
}

func strategyItemTextValue(resolved ResolvedInstanceStrategy, itemType string) (string, bool) {
	for _, item := range resolved.Items {
		if item.Type != itemType {
			continue
		}
		return strings.TrimSpace(fmt.Sprint(item.Value)), true
	}
	return "", false
}

// parseStrategyOverrides 解析实例级覆盖。非法键（注册表里没有的类型）直接丢弃：
// 留着它们只会让页面显示一个没人消费的配置项。
func parseStrategyOverrides(raw datatypes.JSON) (map[string]any, []string) {
	result := map[string]any{}
	types := make([]string, 0)
	if len(raw) == 0 {
		return result, types
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return result, types
	}
	for key, value := range decoded {
		spec, ok := strategyItemSpec(key)
		if !ok {
			continue
		}
		normalized, err := parseStrategyValue(spec, value)
		if err != nil {
			continue
		}
		result[spec.Type] = normalized
		types = append(types, spec.Type)
	}
	sort.Strings(types)
	return result, types
}

// applyResolvedStrategy 把解析结果回写到实例的缓存列。
//
// 为什么要回写而不是让采集器现算：采集是每轮都要走的热路径，
// 让它每次重新解析"模板 → 覆盖 → 平台默认"会把一次读取放大成多表联查 + JSON 解析。
// 缓存列换来的是采集器只读一行，模板侧的复杂度全留在配置写入路径上。
//
// metric_template 特殊处理：模板里留空时不覆盖实例已有的指标模板绑定，
// 否则"只想用策略模板管周期"的用户会被顺手清掉指标模板。
func applyResolvedStrategy(tx *gorm.DB, instanceID string, resolved ResolvedInstanceStrategy) error {
	updates := map[string]any{}
	if value, ok := strategyItemValue(resolved, StrategyItemInterval); ok {
		updates["scrape_interval_seconds"] = value
	}
	if value, ok := strategyItemValue(resolved, StrategyItemRetention); ok {
		updates["retention_hours"] = value
	}
	if value, ok := strategyItemValue(resolved, StrategyItemTimeout); ok {
		updates["timeout_seconds"] = value
	}
	if value, ok := strategyItemValue(resolved, StrategyItemRetry); ok {
		updates["retry_count"] = value
	}
	if value, ok := strategyItemTextValue(resolved, StrategyItemResourceSource); ok && value != "" {
		updates["resource_source"] = value
	}
	if value, ok := strategyItemTextValue(resolved, StrategyItemEngineSource); ok && value != "" {
		updates["engine_source"] = value
	}
	if value, ok := strategyItemTextValue(resolved, StrategyItemMetricTemplate); ok && value != "" {
		updates["metrics_template"] = value
	}
	if len(updates) == 0 {
		return nil
	}
	updates["updated_at"] = time.Now()
	return tx.Model(&monitorModels.InsightMonitorTargetConfigs{}).
		Where("instance_id = ?", instanceID).Updates(updates).Error
}

// reapplyInstanceStrategy 重新解析并回写单个实例（挂载 / 解绑 / 模板变更后调用）。
func reapplyInstanceStrategy(tx *gorm.DB, instance monitorModels.InsightMonitorTargetConfigs) (ResolvedInstanceStrategy, error) {
	resolved := ResolveInstanceStrategy(instance.InstanceID, string(instance.DBType), instance.StrategyTemplateID, instance.StrategyOverrides)
	if err := applyResolvedStrategy(tx, instance.InstanceID, resolved); err != nil {
		return resolved, err
	}
	return resolved, nil
}

// ---------------------------------------------------------------------------
// DTO
// ---------------------------------------------------------------------------

type StrategyTemplateItem struct {
	ID           uint64 `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	DBType       string `json:"db_type"`
	DBTypeLabel  string `json:"db_type_label"`
	Description  string `json:"description"`
	Builtin      bool   `json:"builtin"`
	IsDefault    bool   `json:"is_default"`
	Enabled      bool   `json:"enabled"`
	ItemCount    int    `json:"item_count"`
	BoundCount   int    `json:"bound_instances"`
	EffectiveNum int    `json:"effective_instances"`
	// Summary 策略概要（"周期 跟随平台 · 保留 168 小时 · …"）：列表里一眼看出模板内容，
	// 不必逐个点开详情。由后端拼好，避免前端再维护一份"每项怎么显示"的口径。
	Summary   string `json:"summary"`
	UpdatedAt string `json:"updated_at"`
}

type StrategyTemplateItemRow struct {
	ID        uint64 `json:"id"`
	ItemType  string `json:"item_type"`
	ItemKey   string `json:"item_key"`
	TypeName  string `json:"type_name"`
	Value     any    `json:"value"`
	ValueText string `json:"value_text"`
	Unit      string `json:"unit"`
	Enabled   bool   `json:"enabled"`
	SortOrder int    `json:"sort_order"`
	// Orphan 该项类型已不在注册表中（版本回退 / 手工写入），页面需要标出来而不是静默隐藏
	Orphan bool `json:"orphan"`
}

type StrategyTemplateListData struct {
	GeneratedAt string                 `json:"generated_at"`
	DBTypes     []FilterOption         `json:"db_types"`
	Engines     []EngineCapability     `json:"engines"`
	Types       []StrategyItemTypeSpec `json:"types"`
	Templates   []StrategyTemplateItem `json:"templates"`
}

type StrategyTemplateDetailData struct {
	GeneratedAt string                     `json:"generated_at"`
	Template    StrategyTemplateItem       `json:"template"`
	Items       []StrategyTemplateItemRow  `json:"items"`
	Types       []StrategyItemTypeSpec     `json:"types"`
	Instances   []StrategyBoundInstanceRow `json:"instances"`
}

type StrategyBoundInstanceRow struct {
	InstanceID   string   `json:"instance_id"`
	Name         string   `json:"name"`
	DBType       string   `json:"db_type"`
	Enabled      bool     `json:"enabled"`
	Business     string   `json:"business"`
	GroupName    string   `json:"group_name"`
	Fallback     bool     `json:"fallback"`
	FallbackNote string   `json:"fallback_note"`
	Overrides    []string `json:"overrides"`
}

type StrategyBindResult struct {
	TemplateID   uint64   `json:"template_id"`
	TemplateName string   `json:"template_name"`
	Bound        int      `json:"bound"`
	Skipped      int      `json:"skipped"`
	Mode         string   `json:"mode"`
	Notes        []string `json:"notes"`
}

type InstanceStrategyData struct {
	GeneratedAt string                   `json:"generated_at"`
	InstanceID  string                   `json:"instance_id"`
	Resolved    ResolvedInstanceStrategy `json:"resolved"`
}

// ---------------------------------------------------------------------------
// 读取
// ---------------------------------------------------------------------------

func strategyTemplateItemOf(tmpl *strategyTemplate, bound, effective int) StrategyTemplateItem {
	label := tmpl.DBType
	if capability, ok := engineCapabilityIndex[tmpl.DBType]; ok {
		label = capability.Label
	}
	return StrategyTemplateItem{
		ID:           tmpl.ID,
		Name:         tmpl.Name,
		DisplayName:  tmpl.DisplayName,
		DBType:       tmpl.DBType,
		DBTypeLabel:  label,
		Description:  tmpl.Description,
		Builtin:      tmpl.Builtin,
		IsDefault:    tmpl.IsDefault,
		Enabled:      tmpl.Enabled,
		ItemCount:    len(tmpl.Items),
		BoundCount:   bound,
		EffectiveNum: effective,
		Summary:      strategySummaryOf(tmpl),
		UpdatedAt:    tmpl.UpdatedAt,
	}
}

// strategySummaryOf 拼一行策略概要。跳过指标模板项（它的值常常是空的，
// 显示"指标模板 —"只会占地方），其余按注册表顺序输出。
func strategySummaryOf(tmpl *strategyTemplate) string {
	if len(tmpl.Items) == 0 {
		return "未配置策略项"
	}
	parts := make([]string, 0, len(strategyItemTypeSpecs))
	for _, spec := range strategyItemTypeSpecs {
		if spec.Type == StrategyItemMetricTemplate {
			continue
		}
		item, ok := tmpl.Items[itemIdentityKey(spec.Type, "")]
		if !ok {
			continue
		}
		parts = append(parts, spec.Name+" "+item.Text)
	}
	if len(parts) == 0 {
		return "未配置策略项"
	}
	return strings.Join(parts, " · ")
}

// strategyBindingCounts 统计模板挂载情况。
// bound = 实例行字面挂载该模板的数量（删除保护看这个，避免留下悬空引用）；
// effective = 解析后真正套用该模板的数量（停用模板的生效实例会算在默认模板头上）。
func strategyBindingCounts() (map[uint64]int, map[uint64]int) {
	bound := map[uint64]int{}
	effective := map[uint64]int{}
	if global.App == nil || global.App.DB == nil {
		return bound, effective
	}
	type row struct {
		InstanceID         string
		DbType             string
		StrategyTemplateID *uint64
	}
	rows := make([]row, 0)
	if err := global.App.DB.Table("insight_monitor_target_configs").
		Select("instance_id, db_type, strategy_template_id").Find(&rows).Error; err != nil {
		return bound, effective
	}
	for _, item := range rows {
		if item.StrategyTemplateID != nil && *item.StrategyTemplateID > 0 {
			bound[*item.StrategyTemplateID]++
		}
		tmpl, _, _ := resolveTemplateForInstanceRow(item.DbType, item.StrategyTemplateID)
		if tmpl != nil {
			effective[tmpl.ID]++
		}
	}
	return bound, effective
}

type GetStrategyTemplateListService struct {
	DBType string
	C      *gin.Context
}

func (s *GetStrategyTemplateListService) Run() (*StrategyTemplateListData, error) {
	if err := EnsureMetricTemplates(); err != nil {
		global.App.Log.Warnf("seed metric templates failed: %v", err)
	}
	if err := EnsureScrapeStrategyTemplates(global.App.DB); err != nil {
		global.App.Log.Warnf("seed scrape strategy templates failed: %v", err)
	}
	_, _, list := loadStrategyTemplateCache()
	bound, effective := strategyBindingCounts()
	engineFilter := resolveEngine(s.DBType)

	data := &StrategyTemplateListData{
		GeneratedAt: time.Now().Format(time.DateTime),
		Engines:     EngineCapabilityList(),
		Types:       StrategyItemTypeSpecs(),
		Templates:   make([]StrategyTemplateItem, 0, len(list)),
	}
	seen := map[string]struct{}{}
	for _, tmpl := range list {
		if engineFilter != "" && tmpl.DBType != engineFilter {
			continue
		}
		item := strategyTemplateItemOf(tmpl, bound[tmpl.ID], effective[tmpl.ID])
		if tmpl.Builtin {
			item.UpdatedAt = ""
		}
		data.Templates = append(data.Templates, item)
		if _, ok := seen[tmpl.DBType]; !ok {
			seen[tmpl.DBType] = struct{}{}
			label := tmpl.DBType
			if capability, ok := engineCapabilityIndex[tmpl.DBType]; ok {
				label = capability.Label
			}
			data.DBTypes = append(data.DBTypes, FilterOption{Value: tmpl.DBType, Label: label})
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

type GetStrategyTemplateDetailService struct {
	ID uint64
	C  *gin.Context
}

func (s *GetStrategyTemplateDetailService) Run() (*StrategyTemplateDetailData, error) {
	tmpl := strategyTemplateByID(s.ID)
	if tmpl == nil {
		return nil, fmt.Errorf("采集策略模板不存在")
	}
	bound, effective := strategyBindingCounts()

	// 详情要展示**未启用**的策略项（页面要能重新勾上），所以绕开缓存直接读库
	items := make([]StrategyTemplateItemRow, 0, len(strategyItemTypeSpecs))
	rows := make([]monitorModels.InsightMonitorScrapeStrategyItems, 0)
	if global.App != nil && global.App.DB != nil {
		if err := global.App.DB.Where("template_id = ?", tmpl.ID).
			Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("读取策略项失败：%w", err)
		}
	}
	for _, row := range rows {
		entry := StrategyTemplateItemRow{
			ID: row.ID, ItemType: row.ItemType, ItemKey: row.ItemKey,
			Enabled: row.Enabled, SortOrder: row.SortOrder,
		}
		spec, ok := strategyItemSpec(row.ItemType)
		if !ok {
			entry.Orphan = true
			entry.TypeName = row.ItemType
			items = append(items, entry)
			continue
		}
		entry.TypeName = spec.Name
		entry.Unit = spec.Unit
		if value, ok := decodeStrategyValue(spec, row.ValueJSON); ok {
			entry.Value = value
			entry.ValueText = strategyValueText(spec, value)
		} else {
			entry.Value = spec.Default
			entry.ValueText = strategyValueText(spec, spec.Default)
		}
		items = append(items, entry)
	}

	instances := strategyBoundInstances(tmpl.ID)
	return &StrategyTemplateDetailData{
		GeneratedAt: time.Now().Format(time.DateTime),
		Template:    strategyTemplateItemOf(tmpl, bound[tmpl.ID], effective[tmpl.ID]),
		Items:       items,
		Types:       StrategyItemTypeSpecs(),
		Instances:   instances,
	}, nil
}

// strategyBoundInstances 列出字面挂载该模板的实例，并标出哪些实际已经回退
// （模板停用 / 跨类型时，挂载关系还在但策略不生效 —— 这个差异必须让人看见）。
func strategyBoundInstances(templateID uint64) []StrategyBoundInstanceRow {
	result := make([]StrategyBoundInstanceRow, 0)
	if global.App == nil || global.App.DB == nil {
		return result
	}
	type row struct {
		InstanceID string
		Name       string
		DbType     string
		Enabled    bool
		Business   string
		GroupName  string
		Overrides  datatypes.JSON
	}
	rows := make([]row, 0)
	if err := global.App.DB.Table("insight_monitor_target_configs").
		Select("instance_id, name, db_type, enabled, business, group_name, strategy_overrides").
		Where("strategy_template_id = ?", templateID).Find(&rows).Error; err != nil {
		return result
	}
	tmpl := strategyTemplateByID(templateID)
	for _, item := range rows {
		entry := StrategyBoundInstanceRow{
			InstanceID: item.InstanceID, Name: item.Name, DBType: string(item.DbType),
			Enabled: item.Enabled, Business: item.Business, GroupName: item.GroupName,
		}
		_, overrides := parseStrategyOverrides(item.Overrides)
		entry.Overrides = overrides
		if tmpl != nil && (!tmpl.Enabled || tmpl.DBType != resolveEngine(item.DbType)) {
			entry.Fallback = true
			entry.FallbackNote = "该模板对此实例不生效（已停用或类型不一致），实际套用默认模板"
		}
		result = append(result, entry)
	}
	return result
}

// GetInstanceStrategyService 查看某个实例当前生效的采集策略（含每一项来自哪）。
type GetInstanceStrategyService struct {
	InstanceID string
	C          *gin.Context
}

func (s *GetInstanceStrategyService) Run() (*InstanceStrategyData, error) {
	if global.App == nil || global.App.DB == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	var record monitorModels.InsightMonitorTargetConfigs
	if err := global.App.DB.Where("instance_id = ?", s.InstanceID).Take(&record).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("实例不存在")
		}
		return nil, err
	}
	resolved := ResolveInstanceStrategy(record.InstanceID, string(record.DBType), record.StrategyTemplateID, record.StrategyOverrides)
	return &InstanceStrategyData{
		GeneratedAt: time.Now().Format(time.DateTime),
		InstanceID:  record.InstanceID,
		Resolved:    resolved,
	}, nil
}

// ---------------------------------------------------------------------------
// 写入：新建 / 修改 / 删除 / 挂载 / 解绑
// ---------------------------------------------------------------------------

func normalizeStrategyTemplateName(value string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(value))
	if name == "" {
		return "", fmt.Errorf("模板标识不能为空")
	}
	if len(name) > 64 {
		return "", fmt.Errorf("模板标识最长 64 个字符")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return "", fmt.Errorf("模板标识只能包含小写字母、数字、中划线与下划线")
	}
	return name, nil
}

// validateStrategyItems 校验并归一化策略项。
// 只接受注册表里存在的类型；值按 Spec 校验；重复类型以最后一条为准。
func validateStrategyItems(raw []forms.StrategyItemInput) ([]forms.StrategyItemInput, error) {
	result := make([]forms.StrategyItemInput, 0, len(raw))
	seen := map[string]struct{}{}
	rejected := make([]string, 0)
	for _, input := range raw {
		spec, ok := strategyItemSpec(input.ItemType)
		if !ok {
			if strings.TrimSpace(input.ItemType) != "" {
				rejected = append(rejected, fmt.Sprintf("%s（未注册的策略项类型）", input.ItemType))
			}
			continue
		}
		identity := itemIdentityKey(spec.Type, input.ItemKey)
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		value, err := parseStrategyValue(spec, input.Value)
		if err != nil {
			rejected = append(rejected, fmt.Sprintf("%s：%s", spec.Name, err.Error()))
			continue
		}
		result = append(result, forms.StrategyItemInput{ItemType: spec.Type, ItemKey: strings.TrimSpace(input.ItemKey), Value: value})
	}
	if len(rejected) > 0 {
		return nil, fmt.Errorf("以下策略项无法保存：%s", strings.Join(rejected, "、"))
	}
	return result, nil
}

func replaceStrategyItems(tx *gorm.DB, templateID uint64, items []forms.StrategyItemInput) error {
	if err := tx.Where("template_id = ?", templateID).
		Delete(&monitorModels.InsightMonitorScrapeStrategyItems{}).Error; err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}
	now := commonModels.LocalTime(time.Now())
	rows := make([]monitorModels.InsightMonitorScrapeStrategyItems, 0, len(items))
	for index, item := range items {
		raw, err := strategyValueJSON(item.Value)
		if err != nil {
			return err
		}
		rows = append(rows, monitorModels.InsightMonitorScrapeStrategyItems{
			TemplateID: templateID, ItemType: item.ItemType, ItemKey: item.ItemKey,
			ValueJSON: raw, Enabled: true, SortOrder: index * 10,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	return tx.CreateInBatches(&rows, 100).Error
}

type CreateStrategyTemplateService struct {
	*forms.SaveStrategyTemplateForm
	C *gin.Context
}

func (s *CreateStrategyTemplateService) Run() (*StrategyTemplateItem, error) {
	if err := EnsureScrapeStrategyTemplates(global.App.DB); err != nil {
		global.App.Log.Warnf("seed scrape strategy templates failed: %v", err)
	}
	name, err := normalizeStrategyTemplateName(s.Name)
	if err != nil {
		return nil, err
	}
	engineKey := resolveEngine(s.DBType)
	if !engineImplemented(engineKey) {
		return nil, fmt.Errorf("暂不支持为 %s 创建采集策略模板（该引擎尚未实现监控采集）", s.DBType)
	}
	displayName := strings.TrimSpace(s.DisplayName)
	if displayName == "" {
		displayName = name
	}
	var count int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorScrapeStrategyTemplates{}).
		Where("name = ?", name).Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, fmt.Errorf("模板标识 %s 已存在，请换一个", name)
	}

	items := make([]forms.StrategyItemInput, 0)
	if len(s.Items) > 0 {
		validated, err := validateStrategyItems(s.Items)
		if err != nil {
			return nil, err
		}
		items = validated
	} else {
		// 没给策略项时，从 copy_from 指定的模板（默认取该类型默认模板）复制一份：
		// "以默认模板为基础改两个值"是最高频的用法。
		source := strategyTemplateByName(strings.TrimSpace(s.CopyFrom))
		if source == nil {
			source = defaultStrategyTemplate(engineKey)
		}
		if source == nil {
			return nil, fmt.Errorf("没有可复制的模板，请直接配置策略项")
		}
		for _, spec := range strategyItemTypeSpecs {
			if owned, ok := source.Items[itemIdentityKey(spec.Type, "")]; ok {
				items = append(items, forms.StrategyItemInput{ItemType: spec.Type, Value: owned.Value})
			}
		}
	}

	record := monitorModels.InsightMonitorScrapeStrategyTemplates{
		Model:       &commonModels.Model{},
		Name:        name,
		DisplayName: displayName,
		DBType:      engineKey,
		Description: strings.TrimSpace(s.Description),
		Builtin:     false,
		IsDefault:   false,
		Enabled:     s.Enabled == nil || *s.Enabled,
	}
	err = global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return replaceStrategyItems(tx, record.ID, items)
	})
	if err != nil {
		return nil, fmt.Errorf("创建采集策略模板失败：%w", err)
	}
	invalidateStrategyTemplateCache()
	tmpl := strategyTemplateByID(record.ID)
	if tmpl == nil {
		return nil, fmt.Errorf("模板读取失败")
	}
	item := strategyTemplateItemOf(tmpl, 0, 0)
	item.UpdatedAt = time.Now().Format(time.DateTime)
	return &item, nil
}

type UpdateStrategyTemplateService struct {
	ID uint64
	*forms.UpdateStrategyTemplateForm
	C *gin.Context
}

func (s *UpdateStrategyTemplateService) Run() (*StrategyTemplateItem, error) {
	if global.App == nil || global.App.DB == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	var record monitorModels.InsightMonitorScrapeStrategyTemplates
	if err := global.App.DB.Where("id = ?", s.ID).Take(&record).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("采集策略模板不存在")
		}
		return nil, err
	}
	if value := strings.TrimSpace(s.Name); value != "" &&
		strings.ToLower(value) != strings.ToLower(strings.TrimSpace(record.Name)) {
		return nil, fmt.Errorf("模板标识不可修改（实例靠 ID 挂载，改名会造成展示与引用错位）")
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
			return nil, fmt.Errorf("内置默认模板不可停用；如需停用请新建自定义模板并切换到它")
		}
		updates["enabled"] = *s.Enabled
	}

	needReapply := false
	err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if s.Items != nil {
			items, err := validateStrategyItems(*s.Items)
			if err != nil {
				return err
			}
			if err := replaceStrategyItems(tx, record.ID, items); err != nil {
				return err
			}
			updates["updated_at"] = time.Now()
			needReapply = true
		}
		if len(updates) > 0 {
			if err := tx.Model(&monitorModels.InsightMonitorScrapeStrategyTemplates{}).
				Where("id = ?", record.ID).Updates(updates).Error; err != nil {
				return err
			}
		}
		// 启用状态变化会改变一批实例的回退判定，同样需要重算
		if s.Enabled != nil {
			needReapply = true
		}
		if !needReapply {
			return nil
		}
		return reapplyInstancesOfTemplate(tx, record.ID)
	})
	if err != nil {
		return nil, err
	}
	invalidateStrategyTemplateCache()
	tmpl := strategyTemplateByID(record.ID)
	if tmpl == nil {
		return nil, fmt.Errorf("模板读取失败")
	}
	bound, effective := strategyBindingCounts()
	return strategyTemplateItemOfWithUpdate(tmpl, bound[tmpl.ID], effective[tmpl.ID], record.ID), nil
}

func strategyTemplateItemOfWithUpdate(tmpl *strategyTemplate, bound, effective int, id uint64) *StrategyTemplateItem {
	item := strategyTemplateItemOf(tmpl, bound, effective)
	if !tmpl.Builtin {
		var record monitorModels.InsightMonitorScrapeStrategyTemplates
		if global.App != nil && global.App.DB != nil {
			if err := global.App.DB.Where("id = ?", id).Take(&record).Error; err == nil && record.Model != nil {
				item.UpdatedAt = time.Time(record.UpdatedAt).Format(time.DateTime)
			}
		}
	}
	return &item
}

// reapplyInstancesOfTemplate 重算某个模板名下所有实例的生效策略。
// 模板改了策略项后必须调它：实时继承的承诺就落在这里 ——
// 不重算的话，库里的缓存列还停在旧值，"改了不生效"就又回来了。
func reapplyInstancesOfTemplate(tx *gorm.DB, templateID uint64) error {
	rows := make([]monitorModels.InsightMonitorTargetConfigs, 0)
	if err := tx.Where("strategy_template_id = ?", templateID).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := reapplyInstanceStrategy(tx, row); err != nil {
			return err
		}
	}
	return nil
}

type DeleteStrategyTemplateService struct {
	ID uint64
	C  *gin.Context
}

func (s *DeleteStrategyTemplateService) Run() error {
	if global.App == nil || global.App.DB == nil {
		return fmt.Errorf("数据库未就绪")
	}
	var record monitorModels.InsightMonitorScrapeStrategyTemplates
	if err := global.App.DB.Where("id = ?", s.ID).Take(&record).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("采集策略模板不存在")
		}
		return err
	}
	if record.Builtin || record.IsDefault {
		return fmt.Errorf("内置默认模板不可删除：它是未挂载实例的兜底，删掉会出现监控盲区")
	}
	var bound int64
	if err := global.App.DB.Model(&monitorModels.InsightMonitorTargetConfigs{}).
		Where("strategy_template_id = ?", record.ID).Count(&bound).Error; err != nil {
		return err
	}
	if bound > 0 {
		return fmt.Errorf("该模板仍挂载在 %d 个实例上，请先切换到其他模板再删除", bound)
	}
	err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("template_id = ?", record.ID).
			Delete(&monitorModels.InsightMonitorScrapeStrategyItems{}).Error; err != nil {
			return err
		}
		return tx.Delete(&monitorModels.InsightMonitorScrapeStrategyTemplates{}, record.ID).Error
	})
	if err != nil {
		return err
	}
	invalidateStrategyTemplateCache()
	return nil
}

// ---------------------------------------------------------------------------
// 挂载 / 解绑
// ---------------------------------------------------------------------------

type BindStrategyInstancesService struct {
	ID uint64
	*forms.BindStrategyInstancesForm
	C *gin.Context
}

func (s *BindStrategyInstancesService) Run() (*StrategyBindResult, error) {
	if global.App == nil || global.App.DB == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	tmpl := strategyTemplateByID(s.ID)
	if tmpl == nil {
		return nil, fmt.Errorf("采集策略模板不存在")
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
			// 跨类型挂载直接拒绝：把 MySQL 的采集策略挂到 PG 实例上，
			// 解析时只会回退，页面还显示"已挂载"，等于给了一个假的成功反馈。
			if resolveEngine(string(record.DBType)) != tmpl.DBType {
				result.Skipped++
				result.Notes = append(result.Notes, fmt.Sprintf("%s：实例类型 %s 与模板类型 %s 不一致",
					id, resolveEngine(string(record.DBType)), tmpl.DBType))
				continue
			}
			if err := tx.Model(&monitorModels.InsightMonitorTargetConfigs{}).
				Where("instance_id = ?", id).
				Updates(map[string]any{"strategy_template_id": tmpl.ID, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
			record.StrategyTemplateID = &tmpl.ID
			if _, err := reapplyInstanceStrategy(tx, record); err != nil {
				return err
			}
			result.Bound++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	invalidateStrategyTemplateCache()
	return result, nil
}

type UnbindStrategyInstancesService struct {
	*forms.UnbindStrategyInstancesForm
	C *gin.Context
}

func (s *UnbindStrategyInstancesService) Run() (*StrategyBindResult, error) {
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
			// 解绑不是"清空配置"：置 NULL 后立即按默认模板重算，
			// 保证任何时刻实例都有策略在跑（未挂载 = 套用默认，不是不采集）。
			if err := tx.Model(&monitorModels.InsightMonitorTargetConfigs{}).
				Where("instance_id = ?", id).
				Updates(map[string]any{"strategy_template_id": nil, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
			record.StrategyTemplateID = nil
			resolved, err := reapplyInstanceStrategy(tx, record)
			if err != nil {
				return err
			}
			result.Bound++
			result.TemplateName = resolved.TemplateName
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	invalidateStrategyTemplateCache()
	return result, nil
}

// SaveInstanceOverridesService 保存实例级策略覆盖。
// 覆盖项只接受注册表中的类型，且值必须合法；清空传空对象即可恢复全量继承。
type SaveInstanceOverridesService struct {
	InstanceID string
	*forms.SaveInstanceOverridesForm
	C *gin.Context
}

func (s *SaveInstanceOverridesService) Run() (*InstanceStrategyData, error) {
	if global.App == nil || global.App.DB == nil {
		return nil, fmt.Errorf("数据库未就绪")
	}
	var record monitorModels.InsightMonitorTargetConfigs
	if err := global.App.DB.Where("instance_id = ?", s.InstanceID).Take(&record).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("实例不存在")
		}
		return nil, err
	}
	normalized := map[string]any{}
	rejected := make([]string, 0)
	for key, value := range s.Overrides {
		spec, ok := strategyItemSpec(key)
		if !ok {
			rejected = append(rejected, fmt.Sprintf("%s（未注册的策略项类型）", key))
			continue
		}
		parsed, err := parseStrategyValue(spec, value)
		if err != nil {
			rejected = append(rejected, fmt.Sprintf("%s：%s", spec.Name, err.Error()))
			continue
		}
		normalized[spec.Type] = parsed
	}
	if len(rejected) > 0 {
		return nil, fmt.Errorf("以下覆盖项无法保存：%s", strings.Join(rejected, "、"))
	}
	var raw datatypes.JSON = nil
	if len(normalized) > 0 {
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return nil, err
		}
		raw = datatypes.JSON(encoded)
	}
	err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&monitorModels.InsightMonitorTargetConfigs{}).
			Where("instance_id = ?", record.InstanceID).
			Updates(map[string]any{"strategy_overrides": raw, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		record.StrategyOverrides = raw
		_, err := reapplyInstanceStrategy(tx, record)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &InstanceStrategyData{
		GeneratedAt: time.Now().Format(time.DateTime),
		InstanceID:  record.InstanceID,
		Resolved: ResolveInstanceStrategy(record.InstanceID, string(record.DBType),
			record.StrategyTemplateID, record.StrategyOverrides),
	}, nil
}

// ---------------------------------------------------------------------------
// 实例列表/详情的展示信息
// ---------------------------------------------------------------------------

// StrategyInfoForInstance 给实例行补上"挂了哪个策略模板、是否发生回退"。
// 回退必须显式下发：页面只看模板名分不出"没挂载"和"挂载失效"，
// 而这两种状态的处置动作完全不同。
func StrategyInfoForInstance(dbType string, templateID *uint64) (uint64, string, string, bool, string) {
	tmpl, fallback, reason := resolveTemplateForInstanceRow(dbType, templateID)
	if tmpl == nil {
		return 0, "", "", true, reason
	}
	label := tmpl.DisplayName
	if label == "" {
		label = tmpl.Name
	}
	return tmpl.ID, tmpl.Name, label, fallback, reason
}

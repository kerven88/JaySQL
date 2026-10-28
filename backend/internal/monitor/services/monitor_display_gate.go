package services

// ============================================================================
// 模板勾选 → 监控明细展示 的门控
//
// 业务口径：实例绑定模板后，「指标模板」里未勾选的指标不再出现在监控明细页
//（Tab / 监控项卡片 / 曲线 / 历史查询接口四处同步生效）。
//
// 治理范围（与 hostMetricsInTemplate 的既有口径一致）：
//   - MySQL / PostgreSQL / Oracle：模板勾选 = 明细展示的唯一事实来源，未勾选即隐藏；
//   - TiDB / Doris 等复用 MySQL 目录的类型：按既有约定"不纳入模板裁剪"，
//     明细照常全量展示（它们的默认模板不含主机层指标，若强行门控会把
//     基础指标 Tab 整个剪没）；
//   - 未绑定 / 绑定失效 / 模板表为空：回落默认模板（= 全量目录），展示不受影响。
//
// 历史数据不做物理删除：取消勾选只是"不再展示"，时序点与最新值保留在库，
// 重新勾选后曲线立即恢复 —— 避免误操作造成不可逆丢失。
// ============================================================================

import (
	"strings"

	"gorm.io/gorm"

	"github.com/simajiong/jaysql/internal/global"
)

// displayGateTemplate 返回实例明细展示应遵循的模板；返回 nil 表示该实例不受模板治理。
func displayGateTemplate(row monitorInstanceRow) *metricTemplate {
	// 与 hostMetricsInTemplate 同一口径：MySQL / PostgreSQL / Oracle 的门控生效，
	// TiDB / Doris 与其余类型（明细不按模板裁剪）返回 nil。
	if !hostMetricsInTemplate(row.DbType) {
		return nil
	}
	tmpl, _ := resolveTemplateForInstance(row.DbType, row.MetricsTemplate)
	if tmpl == nil || len(tmpl.Keys) == 0 {
		return nil
	}
	return tmpl
}

// displayHistoryGate 历史查询接口的门控入口：按实例ID加载类型与模板绑定后复用同一套判定。
// 实例不存在或查询失败时返回 nil（不拦截）—— 历史接口的访问主体已在路由层鉴权，
// 这里只负责展示口径收敛，不因元数据缺失把正常请求打成 500。
func displayHistoryGate(instanceID string) *metricTemplate {
	if global.App.DB == nil {
		return nil
	}
	var brief struct {
		DbType          string
		MetricsTemplate string
	}
	err := global.App.DB.Table("insight_instances a").
		Select("a.db_type as db_type, COALESCE(mt.metrics_template, '') as metrics_template").
		Joins("left join insight_monitor_target_configs mt on mt.instance_id = a.instance_id").
		Where("a.instance_id = ?", strings.TrimSpace(instanceID)).
		Take(&brief).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		global.App.Log.Warnf("displayHistoryGate load instance failed: %v", err)
		return nil
	}
	return displayGateTemplate(monitorInstanceRow{
		InstanceID:      instanceID,
		DbType:          brief.DbType,
		MetricsTemplate: brief.MetricsTemplate,
	})
}

// filterDisplayByTemplate 按模板勾选过滤展示目录：未勾选指标的行指标被剔除，
// 一行不剩的监控项整卡移除，一个监控项不剩的 Tab 整页移除 —— 与角色裁剪
// （MonitorDisplayLayoutFor）保持同一条"宁可少下发，不下发空壳"的规则。
// Series.MetricKey 为空的行表示"业务口径里有、但没有采集源"，不归模板管，照常保留。
func filterDisplayByTemplate(layout *MonitorDisplayLayout, tmpl *metricTemplate) *MonitorDisplayLayout {
	if layout == nil || tmpl == nil {
		return layout
	}
	items := make([]DisplayItem, 0, len(layout.Items))
	for _, item := range layout.Items {
		series := make([]DisplaySeries, 0, len(item.Series))
		for _, s := range item.Series {
			key := strings.TrimSpace(s.MetricKey)
			if key != "" && !tmpl.contains(key) {
				continue
			}
			series = append(series, s)
		}
		if len(series) == 0 {
			continue
		}
		item.Series = series
		items = append(items, item)
	}
	used := map[string]struct{}{}
	for _, item := range items {
		used[item.Tab] = struct{}{}
	}
	tabs := make([]DisplayTab, 0, len(layout.Tabs))
	for _, tab := range layout.Tabs {
		if _, ok := used[tab.Key]; !ok {
			continue
		}
		tabs = append(tabs, tab)
	}
	return &MonitorDisplayLayout{
		DbType: layout.DbType,
		Tabs:   tabs,
		Items:  items,
	}
}

// filterKeysByTemplate 把请求的指标键集合约束到模板勾选范围内（历史查询接口用）。
// 返回交集；允许为空切片（语义 = 该实例一个可查指标都没有），调用方据此返回空序列。
func filterKeysByTemplate(keys []string, tmpl *metricTemplate) []string {
	if tmpl == nil {
		return keys
	}
	allowed := make([]string, 0, len(keys))
	for _, key := range keys {
		if tmpl.contains(canonicalMetricKey(key)) {
			allowed = append(allowed, key)
		}
	}
	return allowed
}

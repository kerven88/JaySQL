package services

import "testing"

// ============================================================================
// 实例角色适用性：区分"结构性不存在"与"存在但采不到"
// ============================================================================

func TestNormalizeInstanceRole(t *testing.T) {
	cases := map[string]string{
		"master":     RoleMaster,
		"MASTER":     RoleMaster,
		" primary ":  RoleMaster,
		"slave":      RoleSlave,
		"standby":    RoleSlave,
		" Replica ":  RoleSlave,
		"standalone": RoleStandalone,
		"single":     RoleStandalone,
		"cascade":    RoleCascade,
		"":           "",
		"unknown01":  "",
	}
	for input, want := range cases {
		if got := normalizeInstanceRole(input); got != want {
			t.Fatalf("normalizeInstanceRole(%q) = %q，期望 %q", input, got, want)
		}
	}
}

func TestMetricApplicableToRole(t *testing.T) {
	replicationKeys := []string{
		MetricEngLogicalRepLagBytes,
		MetricEngReplSlotLag,
		MetricEngReplayLagSeconds,
		MetricEngSentLagBytes,
	}
	for _, key := range replicationKeys {
		if !metricApplicableToRole("postgresql", key, RoleMaster) {
			t.Fatalf("主库应展示复制类指标 %s", key)
		}
		if metricApplicableToRole("postgresql", key, RoleStandalone) {
			t.Fatalf("单机不存在复制类指标 %s，不应展示", key)
		}
		if metricApplicableToRole("postgresql", key, RoleSlave) {
			t.Fatalf("从库视角不存在主库复制类指标 %s，不应展示", key)
		}
		// 未知角色必须 fail-open：探测不到角色时绝不裁剪指标
		if !metricApplicableToRole("postgresql", key, "") {
			t.Fatalf("角色未知时 %s 应保留（fail-open），实际被裁掉", key)
		}
		if !metricApplicableToRole("postgresql", key, "not-a-role") {
			t.Fatalf("非法角色值应视为未知并保留 %s", key)
		}
	}
	// 与复制无关的 PG 指标在任何角色下都不受影响
	for _, role := range []string{RoleMaster, RoleSlave, RoleStandalone, ""} {
		if !metricApplicableToRole("postgresql", MetricPerfQPS, role) {
			t.Fatalf("QPS 与拓扑无关，角色 %q 下不应被裁剪", role)
		}
	}
	// MySQL 尚未登记任何受角色约束的指标，行为必须完全不变
	if metricApplicableToRole("mysql", MetricPerfQPS, RoleStandalone) == false {
		t.Fatal("MySQL 指标不应受角色矩阵影响")
	}
}

func TestFilterRoleInapplicableKeys(t *testing.T) {
	keys := []string{MetricPerfQPS, MetricEngReplayLagSeconds, MetricEngReplSlotLag}

	standalone := filterRoleInapplicableKeys("postgresql", RoleStandalone, keys)
	if len(standalone) != 1 || standalone[0] != MetricPerfQPS {
		t.Fatalf("单机实例应只剩与复制无关的指标，实际 %+v", standalone)
	}
	master := filterRoleInapplicableKeys("postgresql", RoleMaster, keys)
	if len(master) != 3 {
		t.Fatalf("主库应保留全部指标，实际 %+v", master)
	}
	unknown := filterRoleInapplicableKeys("postgresql", "", keys)
	if len(unknown) != 3 {
		t.Fatalf("角色未知应全部保留，实际 %+v", unknown)
	}
	// nil 表示"全采"兜底，必须原样透传（不能变成空切片，否则会真的停摆）
	if got := filterRoleInapplicableKeys("postgresql", RoleStandalone, nil); got != nil {
		t.Fatalf("keys 为 nil 时应原样返回 nil，实际 %+v", got)
	}
}

func TestInferPostgresRole(t *testing.T) {
	cases := []struct {
		name   string
		in     pgRoleSignals
		want   string
		reason string
	}{
		{"恢复中即为从库", pgRoleSignals{InRecovery: true}, RoleSlave, "pg_is_in_recovery() 无权限门槛，可无条件采信"},
		{"有下游即为主库", pgRoleSignals{ReplicaCount: 2}, RoleMaster, ""},
		{"无下游但仍有复制槽", pgRoleSignals{ReplicaCount: 0, SlotCount: 3}, RoleMaster, "下游暂时断开不代表拓扑不存在"},
		{
			"有槽即使看不清下游也判主库",
			pgRoleSignals{ReplicaCount: ReplicaCountUnknown, SlotCount: 1},
			RoleMaster,
			"复制槽可读门槛低于 pg_stat_replication，低权限账号下也要能识别拓扑",
		},
		{"无下游且无槽则是单机", pgRoleSignals{ReplicaCount: 0, SlotCount: 0}, RoleStandalone, ""},
		{
			"无权查看下游时返回未知",
			pgRoleSignals{ReplicaCount: ReplicaCountUnknown, SlotCount: 0},
			"",
			"此时 count 为 0 可能是权限屏蔽，把主库误判成单机会把真实指标整片藏掉",
		},
		{"恢复中优先于下游信号", pgRoleSignals{InRecovery: true, ReplicaCount: 5}, RoleSlave, ""},
	}
	for _, c := range cases {
		if got := inferPostgresRole(c.in); got != c.want {
			t.Fatalf("%s：inferPostgresRole(%+v) = %q，期望 %q（%s）", c.name, c.in, got, c.want, c.reason)
		}
	}
}

// TestDisplayLayoutKeepsReplicationItemsWithReason 覆盖展示层的核心口径：
// 复制类指标在任何角色下都保留条目，不适用的打标并给出原因，
// 保证「指标模板有、监控明细也有」；采集侧仍按角色剔除（见 filterRoleInapplicableKeys）。
func TestDisplayLayoutKeepsReplicationItemsWithReason(t *testing.T) {
	engineItems := func(role string) []DisplayItem {
		layout := MonitorDisplayLayoutFor("postgresql", role)
		if layout == nil {
			t.Fatalf("角色 %q 下 PostgreSQL 展示目录不应为空", role)
		}
		items := make([]DisplayItem, 0, len(layout.Items))
		for _, item := range layout.Items {
			if item.Tab == LayerEngine {
				items = append(items, item)
			}
		}
		return items
	}

	replicationNames := []string{"逻辑复制延迟", "复制槽堆积", "备库 APPLY 延迟", "备库发送延迟", "复制延迟"}

	// 任何角色下监控项数量都不变：不再整卡移除
	for _, role := range []string{"", RoleStandalone, RoleMaster, RoleSlave} {
		items := engineItems(role)
		if len(items) != 17 {
			t.Fatalf("角色 %q 下 PostgreSQL 数据库指标应保持 %d 个监控项，实际 %d", role, 17, len(items))
		}
		for i, name := range replicationNames {
			if items[i].Name != name || len(items[i].Series) != 1 {
				t.Fatalf("角色 %q 下第 %d 项应为 %s 且独立成项，实际 %+v", role, i+1, name, items[i])
			}
		}
	}

	// 角色未知（空串）：全部适用，不打标
	full := engineItems("")
	for i := range replicationNames {
		if full[i].Series[0].NotApplicable {
			t.Fatalf("角色未知时 %s 不应标记为不适用（fail-open），实际 %+v", replicationNames[i], full[i].Series[0])
		}
	}

	// 单机：5 项全部不适用，且必须给出原因
	standalone := engineItems(RoleStandalone)
	for i, name := range replicationNames {
		s := standalone[i].Series[0]
		if !s.NotApplicable {
			t.Fatalf("单机实例下 %s 应标记为不适用，实际 %+v", name, s)
		}
		if s.NotApplicableReason == "" {
			t.Fatalf("单机实例下 %s 必须给出不适用原因，实际为空", name)
		}
	}

	// 主库：仅「复制延迟」不适用（备库视角指标），其余 4 项适用
	master := engineItems(RoleMaster)
	for i, name := range replicationNames {
		want := name == "复制延迟"
		if master[i].Series[0].NotApplicable != want {
			t.Fatalf("主库下 %s 的适用性应为 %v，实际 %+v", name, want, master[i].Series[0])
		}
	}

	// 从库：仅「复制延迟」适用，其余 4 项不适用
	slave := engineItems(RoleSlave)
	for i, name := range replicationNames {
		want := name != "复制延迟"
		if slave[i].Series[0].NotApplicable != want {
			t.Fatalf("从库下 %s 的适用性应为 %v，实际 %+v", name, want, slave[i].Series[0])
		}
	}

	// 打标不得污染非复制类指标：第 6 项起（长事务等）在任何角色下都适用
	for _, role := range []string{RoleStandalone, RoleMaster, RoleSlave} {
		items := engineItems(role)
		for _, item := range items[len(replicationNames):] {
			for _, s := range item.Series {
				if s.NotApplicable {
					t.Fatalf("角色 %q 下非复制类指标 %s 不应被标记为不适用", role, s.MetricKey)
				}
			}
		}
	}

	// 采集侧口径不变：角色不适用的键仍被剔除，不会产生数据也不会告警
	standaloneKeys := filterRoleInapplicableKeys("postgresql", RoleStandalone,
		[]string{MetricEngLogicalRepLagBytes, MetricEngReplSlotLag, MetricEngReplayLagSeconds,
			MetricEngSentLagBytes, MetricReplLagSeconds, MetricPerfConnCount})
	if len(standaloneKeys) != 1 || standaloneKeys[0] != MetricPerfConnCount {
		t.Fatalf("单机实例采集侧应剔除全部复制类指标，仅保留 %s，实际 %+v", MetricPerfConnCount, standaloneKeys)
	}
	// 角色适用性不得污染主机层：res.* 与拓扑无关，单机仍应全量下发
	standaloneLayout := MonitorDisplayLayoutFor("postgresql", RoleStandalone)
	hostCount := 0
	for _, item := range standaloneLayout.Items {
		if item.Tab == LayerHost {
			hostCount++
		}
	}
	if hostCount != 10 {
		t.Fatalf("主机层指标与拓扑无关，单机仍应下发 10 项，实际 %d", hostCount)
	}
	if len(standaloneLayout.Tabs) != 2 {
		t.Fatalf("复制类条目保留后两个 Tab 都应存在，实际 %+v", standaloneLayout.Tabs)
	}
}

// TestDisplayLayoutRoleScopedOnlyAffectsEnginesWithMatrix MySQL 未登记角色矩阵，
// 展示目录必须与改动前完全一致。
func TestDisplayLayoutRoleScopedOnlyAffectsEnginesWithMatrix(t *testing.T) {
	baseline := MonitorDisplayLayoutFor("mysql", "")
	for _, role := range []string{RoleMaster, RoleSlave, RoleStandalone, RoleCascade} {
		got := MonitorDisplayLayoutFor("mysql", role)
		if got == nil {
			t.Fatalf("MySQL 展示目录在角色 %q 下不应为空", role)
		}
		if len(got.Items) != len(baseline.Items) {
			t.Fatalf("MySQL 展示目录不受角色影响，角色 %q 下应有 %d 项，实际 %d", role, len(baseline.Items), len(got.Items))
		}
	}
}

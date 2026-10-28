package services

// ---------------------------------------------------------------------------
// 细粒度数据授权的「写」侧：新增 / 撤销 / 续期 / 查询 / 审计
//
// 与 data_grant.go（判定侧）的分工：
//   data_grant.go      —— 只读判定，回答「能不能访问」
//   data_grant_admin.go —— 写入与审计，回答「谁给谁授了什么、什么时候生效/失效」
//
// 三条硬约束：
//   1. 授权记录只允许软变更（撤销置 status=revoked），不做物理删除——
//      物理删除会让审计流水失去可对照的主体，也会让「撤销」无法追溯；
//   2. 每一次写操作必须在同一事务里写一条 insight_das_grant_audit 流水；
//   3. 自助授权（Self=true）强制把主体改写为操作者本人，
//      防止借接口给他人（或给更高权限角色）授权——越权路径必须堵死。
// ---------------------------------------------------------------------------

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/das/forms"
	"github.com/simajiong/jaysql/internal/das/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/pagination"
	"github.com/simajiong/jaysql/pkg/utils"
	"gorm.io/gorm"
)

const dataGrantTimeLayout = "2006-01-02 15:04:05"

var (
	ErrDataGrantNotFound  = errors.New("授权记录不存在")
	ErrDataGrantConflict  = errors.New("已存在同主体同资源的有效授权")
	ErrSelfGrantForbidden = errors.New("自助授权只能授给本人")
)

func parseDataGrantTime(value string) (*time.Time, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil, nil
	}
	// 兼容前端常传的 ISO8601（2026-10-15T16:00:00Z / +08:00）
	layouts := []string{dataGrantTimeLayout, "2006-01-02T15:04:05Z07:00", "2006-01-02 15:04", "2006-01-02"}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("时间格式不正确，期望 %s", dataGrantTimeLayout)
}

// snapshotGrant 生成授权记录的 JSON 快照，写审计流水用
func snapshotGrant(g *models.InsightDasDataGrant) string {
	if g == nil {
		return ""
	}
	raw, err := json.Marshal(map[string]any{
		"grant_no":       g.GrantNo,
		"principal_type": g.PrincipalType,
		"principal_id":   g.PrincipalID,
		"principal_name": g.PrincipalName,
		"resource_level": g.ResourceLevel,
		"instance_id":    g.InstanceID,
		"schema_name":    g.SchemaName,
		"table_name":     g.Table,
		"actions":        g.Actions,
		"effect":         g.Effect,
		"status":         g.Status,
		"grant_source":   g.GrantSource,
		"grantor":        g.GrantorUsername,
		"effective_at":   g.EffectiveAt,
		"expire_at":      g.ExpireAt,
		"reason":         g.Reason,
		"revoked_at":     g.RevokedAt,
		"revoked_by":     g.RevokedBy,
	})
	if err != nil {
		return ""
	}
	return string(raw)
}

// writeGrantAudit 写一条授权变更流水。必须在调用方的事务里执行（tx 非空时）。
func writeGrantAudit(tx *gorm.DB, audit *models.InsightDasGrantAudit) {
	if tx == nil {
		tx = global.App.DB
	}
	_ = tx.Table("insight_das_grant_audit").Create(audit).Error
}

// normalizeGrantActions 规范化操作类型：空 -> select；过滤非法值
func normalizeGrantActions(raw string) (string, error) {
	items := strings.Split(raw, ",")
	valid := make([]string, 0, len(items))
	for _, item := range items {
		v := strings.ToLower(strings.TrimSpace(item))
		if v == "" {
			continue
		}
		switch v {
		case DataActionSelect, DataActionExport, DataActionDML:
			if !utils.IsContain(valid, v) {
				valid = append(valid, v)
			}
		default:
			return "", fmt.Errorf("不支持的操作类型：%s", item)
		}
	}
	if len(valid) == 0 {
		return DataActionSelect, nil
	}
	return strings.Join(valid, ","), nil
}

// normalizeResourceLevel 按层级清理资源字段：
// instance 级不允许带库/表；database 级不允许带表；table 级必须三者齐全。
func normalizeResourceLevel(level, schema, table string) (string, string, string, error) {
	level = strings.ToLower(strings.TrimSpace(level))
	schema = strings.TrimSpace(schema)
	table = strings.TrimSpace(table)
	switch level {
	case DataResourceLevelInstance:
		return level, "", "", nil
	case DataResourceLevelDatabase:
		if schema == "" {
			return "", "", "", errors.New("库级授权必须指定 schema_name")
		}
		return level, schema, "", nil
	case DataResourceLevelSchema:
		// 模式级：schema_name 是「库.模式」完整路径（PG / SQLServer）。
		// 单段路径说明调用方把它当库名传了，直接拒掉比静默存成库级更安全。
		if schema == "" || !strings.Contains(schema, ".") {
			return "", "", "", errors.New("模式级授权必须指定「库.模式」完整路径")
		}
		return level, schema, "", nil
	case DataResourceLevelTable:
		if schema == "" || table == "" {
			return "", "", "", errors.New("表级授权必须同时指定 schema_name 与 table_name")
		}
		return level, schema, table, nil
	default:
		return "", "", "", fmt.Errorf("不支持的资源层级：%s", level)
	}
}

// ---------------------------------------------------------------------------
// 新增授权
// ---------------------------------------------------------------------------

type CreateDataGrantService struct {
	*forms.DataGrantCreateForm
	C        *gin.Context
	Username string // 操作人（授权人）
	SourceIP string
}

// ensureSelfGrantWithinOwnScope 自助授权的「子集约束」。
//
// 规则：申请的每个操作，必须已经在自己的授权范围内（同资源或更粗粒度层级）。
// 实现上直接复用 HasDataGrant —— 它本身就会同时匹配表级/库级/实例级三层，
// 因此「自己有实例级 select」可以派生出「库级 select」，反之则不行，
// 天然就是「子集」语义，不需要另写一套层级包含判断。
//
// 只有 allow 授权可以派生（deny 是自己给自己加限制，不涉及提权，放行）。
// 旧模型没有操作维度，无法判定子集，直接放行以免把存量自助授权卡死。
func ensureSelfGrantWithinOwnScope(scope *dasActorScope, instanceID, schemaName, tableName, actions, effect string) error {
	if !strings.EqualFold(strings.TrimSpace(effect), DataEffectAllow) {
		return nil
	}
	if !dataGrantEnabled() {
		return nil
	}
	if strings.TrimSpace(instanceID) == "" {
		return errors.New("自助授权必须指定数据源")
	}
	for _, action := range strings.Split(actions, ",") {
		action = strings.TrimSpace(action)
		if action == "" {
			continue
		}
		ok, err := HasDataGrant(scope, instanceID, schemaName, tableName, action)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("自助授权只能授予自己已有权限的子集：您尚未被授予对资源 %s 执行 %s 的权限",
				describeDataResource(instanceID, schemaName, tableName), action)
		}
	}
	return nil
}

func (s *CreateDataGrantService) Run() (*models.InsightDasDataGrant, error) {
	principalType := strings.ToLower(strings.TrimSpace(s.PrincipalType))
	principalID := s.PrincipalID

	// 自助授权：主体强制改写为操作者本人，杜绝借接口给他人/给角色授权
	source := DataGrantSourceManual
	actorScope, err := resolveDASActorScope(s.Username)
	if err != nil {
		return nil, err
	}
	if s.Self {
		principalType = dasPrincipalUser
		principalID = actorScope.UID
		source = DataGrantSourceSelf
		// 自助授权没有第二个人复核，审计流水是唯一的追溯依据：
		// 没写理由的也要留一句可解释的说明，避免出现「谁也说不清为什么有这条授权」。
		if strings.TrimSpace(s.Reason) == "" {
			s.Reason = "自助授权（本人已有授权的子集）"
		}
	}
	if principalID == 0 {
		return nil, errors.New("被授权主体不能为空")
	}

	actions, err := normalizeGrantActions(s.Actions)
	if err != nil {
		return nil, err
	}
	level, schemaName, tableName, err := normalizeResourceLevel(s.ResourceLevel, s.SchemaName, s.TableName)
	if err != nil {
		return nil, err
	}
	effect := strings.ToLower(strings.TrimSpace(s.Effect))
	if effect == "" {
		effect = DataEffectAllow
	}
	if effect != DataEffectAllow && effect != DataEffectDeny {
		return nil, fmt.Errorf("不支持的效力：%s", effect)
	}
	// 自助授权只能授予「自己已有授权的子集」：
	// 这是「自助」与「提权」的分界线——否则任何有管理权限的人都能靠
	// 自助接口把任意实例的任意操作颁给自己，授权体系等于形同虚设。
	if s.Self {
		if err := ensureSelfGrantWithinOwnScope(actorScope, strings.TrimSpace(s.InstanceID), schemaName, tableName, actions, effect); err != nil {
			return nil, err
		}
	}
	effectiveAt, err := parseDataGrantTime(s.EffectiveAt)
	if err != nil {
		return nil, err
	}
	expireAt, err := parseDataGrantTime(s.ExpireAt)
	if err != nil {
		return nil, err
	}
	if effectiveAt != nil && expireAt != nil && !expireAt.After(*effectiveAt) {
		return nil, errors.New("失效时间必须晚于生效时间")
	}

	// 主体与授权人快照
	var principalName string
	instanceID := strings.TrimSpace(s.InstanceID)
	if err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		name, _, err := resolveGrantPrincipalDisplayName(tx, principalType, principalID)
		if err != nil {
			return err
		}
		principalName = name
		// 同主体 + 同资源 + 同效力 的有效授权不允许重复创建
		var exists int64
		dup := tx.Table("insight_das_data_grants").
			Where("principal_type = ? AND principal_id = ? AND instance_id = ? AND schema_name = ? AND table_name = ? AND effect = ? AND status = ?",
				principalType, principalID, instanceID, schemaName, tableName, effect, DataGrantStatusActive)
		if err := dup.Count(&exists).Error; err != nil {
			return err
		}
		if exists > 0 {
			return ErrDataGrantConflict
		}
		return nil
	}); err != nil {
		return nil, err
	}

	var grantorName string
	{
		var row struct {
			Username string
		}
		if err := global.App.DB.Table("insight_users").Select("username").
			Where("username = ? AND is_deleted = ?", s.Username, false).Take(&row).Error; err == nil {
			grantorName = row.Username
		}
	}

	grant := &models.InsightDasDataGrant{
		Model:           &commonModels.Model{},
		GrantNo:         utils.GenerateDataGrantID(),
		PrincipalType:   principalType,
		PrincipalID:     principalID,
		PrincipalName:   principalName,
		ResourceLevel:   level,
		InstanceID:      instanceID,
		SchemaName:      schemaName,
		Table:           tableName,
		Actions:         actions,
		Effect:          effect,
		EffectiveAt:     toLocalTimePtr(effectiveAt),
		ExpireAt:        toLocalTimePtr(expireAt),
		GrantorUsername: s.Username,
		GrantorName:     grantorName,
		GrantSource:     source,
		Status:          DataGrantStatusActive,
		Reason:          strings.TrimSpace(s.Reason),
	}

	if err := global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(grant).Error; err != nil {
			return err
		}
		writeGrantAudit(tx, &models.InsightDasGrantAudit{
			Model:            &commonModels.Model{},
			GrantNo:          grant.GrantNo,
			GrantID:          uint64(grant.Model.ID),
			Action:           DataGrantAuditCreate,
			OperatorUsername: s.Username,
			OperatorName:     grantorName,
			PrincipalType:    grant.PrincipalType,
			PrincipalID:      grant.PrincipalID,
			PrincipalName:    grant.PrincipalName,
			ResourceDesc:     describeDataResource(grant.InstanceID, grant.SchemaName, grant.Table),
			Actions:          grant.Actions,
			BeforeSnapshot:   "",
			AfterSnapshot:    snapshotGrant(grant),
			Reason:           grant.Reason,
			SourceIP:         s.SourceIP,
		})
		return nil
	}); err != nil {
		return nil, err
	}
	return grant, nil
}

// ---------------------------------------------------------------------------
// 撤销授权
// ---------------------------------------------------------------------------

// cascadeDeleteLegacyGrantRows 撤销新表授权时同步删除旧授权表里的对应行。
//
// 为什么必须做：旧「数据访问」管理页的写操作只落旧表，之后靠
// ReconcileLegacyGrantsForPrincipal 对账到新表，对账的补齐规则是
// 「旧表有、新表无 → 新建」。如果撤销时只把新表置为 revoked 而不动旧表，
// 下一次任意授权增删触发对账，这条授权就会被当成「新表缺失」重新创建，
// 管理员看到的现象是「删掉的授权过一会儿自己回来了」。
//
// 删除粒度与新表记录的层级对齐，不做扩散：
//
//	instance 级 → 只删旧表 schema_perms 的实例级行（schema='*'）
//	database 级 → 删该库的 schema_perms 行 + 该库下全部 table_perms 行
//	table 级    → 只删这一张表的 table_perms 行
func cascadeDeleteLegacyGrantRows(tx *gorm.DB, grant *models.InsightDasDataGrant) error {
	if tx == nil || grant == nil {
		return nil
	}
	pt := normalizeDASPrincipalType(grant.PrincipalType)
	if grant.PrincipalID == 0 {
		return nil
	}
	const deleteSchemaPerms = "DELETE FROM insight_das_schema_perms WHERE principal_type = ? AND principal_id = ? AND instance_id = ? AND `schema` = ?"
	const deleteTablePerms = "DELETE FROM insight_das_table_perms WHERE principal_type = ? AND principal_id = ? AND instance_id = ? AND `schema` = ?"

	switch grant.ResourceLevel {
	case DataResourceLevelInstance:
		return tx.Exec(deleteSchemaPerms, pt, grant.PrincipalID, grant.InstanceID, dasInstanceGrantSchema).Error
	case DataResourceLevelTable:
		return tx.Exec(deleteTablePerms+" AND `table` = ?", pt, grant.PrincipalID, grant.InstanceID, grant.SchemaName, grant.Table).Error
	default:
		if err := tx.Exec(deleteSchemaPerms, pt, grant.PrincipalID, grant.InstanceID, grant.SchemaName).Error; err != nil {
			return err
		}
		return tx.Exec(deleteTablePerms, pt, grant.PrincipalID, grant.InstanceID, grant.SchemaName).Error
	}
}

type RevokeDataGrantService struct {
	*forms.DataGrantRevokeForm
	C        *gin.Context
	Username string
	SourceIP string
}

func (s *RevokeDataGrantService) Run() error {
	grantNo := strings.TrimSpace(s.GrantNo)
	var grant models.InsightDasDataGrant
	if err := global.App.DB.Where("grant_no = ?", grantNo).Take(&grant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDataGrantNotFound
		}
		return err
	}
	if grant.Status == DataGrantStatusRevoked {
		return nil // 幂等：已撤销直接成功，避免重复写流水
	}
	before := snapshotGrant(&grant)
	now := time.Now()
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("insight_das_data_grants").Where("id = ?", grant.Model.ID).
			Updates(map[string]any{
				"status":     DataGrantStatusRevoked,
				"revoked_at": now,
				"revoked_by": s.Username,
			}).Error; err != nil {
			return err
		}
		// 级联清掉旧授权表里的对应行。
		// 不清理的话，旧管理页任何一次授权增删都会触发 ReconcileLegacyGrantsForPrincipal，
		// 而它的补齐规则是「旧表有、新表无 → 新建」——刚撤销的授权会被当成缺失重新建出来，
		// 表现为「删了过一会儿又自己回来」。
		if err := cascadeDeleteLegacyGrantRows(tx, &grant); err != nil {
			return err
		}
		grant.Status = DataGrantStatusRevoked
		grant.RevokedAt = toLocalTimePtr(&now)
		grant.RevokedBy = s.Username
		writeGrantAudit(tx, &models.InsightDasGrantAudit{
			Model:            &commonModels.Model{},
			GrantNo:          grant.GrantNo,
			GrantID:          uint64(grant.Model.ID),
			Action:           DataGrantAuditRevoke,
			OperatorUsername: s.Username,
			OperatorName:     s.Username,
			PrincipalType:    grant.PrincipalType,
			PrincipalID:      grant.PrincipalID,
			PrincipalName:    grant.PrincipalName,
			ResourceDesc:     describeDataResource(grant.InstanceID, grant.SchemaName, grant.Table),
			Actions:          grant.Actions,
			BeforeSnapshot:   before,
			AfterSnapshot:    snapshotGrant(&grant),
			Reason:           strings.TrimSpace(s.Reason),
			SourceIP:         s.SourceIP,
		})
		// 若这条授权来自「数据访问权限申请」，把申请单一起收敛成已回收：
		// 否则申请单会一直显示「已通过」，而权限早就没了。
		MarkAccessRequestRevokedByGrantNo(tx, grant.GrantNo, s.Username, "授权已被管理员撤销："+strings.TrimSpace(s.Reason))
		return nil
	})
}

// ---------------------------------------------------------------------------
// 续期
// ---------------------------------------------------------------------------

type RenewDataGrantService struct {
	*forms.DataGrantRenewForm
	C        *gin.Context
	Username string
	SourceIP string
}

func (s *RenewDataGrantService) Run() error {
	grantNo := strings.TrimSpace(s.GrantNo)
	newExpire, err := parseDataGrantTime(s.ExpireAt)
	if err != nil {
		return err
	}
	if newExpire == nil {
		return errors.New("续期必须指定新的失效时间")
	}
	var grant models.InsightDasDataGrant
	if err := global.App.DB.Where("grant_no = ?", grantNo).Take(&grant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrDataGrantNotFound
		}
		return err
	}
	if grant.Status != DataGrantStatusActive {
		return errors.New("只有生效中的授权可以续期")
	}
	before := snapshotGrant(&grant)
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("insight_das_data_grants").Where("id = ?", grant.Model.ID).
			Updates(map[string]any{
				"expire_at": newExpire,
				// 续期后若已被置为 expired，恢复为 active
				"status": DataGrantStatusActive,
			}).Error; err != nil {
			return err
		}
		grant.ExpireAt = toLocalTimePtr(newExpire)
		writeGrantAudit(tx, &models.InsightDasGrantAudit{
			Model:            &commonModels.Model{},
			GrantNo:          grant.GrantNo,
			GrantID:          uint64(grant.Model.ID),
			Action:           DataGrantAuditRenew,
			OperatorUsername: s.Username,
			OperatorName:     s.Username,
			PrincipalType:    grant.PrincipalType,
			PrincipalID:      grant.PrincipalID,
			PrincipalName:    grant.PrincipalName,
			ResourceDesc:     describeDataResource(grant.InstanceID, grant.SchemaName, grant.Table),
			Actions:          grant.Actions,
			BeforeSnapshot:   before,
			AfterSnapshot:    snapshotGrant(&grant),
			Reason:           strings.TrimSpace(s.Reason),
			SourceIP:         s.SourceIP,
		})
		return nil
	})
}

// ---------------------------------------------------------------------------
// 查询：授权列表
// ---------------------------------------------------------------------------

type ListDataGrantsService struct {
	*forms.DataGrantListForm
	C        *gin.Context
	Username string
}

// dataGrantListRow 「数据访问」管理页的一行授权。
//
// 除了新授权表自身的列，还带上一批从 insight_instances / insight_instance_schemas
// 派生出来的展示字段——新表只存 instance_id / schema_name / table_name 三个定位键，
// 而页面要显示数据源名称、连接地址、环境、数据库类型、Schema 展示名。
// 这些信息都在别的表里，由列表查询一次 join 出来，避免前端再发一轮请求。
type dataGrantListRow struct {
	ID              uint64                  `json:"id"`
	CreatedAt       commonModels.LocalTime  `json:"created_at"`
	UpdatedAt       commonModels.LocalTime  `json:"updated_at"`
	GrantNo         string                  `json:"grant_no"`
	PrincipalType   string                  `json:"principal_type"`
	PrincipalID     uint64                  `json:"principal_id"`
	PrincipalName   string                  `json:"principal_name"`
	ResourceLevel   string                  `json:"resource_level"`
	InstanceID      string                  `json:"instance_id"`
	SchemaName      string                  `json:"schema_name"`
	TableName       string                  `json:"table_name"`
	Actions         string                  `json:"actions"`
	Effect          string                  `json:"effect"`
	Status          string                  `json:"status"`
	GrantSource     string                  `json:"grant_source"`
	GrantorUsername string                  `json:"grantor_username"`
	GrantorName     string                  `json:"grantor_name"`
	Reason          string                  `json:"reason"`
	EffectiveAt     *commonModels.LocalTime `json:"effective_at"`
	ExpireAt        *commonModels.LocalTime `json:"expire_at"`
	RevokedAt       *commonModels.LocalTime `json:"revoked_at"`
	RevokedBy       string                  `json:"revoked_by"`
	InstanceName    string                  `json:"instance_name"`

	DBType        string `json:"db_type"`
	Hostname      string `json:"hostname"`
	Port          int    `json:"port"`
	Remark        string `json:"remark"`
	Environment   string `json:"environment"`
	DisplayName   string `json:"display_name"`
	ParentPath    string `json:"parent_path"`
	ResourceType  string `json:"resource_type"`
	PrincipalHint string `json:"principal_deleted_hint"`

	// PrincipalEarliestCreatedAt 该主体在整个授权表里的第一次授权时间（MIN(created_at)）。
	// 页面拿它当「创建时间」展示并 PIN 住——否则删光重加之后创建时间会被刷新，
	// 看起来像「这个人的授权是刚给的」，把授权的历史事实抹掉了。
	PrincipalEarliestCreatedAt *commonModels.LocalTime `json:"principal_earliest_created_at"`

	// SchemaResourceType 内部用：insight_instance_schemas 里该库/Schema 的
	// 原始 resource_type（database / schema）。新授权表不存这个维度，
	// 但旧页面用它区分「库」与「Schema」，所以查出来回填到 ResourceType。
	SchemaResourceType string `json:"-"`
}

func (s *ListDataGrantsService) Run() (data any, total int64, err error) {
	tx := global.App.DB.Table("insight_das_data_grants g").
		Select(`g.id, g.created_at, g.updated_at, g.grant_no, g.principal_type, g.principal_id, g.principal_name,
		        g.resource_level, g.instance_id, g.schema_name, g.table_name, g.actions, g.effect,
		        g.status, g.grant_source, g.grantor_username, g.grantor_name, g.reason,
		        g.effective_at, g.expire_at, g.revoked_at, g.revoked_by,
		        ifnull(i.hostname, '') as hostname,
		        ifnull(i.port, 0) as port,
		        ifnull(i.db_type, '') as db_type,
		        ifnull(i.remark, '') as remark,
		        ifnull(e.name, '') as environment,
		        ifnull(concat(i.hostname, ':', i.port), '') as instance_name,
		        ifnull((select sm.display_name from insight_instance_schemas sm
		                 where sm.instance_id = g.instance_id and sm.schema = g.schema_name and sm.is_deleted = 0
		                 limit 1), '') as display_name,
		        ifnull((select sm.parent_path from insight_instance_schemas sm
		                 where sm.instance_id = g.instance_id and sm.schema = g.schema_name and sm.is_deleted = 0
		                 limit 1), '') as parent_path,
		        ifnull((select sm.resource_type from insight_instance_schemas sm
		                 where sm.instance_id = g.instance_id and sm.schema = g.schema_name and sm.is_deleted = 0
		                 limit 1), '') as schema_resource_type`).
		Joins("left join insight_instances i on i.instance_id = g.instance_id").
		Joins("left join insight_instance_environments e on i.environment = e.id")

	// 自助视角：只返回「本人 + 本人所属角色」的授权
	if strings.EqualFold(strings.TrimSpace(s.Scope), "self") {
		scope, err := resolveDASActorScope(s.Username)
		if err != nil {
			return nil, 0, err
		}
		tx = applyDataGrantPrincipalFilter(tx, "g", scope)
	}
	if v := strings.TrimSpace(s.PrincipalType); v != "" {
		tx = tx.Where("g.principal_type = ?", v)
	}
	if s.PrincipalID > 0 {
		tx = tx.Where("g.principal_id = ?", s.PrincipalID)
	}
	if v := strings.TrimSpace(s.InstanceID); v != "" {
		tx = tx.Where("g.instance_id = ?", v)
	}
	if v := strings.TrimSpace(s.SchemaName); v != "" {
		tx = tx.Where("g.schema_name = ?", v)
	}
	if v := strings.TrimSpace(s.Status); v != "" {
		tx = tx.Where("g.status = ?", v)
	}
	if v := strings.TrimSpace(s.Search); v != "" {
		like := "%" + v + "%"
		tx = tx.Where("(g.principal_name like ? or g.instance_id like ? or g.schema_name like ? or g.table_name like ? or g.grant_no like ? or g.grantor_username like ?)",
			like, like, like, like, like, like)
	}

	var rows []dataGrantListRow
	total = pagination.Pager(&s.PaginationQ, tx.Order("g.id desc"), &rows)
	fillDataGrantDisplayFields(rows)
	return rows, total, nil
}

// fillDataGrantDisplayFields 补齐「数据访问」管理页需要的派生字段。
//
// 两件事：
//  1. resource_type：新授权表只存 instance / database / table 三种层级，
//     旧页面还要区分「库」与「Schema」（PostgreSQL 的展示名要靠它决定）。
//     这里优先用 insight_instance_schemas 的原始 resource_type 回填，
//     查不到时（库已从元数据里消失）退回到 resource_level。
//  2. principal_deleted_hint：被授权用户/角色已删除或停用时给页面一个灰标，
//     否则列表上会出现一堆指向不存在主体的授权，管理员无从判断。
func fillDataGrantDisplayFields(rows []dataGrantListRow) {
	if len(rows) == 0 {
		return
	}
	userIDs := make([]uint64, 0, len(rows))
	roleIDs := make([]uint64, 0, len(rows))
	for i := range rows {
		// 层级本身就能回答 instance / table 两种粒度；只有 database 这一层需要
		// 借 insight_instance_schemas 的原始 resource_type 区分「库」与「Schema」
		// （PostgreSQL 的展示名要靠它决定），否则表级授权会被误标成「数据库」。
		switch rows[i].ResourceLevel {
		case DataResourceLevelInstance:
			rows[i].ResourceType = "instance"
		case DataResourceLevelTable:
			rows[i].ResourceType = "table"
		default:
			rows[i].ResourceType = "database"
			if rows[i].SchemaResourceType != "" {
				rows[i].ResourceType = rows[i].SchemaResourceType
			}
		}
		if rows[i].DisplayName == "" {
			rows[i].DisplayName = rows[i].SchemaName
		}
		if normalizeDASPrincipalType(rows[i].PrincipalType) == dasPrincipalRole {
			roleIDs = append(roleIDs, rows[i].PrincipalID)
		} else {
			userIDs = append(userIDs, rows[i].PrincipalID)
		}
	}

	userStatus := make(map[uint64]string, len(userIDs))
	if len(userIDs) > 0 {
		type userRow struct {
			Uid       uint64
			IsActive  bool
			IsDeleted bool
		}
		var users []userRow
		_ = global.App.DB.Table("insight_users").Select("uid, is_active, is_deleted").Where("uid in ?", userIDs).Scan(&users).Error
		existing := make(map[uint64]struct{}, len(users))
		for _, u := range users {
			existing[u.Uid] = struct{}{}
			switch {
			case u.IsDeleted:
				userStatus[u.Uid] = "已删除"
			case !u.IsActive:
				userStatus[u.Uid] = "已停用"
			}
		}
		for _, uid := range userIDs {
			if _, ok := existing[uid]; !ok {
				userStatus[uid] = "主体已不存在"
			}
		}
	}

	roleStatus := make(map[uint64]string, len(roleIDs))
	if len(roleIDs) > 0 {
		type roleRow struct {
			Id        uint64
			IsDeleted bool
		}
		var roles []roleRow
		_ = global.App.DB.Table("insight_roles").Select("id, is_deleted").Where("id in ?", roleIDs).Scan(&roles).Error
		existing := make(map[uint64]struct{}, len(roles))
		for _, r := range roles {
			existing[r.Id] = struct{}{}
			if r.IsDeleted {
				roleStatus[r.Id] = "已删除"
			}
		}
		for _, rid := range roleIDs {
			if _, ok := existing[rid]; !ok {
				roleStatus[rid] = "角色已不存在"
			}
		}
	}

	for i := range rows {
		if normalizeDASPrincipalType(rows[i].PrincipalType) == dasPrincipalRole {
			rows[i].PrincipalHint = roleStatus[rows[i].PrincipalID]
			continue
		}
		rows[i].PrincipalHint = userStatus[rows[i].PrincipalID]
	}

	// 主体级第一次授权时间：一次聚合查全表再按主体回填。
	// 只查当前页涉及的主体，避免表很大时把整表聚合开销放大。
	type earliestRow struct {
		PrincipalType string
		PrincipalID   uint64
		Earliest      *commonModels.LocalTime
	}
	var earliest []earliestRow
	if err := global.App.DB.Table("insight_das_data_grants").
		Select("principal_type, principal_id, min(created_at) as earliest").
		Where("principal_type = ?", dasPrincipalUser).Where("principal_id in ?", userIDs).
		Group("principal_type, principal_id").
		Scan(&earliest).Error; err != nil {
		return
	}
	if len(roleIDs) > 0 {
		var roleEarliest []earliestRow
		if err := global.App.DB.Table("insight_das_data_grants").
			Select("principal_type, principal_id, min(created_at) as earliest").
			Where("principal_type = ?", dasPrincipalRole).Where("principal_id in ?", roleIDs).
			Group("principal_type, principal_id").
			Scan(&roleEarliest).Error; err == nil {
			earliest = append(earliest, roleEarliest...)
		}
	}
	earliestByKey := make(map[string]*commonModels.LocalTime, len(earliest))
	for _, item := range earliest {
		earliestByKey[fmt.Sprintf("%s:%d", item.PrincipalType, item.PrincipalID)] = item.Earliest
	}
	for i := range rows {
		key := fmt.Sprintf("%s:%d", normalizeDASPrincipalType(rows[i].PrincipalType), rows[i].PrincipalID)
		if val, ok := earliestByKey[key]; ok {
			rows[i].PrincipalEarliestCreatedAt = val
		}
	}
}

// ---------------------------------------------------------------------------
// 查询：授权变更流水
// ---------------------------------------------------------------------------

type ListDataGrantAuditsService struct {
	*forms.DataGrantAuditListForm
	C *gin.Context
}

func (s *ListDataGrantAuditsService) Run() (data any, total int64, err error) {
	type row struct {
		ID               uint64                 `json:"id"`
		CreatedAt        commonModels.LocalTime `json:"created_at"`
		GrantNo          string                 `json:"grant_no"`
		GrantID          uint64                 `json:"grant_id"`
		Action           string                 `json:"action"`
		OperatorUsername string                 `json:"operator_username"`
		OperatorName     string                 `json:"operator_name"`
		PrincipalType    string                 `json:"principal_type"`
		PrincipalID      uint64                 `json:"principal_id"`
		PrincipalName    string                 `json:"principal_name"`
		ResourceDesc     string                 `json:"resource_desc"`
		Actions          string                 `json:"actions"`
		BeforeSnapshot   string                 `json:"before_snapshot"`
		AfterSnapshot    string                 `json:"after_snapshot"`
		Reason           string                 `json:"reason"`
		SourceIP         string                 `json:"source_ip"`
	}
	tx := global.App.DB.Table("insight_das_grant_audit a")
	if v := strings.TrimSpace(s.GrantNo); v != "" {
		tx = tx.Where("a.grant_no = ?", v)
	}
	if v := strings.TrimSpace(s.Action); v != "" {
		tx = tx.Where("a.action = ?", v)
	}
	if v := strings.TrimSpace(s.Search); v != "" {
		like := "%" + v + "%"
		tx = tx.Where("(a.principal_name like ? or a.operator_username like ? or a.resource_desc like ? or a.grant_no like ?)", like, like, like, like)
	}
	var rows []dataGrantListRow
	total = pagination.Pager(&s.PaginationQ, tx.Order("a.id desc"), &rows)
	return rows, total, nil
}

// ---------------------------------------------------------------------------
// 旧管理页（数据访问）授权操作 → 新授权表 的对账
//
// 为什么需要这一步：
//   改造后判定只认 insight_das_data_grants。而「数据访问」管理页（本次未改前端）
//   的增删仍写 insight_das_schema_perms / insight_das_table_perms。
//   如果不做对账，用户在界面上授权/撤权会「看起来成功但完全不起作用」——
//   静默失效比报错更危险，所以这里把旧表的当前状态对账成新表状态。
//
// 对账规则（按主体维度全量对齐）：
//   旧表有、新表无            → 新建（来源 manual，写 create 流水）
//   旧表已删、新表仍 active   → 撤销（写 revoke 流水）
//   仅在来源为 manual / migrate 的记录上做撤销，避免误撤掉：
//     self（自助授权）、system（系统内置）——这两类不是旧表能表达的，
//     对账时只做「补齐」，绝不反向删除。
// ---------------------------------------------------------------------------

type legacySchemaGrantRow struct {
	InstanceID   string
	Schema       string
	ResourceType string
}

type legacyTableGrantRow struct {
	InstanceID string
	Schema     string
	Table      string
	Rule       string
}

type existingDataGrantRow struct {
	ID            uint64
	ResourceLevel string
	InstanceID    string
	SchemaName    string
	TableName     string
	Effect        string
	GrantSource   string
}

type desiredDataGrant struct {
	Level        string
	InstanceID   string
	SchemaName   string
	TableName    string
	ResourceType string
	Effect       string
}

func dataGrantKey(level, instanceID, schemaName, tableName, effect string) string {
	return strings.Join([]string{level, instanceID, schemaName, tableName, effect}, "|")
}

// ReconcileLegacyGrantsForPrincipal 把某个主体在旧表里的授权状态对账到新表。
// operator 记为本次对账的触发人（管理页操作者），无操作人时传 "system"。
func ReconcileLegacyGrantsForPrincipal(tx *gorm.DB, principalType string, principalID uint64, operator string) error {
	if tx == nil {
		tx = global.App.DB
	}
	pt := normalizeDASPrincipalType(principalType)
	if principalID == 0 {
		return nil
	}
	if strings.TrimSpace(operator) == "" {
		operator = "system"
	}

	principalName, _, err := resolveGrantPrincipalDisplayName(tx, pt, principalID)
	if err != nil {
		// 主体已被删除时不做对账，避免造出指向不存在主体的授权
		return nil
	}

	var schemaRows []legacySchemaGrantRow
	if err := tx.Raw("SELECT instance_id, `schema`, resource_type FROM insight_das_schema_perms WHERE principal_type = ? AND principal_id = ?", pt, principalID).
		Scan(&schemaRows).Error; err != nil {
		return err
	}
	var tableRows []legacyTableGrantRow
	if err := tx.Raw("SELECT instance_id, `schema`, `table`, rule FROM insight_das_table_perms WHERE principal_type = ? AND principal_id = ?", pt, principalID).
		Scan(&tableRows).Error; err != nil {
		return err
	}

	// 期望状态
	desired := make(map[string]desiredDataGrant, len(schemaRows)+len(tableRows))
	for _, row := range schemaRows {
		instanceID := strings.TrimSpace(row.InstanceID)
		if instanceID == "" {
			continue
		}
		schemaName := strings.TrimSpace(row.Schema)
		level := DataResourceLevelDatabase
		if schemaName == "" || schemaName == dasInstanceGrantSchema {
			// '*' 表示实例级
			schemaName = ""
			level = DataResourceLevelInstance
		}
		resourceType := strings.TrimSpace(row.ResourceType)
		if resourceType == "" {
			resourceType = "database"
		}
		item := desiredDataGrant{
			Level: level, InstanceID: instanceID, SchemaName: schemaName,
			ResourceType: resourceType, Effect: DataEffectAllow,
		}
		desired[dataGrantKey(item.Level, item.InstanceID, item.SchemaName, item.TableName, item.Effect)] = item
	}
	for _, row := range tableRows {
		instanceID := strings.TrimSpace(row.InstanceID)
		tableName := strings.TrimSpace(row.Table)
		if instanceID == "" || tableName == "" {
			continue
		}
		effect := DataEffectAllow
		if strings.EqualFold(strings.TrimSpace(row.Rule), DataEffectDeny) {
			effect = DataEffectDeny
		}
		item := desiredDataGrant{
			Level: DataResourceLevelTable, InstanceID: instanceID,
			SchemaName: strings.TrimSpace(row.Schema), TableName: tableName,
			ResourceType: "table", Effect: effect,
		}
		desired[dataGrantKey(item.Level, item.InstanceID, item.SchemaName, item.TableName, item.Effect)] = item
	}

	// 现状（只取 active）
	var existing []existingDataGrantRow
	if err := tx.Table("insight_das_data_grants").
		Select("id, resource_level, instance_id, schema_name, table_name, effect, grant_source").
		Where("principal_type = ? AND principal_id = ? AND status = ?", pt, principalID, DataGrantStatusActive).
		Scan(&existing).Error; err != nil {
		return err
	}

	now := time.Now()
	existingKeys := make(map[string]uint64, len(existing))
	for _, row := range existing {
		key := dataGrantKey(row.ResourceLevel, row.InstanceID, row.SchemaName, row.TableName, row.Effect)
		existingKeys[key] = row.ID
	}

	// 1) 补齐缺的
	for key, item := range desired {
		if _, ok := existingKeys[key]; ok {
			continue
		}
		grant := &models.InsightDasDataGrant{
			Model:         &commonModels.Model{},
			GrantNo:       utils.GenerateDataGrantID(),
			PrincipalType: pt,
			PrincipalID:   principalID,
			PrincipalName: principalName,
			ResourceLevel: item.Level,
			InstanceID:    item.InstanceID,
			SchemaName:    item.SchemaName,
			Table:         item.TableName,
			ResourceType:  item.ResourceType,
			// 旧「数据访问」管理页没有操作维度，一条库授权等价于三种操作全放行。
			// 同步时若只写 select，等于在用户不知情的情况下把 DML / 导出权限收走。
			Actions:         DataActionAll,
			Effect:          item.Effect,
			EffectiveAt:     toLocalTimePtr(&now),
			GrantorUsername: operator,
			GrantorName:     operator,
			GrantSource:     DataGrantSourceManual,
			Status:          DataGrantStatusActive,
			Reason:          "由数据访问管理页同步",
		}
		if err := tx.Create(grant).Error; err != nil {
			return err
		}
		writeGrantAudit(tx, &models.InsightDasGrantAudit{
			Model:            &commonModels.Model{},
			GrantNo:          grant.GrantNo,
			GrantID:          uint64(grant.Model.ID),
			Action:           DataGrantAuditCreate,
			OperatorUsername: operator,
			OperatorName:     operator,
			PrincipalType:    pt,
			PrincipalID:      principalID,
			PrincipalName:    principalName,
			ResourceDesc:     describeDataResource(item.InstanceID, item.SchemaName, item.TableName),
			Actions:          grant.Actions,
			AfterSnapshot:    snapshotGrant(grant),
			Reason:           "由数据访问管理页同步",
		})
	}

	// 2) 撤掉旧表已不存在的（只针对 manual / migrate 来源）
	for _, row := range existing {
		if row.GrantSource != DataGrantSourceManual && row.GrantSource != DataGrantSourceMigrate {
			continue
		}
		key := dataGrantKey(row.ResourceLevel, row.InstanceID, row.SchemaName, row.TableName, row.Effect)
		if _, ok := desired[key]; ok {
			continue
		}
		var grant models.InsightDasDataGrant
		if err := tx.Where("id = ?", row.ID).Take(&grant).Error; err != nil {
			continue
		}
		before := snapshotGrant(&grant)
		if err := tx.Table("insight_das_data_grants").Where("id = ?", row.ID).
			Updates(map[string]any{
				"status":     DataGrantStatusRevoked,
				"revoked_at": now,
				"revoked_by": operator,
			}).Error; err != nil {
			return err
		}
		grant.Status = DataGrantStatusRevoked
		grant.RevokedAt = toLocalTimePtr(&now)
		grant.RevokedBy = operator
		writeGrantAudit(tx, &models.InsightDasGrantAudit{
			Model:            &commonModels.Model{},
			GrantNo:          grant.GrantNo,
			GrantID:          row.ID,
			Action:           DataGrantAuditRevoke,
			OperatorUsername: operator,
			OperatorName:     operator,
			PrincipalType:    pt,
			PrincipalID:      principalID,
			PrincipalName:    principalName,
			ResourceDesc:     describeDataResource(row.InstanceID, row.SchemaName, row.TableName),
			Actions:          grant.Actions,
			BeforeSnapshot:   before,
			AfterSnapshot:    snapshotGrant(&grant),
			Reason:           "由数据访问管理页同步（旧表已删除）",
		})
	}
	return nil
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

// resolveGrantPrincipalDisplayName 取主体展示名（用户取 username，角色取 name）
func resolveGrantPrincipalDisplayName(tx *gorm.DB, principalType string, principalID uint64) (string, string, error) {
	switch normalizeDASPrincipalType(principalType) {
	case dasPrincipalRole:
		var row struct {
			ID   uint64
			Code string
			Name string
		}
		if err := tx.Table("insight_roles").Select("id, code, name").
			Where("id = ? AND is_deleted = ?", principalID, false).Take(&row).Error; err != nil {
			return "", "", errors.New("角色不存在或已删除")
		}
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = row.Code
		}
		return name, row.Code, nil
	default:
		var row struct {
			UID      uint64
			Username string
		}
		if err := tx.Table("insight_users").Select("uid, username").
			Where("uid = ? AND is_deleted = ?", principalID, false).Take(&row).Error; err != nil {
			return "", "", errors.New("用户不存在或已删除")
		}
		return row.Username, row.Username, nil
	}
}

func toLocalTimePtr(t *time.Time) *commonModels.LocalTime {
	if t == nil {
		return nil
	}
	v := commonModels.LocalTime(*t)
	return &v
}

package models

import (
	"github.com/simajiong/jaysql/internal/common/models"
)

// ---------------------------------------------------------------------------
// 数据访问权限申请（数据源对象的提权通道）
//
// 为什么需要这张表：
//   授权表（insight_das_data_grants）只记录「结果」，不记录「为什么给」。
//   普通用户没有被授权时，唯一能做的提权动作是走「自助授权」——而自助授权
//   被限制为「自己已有权限的子集」，本质上是权限的重新切分，拿不到新权限。
//   于是出现一个死结：没有权限 → 无法自助 → 永远没有权限。
//
//   本表补的是这条通道：用户直接选择目标数据源、库/表与所需操作发起申请，
//   说明真实事由；审批通过后由系统在同一事务里写入授权表。
//
// 三项硬约束：
//   1. 申请单本身不是权限——CheckDataAccess 只认授权表，申请单状态不参与判定，
//      因此「执行授权」必须在同一事务里写出授权记录，否则会形成第二套真相；
//   2. 申请人不能审批自己的申请（服务层拦截，不依赖前端隐藏按钮）；
//   3. 目标资源与操作维度在提交时落快照，审批期间元数据再变也不影响已批的内容。
//
// 审批与落地分两步（2026-10 起）：
//   审批通过只把单子推到 wait_grant（待执行授权），由执行人点「执行授权」才写授权表。
//   这样「谁批的」与「谁把权限落下去的」分别留痕，与 SQL 工单的审批/执行分离一致。
//   代价是 approved 状态存在一个中间态，列表与详情都必须显式展示 wait_grant，
//   否则申请人会以为「通过了却没权限」。
//
// 历史兼容：OrderID/OrderTitle/SQLType 是早期「凭工单申请」版本的快照字段，
// 新申请不再写入，仅在旧数据的详情展示里兜底。
// ---------------------------------------------------------------------------

// InsightDasAccessRequest 数据访问权限申请单
type InsightDasAccessRequest struct {
	*models.Model
	RequestNo string `gorm:"type:char(15);not null;default:'';comment:申请单号;uniqueIndex:uniq_access_request_no" json:"request_no"`
	// 历史兼容：早期版本要求凭 SQL 工单申请，新申请单不再关联工单
	OrderID    string `gorm:"type:char(15);not null;default:'';comment:关联工单ID(历史兼容);index:idx_access_request_order" json:"order_id"`
	OrderTitle string `gorm:"type:varchar(256);not null;default:'';comment:工单标题快照(历史兼容)" json:"order_title"`
	// 申请人。ApplicantID 落库是为了判定侧直接按 uid 匹配，
	// 不再依赖 username 字符串关联（同名/改名都会让 username 关联失效）。
	ApplicantID       uint64 `gorm:"type:bigint;not null;default:0;comment:申请人UID" json:"applicant_id"`
	ApplicantUsername string `gorm:"type:varchar(64);not null;default:'';comment:申请人账号;index:idx_access_request_applicant" json:"applicant_username"`
	ApplicantName     string `gorm:"type:varchar(128);not null;default:'';comment:申请人名称快照" json:"applicant_name"`
	// 目标资源快照：申请提交时确定，审批期间元数据再变也不影响已批的内容
	InstanceID    string `gorm:"type:char(15);not null;default:'';comment:数据源实例ID;index:idx_access_request_resource" json:"instance_id"`
	InstanceName  string `gorm:"type:varchar(255);not null;default:'';comment:数据源名称快照" json:"instance_name"`
	DBType        string `gorm:"type:varchar(32);not null;default:'';comment:数据库类型快照" json:"db_type"`
	Database      string `gorm:"type:varchar(255);not null;default:'';comment:数据库名快照" json:"database"`
	SchemaName    string `gorm:"type:varchar(255);not null;default:'';comment:库/Schema快照;index:idx_access_request_resource" json:"schema_name"`
	// 注意：字段名不能叫 TableName——会和 gorm 要求的 TableName() 方法重名导致编译失败，
	// 这里用 Table + column 显式映射到列 table_name。
	Table string `gorm:"column:table_name;type:varchar(128);not null;default:'';comment:表名快照;index:idx_access_request_resource" json:"table_name"`
	// ResourceSpec 多资源快照（2026-10-23 起）：多库 / 多模式 / 表维度圈选方式都在这里，
	// JSON 结构 {"databases":[],"schemas":["库.模式"],"table_mode":"list|wildcard|except","tables":[],"pattern":"","except":[]}。
	// 旧列（database / schema_name / table_name）只保留第一个资源的值做兼容展示，
	// spec 为空视为旧数据，按旧列解析成单资源。两份快照必须同事务写入。
	ResourceSpec string `gorm:"type:text;comment:多资源圈选快照JSON" json:"resource_spec"`
	ResourceLevel string `gorm:"type:varchar(16);not null;default:'database';comment:申请粒度:instance/database/table" json:"resource_level"`
	SQLType       string `gorm:"type:varchar(16);not null;default:'';comment:工单SQL类型快照(历史兼容)" json:"sql_type"`
	// 申请的操作类型：申请人直接勾选，逗号分隔 select/export/dml
	Actions string `gorm:"type:varchar(64);not null;default:'select';comment:申请的操作类型" json:"actions"`
	Reason  string `gorm:"type:varchar(512);not null;default:'';comment:申请事由" json:"reason"`
	// 申请的有效期（申请侧填的期望值）与实际批准值分开存：
	// 审批人可以缩短有效期，事后追溯时「申请的」与「批的」必须都能看到。
	RequestedExpireAt *models.LocalTime `gorm:"comment:申请有效期至" json:"requested_expire_at"`
	// ApprovedExpireAt 审批通过时确定的有效期。审批与落地分两步后，授权表要等到
	// 执行授权才写入，若不留这一列，审批人调整后的有效期会在两步之间丢失。
	ApprovedExpireAt *models.LocalTime `gorm:"comment:审批确定的有效期至" json:"approved_expire_at"`
	// 审批。ApproverUsername 是「当前处理人」：转交后会被改写，历史处理人看事件流水。
	Status           string            `gorm:"type:varchar(16);not null;default:'pending';comment:状态:pending/forwarded/wait_grant/approved/rejected/withdrawn/terminated/expired/revoked;index:idx_access_request_status" json:"status"`
	ApproverUsername string            `gorm:"type:varchar(64);not null;default:'';comment:审批人账号(当前处理人)" json:"approver_username"`
	ApproverName     string            `gorm:"type:varchar(128);not null;default:'';comment:审批人名称快照" json:"approver_name"`
	ApprovedAt       *models.LocalTime `gorm:"comment:审批时间" json:"approved_at"`
	ApproveComment   string            `gorm:"type:varchar(512);not null;default:'';comment:审批意见" json:"approve_comment"`
	// 执行授权：审批通过后由执行人写入授权表，这一步不推进审批链，只落权限
	ExecutorUsername string            `gorm:"type:varchar(64);not null;default:'';comment:执行授权人账号" json:"executor_username"`
	ExecutorName     string            `gorm:"type:varchar(128);not null;default:'';comment:执行授权人名称快照" json:"executor_name"`
	ExecutedAt       *models.LocalTime `gorm:"comment:执行授权时间" json:"executed_at"`
	// 授权落地：通过后指向 insight_das_data_grants.grant_no，是「申请」与「权限」的唯一关联点
	GrantNo     string            `gorm:"type:varchar(32);not null;default:'';comment:落地授权单号;index:idx_access_request_grant" json:"grant_no"`
	EffectiveAt *models.LocalTime `gorm:"comment:授权生效时间" json:"effective_at"`
	ExpireAt    *models.LocalTime `gorm:"comment:授权失效时间" json:"expire_at"`
}

func (InsightDasAccessRequest) TableName() string {
	return "insight_das_access_requests"
}

// InsightDasAccessRequestEvent 申请单流转事件。
//
// 申请单只保存当前状态，完整流转靠这张流水还原：
// 谁在什么时候、把状态从什么改成什么、依据是什么。
// 审批链路的合规追责只认真实落库的时间线，不认前端拼出来的展示。
type InsightDasAccessRequestEvent struct {
	*models.Model
	RequestNo string `gorm:"type:char(15);not null;default:'';comment:申请单号;index:idx_access_event_request" json:"request_no"`
	// EventType: create / approve / reject / forward / withdraw / terminate / grant / expire / revoke
	EventType        string `gorm:"type:varchar(16);not null;default:'';comment:事件类型" json:"event_type"`
	FromStatus       string `gorm:"type:varchar(16);not null;default:'';comment:变更前状态" json:"from_status"`
	ToStatus         string `gorm:"type:varchar(16);not null;default:'';comment:变更后状态" json:"to_status"`
	OperatorUsername string `gorm:"type:varchar(64);not null;default:'';comment:操作人账号;index:idx_access_event_operator" json:"operator_username"`
	OperatorName     string `gorm:"type:varchar(128);not null;default:'';comment:操作人名称快照" json:"operator_name"`
	Comment          string `gorm:"type:varchar(512);not null;default:'';comment:操作说明" json:"comment"`
	SourceIP         string `gorm:"type:varchar(64);not null;default:'';comment:来源IP" json:"source_ip"`
}

func (InsightDasAccessRequestEvent) TableName() string {
	return "insight_das_access_request_events"
}

package services

// ---------------------------------------------------------------------------
// 申请单的审批人候选
//
// 申请单要求申请人显式指定审批人（而不是「有审批权的第一个人」兜底），
// 所以入口必须能列出候选人：持有 das.access.approve 的用户（直接授权或角色继承），
// 外加超管（超管等价于持有全部权限）。
//
// 查询走原生 SQL 而不引 users/services：das/services 目前只依赖 users/models，
// 反过来引 users/services 会把「权限体系 → 数据授权」的依赖方向搅乱（users/services
// 已经引用 das/models）。
// ---------------------------------------------------------------------------

import (
	"errors"
	"strings"

	"github.com/simajiong/jaysql/internal/global"
)

const accessApprovePermissionCode = "das.access.approve"

// accessApproverCandidateSQL 与 users/services 的有效权限口径保持一致：
// 直接授权（insight_user_permissions）或角色继承（insight_user_roles → insight_role_permissions），
// 且权限本身 is_enabled、角色未软删、用户未软删且处于激活状态。
const accessApproverCandidateSQL = `
SELECT DISTINCT u.uid, u.username
FROM insight_users u
WHERE u.is_deleted = 0
  AND u.is_active = 1
  AND (
    u.is_superuser = 1
    OR EXISTS (
      SELECT 1 FROM insight_user_permissions up
      JOIN insight_permissions p ON p.id = up.permission_id
      WHERE up.uid = u.uid AND p.code = ? AND p.is_enabled = 1
    )
    OR EXISTS (
      SELECT 1 FROM insight_user_roles ur
      JOIN insight_roles r ON r.id = ur.role_id AND r.is_deleted = 0
      JOIN insight_role_permissions rp ON rp.role_id = ur.role_id
      JOIN insight_permissions p2 ON p2.id = rp.permission_id
      WHERE ur.uid = u.uid AND p2.code = ? AND p2.is_enabled = 1
    )
  )
ORDER BY u.username ASC
`

type accessApproverCandidateRow struct {
	UID      uint64
	Username string
}

// AccessApproverOption 审批人候选（详情页与新建页共用）
type AccessApproverOption struct {
	UID      uint64 `json:"uid"`
	Username string `json:"username"`
	// Name 与 Username 同值：本系统没有独立昵称列，展示口径统一用账号
	Name string `json:"name"`
}

// ListAccessApproverOptionsService 列出可指定为审批人的用户
type ListAccessApproverOptionsService struct {
	// Search 关键字（按账号模糊匹配）
	Search string
	// ExcludeUsername 需要排除的账号（转交时排除当前处理人自己）
	ExcludeUsername string
}

func (s *ListAccessApproverOptionsService) Run() ([]AccessApproverOption, error) {
	var rows []accessApproverCandidateRow
	if err := global.App.DB.Raw(accessApproverCandidateSQL, accessApprovePermissionCode, accessApprovePermissionCode).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	search := strings.ToLower(strings.TrimSpace(s.Search))
	exclude := strings.ToLower(strings.TrimSpace(s.ExcludeUsername))
	out := make([]AccessApproverOption, 0, len(rows))
	for _, row := range rows {
		if exclude != "" && strings.ToLower(row.Username) == exclude {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(row.Username), search) {
			continue
		}
		out = append(out, AccessApproverOption{UID: row.UID, Username: row.Username, Name: row.Username})
	}
	return out, nil
}

// accessApproverCheckSQL 单账号判定（提交 / 转交时用），口径与候选列表完全一致
const accessApproverCheckSQL = `
SELECT COUNT(1)
FROM insight_users u
WHERE u.username = ?
  AND u.is_deleted = 0
  AND u.is_active = 1
  AND (
    u.is_superuser = 1
    OR EXISTS (
      SELECT 1 FROM insight_user_permissions up
      JOIN insight_permissions p ON p.id = up.permission_id
      WHERE up.uid = u.uid AND p.code = ? AND p.is_enabled = 1
    )
    OR EXISTS (
      SELECT 1 FROM insight_user_roles ur
      JOIN insight_roles r ON r.id = ur.role_id AND r.is_deleted = 0
      JOIN insight_role_permissions rp ON rp.role_id = ur.role_id
      JOIN insight_permissions p2 ON p2.id = rp.permission_id
      WHERE ur.uid = u.uid AND p2.code = ? AND p2.is_enabled = 1
    )
  )
`

// ensureAccessApproverCandidate 校验目标账号确实可以审批。
//
// 不能只校验「用户存在」：指定一个没有审批权的人，等于把单子投进一个没人能处理的池子，
// 申请人只能撤回重提。这里直接按有效权限判定，错误信息里说清原因。
func ensureAccessApproverCandidate(username string) error {
	name := strings.TrimSpace(username)
	if name == "" {
		return errors.New("请选择审批人")
	}
	var count int64
	if err := global.App.DB.Raw(accessApproverCheckSQL, name, accessApprovePermissionCode, accessApprovePermissionCode).
		Scan(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return errors.New("指定的审批人不具备数据访问审批权限（或账号不存在 / 已停用）")
	}
	return nil
}

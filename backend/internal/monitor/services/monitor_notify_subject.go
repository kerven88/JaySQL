package services

import (
	"fmt"
	"sort"
	"strings"

	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	usersModels "github.com/simajiong/jaysql/internal/users/models"
	"github.com/simajiong/jaysql/pkg/notifier"
)

// ============================================================================
// 接收人解析：把「配置时选的人 / 组织 / 通知组」展开成「发送时要的地址」
//
// 三条不可违背的规则（写在这里，也写进测试）：
//  1. 地址缺失不阻塞整体投递：某人没有手机号，只影响短信那一条任务；
//  2. 同一地址只发一次：一个人既在组织树里又被单独选中，同渠道不重复投递；
//  3. 无法解析的接收人要显式告警（warnings），绝不静默丢消息。
// ============================================================================

// notifyResolvedRecipient 解析后的接收人（含多形态地址）。
type notifyResolvedRecipient struct {
	UserID uint64
	Name   string
	Email  string
	Mobile string
}

// notifySubjectResolution 一次解析的完整结果。
type notifySubjectResolution struct {
	Recipients []notifyResolvedRecipient
	Mentions   notifier.Mentions
	Warnings   []string
}

// resolveNotifySubjects 展开接收人列表。
func resolveNotifySubjects(subjects []monitorModels.NotifySubject) notifySubjectResolution {
	resolution := notifySubjectResolution{}
	if len(subjects) == 0 {
		return resolution
	}

	seenUsers := map[uint64]struct{}{}
	seenRaw := map[string]struct{}{}
	seenOrgs := map[string]struct{}{}

	for _, subject := range subjects {
		switch strings.ToLower(strings.TrimSpace(subject.Type)) {
		case "user":
			user, err := loadNotifyUserByUID(subject.ID)
			if err != nil {
				resolution.Warnings = append(resolution.Warnings,
					fmt.Sprintf("接收人「%s」不存在或已删除", subject.ID))
				continue
			}
			if _, ok := seenUsers[user.UserID]; ok {
				continue
			}
			seenUsers[user.UserID] = struct{}{}
			resolution.Recipients = append(resolution.Recipients, user)

		case "org":
			key := strings.TrimSpace(subject.ID)
			if key == "" {
				continue
			}
			if _, ok := seenOrgs[key]; ok {
				continue
			}
			seenOrgs[key] = struct{}{}
			members, err := loadNotifyOrgMembers(key)
			if err != nil {
				resolution.Warnings = append(resolution.Warnings,
					fmt.Sprintf("用户组「%s」成员读取失败：%v", key, err))
				continue
			}
			if len(members) == 0 {
				resolution.Warnings = append(resolution.Warnings,
					fmt.Sprintf("用户组「%s」没有可用的成员", key))
				continue
			}
			for _, member := range members {
				if _, ok := seenUsers[member.UserID]; ok {
					continue
				}
				seenUsers[member.UserID] = struct{}{}
				resolution.Recipients = append(resolution.Recipients, member)
			}

		case "notify_group":
			// 值班组为二期能力（需要轮值规则），当前显式提示而不是假装成功
			resolution.Warnings = append(resolution.Warnings,
				fmt.Sprintf("通知组「%s」暂未启用（值班组能力计划在二期提供）", subject.ID))

		case "raw":
			address := strings.TrimSpace(subject.ID)
			if address == "" {
				continue
			}
			if _, ok := seenRaw[address]; ok {
				continue
			}
			seenRaw[address] = struct{}{}
			name := strings.TrimSpace(subject.Note)
			if name == "" {
				name = address
			}
			resolution.Recipients = append(resolution.Recipients, notifyResolvedRecipient{
				Name:  name,
				Email: address,
			})

		default:
			resolution.Warnings = append(resolution.Warnings,
				fmt.Sprintf("无法识别的接收人类型「%s」", subject.Type))
		}
	}

	// @ 名单：钉钉/企微按手机号 @ 人；缺失手机号的人不 @（但仍然收群消息）
	mobiles := make([]string, 0, len(resolution.Recipients))
	names := make([]string, 0, len(resolution.Recipients))
	for _, recipient := range resolution.Recipients {
		if strings.TrimSpace(recipient.Mobile) != "" {
			mobiles = append(mobiles, recipient.Mobile)
		}
		if strings.TrimSpace(recipient.Name) != "" {
			names = append(names, recipient.Name)
		}
	}
	resolution.Mentions = notifier.Mentions{Mobiles: uniqueStrings(mobiles), Names: uniqueStrings(names)}
	return resolution
}

// notifySubjectsByChannel 按渠道能力把解析结果转成该渠道的投递目标。
//
// 返回：tasks 目标列表（群渠道为单个空地址条目）+ 被跳过的接收人（缺地址）。
func notifySubjectsByChannel(caps notifier.Capabilities, resolution notifySubjectResolution) ([]notifyResolvedRecipient, []string) {
	skipped := make([]string, 0)
	if !caps.Personalizable {
		// 群渠道：投递目标是"群"本身，人只体现在 @ 名单里
		return []notifyResolvedRecipient{{Name: "群消息"}}, skipped
	}

	targets := make([]notifyResolvedRecipient, 0, len(resolution.Recipients))
	for _, recipient := range resolution.Recipients {
		address := strings.TrimSpace(recipient.Email)
		if caps.RequiresBinding {
			// 二期：短信/电话/飞书 @ 需要绑定地址，这里保持同一套过滤逻辑
			address = strings.TrimSpace(recipient.Mobile)
		}
		if address == "" {
			skipped = append(skipped, recipient.Name)
			continue
		}
		recipient.Email = address
		targets = append(targets, recipient)
	}
	return targets, skipped
}

// ---------- 查询 ----------

func loadNotifyUserByUID(raw string) (notifyResolvedRecipient, error) {
	var uid uint64
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &uid); err != nil || uid == 0 {
		return notifyResolvedRecipient{}, fmt.Errorf("用户 ID 非法")
	}
	var user usersModels.InsightUsers
	if err := global.App.DB.Where("uid = ?", uid).Take(&user).Error; err != nil {
		return notifyResolvedRecipient{}, err
	}
	return notifyResolvedRecipient{
		UserID: user.Uid,
		Name:   user.Username,
		Email:  strings.TrimSpace(user.Email),
		Mobile: strings.TrimSpace(user.Mobile),
	}, nil
}

// loadNotifyOrgMembers 取组织节点及其子树的全部成员。
//
// insight_orgs.key 是搜索路径（如 a/b/c），因此子树查询等价于前缀匹配。
// 注意 LIKE 的通配符：key 前缀后必须紧跟 '/'，否则 a/b 会误匹配 a/bc。
func loadNotifyOrgMembers(organizationKey string) ([]notifyResolvedRecipient, error) {
	key := strings.TrimSpace(organizationKey)
	if key == "" {
		return nil, nil
	}
	var uids []uint64
	err := global.App.DB.Table("insight_org_users").
		Select("DISTINCT uid").
		Where("organization_key = ? OR organization_key LIKE ?", key, key+"/%").
		Where("is_active = ?", true).
		Pluck("uid", &uids).Error
	if err != nil {
		return nil, err
	}
	if len(uids) == 0 {
		return nil, nil
	}

	var users []usersModels.InsightUsers
	if err := global.App.DB.Where("uid IN ? AND is_deleted = ?", uids, false).Find(&users).Error; err != nil {
		return nil, err
	}
	members := make([]notifyResolvedRecipient, 0, len(users))
	for _, user := range users {
		members = append(members, notifyResolvedRecipient{
			UserID: user.Uid,
			Name:   user.Username,
			Email:  strings.TrimSpace(user.Email),
			Mobile: strings.TrimSpace(user.Mobile),
		})
	}
	sort.SliceStable(members, func(i, j int) bool { return members[i].UserID < members[j].UserID })
	return members, nil
}

// ---------- 展示用摘要（策略列表） ----------

// notifySubjectSummary 把接收人列表转成人可读的摘要。
func notifySubjectSummary(subjects []monitorModels.NotifySubject) []string {
	summary := make([]string, 0, len(subjects))
	for _, subject := range subjects {
		switch strings.ToLower(strings.TrimSpace(subject.Type)) {
		case "user":
			if user, err := loadNotifyUserByUID(subject.ID); err == nil {
				summary = append(summary, user.Name)
			} else {
				summary = append(summary, fmt.Sprintf("用户#%s(已失效)", subject.ID))
			}
		case "org":
			name := notifyOrgName(subject.ID)
			count, _ := loadNotifyOrgMembers(subject.ID)
			if name == "" {
				name = subject.ID
			}
			summary = append(summary, fmt.Sprintf("%s（%d 人）", name, len(count)))
		case "notify_group":
			summary = append(summary, fmt.Sprintf("通知组#%s（二期）", subject.ID))
		case "raw":
			summary = append(summary, subject.ID)
		}
	}
	return summary
}

// notifySubjectAddressCount 可达地址数（用于配置界面的"覆盖率"提示）。
func notifySubjectAddressCount(subjects []monitorModels.NotifySubject) int {
	resolution := resolveNotifySubjects(subjects)
	addresses := map[string]struct{}{}
	for _, recipient := range resolution.Recipients {
		if recipient.Email != "" {
			addresses[recipient.Email] = struct{}{}
		}
		if recipient.Mobile != "" {
			addresses[recipient.Mobile] = struct{}{}
		}
	}
	return len(addresses)
}

func notifyOrgName(organizationKey string) string {
	if global.App.DB == nil {
		return ""
	}
	var name string
	if err := global.App.DB.Table("insight_orgs").
		Select("name").Where("`key` = ?", strings.TrimSpace(organizationKey)).
		Limit(1).Scan(&name).Error; err != nil {
		return ""
	}
	return name
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

// notifyChannelMetaSafe 取渠道能力，未注册的类型返回 false（调用方按"非个人渠道"处理）。
func notifyChannelMetaSafe(kind string) (notifier.Capabilities, bool) {
	channel, ok := notifier.Get(kind)
	if !ok {
		return notifier.Capabilities{}, false
	}
	return channel.Capabilities(), true
}

// ---------- 接收人选择器的数据源 ----------

// NotifySubjectOrg 组织树节点（作为"用户组"供选择）。
type NotifySubjectOrg struct {
	ID       uint64 `json:"id"`
	Name     string `json:"name"`
	ParentID uint64 `json:"parent_id"`
	Key      string `json:"key"`
	Level    uint64 `json:"level"`
	Members  int    `json:"members"`
}

// NotifySubjectUser 可选用户（邮箱/手机号做部分打码，避免界面明文外泄）。
type NotifySubjectUser struct {
	UID         uint64 `json:"uid"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	Mobile      string `json:"mobile"`
	HasEmail    bool   `json:"has_email"`
	HasMobile   bool   `json:"has_mobile"`
	EmailMasked string `json:"email_masked"`
	PhoneMasked string `json:"phone_masked"`
}

// NotifySubjectTree 返回接收人选择器所需的组织树与用户清单。
func NotifySubjectTree() (map[string]any, error) {
	if global.App.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	var orgs []NotifySubjectOrg
	// key 是 MySQL 8 的保留字，必须加反引号，否则直接 1064 语法错误
	if err := global.App.DB.Table("insight_orgs").
		Select("id, name, parent_id, `key`, level").
		Order("level asc, id asc").Scan(&orgs).Error; err != nil {
		return nil, err
	}
	// 成员数：一次聚合查询，避免前端逐节点请求
	memberCounts := map[string]int{}
	rows, err := global.App.DB.Table("insight_org_users").
		Select("organization_key, COUNT(DISTINCT uid) AS members").
		Where("is_active = ?", true).
		Group("organization_key").Rows()
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var key string
			var count int
			if scanErr := rows.Scan(&key, &count); scanErr == nil {
				memberCounts[key] = count
			}
		}
	}
	for index := range orgs {
		orgs[index].Members = memberCounts[orgs[index].Key]
	}

	var users []usersModels.InsightUsers
	if err := global.App.DB.Where("is_deleted = ?", false).Order("uid asc").Find(&users).Error; err != nil {
		return nil, err
	}
	items := make([]NotifySubjectUser, 0, len(users))
	for _, user := range users {
		email := strings.TrimSpace(user.Email)
		mobile := strings.TrimSpace(user.Mobile)
		items = append(items, NotifySubjectUser{
			UID:         user.Uid,
			Username:    user.Username,
			Email:       email,
			Mobile:      mobile,
			HasEmail:    email != "",
			HasMobile:   mobile != "",
			EmailMasked: maskEmail(email),
			PhoneMasked: maskPhone(mobile),
		})
	}

	return map[string]any{
		"orgs":  orgs,
		"users": items,
		"subject_types": []map[string]any{
			{"type": "user", "label": "用户", "available": true},
			{"type": "org", "label": "用户组（组织节点，含子节点成员）", "available": true},
			{"type": "notify_group", "label": "通知组（值班组，二期）", "available": false},
			{"type": "raw", "label": "外部地址", "available": true},
		},
	}, nil
}

// maskEmail 邮箱打码：只保留首字符与域名。
func maskEmail(email string) string {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return email
	}
	local := parts[0]
	if len(local) <= 1 {
		return "*@" + parts[1]
	}
	return local[:1] + "***@" + parts[1]
}

// maskPhone 手机号打码：保留前 3 后 4。
func maskPhone(mobile string) string {
	runes := []rune(mobile)
	if len(runes) < 7 {
		return mobile
	}
	return string(runes[:3]) + "****" + string(runes[len(runes)-4:])
}

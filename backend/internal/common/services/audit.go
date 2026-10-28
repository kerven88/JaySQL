package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/simajiong/jaysql/internal/common/forms"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	usersModels "github.com/simajiong/jaysql/internal/users/models"
	"github.com/simajiong/jaysql/pkg/pagination"
	"gorm.io/datatypes"
)

type AuditLogEvent struct {
	Username       string
	AuditLevel     string
	ModuleCode     string
	ModuleName     string
	ActionCode     string
	ActionName     string
	ActionResult   string
	ResourceType   string
	ResourceID     string
	ResourceName   string
	RelatedOrderID string
	Summary        string
	ErrorMessage   string
	RequestPayload any
	BeforeData     any
	AfterData      any
	SQLText        string
}

type GetAuditLogsService struct {
	*forms.GetAuditLogsForm
	C *gin.Context
}

type GetAuditLogDetailService struct {
	C       *gin.Context
	AuditID string
}

func WriteAuditLog(c *gin.Context, event AuditLogEvent) error {
	if c == nil {
		return nil
	}
	username := strings.TrimSpace(event.Username)
	if username == "" {
		username = currentAuditUsername(c)
	}
	operatorName := lookupAuditOperatorName(username)
	record := commonModels.InsightAuditLogs{
		Model:            &commonModels.Model{},
		AuditID:          buildAuditID(),
		RequestID:        requestid.Get(c),
		OperatedAt:       commonModels.LocalTime(time.Now()),
		OperatorUsername: username,
		OperatorName:     operatorName,
		OperatorIP:       strings.TrimSpace(c.ClientIP()),
		OperatorTerminal: strings.TrimSpace(c.Request.UserAgent()),
		AuditLevel:       commonModels.EnumType(defaultAuditLevel(event.AuditLevel)),
		ModuleCode:       strings.TrimSpace(event.ModuleCode),
		ModuleName:       strings.TrimSpace(event.ModuleName),
		ActionCode:       strings.TrimSpace(event.ActionCode),
		ActionName:       strings.TrimSpace(event.ActionName),
		ActionResult:     commonModels.EnumType(defaultAuditResult(event.ActionResult)),
		ResourceType:     strings.TrimSpace(event.ResourceType),
		ResourceID:       strings.TrimSpace(event.ResourceID),
		ResourceName:     strings.TrimSpace(event.ResourceName),
		RelatedOrderID:   strings.TrimSpace(event.RelatedOrderID),
		Summary:          truncateText(strings.TrimSpace(event.Summary), 512),
		ErrorMessage:     truncateText(strings.TrimSpace(event.ErrorMessage), 2048),
		RequestPayload:   marshalAuditJSON(event.RequestPayload),
		BeforeData:       marshalAuditJSON(event.BeforeData),
		AfterData:        marshalAuditJSON(event.AfterData),
		SQLText:          strings.TrimSpace(event.SQLText),
	}
	return global.App.DB.Create(&record).Error
}

func TryWriteAuditLog(c *gin.Context, event AuditLogEvent) {
	if err := WriteAuditLog(c, event); err != nil {
		global.App.Log.Warnf("write audit log failed: %v", err)
	}
}

func (s *GetAuditLogsService) Run() (responseData any, total int64, err error) {
	type result struct {
		AuditID          string                 `json:"audit_id"`
		OperatedAt       commonModels.LocalTime `json:"operated_at"`
		OperatorUsername string                 `json:"operator_username"`
		OperatorName     string                 `json:"operator_name"`
		AuditLevel       string                 `json:"audit_level"`
		ModuleCode       string                 `json:"module_code"`
		ModuleName       string                 `json:"module_name"`
		ActionName       string                 `json:"action_name"`
		ActionResult     string                 `json:"action_result"`
		Summary          string                 `json:"summary"`
	}
	var rows []result
	tx := global.App.DB.Table("insight_audit_logs").
		Select("audit_id, operated_at, operator_username, operator_name, audit_level, module_code, module_name, action_name, action_result, summary").
		Order("operated_at desc, id desc")
	if strings.TrimSpace(s.StartTime) != "" {
		tx = tx.Where("operated_at >= ?", strings.TrimSpace(s.StartTime))
	}
	if strings.TrimSpace(s.EndTime) != "" {
		tx = tx.Where("operated_at <= ?", strings.TrimSpace(s.EndTime))
	}
	if strings.TrimSpace(s.OperatorUsername) != "" {
		like := "%" + strings.TrimSpace(s.OperatorUsername) + "%"
		tx = tx.Where("operator_username LIKE ? OR operator_name LIKE ?", like, like)
	}
	if strings.TrimSpace(s.AuditLevel) != "" {
		tx = tx.Where("audit_level = ?", strings.TrimSpace(s.AuditLevel))
	}
	if strings.TrimSpace(s.ModuleCode) != "" {
		tx = tx.Where("module_code = ?", strings.TrimSpace(s.ModuleCode))
	}
	if strings.TrimSpace(s.ActionResult) != "" {
		tx = tx.Where("action_result = ?", strings.TrimSpace(s.ActionResult))
	}
	if strings.TrimSpace(s.Keyword) != "" {
		like := "%" + strings.TrimSpace(s.Keyword) + "%"
		tx = tx.Where("action_name LIKE ? OR summary LIKE ? OR resource_name LIKE ? OR related_order_id LIKE ?", like, like, like, like)
	}
	total = pagination.Pager(&s.PaginationQ, tx, &rows)
	return &rows, total, nil
}

func (s *GetAuditLogDetailService) Run() (any, error) {
	var row commonModels.InsightAuditLogs
	if err := global.App.DB.Where("audit_id = ?", s.AuditID).Take(&row).Error; err != nil {
		return nil, err
	}
	return row, nil
}

func currentAuditUsername(c *gin.Context) string {
	claims := jwt.ExtractClaims(c)
	raw, ok := claims["id"]
	if !ok {
		return ""
	}
	username, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(username)
}

func lookupAuditOperatorName(username string) string {
	if strings.TrimSpace(username) == "" {
		return ""
	}
	var user usersModels.InsightUsers
	if err := global.App.DB.Select("username").Where("username = ?", username).Take(&user).Error; err != nil {
		return username
	}
	return user.Username
}

func buildAuditID() string {
	return "AUD-" + strings.ReplaceAll(strings.ToUpper(uuid.NewString()), "-", "")
}

func defaultAuditLevel(level string) string {
	value := strings.TrimSpace(strings.ToLower(level))
	if value == "high" {
		return "high"
	}
	return "normal"
}

func defaultAuditResult(result string) string {
	value := strings.TrimSpace(strings.ToLower(result))
	switch value {
	case "fail", "rejected", "cancelled":
		return value
	default:
		return "success"
	}
}

func marshalAuditJSON(value any) datatypes.JSON {
	if value == nil {
		return nil
	}
	sanitized := sanitizeAuditValue(value)
	buf, err := json.Marshal(sanitized)
	if err != nil {
		return nil
	}
	if len(buf) == 0 || string(buf) == "null" {
		return nil
	}
	return datatypes.JSON(buf)
}

func sanitizeAuditValue(value any) any {
	if value == nil {
		return nil
	}
	buf, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	var decoded any
	if err := json.Unmarshal(buf, &decoded); err != nil {
		return string(buf)
	}
	return maskSensitiveAuditData(decoded)
}

func maskSensitiveAuditData(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for k, v := range typed {
			if isSensitiveAuditKey(k) {
				typed[k] = "***"
				continue
			}
			typed[k] = maskSensitiveAuditData(v)
		}
		return typed
	case []any:
		for i, v := range typed {
			typed[i] = maskSensitiveAuditData(v)
		}
		return typed
	default:
		return typed
	}
}

func isSensitiveAuditKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "password", "old_password", "new_password", "token", "authorization", "secret", "access_key", "secret_key":
		return true
	default:
		return false
	}
}

func truncateText(value string, maxLen int) string {
	if maxLen <= 0 || len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}

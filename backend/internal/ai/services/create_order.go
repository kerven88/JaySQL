package services

import (
	"fmt"
	"strings"

	aiForms "github.com/simajiong/jaysql/internal/ai/forms"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	ordersForms "github.com/simajiong/jaysql/internal/orders/forms"
	ordersServices "github.com/simajiong/jaysql/internal/orders/services"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ConfirmCreateOrderService 用户在 AI 对话里点「确认创建」之后真正建单。
//
// 与工单创建页共用 orders 模块的 CreateOrderService，因此建单后的行为完全一致：
// 记录直接以 Progress = WAIT_PRECHECK（待规范预审）落库，并在事务提交后生成一份
// 规范预审快照。也就是说「进入规范预审」本身不需要额外的提交动作——工单被创建出来的
// 那一刻就已经在预审环节里了，这正是本能力不需要用户再去点「提交预审」的原因。
//
// 刻意不复用创建页的入口，是为了绕开 CreateOrderForm 上 db_type 的 oneof 限制
// （只允许 MySQL/PostgreSQL）：AI 会对 TiDB / Doris 等实例建单，这里统一按
// instance_id 查实例补齐 db_type，既不受枚举限制，也拿不到「传错类型绕过权限校验」的缝。
type ConfirmCreateOrderService struct {
	Form     *aiForms.AICreateOrderConfirmForm
	Username string
	C        *gin.Context
}

// ConfirmCreateOrderResult 返回给对话卡片，用于就地展示建单结果
type ConfirmCreateOrderResult struct {
	OrderID    string `json:"order_id"`
	Title      string `json:"title"`
	Progress   string `json:"progress"`
	StatusText string `json:"status_text"`
	Applicant  string `json:"applicant"`
	Executor   string `json:"executor"`
	Instance   string `json:"instance"`
	DetailURL  string `json:"detail_url"`
}

// OrderProgressWaitPrecheck 工单创建后的初始进度：待规范预审。
// 与 orders 模块落库时写死的值是同一个口径，这里显式取出来回给前端做状态文案。
const OrderProgressWaitPrecheck = "WAIT_PRECHECK"

func (s *ConfirmCreateOrderService) Run() (*ConfirmCreateOrderResult, error) {
	applicant := strings.TrimSpace(s.Username)
	if applicant == "" {
		return nil, fmt.Errorf("无法确认当前登录用户，已中止建单")
	}
	if s.Form == nil {
		return nil, fmt.Errorf("缺少工单参数")
	}

	// 1. 实例必须已在平台登记；db_type / environment 一律以实例为准，不接受调用方指定
	var instance commonModels.InsightInstances
	err := global.App.DB.Table("`insight_instances`").
		Where("instance_id = ?", strings.TrimSpace(s.Form.InstanceID)).
		First(&instance).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("实例 %s 未在平台登记，请先在「实例管理」中录入后再建单", strings.TrimSpace(s.Form.InstanceID))
		}
		return nil, fmt.Errorf("查询实例失败: %w", err)
	}

	// 2. 执行人默认取当前登录用户；用户明确指定时才沿用其意图，合法性仍由创建服务校验
	executor := strings.TrimSpace(s.Form.Executor)
	if executor == "" {
		executor = applicant
	}

	form := &ordersForms.CreateOrderForm{
		Title:       strings.TrimSpace(s.Form.Title),
		Remark:      strings.TrimSpace(s.Form.Remark),
		DBType:      instance.DbType,
		InstanceID:  instance.InstanceID,
		Database:    strings.TrimSpace(s.Form.Database),
		Schema:      strings.TrimSpace(s.Form.Schema),
		Executor:    executor,
		RollbackSQL: s.Form.RollbackSQL,
		Content:     s.Form.Content,
	}

	// 3. 复用创建服务：内部完成 SQL 类型推断、目标库权限校验、落库（待预审）与预审快照生成
	service := ordersServices.CreateOrderService{
		CreateOrderForm: form,
		C:               s.C,
		Username:        applicant,
	}
	orderID, err := service.Run()
	if err != nil {
		return nil, err
	}

	return &ConfirmCreateOrderResult{
		OrderID:    orderID,
		Title:      form.Title,
		Progress:   OrderProgressWaitPrecheck,
		StatusText: "待规范预审",
		Applicant:  applicant,
		Executor:   executor,
		Instance:   fmt.Sprintf("%s:%d", instance.Hostname, instance.Port),
		DetailURL:  "/orders/" + orderID,
	}, nil
}

package services

import (
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/sqlsplit"
	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/orders/forms"
	ordersModels "github.com/simajiong/jaysql/internal/orders/models"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 生成执行任务
type GenOrderTasksService struct {
	*forms.GenOrderTasksForm
	C        *gin.Context
	Username string
}

func (s *GenOrderTasksService) subTasksExist() bool {
	// 如果tasks记录存在，跳过
	var count int64
	global.App.DB.
		Model(&ordersModels.InsightOrderTasks{}).
		Where("order_id = ?", s.OrderID).
		Count(&count)
	return count == 0
}

// orderTaskSpec 一条待生成的执行任务
type orderTaskSpec struct {
	SQL     string
	SQLType string
}

// buildOrderTaskSpecs 把工单内容拆成「语句 + 该语句自己的类型」。
//
// 混合工单（DDL + DML 同单）的关键就在这一步：任务表里必须落**每条语句自己的类型**，
// 不能整单共用一个值 —— 否则 DDL 语句会被塞进 DML 执行器（多走一遍 binlog 回滚
// SQL 生成，且拿不到任何回滚依据），DML 语句被塞进 DDL 执行器则完全不生成回滚依据。
func buildOrderTaskSpecs(content string, dbType string, orderSQLType string) ([]orderTaskSpec, error) {
	plan, err := BuildOrderPlan(content, dbType)
	if err == nil {
		specs := make([]orderTaskSpec, 0, len(plan.Statements))
		for _, st := range plan.Statements {
			specs = append(specs, orderTaskSpec{SQL: strings.TrimSpace(st.Text), SQLType: st.SQLType})
		}
		if len(specs) > 0 {
			return specs, nil
		}
	}
	// 语句计划构建失败（极端方言/超长注释等）：退化为单条兜底，
	// 但绝不把整段脏字符串直接塞给执行器，也不允许把 MIXED 当成任务类型落库。
	cleaned := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(content), ";"))
	if cleaned == "" {
		return nil, fmt.Errorf("工单SQL内容为空或全部为空语句，请重新提交")
	}
	sqlType := strings.TrimSpace(orderSQLType)
	if sqlType == "" || sqlType == orderSQLTypeMixed {
		sqlType = sqlsplit.StatementType(cleaned)
	}
	if sqlType == "" || sqlType == orderSQLTypeMixed {
		return nil, fmt.Errorf("未能识别工单SQL语句类型，请检查语句是否为标准 DDL、DML 后重新提交")
	}
	return []orderTaskSpec{{SQL: cleaned, SQLType: sqlType}}, nil
}

func (s *GenOrderTasksService) Run() (err error) {
	var record ordersModels.InsightOrderRecords
	tx := global.App.DB.Table("`insight_order_records`").Where("order_id=?", s.OrderID).Take(&record)
	if tx.RowsAffected == 0 {
		return fmt.Errorf("记录`%s`不存在", s.OrderID)
	}
	if s.Username != record.Executor {
		return fmt.Errorf("您不是工单执行人，没有执行工单权限")
	}
	if !utils.IsContain([]string{"APPROVED", "EXECUTING", "COMPLETED", "FAILED"}, string(record.Progress)) {
		return fmt.Errorf("当前工单状态，禁止操作")
	}
	if !s.subTasksExist() {
		return nil
	}

	// 把用户提交的多条 SQL 拆成独立 task（解决：多语句一次性塞给 db.Exec 导致 Error 1064 near ...）
	// 每条任务带上自己的类型与执行序号，混合工单按序号还原语句书写顺序。
	specs, err := buildOrderTaskSpecs(record.Content, string(record.DBType), string(record.SQLType))
	if err != nil {
		return err
	}

	now := time.Now().Format("2006-01-02 15:04:05")
	tasks := make([]map[string]any, 0, len(specs))
	for i := range specs {
		tasks = append(tasks, map[string]any{
			"OrderID":    s.OrderID,
			"TaskID":     utils.GenerateOrderTaskID(),
			"Seq":        i + 1,
			"DBType":     record.DBType,
			"SQLType":    specs[i].SQLType,
			"SQL":        specs[i].SQL,
			"created_at": now,
			"updated_at": now,
		})
	}
	return global.App.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&ordersModels.InsightOrderTasks{}).Create(tasks).Error; err != nil {
			global.App.Log.Error(err)
			return err
		}
		return nil
	})
}

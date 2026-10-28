package services

import (
	"fmt"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/simajiong/jaysql/pkg/pagination"

	"github.com/simajiong/jaysql/internal/orders/forms"
	ordersModels "github.com/simajiong/jaysql/internal/orders/models"

	"github.com/gin-gonic/gin"
)

// 获取任务列表
type GetTasksServices struct {
	*forms.GetTasksForm
	C        *gin.Context
	OrderID  string `json:"order_id"`
	Username string
}

func (s *GetTasksServices) Run() (responseData any, total int64, err error) {
	var records []ordersModels.InsightOrderTasks
	var orderCount int64
	if err := global.App.DB.Model(&ordersModels.InsightOrderRecords{}).Where("order_id=?", s.OrderID).Count(&orderCount).Error; err != nil {
		return records, total, err
	}
	if orderCount == 0 {
		return records, total, fmt.Errorf("工单记录`%s`不存在", s.OrderID)
	}
	tx := global.App.DB.Table("`insight_order_tasks`").Where("order_id=?", s.OrderID)
	// 搜索
	if s.Search != "" {
		tx = tx.Where("`sql` like ?", "%"+s.Search+"%")
	}
	if s.Progress != "" {
		tx = tx.Where("progress=?", s.Progress)
	}
	total = pagination.Pager(&s.PaginationQ, tx, &records)
	return &records, total, nil
}

// 预览任务统计
type PreviewTasksServices struct {
	*forms.PreviewTasksForm
	C *gin.Context
}

func (s *PreviewTasksServices) Run() (responseData any, err error) {
	type record struct {
		Total                        int `json:"total"`
		Unexecuted                   int `json:"unexecuted"`
		Processing                   int `json:"processing"`
		Completed                    int `json:"completed"`
		CompletedWithRollbackFailure int `json:"completed_with_rollback_failure"`
		Failed                       int `json:"failed"`
		Paused                       int `json:"paused"`
	}
	var records record
	global.App.DB.Table("`insight_order_tasks`").
		Select("COUNT(*) as total, SUM(if(progress='未执行',1,0)) as unexecuted, SUM(if(progress='执行中',1,0)) as processing, SUM(if(progress='已完成',1,0)) as completed, SUM(if(progress='已失败',1,0)) as failed,SUM(if(progress='已暂停',1,0)) as paused").
		Where("order_id=?", s.OrderID).
		Take(&records)

	return records, nil
}

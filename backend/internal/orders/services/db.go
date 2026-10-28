package services

import (
	"fmt"
	"strings"

	"github.com/simajiong/jaysql/internal/orders/models"
	"gorm.io/gorm"
)

func WriteOrderLog(tx *gorm.DB, order_id, username, msg string) error {
	orderID := strings.TrimSpace(order_id)
	if orderID == "" {
		return fmt.Errorf("工单ID不能为空")
	}
	if err := tx.Create(&models.InsightOrderLogs{
		OrderID:  orderID,
		Username: username,
		Msg:      msg,
	}).Error; err != nil {
		return err
	}
	return nil
}

package tidb

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/orders/api/base"
)

// TiDB DML
type ExecuteTiDBDML struct {
	*base.DBConfig
}

// 执行TiDB DML
func (e *ExecuteTiDBDML) Run() (data base.ReturnData, err error) {
	logger := base.NewExecuteLogger()
	publisher := base.NewPublisher()

	log := func(msg string) {
		cleaned := strings.TrimSpace(msg)
		formatted := logger.Add(cleaned) + "\n"
		publisher.Publish(e.OrderID, e.TaskID, utils.RenderLogStream, formatted)
	}

	// CREATE A NEW DATABASE CONNECTION
	db, err := NewTiDBCnx(e.DBConfig)
	if err != nil {
		log(fmt.Sprintf("访问数据库(%s:%d)失败，错误：%s", e.Hostname, e.Port, err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	defer db.Close()
	log(fmt.Sprintf("访问数据库(%s:%d)成功", e.Hostname, e.Port))

	// GET CONNECTION ID
	connectionID, err := DaoGetTiDBConnectionID(db)
	if err != nil {
		log(fmt.Sprintf("获取数据库Connection ID失败，错误：%s", err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	log(fmt.Sprintf("数据库Connection ID：%d", connectionID))

	// 多条SQL拆句执行（同 MySQL DDL/DML 修复模式：避免整包塞 db.Exec 导致 1064）
	startTime := time.Now()
	affectedRows, err := executeTiDBSQLsInConnection(db, e.SQL, log,
		func(d *sql.DB, s string, c chan<- int64) (int64, error) { return DaoTiDBExecute(d, s, c) },
		e.DBConfig,
		connectionID,
	)
	if err != nil {
		log(fmt.Sprintf("SQL执行失败，错误：%s", err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	endTime := time.Now()
	executeCostTime := utils.HumanfriendlyTimeUnit(endTime.Sub(startTime))
	log(fmt.Sprintf("SQL执行成功，累计影响行数%d，执行总耗时：%s", affectedRows, executeCostTime))

	// TiDB不支持生成回滚SQL
	log("TiDB不支持生成回滚SQL")

	// 返回数据
	data.ExecuteLog = logger.String()
	data.AffectedRows = affectedRows
	data.ExecuteCostTime = executeCostTime
	return
}

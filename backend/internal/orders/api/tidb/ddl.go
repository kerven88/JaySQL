package tidb

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/pkg/parser"
	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/orders/api/base"
)

// 同一连接内按拆分顺序执行多条SQL（支持「整批SQL单个task」场景）
func executeTiDBSQLsInConnection(db *sql.DB, fullSQL string,
	log func(string),
	executeOne func(db *sql.DB, single string, ch chan<- int64) (int64, error),
	cfg *base.DBConfig,
	connectionID int64,
) (totalAffected int64, err error) {
	parts, splitErr := parser.SplitSQLText(fullSQL)
	if splitErr != nil || len(parts) == 0 {
		parts = []string{fullSQL}
	}
	validParts := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			validParts = append(validParts, s)
		}
	}
	if len(validParts) == 0 {
		return 0, errors.New("没有可执行的SQL语句")
	}
	for idx, one := range validParts {
		log(fmt.Sprintf("开始执行第 %d/%d 条SQL", idx+1, len(validParts)))
		ch1 := make(chan int64)
		go DaoTiDBGetProcesslist(cfg, cfg.OrderID, connectionID, ch1)
		startT := time.Now()
		n, e := executeOne(db, one, ch1)
		cost := utils.HumanfriendlyTimeUnit(time.Since(startT))
		if e != nil {
			log(fmt.Sprintf("第 %d/%d 条SQL执行失败，错误：%s，耗时：%s", idx+1, len(validParts), e.Error(), cost))
			return totalAffected, e
		}
		totalAffected += n
		log(fmt.Sprintf("第 %d/%d 条SQL执行成功，影响行数%d，耗时：%s", idx+1, len(validParts), n, cost))
	}
	return totalAffected, nil
}

// 执行Online DDL语句
func (e *ExecuteTiDBDDL) ExecuteOnlineDDL() (data base.ReturnData, err error) {
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

	// SHOW PROCESS + 多条语句拆句执行
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
	log(fmt.Sprintf("SQL全部执行成功，累计影响行数%d，总耗时：%s", affectedRows, executeCostTime))

	// 返回数据
	data.ExecuteLog = logger.String()
	data.AffectedRows = affectedRows
	data.ExecuteCostTime = executeCostTime
	return
}

// TiDB DDL
type ExecuteTiDBDDL struct {
	*base.DBConfig
}

func (e *ExecuteTiDBDDL) Run() (data base.ReturnData, err error) {
	// Create/Drop/Truncate/Rename/Alter
	sqlType, err := parser.GetSqlStatement(e.SQL)
	if err != nil {
		return
	}

	switch sqlType {
	case "CreateDatabase", "CreateTable", "CreateView":
		return e.ExecuteOnlineDDL()
	case "DropTable", "DropIndex":
		return e.ExecuteOnlineDDL()
	case "TruncateTable":
		return e.ExecuteOnlineDDL()
	case "RenameTable":
		return e.ExecuteOnlineDDL()
	case "CreateIndex":
		return e.ExecuteOnlineDDL()
	case "DropDatabase":
		return data, errors.New("【风险】禁止执行drop database操作")
	case "AlterTable":
		return e.ExecuteOnlineDDL()
	default:
		return data, fmt.Errorf("当前SQL未匹配到规则，执行失败，SQL类型为：%s", sqlType)
	}
}

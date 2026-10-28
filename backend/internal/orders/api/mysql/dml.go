package mysql

import (
	"fmt"
	"strings"
	"time"

	"github.com/simajiong/jaysql/pkg/utils"

	"github.com/simajiong/jaysql/internal/orders/api/base"
)

// dmlRollbackMaxAffectedRows 触发「跳过 Binlog 回滚 SQL 生成」的影响行数阈值。
// 十万行级别的行事件解析会造成：巨量 Binlog 读取 + 逐事件建连 + 数十万条
// 回滚 SQL 常驻内存，仅适用于小批量变更，不适合批量清理。
const dmlRollbackMaxAffectedRows int64 = 100000

// MySQL DML
type ExecuteMySQLDML struct {
	*base.DBConfig
}

// 执行MySQL DML语句
func (e *ExecuteMySQLDML) Run() (data base.ReturnData, err error) {
	logger := base.NewExecuteLogger()
	publisher := base.NewPublisher()

	log := func(msg string) {
		cleaned := strings.TrimSpace(msg)
		formatted := logger.Add(cleaned) + "\n"
		publisher.Publish(e.OrderID, e.TaskID, utils.RenderLogStream, formatted)
	}

	// CREATE A NEW DATABASE CONNECTION
	db, err := NewMySQLCnx(e.DBConfig)
	if err != nil {
		log(fmt.Sprintf("访问数据库(%s:%d)失败，错误：%s", e.DBConfig.Hostname, e.DBConfig.Port, err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	defer db.Close()
	log(fmt.Sprintf("访问数据库(%s:%d)成功", e.DBConfig.Hostname, e.DBConfig.Port))

	// GET CONNECTION ID
	connectionID, err := DaoMySQLGetConnectionID(db)
	if err != nil {
		log(fmt.Sprintf("获取数据库Connection ID失败，错误：%s", err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	log(fmt.Sprintf("数据库Connection ID：%d", connectionID))

	// SHOW PROCESS
	ch1 := make(chan int64)
	go DaoMySQLGetProcesslist(e.DBConfig, e.OrderID, connectionID, ch1)

	// 获取执行开始前的binlog position
	startFile, startPosition, err := DaoMySQLGetBinlogPos(db)
	if err != nil {
		log(fmt.Sprintf("获取Start Binlog File和Position失败，错误：%s", err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	log(fmt.Sprintf("Start Binlog File：%s，Position：%d", startFile, startPosition))

	// 执行SQL
	startTime := time.Now()
	affectedRows, err := DaoMySQLExecute(db, e.SQL, ch1)
	if err != nil {
		log(fmt.Sprintf("SQL执行失败，错误：%s", err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	endTime := time.Now()
	executeCostTime := utils.HumanfriendlyTimeUnit(endTime.Sub(startTime))
	log(fmt.Sprintf("SQL执行成功，影响行数%d，执行耗时：%s", affectedRows, executeCostTime))

	data.AffectedRows = affectedRows
	data.ExecuteCostTime = executeCostTime

	// 获取执行后的binlog position
	endFile, endPosition, err := DaoMySQLGetBinlogPos(db)
	if err != nil {
		log(fmt.Sprintf("获取End Binlog File和Position失败，错误：%s", err.Error()))
		data.ExecuteLog = logger.String()
		return data, base.SQLExecuteError{Err: err}
	}
	log(fmt.Sprintf("End Binlog File：%s，Position：%d", endFile, endPosition))

	var rollbackSQL, backupCostTime string
	// 影响行数超过阈值时跳过 Binlog 回滚 SQL 生成：
	// 回滚 SQL 需要逐行解析 Binlog 行事件并拼接 INSERT，行数达到十万级时需要
	// 读取巨量 Binlog 事件（且历史上每个事件都会新建一次数据库连接），
	// 会把工单执行链拖死数十小时并撑爆内存。此类批量清理应走分批删除 + 备份/闪回。
	if affectedRows > dmlRollbackMaxAffectedRows {
		log(fmt.Sprintf("影响行数 %d 超过阈值 %d，已跳过 Binlog 回滚 SQL 生成（批量清理场景请使用分批删除，回滚请使用备份或闪回工具）",
			affectedRows, dmlRollbackMaxAffectedRows))
		data.ExecuteLog = logger.String()
		data.AffectedRows = affectedRows
		data.ExecuteCostTime = executeCostTime
		data.BackupStatus = "已跳过（影响行数超阈值）"
		return
	}
	// 影响行数大于0，才执行生成回滚SQL操作
	if affectedRows > 0 {
		// 生成回滚SQL
		log("开始解析Binlog生成回滚SQL")
		startTime = time.Now()
		binlog := Binlog{
			DBConfig:      e.DBConfig,
			ConnectionID:  connectionID,
			StartFile:     startFile,
			StartPosition: startPosition,
			EndFile:       endFile,
			EndPosition:   endPosition}
		rollbackSQL, err = binlog.Run()
		if err != nil {
			// SQL 本身已执行成功，失败的是「生成回滚 SQL」这一步。
			// 返回 BackupSQLError（而非 SQLExecuteError）让上层按「备份失败处理」策略处理，
			// 避免把已生效的变更误报为执行失败。
			log(fmt.Sprintf("生成回滚SQL失败，错误：%s", err.Error()))
			data.ExecuteLog = logger.String()
			data.AffectedRows = affectedRows
			data.ExecuteCostTime = executeCostTime
			data.BackupStatus = "备份失败：" + err.Error()
			return data, base.BackupSQLError{Err: err}
		}

		endTime = time.Now()
		backupCostTime = utils.HumanfriendlyTimeUnit(endTime.Sub(startTime))
		log(fmt.Sprintf("生成回滚SQL成功，耗时：%s", backupCostTime))
		data.BackupStatus = "备份成功"
	} else {
		data.BackupStatus = "已跳过（无数据变更）"
	}
	// 返回数据
	data.ExecuteLog = logger.String()
	data.RollbackSQL = rollbackSQL
	data.BackupCostTime = backupCostTime
	return
}

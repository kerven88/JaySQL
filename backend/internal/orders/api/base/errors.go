package base

// SQL执行失败
type SQLExecuteError struct {
	Err error
}

func (e SQLExecuteError) Error() string {
	return e.Err.Error()
}

// 生成回滚SQL失败
type RollbackSQLError struct {
	Err error
}

func (e RollbackSQLError) Error() string {
	return e.Err.Error()
}

// BackupSQLError 备份（MySQL 类实为 Binlog 回滚 SQL 生成）失败。
// 与 SQLExecuteError 的区别很关键：此时**目标 SQL 已经执行成功**，
// 只是「备份/回滚依据」没拿到。执行循环据此套用「备份失败处理」策略
// （CONTINUE 继续后续任务 / STOP 停止后续任务），而不是把任务判为执行失败。
type BackupSQLError struct {
	Err error
}

func (e BackupSQLError) Error() string {
	return e.Err.Error()
}

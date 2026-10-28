package tasks

import "testing"

// Cron 未初始化时注册必须安全退出（不能 panic）。
// 平台启动顺序里 InitializeCron 会先创建 Cron 句柄，但单测 / 工具命令
// 可能只加载了部分 bootstrap，注册函数必须能承受空句柄。
func TestRegisterOrderCronJobsWithoutCron(t *testing.T) {
	// global.App.Cron 默认为 nil
	RegisterOrderCronJobs()
}

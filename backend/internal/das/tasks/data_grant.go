/*
@Desc : 细粒度数据授权的后台维护任务。

授权记录带「失效时间」，到期后必须在状态上真实落地（status=expired），
否则列表与统计口径会显示成"还在生效"，而判定侧虽然按时间窗口过滤，
两边口径不一致会让运维误判。判定侧（data_grant.go）负责实时拦截，
本任务负责把过期状态收敛，两者口径必须一致。
*/

package tasks

import (
	"time"

	"github.com/simajiong/jaysql/internal/das/services"
	"github.com/simajiong/jaysql/internal/global"
)

// RegisterDataGrantCronJobs 注册数据授权相关定时任务（由 bootstrap.InitializeCron 调用）
func RegisterDataGrantCronJobs() {
	if global.App.Cron == nil {
		return
	}
	// 每 10 分钟收敛一次过期授权：周期短则无意义（判定侧本来就是实时过滤），
	// 周期长会让列表展示滞后。
	if _, err := global.App.Cron.AddFunc("@every 10m", func() {
		count, err := services.ExpireStaleGrants()
		if err != nil {
			global.App.Log.Warnf("expire stale data grants failed: %v", err)
		} else if count > 0 {
			global.App.Log.Infof("expired %d stale data grants at %s", count, time.Now().Format("2006-01-02 15:04:05"))
		}
		// 申请单同步收敛：授权过期后申请单不能还挂着「已通过」，
		// 两个口径错开会出现「页面说批了、实际没权限」的误判。
		reqCount, err := services.ExpireStaleAccessRequests()
		if err != nil {
			global.App.Log.Warnf("expire stale access requests failed: %v", err)
			return
		}
		if reqCount > 0 {
			global.App.Log.Infof("expired %d stale access requests at %s", reqCount, time.Now().Format("2006-01-02 15:04:05"))
		}
	}); err != nil {
		global.App.Log.Warnf("register data grant expire cron failed: %v", err)
	}
}

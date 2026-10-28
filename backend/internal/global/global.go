package global

import (
	config "github.com/simajiong/jaysql/internal/config"
	"github.com/simajiong/jaysql/pkg/eventbus"
	"github.com/simajiong/jaysql/pkg/lock"

	jwt "github.com/appleboy/gin-jwt/v2"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gorm.io/gorm"
)

type Application struct {
	ConfigViper *viper.Viper
	Config      config.Configuration
	JWT         *jwt.GinJWTMiddleware
	Log         *logrus.Logger
	DB          *gorm.DB
	// Bus 是进程内的发布/订阅总线，承载工单执行日志、processlist、gh-ost 进度的实时推送。
	// 原先这些消息经由 Redis Pub/Sub，为去掉这个外部依赖改为进程内广播；
	// 单实例部署下语义等价，多实例部署下消息不会跨进程（见 pkg/eventbus 包注释）。
	Bus *eventbus.Bus
	// Locks 是进程内的带租约互斥锁，用于工单执行的并发保护（防止手动执行与定时执行撞车）。
	// 相比原先的 Redis SetNX 多了租约兜底：持有者异常退出时锁最多在 TTL 后自动失效，
	// 不会像以前那样让工单永久卡死。
	Locks *lock.Manager
	Cron  *cron.Cron
}

var App = new(Application)

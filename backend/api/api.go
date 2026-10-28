package api

import (
	"time"

	"github.com/simajiong/jaysql/middleware"

	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
)

type Option func(*gin.Engine)

var options = []Option{}

// 注册app的路由配置
func Include(opts ...Option) {
	options = append(options, opts...)
}

// 初始化
func Init() *gin.Engine {
	r := gin.New()

	// 使用CORS中间件
	// r.Use(cors.Default())
	r.Use(cors.New(cors.Config{
		AllowOrigins:  []string{"*"},
		AllowMethods:  []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Length", "Authorization", "Token", "X-Trace-Id", "x-requested-with", "Content-Type"},
		ExposeHeaders: []string{"Content-Length", "Access-Control-Allow-Origin", "Access-Control-Allow-Headers", "Content-Type"},
		//AllowCredentials: true,
		MaxAge: 24 * time.Hour,
	}))

	// 使用requestid
	r.Use(requestid.New())

	// panic 防护必须挂在路由注册之前：gin 的 Use 只对之后注册的路由生效，
	// app.go 里那个放在全部 setupXxx 之后的 Recovery 是死代码 —— 没有它，
	// 任何 handler 的 panic 都会穿透到 net/http 直接断连（前端只见"连接已断开"）。
	r.Use(gin.Recovery())

	// 初始化请求日志记录器
	requestLogger := middleware.InitLogger(time.Now().Format("2006-01-02") + "-request.log")
	r.Use(middleware.LoggerRequestToFile(requestLogger))

	for _, opt := range options {
		opt(r)
	}
	return r
}

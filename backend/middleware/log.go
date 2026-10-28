package middleware

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/simajiong/jaysql/internal/global"

	"github.com/gin-contrib/requestid"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

// InitLogger 初始化日志记录器，支持日志文件轮转
func InitLogger(logFileName string) *logrus.Logger {
	// 创建日志文件夹
	logFilePath := resolveLogDir(global.App.Config.Log.RootDir)
	if err := os.MkdirAll(logFilePath, 0777); err != nil {
		fmt.Printf("create log dir %s failed: %s\n", logFilePath, err)
	}

	// 实例化
	logger := logrus.New()

	// 使用lumberjack进行日志轮转
	logger.SetOutput(&lumberjack.Logger{
		Filename:   filepath.Join(logFilePath, logFileName),
		MaxSize:    100,  // 每个日志文件的最大尺寸（单位：MB）
		MaxBackups: 30,   // 保留的旧日志文件的最大数量
		MaxAge:     7,    // 保留旧日志文件的最大天数
		Compress:   true, // 是否压缩旧的日志文件
	})

	// 设置日志级别
	switch strings.ToLower(global.App.Config.Log.Level) {
	case "debug":
		logger.SetLevel(logrus.DebugLevel)
	case "info":
		logger.SetLevel(logrus.InfoLevel)
	case "warn":
		logger.SetLevel(logrus.WarnLevel)
	case "error":
		logger.SetLevel(logrus.ErrorLevel)
	default:
		logger.SetLevel(logrus.InfoLevel)
	}

	// 设置日志格式
	logger.Formatter = &logrus.JSONFormatter{
		TimestampFormat: "2006-01-02 15:04:05",
	}
	return logger
}

// resolveLogDir 把配置中的日志目录规范为绝对路径。
//
// 历史问题：配置里写相对路径（如 "./logs"）时，原始实现直接交给 lumberjack，
// 路径由**进程 CWD** 决定 —— 服务端被手工从 /root 拉起时日志就落到了 /root/logs，
// 重启方式一变日志目录就跟着漂，历史被劈成两半（2026-09-15 事故）。
//
// 现在的语义：相对路径以**配置文件所在目录**为基准解析，与启动目录无关；
// 配置文件路径也是相对时才退回进程 CWD。因此 `-config /opt/app/goinsight/config.yaml`
// 无论从哪个目录启动，"logs" 都稳定落在 /opt/app/goinsight/logs。
func resolveLogDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "logs"
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}

	base := ""
	if v := global.App.ConfigViper; v != nil {
		if used := strings.TrimSpace(v.ConfigFileUsed()); used != "" {
			if !filepath.IsAbs(used) {
				if abs, err := filepath.Abs(used); err == nil {
					used = abs
				}
			}
			base = filepath.Dir(used)
		}
	}
	if base == "" {
		if wd, err := os.Getwd(); err == nil {
			base = wd
		}
	}
	if base == "" {
		return filepath.Clean(dir)
	}

	joined := filepath.Join(base, dir)
	if abs, err := filepath.Abs(joined); err == nil {
		return abs
	}
	return joined
}

func LoggerRequestToFile(logger *logrus.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 开始时间
		startTime := time.Now()
		// 继续处理请求
		c.Next()
		// 结束时间
		endTime := time.Now()
		latencyTime := fmt.Sprintf("%dms", endTime.Sub(startTime).Milliseconds())

		safeURI := sanitizeRequestURI(c.Request.URL)
		safeHeaders := sanitizeRequestHeaders(c.Request.Header)

		//日志格式
		logger.WithFields(logrus.Fields{
			"status_code":       c.Writer.Status(),
			"latency_time":      latencyTime,
			"request_client_ip": c.ClientIP(),
			"request_method":    c.Request.Method,
			"request_uri":       safeURI,
			"request_ua":        c.Request.UserAgent(),
			"request_referer":   c.Request.Referer(),
			"request_id":        requestid.Get(c),
			"request_header":    safeHeaders,
		}).Info()
	}
}

var sensitiveHeaderKeys = map[string]struct{}{
	"authorization": {},
	"cookie":        {},
	"set-cookie":    {},
	"x-api-key":     {},
}

var sensitiveQueryKeys = map[string]struct{}{
	"token":        {},
	"password":     {},
	"old_password": {},
	"new_password": {},
}

func sanitizeRequestHeaders(headers http.Header) map[string][]string {
	if headers == nil {
		return map[string][]string{}
	}
	masked := make(map[string][]string, len(headers))
	for k, values := range headers {
		if _, ok := sensitiveHeaderKeys[strings.ToLower(k)]; ok {
			masked[k] = []string{"***"}
			continue
		}
		copied := make([]string, len(values))
		copy(copied, values)
		masked[k] = copied
	}
	return masked
}

func sanitizeRequestURI(u *url.URL) string {
	if u == nil {
		return ""
	}
	query := u.Query()
	for key := range query {
		if _, ok := sensitiveQueryKeys[strings.ToLower(key)]; ok {
			query.Set(key, "***")
		}
	}
	safeURL := *u
	safeURL.RawQuery = query.Encode()
	return safeURL.RequestURI()
}

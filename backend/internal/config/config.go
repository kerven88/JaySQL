package config

import "strings"

type App struct {
	ListenAddress string `mapstructure:"listen_address" json:"listen_address" yaml:"listen_address"`
	Environment   string `mapstructure:"environment" json:"environment" yaml:"environment"`
	SECRET_KEY    string `mapstructure:"secret_key" json:"secret_key" yaml:"secret_key"`
}

type Crontab struct {
	SyncDBMetas string `mapstructure:"sync_db_metas" json:"sync_db_metas" yaml:"sync_db_metas"`
	// 实例连通性探活。留空/不配一律回落代码默认值（见 common/services/instance_probe.go）：
	// tick @every 30s、异常实例 60s 复测、可用实例 600s 兜底。
	InstanceProbe              string `mapstructure:"instance_probe" json:"instance_probe" yaml:"instance_probe"`
	InstanceProbeErrorInterval int    `mapstructure:"instance_probe_error_interval" json:"instance_probe_error_interval" yaml:"instance_probe_error_interval"`
	InstanceProbeOKInterval    int    `mapstructure:"instance_probe_ok_interval" json:"instance_probe_ok_interval" yaml:"instance_probe_ok_interval"`
	InstanceProbeBatchSize     int    `mapstructure:"instance_probe_batch_size" json:"instance_probe_batch_size" yaml:"instance_probe_batch_size"`
}

type Log struct {
	Level   string `mapstructure:"level" json:"level" yaml:"level"`
	RootDir string `mapstructure:"root_dir" json:"root_dir" yaml:"root_dir"`
}

type Database struct {
	Driver          string `mapstructure:"driver" json:"driver" yaml:"driver"`
	Host            string `mapstructure:"host" json:"host" yaml:"host"`
	Port            int    `mapstructure:"port" json:"port" yaml:"port"`
	Database        string `mapstructure:"database" json:"database" yaml:"database"`
	UserName        string `mapstructure:"username" json:"username" yaml:"username"`
	Password        string `mapstructure:"password" json:"password" yaml:"password"`
	Charset         string `mapstructure:"charset" json:"charset" yaml:"charset"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns" json:"max_idle_conns" yaml:"max_idle_conns"`
	MaxOpenConns    int    `mapstructure:"max_open_conns" json:"max_open_conns" yaml:"max_open_conns"`
	ConnMaxIdleTime int    `mapstructure:"conn_max_idle_time" json:"conn_max_idle_time" yaml:"conn_max_idle_time"`
	ConnMaxLifetime int    `mapstructure:"conn_max_life_time" json:"conn_max_life_time" yaml:"conn_max_life_time"`
}

// Deprecated: Redis 已不再是 JaySQL 的依赖（2026-10 移除）。
// 工单日志推送改走进程内总线（pkg/eventbus），执行锁改走进程内带租约锁（pkg/lock）。
// 结构体保留仅为兼容旧配置文件：viper 读到多余的 redis 段会静默忽略，
// 已部署环境的 config.yaml 无需改动即可继续启动；新部署可直接删除该段。
type Redis struct {
	Host     string `mapstructure:"host" json:"host" yaml:"host"`
	Port     int    `mapstructure:"port" json:"port" yaml:"port"`
	DB       int    `mapstructure:"db" json:"db" yaml:"db"`
	Password string `mapstructure:"password" json:"password" yaml:"password"`
}

type Das struct {
	MaxExecutionTime  uint64 `mapstructure:"max_execution_time" json:"max_execution_time" yaml:"max_execution_time"`
	DefaultReturnRows uint64 `mapstructure:"default_return_rows" json:"default_return_rows" yaml:"default_return_rows"`
	MaxReturnRows     uint64 `mapstructure:"max_return_rows" json:"max_return_rows" yaml:"max_return_rows"`
}

type Ghost struct {
	Path string   `mapstructure:"path" json:"path" yaml:"path"`
	Args []string `mapstructure:"args" json:"args" yaml:"args"`
}

type Notify struct {
	NoticeURL string `mapstructure:"notice_url" json:"notice_url" yaml:"notice_url"`
	Wechat    struct {
		Enable  bool   `mapstructure:"enable" json:"enable" yaml:"enable"`
		Webhook string `mapstructure:"webhook" json:"webhook" yaml:"webhook"`
	}
	Mail struct {
		Enable   bool   `mapstructure:"enable" json:"enable" yaml:"enable"`
		Username string `mapstructure:"username" json:"username" yaml:"username"`
		Password string `mapstructure:"password" json:"password" yaml:"password"`
		Host     string `mapstructure:"host" json:"host" yaml:"host"`
		Port     int    `mapstructure:"port" json:"port" yaml:"port"`
	}
	DingTalk struct {
		Enable   bool   `mapstructure:"enable" json:"enable" yaml:"enable"`
		Webhook  string `mapstructure:"webhook" json:"webhook" yaml:"webhook"`
		Keywords string `mapstructure:"keywords" json:"keywords" yaml:"keywords"`
	}
}

// AIConfig 大模型能力接入的平台级配置。
//
// 注意：服务商凭证（API Key / 端点地址）存储在数据库 insight_llm_providers 表
// 并经过 AES-256-GCM 加密，**禁止写在本配置文件里**。本节点只保留开关与安全策略。
type AIConfig struct {
	Enabled              bool   `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	AllowInsecureHTTP    bool   `mapstructure:"allow_insecure_http" json:"allow_insecure_http" yaml:"allow_insecure_http"`
	FailoverEnabled      bool   `mapstructure:"failover_enabled" json:"failover_enabled" yaml:"failover_enabled"`
	DegradeToRuleEngine  bool   `mapstructure:"degrade_to_rule_engine" json:"degrade_to_rule_engine" yaml:"degrade_to_rule_engine"`
	HealthCheckInterval  string `mapstructure:"health_check_interval" json:"health_check_interval" yaml:"health_check_interval"`
	RequestTimeoutSecond int    `mapstructure:"request_timeout_seconds" json:"request_timeout_seconds" yaml:"request_timeout_seconds"`
}

// AISettings 返回 AI 模块的平台级配置，并对零值应用安全默认值。
//
// 老配置文件里没有 ai 节点时 viper 会填零值，其中 request_timeout_seconds=0
// 会让 HTTP 客户端立刻超时、health_check_interval="" 会让定时任务无法解析，
// 因此使用方一律通过本方法读取，不要直接读 c.AI。
func (c Configuration) AISettings() AIConfig {
	cfg := c.AI
	if cfg.RequestTimeoutSecond <= 0 {
		cfg.RequestTimeoutSecond = 60
	}
	if strings.TrimSpace(cfg.HealthCheckInterval) == "" {
		cfg.HealthCheckInterval = "*/5 * * * *"
	}
	return cfg
}

type Configuration struct {
	App      App      `mapstructure:"app" json:"app" yaml:"app"`
	Crontab  Crontab  `mapstructure:"crontab" json:"crontab" yaml:"crontab"`
	Log      Log      `mapstructure:"log" json:"log" yaml:"log"`
	Database Database `mapstructure:"database" json:"database" yaml:"database"`
	Redis    Redis    `mapstructure:"redis" json:"redis" yaml:"redis"`
	Das      Das      `mapstructure:"das" json:"das" yaml:"das"`
	Ghost    Ghost    `mapstructure:"ghost" json:"ghost" yaml:"ghost"`
	Notify   Notify   `mapstructure:"-" json:"notify" yaml:"-"`
	AI       AIConfig `mapstructure:"ai" json:"ai" yaml:"ai"`
}

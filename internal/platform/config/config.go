// Package config 从环境变量加载 Hify 全部配置（本地开发 .env、生产 Docker env）。
//
// .env 文件加载由 cmd/hify/main.go 用 godotenv 完成（开发便利）；本包只从进程环境变量读取 + 校验。
// godotenv 默认不覆盖已设环境变量 → Docker 注入的生产值优先于 .env。
// 必填项缺失即 panic（启动期 fail-fast，不让错误配置上线，CLAUDE.md §密钥 / §config）。
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config 是 Hify 全量配置，按子系统分组。组合根（internal/app）据此初始化 platform、注入业务模块。
type Config struct {
	Server   ServerCfg
	PG       PGCfg
	Redis    RedisCfg
	Auth     AuthCfg
	LLM      LLMCfg
	Provider ProviderCfg
	Budget   BudgetCfg
	Logging  LoggingCfg
}

// ServerCfg HTTP 服务监听。
type ServerCfg struct {
	Port string // 默认 8080
}

// PGCfg PostgreSQL 17 + pgvector 连接。
type PGCfg struct {
	DSN string // pgx 格式 DSN（gorm postgres 驱动底层为 pgx/v5）；走 Docker secrets，禁止入 Git
}

// RedisCfg Redis 7 连接。
type RedisCfg struct {
	Addr     string
	Password string
	DB       int
}

// AuthCfg 最简登录 / session。
type AuthCfg struct {
	SessionSecret string // session 签名密钥；必填，生产用随机长串
	CookieSecure  bool   // session cookie 加 Secure 属性；生产 HTTPS 下 true，本地 dev http 下 false
}

// LLMCfg 外部模型提供商 API Key（CLAUDE.md §密钥：禁止入 Git）。
// 注意：这是进程直连 LLM 用的 env key；DB 内 provider 的 API Key 加密主密钥见 ProviderCfg。
type LLMCfg struct {
	OpenAIKey     string
	ClaudeKey     string
	GeminiKey     string
	OllamaBaseURL string
}

// ProviderCfg provider 模块配置：DB 内 API Key 加密的主密钥（AES-256-GCM）。
type ProviderCfg struct {
	MasterKey []byte // PROVIDER_MASTER_KEY：base64 解码后须为 32 字节；缺失/非法启动即 panic
}

// BudgetCfg 预算护栏（CLAUDE.md §budget：每用户限流 + 每日预算熔断，fail-open + 80% 告警）。
type BudgetCfg struct {
	DailyBudgetUSDCents int64 // 每日预算，单位美分（避免浮点）；1000 = $10
	UserRPM             int   // 每用户每分钟请求上限
}

// LoggingCfg 结构化日志（CLAUDE.md §部署架构 logging：stdout + 文件落 logs 卷 + rotate）。
type LoggingCfg struct {
	Level  string // debug/info/warn/error（LOG_LEVEL）
	Format string // json / text（LOG_FORMAT）
	File   string // 日志文件路径，空 = 只 stdout（LOG_FILE）
	// ExecutionsRetentionDays executions 分区在线保留天数（EXECUTIONS_RETENTION_DAYS，默认 90；
	// 应用内后台任务每日维护：建当月/下月分区、删 分区end+保留期 早于 now 的旧分区）。
	// <=0 关闭维护（不建不删）；「永不删除」用超大值（如 36500）表达，不设第三态。
	ExecutionsRetentionDays int
}

// MustLoad 从环境变量加载配置；必填项缺失即 panic。
func MustLoad() *Config {
	cfg := &Config{
		Server: ServerCfg{
			Port: envStr("SERVER_PORT", "8080"),
		},
		PG: PGCfg{
			DSN: envStr("PG_DSN", ""),
		},
		Redis: RedisCfg{
			Addr:     envStr("REDIS_ADDR", "localhost:6379"),
			Password: envStr("REDIS_PASSWORD", ""),
			DB:       envInt("REDIS_DB", 0),
		},
		Auth: AuthCfg{
			SessionSecret: envStr("SESSION_SECRET", ""),
			CookieSecure:  envBool("AUTH_COOKIE_SECURE", false),
		},
		LLM: LLMCfg{
			OpenAIKey:     envStr("OPENAI_API_KEY", ""),
			ClaudeKey:     envStr("CLAUDE_API_KEY", ""),
			GeminiKey:     envStr("GEMINI_API_KEY", ""),
			OllamaBaseURL: envStr("OLLAMA_BASE_URL", "http://localhost:11434"),
		},
		Provider: ProviderCfg{
			MasterKey: envMasterKey(),
		},
		Budget: BudgetCfg{
			DailyBudgetUSDCents: envInt64("DAILY_BUDGET_USD_CENTS", 1000),
			UserRPM:             envInt("USER_RPM", 60),
		},
		Logging: LoggingCfg{
			Level:                   envStr("LOG_LEVEL", "info"),
			Format:                  envStr("LOG_FORMAT", "json"),
			File:                    envStr("LOG_FILE", "logs/hify.log"),
			ExecutionsRetentionDays: envInt("EXECUTIONS_RETENTION_DAYS", 90),
		},
	}
	cfg.mustValidate()
	return cfg
}

// mustValidate 校验必填项；缺失即 panic，错误消息列出全部缺漏，便于一次配齐。
func (c *Config) mustValidate() {
	var missing []string
	if c.PG.DSN == "" {
		missing = append(missing, "PG_DSN")
	}
	if c.Auth.SessionSecret == "" {
		missing = append(missing, "SESSION_SECRET")
	}
	if c.Provider.MasterKey == nil {
		missing = append(missing, "PROVIDER_MASTER_KEY（32 字节 base64，openssl rand -base64 32 生成）")
	}
	if len(missing) > 0 {
		panic(fmt.Sprintf("config: missing required env vars: %s", strings.Join(missing, ", ")))
	}
}

// envMasterKey 读并解码 PROVIDER_MASTER_KEY。未设置返回 nil（由 mustValidate 报缺失）；
// 设置但非法（非 base64，或解码后非 32 字节）直接 panic 并给生成命令提示——错误长度的主密钥
// 只会在运行期加解密处爆，不如启动期拦截。
func envMasterKey() []byte {
	v, ok := os.LookupEnv("PROVIDER_MASTER_KEY")
	if !ok || v == "" {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(raw) != 32 {
		panic("config: PROVIDER_MASTER_KEY 必须是 32 字节 AES-256 主密钥的 base64（openssl rand -base64 32 生成）")
	}
	return raw
}

// envStr 读字符串环境变量，缺失返回 defaultValue。
func envStr(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return defaultValue
}

// envInt 读 int 环境变量，缺失或非法返回 defaultValue（不 panic，让调用方默认值兜底）。
func envInt(key string, defaultValue int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultValue
}

// envInt64 读 int64 环境变量，缺失或非法返回 defaultValue。
func envInt64(key string, defaultValue int64) int64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return defaultValue
}

// envBool 读布尔环境变量（true/false/1/0），缺失或非法返回 defaultValue。
func envBool(key string, defaultValue bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return defaultValue
}

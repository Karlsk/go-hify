package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

// mustLoadOrPanic 跑 MustLoad，返回 (cfg, recover 值)；panic 时 cfg 为 nil。
func mustLoadOrPanic(t *testing.T) (cfg *Config, recovered any) {
	t.Helper()
	defer func() { recovered = recover() }()
	cfg = MustLoad()
	return
}

// setRequiredExcept 预置除 skip 外的全部必填 env（skip 为空串时全设齐）。
func setRequiredExcept(t *testing.T, skip string) {
	t.Helper()
	t.Setenv("PG_DSN", "host=localhost user=hify password=hify dbname=hify port=5432 sslmode=disable")
	t.Setenv("SESSION_SECRET", "test-secret")
	t.Setenv("PROVIDER_MASTER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if skip != "" {
		t.Setenv(skip, "")
	}
}

func TestMustLoad_OK(t *testing.T) {
	setRequiredExcept(t, "")
	cfg, recovered := mustLoadOrPanic(t)
	if recovered != nil {
		t.Fatalf("panic: %v", recovered)
	}
	if len(cfg.Provider.MasterKey) != 32 {
		t.Fatalf("MasterKey len = %d, want 32", len(cfg.Provider.MasterKey))
	}
}

// 可选数值 / 布尔 env：合法值生效，非法值回退默认（不 panic）。
func TestMustLoad_OptionalNumericAndBool(t *testing.T) {
	setRequiredExcept(t, "")
	t.Setenv("REDIS_DB", "7")                  // envInt 合法
	t.Setenv("USER_RPM", "not-a-number")       // envInt 非法 → 默认 60
	t.Setenv("DAILY_BUDGET_USD_CENTS", "2500") // envInt64 合法
	t.Setenv("AUTH_COOKIE_SECURE", "true")     // envBool 合法
	t.Setenv("EXECUTIONS_RETENTION_DAYS", "180")

	cfg, recovered := mustLoadOrPanic(t)
	if recovered != nil {
		t.Fatalf("panic: %v", recovered)
	}
	if cfg.Redis.DB != 7 {
		t.Errorf("Redis.DB = %d, want 7", cfg.Redis.DB)
	}
	if cfg.Budget.UserRPM != 60 {
		t.Errorf("UserRPM = %d, want 默认 60", cfg.Budget.UserRPM)
	}
	if cfg.Budget.DailyBudgetUSDCents != 2500 {
		t.Errorf("DailyBudgetUSDCents = %d, want 2500", cfg.Budget.DailyBudgetUSDCents)
	}
	if !cfg.Auth.CookieSecure {
		t.Error("CookieSecure = false, want true")
	}
	if cfg.Logging.ExecutionsRetentionDays != 180 {
		t.Errorf("ExecutionsRetentionDays = %d, want 180", cfg.Logging.ExecutionsRetentionDays)
	}
}

func TestMustLoad_ExecutionsRetentionDays(t *testing.T) {
	cases := []struct {
		name string
		env  string // 空串 = 不设
		want int
	}{
		{"未设走默认 90", "", 90},
		{"非法值回退默认 90", "not-a-number", 90},
		{"自定义 365", "365", 365},
		{"0 是合法值（关闭维护，语义必须透传）", "0", 0},
		{"负数同理透传（关闭）", "-1", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequiredExcept(t, "")
			if tc.env != "" {
				t.Setenv("EXECUTIONS_RETENTION_DAYS", tc.env)
			}
			cfg, recovered := mustLoadOrPanic(t)
			if recovered != nil {
				t.Fatalf("panic: %v", recovered)
			}
			if cfg.Logging.ExecutionsRetentionDays != tc.want {
				t.Errorf("ExecutionsRetentionDays = %d, want %d", cfg.Logging.ExecutionsRetentionDays, tc.want)
			}
		})
	}
}

func TestMustLoad_MissingPGDSN(t *testing.T) {
	setRequiredExcept(t, "PG_DSN")
	_, recovered := mustLoadOrPanic(t)
	if recovered == nil {
		t.Fatal("缺 PG_DSN 应 panic")
	}
	if msg, ok := recovered.(string); !ok || !strings.Contains(msg, "PG_DSN") {
		t.Fatalf("panic 消息应点名 PG_DSN，got %v", recovered)
	}
}

func TestMustLoad_MissingSessionSecret(t *testing.T) {
	setRequiredExcept(t, "SESSION_SECRET")
	_, recovered := mustLoadOrPanic(t)
	if recovered == nil {
		t.Fatal("缺 SESSION_SECRET 应 panic")
	}
	if msg, ok := recovered.(string); !ok || !strings.Contains(msg, "SESSION_SECRET") {
		t.Fatalf("panic 消息应点名 SESSION_SECRET，got %v", recovered)
	}
}

func TestMustLoad_MasterKeyMissing(t *testing.T) {
	setRequiredExcept(t, "PROVIDER_MASTER_KEY")
	_, recovered := mustLoadOrPanic(t)
	if recovered == nil {
		t.Fatal("缺 PROVIDER_MASTER_KEY 应 panic")
	}
	if msg, ok := recovered.(string); !ok || !strings.Contains(msg, "PROVIDER_MASTER_KEY") {
		t.Fatalf("panic 消息应点名 PROVIDER_MASTER_KEY，got %v", recovered)
	}
}

func TestMustLoad_MasterKeyMalformed(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"非 base64", "!!!not-base64!!!"},
		{"长度不足（16B）", base64.StdEncoding.EncodeToString(make([]byte, 16))},
		{"长度超出（64B）", base64.StdEncoding.EncodeToString(make([]byte, 64))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setRequiredExcept(t, "")
			t.Setenv("PROVIDER_MASTER_KEY", c.value)
			if _, recovered := mustLoadOrPanic(t); recovered == nil {
				t.Fatalf("%s 应 panic", c.name)
			}
		})
	}
}

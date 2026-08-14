package redisx

import "testing"

// key.go 的 Key 拼接测试：前缀、多段、单段、空段。

func TestKey(t *testing.T) {
	cases := []struct {
		name  string
		parts []string
		want  string
	}{
		{"no parts → prefix only", nil, "hify"},
		{"empty slice", []string{}, "hify"},
		{"single part", []string{"session"}, "hify:session"},
		{"two parts (session)", []string{"session", "abc123"}, "hify:session:abc123"},
		{"three parts (cache)", []string{"cache", "provider-cache", "42"}, "hify:cache:provider-cache:42"},
		{"empty segment preserved", []string{"a", "", "b"}, "hify:a::b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Key(tc.parts...); got != tc.want {
				t.Fatalf("Key(%v) = %q, want %q", tc.parts, got, tc.want)
			}
		})
	}
}

func TestKeyPrefixConstant(t *testing.T) {
	// 钉死前缀常量值——被外部 key 拼接依赖，不能漂移。
	if KeyPrefix != "hify" {
		t.Fatalf("KeyPrefix = %q, want \"hify\"", KeyPrefix)
	}
	// 前缀必须出现在任意 Key() 结果开头。
	if got := Key("x"); len(got) < len(KeyPrefix)+1 || got[:len(KeyPrefix)] != KeyPrefix {
		t.Fatalf("Key(\"x\") = %q, must start with %q:", got, KeyPrefix)
	}
}

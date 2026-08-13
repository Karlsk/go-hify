// Command hify 是 Hify 服务入口（极薄）：加载配置 → 调组合根 app.Run。
// 不含业务、不含装配细节——装配全在 internal/app/server.go（CLAUDE.md §组合根）。
package main

import (
	"log"
	"os"

	"github.com/joho/godotenv"

	"github.com/Karlsk/go-hify/internal/app"
	"github.com/Karlsk/go-hify/internal/platform/config"
)

func main() {
	// 本地开发：从 .env 加载环境变量；生产走 Docker env，文件不存在时忽略。
	// godotenv 默认不覆盖已设环境变量 → Docker 注入值优先于 .env。
	_ = godotenv.Load()

	// migrate 子命令（./hify migrate up|down|status）：表结构演进，goose，不启动服务。
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := app.RunMigrate(os.Args[2:]); err != nil {
			log.Fatalf("migrate: %v", err)
		}
		return
	}

	cfg := config.MustLoad()

	if err := app.Run(cfg); err != nil {
		log.Fatalf("hify exited: %v", err)
	}
}

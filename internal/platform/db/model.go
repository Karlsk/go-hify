package db

import (
	"time"

	"gorm.io/gorm"
)

// 本文件定义三套基础表头 mixin。各业务模块的 model（service/model.go，模块私有）按表性质
// embed 对应 mixin，获得统一的 id / created_at（/ updated_at / deleted_at）字段与 GORM 行为，
// 不必每张表重复声明——CLAUDE.md《SQL 通用字段约定》标准表头的 Go 侧等价物。
//
// 约定（CLAUDE.md《模块内部结构》）：model 是 GORM 实体、模块私有、禁止跨模块；
// 「json 序列化是 schema 的事，model 不打 json tag」。故本文件 mixin 只带 gorm tag，
// 不带 json tag——ID 的 `"id,string"` 字符串化精度保护在各模块 api/schema 上做。
//
// 注意：表的真实 DDL（含 id bigint GENERATED ALWAYS AS IDENTITY、timestamptz、partial 索引）
// 由 migrations/ 的 SQL 决定，GORM tag 仅作运行时映射 + 文档（本包从不 AutoMigrate）。

// BaseAppendOnly 是 append-only 表（executions / messages / chunks）的标准表头：
// 只有 created_at，没有 updated_at、deleted_at（CLAUDE.md《updated_at 归属》）。
//
// GORM 对名为 CreatedAt 的字段在 INSERT 时自动填充创建时间（内建约定）；
// `default:now()` 是裸 SQL 插入路径的兜底（与 migrations 的 DEFAULT now() 对齐）。
type BaseAppendOnly struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"`
	CreatedAt time.Time `gorm:"type:timestamptz;not null;default:now()"`
}

// BaseMutable 是可变表（providers / agents / mcp_servers / knowledge_bases /
// workflows / conversations / documents）的标准表头：在 append-only 基础上加 updated_at。
// GORM autoUpdateTime 在每次 UPDATE 时维护 updated_at（CLAUDE.md：「GORM autoUpdateTime 维护」）。
type BaseMutable struct {
	BaseAppendOnly
	UpdatedAt time.Time `gorm:"type:timestamptz;not null;autoUpdateTime"`
}

// BaseSoftDelete 是软删除表（仅 agents / documents）的标准表头。
// gorm.DeletedAt 字段让 GORM 自动：查询加 WHERE deleted_at IS NULL、DELETE 改写为 UPDATE。
// CLAUDE.md §软删除：「只给业务需要的表（agents / documents），不全局加」——
// 需要软删除的 model embed 本结构，不需要的不 embed。
//
// partial 索引（WHERE deleted_at IS NULL）由 migrations/ 建，GORM tag 表达不了 partial。
type BaseSoftDelete struct {
	BaseMutable
	DeletedAt gorm.DeletedAt `gorm:"type:timestamptz"`
}

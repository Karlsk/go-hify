package service

import (
	"github.com/Karlsk/go-hify/internal/platform/db"
)

// DemoItem demo 条目（GORM 实体，模块私有）；表结构由 migrations/00008_demo_items.sql 定义。
// 嵌入 db.BaseMutable（= 「继承基类」的 Go 映射：组合获得 id / created_at / updated_at）。
type DemoItem struct {
	db.BaseMutable
	Name   string `gorm:"not null"`
	Status string `gorm:"not null;default:'draft'"`
}

func (DemoItem) TableName() string { return "demo_items" }

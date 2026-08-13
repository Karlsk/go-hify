package service

import (
	"github.com/Karlsk/go-hify/internal/platform/db"
)

// User 登录账号（GORM 实体，模块私有）；表结构由 migrations/00001_auth.sql 定义。
type User struct {
	db.BaseMutable
	Username     string `gorm:"size:128;uniqueIndex:uq_users_username;not null"`
	PasswordHash string `gorm:"not null"`
}

func (User) TableName() string { return "users" }

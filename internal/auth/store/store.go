package store

import (
	"context"

	"gorm.io/gorm"

	authsvc "github.com/Karlsk/go-hify/internal/auth/service"
)

var _ authsvc.Store = (*Store)(nil) // 编译期断言：Store 实现了 service.Store

// Store 实现 authsvc.Store（GORM CRUD）；error 原样上抛，业务翻译在 service 层。
type Store struct{ db *gorm.DB }

// New 创建 Store。
func New(db *gorm.DB) *Store { return &Store{db: db} }

// Create 插入新用户。
func (s *Store) Create(ctx context.Context, u *authsvc.User) error {
	return s.db.WithContext(ctx).Create(u).Error
}

// GetByUsername 按用户名查询；未找到返回 gorm.ErrRecordNotFound。
func (s *Store) GetByUsername(ctx context.Context, username string) (*authsvc.User, error) {
	var u authsvc.User
	err := s.db.WithContext(ctx).Where("username = ?", username).First(&u).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByID 按 ID 查询；未找到返回 gorm.ErrRecordNotFound。
func (s *Store) GetByID(ctx context.Context, id uint64) (*authsvc.User, error) {
	var u authsvc.User
	err := s.db.WithContext(ctx).First(&u, id).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

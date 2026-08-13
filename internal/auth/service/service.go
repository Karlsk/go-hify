package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	authapi "github.com/Karlsk/go-hify/internal/auth/api"
	"github.com/Karlsk/go-hify/internal/platform/authctx"
	"github.com/Karlsk/go-hify/internal/platform/redisx"
)

// Store 数据层接口：定义在消费方（本包），store 包实现，组合根注入——依赖倒置。
type Store interface {
	Create(ctx context.Context, u *User) error
	GetByUsername(ctx context.Context, username string) (*User, error)
	GetByID(ctx context.Context, id uint64) (*User, error)
}

type authService struct {
	store Store
	rdb   *redis.Client
}

// New 返回 api 接口类型：组合根拿到后可直接注入任何消费方。
func New(store Store, rdb *redis.Client) authapi.AuthService {
	return &authService{store: store, rdb: rdb}
}

// sessionKey session 在 Redis 的键。
func sessionKey(token string) string { return "session:" + token }

// Register 创建新用户（用户名唯一，密码 bcrypt 哈希）。
func (s *authService) Register(ctx context.Context, req authapi.RegisterReq) (*authapi.UserSchema, error) {
	if _, err := s.store.GetByUsername(ctx, req.Username); err == nil {
		return nil, authapi.ErrUsernameConflict
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("check username %s: %w", req.Username, err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	u := &User{Username: req.Username, PasswordHash: string(hash)}
	if err := s.store.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return toSchema(u), nil
}

// Login 校验凭据，创建 session（Redis 7 天），返回 token。
// 用户不存在与密码错误返回同一个哨兵——不泄露用户名是否存在。
func (s *authService) Login(ctx context.Context, req authapi.LoginReq) (*authapi.SessionSchema, error) {
	u, err := s.store.GetByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, authapi.ErrUnauthorized
		}
		return nil, fmt.Errorf("get user %s: %w", req.Username, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		return nil, authapi.ErrUnauthorized
	}

	token, err := newSessionToken()
	if err != nil {
		return nil, fmt.Errorf("generate session token: %w", err)
	}
	if err := redisx.SetStruct(ctx, s.rdb, sessionKey(token), u, authapi.SessionTTL); err != nil {
		return nil, fmt.Errorf("store session: %w", err)
	}
	return &authapi.SessionSchema{Token: token, User: *toSchema(u)}, nil
}

// Logout 删除 session（幂等：session 不存在视为已登出）。
func (s *authService) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if err := redisx.Delete(ctx, s.rdb, sessionKey(token)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// Validate 校验 session token，返回登录用户；session 失效返回 ErrSessionExpired。
func (s *authService) Validate(ctx context.Context, token string) (*authapi.UserSchema, error) {
	if token == "" {
		return nil, authapi.ErrSessionExpired
	}
	var u User
	ok, err := redisx.GetStruct(ctx, s.rdb, sessionKey(token), &u)
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	if !ok {
		return nil, authapi.ErrSessionExpired
	}
	return toSchema(&u), nil
}

// Me 返回 ctx 内的当前登录用户（中间件注入）；无身份返回 ErrUnauthorized。
// 走 DB 取完整信息（ctx 身份是最小集，无 created_at 等字段）。
func (s *authService) Me(ctx context.Context) (*authapi.UserSchema, error) {
	u, ok := authctx.UserFrom(ctx)
	if !ok {
		return nil, authapi.ErrUnauthorized
	}
	me, err := s.store.GetByID(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("get user %d: %w", u.ID, err)
	}
	return toSchema(me), nil
}

// newSessionToken 生成 256 bit 随机 session token（hex）。
func newSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// toSchema model → schema 转换（边界处）。
func toSchema(u *User) *authapi.UserSchema {
	return &authapi.UserSchema{
		ID:        strconv.FormatUint(u.ID, 10),
		Username:  u.Username,
		CreatedAt: u.CreatedAt,
	}
}

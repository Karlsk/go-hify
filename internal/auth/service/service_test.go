package service

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	authapi "github.com/Karlsk/go-hify/internal/auth/api"
	"github.com/Karlsk/go-hify/internal/platform/authctx"
)

// stubStore 内存版 Store：只覆写用得到的方法（Go 小接口惯例）。
type stubStore struct {
	users map[string]*User // key: username
	byID  map[uint64]*User
	next  uint64
}

func newStubStore() *stubStore {
	return &stubStore{users: map[string]*User{}, byID: map[uint64]*User{}, next: 1}
}

func (s *stubStore) Create(_ context.Context, u *User) error {
	u.ID = s.next
	s.next++
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now() // 模拟 DB 的 now() 默认值
	}
	cp := *u
	s.users[u.Username] = &cp
	s.byID[u.ID] = &cp
	return nil
}

func (s *stubStore) GetByUsername(_ context.Context, username string) (*User, error) {
	u, ok := s.users[username]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return u, nil
}

func (s *stubStore) GetByID(_ context.Context, id uint64) (*User, error) {
	u, ok := s.byID[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return u, nil
}

func newTestSvc(t *testing.T, st *stubStore) authapi.AuthService {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { rdb.Close() })
	// 同包直构注入 MinCost（New 默认 DefaultCost 不变）：哈希/比对从 ~100ms 降到
	// ~0 量级，测试断言的是哈希存在与可校验性，不是 cost 强度。
	return &authService{store: st, rdb: rdb, hashCost: bcrypt.MinCost}
}

// withTestUser 注入测试用户身份（模拟中间件）。
func withTestUser(ctx context.Context, id uint64, username string) context.Context {
	return authctx.WithUser(ctx, &authctx.User{ID: id, Username: username})
}

func TestRegisterHashesPassword(t *testing.T) {
	st := newStubStore()
	svc := newTestSvc(t, st)
	u, err := svc.Register(context.Background(), authapi.RegisterReq{Username: "alice", Password: "password123"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if u.Username != "alice" || u.ID == "" {
		t.Fatalf("schema = %+v", u)
	}
}

func TestRegisterPasswordStoredAsHash(t *testing.T) {
	st := newStubStore()
	svc := newTestSvc(t, st)
	if _, err := svc.Register(context.Background(), authapi.RegisterReq{Username: "alice", Password: "password123"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	stored, err := st.GetByUsername(context.Background(), "alice")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.PasswordHash == "password123" {
		t.Fatal("password must be hashed, not plaintext")
	}
	if bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte("password123")) != nil {
		t.Fatal("stored hash does not match password")
	}
}

func TestRegisterUsernameConflict(t *testing.T) {
	svc := newTestSvc(t, newStubStore())
	req := authapi.RegisterReq{Username: "alice", Password: "password123"}
	if _, err := svc.Register(context.Background(), req); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if _, err := svc.Register(context.Background(), req); !errors.Is(err, authapi.ErrUsernameConflict) {
		t.Fatalf("second register err = %v, want ErrUsernameConflict", err)
	}
}

func TestLoginSuccessCreatesSession(t *testing.T) {
	st := newStubStore()
	svc := newTestSvc(t, st)
	if _, err := svc.Register(context.Background(), authapi.RegisterReq{Username: "alice", Password: "password123"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	sess, err := svc.Login(context.Background(), authapi.LoginReq{Username: "alice", Password: "password123"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if sess.Token == "" {
		t.Fatal("token empty")
	}
	// session 已入 Redis，Validate 可读
	u, err := svc.Validate(context.Background(), sess.Token)
	if err != nil || u.Username != "alice" {
		t.Fatalf("validate = (%v, %v)", u, err)
	}
}

func TestLoginWrongPassword(t *testing.T) {
	st := newStubStore()
	svc := newTestSvc(t, st)
	if _, err := svc.Register(context.Background(), authapi.RegisterReq{Username: "alice", Password: "password123"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := svc.Login(context.Background(), authapi.LoginReq{Username: "alice", Password: "wrongpass1"}); !errors.Is(err, authapi.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestLoginUnknownUserSameError(t *testing.T) {
	svc := newTestSvc(t, newStubStore())
	// 用户不存在与密码错误必须同错误——不泄露用户名是否存在
	if _, err := svc.Login(context.Background(), authapi.LoginReq{Username: "ghost", Password: "password123"}); !errors.Is(err, authapi.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestLogoutIdempotent(t *testing.T) {
	st := newStubStore()
	svc := newTestSvc(t, st)
	if _, err := svc.Register(context.Background(), authapi.RegisterReq{Username: "alice", Password: "password123"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	sess, err := svc.Login(context.Background(), authapi.LoginReq{Username: "alice", Password: "password123"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if err := svc.Logout(context.Background(), sess.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if _, err := svc.Validate(context.Background(), sess.Token); !errors.Is(err, authapi.ErrSessionExpired) {
		t.Fatalf("validate after logout err = %v, want ErrSessionExpired", err)
	}
	// 二次登出 / 空 token 不报错
	if err := svc.Logout(context.Background(), sess.Token); err != nil {
		t.Fatalf("second logout: %v", err)
	}
	if err := svc.Logout(context.Background(), ""); err != nil {
		t.Fatalf("empty logout: %v", err)
	}
}

func TestValidateMissingSession(t *testing.T) {
	svc := newTestSvc(t, newStubStore())
	if _, err := svc.Validate(context.Background(), ""); !errors.Is(err, authapi.ErrSessionExpired) {
		t.Fatalf("empty token err = %v, want ErrSessionExpired", err)
	}
	if _, err := svc.Validate(context.Background(), "nonexistent"); !errors.Is(err, authapi.ErrSessionExpired) {
		t.Fatalf("unknown token err = %v, want ErrSessionExpired", err)
	}
}

func TestMeFromContext(t *testing.T) {
	st := newStubStore()
	svc := newTestSvc(t, st)
	reg, err := svc.Register(context.Background(), authapi.RegisterReq{Username: "alice", Password: "password123"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	id, err := strconv.ParseUint(reg.ID, 10, 64)
	if err != nil {
		t.Fatalf("parse id: %v", err)
	}
	ctx := withTestUser(context.Background(), id, "alice")
	u, err := svc.Me(ctx)
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if u.ID != reg.ID || u.Username != "alice" {
		t.Fatalf("me = %+v", u)
	}
	if u.CreatedAt.IsZero() {
		t.Fatal("me must return full user info incl. created_at")
	}
}

func TestMeWithoutIdentity(t *testing.T) {
	svc := newTestSvc(t, newStubStore())
	if _, err := svc.Me(context.Background()); !errors.Is(err, authapi.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestNewSessionTokenUnique(t *testing.T) {
	a, err := newSessionToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	b, err := newSessionToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if a == b {
		t.Fatal("tokens must be unique")
	}
	if len(a) != 64 {
		t.Fatalf("token len = %d, want 64 (32 bytes hex)", len(a))
	}
}

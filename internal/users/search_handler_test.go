package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"github.com/google/uuid"
)

type searchTestMockRepo struct {
	users map[uuid.UUID]*User
}

func newSearchTestMockRepo() *searchTestMockRepo {
	return &searchTestMockRepo{users: make(map[uuid.UUID]*User)}
}

func (m *searchTestMockRepo) Create(ctx context.Context, input CreateUserInput) (*User, error) {
	return nil, nil
}
func (m *searchTestMockRepo) GetUserByEmail(ctx context.Context, email string) (*User, string, error) {
	return nil, "", nil
}
func (m *searchTestMockRepo) GetUserByID(ctx context.Context, id uuid.UUID) (*User, string, error) {
	return nil, "", nil
}
func (m *searchTestMockRepo) UpdateUserPassword(ctx context.Context, payload UpdateUserPassword) (*User, error) {
	return nil, nil
}
func (m *searchTestMockRepo) GetAllUsers(ctx context.Context) (*[]any, error)          { return nil, nil }
func (m *searchTestMockRepo) GetAllUsersRs(ctx context.Context) (*[]any, error)        { return nil, nil }
func (m *searchTestMockRepo) SendFriendRequest(fromID, toID, friReqID uuid.UUID) error { return nil }
func (m *searchTestMockRepo) GetMyFriReqList(ctx context.Context, userID uuid.UUID) (*[]any, error) {
	return nil, nil
}
func (m *searchTestMockRepo) GetMySendFirReqList(ctx context.Context, userID uuid.UUID) (*[]any, error) {
	return nil, nil
}
func (m *searchTestMockRepo) UpdateFriReq(reqID uuid.UUID) error { return nil }
func (m *searchTestMockRepo) GetUserFriListByID(ctx context.Context, userID uuid.UUID) (*[]any, error) {
	return nil, nil
}
func (m *searchTestMockRepo) CancelFriReq(reqID uuid.UUID, updateTime time.Time) error { return nil }
func (m *searchTestMockRepo) DeleteFriReq(reqID uuid.UUID) error                       { return nil }
func (m *searchTestMockRepo) GetOtherUserIDByReqID(ctx context.Context, userID uuid.UUID, reqID uuid.UUID) (*User, error) {
	return nil, nil
}
func (m *searchTestMockRepo) GetMatchName(ctx context.Context, searchName string) (*[]User, error) {
	var result []User
	for _, u := range m.users {
		if len(searchName) > 0 && len(u.Name) >= len(searchName) && u.Name[:len(searchName)] == searchName {
			result = append(result, *u)
		}
	}
	if len(result) == 0 {
		return nil, sql.ErrNoRows
	}
	return &result, nil
}

type searchTestMockCache struct{}

func (m *searchTestMockCache) Load()                                                    {}
func (m *searchTestMockCache) UpdateUserRs(payload interface{})                         {}
func (m *searchTestMockCache) CleanUpUserRs(payload *CacheRsDeleteStruct)               {}
func (m *searchTestMockCache) GetUserFriList(userID uuid.UUID) *[]uuid.UUID             { return nil }
func (m *searchTestMockCache) GetUserRs(userID uuid.UUID) bool                          { return false }
func (m *searchTestMockCache) GetUserReqList(userID uuid.UUID) *map[uuid.UUID]uuid.UUID { return nil }
func (m *searchTestMockCache) GetUserSendReqList(userID uuid.UUID) *map[uuid.UUID]uuid.UUID {
	return nil
}
func (m *searchTestMockCache) GetOtherUserIDByReqID(userID, reqID uuid.UUID, label string) *FriendMetaData {
	return nil
}
func (m *searchTestMockCache) UpdateUserCache(user *User)              {}
func (m *searchTestMockCache) GetUserNameByID(userID uuid.UUID) string { return "" }

type searchTestMockMQ struct{}

func (m *searchTestMockMQ) PublishWithContext(ctx context.Context, topic string, job interface{}) error {
	return nil
}
func (m *searchTestMockMQ) Run() {}
func (m *searchTestMockMQ) ListeningForTheChannels(topic string, bufferSize int, worker func(chan *mq.Channel)) {
}
func (m *searchTestMockMQ) Republish(msg *mq.Channel, retries int) {}

type searchTestUserService struct {
	userRepo  *searchTestMockRepo
	userCache *searchTestMockCache
	mainMq    *searchTestMockMQ
	logger    *slog.Logger
}

func (s *searchTestUserService) Register(ctx context.Context, name, email, password string) (*User, error) {
	return nil, nil
}
func (s *searchTestUserService) UpdatePassword(ctx context.Context, userID uuid.UUID, oldPass, newPass string) (*User, error) {
	return nil, nil
}
func (s *searchTestUserService) AddFriendSend(ctx context.Context, sendID, recieverID uuid.UUID, label string) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (s *searchTestUserService) ConfirmFriendReq(ctx context.Context, fromID, reqID uuid.UUID, status string) error {
	return nil
}
func (s *searchTestUserService) CancelFriReq(ctx context.Context, userID, reqID uuid.UUID) error {
	return nil
}
func (s *searchTestUserService) DeleteFriReq(ctx context.Context, userID, reqID uuid.UUID) error {
	return nil
}
func (s *searchTestUserService) GetPendingList(ctx context.Context, userID uuid.UUID) (*GetReqList, error) {
	return nil, nil
}
func (s *searchTestUserService) GetFriendList(ctx context.Context, userID uuid.UUID) (*[]FriendMetaData, error) {
	return nil, nil
}
func (s *searchTestUserService) SearchUser(ctx context.Context, searchName string) (*[]User, error) {
	return s.userRepo.GetMatchName(ctx, searchName)
}
func (s *searchTestUserService) StartWorkerForAddFri(channel chan *mq.Channel)          {}
func (s *searchTestUserService) StartWorkerForConfirmFri(channel chan *mq.Channel)      {}
func (s *searchTestUserService) StartWorkerForDeleteReq(channel chan *mq.Channel)       {}
func (s *searchTestUserService) StartWorkerForCancelReq(channel chan *mq.Channel)       {}
func (s *searchTestUserService) StartWorkerForUpdateUserCache(channel chan *mq.Channel) {}

type searchPayload struct {
	userContext uuid.UUID
	reqContext  uuid.UUID
}

func TestSearchUserHandler_Success(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	mockRepo := newSearchTestMockRepo()
	testUser1 := &User{ID: uuid.New(), Name: "alice", Email: "alice@example.com"}
	testUser2 := &User{ID: uuid.New(), Name: "alex", Email: "alex@example.com"}
	mockRepo.users[testUser1.ID] = testUser1
	mockRepo.users[testUser2.ID] = testUser2

	service := &searchTestUserService{
		userRepo:  mockRepo,
		userCache: &searchTestMockCache{},
		mainMq:    &searchTestMockMQ{},
		logger:    logger,
	}

	handler := &UserHandler{userService: service}

	userID := testUser1.ID
	payloadCtx := searchPayload{userContext: userID, reqContext: uuid.New()}
	userCtx := context.WithValue(context.Background(), middleware.PAYLOADCONTEXT, payloadCtx)

	req := httptest.NewRequest(http.MethodGet, "/users/search?q=ali", nil).WithContext(userCtx)
	w := httptest.NewRecorder()

	handler.SearchUser(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d. Body: %s", w.Code, w.Body.String())
	}

	var resp FoundUserListRes
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp.UserList) != 1 {
		t.Errorf("expected 1 user, got %d", len(resp.UserList))
	}
}

func TestSearchUserHandler_NoQuery(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	mockRepo := newSearchTestMockRepo()

	service := &searchTestUserService{
		userRepo:  mockRepo,
		userCache: &searchTestMockCache{},
		mainMq:    &searchTestMockMQ{},
		logger:    logger,
	}

	handler := &UserHandler{userService: service}

	testUserID := uuid.New()
	payloadCtx := searchPayload{userContext: testUserID, reqContext: uuid.New()}
	userCtx := context.WithValue(context.Background(), middleware.PAYLOADCONTEXT, payloadCtx)

	req := httptest.NewRequest(http.MethodGet, "/users/search", nil).WithContext(userCtx)
	w := httptest.NewRecorder()

	handler.SearchUser(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestSearchUserHandler_NotFound(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	mockRepo := newSearchTestMockRepo()

	service := &searchTestUserService{
		userRepo:  mockRepo,
		userCache: &searchTestMockCache{},
		mainMq:    &searchTestMockMQ{},
		logger:    logger,
	}

	handler := &UserHandler{userService: service}

	testUserID := uuid.New()
	payloadCtx := searchPayload{userContext: testUserID, reqContext: uuid.New()}
	userCtx := context.WithValue(context.Background(), middleware.PAYLOADCONTEXT, payloadCtx)

	req := httptest.NewRequest(http.MethodGet, "/users/search?q=nonexistent", nil).WithContext(userCtx)
	w := httptest.NewRecorder()

	handler.SearchUser(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}
}

func TestSearchUserHandler_NoAuth(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	mockRepo := newSearchTestMockRepo()

	service := &searchTestUserService{
		userRepo:  mockRepo,
		userCache: &searchTestMockCache{},
		mainMq:    &searchTestMockMQ{},
		logger:    logger,
	}

	handler := &UserHandler{userService: service}

	req := httptest.NewRequest(http.MethodGet, "/users/search?q=test", nil)
	w := httptest.NewRecorder()

	handler.SearchUser(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

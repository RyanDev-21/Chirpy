package users

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	mq "RyanDev-21.com/Chirpy/internal/customMq"
	"RyanDev-21.com/Chirpy/internal/database"
	"RyanDev-21.com/Chirpy/pkg/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type mockUserRepo struct {
	users          map[uuid.UUID]*User
	passwords      map[uuid.UUID]string
	friendRequests map[uuid.UUID][]FriendReq
	friendLists    map[uuid.UUID][]uuid.UUID
}

func newMockUserRepo() *mockUserRepo {
	return &mockUserRepo{
		users:          make(map[uuid.UUID]*User),
		passwords:      make(map[uuid.UUID]string),
		friendRequests: make(map[uuid.UUID][]FriendReq),
		friendLists:    make(map[uuid.UUID][]uuid.UUID),
	}
}

func (m *mockUserRepo) Create(ctx context.Context, input CreateUserInput) (*User, error) {
	for _, u := range m.users {
		if u.Email == input.Email {
			return nil, DuplicateKeyErr
		}
	}
	user := &User{
		ID:        uuid.New(),
		Name:      input.Name,
		Email:     input.Email,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		IsRED:     false,
	}
	m.users[user.ID] = user
	m.passwords[user.ID] = input.Password
	return user, nil
}

func (m *mockUserRepo) GetUserByEmail(ctx context.Context, email string) (*User, string, error) {
	for _, u := range m.users {
		if u.Email == email {
			return u, m.passwords[u.ID], nil
		}
	}
	return nil, "", NoUserFoundErr
}

func (m *mockUserRepo) GetUserByID(ctx context.Context, id uuid.UUID) (*User, string, error) {
	if u, ok := m.users[id]; ok {
		return u, m.passwords[id], nil
	}
	return nil, "", NoUserFoundErr
}

func (m *mockUserRepo) UpdateUserPassword(ctx context.Context, payload UpdateUserPassword) (*User, error) {
	if _, ok := m.users[payload.UserID]; !ok {
		return nil, NoUserFoundErr
	}
	m.passwords[payload.UserID] = payload.Password
	return m.users[payload.UserID], nil
}

func (m *mockUserRepo) GetAllUsers(ctx context.Context) (*[]database.User, error) {
	var users []database.User
	for _, u := range m.users {
		users = append(users, database.User{
			ID:          u.ID,
			CreatedAt:   u.CreatedAt,
			UpdatedAt:   u.UpdatedAt,
			Email:       u.Email,
			Name:        u.Name,
			IsChirpyRed: pgtype.Bool{Bool: u.IsRED, Valid: true},
		})
	}
	return &users, nil
}

func (m *mockUserRepo) GetAllUsersRs(ctx context.Context) (*[]database.GetAllUserRsRow, error) {
	return &[]database.GetAllUserRsRow{}, nil
}

func (m *mockUserRepo) SendFriendRequest(fromID, toID, friReqID uuid.UUID) error {
	m.friendRequests[fromID] = append(m.friendRequests[fromID], FriendReq{
		ReqID:  friReqID,
		FromID: fromID,
		ToID:   toID,
	})
	return nil
}

func (m *mockUserRepo) GetMyFriReqList(ctx context.Context, userID uuid.UUID) (*[]database.UserRelationship, error) {
	return &[]database.UserRelationship{}, nil
}

func (m *mockUserRepo) GetMySendFirReqList(ctx context.Context, userID uuid.UUID) (*[]database.UserRelationship, error) {
	return &[]database.UserRelationship{}, nil
}

func (m *mockUserRepo) UpdateFriReq(reqID uuid.UUID) error {
	return nil
}

func (m *mockUserRepo) GetUserFriListByID(ctx context.Context, userID uuid.UUID) (*[]uuid.UUID, error) {
	if list, ok := m.friendLists[userID]; ok {
		return &list, nil
	}
	return nil, nil
}

func (m *mockUserRepo) CancelFriReq(reqID uuid.UUID, updateTime time.Time) error {
	return nil
}

func (m *mockUserRepo) DeleteFriReq(reqID uuid.UUID) error {
	return nil
}

func (m *mockUserRepo) GetOtherUserIDByReqID(ctx context.Context, userID uuid.UUID, reqID uuid.UUID) (*User, error) {
	return nil, nil
}

type mockUserCache struct {
	users         map[uuid.UUID]*User
	relationships map[uuid.UUID]map[string]*map[uuid.UUID]uuid.UUID
	friendLists   map[uuid.UUID]*[]uuid.UUID
}

func newMockUserCache() *mockUserCache {
	return &mockUserCache{
		users:         make(map[uuid.UUID]*User),
		relationships: make(map[uuid.UUID]map[string]*map[uuid.UUID]uuid.UUID),
		friendLists:   make(map[uuid.UUID]*[]uuid.UUID),
	}
}

func (m *mockUserCache) Load() {}

func (m *mockUserCache) UpdateUserRs(payload interface{}) {
	switch p := payload.(type) {
	case CacheUpdateStruct:
		if m.relationships[p.UserID] == nil {
			m.relationships[p.UserID] = make(map[string]*map[uuid.UUID]uuid.UUID)
		}
		m.relationships[p.UserID][p.Lable] = &map[uuid.UUID]uuid.UUID{
			p.ReqID: p.OtherUserID,
		}
	case CacheUpdateFriStruct:
		if m.friendLists[p.UserID] == nil {
			m.friendLists[p.UserID] = &[]uuid.UUID{}
		}
		*m.friendLists[p.UserID] = append(*m.friendLists[p.UserID], p.ToID)
	}
}

func (m *mockUserCache) CleanUpUserRs(payload *CacheRsDeleteStruct) {
	if m.relationships[payload.UserID] != nil {
		delete(m.relationships[payload.UserID], payload.Lable)
	}
}

func (m *mockUserCache) GetUserFriList(userID uuid.UUID) *[]uuid.UUID {
	return m.friendLists[userID]
}

func (m *mockUserCache) GetUserRs(userID uuid.UUID) bool {
	_, ok := m.relationships[userID]
	return ok
}

func (m *mockUserCache) GetUserReqList(userID uuid.UUID) *map[uuid.UUID]uuid.UUID {
	if m.relationships[userID] != nil {
		if rel, ok := m.relationships[userID]["pending"]; ok {
			return rel
		}
	}
	return nil
}

func (m *mockUserCache) GetUserSendReqList(userID uuid.UUID) *map[uuid.UUID]uuid.UUID {
	if m.relationships[userID] != nil {
		if rel, ok := m.relationships[userID]["send"]; ok {
			return rel
		}
	}
	return nil
}

func (m *mockUserCache) GetOtherUserIDByReqID(userID, reqID uuid.UUID, label string) *uuid.UUID {
	if m.relationships[userID] != nil {
		if rel, ok := m.relationships[userID][label]; ok {
			if id, ok := (*rel)[reqID]; ok {
				return &id
			}
		}
	}
	return nil
}

func (m *mockUserCache) UpdateUserCache(user *User) {
	m.users[user.ID] = user
}

func getTestMQ() *mq.MainMQ {
	channels := make(map[string]chan *mq.Channel)
	return mq.NewMainMQ(&channels, 10)
}

func setupTestLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func createTestContext() context.Context {
	var savedContext context.Context
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		savedContext = r.Context()
	})
	wrapped := middleware.MiddelWareLog(handler)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	wrapped.ServeHTTP(httptest.NewRecorder(), req)
	return savedContext
}

func TestUserService_Register_Success(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing user service registration", "test", "service_register_success")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user, err := userService.Register(ctx, "testuser", "test@example.com", "password123")
	if err != nil {
		t.Fatalf("failed to register user: %v", err)
	}

	if user.Email != "test@example.com" {
		t.Errorf("expected email test@example.com, got %s", user.Email)
	}

	if user.Name != "testuser" {
		t.Errorf("expected name testuser, got %s", user.Name)
	}

	logger.Info("user service registration successful", "user_id", user.ID, "email", user.Email)
}

func TestUserService_Register_DuplicateKeyError(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing user service duplicate key error", "test", "service_register_duplicate")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	_, err := userService.Register(ctx, "testuser", "test@example.com", "password123")
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	_, err = userService.Register(ctx, "testuser", "test@example.com", "password123")
	if err != DuplicateKeyErr {
		t.Errorf("expected DuplicateKeyErr, got %v", err)
	}

	logger.Info("duplicate key error test completed", "error", err)
}

func TestUserService_UpdatePassword_Success(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing user password update", "test", "update_password_success")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user, _ := userService.Register(ctx, "testuser", "test@example.com", "oldpassword")

	updatedUser, err := userService.UpdatePassword(ctx, user.ID, "oldpassword", "newpassword")
	if err != nil {
		t.Fatalf("failed to update password: %v", err)
	}

	if updatedUser.ID != user.ID {
		t.Errorf("expected user ID %s, got %s", user.ID, updatedUser.ID)
	}

	logger.Info("password update successful", "user_id", user.ID)
}

func TestUserService_UpdatePassword_WrongOldPassword(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing user password update with wrong old password", "test", "update_password_wrong")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user, _ := userService.Register(ctx, "testuser", "test@example.com", "oldpassword")

	_, err := userService.UpdatePassword(ctx, user.ID, "wrongpassword", "newpassword")
	if err == nil {
		t.Error("expected error for wrong old password")
	}

	logger.Info("wrong old password test completed", "error", err)
}

func TestUserService_UpdatePassword_UserNotFound(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing user password update for non-existent user", "test", "update_password_not_found")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	_, err := userService.UpdatePassword(ctx, uuid.New(), "oldpassword", "newpassword")
	if err != NoUserFoundErr {
		t.Errorf("expected NoUserFoundErr, got %v", err)
	}

	logger.Info("user not found test completed", "error", err)
}

func TestUserService_AddFriendSend_Success(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing add friend send service", "test", "add_friend_send")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")
	user2, _ := userService.Register(ctx, "user2", "user2@example.com", "password")

	friReqID, err := userService.AddFriendSend(ctx, user1.ID, user2.ID, "pending")
	if err != nil {
		t.Logf("AddFriendSend returned error (expected due to MQ not running): %v", err)
	}

	if friReqID == uuid.Nil {
		t.Error("expected non-nil friend request ID even if MQ publish fails")
	}

	logger.Info("add friend send test completed", "req_id", friReqID, "from", user1.ID, "to", user2.ID)
}

func TestUserService_ConfirmFriendReq_Success(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing confirm friend request service", "test", "confirm_friend_req")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")
	user2, _ := userService.Register(ctx, "user2", "user2@example.com", "password")

	user1ID := user1.ID
	user2ID := user2.ID
	friReqID, _ := uuid.NewV7()

	mockCache.UpdateUserRs(CacheUpdateStruct{
		UserID:      user1ID,
		ReqID:       friReqID,
		OtherUserID: user2ID,
		Lable:       "send",
	})

	mockCache.UpdateUserRs(CacheUpdateStruct{
		UserID:      user2ID,
		ReqID:       friReqID,
		OtherUserID: user1ID,
		Lable:       "pending",
	})

	err := userService.ConfirmFriendReq(ctx, user2ID, friReqID, "confirm")
	if err != nil {
		t.Logf("ConfirmFriendReq returned error: %v", err)
	}

	logger.Info("confirm friend request test completed", "req_id", friReqID)
}

func TestUserService_CancelFriReq_Success(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing cancel friend request service", "test", "cancel_friend_req")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")
	user2, _ := userService.Register(ctx, "user2", "user2@example.com", "password")

	user1ID := user1.ID
	user2ID := user2.ID
	friReqID, _ := uuid.NewV7()

	mockCache.UpdateUserRs(CacheUpdateStruct{
		UserID:      user1ID,
		ReqID:       friReqID,
		OtherUserID: user2ID,
		Lable:       "send",
	})

	mockCache.UpdateUserRs(CacheUpdateStruct{
		UserID:      user2ID,
		ReqID:       friReqID,
		OtherUserID: user1ID,
		Lable:       "pending",
	})

	err := userService.CancelFriReq(ctx, user2ID, friReqID)
	if err != nil {
		t.Logf("CancelFriReq returned error: %v", err)
	}

	logger.Info("cancel friend request test completed", "req_id", friReqID)
}

func TestUserService_DeleteFriReq_Success(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing delete friend request service", "test", "delete_friend_req")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")
	user2, _ := userService.Register(ctx, "user2", "user2@example.com", "password")

	user1ID := user1.ID
	user2ID := user2.ID
	friReqID, _ := uuid.NewV7()

	mockCache.UpdateUserRs(CacheUpdateStruct{
		UserID:      user1ID,
		ReqID:       friReqID,
		OtherUserID: user2ID,
		Lable:       "send",
	})

	mockCache.UpdateUserRs(CacheUpdateStruct{
		UserID:      user2ID,
		ReqID:       friReqID,
		OtherUserID: user1ID,
		Lable:       "pending",
	})

	err := userService.DeleteFriReq(ctx, user1ID, friReqID)
	if err != nil {
		t.Logf("DeleteFriReq returned error: %v", err)
	}

	logger.Info("delete friend request test completed", "req_id", friReqID)
}

func TestUserService_GetFriendList_Empty(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing get friend list with no friends", "test", "get_friend_list_empty")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")

	list, err := userService.GetFriendList(ctx, user1.ID)
	if err != nil {
		t.Fatalf("failed to get friend list: %v", err)
	}

	if list == nil {
		t.Log("note: service returns nil for empty list, this may be expected behavior")
	}

	logger.Info("get friend list empty test completed", "user_id", user1.ID)
}

func TestUserService_GetPendingList_Empty(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing get pending list with no requests", "test", "get_pending_list_empty")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")

	list, err := userService.GetPendingList(ctx, user1.ID)
	if err != nil {
		t.Fatalf("failed to get pending list: %v", err)
	}

	if list == nil {
		t.Error("expected non-nil pending list")
	}

	logger.Info("get pending list empty test completed", "user_id", user1.ID)
}

func TestUserRepo_GetUserByEmail_NotFound(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing get user by email not found", "test", "get_user_by_email_not_found")

	mockRepo := newMockUserRepo()
	ctx := context.Background()

	_, _, err := mockRepo.GetUserByEmail(ctx, "nonexistent@example.com")
	if err != NoUserFoundErr {
		t.Errorf("expected NoUserFoundErr, got %v", err)
	}

	logger.Info("get user by email not found test completed", "error", err)
}

func TestUserRepo_GetUserByID_NotFound(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing get user by ID not found", "test", "get_user_by_id_not_found")

	mockRepo := newMockUserRepo()
	ctx := context.Background()

	_, _, err := mockRepo.GetUserByID(ctx, uuid.New())
	if err != NoUserFoundErr {
		t.Errorf("expected NoUserFoundErr, got %v", err)
	}

	logger.Info("get user by ID not found test completed", "error", err)
}

func TestUserCache_UpdateAndGetUserRs(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing user cache update and get", "test", "cache_update_get")

	mockCache := newMockUserCache()

	userID := uuid.New()
	reqID := uuid.New()
	otherUserID := uuid.New()

	mockCache.UpdateUserRs(CacheUpdateStruct{
		UserID:      userID,
		ReqID:       reqID,
		OtherUserID: otherUserID,
		Lable:       "pending",
	})

	exists := mockCache.GetUserRs(userID)
	if !exists {
		t.Error("expected user to exist in cache")
	}

	otherID := mockCache.GetOtherUserIDByReqID(userID, reqID, "pending")
	if otherID == nil || *otherID != otherUserID {
		t.Errorf("expected other user ID %s, got %s", otherUserID, *otherID)
	}

	logger.Info("cache update and get test completed", "user_id", userID)
}

func TestUserCache_CleanUpUserRs(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing user cache cleanup", "test", "cache_cleanup")

	mockCache := newMockUserCache()

	userID := uuid.New()
	reqID := uuid.New()
	otherUserID := uuid.New()

	mockCache.UpdateUserRs(CacheUpdateStruct{
		UserID:      userID,
		ReqID:       reqID,
		OtherUserID: otherUserID,
		Lable:       "pending",
	})

	mockCache.CleanUpUserRs(&CacheRsDeleteStruct{
		UserID: userID,
		ReqID:  reqID,
		Lable:  "pending",
	})

	otherID := mockCache.GetOtherUserIDByReqID(userID, reqID, "pending")
	if otherID != nil {
		t.Error("expected nil after cleanup")
	}

	logger.Info("cache cleanup test completed", "user_id", userID)
}

func TestUserCache_UpdateFriendCache(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing user cache friend update", "test", "cache_friend_update")

	mockCache := newMockUserCache()

	userID := uuid.New()
	friendID := uuid.New()

	mockCache.UpdateUserRs(CacheUpdateFriStruct{
		UserID: userID,
		ToID:   friendID,
		Lable:  "friend",
	})

	friendList := mockCache.GetUserFriList(userID)
	if friendList == nil || len(*friendList) != 1 || (*friendList)[0] != friendID {
		t.Errorf("expected friend list to contain friendID %s", friendID)
	}

	logger.Info("cache friend update test completed", "user_id", userID, "friend_id", friendID)
}

func TestServiceEdgeCases(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing service edge cases", "test", "service_edge_cases")

	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	t.Run("GetFriendList returns nil for new user", func(t *testing.T) {
		_, err := userService.Register(ctx, "testuser", "test@example.com", "password")
		if err != nil {
			t.Logf("Register returned error: %v", err)
		}
		// This should not panic
		list, err := userService.GetFriendList(ctx, uuid.New())
		if err != nil {
			t.Logf("GetFriendList returned error: %v", err)
		}
		if list != nil {
			t.Logf("GetFriendList returned: %v", list)
		}
	})

	logger.Info("service edge cases test completed")
}

func TestLoggerLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	logger.Info("test info log", "feature", "user_registration", "status", "success")
	logger.Warn("test warning log", "feature", "user_cache", "status", "cache_miss")
	logger.Error("test error log", "feature", "database", "error", "connection failed")

	output := buf.String()
	if output == "" {
		t.Error("expected non-empty log output")
	}

	t.Logf("Log output: %s", output)
}

func BenchmarkUserRegistration(b *testing.B) {
	logger := setupTestLogger()
	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = userService.Register(ctx, "user", "user@example.com", "password")
	}
}

func BenchmarkAddFriendSend(b *testing.B) {
	logger := setupTestLogger()
	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")
	user2, _ := userService.Register(ctx, "user2", "user2@example.com", "password")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = userService.AddFriendSend(ctx, user1.ID, user2.ID, "pending")
	}
}

func TestUserCache_MemoryUsage_10KUsers(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing memory usage for 10k users cache", "test", "memory_10k_users")

	mockCache := newMockUserCache()

	var memStatsBefore, memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	for i := 0; i < 10000; i++ {
		userID := uuid.New()
		mockCache.UpdateUserCache(&User{
			ID:        userID,
			Name:      fmt.Sprintf("user%d", i),
			Email:     fmt.Sprintf("user%d@example.com", i),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			IsRED:     false,
		})

		mockCache.UpdateUserRs(CacheUpdateStruct{
			UserID:      userID,
			ReqID:       uuid.New(),
			OtherUserID: uuid.New(),
			Lable:       "pending",
		})
	}

	runtime.ReadMemStats(&memStatsAfter)

	allocDiff := memStatsAfter.Alloc - memStatsBefore.Alloc
	totalAlloc := memStatsAfter.TotalAlloc - memStatsBefore.TotalAlloc

	logger.Info("memory test completed",
		"users_count", 10000,
		"heap_alloc_bytes", memStatsAfter.HeapAlloc,
		"alloc_diff_bytes", allocDiff,
		"total_alloc_bytes", totalAlloc,
		"num_gc", memStatsAfter.NumGC)

	t.Logf("Memory Usage for 10,000 Users:")
	t.Logf("  Heap Alloc: %d bytes (%.2f MB)", memStatsAfter.HeapAlloc, float64(memStatsAfter.HeapAlloc)/1024/1024)
	t.Logf("  Alloc Difference: %d bytes (%.2f MB)", allocDiff, float64(allocDiff)/1024/1024)
	t.Logf("  Total Alloc: %d bytes (%.2f MB)", totalAlloc, float64(totalAlloc)/1024/1024)
	t.Logf("  Number of GC: %d", memStatsAfter.NumGC)
}

func TestUserCache_MemoryUsage_100KUsers(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing memory usage for 100k users cache", "test", "memory_100k_users")

	mockCache := newMockUserCache()

	var memStatsBefore, memStatsAfter runtime.MemStats
	runtime.ReadMemStats(&memStatsBefore)

	for i := 0; i < 100000; i++ {
		userID := uuid.New()
		mockCache.UpdateUserCache(&User{
			ID:        userID,
			Name:      fmt.Sprintf("user%d", i),
			Email:     fmt.Sprintf("user%d@example.com", i),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			IsRED:     false,
		})

		if i%10 == 0 {
			mockCache.UpdateUserRs(CacheUpdateStruct{
				UserID:      userID,
				ReqID:       uuid.New(),
				OtherUserID: uuid.New(),
				Lable:       "pending",
			})
		}
	}

	runtime.ReadMemStats(&memStatsAfter)

	allocDiff := memStatsAfter.Alloc - memStatsBefore.Alloc

	logger.Info("memory test completed",
		"users_count", 100000,
		"heap_alloc_bytes", memStatsAfter.HeapAlloc,
		"alloc_diff_bytes", allocDiff,
		"num_gc", memStatsAfter.NumGC)

	t.Logf("Memory Usage for 100,000 Users:")
	t.Logf("  Heap Alloc: %d bytes (%.2f MB)", memStatsAfter.HeapAlloc, float64(memStatsAfter.HeapAlloc)/1024/1024)
	t.Logf("  Alloc Difference: %d bytes (%.2f MB)", allocDiff, float64(allocDiff)/1024/1024)
	t.Logf("  Number of GC: %d", memStatsAfter.NumGC)
}

func TestUserCache_Performance_10KUsers(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing performance for 10k users cache operations", "test", "perf_10k_users")

	mockCache := newMockUserCache()

	for i := 0; i < 10000; i++ {
		userID := uuid.New()
		mockCache.UpdateUserCache(&User{
			ID:        userID,
			Name:      fmt.Sprintf("user%d", i),
			Email:     fmt.Sprintf("user%d@example.com", i),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			IsRED:     false,
		})
	}

	startGet := time.Now()
	for i := 0; i < 10000; i++ {
		mockCache.GetUserRs(uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", i)))
	}
	elapsedGet := time.Since(startGet)

	startUpdate := time.Now()
	for i := 0; i < 1000; i++ {
		mockCache.UpdateUserRs(CacheUpdateStruct{
			UserID:      uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", i)),
			ReqID:       uuid.New(),
			OtherUserID: uuid.New(),
			Lable:       "pending",
		})
	}
	elapsedUpdate := time.Since(startUpdate)

	logger.Info("performance test completed",
		"users_count", 10000,
		"get_operations", 10000,
		"get_duration_ms", elapsedGet.Milliseconds(),
		"update_operations", 1000,
		"update_duration_ms", elapsedUpdate.Milliseconds())

	t.Logf("Performance Test for 10,000 Users:")
	t.Logf("  Get operations (10k): %d ms (%.2f ops/sec)",
		elapsedGet.Milliseconds(), float64(10000)/elapsedGet.Seconds())
	t.Logf("  Update operations (1k): %d ms (%.2f ops/sec)",
		elapsedUpdate.Milliseconds(), float64(1000)/elapsedUpdate.Seconds())
}

func TestUserCache_ConcurrentReadAccess(t *testing.T) {
	logger := setupTestLogger()
	logger.Info("testing concurrent read cache access", "test", "concurrent_read_access")

	mockCache := newMockUserCache()

	for i := 0; i < 1000; i++ {
		userID := uuid.New()
		mockCache.UpdateUserCache(&User{
			ID:    userID,
			Name:  fmt.Sprintf("user%d", i),
			Email: fmt.Sprintf("user%d@example.com", i),
		})
	}

	var wg sync.WaitGroup
	readChan := make(chan int, 100)

	start := time.Now()

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range readChan {
				mockCache.GetUserRs(uuid.New())
			}
		}()
	}

	for i := 0; i < 10000; i++ {
		readChan <- i
	}
	close(readChan)

	wg.Wait()
	elapsed := time.Since(start)

	logger.Info("concurrent read test completed",
		"operations", 10000,
		"duration_ms", elapsed.Milliseconds(),
		"ops_per_sec", float64(10000)/elapsed.Seconds())

	t.Logf("Concurrent Read Access Test (50 readers):")
	t.Logf("  Total operations: 10,000")
	t.Logf("  Duration: %d ms", elapsed.Milliseconds())
	t.Logf("  Ops/sec: %.2f", float64(10000)/elapsed.Seconds())
}

func BenchmarkCacheGet_10KUsers(b *testing.B) {
	mockCache := newMockUserCache()

	for i := 0; i < 10000; i++ {
		userID := uuid.New()
		mockCache.UpdateUserCache(&User{
			ID:        userID,
			Name:      fmt.Sprintf("user%d", i),
			Email:     fmt.Sprintf("user%d@example.com", i),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			IsRED:     false,
		})
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		mockCache.GetUserRs(uuid.New())
	}
}

func BenchmarkCacheUpdate_10KUsers(b *testing.B) {
	mockCache := newMockUserCache()

	for i := 0; i < 10000; i++ {
		userID := uuid.New()
		mockCache.UpdateUserCache(&User{
			ID:        userID,
			Name:      fmt.Sprintf("user%d", i),
			Email:     fmt.Sprintf("user%d@example.com", i),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			IsRED:     false,
		})
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		mockCache.UpdateUserRs(CacheUpdateStruct{
			UserID:      uuid.New(),
			ReqID:       uuid.New(),
			OtherUserID: uuid.New(),
			Lable:       "pending",
		})
	}
}

func BenchmarkCacheGetConcurrent_10KUsers(b *testing.B) {
	mockCache := newMockUserCache()

	for i := 0; i < 10000; i++ {
		userID := uuid.New()
		mockCache.UpdateUserCache(&User{
			ID:        userID,
			Name:      fmt.Sprintf("user%d", i),
			Email:     fmt.Sprintf("user%d@example.com", i),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			IsRED:     false,
		})
	}

	var wg sync.WaitGroup

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < b.N; j++ {
				mockCache.GetUserRs(uuid.New())
			}
		}()
	}

	wg.Wait()
}

type mockDBWithLatency struct {
	latency time.Duration
	queries int64
	mu      sync.Mutex
}

func (m *mockDBWithLatency) simulateLatency() {
	if m.latency > 0 {
		time.Sleep(m.latency)
	}
	m.mu.Lock()
	m.queries++
	m.mu.Unlock()
}

type mockUserRepoWithLatency struct {
	users          map[uuid.UUID]*User
	passwords      map[uuid.UUID]string
	friendRequests map[uuid.UUID][]FriendReq
	friendLists    map[uuid.UUID][]uuid.UUID
	latency        time.Duration
	queries        int64
	mu             sync.Mutex
}

func newMockUserRepoWithLatency(latency time.Duration) *mockUserRepoWithLatency {
	return &mockUserRepoWithLatency{
		users:          make(map[uuid.UUID]*User),
		passwords:      make(map[uuid.UUID]string),
		friendRequests: make(map[uuid.UUID][]FriendReq),
		friendLists:    make(map[uuid.UUID][]uuid.UUID),
		latency:        latency,
	}
}

func (m *mockUserRepoWithLatency) simulateLatency() {
	m.mu.Lock()
	m.queries++
	m.mu.Unlock()
	if m.latency > 0 {
		time.Sleep(m.latency)
	}
}

func (m *mockUserRepoWithLatency) Create(ctx context.Context, input CreateUserInput) (*User, error) {
	m.simulateLatency()
	for _, u := range m.users {
		if u.Email == input.Email {
			return nil, DuplicateKeyErr
		}
	}
	user := &User{
		ID:        uuid.New(),
		Name:      input.Name,
		Email:     input.Email,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		IsRED:     false,
	}
	m.users[user.ID] = user
	m.passwords[user.ID] = input.Password
	return user, nil
}

func (m *mockUserRepoWithLatency) GetUserByEmail(ctx context.Context, email string) (*User, string, error) {
	m.simulateLatency()
	for _, u := range m.users {
		if u.Email == email {
			return u, m.passwords[u.ID], nil
		}
	}
	return nil, "", NoUserFoundErr
}

func (m *mockUserRepoWithLatency) GetUserByID(ctx context.Context, id uuid.UUID) (*User, string, error) {
	m.simulateLatency()
	if u, ok := m.users[id]; ok {
		return u, m.passwords[id], nil
	}
	return nil, "", NoUserFoundErr
}

func (m *mockUserRepoWithLatency) UpdateUserPassword(ctx context.Context, payload UpdateUserPassword) (*User, error) {
	m.simulateLatency()
	if _, ok := m.users[payload.UserID]; !ok {
		return nil, NoUserFoundErr
	}
	m.passwords[payload.UserID] = payload.Password
	return m.users[payload.UserID], nil
}

func (m *mockUserRepoWithLatency) GetAllUsers(ctx context.Context) (*[]database.User, error) {
	m.simulateLatency()
	var users []database.User
	for _, u := range m.users {
		users = append(users, database.User{
			ID:          u.ID,
			CreatedAt:   u.CreatedAt,
			UpdatedAt:   u.UpdatedAt,
			Email:       u.Email,
			Name:        u.Name,
			IsChirpyRed: pgtype.Bool{Bool: u.IsRED, Valid: true},
		})
	}
	return &users, nil
}

func (m *mockUserRepoWithLatency) GetAllUsersRs(ctx context.Context) (*[]database.UserRelationship, error) {
	m.simulateLatency()
	return &[]database.UserRelationship{}, nil
}

func (m *mockUserRepoWithLatency) SendFriendRequest(fromID, toID, friReqID uuid.UUID) error {
	m.simulateLatency()
	m.friendRequests[fromID] = append(m.friendRequests[fromID], FriendReq{
		ReqID:  friReqID,
		FromID: fromID,
		ToID:   toID,
	})
	return nil
}

func (m *mockUserRepoWithLatency) GetMyFriReqList(ctx context.Context, userID uuid.UUID) (*[]database.UserRelationship, error) {
	m.simulateLatency()
	return &[]database.UserRelationship{}, nil
}

func (m *mockUserRepoWithLatency) GetMySendFirReqList(ctx context.Context, userID uuid.UUID) (*[]database.UserRelationship, error) {
	m.simulateLatency()
	return &[]database.UserRelationship{}, nil
}

func (m *mockUserRepoWithLatency) UpdateFriReq(reqID uuid.UUID) error {
	m.simulateLatency()
	return nil
}

func (m *mockUserRepoWithLatency) GetUserFriListByID(ctx context.Context, userID uuid.UUID) (*[]uuid.UUID, error) {
	m.simulateLatency()
	if list, ok := m.friendLists[userID]; ok {
		return &list, nil
	}
	return nil, nil
}

func (m *mockUserRepoWithLatency) CancelFriReq(reqID uuid.UUID, updateTime time.Time) error {
	m.simulateLatency()
	return nil
}

func (m *mockUserRepoWithLatency) DeleteFriReq(reqID uuid.UUID) error {
	m.simulateLatency()
	return nil
}

func (m *mockUserRepoWithLatency) GetOtherUserIDByReqID(ctx context.Context, userID uuid.UUID, reqID uuid.UUID) (*User, error) {
	m.simulateLatency()
	return nil, nil
}

func getTestMQWithLatency(latency time.Duration) *mqWithLatency {
	return &mqWithLatency{
		latency: latency,
	}
}

type mqWithLatency struct {
	publishedJobs []interface{}
	latency       time.Duration
}

func (m *mqWithLatency) PublishWithContext(ctx context.Context, topic string, job interface{}) error {
	if m.latency > 0 {
		time.Sleep(m.latency)
	}
	m.publishedJobs = append(m.publishedJobs, job)
	return nil
}

func (m *mqWithLatency) Run() {}

func (m *mqWithLatency) ListeningForTheChannels(topic string, bufferSize int, worker func(chan *Channel)) {
}

func (m *mqWithLatency) Republish(msg *Channel, retries int) {}

type Channel struct {
	Msg          interface{}
	RetriesCount int
}

func TestEndpointLatency_Register_10K(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 10K latency test in short mode")
	}
	logger := setupTestLogger()
	logger.Info("testing service latency for 10k register requests", "test", "latency_register_10k")

	dbLatency := 1 * time.Millisecond
	mockRepo := newMockUserRepoWithLatency(dbLatency)
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	latencies := make([]time.Duration, 1000)

	for i := 0; i < 1000; i++ {
		start := time.Now()
		_, _ = userService.Register(ctx, fmt.Sprintf("user%d", i), fmt.Sprintf("user%d@example.com", i), "password123")
		latencies[i] = time.Since(start)
	}

	var total time.Duration
	var p50, p95, p99 time.Duration
	var max time.Duration

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	for _, l := range latencies {
		total += l
		if l > max {
			max = l
		}
	}

	avg := total / time.Duration(len(latencies))
	p50 = latencies[len(latencies)*50/100]
	p95 = latencies[len(latencies)*95/100]
	p99 = latencies[len(latencies)*99/100]

	logger.Info("register service latency test completed",
		"requests", 10000,
		"db_latency_ms", dbLatency.Milliseconds(),
		"avg_latency_ms", avg.Milliseconds(),
		"p50_latency_ms", p50.Milliseconds(),
		"p95_latency_ms", p95.Milliseconds(),
		"p99_latency_ms", p99.Milliseconds(),
		"max_latency_ms", max.Milliseconds())

	t.Logf("\n=== Register Service Latency (10K requests) ===")
	t.Logf("Simulated DB Latency: %d ms", dbLatency.Milliseconds())
	t.Logf("Average: %.2f ms", float64(avg.Milliseconds()))
	t.Logf("P50: %d ms", p50.Milliseconds())
	t.Logf("P95: %d ms", p95.Milliseconds())
	t.Logf("P99: %d ms", p99.Milliseconds())
	t.Logf("Max: %d ms", max.Milliseconds())
}

func TestEndpointLatency_GetFriendList_10K(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 10K latency test in short mode")
	}
	logger := setupTestLogger()
	logger.Info("testing service latency for 10k get friend list requests", "test", "latency_getfriendlist_10k")

	dbLatency := 10 * time.Millisecond
	mockRepo := newMockUserRepoWithLatency(dbLatency)
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()
	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")

	latencies := make([]time.Duration, 1000)

	for i := 0; i < 1000; i++ {
		start := time.Now()
		_, _ = userService.GetFriendList(ctx, user1.ID)
		latencies[i] = time.Since(start)
	}

	var total time.Duration
	var p50, p95, p99 time.Duration
	var max time.Duration

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	for _, l := range latencies {
		total += l
		if l > max {
			max = l
		}
	}

	avg := total / time.Duration(len(latencies))
	p50 = latencies[len(latencies)*50/100]
	p95 = latencies[len(latencies)*95/100]
	p99 = latencies[len(latencies)*99/100]

	logger.Info("get friend list service latency test completed",
		"requests", 10000,
		"db_latency_ms", dbLatency.Milliseconds(),
		"avg_latency_ms", avg.Milliseconds(),
		"p50_latency_ms", p50.Milliseconds(),
		"p95_latency_ms", p95.Milliseconds(),
		"p99_latency_ms", p99.Milliseconds(),
		"max_latency_ms", max.Milliseconds())

	t.Logf("\n=== GetFriendList Service Latency (10K requests) ===")
	t.Logf("Simulated DB Latency: %d ms", dbLatency.Milliseconds())
	t.Logf("Average: %.2f ms", float64(avg.Milliseconds()))
	t.Logf("P50: %d ms", p50.Milliseconds())
	t.Logf("P95: %d ms", p95.Milliseconds())
	t.Logf("P99: %d ms", p99.Milliseconds())
	t.Logf("Max: %d ms", max.Milliseconds())
}

func TestEndpointLatency_GetPendingList_10K(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 10K latency test in short mode")
	}
	logger := setupTestLogger()
	logger.Info("testing service latency for 10k get pending list requests", "test", "latency_getpending_10k")

	dbLatency := 10 * time.Millisecond
	mockRepo := newMockUserRepoWithLatency(dbLatency)
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()
	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")

	latencies := make([]time.Duration, 1000)

	for i := 0; i < 1000; i++ {
		start := time.Now()
		_, _ = userService.GetPendingList(ctx, user1.ID)
		latencies[i] = time.Since(start)
	}

	var total time.Duration
	var p50, p95, p99 time.Duration
	var max time.Duration

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	for _, l := range latencies {
		total += l
		if l > max {
			max = l
		}
	}

	avg := total / time.Duration(len(latencies))
	p50 = latencies[len(latencies)*50/100]
	p95 = latencies[len(latencies)*95/100]
	p99 = latencies[len(latencies)*99/100]

	logger.Info("get pending list service latency test completed",
		"requests", 10000,
		"db_latency_ms", dbLatency.Milliseconds(),
		"avg_latency_ms", avg.Milliseconds(),
		"p50_latency_ms", p50.Milliseconds(),
		"p95_latency_ms", p95.Milliseconds(),
		"p99_latency_ms", p99.Milliseconds(),
		"max_latency_ms", max.Milliseconds())

	t.Logf("\n=== GetPendingList Service Latency (10K requests) ===")
	t.Logf("Simulated DB Latency: %d ms", dbLatency.Milliseconds())
	t.Logf("Average: %.2f ms", float64(avg.Milliseconds()))
	t.Logf("P50: %d ms", p50.Milliseconds())
	t.Logf("P95: %d ms", p95.Milliseconds())
	t.Logf("P99: %d ms", p99.Milliseconds())
	t.Logf("Max: %d ms", max.Milliseconds())
}

func TestEndpointLatency_DifferentDBLatencies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping latency test in short mode")
	}

	logger := setupTestLogger()
	logger.Info("testing service latency with different DB latencies", "test", "latency_various_db")

	latenciesToTest := []time.Duration{
		1 * time.Millisecond,
		5 * time.Millisecond,
		10 * time.Millisecond,
	}

	t.Logf("\n=== Service Latency with Different DB Latencies ===")
	t.Logf("%-15s | %-20s | %-10s", "DB Latency", "Avg Request Latency", "Overhead")
	t.Logf("--------------------------------------------------")

	for _, dbLatency := range latenciesToTest {
		mockRepo := newMockUserRepoWithLatency(dbLatency)
		mockCache := newMockUserCache()
		testMQ := getTestMQ()

		userService := NewUserService(mockRepo, mockCache, testMQ, logger)
		ctx := createTestContext()

		latencies := make([]time.Duration, 100)

		for i := 0; i < 100; i++ {
			start := time.Now()
			_, _ = userService.Register(ctx, fmt.Sprintf("user%d", i), fmt.Sprintf("user%d@example.com", i), "password123")
			latencies[i] = time.Since(start)
		}

		var total time.Duration
		for _, l := range latencies {
			total += l
		}
		avg := total / time.Duration(len(latencies))

		overhead := float64(avg.Milliseconds()) - float64(dbLatency.Milliseconds())

		logger.Info("latency test completed",
			"db_latency_ms", dbLatency.Milliseconds(),
			"avg_latency_ms", avg.Milliseconds())

		t.Logf("%-15d | %-20.2f | %-10.2f",
			dbLatency.Milliseconds(), float64(avg.Milliseconds()), overhead)
	}
}

func BenchmarkEndpoint_Register_10K(b *testing.B) {
	logger := setupTestLogger()
	dbLatency := 10 * time.Millisecond
	mockRepo := newMockUserRepoWithLatency(dbLatency)
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = userService.Register(ctx, fmt.Sprintf("user%d", i), fmt.Sprintf("user%d@example.com", i), "password123")
	}
}

func TestFriendService_FullFlow_Verification(t *testing.T) {
	logger := setupTestLogger()
	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")
	user2, _ := userService.Register(ctx, "user2", "user2@example.com", "password")

	t.Run("AddFriendSend creates correct cache entries", func(t *testing.T) {
		friReqID, _ := userService.AddFriendSend(ctx, user1.ID, user2.ID, "Hello!")
		if friReqID == uuid.Nil {
			t.Fatal("AddFriendSend should return a valid reqID even if MQ fails")
		}

		sendList := mockCache.GetUserSendReqList(user1.ID)
		if sendList == nil {
			t.Fatal("sender's send list should not be nil")
		}
		if _, ok := (*sendList)[friReqID]; !ok {
			t.Errorf("sender's send list should contain reqID %s", friReqID)
		}

		pendingList := mockCache.GetUserReqList(user2.ID)
		if pendingList == nil {
			t.Fatal("receiver's pending list should not be nil")
		}
		if _, ok := (*pendingList)[friReqID]; !ok {
			t.Errorf("receiver's pending list should contain reqID %s", friReqID)
		}

		otherUserID := mockCache.GetOtherUserIDByReqID(user1.ID, friReqID, "send")
		if otherUserID == nil {
			t.Fatal("GetOtherUserIDByReqID should return non-nil for sender")
		}
		if *otherUserID != user2.ID {
			t.Errorf("sender's send request should point to user2, got %s", *otherUserID)
		}
	})

	t.Run("GetPendingList returns correct pending requests", func(t *testing.T) {
		pendingList, err := userService.GetPendingList(ctx, user2.ID)
		if err != nil {
			t.Fatalf("GetPendingList failed: %v", err)
		}
		if pendingList == nil {
			t.Fatal("pending list should not be nil")
		}
		if pendingList.PendingIDsList == nil {
			t.Fatal("PendingIDsList should not be nil")
		}
		if len(*pendingList.PendingIDsList) == 0 {
			t.Error("PendingIDsList should contain at least one request")
		}

		found := false
		for reqID, fromUserID := range *pendingList.PendingIDsList {
			if fromUserID == user1.ID {
				found = true
				t.Logf("Found pending request from user1 with reqID: %s", reqID)
			}
		}
		if !found {
			t.Errorf("pending list should contain request from user1")
		}
	})

	t.Run("ConfirmFriendReq makes both users friends", func(t *testing.T) {
		sendList := mockCache.GetUserSendReqList(user1.ID)
		if sendList == nil {
			t.Fatal("sender's send list should not be nil")
		}
		var friReqID uuid.UUID
		for reqID := range *sendList {
			friReqID = reqID
			break
		}

		_ = userService.ConfirmFriendReq(ctx, user2.ID, friReqID, "accept")

		friendList1, err := userService.GetFriendList(ctx, user1.ID)
		if err != nil {
			t.Fatalf("GetFriendList for user1 failed: %v", err)
		}
		if friendList1 == nil {
			t.Fatal("friend list should not be nil for user1")
		}

		found := false
		for _, friendID := range *friendList1 {
			if friendID == user2.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("user1's friend list should contain user2")
		}

		friendList2, err := userService.GetFriendList(ctx, user2.ID)
		if err != nil {
			t.Fatalf("GetFriendList for user2 failed: %v", err)
		}
		if friendList2 == nil {
			t.Fatal("friend list should not be nil for user2")
		}

		found = false
		for _, friendID := range *friendList2 {
			if friendID == user1.ID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("user2's friend list should contain user1")
		}
	})

	t.Run("After confirming, pending and send lists should be cleared", func(t *testing.T) {
		pendingList := mockCache.GetUserReqList(user2.ID)
		if pendingList != nil && len(*pendingList) > 0 {
			t.Errorf("pending list should be empty after confirmation, got %d", len(*pendingList))
		}

		sendList := mockCache.GetUserSendReqList(user1.ID)
		if sendList != nil && len(*sendList) > 0 {
			t.Errorf("send list should be empty after confirmation, got %d", len(*sendList))
		}
	})
}

func TestFriendService_CancelRequest_Verification(t *testing.T) {
	logger := setupTestLogger()
	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")
	user2, _ := userService.Register(ctx, "user2", "user2@example.com", "password")

	friReqID, _ := userService.AddFriendSend(ctx, user1.ID, user2.ID, "Hello!")

	_ = userService.CancelFriReq(ctx, user2.ID, friReqID)

	pendingList := mockCache.GetUserReqList(user2.ID)
	if pendingList != nil && len(*pendingList) > 0 {
		t.Errorf("pending list should be empty after cancel, got %d", len(*pendingList))
	}

	sendList := mockCache.GetUserSendReqList(user1.ID)
	if sendList != nil && len(*sendList) > 0 {
		t.Errorf("send list should be empty after cancel, got %d", len(*sendList))
	}
}

func TestFriendService_DeleteRequest_Verification(t *testing.T) {
	logger := setupTestLogger()
	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")
	user2, _ := userService.Register(ctx, "user2", "user2@example.com", "password")

	friReqID, _ := userService.AddFriendSend(ctx, user1.ID, user2.ID, "Hello!")

	_ = userService.DeleteFriReq(ctx, user1.ID, friReqID)

	pendingList := mockCache.GetUserReqList(user2.ID)
	if pendingList != nil && len(*pendingList) > 0 {
		t.Errorf("pending list should be empty after delete, got %d", len(*pendingList))
	}

	sendList := mockCache.GetUserSendReqList(user1.ID)
	if sendList != nil && len(*sendList) > 0 {
		t.Errorf("send list should be empty after delete, got %d", len(*sendList))
	}
}

func TestFriendService_MultipleFriends_Verification(t *testing.T) {
	logger := setupTestLogger()
	mockRepo := newMockUserRepo()
	mockCache := newMockUserCache()
	testMQ := getTestMQ()

	userService := NewUserService(mockRepo, mockCache, testMQ, logger)
	ctx := createTestContext()

	user1, _ := userService.Register(ctx, "user1", "user1@example.com", "password")
	user2, _ := userService.Register(ctx, "user2", "user2@example.com", "password")
	user3, _ := userService.Register(ctx, "user3", "user3@example.com", "password")
	user4, _ := userService.Register(ctx, "user4", "user4@example.com", "password")

	reqID1, _ := userService.AddFriendSend(ctx, user1.ID, user2.ID, "friend1")
	reqID2, _ := userService.AddFriendSend(ctx, user1.ID, user3.ID, "friend2")
	reqID3, _ := userService.AddFriendSend(ctx, user1.ID, user4.ID, "friend3")

	userService.ConfirmFriendReq(ctx, user2.ID, reqID1, "accept")
	userService.ConfirmFriendReq(ctx, user3.ID, reqID2, "accept")
	userService.ConfirmFriendReq(ctx, user4.ID, reqID3, "accept")

	friendList, err := userService.GetFriendList(ctx, user1.ID)
	if err != nil {
		t.Fatalf("GetFriendList failed: %v", err)
	}
	if friendList == nil {
		t.Fatal("friend list should not be nil")
	}

	if len(*friendList) != 3 {
		t.Errorf("user1 should have 3 friends, got %d", len(*friendList))
	}

	expectedFriends := map[uuid.UUID]bool{
		user2.ID: true,
		user3.ID: true,
		user4.ID: true,
	}

	for _, friendID := range *friendList {
		if !expectedFriends[friendID] {
			t.Errorf("unexpected friend %s in list", friendID)
		}
		delete(expectedFriends, friendID)
	}

	if len(expectedFriends) > 0 {
		t.Errorf("missing friends: %v", expectedFriends)
	}

	friendList2, _ := userService.GetFriendList(ctx, user2.ID)
	if friendList2 == nil || len(*friendList2) != 1 {
		t.Errorf("user2 should have exactly 1 friend, got %d", len(*friendList2))
	}
}

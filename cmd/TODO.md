# TODO - Chirpy Project

## Completed Today (2026-02-15)

### Friend System
- ✅ Fixed `AddFriendSend` to accept only `to_id` from client, server fetches friend's name
- ✅ Implemented `FriendMetaData` struct with `UserID` and `Name`
- ✅ Added server-side name fetching in `ConfirmFriendReq`, `CancelFriReq`, `DeleteFriReq`
- ✅ Fixed GetFriendList to return `[]FriendMetaData` (includes UserID + Name)
- ✅ Fixed GetPendingList to update cache after DB fetch
- ✅ Added comprehensive verification tests for friend flows
- ✅ Fixed route bug - DELETE endpoint was using wrong handler

### Password Validation
- ✅ Added password validation using regex (built-in, no external package)
- ✅ Minimum 8 characters
- ✅ At least one special character (@$!%*?&)
- ✅ Applied to both Register and UpdatePassword endpoints

### Rate Limiting
- ✅ Created in-memory rate limiter package (`pkg/ratelimit`)
- ✅ Added rate limiting to friend endpoints:
  - POST /api/friends/requests: 10 requests/minute
  - PUT /api/friends/requests/{id}: 30 requests/minute
  - DELETE /api/friends/requests/{id}: 30 requests/minute
- ✅ Returns 429 Too Many Requests when limit exceeded
- ✅ Logs rate limit hits

### Logging
- ✅ Added proper slog logging with request IDs to:
  - AddFriendSend
  - ConfirmFriendReq
- ✅ Replaced some log.Printf with slog

### Database Seeder
- ✅ Created seed data generator (`cmd/seed/main.go`)
- ✅ Seeds 100 users with hashed passwords
- ✅ Creates ~50 random friend relationships

### Testing
- ✅ Verified all friend features work correctly via API
- ✅ Tested password validation
- ✅ Tested rate limiting

### Documentation
- ✅ Created API documentation (`cmd/doc.md`)

---

## Still Need to Do

### Priority High
- [ ] Fix test mocks - they have interface mismatches (GetMyFriReqList, GetOtherUserIDByReqID)
- [ ] Add TTL to user cache (memory cleanup)
- [ ] Name update feature - when user changes name, update all their relationships
- [ ] Fix duplicate friend request check - can send multiple requests to same user

### Priority Medium
- [ ] Convert cache logging from log.Printf to slog
- [ ] Add rate limiting to more endpoints (login, register)
- [ ] Add pagination to friend list endpoints
- [ ] Consider Redis-based rate limiting for production

### Priority Low
- [ ] Abstract business logic between service and worker
- [ ] Use Redis streams for message cache
- [ ] Implement email verification flow

---

## Known Issues
- Test files have interface mismatches (tests won't compile)
- Cache doesn't release memory back to OS (Go behavior)
- No duplicate friend request prevention

---

## Notes
- Security: Client only sends user ID, server fetches name - prevents name spoofing
- Friend list returns UserID + Name in single call - frontend doesn't need additional API calls
- Rate limiting is in-memory only (resets on server restart)

# Chirpy API Documentation

## Authentication Flow

### 1. Register User
**Endpoint:** `POST /api/users`

Register a new user in the system.

**Request Body:**
```json
{
  "name": "string",
  "email": "string",
  "password": "string (min 8 chars, at least one special character @ $ ! % * ? &)"
}
```

**Response (201):**
```json
{
  "id": "uuid",
  "name": "string",
  "email": "string",
  "is_chirpy_red": false,
  "created_at": "timestamp",
  "updated_at": "timestamp"
}
```

**Errors:**
- 400: "all fields need to have value"
- 400: "password must be at least 8 characters with at least one special character (@$!%*?&)"
- 400: "the user already exists"
- 400: "the user name already exists"

---

### 2. Login
**Endpoint:** `POST /api/login`

Authenticate user and get JWT token.

**Request Body:**
```json
{
  "email": "string",
  "password": "string"
}
```

**Response (200):**
```json
{
  "id": "uuid",
  "email": "string",
  "name": "string",
  "token": "jwt_token",
  "refresh_token": "refresh_token",
  "is_chirpy_red": false,
  "created_at": "timestamp",
  "updated_at": "timestamp"
}
```

**Errors:**
- 401: "invalid credentials"

---

### 3. Refresh Token
**Endpoint:** `POST /api/refresh`

Get new access token using refresh token.

**Headers:**
```
Authorization: Bearer <refresh_token>
```

**Response (200):**
```json
{
  "token": "jwt_token"
}
```

---

### 4. Revoke Token
**Endpoint:** `POST /api/revoke`

Revoke a refresh token.

**Request Body:**
```json
{
  "token": "refresh_token"
}
```

**Response (204):** No content

---

## User Endpoints

### 5. Update Password
**Endpoint:** `POST /api/users/password` (Authenticated)

Update user's password.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Request Body:**
```json
{
  "old_password": "string",
  "new_password": "string (min 8 chars, at least one special character @ $ ! % * ? &)"
}
```

**Response (200):**
```json
{
  "id": "uuid",
  "email": "string",
  "name": "string"
}
```

**Errors:**
- 400: "password must be at least 8 characters with at least one special character (@$!%*?&)"
- 401: "unauthorized" (wrong old password)
- 404: "no user found error"

---

### 6. Search Users
**Endpoint:** `GET /api/users/search?q=<name>` (Authenticated, Rate Limited: 20/min)

Search for users by name prefix.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Query Parameters:**
- `q` (required): Search query (partial name match)

**Response (200):**
```json
{
  "UserList": [
    {
      "id": "uuid",
      "name": "string",
      "email": "string",
      "is_chirpy_red": false,
      "created_at": "timestamp",
      "updated_at": "timestamp"
    }
  ]
}
```

**Errors:**
- 400: "search name is not valid" (missing query param)

---

## Friend System Endpoints

### 7. Send Friend Request
**Endpoint:** `POST /api/friends/requests` (Authenticated, Rate Limited: 10/min)

Send a friend request to another user.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Request Body:**
```json
{
  "to_id": "uuid"
}
```

**Response (201):**
```json
{
  "req_id": "uuid"
}
```

**Errors:**
- 400: "invalid parameters"
- 429: "too many requests, please try again later"

---

### 8. Get Pending Requests
**Endpoint:** `GET /api/friends/requests` (Authenticated)

Get pending friend requests (incoming and outgoing).

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Response (200):**
```json
{
  "pending_ids": {
    "request_uuid": {
      "UserID": "uuid",
      "Name": "string"
    }
  },
  "request_ids": {
    "request_uuid": {
      "UserID": "uuid",
      "Name": "string"
    }
  }
}
```

- `pending_ids`: Requests sent TO the user
- `request_ids`: Requests sent BY the user

---

### 9. Confirm/Reject Friend Request
**Endpoint:** `PUT /api/friends/requests/{request_id}/` (Authenticated, Rate Limited: 30/min)

Confirm or cancel a friend request.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Request Body:**
```json
{
  "status": "confirm"
}
```

**Response (200):** Empty

**Request Body (for cancel):**
```json
{
  "status": "cancel"
}
```

**Errors:**
- 400: "invalid parameters"
- 429: "too many requests, please try again later"

---

### 10. Delete/Cancel Friend Request
**Endpoint:** `DELETE /api/friends/requests/{request_id}/` (Authenticated, Rate Limited: 30/min)

Cancel a sent friend request.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Response (204):** No content

**Errors:**
- 429: "too many requests, please try again later"

---

### 11. Get Friend List
**Endpoint:** `GET /api/friends` (Authenticated)

Get list of all confirmed friends.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Response (200):**
```json
{
  "id_list": [
    {
      "UserID": "uuid",
      "Name": "string"
    }
  ]
}
```

---

## Group Endpoints

### 12. Create Group
**Endpoint:** `POST /api/chats/groups` (Authenticated)

Create a new chat group.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Request Body:**
```json
{
  "group_name": "string (unique)",
  "description": "string (optional)",
  "max_mems": number (optional, default 10),
  "member_ids": ["uuid"] (optional)
}
```

**Response (201):**
```json
{
  "chat_id": "uuid"
}
```

**Errors:**
- 400: "duplicate name"

---

### 13. Join Group
**Endpoint:** `POST /api/chats/groups/{group_id}/members` (Authenticated)

Add a member to a group.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Request Body:**
```json
{
  "user_id": "uuid"
}
```

**Response (201):** Empty

---

### 14. Leave Group
**Endpoint:** `DELETE /api/chats/groups/{group_id}/members` (Authenticated)

Remove a member from a group.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Request Body:**
```json
{
  "user_id": "uuid"
}
```

**Response (204):** No content

---

## Chat/Messages Endpoints

### 15. Send Message
**Endpoint:** `POST /api/chats/message` (Authenticated)

Send a message to a user or group. Recipient must be connected via WebSocket.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Request Body:**
```json
{
  "to_id": "uuid (user or group ID)",
  "msg": "string",
  "type": "private" | "public"
}
```
- `type`: "private" for direct messages, "public" for group messages

**Response (200):**
```json
{
  "msg_id": "uuid"
}
```

**Errors:**
- 400: "client not found, consider connecting to ws" (recipient not connected)

---

### 16. Get Private Messages
**Endpoint:** `GET /api/chat/{otherUser_id}/messages` (Authenticated)

Get messages between current user and another user.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Response (200):**
```json
{
  "messages": [
    {
      "id": "uuid",
      "from_id": "uuid",
      "to_id": "uuid",
      "message": "string",
      "created_at": "timestamp"
    }
  ]
}
```

---

### 17. Get Group Messages
**Endpoint:** `GET /api/chat/groups/{group_id}/messages` (Authenticated)

Get messages in a group.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

**Response (200):**
```json
{
  "messages": [
    {
      "id": "uuid",
      "from_id": "uuid",
      "to_id": "uuid",
      "message": "string",
      "created_at": "timestamp"
    }
  ]
}
```

---

### 18. WebSocket Connection
**Endpoint:** `GET /api/chats/ws` (Authenticated)

WebSocket endpoint for real-time chat.

**Headers:**
```
Authorization: Bearer <jwt_token>
```

---

## Admin Endpoints

### 19. Get Metrics
**Endpoint:** `GET /admin/metrics`

Get server metrics.

**Response (200):**
```json
{
  "views": 0
}
```

---

### 20. Reset Metrics
**Endpoint:** `POST /admin/metrics/reset`

Reset server metrics.

**Response (204):** No content

---

### 21. Reset Database
**Endpoint:** `POST /admin/reset`

Reset the database (dev only).

**Response (204):** No content

---

## Webhooks

### 22. Polka Webhook
**Endpoint:** `POST /api/polka/webhooks`

Handle Polka webhooks.

**Request Body:**
```json
{
  "event": "string",
  "data": {
    "user_id": "uuid"
  }
}
```

**Response (200):** OK

---

## Health Check

### 23. Health Check
**Endpoint:** `GET /api/healthz`

Check if the server is running.

**Response (200):** OK

---

## Rate Limiting

| Endpoint | Limit |
|----------|-------|
| GET /api/users/search | 20 requests/minute |
| POST /api/friends/requests | 10 requests/minute |
| PUT /api/friends/requests/{id} | 30 requests/minute |
| DELETE /api/friends/requests/{id} | 30 requests/minute |

---

## Error Responses

All error responses follow this format:
```json
{
  "error": "error message"
}
```

Common status codes:
- 400: Bad Request
- 401: Unauthorized
- 403: Forbidden
- 404: Not Found
- 429: Too Many Requests
- 500: Internal Server Error

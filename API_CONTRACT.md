# Chirpy frontend API contract

This document describes the API currently implemented by the checked-out
backend. It is intended for a frontend client (web or native).

## Environments and conventions

- Production base URL: `https://api.ponypigeon.com`.
- Render fallback host: `https://chirpy-api.onrender.com`.
- Local default: `http://localhost:8080`.
- REST paths below are relative to the base URL and already include `/api`
  where applicable.
- JSON requests must send `Content-Type: application/json`.
- UUIDs are lowercase UUID strings. Timestamps are RFC3339/RFC3339Nano.
- Authenticated REST requests use:

  ```http
  Authorization: Bearer <access-token>
  ```

- Access tokens last one hour by default. Refresh tokens last 30 days and are
  rotated on every successful refresh. Store the replacement refresh token;
  the previous one becomes invalid immediately.

Unless stated otherwise, an error is JSON in this shape:

```json
{"error":"human-readable message"}
```

`401` means missing, malformed, expired, or revoked access credentials
(`{"error":"unauthorized"}`). A request body that isn't valid JSON returns
`400`. `429` means the route rate limit was exceeded
(`{"error":"too many requests, please try again later"}`); every rate-limited
response carries `X-RateLimit-Limit` and `X-RateLimit-Remaining` headers.
Unexpected failures return `500` with `{"error":"internal server error"}`.

Rate limits are per user when authenticated, otherwise per client IP (the
real client address behind Render's proxy, which clients cannot spoof).
**Login, refresh and revoke share one budget of 10 requests/minute** (they are
one policy), so token refreshes count against the same limit as logins.

## Health endpoints

| Method | Path | Auth | Success |
|---|---|---|---|
| GET | `/livez` | No | `204 No Content`; process liveness only |
| GET | `/readyz` | No | `200` or `503`; `{"postgres":"ok","redis":"ok"}` |
| GET | `/healthz` | No | Alias of `/readyz` |

## Authentication and users

### Register

`POST /api/users` — no auth, maximum 5 requests/minute.

Request:

```json
{"name":"alice","email":"alice@example.com","password":"Pw-example!"}
```

Rules: name is 3–25 characters with no whitespace; email must be valid and is
normalized to lowercase; password is 8–128 characters and must contain at
least one of `@$!%*?&`.

Success: `201`, a user object:

```json
{
  "id":"uuid", "name":"alice", "created_at":"2026-01-01T00:00:00Z",
  "updated_at":"2026-01-01T00:00:00Z", "email":"alice@example.com",
  "is_chirpy_red":false
}
```

`400` invalid input; `409` duplicate email (`the user already exists`) or
duplicate name (`the user name already exists`). Note: the duplicate-email
response reveals that an email is registered; this changes when email
verification is added.

### Login

`POST /api/login` — no auth; shares the 10 requests/minute auth budget.

Request: `{"email":"alice@example.com","password":"Pw-example!"}`

Success: `200`:

```json
{
  "id":"uuid", "name":"alice", "created_at":"...", "updated_at":"...",
  "email":"alice@example.com", "is_chirpy_red":false,
  "token":"<access-token>", "refresh_token":"<refresh-token>"
}
```

Invalid email or password returns the same `401 {"error":"invalid credentials"}`
response. This is intentional and must not be used to distinguish an unknown
account.

### Refresh

`POST /api/refresh` — no access token; send the **refresh token** as the bearer
credential. Shares the 10 requests/minute auth budget. Unknown, revoked or
expired refresh tokens return `401`.

Success: `200`:

```json
{"access_token":"<new-access-token>","refresh_token":"<new-refresh-token>"}
```

### Revoke

`POST /api/revoke` (log out) — send the **refresh token** (not the access
token) as the bearer credential. Shares the 10 requests/minute auth budget.
Always `204 No Content`, including for a token that was already revoked or
never existed. The access token stays valid until it expires (up to one hour),
so the client should also discard it.

### Current user

`GET /api/users/me` — authenticated. Returns `200` with the signed-in user's
profile (same shape as the register response). Use it on app start when a
saved token exists. `401` if the token is invalid or the account no longer
exists.

### Change password

`POST /api/users/password` — authenticated.

Request:

```json
{"old_password":"old-password","new_password":"New-password!"}
```

Success: `200`, the updated user object (without tokens). The old password
must match; changing the password revokes all refresh tokens. `400` invalid
new password, `401` wrong old password, `404` user not found.

### Search users

`GET /api/users/search?q=ali` — authenticated, maximum 20 requests/minute.

`q` is trimmed and must be 1–25 characters. Success: `200`:

```json
{"UserList":[{"id":"uuid","name":"alice"}]}
```

The key is intentionally `UserList` (capital U/L), and search results never
include email addresses.

## Friends

All friend endpoints require authentication.

### List friends

`GET /api/friends` → `200`:

```json
{"id_list":[{"user_id":"uuid","name":"bob"}]}
```

### List pending requests

`GET /api/friends/requests` → `200`:

```json
{
  "pending_ids":{"request-uuid":{"user_id":"sender-uuid","name":"alice"}},
  "request_ids":{"request-uuid":{"user_id":"receiver-uuid","name":"bob"}}
}
```

`pending_ids` are incoming requests; `request_ids` are outgoing requests.

### Send request

`POST /api/friends/requests` — maximum 10 requests/minute.

Request: `{"to_id":"user-uuid"}`

Success: `200 {"req_id":"request-uuid"}`. `400` self-request or malformed
input; `404` unknown user; `409` pending request already exists or users are
already friends.

### Accept or deny an incoming request

`PUT /api/friends/requests/{request_id}` — maximum 30 requests/minute.

Request: `{"status":"confirm"}` or `{"status":"cancel"}`. Success is `201`
with an empty body; any other status value is `400`. Only the request recipient may perform this operation;
unknown requests return `404`.

### Cancel an outgoing request

`DELETE /api/friends/requests/{request_id}` — maximum 30 requests/minute.

Success: `204 No Content`.

## Groups

All group endpoints require authentication.

### Create group

`POST /api/chats/groups`

Request:

```json
{"group_name":"devs","description":"Team chat","max_mems":20}
```

`group_name` is 1–30 characters, `description` is at most 1000 characters,
and `max_mems` is 2–1000. Success: `201 {"chat_id":"group-uuid"}`. The
creator is automatically the leader and occupies one member slot. Group names
are case-insensitively unique (`409`).

### My groups

`GET /api/chats/groups` → `200`:

```json
{"groups":[{"id":"group-uuid","name":"devs","description":"Team chat",
  "max_members":20,"member_count":3,"role":"Leader"}]}
```

Every group the caller belongs to, sorted by name. `role` is the caller's
role: `Leader` or `member`.

### Group details

`GET /api/chats/groups/{group_id}` → `200`:

```json
{"id":"group-uuid","name":"devs","description":"Team chat","max_members":20,
 "member_count":3,"is_member":true,
 "members":[{"user_id":"uuid","name":"alice","role":"Leader"}]}
```

Any signed-in user can see a group's name, description and size (for example
before joining). `members` (leader first) is only included for members.
`400` invalid ID, `404` unknown group.

### Join group

`POST /api/chats/groups/{group_id}/members` → `201` with an empty body.

`400` invalid group ID, `404` group not found, `409` already a member or group
full.

### Leave group

`DELETE /api/chats/groups/{group_id}/members` → `204 No Content`.

The leader cannot leave (`403`); a non-member gets `400`.

## Messages

### Send a message

`POST /api/chats/message` — authenticated, maximum 120 requests/minute.

Request:

```json
{
  "msg":"Hello",
  "to_id":"user-or-group-uuid",
  "type":"private",
  "parent_id":"optional-message-uuid"
}
```

`msg` is trimmed and must be 1–500 Unicode characters. `type` is `private` or
`public`; private messages require friendship, and public messages require
group membership. `parent_id`, when present, must refer to a message in the
same chat. Success: `200 {"msg_id":"message-uuid"}`. The message is durable
before this response is returned.

`403` means the caller is not allowed to message that chat; validation errors
are `400`.

### Private history

`GET /api/chat/{otherUser_id}/messages[?since=<RFC3339>]`

Returns `200`:

```json
{
  "chat_id":"stable-user-uuid_user-uuid",
  "msgList":[
    {
      "message_id":"message-uuid",
      "message_info":{
        "Msg":{"msg":"Hello","to_id":"uuid","type":"private"},
        "FromID":"sender-uuid",
        "CreatedAt":"2026-01-01T00:00:00Z"
      },
      "created_at":"2026-01-01T00:00:00Z"
    }
  ]
}
```

The nested keys `Msg`, `FromID`, and `CreatedAt` are case-sensitive and are
part of the current wire format. `msgList` is always oldest-first. Without
`since`, it holds the 50 most recent messages. With `since`, it holds
messages strictly newer than the timestamp (up to 200). Invalid timestamps
return `400`. There is no pagination for older history yet. An empty
conversation returns `"msgList":[]`.

### Group history

`GET /api/chat/groups/{group_id}/messages` → the same response shape; `chat_id`
is the group UUID and `msgList` holds the 50 most recent messages,
oldest-first. `since` is not supported here. Only members may read (`403`).

### Mark a private message seen

`POST /api/chat/{otherUser_id}/messages` with `{"msg_id":"message-uuid"}`.
Success is `201` with an empty body. The message must belong to this private
chat; otherwise the endpoint returns `400`. The other user receives a `Seen`
event only if you are friends.

## WebSocket

Connect to `wss://api.ponypigeon.com/api/chats/ws` (or the equivalent `ws://`
local URL). The connection requires the same `Authorization: Bearer` header
used by REST. Tokens in `?token=` are rejected. Maximum 20 connection attempts
per minute.

Every WebSocket message is an envelope:

```json
{"event":"<event-name>","payload":{}}
```

Only one live connection is retained per user; a newer connection replaces the
older one. Clients should handle reconnects and reload missed history using the
REST endpoints. The server sends WebSocket text messages and periodic ping
frames, and closes connections that stop answering pings for 60 seconds.
A client message larger than 4096 bytes **closes the connection**. A failed
upgrade (no or bad token) returns `401` JSON instead of switching protocols.

### Server events

| Event | Payload |
|---|---|
| `msg` | `{"id":"message-uuid","msg":"Hello","parent_id":"...","from_id":"uuid","type":"private|public","group_id":"...","created_at":"..."}` — `group_id` is present only for `public` (group) messages; for private messages the chat is identified by `from_id`. `parent_id` is omitted when empty |
| `Typing` | Private: `{"from_id":"uuid"}`; group: `{"from_id":"uuid","group_id":"uuid"}` |
| `Seen` | `{"msg_id":"message-uuid","from_id":"uuid"}` |
| `AddFri` | `{"req_id":"request-uuid","from_id":"uuid"}` |
| `AcceptFri` | `{"req_id":"request-uuid","from_id":"uuid"}` |
| `DenyFri` | `{"req_id":"request-uuid","from_id":"uuid"}` |

`msg`, `AddFri`, `AcceptFri`, and `DenyFri` are generated by backend actions.
The event payloads for friend accept/deny follow the same shape, although the
corresponding REST action is the source of truth.

### Client events

Typing:

```json
{"event":"Typing","payload":{"to_id":"user-or-group-uuid","type":"private"}}
```

Use `type: "public"` for group typing; omit it for private typing. The server
forwards at most one typing event per chat every 3 seconds per connection and
drops the rest, so send it on the first keystroke and then at most every few
seconds while the user keeps typing. Seen:

```json
{"event":"Seen","payload":{"to_id":"user-uuid","msg_id":"message-uuid"}}
```

Typing and seen events from strangers or unauthorized group members are
silently dropped. REST message sending and REST seen updates remain the
authoritative operations.

## AI suggestions (optional, disabled in production)

This route exists only when the backend has a Gemini API key configured. It is
not part of the first release, so production returns `404`.

`GET /api/ai/suggestions` — authenticated, maximum 5 requests/minute.

Success is a streamed `text/plain; charset=utf-8` response. Each suggestion
chunk is emitted as one line. The frontend should consume the response as a
stream rather than expecting JSON. If AI is not configured, the route is not
registered and returns `404`.

## Frontend implementation notes

- On `401`, attempt one refresh using the current refresh token, atomically
  replace both tokens, then retry the original request once. If refresh fails,
  clear the session and show login.
- Treat `204` and `201` responses with no body as success; do not JSON-decode
  them.
- Handle `429` with backoff; read `X-RateLimit-Remaining` to slow down early.
  The backend does not currently send `Retry-After`.
- Refresh proactively shortly before the access token's `exp` claim instead of
  only after a `401`: refreshes share the 10/minute auth budget with logins.
- Open the WebSocket after authentication, but do not put credentials in the
  URL. Reconnect with backoff and reconcile history after reconnecting.

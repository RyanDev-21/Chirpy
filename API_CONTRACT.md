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

- Access tokens last 15 minutes. Refresh tokens last 30 days and are
  rotated on every successful refresh. Refresh shortly before the access
  token's `exp` (or when a request returns `401`) and retry once. Store the replacement refresh token;
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
Every authenticated route (and the WebSocket upgrade) also counts against a
shared budget of **300 requests/minute per user**, on top of any route limit
listed below. `/readyz` and `/healthz` allow 30 requests/minute per IP;
`/livez` is not limited.

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

Success: `202 {"status":"check_your_email"}`. A 6-digit code is emailed;
send it to **Verify email** to finish and sign in. The answer is the same if
the email already has an account (the owner gets a notice email instead), so
sign-up can't be used to find out who is registered.

`400` invalid input; `409` username taken (`the user name already exists`);
`503` the email could not be sent (retry; registering again resends the code).

### Verify email

`POST /api/auth/verify-email` with `{"email":"alice@example.com","code":"482913"}`
→ `200`, the same session response as **Login**.

Codes expire after 2 minutes and die after 5 wrong tries. Any failure is
`400 {"error":"invalid or expired code"}`.

### Resend verification code

`POST /api/auth/resend-verification` with `{"email":"…"}` → always
`202 {"status":"check_your_email"}`. At most one code per minute is sent;
nothing is sent for unknown or already verified emails.

The `/api/auth/*` routes share a budget of 10 requests/minute.

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
account. Accounts created with Google have no password and also get this `401`
here; they sign in with Google (or set a password with **Reset password**).

A correct password on an account whose email isn't verified yet returns
`403 {"error":"email not verified"}`: show the code screen and offer
**Resend verification code**.

All user objects include `"email_verified": true|false`.

### Reset password

1. `POST /api/auth/password-reset` with `{"email":"…"}` → always `202`; a code is
   emailed if the account exists.
2. `POST /api/auth/password-reset/confirm` with
   `{"email":"…","code":"123456","new_password":"…"}` → `204`.

The new password follows the register rules (`400` otherwise); wrong or
expired codes are `400`. A reset signs out every session and also counts as
email verification. It works for Google-only accounts, adding a password.

### Google sign-in

Use Android's Credential Manager "Sign in with Google" with the server's
**Web client ID** as `serverClientId`, then send the ID token:

`POST /api/auth/google` with `{"id_token":"…"}` → `200`, one of:

- the **Login** session response, for a returning user or when the Google
  email matches an existing account (it is linked; an unverified account with
  that email loses its password, since Google proved who owns the address);
- `{"status":"username_required","signup_token":"…"}` for a new user. Ask for a
  username, then:

`POST /api/auth/google/complete` with `{"signup_token":"…","name":"alice"}` →
`200` session. The signup token lasts 15 minutes and works once. `400` bad
username, `409` username taken, `401` expired/used token (start again).

Invalid tokens and Google accounts with an unverified email return `401`.

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
never existed. The access token stays valid until it expires (up to 15 minutes),
so the client should also discard it.

### Current user

`GET /api/users/me` — authenticated. Returns `200` with the signed-in user's
profile (same shape as the register response). Use it on app start when a
saved token exists. `401` if the token is invalid or the account no longer
exists.

```json
{"id":"uuid","name":"alice","email":"alice@example.com","email_verified":true,
 "bio":"hello","name_change_available_at":null,
 "created_at":"…","updated_at":"…","is_chirpy_red":false}
```

`name_change_available_at` is when the username may next be changed (`null`
when it can be changed now).

### Edit profile

`PATCH /api/users/me` with `{"name":"alice2"}`, `{"bio":"…"}` or both →
`200` with the same shape as **Current user**. A missing field stays as it
is. Maximum 10 requests/minute.

- `name`: same rules as sign-up (3–25 characters, no spaces; `400`), unique
  (`409 username taken`). It can be changed **once every 30 days**: earlier is
  `403` with the date in the message (also in `name_change_available_at`).
  Sending your current name changes nothing. Signing in is by email, so a new
  name doesn't affect login.
- `bio`: trimmed, at most 150 characters (`400`); can be changed any time,
  `""` clears it.

### View a profile

`GET /api/users/{user_id}` → `200 {"id":"uuid","name":"bob","bio":"…"}`.
No email. `404` for unknown users, and when either of you blocked the other.

### Change password

`POST /api/users/password` — authenticated.

Request:

```json
{"old_password":"old-password","new_password":"New-password!"}
```

Success: `200`, the updated user object (without tokens). The old password
must match; changing the password revokes all refresh tokens. `400` invalid
new password, `401` wrong old password, `409` the account signs in with Google
only and has no password (set one with **Password reset**), `404` user not
found. Maximum 5 requests/minute.

### Delete account

`DELETE /api/users/me` — authenticated. Body `{"password":"…"}`, or for an
account without a password `{"google_id_token":"<fresh token for the same Google account>"}`.
→ `204`. Wrong or missing confirmation is `401`.

Deletion is immediate and permanent: friendships, requests, memberships and
sessions go; messages the user sent stay in their chats without an author
(`FromID` becomes the zero UUID). For each group they led, the member who
joined earliest becomes leader; groups with no one else left are deleted.
Their live WebSocket is closed. Google Play requires apps with accounts to
offer this.

### Search users

`GET /api/users/search?q=ali` — authenticated, maximum 20 requests/minute.

`q` is trimmed and must be 1–25 characters. Success: `200`:

```json
{"UserList":[{"id":"uuid","name":"alice"}]}
```

The key is intentionally `UserList` (capital U/L), and search results never
include email addresses. People you blocked, and people who blocked you, are
left out.

### Block users

Blocking someone removes your friendship and any pending request between
you. Afterwards neither of you can send the other a friend request (so no
private messages either, which need a friendship), and neither appears in
the other's search. Private chat history is kept, and shared groups work as
before. They are not told about the block: if you were friends they get the
same `RemoveFri` event as for an unfriend, and their friend requests to you
fail as if your account didn't exist (`404`).

- `POST /api/users/blocks` with `{"user_id":"uuid"}` → `204` (also if
  already blocked). `400` yourself, `404` unknown user. Maximum 30
  block/unblock requests per minute.
- `DELETE /api/users/blocks/{user_id}` → `204` (also if not blocked).
  Unblocking doesn't restore the friendship; send a new request.
- `GET /api/users/blocks` →
  `200 {"blocked":[{"user_id":"uuid","name":"bob","created_at":"…"}]}`,
  newest first.

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
input; `404` unknown user (or one who blocked you); `409` pending request
already exists, users are already friends, or you blocked them (unblock
first).

### Accept or deny an incoming request

`PUT /api/friends/requests/{request_id}` — maximum 30 requests/minute.

Request: `{"status":"confirm"}` or `{"status":"cancel"}`. Success is `201`
with an empty body; any other status value is `400`. Only the request recipient may perform this operation;
unknown requests return `404`.

### Unfriend

`DELETE /api/friends/{user_id}` → `204`. Removes the friendship for both
users; the other user gets a `RemoveFri` WebSocket event. Chat history stays
readable, but new private messages need a new friend request. `404` if you
aren't friends.

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
are case-insensitively unique (`409`). Maximum 10 groups created per minute.

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

Members only: groups are private, so a non-member gets `404`, the same as an
unknown group. `members` lists the leader first. `400` invalid ID.

### Invite codes

Every group has an invite code like `K7M9-QX2P`: 8 characters from
`A–Z` and `2–9` without the look-alikes `0 O 1 I`. Joining a group requires
its code. Codes are case-insensitive and the dash and spaces are optional, so
`k7m9 qx2p` also works.

- `GET /api/chats/groups/{group_id}/invite` → `200 {"code":"K7M9-QX2P"}`.
  Members only (`404` otherwise).
- `POST /api/chats/groups/{group_id}/invite` → `200 {"code":"NEW2-CODE"}`.
  Leader only (`403` for other members): makes a new code, and the old code
  stops working immediately. Maximum 10 requests/minute.

### Preview an invite

`GET /api/chats/groups/invites/{code}` → `200`:

```json
{"id":"group-uuid","name":"devs","description":"Team chat","max_members":20,"member_count":3}
```

Use it to show "Join devs?" before joining. Unknown or malformed codes return
`404 {"error":"invalid invite code"}`. Shares the 20 requests/minute join
budget.

### Join with a code

`POST /api/chats/groups/join` with `{"code":"K7M9-QX2P"}` →
`201 {"chat_id":"group-uuid"}`.

`404` invalid code, `409` already a member or group full. Maximum 20
join/preview requests per minute.

There is no way to join by group ID.

### Leave group

`DELETE /api/chats/groups/{group_id}/members` → `204 No Content`.

The leader cannot leave (`403`): transfer leadership first, or delete the
group. A non-member gets `400`.

### Manage a group (leader only)

Members who aren't the leader get `403`; non-members get `404`. Maximum 30
requests/minute across these routes.

- `PATCH /api/chats/groups/{group_id}` with `{"group_name":"…"}`,
  `{"description":"…"}` or both → `200`
  `{"id","name","description","max_members","member_count"}`. A missing field
  stays as it is. Same rules as creating (`400`); a name another group has,
  ignoring case, is `409`.
- `DELETE /api/chats/groups/{group_id}/members/{user_id}` → `204`: removes
  that member. They lose access to the group and its history; they can come
  back only with an invite code (regenerate it to stop that). `400` for
  yourself or someone who isn't a member.
- `PUT /api/chats/groups/{group_id}/leader` with `{"user_id":"…"}` → `204`:
  makes another member the leader; you become a regular member (and may then
  leave). `400` for yourself or a non-member.
- `DELETE /api/chats/groups/{group_id}` → `204`: deletes the group with all
  its messages and reactions, for everyone.

Members are told over the WebSocket: `GroupUpdated` for changes (refetch
the group) and `GroupRemoved` for whoever was kicked, or for everyone when
the group is deleted.

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
      "created_at":"2026-01-01T00:00:00Z",
      "edited_at":null,
      "deleted":false,
      "reactions":[]
    }
  ]
}
```

The nested keys `Msg`, `FromID`, and `CreatedAt` are case-sensitive and are
part of the current wire format. Every message also has
`"reactions":[{"emoji":"❤️","count":2,"me":true}]` (an empty list when there
are none), ordered by which emoji was used first. `msgList` is always oldest-first; an empty
conversation returns `"msgList":[]`.

`edited_at` is the time of the last edit (`null` if never edited; show
"edited"). A message deleted for everyone stays in the list as a placeholder
with `"deleted":true`, an empty `msg` and no reactions: show "message
deleted", and keep replies pointing at it working.

Query parameters (use `since` or `before`, not both):

- none: the most recent page.
- `limit=1..100`: page size (default 50).
- `before=<message_id>`: the page just older than that message, for "load
  more" while scrolling up. Pass the `message_id` of the oldest message you
  have; an empty list means you reached the start. A message from another
  chat returns `400`.
- `since=<RFC3339>`: messages strictly newer than the timestamp (up to 200),
  for catching up after a reconnect. Invalid timestamps return `400`.

### Group history

`GET /api/chat/groups/{group_id}/messages` → the same response shape; `chat_id`
is the group UUID. Supports `limit` and `before` like private history;
`since` is not supported here (`400`). Only members may read (`403`).

### Reactions

Each person can have one reaction per message, in private chats and groups.

- `PUT /api/chat/messages/{msg_id}/reaction` with `{"emoji":"👍"}` → `204`.
  Sets your reaction, replacing any earlier one. For "tap the same emoji to
  remove it", call `DELETE` instead.
- `DELETE /api/chat/messages/{msg_id}/reaction` → `204` (also when you had
  none).
- `GET /api/chat/messages/{msg_id}/reactions` →
  `200 {"reactions":[{"user_id":"…","name":"bob","emoji":"❤️","created_at":"…"}]}`,
  oldest first.

`emoji` must be exactly one emoji: skin tones (👍🏽), combined emojis (👩‍💻),
flags (🇹🇭) and keycaps (1️⃣) are fine; text or two emojis are `400`. You can
react to messages in chats you can read: in private chats only while you are
friends (removing still works after unfriending, which otherwise is `403`),
in groups as a member. A message in a chat you can't read is `404`.
Maximum 60 reaction changes per minute.

The rest of the chat receives a `Reaction` WebSocket event. Deleted messages
can't be reacted to (`404`).

### Edit or delete a message

Only the sender can change a message (`403` otherwise), in private chats and
groups. Maximum 60 edits and deletes per minute.

- `PATCH /api/chat/messages/{msg_id}` with `{"msg":"new text"}` →
  `200 {"msg_id":"…","msg":"new text","edited_at":"…"}`. Same text rules as
  sending (1–500 characters, trimmed; `400`). Allowed for **15 minutes** after
  sending (`403` after that), and in private chats only while you are still
  friends (`403`).
- `DELETE /api/chat/messages/{msg_id}` → `204`. Deletes it for everyone, at
  any time (also after unfriending). The text and its reactions are removed;
  the message stays in history as a placeholder.

Deleted or unknown messages, and messages in chats you can't read, are `404`.
The rest of the chat receives a `MsgEdited` or `MsgDeleted` WebSocket event.
After a reconnect, reload the visible page of history: edits and deletes made
while you were offline are only in REST responses.

### Mark group messages read

`POST /api/chat/groups/{group_id}/messages` with `{"msg_id":"message-uuid"}` →
`201` with an empty body. Records the latest message you've read in that
group, for unread counts. Markers only move forward: sending an older id is
accepted but changes nothing. Members only (`403`); an id from another chat
is `400`.

### Conversation list

`GET /api/chats` → `200`:

```json
{"conversations":[
  {"type":"public","id":"group-uuid","name":"devs",
   "last_message":{"id":"msg-uuid","msg":"standup?","from_id":"uuid","created_at":"…"},
   "unread":2},
  {"type":"private","id":"other-user-uuid","name":"alice",
   "last_message":{"id":"…","msg":"hi","from_id":"…","created_at":"…","deleted":false},
   "unread":0},
  {"type":"public","id":"group-uuid","name":"quiet","last_message":null,"unread":0}
]}
```

Every private chat you have messages in (with anyone, friend or not) and
every group you belong to, most recent activity first; groups with no
messages come last, by name. `unread` counts messages from others newer than
your read marker: for private chats that's the last message you marked
**seen**, for groups the last one you marked **read**. Group messages from
before you joined don't count. `from_id` is the zero UUID when the sender
deleted their account. If the last message was deleted for everyone,
`last_message.deleted` is `true` and `msg` is `""`.

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
| `RemoveFri` | `{"from_id":"uuid"}`: that user removed you as a friend |
| `Reaction` | `{"msg_id":"…","from_id":"uuid","type":"private|public","group_id":"…","emoji":"❤️"}`: `emoji` is `""` when the reaction was removed; `group_id` only for group messages |
| `MsgEdited` | `{"msg_id":"…","from_id":"uuid","type":"private|public","group_id":"…","msg":"new text","edited_at":"…"}`; `group_id` only for group messages |
| `MsgDeleted` | `{"msg_id":"…","from_id":"uuid","type":"private|public","group_id":"…"}`: show the placeholder and drop its reactions |
| `GroupUpdated` | `{"group_id":"…","change":"info|leader|members","user_id":"…"}`: `info` = name/description changed; `leader` = `user_id` is the new leader; `members` = `user_id` was removed. Refetch the group |
| `GroupRemoved` | `{"group_id":"…","reason":"kicked|deleted"}`: you are no longer in that group; drop it from the list |

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

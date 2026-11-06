# Chirpy frontend API contract

This document describes the API currently implemented by the checked-out
backend. It is intended for a frontend client (web or native).

## Environments and conventions

**People have two names.** `name` is the **username**: a unique handle
(`a-z 0-9 _ .`), shown as `@name`, used to find and add people.
`display_name` is what to show in chats, lists and notifications; it isn't
unique, so profiles should show `@name` too. Every object describing a person
(friends, requests, search, group members, conversation list, reactions,
discovery, profiles, `/users/me`) has both. Search matches either one.

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

- Access tokens stop working early when the account's sessions end:
  after a password change or reset, and when the account is deleted
  (`401`; sign in again). Token times (`iat`, `exp`) have millisecond
  precision, e.g. `1767225600.123`.
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

## App updates

`GET /api/app/version` (public) →
`200 {"latest":"1.2.0","minimum":"1.0.0","download_url":"https://…","sha256":"…","notes":"…"}`.
The APK is downloaded from the website, so the app checks this at start:
below `minimum`, block with "please update"; below `latest`, offer the
update. Verify the downloaded file's SHA-256 against `sha256` before
installing. `latest` is `""` before the first release. Cached for 5 minutes.

## Health endpoints

| Method | Path | Auth | Success |
|---|---|---|---|
| GET | `/` | No | `200 {"name":"Chirpy API"}` |
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

Rules: `name` is the **username**: 3–25 characters of `a-z`, `0-9`, `_` and
`.`, stored lowercase (`Alice` becomes `alice`) and unique ignoring case; email
must be valid and is normalized to lowercase; password is 8–128 characters and must contain at
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
  "email":"alice@example.com",
  "token":"<access-token>", "refresh_token":"<refresh-token>"
}
```

Invalid email or password returns the same `401 {"error":"invalid credentials"}`
response. This is intentional and must not be used to distinguish an unknown
account. Accounts created with Google have no password and also get this `401`
here; they sign in with Google (or set a password with **Reset password**).

**Suspended accounts:** an account banned by an admin gets
`403 {"error":"account suspended"}` from login, refresh, Google sign-in and
email verification, and its existing tokens stop working (`401`). Show
"Your account is suspended" and stop retrying. Bans can be temporary; they
lift by themselves.

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
`503` means Google sign-in isn't configured on the server (offer email sign-in).

### Refresh

`POST /api/refresh` — no access token; send the **refresh token** as the bearer
credential. Shares the 10 requests/minute auth budget. Unknown, revoked or
expired refresh tokens return `401`.

Success: `200`:

```json
{"access_token":"<new-access-token>","refresh_token":"<new-refresh-token>"}
```

### Log out all devices

`POST /api/users/me/logout-all` (authenticated) → `204`. Ends every session
of the account: all refresh and access tokens stop working, open WebSockets
close and registered phones are removed (no more push until they sign in
again). Maximum 10 requests/minute.

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
 "bio":"hello","name_change_available_at":null,"avatar_url":null,
 "location":{"id":"th-bangkok","name":"Bangkok","country":"th"},
 "interests":["gaming","kpop"],"discoverable":false,"hide_last_seen":false,
 "created_at":"…","updated_at":"…"}
```

`name_change_available_at` is when the username may next be changed (`null`
when it can be changed now).

### Edit profile

`PATCH /api/users/me` with `{"name":"alice2"}`, `{"bio":"…"}` or both →
`200` with the same shape as **Current user**. A missing field stays as it
is. Maximum 10 requests/minute.

- `display_name`: what people see (1–50 characters; spaces and emoji are
  fine; no line breaks or invisible characters, `400`). Not unique, change it
  any time. New accounts start with their username as display name; ask for
  one right after sign-up.
- `name`: the username, same rules as sign-up (`a-z 0-9 _ .`, 3–25, stored
  lowercase; `400`), unique
  (`409 username taken`). It can be changed **once every 30 days**: earlier is
  `403` with the date in the message (also in `name_change_available_at`).
  Sending your current name changes nothing. Signing in is by email, so a new
  name doesn't affect login.
- `bio`: trimmed, at most 150 characters (`400`); can be changed any time,
  `""` clears it.
- `location`: a place `id` from **Discovery options** (`400` otherwise), or
  `""` to clear. A city, "other city in <country>", or a world region; no
  coordinates are ever sent or stored.
- `interests`: the full new list, up to 10 ids from **Discovery options**
  (`400` otherwise); `[]` clears it. Duplicates are ignored.
- `discoverable`: `true` to appear in other people's suggestions (off by
  default).

Everything is validated first: if one field is invalid, nothing changes.

Everywhere a person is listed (friends, friend requests, search, group
members, conversation list, discovery) they come with `avatar_url`, `null`
without a picture. It changes when the picture changes, so cache images by
URL.

### View a profile

`GET /api/users/{user_id}` →
`200 {"id":"uuid","name":"bob","bio":"…","avatar_url":"/api/users/uuid/avatar?v=…","location":{…},"interests":["…"]}`.
No email. `location` and `interests` are only shown to friends, or to
everyone if the user is discoverable (otherwise `null` and `[]`). `404` for
unknown users, and when either of you blocked the other.

### Profile picture

- `PUT /api/users/me/avatar` with the image file itself as the request body
  (JPEG, PNG or WebP; not multipart) → `200` with the same shape as
  **Current user**, including the new `avatar_url`. At most 5 MB (`413`) and
  40 megapixels; anything else is `400`. The server crops the centre square,
  scales it to 256×256 and stores it as JPEG; photo metadata (EXIF, GPS) is
  removed. Shrink large photos on the phone first (e.g. to 1024 px): uploads
  must finish within 15 seconds. Maximum 10 uploads/deletes per minute.
- `DELETE /api/users/me/avatar` → `204`; `avatar_url` becomes `null`.
- `GET /api/users/{user_id}/avatar?v=…`, i.e. `GET <avatar_url>` (authenticated, prefix the API host) → the JPEG.
  `avatar_url` changes with every upload, so cache it forever (the response
  says `Cache-Control: private, max-age=31536000, immutable`); `ETag` /
  `If-None-Match` → `304` also works. `404` without a picture or across a
  block. With Coil/Glide, add the `Authorization` header to image requests.

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

### Change email

1. `POST /api/users/me/email` with `{"email":"new@example.com","password":"…"}`
   (or `"google_id_token"` for accounts without a password) → `202
   {"status":"check_your_email"}`. A 6-digit code goes to the **new**
   address and expires in 2 minutes. `401` wrong password, `400` invalid
   address or your current one, `429` (with `Retry-After`) if a code was
   sent less than a minute ago. If the new address already has an account,
   the answer is the same but no code is sent (its owner gets a notice), so
   this can't be used to find out who is registered.
2. `POST /api/users/me/email/confirm` with `{"code":"123456"}` → `200` with
   the same body as login (new `token` and `refresh_token`). **Every other
   session ends**, and so does the token you called this with: switch to the
   new tokens, reconnect the WebSocket and register the push token again.
   The old address gets a notice. `400` wrong or expired code (5 wrong
   tries end it), `409` if the address was taken in the meantime.

### Delete account

`DELETE /api/users/me` — authenticated. Body `{"password":"…"}`, or for an
account without a password `{"google_id_token":"<fresh token for the same Google account>"}`.
→ `204`. Wrong or missing confirmation is `401`.

Deletion is immediate and permanent: friendships, requests, memberships and
sessions go; messages the user sent stay in their chats without an author
(`FromID` becomes the zero UUID). For each group they led, the member who
served longest (moderators first) becomes admin; groups with no one else left are deleted.
Their live WebSocket is closed. Google Play requires apps with accounts to
offer this.

### Search users

`GET /api/users/search?q=ali` — authenticated, maximum 20 requests/minute.

`q` is trimmed and must be 1–25 characters. Success: `200`:

```json
{"UserList":[{"id":"uuid","name":"alice","display_name":"Alice Wong","avatar_url":"/api/users/uuid/avatar?v=…"}]}
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

## Reporting a user

`POST /api/reports` with
`{"user_id":"…","reason":"harassment","note":"…","messages":["msg-id", …]}`
→ `201 {"report_id":"…"}`.

- `reason`: `spam`, `harassment`, `hate`, `violence`, `sexual`,
  `impersonation`, `scam` or `other`. `note` up to 1000 characters (optional).
- `messages` (optional, up to 20): messages **the user chooses** as evidence,
  from their private chat with that person or from a group they're in
  (`400` otherwise). They're copied when reported; admins see only these.
- Reporting the same person again while the report is open updates it.
  `400` for yourself or bad input, `404` unknown user. Maximum 10 reports
  per hour.
- Offer **Block** next to **Report** in the app; reporting doesn't block.

## Discovery ("people you may know")

### Discovery options

`GET /api/discover/options` → `200`:

```json
{"countries":[{"code":"th","name":"Thailand","places":[
   {"id":"th-bangkok","name":"Bangkok","country":"th"},
   {"id":"th","name":"Other city in Thailand","country":"th"}]}],
 "regions":[{"id":"europe","name":"Europe"}],
 "interests":[{"id":"gaming","name":"Video games","category":"Games"}],
 "max_interests":10}
```

Detailed cities for Asian countries; the rest of the world is picked by
region. Show interests grouped by `category`. The lists rarely change
(cached for a day).

### Suggestions

`GET /api/discover[?limit=1..50&page=0..9]` (default 20 per page) →
`200`:

```json
{"suggestions":[{"id":"uuid","name":"lin","avatar_url":null,
  "location":{"id":"th-bangkok","name":"Bangkok","country":"th"},
  "shared_interests":["gaming"],"mutual_friends":2}]}
```

Only users who turned on `discoverable`, and you must be discoverable too
(`403` otherwise: seeing others means being visible). Friends, anyone with a
pending request either way, and blocks either way are left out. People are
ranked by mutual friends, then shared interests, then same city (same
country counts a little); people with nothing in common don't appear. No
distances are shown. Maximum 20 requests/minute.

## Friends

All friend endpoints require authentication.

### List friends

`GET /api/friends` → `200`:

```json
{"id_list":[{"user_id":"uuid","name":"bob","display_name":"Bob","avatar_url":null}]}
```

### Online and last seen

`GET /api/friends/presence` → `200`:

```json
{"hide_last_seen":false,
 "friends":[{"user_id":"uuid","online":true,"last_seen_at":null,"hidden":false},
            {"user_id":"uuid","online":false,"last_seen_at":"2026-11-05T09:12:00Z","hidden":false},
            {"user_id":"uuid","online":false,"last_seen_at":null,"hidden":true}]}
```

Only friends are listed (strangers and blocked users see nothing). "Online"
means the app has a live WebSocket. `last_seen_at` is set only when the
friend is offline (it can be `null` for accounts that never connected).
Fetch this when the friends or chat list opens, then follow `Presence`
events. A disconnect is announced only after 5 seconds, so a quick reconnect
(network switch) doesn't flash "offline".

`hidden: true` means that friend hides their last seen, **or you hide
yours** (it works both ways, as in WhatsApp): show nothing then, neither
"online" nor a time.

Turn it on or off: `PUT /api/users/me/privacy` `{"hide_last_seen":true}` →
`200 {"hide_last_seen":true}` (`400` if not a boolean; 10/minute). Online
friends get a `Presence` event right away. After turning it **off**, refetch
`GET /api/friends/presence`, since you can see others again.

### List pending requests

`GET /api/friends/requests` → `200`:

```json
{
  "pending_ids":{"request-uuid":{"user_id":"sender-uuid","name":"alice","avatar_url":null}},
  "request_ids":{"request-uuid":{"user_id":"receiver-uuid","name":"bob","avatar_url":null}}
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

### Private and public groups

| | `private` (default) | `public` |
|---|---|---|
| Found in search | no | yes, by name |
| Joining | invite code, or being added | join button, invite code, or being added |
| Non-members see | nothing (`404`); a code previews name and size | name, description, picture, member count (not the members) |
| History after joining | from when you joined | from when you joined |

Group names don't have to be unique.

### Roles and permissions

Every group has one **admin** (its creator, or whoever it was handed to),
any number of **moderators**, and **members**. The admin picks each
moderator's permissions:

| Permission | Allows |
|---|---|
| `add_members` | add their own friends directly |
| `remove_members` | remove ordinary members (not the admin or other moderators) |
| `delete_messages` | delete ordinary members' messages |
| `edit_info` | change the name, description and picture |
| `manage_invite` | replace the invite code |

The admin can do all of that and also: make or unmake moderators and set
their permissions, remove moderators, switch public/private, hand the admin
role to someone else, and delete the group. Nobody can edit someone else's
message. A change of role takes effect immediately. Without the permission
the answer is `403`; non-members of a private group get `404`.

### Create group

`POST /api/chats/groups`

```json
{"group_name":"devs","description":"Team chat","max_mems":20,"visibility":"private"}
```

`group_name` is 1–30 characters, `description` at most 1000, `max_mems`
2–1000, `visibility` `private` (default) or `public`. → `201
{"chat_id":"group-uuid"}`. The creator is the admin and takes one member
slot. 10 groups/minute.

### My groups

`GET /api/chats/groups` → `200`:

```json
{"groups":[{"id":"group-uuid","name":"devs","description":"Team chat","max_members":20,
  "member_count":3,"visibility":"private","role":"moderator",
  "permissions":["add_members","delete_messages"],"avatar_url":null}]}
```

Sorted by name. `role` and `permissions` are yours (permissions are empty
unless you're a moderator): use them to show or hide the management actions.

### Group details

`GET /api/chats/groups/{group_id}` → `200`:

```json
{"id":"group-uuid","name":"devs","description":"Team chat","max_members":20,
 "member_count":3,"visibility":"private","is_member":true,
 "avatar_url":"/api/chats/groups/group-uuid/avatar?v=1762300000000",
 "members":[{"user_id":"uuid","name":"alice","display_name":"Alice","role":"admin","permissions":[],"avatar_url":null}]}
```

`members` (members only) lists the admin first, then moderators, then
members by join time. A public group answers non-members too, with
`is_member: false` and no `members`. A private one is `404` for them.

### Search public groups

`GET /api/chats/groups/search?q=go` → `200`:

```json
{"groups":[{"id":"group-uuid","name":"Go Learners","description":"…","max_members":50,
  "member_count":12,"visibility":"public","avatar_url":null,"is_member":false}]}
```

Public groups whose name contains `q` (2–30 characters, case-insensitive;
`%` and `_` are literal). Names starting with `q` come first, then bigger
groups. At most 20. `400` for a short or long `q`. 20 searches/minute.

### Join a public group

`POST /api/chats/groups/{group_id}/join` → `201 {"chat_id":"group-uuid"}`.
`404` for a private (or unknown) group, `409` already a member or full.
Shares the 20/minute join budget.

### Invite codes

Every group has an invite code like `K7M9-QX2P`: 8 characters from `A–Z`
and `2–9` without the look-alikes `0 O 1 I`. Case-insensitive; the dash and
spaces are optional (`k7m9 qx2p` works).

- `GET /api/chats/groups/{group_id}/invite` → `200 {"code":"K7M9-QX2P"}`.
  Members only (`404` otherwise).
- `POST /api/chats/groups/{group_id}/invite` → `200 {"code":"NEW2-CODE"}`
  (`manage_invite`): the old code stops working immediately. 10/minute.

### Preview an invite

`GET /api/chats/groups/invites/{code}` → `200`:

```json
{"id":"group-uuid","name":"devs","description":"Team chat","max_members":20,
 "member_count":3,"visibility":"private","avatar_url":null}
```

Show "Join devs?" before joining (`avatar_url` only for public groups).
Unknown or malformed codes: `404 {"error":"invalid invite code"}`. Shares
the 20/minute join budget.

### Join with a code

`POST /api/chats/groups/join` with `{"code":"K7M9-QX2P"}` →
`201 {"chat_id":"group-uuid"}`. `404` invalid code, `409` already a member
or full.

### Leave group

`DELETE /api/chats/groups/{group_id}/members` → `204`. `400` if you aren't a
member.

The admin may name who takes over:
`DELETE /api/chats/groups/{group_id}/members?successor={user_id}` (`400` if
that person isn't a member). Without it the longest-serving moderator takes
over, then the longest-serving member. The last one to leave deletes the
group with everything in it.

### Manage a group

30 requests/minute across these routes.

- `PATCH /api/chats/groups/{group_id}` (`edit_info`) with any of
  `{"group_name","description","visibility"}` → `200`
  `{"id","name","description","max_members","member_count","visibility","avatar_url"}`.
  Missing fields stay. `visibility` is admin only. Same rules as creating
  (`400`).
- `POST /api/chats/groups/{group_id}/members` (`add_members`) with
  `{"user_id":"…"}` → `201`: adds one of **your friends**, no code needed.
  `403` not your friend (blocking ends a friendship), `409` already in or
  full. They get `GroupAdded` and, if offline, a `group_added` push.
- `DELETE /api/chats/groups/{group_id}/members/{user_id}`
  (`remove_members`) → `204`. Moderators can only be removed by the admin,
  and the admin by nobody (`403`); `400` for yourself or a non-member. They
  lose access to the group and its history and can come back only with an
  invite code (replace it to stop that) or by joining a public group.
- `PUT /api/chats/groups/{group_id}/members/{user_id}/role` (admin) with
  `{"role":"moderator","permissions":["add_members","delete_messages"]}` or
  `{"role":"member"}` → `204`. Unknown roles or permissions, or your own
  role: `400`. Sending the permissions again replaces them.
- `PUT /api/chats/groups/{group_id}/admin` (admin) with `{"user_id":"…"}`
  → `204`: hands the admin role over; you become a member. `400` for
  yourself or a non-member.
- `DELETE /api/chats/groups/{group_id}` (admin) → `204`: deletes the group
  with all its messages, reactions and media, for everyone.
- Deleting someone else's message: the same
  `DELETE /api/chat/messages/{msg_id}` as for your own. The admin may delete
  anyone's; a moderator with `delete_messages` only ordinary members'.
  `MsgDeleted` then carries `by_id`.

Members are told over the WebSocket: `GroupUpdated` for changes (refetch
the group), `GroupRemoved` for whoever was removed (or everyone, when the
group is deleted), `GroupAdded` for whoever was added.

### Group picture

- `PUT /api/chats/groups/{group_id}/avatar` (`edit_info`): the body is the
  image itself (JPEG, PNG or WebP, at most 5 MB and 40 megapixels; any
  Content-Type) → `200 {"avatar_url":"/api/chats/groups/…/avatar?v=…"}`.
  Cropped to the centre square, 256×256, re-encoded as JPEG (metadata such
  as GPS is dropped). `400` not an image, `413` too large. 5/minute.
- `DELETE /api/chats/groups/{group_id}/avatar` (`edit_info`) → `204`.
- `GET <avatar_url>` (members; anyone for a public group) → the JPEG with
  `ETag` and a one-year private cache: the URL changes with every upload.
  `404` without a picture or for non-members of a private group.

`avatar_url` (or `null`) appears in My groups, Group details, search,
previews of public groups and the conversation list. Members get
`GroupUpdated` with `change: "info"` when it changes.

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
      "sender_suspended":false,
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

`sender_suspended` is `true` when the sender's account is suspended: `msg`
is `""` and media and reactions are empty; show "Message from a suspended
user". (Lifting the ban shows the message again.) `edited_at` is the time of
the last edit (`null` if never edited; show
"edited"). A message deleted for everyone stays in the list as a placeholder
with `"deleted":true`, an empty `msg` and no reactions: show "message
deleted", and keep replies pointing at it working.

Query parameters (use `since` or `before`, not both):

- none: the most recent page.
- `limit=1..100`: page size (default 30).
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
Members only see messages sent **since they joined** (also in the
conversation list); older ones answer `404` everywhere, as if they didn't
exist.

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

In groups of up to 50 members, moving your marker sends `GroupRead` to the
people who sent the last 50 messages up to it (and to your other devices),
so their "read by" ticks update live.

### Who read my group message

`GET /api/chat/messages/{msg_id}/read-by` → `200`:

```json
{"read_by":[{"user_id":"uuid","name":"annie","display_name":"Annie","avatar_url":null,"read_at":"…"}]}
```

Current members (not you) whose marker is at or past that message, in the
order they read. Only for **your own** messages (`403` otherwise) in groups
of up to 50 members (`400` for bigger groups and for private messages; use
`Seen` there). `404` if you aren't in that chat. Show ✓✓ when everyone else
in the group is in the list. Read markers are only sent when the app calls
the endpoint above, so mark the newest visible message as the chat scrolls.

### Conversation list

`GET /api/chats` → `200`:

```json
{"conversations":[
  {"type":"public","id":"group-uuid","name":"devs",
   "last_message":{"id":"msg-uuid","msg":"standup?","from_id":"uuid","created_at":"…"},
   "unread":2},
  {"type":"private","id":"other-user-uuid","name":"alice","avatar_url":"/api/users/uuid/avatar?v=…",
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
`last_message.deleted` is `true` and `msg` is `""`. If its sender is
suspended, `last_message.sender_suspended` is `true` and `msg` is `""`.

### Forward a message

`POST /api/chats/forward` with
`{"message_id":"…","to":[{"type":"private","id":"friend-uuid"},{"type":"public","id":"group-uuid"}]}`
→ `200 {"results":[{"type":"private","id":"…","msg_id":"new-uuid"},{"type":"public","id":"…","error":"you can only message friends"}]}`

- You must be able to see the original (your chat, or a group message sent
  since you joined; otherwise `404`). Deleted messages can't be forwarded.
- `to`: 1–5 chats (`400` otherwise). Each target follows the normal send
  rules (friends only, group members), so results are per target: `msg_id`
  on success, `error` when that one failed.
- Text and media are copied. **Media is copied on the server**: nothing is
  uploaded again, and each copy has its own lifetime in its new chat. It
  counts towards your daily upload quota. If the original media already
  expired on the server, that target's `error` says so: send it again as a
  normal upload from the copy on the phone.
- New messages carry `"forwarded": true` in history and in the `msg` event.
  Show a "Forwarded" label. The original sender is not revealed.
- 10 forwards per minute.

### Search messages

Search the phone's own copy first. Call the server only for messages older
than what the phone has, with `before` = the time of its oldest local
message in that scope.

`GET /api/chats/search?q=dinner&type=private&chat_id={user_id}&before=2026-11-01T10:00:00Z&limit=20` → `200`:

```json
{"results":[{"message_id":"…","type":"private","chat_id":"bob-uuid","from_id":"…",
  "msg":"Dinner at the Thai place?","created_at":"…"}],
 "next_before":"2026-10-30T18:21:07.123456Z","next_before_id":"…"}
```

- `q`: 3–100 characters (2 within one chat), case-insensitive. It matches
  **anywhere in the text**, so it works for Thai, Burmese, Chinese and
  other languages without spaces. `%` and `_` are literal.
- `type` + `chat_id` (optional, together): `private` with the other
  person's id, or `public` with a group id. Without them it searches all
  your chats.
- `before` (RFC3339, optional): only older messages. To fetch the next page,
  pass `next_before` and `next_before_id` back as `before` and `before_id`.
- `after` (RFC3339, optional): only messages from then on. With `before`
  it is a date range ("in March"); `after` must be earlier than `before`.
- `limit`: 1–50, default 20. Newest first. `chat_id` is the other person
  (private) or the group.
- Only **private chats and groups of up to 50 members** are indexed. A
  group's messages are searchable if the group had at most 50 members when
  they were sent. Results only include your chats, and group messages sent
  since you joined. Deleted messages and suspended senders' messages never
  match. Photos without text aren't searchable.
- `400` for a bad `q` or parameters. 20 searches/minute.

### Mark a private message seen

`POST /api/chat/{otherUser_id}/messages` with `{"msg_id":"message-uuid"}`.
Success is `201` with an empty body. The message must belong to this private
chat; otherwise the endpoint returns `400`. The other user receives a `Seen`
event only if you are friends.

## Photos, videos and files

Uploads go **straight from the phone to storage** (Cloudflare R2) with
short-lived signed links; the API only hands out links and tracks them.
If the server has no storage configured, these routes return `503`.

### Limits

| | Accepted types | Max size | Length |
|---|---|---|---|
| `image` | `image/jpeg`, `image/png`, `image/webp`, `image/heic` | 5 MB | |
| `video` | `video/mp4`, `video/quicktime`, `video/webm` | 100 MB | 3 min up to 16 MB, 10 min above |
| `file` | see below, with `file_name` | 100 MB | |
| `voice` | `audio/ogg`, `audio/opus`, `audio/webm` (Opus), `audio/mp4`, `audio/aac`, `audio/x-m4a` (AAC), `audio/mpeg` | 10 MB | up to 15 min, `duration_ms` required |

**How long the server keeps them** (phones keep their own copies):

| Size | Kept |
|---|---|
| up to 16 MB ("small") | 24 h after everyone in the chat downloaded it, 7 days at most |
| over 16 MB ("large") | **24 h after upload**, whatever happens |

Show the expiry (`expires_at`) for large uploads ("available until …"), and
download them automatically on Wi-Fi so recipients don't miss them.

Shrink before uploading: photos to ~1600 px, videos to 720p (a 720p minute
is ~10–15 MB, so most videos stay small); make the video thumbnail on the
phone. At most **300 MB per user per 24 h** (`429`), **10 per message**.
When storage is nearly full, new uploads get `507` for a while.

**Files** (`kind: "file"`) need `file_name`, and its extension must match
`content_type`:

| Extensions | Content type |
|---|---|
| pdf | `application/pdf` |
| doc, docx, xls, xlsx, ppt, pptx | the Microsoft Office types (`application/msword`, `application/vnd.openxmlformats-officedocument.wordprocessingml.document`, …) |
| odt, ods, odp | `application/vnd.oasis.opendocument.*` |
| rtf, txt, csv, md | `application/rtf`, `text/plain`, `text/csv`, `text/markdown` |
| epub | `application/epub+zip` |
| zip, 7z, rar | `application/zip`, `application/x-7z-compressed`, `application/vnd.rar` |
| mp3, m4a, ogg, opus, wav, aac | `audio/mpeg`, `audio/mp4`, `audio/ogg`, `audio/wav`, `audio/aac` |

**Voice messages** (`kind: "voice"`): record Opus (OGG/WebM) at ~24–32
kbit/s (about 200 KB a minute) or AAC. Send `duration_ms`, and optionally
`waveform`: up to 128 bar heights from 0 to 255, computed while recording.
Recipients get both on the attachment, so the bubble shows the length and
the waveform before the audio downloads. The push and preview say
`🎤 Voice message (0:12)`.

Anything else is `400`, in particular **apps and programs** (APK, EXE,
scripts): they are a common way to spread malware and nothing scans files.
The name is cleaned (no folders, control or text-direction characters, at
most 200 characters) and shown to recipients as `file_name`. Downloads save
under that name. Treat received files as untrusted: open them with other
apps, never execute them.

### Sending

1. `POST /api/media` with `{"kind":"image","content_type":"image/jpeg","size":123456}`
   (size in bytes, exact; optionally `"width"`, `"height"`, and for videos
   `"duration_ms"`; for files `"file_name":"report.pdf"`) → `201`:
   ```json
   {"media_id":"uuid","upload_url":"https://…","method":"PUT",
    "headers":{"Content-Type":"image/jpeg","If-None-Match":"*"},"expires_in":600}
   ```
2. `PUT` the file to `upload_url` within 10 minutes, sending **every header
   in `headers`** and exactly the declared size (storage refuses anything
   else). The link works once: it can't replace a file already uploaded.
3. Send the message with `"media":["<media_id>", …]` (text may be empty):
   `{"to_id":"…","type":"private","media":["…"]}`. Each id must be your own
   upload, actually uploaded, and not used before (`400` otherwise).
   Uploads not attached within an hour are deleted.

### Receiving

- History messages and the `msg` WebSocket event carry
  `"media":[{"id":"…","kind":"video","content_type":"video/mp4","size":123456,"expires_at":"…","expired":false,"duration_ms":42000,"width":1280,"height":720,"thumbnail":true}]`
  (`duration_ms`/`width`/`height` only if the sender's app sent them)
  (an empty list when there is none; omitted from the event).
- `GET /api/media/{media_id}` → `200 {"url":"https://…","content_type":"…","size":…,"expires_in":300}`;
  download from `url` within 5 minutes (no auth header needed on that link;
  don't share it, anyone holding it can download until it expires). Browsers
  are told to save the file, not display it.
  Only the two people in the private chat, or group members, may (`404`
  otherwise).
- **Video thumbnails:** after uploading a video, the sending app extracts a
  still frame and sends it: `PUT /api/media/{media_id}/thumbnail` with the
  image as the body (JPEG, PNG or WebP, at most 2 MB) → `204`. The server
  shrinks it to at most 480 px, keeps the shape and removes metadata. Only
  for your own videos (`404`), once per video (`409`), 20 per minute.
  Recipients get it with `GET /api/media/{media_id}/thumbnail` → `200 {"url":…}`
  (same access rules as the video; it doesn't count as a download), so the
  chat can show the still, a ▶️ and the length before anyone downloads the
  video. It's deleted with the video.
- **Save the file on the phone.** The server keeps it only **24 hours after
  everyone in the chat has downloaded it**, and never longer than **7 days**
  (in groups over 50 members, just the 7 days). After that `GET` returns `410`
  and history shows `"expired":true`; show the phone's copy, or "expired".
- Deleting a message for everyone, deleting the group, or the sender deleting
  their account also deletes the media.
- Push for a message without text says `📷 Photo`, `🎥 Video`, `📷 3 photos`…

## Push notifications

Sent through Firebase Cloud Messaging to phones that **aren't connected**
over the WebSocket (when the app is open, the live events are enough).

### Register the phone

- After login (and whenever Firebase gives the app a new token):
  `POST /api/devices` with `{"token":"<FCM registration token>","platform":"android"}`
  → `204`. `platform` is `android` (default), `ios` or `web` (a browser's FCM web-push token). The token goes in the
  body, never the URL. A phone that signs in as another user moves to that
  user.
- On logout: `DELETE /api/devices` with `{"token":"…"}` → `204`.
- A password change or reset removes all of the user's phones (register again
  after signing in). Tokens Firebase reports as invalid are removed.

### What is sent

| Kind (`data.type`) | Title | Body | `data` | Grouping (`tag`) |
|---|---|---|---|---|
| `private` | sender name | message text (≤120 chars) | `from_id`, `msg_id` | one per chat |
| `public` | group name | `sender: text` | `from_id`, `group_id`, `msg_id` | one per group |
| `group_added` | group name | `bob added you to the group` | `from_id`, `group_id` | one per group |
| `call` | (data-only, no visible notification) | | `from_id`, `call_id`, `from_name` | see Voice calls |
| `missed_call` | Missed call | caller's name | `from_id`, `call_id` | one per caller |
| `friend_request` | Friend request | `bob sent you a friend request` | `from_id` | |
| `friend_accept` | Friend request accepted | `bob accepted your friend request` | `from_id` | |
| `reaction` | reactor (`in <group>`) | `reacted ❤️ to your message` | `from_id`, `msg_id`, `group_id`? | one per message |

Android channel id: `messages`. Tapping should open the chat from `data`.

### Settings

- `GET /api/users/me/notifications` →
  `{"private":true,"groups":true,"friends":true,"reactions":true}` (all on by
  default); `PUT` the same object to change them → `200`.
- Mute a chat: `PUT /api/mutes` with
  `{"type":"private","id":"<other user>"}` or `{"type":"public","id":"<group>"}`,
  plus `"until":"<RFC3339>"` for a timed mute (8 hours, a week…) or nothing to
  mute until unmuted → `204`; a past `until` is `400`. Muting a person also
  silences their friend requests and reactions.
- `DELETE /api/mutes` with `{"type","id"}` → `204`.
- `GET /api/mutes` → `{"mutes":[{"type":"public","id":"…","until":null}]}`
  (active mutes only).

Maximum 30 changes per minute across these routes.

## Voice calls

1:1 voice calls between **friends** (blocking ends a friendship, so blocked
people can't call). The audio goes phone to phone over WebRTC (encrypted,
DTLS-SRTP); when a direct path fails it goes through Cloudflare's TURN relay,
still encrypted. The server never receives audio: it checks who may call
whom, rings the callee, passes the WebRTC signalling between the two devices
over the WebSocket, and keeps a call log. On Android use the WebRTC library
(`org.webrtc`) with audio only, and a `ConnectionService` / full-screen
notification for ringing.

### ICE servers

`GET /api/calls/ice` → `200`:

```json
{"ice_servers":[{"urls":["stun:stun.cloudflare.com:3478","turn:turn.cloudflare.com:3478?transport=udp","turns:turn.cloudflare.com:443?transport=tcp"],
  "username":"…","credential":"…"}],"relay":true,"expires_in":14400}
```

Pass `ice_servers` as `RTCConfiguration.iceServers`. Fetch it right before
each call (both caller and callee); the credentials are short-lived and must
not be stored. `relay` is `false` when only STUN is available (some
networks then can't connect). 20/minute.

### Signalling (WebSocket)

| Step | Who sends | Event and payload |
|---|---|---|
| 1. call | caller | `CallStart {"to_id":"friend-uuid"}` |
| 2. ringing | server → caller's device | `CallRinging {"call_id","to_id"}` |
| 2. incoming | server → every device of the callee | `CallIncoming {"call_id","from_id"}` (an offline phone gets the push below, then this event when it connects) |
| 3. answer | callee's device | `CallAccept {"call_id"}` |
| 4. accepted | server → caller's device | `CallAccepted {"call_id"}`; the callee's other devices get `CallEnded` with reason `answered_elsewhere` |
| 5. negotiate | either device | `CallSignal {"call_id","data":{…}}`: the caller sends the SDP offer, the callee the answer, both send ICE candidates. `data` is any JSON (at most 16 KB) and reaches the other device unchanged, as `CallSignal {"call_id","from_id","data"}` |
| 6. hang up | either | `CallEnd {"call_id"}` (or `CallDecline` while ringing) |
| 6. ended | server → both users | `CallEnded {"call_id","reason","duration_ms"}` |

Only the two devices in the call can signal (the callee's device that
answered, and the caller's device that called). `CallSignal` is only
accepted after `CallAccepted`.

`reason` is one of: `ended` (hung up after talking), `missed` (no answer in
45 seconds), `declined`, `cancelled` (the caller gave up while ringing),
`busy` (the callee is in another call), `disconnected` (a device was gone
for more than 30 seconds), `unfriended`, `answered_elsewhere`.

Refused actions answer `CallError {"error","call_id"?}` to that device only:
`not_friends`, `already_in_a_call`, `rate_limited` (10 calls/minute, 500
signals per side per call), `unknown_call` (not your call, or it ended),
`bad_request`, `unavailable`.

**Connection drops.** If a device in an active call loses its WebSocket, it
has 30 seconds to reconnect. The call moves to the reconnected device, and
both sides get `CallResumed {"call_id","peer_id"}`: restart ICE (a new offer
with `iceRestart`) if the audio stopped. The media keeps flowing directly
meanwhile when it can. Calls last at most 4 hours. Calls in progress when the
server restarts end (logged as `interrupted`).

### Push

An offline callee's phones get a **data-only, high-priority** push
(`data.type = "call"`, with `call_id`, `from_id`, `from_name`) that expires
after 45 seconds: show the ringing screen, connect the WebSocket, and wait
for `CallIncoming`. A missed call also sends a normal notification
(`missed_call`, title "Missed call", body the caller's name). Calls ignore
the notification settings, except that muting the person's chat also
silences their calls.

### Call log

`GET /api/calls?limit=30&before={call_id}` → `200`, newest first:

```json
{"calls":[{"id":"call-uuid","direction":"outgoing","state":"ended","reason":"ended",
  "peer":{"user_id":"uuid","name":"bob","display_name":"Bob","avatar_url":null},
  "started_at":"…","answered_at":"…","ended_at":"…","duration_ms":61234}]}
```

`state` is `ended` (it was answered), `missed`, `declined`, `cancelled`,
`busy` or `failed`; `ringing` or `active` for a call in progress. `peer`
fields are null if they deleted their account. `limit` is 1–100; `before`
must be a call from your log (`400`). Entries are kept 180 days. Show them
in the chat timeline too, by time.

## WebSocket

Connect to `wss://api.ponypigeon.com/api/chats/ws` (or the equivalent `ws://`
local URL). Maximum 20 connection attempts per minute.

- **Apps (Android, iOS)** send the same `Authorization: Bearer` header used by
  REST on the upgrade request.
- **Browsers (React Native Web)** can't set headers on a WebSocket. Get a
  ticket first: `POST /api/chats/ws/ticket` (with the Bearer header) →
  `200 {"ticket":"…","expires_in":30}`, then connect to
  `wss://api.ponypigeon.com/api/chats/ws?ticket=<ticket>`. A ticket works
  **once**, within 30 seconds, so ask for a new one on every (re)connect.
  Signing out everywhere makes earlier tickets useless.
- Tokens in `?token=` are always rejected (a reusable token must never sit
  in a URL).

The web app's origin (for example `https://chirpy.ponypigeon.com`) must be
in the server's `CORS_ALLOWED_ORIGINS`, for both REST and the WebSocket. Browsers
uploading photos and files straight to storage also need a CORS rule on the
R2 bucket (see DEPLOY.md).

Every WebSocket message is an envelope:

```json
{"event":"<event-name>","payload":{}}
```

A user can be connected from up to **5 devices** at once (phone, tablet…);
a 6th connection closes their oldest. **Every device receives every event**,
including your own actions from another device: `msg`, `MsgEdited`,
`MsgDeleted` and `Reaction` come back to the sender's devices too (the device
that acted can ignore events it already has, by id). Private `msg` events
carry `to_id`, so your other devices know which chat a message you sent
belongs to. Clients should handle reconnects and reload missed history using the
REST endpoints. The server sends WebSocket text messages and periodic ping
frames, and closes connections that stop answering pings for 60 seconds.
A client message larger than 24 KB **closes the connection**. **One
WebSocket frame can carry several events**, separated by newlines (`\n`),
when they are sent close together: split each frame on newlines and parse
every line as its own envelope. A failed
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
| `MsgDeleted` | `{"msg_id":"…","from_id":"uuid","type":"private|public","group_id":"…","by_id":"uuid"}`: show the placeholder and drop its reactions. `from_id` is the sender; `by_id` is present when a group's admin or a moderator removed it |
| `GroupUpdated` | `{"group_id":"…","change":"info|admin|role|members|joined","user_id":"…"}`: `info` = name, description, picture or visibility changed; `admin` = `user_id` is the new admin; `role` = `user_id` became a moderator or member, or got different permissions; `members` = `user_id` left or was removed; `joined` = `user_id` joined or was added. Refetch the group |
| `GroupRemoved` | `{"group_id":"…","reason":"kicked|deleted"}`: you are no longer in that group; drop it from the list |
| `GroupAdded` | `{"group_id":"…","by_id":"uuid"}`: `by_id` (the admin or a moderator) added you to a group; refetch the group list |
| `GroupRead` | `{"group_id":"…","user_id":"uuid","msg_id":"…"}`: `user_id` has read the group up to `msg_id` (and everything before it). Groups of up to 50 only |
| `Presence` | `{"user_id":"uuid","online":true}`, `{"user_id":"uuid","online":false,"last_seen_at":"…"}`, or `{"user_id":"uuid","online":false,"hidden":true}`: a friend came online, went offline, or hid their last seen. Ignore it while you hide yours |

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

## Admin API (admin page only)

Used by the built-in page at `https://api.ponypigeon.com/admin`; the app
doesn't need it. Admin rights are granted only with the `cmd/admin` CLI.
Admins see **metadata only, never message content or media**, and every
search, view and action is written to an audit log.

- `POST /api/admin/login` `{"email","password"}` → `202` and a 6-digit code
  is emailed (`401` for a wrong password or a non-admin, alike).
- `POST /api/admin/verify` `{"email","code"}` →
  `200 {"token":"…","expires_in":1800}`: an admin token (30 minutes) for the
  routes below. App tokens don't work here, and admin tokens don't work on
  the app API.
- `GET /api/admin/reports?status=open|resolved|dismissed`,
  `GET /api/admin/reports/{report_id}` (with the attached messages; logged),
  `POST /api/admin/reports/{report_id}/resolve` `{"status":"resolved"|"dismissed","note":"…"}`.
  Closed reports and their evidence are deleted after 180 days.
- `GET /api/admin/overview`, `GET /api/admin/users?q=&limit=&offset=`,
  `GET /api/admin/users/{user_id}`, `GET /api/admin/groups?q=`,
  `GET /api/admin/audit?limit=`.
- `POST /api/admin/users/{user_id}/ban` `{"reason":"…","until":"<RFC3339>"|null}`
  → `204` (`null`: permanent). Admins and yourself can't be banned (`400`).
  A ban signs the user out everywhere and blocks new sessions, hides them
  from search, discovery and profiles, removes them from groups with other
  members (an admin hands over to the longest-serving moderator, else member) and hides their past
  messages (`sender_suspended`). `DELETE` the same path to lift it (messages
  come back; groups don't).

## Frontend implementation notes

- On `401`, attempt one refresh using the current refresh token, atomically
  replace both tokens, then retry the original request once. If refresh fails,
  clear the session and show login.
- Treat `204` and `201` responses with no body as success; do not JSON-decode
  them.
- Handle `429` with backoff; read `X-RateLimit-Remaining` to slow down early.
  Every `429` carries `Retry-After` (seconds) for when to try again.
- Refresh proactively shortly before the access token's `exp` claim instead of
  only after a `401`: refreshes share the 10/minute auth budget with logins.
- Open the WebSocket after authentication, but do not put credentials in the
  URL. Reconnect with backoff and reconcile history after reconnecting.

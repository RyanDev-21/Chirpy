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

### Which server to use

| Build | Base URL | WebSocket |
|---|---|---|
| **Release** (APK for users, web app) | `https://api.ponypigeon.com` | `wss://api.ponypigeon.com/api/chats/ws` |
| **Development** | a local backend: `http://<host>:8080` | `ws://<host>:8080/api/chats/ws` |
| Staging | *not set up yet*. Don't use production for testing instead (see below) | |

- **Never hard-code the URL.** Read it from build config, e.g.
  `EXPO_PUBLIC_API_URL` (and derive the WebSocket URL from it: `https` →
  `wss`, `http` → `ws`). Release builds must point at production; check a
  release build never ships with a development URL.
- **Don't develop or test against production.** Real users' data lives
  there, test accounts and messages would mix in, and rate limits and free
  quotas (emails, storage) are shared with them. Use production only for
  the final check of a release build.
- `https://chirpy-api.onrender.com` is the same production server under
  Render's own name; apps should use `api.ponypigeon.com`.

**`<host>` for a development build:**

| Running the app on | `<host>` |
|---|---|
| iOS simulator, or Expo web in a browser on the same computer | `localhost` |
| Android emulator | `10.0.2.2` (the emulator's name for the computer) |
| A real phone (Expo Go or a dev build) | the computer's **Wi-Fi IP**, e.g. `192.168.1.20`. Phone and computer on the same Wi-Fi. Find it with `ipconfig getifaddr en0` (Mac). |

Android blocks plain `http://` and `ws://` by default: allow cleartext
traffic **in development builds only** (Expo: `android.usesCleartextTraffic`
in the dev app config). Release builds use HTTPS/WSS and must not allow it.

**Running the local backend** (from the backend repo; needs Docker and Go):

```sh
cp .env.example .env    # then in .env: PLATFORM=dev, SECRET=<32+ random chars>,
                        # CORS_ALLOWED_ORIGINS=http://localhost:8081  (Expo web; add others comma-separated)
make deps               # starts Postgres + Redis in Docker
make run                # API on :8080 (listens on all interfaces, so phones on the Wi-Fi can reach it)
curl http://localhost:8080/livez   # 204 = running
```

What's different on a local backend:

| Feature | Local behaviour |
|---|---|
| Email codes (sign-up, reset, change email) | **Not sent.** With `PLATFORM=dev` the server prints each email, code included, to its log (`email (not sent, PLATFORM=dev)`). Copy the code from there. |
| Google sign-in | Works if `.env` has `GOOGLE_CLIENT_IDS` (the same Web client id) and the Google account is a **test user** in Google Auth Platform |
| Photos, videos, files, voice | `503 media is not configured` unless `.env` has the `R2_*` settings (use a separate test bucket, never `chirpy-media`) |
| Push notifications | Off unless `.env` has `FIREBASE_SERVICE_ACCOUNT` |
| Calls | Signalling works; `GET /api/calls/ice` returns STUN only (`"relay":false`) unless the `CF_TURN_*` keys are set. Fine on one Wi-Fi |
| Rate limits, rules, responses | Exactly as in production |

The database starts empty: create test accounts with sign-up (codes from the
log). `make deps` keeps the data between runs; `docker compose down -v`
wipes it.

### Conventions

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
`200 {"id":"uuid","name":"bob","display_name":"Bob","bio":"…","avatar_url":"/api/users/uuid/avatar?v=…","location":{…},"interests":["…"],"mutual_friends":{"count":4,"people":[{"user_id":"uuid","name":"carol","display_name":"Carol","avatar_url":null}]}}`.
No email. `location` and `interests` are only shown to friends, or to
everyone if the user is discoverable (otherwise `null` and `[]`). `404` for
unknown users, and when either of you blocked the other.

`mutual_friends` is the friends you and this person share: `count` is how
many, and `people` the first 3 by username (show "Carol, David and 2 others").
Suspended accounts don't count. With nothing in common it's
`{"count":0,"people":[]}`; on your own profile it's left out.

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
  `200 {"blocked":[{"user_id":"uuid","name":"bob","display_name":"Bob","avatar_url":"/api/users/uuid/avatar?v=…","created_at":"…"}]}`,
  newest first. `display_name` falls back to `name`; `avatar_url` is `null`
  without a picture.

## Characters and animated messages

Every user can have a low-poly **character** (pigeons in v1). Animated
messages and pigeon reactions play an animation **with that person's
character**. The app ships all models, textures and animations; the server
only stores which pieces someone picked (about 200 bytes) and which
animation a message or reaction plays.

### The catalog (shared by the app and the server)

The list of valid pieces lives on the server, and the app ships the matching
assets. The frontend owns the content: new entries are sent to the backend
and go into the server's catalog **before** the app version that has them is
released.

`GET /api/characters/catalog` → the catalog (cache it by `version`; the
answer may be cached for an hour):

```json
{"version":1,
 "bodies":["pigeon_round","pigeon_tall"],
 "slots":{"crest":["tuft_01","tuft_02"],"eyes":["round_01","sleepy_01","happy_01"],"hat":["cap_01","beanie_01","crown_01"]},
 "palette":["#8A94A6","#5AA9F2","#F2A65A","#7BD389","#F25A7B","#FFFFFF","#2B2B2B","#B48CF2"],
 "colors":["body","wings","hat"],
 "animations":[{"id":"wave_01","fallback":"👋 waved"},{"id":"sus_01","fallback":"🤨 side-eyed"}, "…"]}
```

- `slots`: each optional part and its choices (`null` = none). The app
  always sets `eyes`; `crest` and `hat` may be `null`.
- `palette`: colors are picked **by index** into this list, for these
  `colors` targets.
- **Only ever added to:** ids, palette entries and color targets are never
  reordered, removed or reused, so old characters and messages keep working.
- `animations[].fallback`: a short sentence, emoji + verb ("👋 waved"). It's
  shown in push notifications and by app versions that don't have the
  animation, and it's the **screen-reader label** ("Maya: 👋 waved").
- **Pieces an older app doesn't have:** draw the default body (the first in
  `bodies`) for an unknown body, leave out an unknown part, use color 0 for
  an unknown index, and show the `fallback` for an unknown animation.
  Nothing fails. The server accepts saves only against its own catalog, and
  the app only offers pieces it has assets for.

### Your character

`PUT /api/users/me/character` with

```json
{"v":1,"body":"pigeon_round","parts":{"crest":"tuft_02","eyes":"round_01","hat":null},
 "colors":{"body":0,"wings":5,"hat":2}}
```

→ `200 {"character":{…},"character_v":"…"}`.
- `400` for an unknown body or part, a part in the wrong slot, a color index
  outside `palette`, an unknown color target, unknown fields, or `v` other
  than 1.
- Missing slots and colors mean "none" and "color 0".
- `character` comes back in a normal form (keys sorted).
- `character_v` changes whenever the character does, so cache the rendered
  character by it.
- 10 changes a minute.

**Where it appears:** `"character":{…}` and `"character_v"` wherever a
person is shown:
- `GET /api/users/me` and `GET /api/users/{id}`;
- friends and friend requests (`GET /api/friends`, `/requests`);
- group members (`GET /api/chats/groups/{id}`);
- private chats in `GET /api/chats`;
- story and note authors.

Both are `null` for people without one. It isn't repeated in messages:
use the sender's character from those lists (cached by `character_v`).

**Onboarding (in the app):** the character isn't forced. After the welcome
tour the app asks once ("Make your pigeon?": make it / maybe later), and
Settings → Your pigeon is always there. `character: null` is a normal state.

**Profile picture:** after saving, the app renders a snapshot and uploads it
with `PUT /api/users/me/avatar` as a normal picture, unless the user chose a
photo (the app remembers that). Push notifications, the website and older
apps show the snapshot.

### Animated messages

`POST /api/chats/message` takes `"anim":"wave_01"` (private chats and groups).
- `msg` may be empty with an `anim`, may be the animation's `fallback`, or
  may be real text: all three are fine. Apps written before `anim` existed
  send the fallback as `msg`, so an app that knows the animation treats
  `msg == fallback` as "no text".
- `400` for an id that isn't in the catalog's `animations`.
- A reply (`parent_id`) may carry an `anim` too.
- History has `message_info.Msg.anim`, and the `msg` event has `anim`.
- Play it with the **sender's** character. If the app doesn't have that
  animation, show the `fallback`.
- With no text (or the fallback as text), push notifications say the
  fallback: `👋 waved` (private), `Maya: 👋 waved` (groups).
- Edits change the text only. Forwarding doesn't copy the animation.

### Pigeon reactions

A reaction can carry an animation of the reactor's character: see
**Reactions** (`anim`).

### Later: user-made animations (not in v1)

An animation will be **data, not code**:
- **Poses:** a list of poses over time for the shared rig (`body`, `head`,
  `limb_left`, `limb_right`, `eyes`, `feet`; every body uses the same joints,
  so a wave works on any body).
- **Camera:** an optional track of `{t, angle, height, distance, zoom,
  ease}` keyframes. The camera always orbits the character and looks at it.
- **Nothing runs:** the app only plays it back. No sandbox, bytecode or
  shaders, and it plays the same on every phone.
- **The server checks it:**
  - total length (for example ≤ 8 s);
  - keyframe counts;
  - joint angles and camera values within ranges;
  - caps on camera speed and shake (motion sickness) and on fast color
    flashes (photosensitivity);
  - a size cap of a few KB.

The catalog's own animations will use the same format.

Steps:
1. Catalog animations only (v1).
2. "Remix": catalog moves, props, a caption and camera presets.
3. A pose editor that saves the same data.
4. Effects or scripting only if step 3 isn't enough, probably never.

More body types (other animals, maybe a stylised human) will use the same
rig and work with every existing animation.

## Notes

A short text shown to your friends for **24 hours** ("at the gym", "🎂
today!"). One note per person; a new one replaces the old.

- `PUT /api/users/me/note` `{"text":"…"}` →
  `200 {"text","created_at","expires_at"}`. 1–60 characters after trimming;
  emoji are fine, line breaks and invisible characters are `400`. 10/minute.
- `DELETE /api/users/me/note` → `204` (also if you have none).
- `GET /api/notes` →
  `200 {"notes":[{"user_id","name","display_name","avatar_url","text","created_at","expires_at"}]}`:
  yours first, then your friends', newest first. Only notes that haven't
  expired; not from suspended accounts. Strangers never see your note, and
  blocking someone (which ends the friendship) hides it from them.

Refetch on app start and when the friends screen opens. Online friends also
get a **`Note`** WebSocket event when you set or clear yours:
`{"user_id","text","expires_at"}`, with `text: ""` and `expires_at: null`
when it was removed.

**Replying to a note:** send a private message to its owner with
`"reply_to_note": true` (`POST /api/chats/message`).
- The server copies their **current note text** into the message, so nobody
  can fake what the note said. `note_text` sent by a client is ignored.
- `400` if they have no note right now, in groups, or together with
  `story_id`.
- History has `message_info.Msg.note_text`, and the `msg` event has
  `note_text`. It stays after the note expires.
- Works with any message: text, a single emoji (a reaction), or `anim` with
  an empty `msg` (a pigeon reaction).
- Push: `replied to your note: <text>`, `reacted ❤️ to your note`, or
  `reacted to your note with their pigeon`, with `data.note = "true"`.

## Stories

A **photo** shown to your friends for **24 hours**. Text or drawings are
part of the image (made on the phone). Only friends see stories: blocking
(which ends the friendship) or unfriending hides them at once.

**Post:** upload the photo as usual (`POST /api/media` with `kind: "image"`,
then `PUT` it), then `POST /api/stories` `{"media_id":"…"}` →
`201 {"id","created_at","expires_at","media":{…},"views":0}`.
- The photo must be your own upload, not used yet, and an image (`400`
  otherwise). It counts toward the daily upload quota.
- At most 30 live stories (`429`), and 30 posts per hour.
- `503` when photos are off on the server.

**List:** `GET /api/stories` →

```json
{"mine":[{"id","created_at","expires_at","media":{…},"views":3}],
 "friends":[{"user_id","name","display_name","avatar_url","all_seen":false,
   "stories":[{"id","created_at","expires_at","media":{…},"seen":false}]}]}
```

- `mine`: your live stories, oldest first, with how many friends saw each.
- `friends`: people with something you haven't seen first, then by their
  newest story. Each person's stories are oldest first. Muted people and
  suspended accounts are left out.
- `media` is the usual attachment. Fetch the photo with
  `GET /api/media/{id}` (friends only, while the story is live). `media` is
  `null` if the photo is already gone; skip that story.
- Refetch when the app opens and when the chat list is shown; there's no
  WebSocket event for stories in v1, and no push.

**Seen:** `POST /api/stories/{id}/view` → `204` when you open it (once is
enough; repeats are fine). Your own views aren't counted. `404` for stories
you can't see.

**Who saw it** (yours only): `GET /api/stories/{id}/viewers` →
`200 {"viewers":[{"user_id","name","display_name","avatar_url","viewed_at","reaction":{"emoji":"🤨","anim":"sus_01"}}]}`,
newest first. Viewers never see each other.
- `reaction` is the viewer's latest reply to this story, when it's a single
  emoji (`{"emoji":"😂"}`) or a pigeon animation with no text
  (`{"emoji","anim"}`, the emoji from the animation's fallback).
- It's left out when they didn't reply, or wrote something.

**Delete early:** `DELETE /api/stories/{id}` → `204` (yours only, else
`404`). The photo is removed too.

**Reply:** send a normal private message to the poster with
`"story_id":"…"` (`POST /api/chats/message`). It must be a live story of the
person you're writing to (`400` otherwise). The message keeps `story_id` in
history (`message_info.Msg.story_id`) and in the `msg` event, so show
"replied to your story". After 24 hours the story itself is gone; show
"story unavailable".
- Push: `replied to your story: <text>`, `reacted ❤️ to your story` (a
  single emoji), or `reacted to your story with their pigeon` (an `anim` with
  no text), with `data.story_id`. It's still `data.type: "private"`, so
  tapping opens the chat.

**Later:** video stories, up to 60 s (the phone trims to 15, 30, 45 or 60),
with `duration_ms` and a still. Not in v1.

**Mute someone's stories:** `PUT /api/stories/mutes/{user_id}` → `204`,
`DELETE` the same → `204`, `GET /api/stories/mutes` →
`200 {"muted":[{"user_id","name","display_name","avatar_url"}]}`. They
aren't told.

**Report a story:** use **Reporting a user** with the poster's `user_id`,
and say in the note that it's about a story. In v1 the photo itself isn't
attached to the report.

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
`200 {"hide_last_seen":true,"hide_streaks":false}` (send either field or
both; `400` if neither or not a boolean; 10/minute; the answer has both). Online
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
  "parent_id":"optional-message-uuid",
  "client_msg_id":"uuid-the-app-generates"
}
```

`msg` is trimmed and must be 1–500 Unicode characters. `type` is `private` or
`public`; private messages require friendship, and public messages require
group membership. `parent_id`, when present, must refer to a message in the
same chat. Success: `200 {"msg_id":"message-uuid"}`. The message is durable
before this response is returned.

`403` means the caller is not allowed to message that chat; validation errors
are `400`.

**Retries: always send `client_msg_id`.** Make a new UUID when the user
taps Send, store it with the pending message, and send **the same one** on
every retry (timeouts, lost connection, app restart). If the server already
saved that message, the retry answers `200` with the **same** `msg_id`
instead of sending it twice. Retries in flight at the same time also get
the same answer. The server stores ids in its database for **7 days**, so
this also works after the phone was offline for hours or the server
restarted. Keep the pending message and retry whenever you're back online.
After 7 days a retry would be a new message: stop retrying on your own and
ask the user ("Couldn't send. Retry?").

**Matching your own messages.** Wherever you get **your own** message back,
it carries the `client_msg_id` it was sent with: the `msg` event to your
devices, history (every page type) and `/api/chats/changes`. It's kept as
long as the message exists, so after any time offline: if a message of
yours in history has the `client_msg_id` of one still waiting on the phone,
that one was sent; mark it sent and drop the waiting copy (don't retry it).
Also use it to pair the live `msg` echo with its waiting bubble instead of
comparing text. Others never see it; it's left out for forwards and for
messages sent without one. The same `client_msg_id` from a
different user is their own message. A non-UUID is `400`. On a `5xx`, retry
with backoff (1 s, 2 s, 4 s, …), using the same `client_msg_id`.

### Private history

`GET /api/chat/{otherUser_id}/messages[?since=<RFC3339>]`

Returns `200`:

```json
{
  "chat_id":"stable-user-uuid_user-uuid",
  "peer_seen_id":"message-uuid",
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

`peer_seen_id` (private history only) is the newest message the other
person has marked **seen**, so the app can show "seen" after reopening a
chat (live changes still arrive as `Seen` events). It is left out when they
haven't seen anything yet, or when you aren't friends (the same rule as the
`Seen` event). It doesn't depend on which page you asked for.

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
is the group UUID. Supports `limit`, `before` and `since` (up to 200,
oldest first) like private history; there is no `peer_seen_id` (use
`read-by`). Only members may read (`403`).
Members only see messages sent **since they joined** (also in the
conversation list); older ones answer `404` everywhere, as if they didn't
exist.

### Reactions

Each person can have one reaction per message, in private chats and groups.

- `PUT /api/chat/messages/{msg_id}/reaction` with `{"emoji":"👍"}` → `204`.
  Sets your reaction, replacing any earlier one (plain or pigeon). For "tap
  the same emoji to remove it", call `DELETE` instead.
- **Pigeon reaction:** add `"anim"`: `{"emoji":"🤨","anim":"sus_01"}`. It's a
  reaction with an animation of your character attached. `anim` must be in
  the catalog's `animations` (`400` otherwise). `emoji` is still required:
  apps that don't know the animation show the plain emoji.
- `DELETE /api/chat/messages/{msg_id}/reaction` → `204` (also when you had
  none).
- `GET /api/chat/messages/{msg_id}/reactions` →
  `200 {"reactions":[{"user_id":"…","name":"bob","emoji":"❤️","created_at":"…"}]}`,
  oldest first. Pigeon reactions also have `"anim"`.

In messages (history, `/api/chats/changes`), `reactions` groups by emoji
**and** animation. A pigeon entry lists who reacted, so each person's
character can play it:
`"reactions":[{"emoji":"❤️","count":2,"me":true},{"emoji":"🤨","anim":"sus_01","count":1,"me":false,"user_ids":["…"]}]`.
The `Reaction` WebSocket event also carries `anim` when set. Push stays
`reacted 🤨 to your message`.

**Two-pigeon reactions** (✋ high five, 🤝 handshake) work with the same rules
once their animations are in the catalog: one person reacts with the
invite's `anim` (waiting), the other reacting with the same `anim` accepts,
and `hanging_01` means "left hanging". The app works it out from the two
reactions.

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
Edits, deletes and reactions made while you were offline aren't replayed
as events: fetch them with `GET /api/chats/changes` (see **Catching up after
being offline**).

### Catching up after being offline

When the app starts or the WebSocket reconnects, with `last_sync` the time
you last caught up (save the server's times, not the phone's clock):

1. `GET /api/chats`: for each chat whose `last_message` is newer than what
   you have, `GET …/messages?since=<newest created_at you have>` (up to 200,
   oldest first; repeat with the last `created_at` while you get 200).
2. `GET /api/chats/changes?since=<last_sync>`: messages you **already have**
   that were edited, deleted or reacted to since, across all your chats at
   once. Replace your copy of each.

```json
{"changes":[
  {"type":"private","chat_id":"other-user-uuid","message":{ …same shape as history…, "changed_at":"2026-11-13T09:00:00.123456Z"}},
  {"type":"public","chat_id":"group-uuid","message":{ … }}
 ],
 "more":false}
```

- Oldest change first, at most 200. With `"more":true`, ask again with
  `since` = the last change's `message.changed_at`.
- A deleted message comes back with `"deleted":true` and an empty `msg`:
  **remove its text and media from the phone**. The sender deleted it for
  everyone.
- `reactions` are the current ones (an empty list when the last one was
  removed); `edited_at` is set for edits.
- Only your chats: private chats with anyone (also after unfriending) and
  groups you're in, from when you joined. `since` is required (`400` if
  missing or not RFC 3339).
- Set `last_sync` to the newest `changed_at` (or `created_at`) you received,
  not the phone's clock.

Every history message also has `changed_at` (left out if it never changed),
and your own messages their `client_msg_id`.

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
   "last_message":{"id":"msg-uuid","msg":"standup?","from_id":"uuid","from_name":"Jon","created_at":"…"},
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
before you joined don't count. For groups, `last_message.from_name` is the
sender's display name (or username), for previews like "Jon: standup?"; it
is left out for private chats and when the sender deleted their account.
`from_id` is the zero UUID when the sender
deleted their account.

**Streaks** (private chats only): `"streak": 5` is how many days in a row you
and this friend both sent each other at least one message. Days run midnight
to midnight **Thailand time (UTC+7)** for everyone. `"streak_ends_soon":
true` means yesterday counted but today hasn't yet (show ⏳). Both are left
out when there's no streak, when it broke (a day was missed), or when either
of you turned streaks off (`PUT /api/users/me/privacy`
`{"hide_streaks":true}`; also in `GET /api/users/me`). Forwarded messages
don't count. Suggestion: show the 🔥 from 3 days. There are no streak push
notifications, on purpose. If the last message was deleted for everyone,
`last_message.deleted` is `true` and `msg` is `""`. If its sender is
suspended, `last_message.sender_suspended` is `true` and `msg` is `""`.

### Forward a message

`POST /api/chats/forward` with
`{"message_id":"…","to":[{"type":"private","id":"friend-uuid"},{"type":"public","id":"group-uuid"}]}`
→ `200 {"results":[{"type":"private","id":"…","msg_id":"new-uuid"},{"type":"public","id":"…","error":"you can only message friends"}]}`
(`msg_id` is left out when that target failed.)

- You must be able to see the original (your chat, or a group message sent
  since you joined; otherwise `404`). Deleted messages can't be forwarded.
- `to`: 1–5 chats (`400` otherwise). Each target follows the normal send
  rules (friends only, group members), so results are per target: `msg_id`
  on success, `error` when that one failed.
- Text and media are copied. **Media is copied on the server**: nothing is
  uploaded again, and each copy has its own lifetime in its new chat. It
  counts towards your daily upload quota (300 MB and 300 files). If the original media already
  expired on the server, that target's `error` says so: send it again as a
  normal upload from the copy on the phone.
- New messages carry `"forwarded": true` in history and in the `msg` event.
  Show a "Forwarded" label. The original sender is not revealed.
- 10 forwards per minute, and each forward also counts against the 120 messages/minute send limit.

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

Read markers only move forward: a late or repeated receipt for an older
message changes nothing, so retrying is always safe.



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
| `image` | `image/jpeg`, `image/png`, `image/webp`, `image/heic`, `image/gif` | 5 MB | |
| `video` | `video/mp4`, `video/quicktime`, `video/webm` | 100 MB | 3 min up to 16 MB, 10 min above |
| `file` | see below, with `file_name` | 100 MB | |
| `voice` | `audio/ogg`, `audio/opus`, `audio/webm` (Opus), `audio/mp4`, `audio/aac`, `audio/x-m4a` (AAC), `audio/mpeg` | 10 MB | up to 15 min, `duration_ms` required |

**How long the server keeps them** (phones keep their own copies):

| Size | Kept |
|---|---|
| up to 16 MB ("small") | 24 h after everyone in the chat downloaded it, 7 days at most |
| over 16 MB ("large") | **3 days after upload**, whatever happens |

Show the expiry (`expires_at`) for large uploads ("available until …"), and
download them automatically on Wi-Fi so recipients don't miss them.

Shrink before uploading: photos to ~1600 px, videos to 720p (a 720p minute
is ~10–15 MB, so most videos stay small); make the video thumbnail on the
phone. At most **300 MB and 300 files per user per 24 h** (`429`; forward copies count too), **10 per message**.
When storage is nearly full, new uploads get `507` for a while.

**GIFs** from the keyboard (Gboard's GIF search, for example): accept them
as image content in the text field (Android `commitContent` / rich content
insertion), then upload them as `kind: "image"`, `image/gif`, like a photo.
No GIF provider is involved, so there is nothing to sign up for and no data
goes to a third party. Optional: convert to MP4 on the phone (about 5x
smaller) and send as a muted video instead.

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
  (in groups over 50 members, just the 7 days); large ones (over 16 MB)
  **3 days after upload**. After that `GET` returns `410`
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
| `big_file` | sender name, or the group | `Video from Maya will be deleted in 3 hours` | `from_id`, `msg_id`, `media_id`, `group_id`? | one per file |

**Big files** (over 16 MB, deleted at a fixed time, see Photos, videos and
files): sending one is an ordinary `private` / `public` push ("🎥 Video").
- **Reminder** (`big_file`): on the **last day** (about 24 hours before it's
  deleted), each recipient who hasn't downloaded it yet gets one push on the
  `big-files` channel: `Video from Maya will be deleted in 23 hours`. Once
  per file and person; never the sender, nor anyone who downloaded it
  (`GET /api/media/{id}` counts). Groups over 50 members don't get
  reminders (downloads aren't tracked there), nor members who joined after
  it was sent.
- It follows the chat's setting (`private` / `groups` notifications and
  chat mutes). Like every push, only phones that aren't connected get it.

Android channel id: `messages`, or `big-files` for the big-file reminder
above (create it with high importance). Tapping should open the chat from
`data`.

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
| 2b. devices | server → caller's device | `CallDevice {"call_id","device"}` per ringing callee device, `CallDeviceGone` when one stops (see **Connecting while ringing**) |
| 4. accepted | server → caller's device | `CallAccepted {"call_id","device"}`; the callee's other devices get `CallEnded` with reason `answered_elsewhere` |
| 5. negotiate | either device | `CallSignal {"call_id","data":{…}}`: the caller sends the SDP offer, the callee the answer, both send ICE candidates. `data` is any JSON (at most 16 KB) and reaches the other device unchanged, as `CallSignal {"call_id","from_id","data"}` |
| 6. hang up | either | `CallEnd {"call_id"}` (or `CallDecline` while ringing) |
| 6. ended | server → both users | `CallEnded {"call_id","reason","duration_ms"}` |

After `CallAccepted`, only the two devices in the call can signal (the
callee's device that answered, and the caller's device that called).
While ringing, signals go between the caller's device and each ringing
callee device, relay-only (next section).

### Connecting while ringing

The WebRTC setup can happen while the phone rings, so audio starts the
moment the callee taps Accept, **without revealing either person's IP
address before the call is answered.** (ICE candidates of type `host` and
`srflx` contain the device's local and public IP: the internet provider,
the rough area, and that the phone is online. A caller who rings and hangs
up must not learn that.)

**1. Relay only before Accept.** Until `CallAccepted`, both sides use only
TURN relay addresses:
- Create the `RTCPeerConnection` with `iceTransportPolicy: "relay"` while
  ringing.
- The server enforces it. Before `CallAccepted` it passes only
  `{"type":"offer"|"answer","sdp":…}` and
  `{"type":"candidate","candidate":{"candidate","sdpMid","sdpMLineIndex"}}`.
  - It forwards a candidate only if it is `typ relay` (or the empty
    end-of-candidates one). Host and srflx candidates are dropped quietly.
  - In relay candidates it blanks the related address (`raddr 0.0.0.0 rport
    0`): on a relay candidate that field is the phone's public IP.
  - In SDPs it removes every `a=candidate` line that isn't `typ relay` and
    blanks the `c=`, `a=rtcp:` and `o=` addresses. These fields aren't
    needed to connect.
  - Anything else is `CallError` `bad_request`.
- If `GET /api/calls/ice` says `"relay": false` (no TURN), there is no
  early setup: negotiate after `CallAccepted`, as before.

**2. No audio before Accept.** Neither side attaches its microphone track
until `CallAccepted` / the user taps Accept (`replaceTrack` then). Both use
a normal `sendrecv` audio transceiver with no track while ringing, so
adding the mic needs no new offer/answer. No audio leaves either phone
before the call is answered. The server can't see media, so this is the
apps' rule.

**3. Every ringing device gets its own connection.** The callee may have
several devices ringing. Device ids are opaque strings for this call only.
- `CallDevice {"call_id","device"}` (to the caller), once per ringing
  device: the devices online when the call starts (sent right after
  `CallRinging`), and any that comes online later while it still rings
  (for example a phone woken by the push). At most 5 per call; never after
  `CallAccepted`.
- `CallDeviceGone {"call_id","device"}`: that device stopped ringing
  (disconnected). Close its connection.
- The caller makes **one `RTCPeerConnection` and offer per device** and
  sends it with `CallSignal {"call_id","device":"…","data":{…}}`. While
  ringing, `device` is required (`bad_request` without it). A signal for a
  device that's already gone is dropped.
- Each callee device receives only its own, as `CallSignal
  {"call_id","from_id","data"}`, and replies without `device`. The caller
  receives `CallSignal {"call_id","from_id","device","data"}`.
- The limits (500 signals, 16 KB each) apply per connection.

**4. On Accept.**
- `CallAccepted {"call_id","device"}`: the device that answered. The caller
  keeps that connection and closes the others. `device` is missing only if
  the answering device wasn't one of the announced ones (more than 5); then
  negotiate a new connection after Accept, as before.
- The other devices get `CallEnded` `answered_elsewhere`, and the server
  drops their later signals.
- Accept can arrive before ICE has finished: keep the connection and let it
  finish; audio starts when it connects.
- **Then go direct:** the caller switches to `iceTransportPolicy: "all"`
  (`setConfiguration`) and sends a new offer with `iceRestart: true`. If a
  direct path works, the call moves to it without a gap (lower delay, less
  relay use); if not, it stays on the relay.
- From `CallAccepted` on, signals pass unchanged between the caller and the
  answering device. Signals from that device to the caller still carry
  `device`.

**Unchanged:** who may call, ringing for 45 s, decline (from any device
ends the call), missed calls, the call log and caps.

`reason` is one of: `ended` (hung up after talking), `missed` (no answer in
45 seconds), `declined`, `cancelled` (the caller gave up while ringing),
`busy` (the callee is in another call), `disconnected` (a device was gone
for more than 30 seconds), `unfriended`, `answered_elsewhere`, `daily_limit`
(one of you used up today's call minutes, during the call or when
answering: show "Call ended: daily call limit reached").

Refused actions answer `CallError {"error","call_id"?}` to that device only:
`not_friends`, `already_in_a_call`, `rate_limited` (10 calls/minute, 500
signals per side per call), `unknown_call` (not your call, or it ended),
`bad_request`, `unavailable`, `daily_limit` (you used up today's call
minutes, 180 by default; they come back over the next 24 hours: show "You've
reached today's call limit").

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
- **Origin header:** the upgrade is accepted with no `Origin`, an `Origin`
  equal to the API's own address (what React Native on Android sends, so no
  workaround is needed), or a web origin listed in `CORS_ALLOWED_ORIGINS`.
  Anything else gets `403`.

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
belongs to. Events missed while disconnected aren't replayed: after a
reconnect, catch up as in **Catching up after being offline**. The server sends WebSocket text messages and periodic ping
frames, and closes connections that stop answering pings for 60 seconds.
A client message larger than 24 KB **closes the connection**. **One
WebSocket frame can carry several events**, separated by newlines (`\n`),
when they are sent close together: split each frame on newlines and parse
every line as its own envelope. A failed
upgrade (no or bad token) returns `401` JSON instead of switching protocols.

### Server events

| Event | Payload |
|---|---|
| `msg` | `{"id":"message-uuid","msg":"Hello","parent_id":"...","from_id":"uuid","type":"private|public","group_id":"...","created_at":"..."}` — `group_id` is present only for `public` (group) messages; for private messages the chat is identified by `from_id`. `parent_id` is omitted when empty. The copy sent to the sender's own devices also has `client_msg_id` (when it was sent with one) |
| `Typing` | Private: `{"from_id":"uuid"}`; group: `{"from_id":"uuid","group_id":"uuid"}` |
| `Seen` | `{"msg_id":"message-uuid","from_id":"uuid"}` |
| `AddFri` | `{"req_id":"request-uuid","from_id":"uuid"}` |
| `AcceptFri` | `{"req_id":"request-uuid","from_id":"uuid"}` |
| `DenyFri` | `{"req_id":"request-uuid","from_id":"uuid"}` |
| `RemoveFri` | `{"from_id":"uuid"}`: that user removed you as a friend |
| `Note` | `{"user_id","text","expires_at"}`: a friend set or cleared their note (`text: ""`, `expires_at: null` when cleared) |
| `Reaction` | `{"msg_id":"…","from_id":"uuid","type":"private|public","group_id":"…","emoji":"❤️"}`: `emoji` is `""` when the reaction was removed; `group_id` only for group messages; `anim` for a pigeon reaction |
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

- **Phone storage screen** (app only, nothing on the server): photos,
  videos and files pile up on the phone, because the phone's copy is the
  one that lasts. Offer "Manage storage": space used per chat, biggest
  files first, filters by type (videos, photos, files, voice). Users select
  and delete **from this phone only**; the messages stay in the chat. If
  the server still has a file (`expired: false`), it can be downloaded
  again. If it's expired, warn before deleting: "This can't be downloaded
  again."

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

# Recipe: Slack Web API (read)

Read access to a single Slack workspace from a coordinator or worker
pane: list the channels the token's user is a member of, read recent
messages from a channel, walk a thread, search messages by query,
resolve user ids to names, and find DM channel ids. Writes
(`chat.postMessage`, reactions, file uploads) are out of scope and
require user-write scopes that this recipe intentionally does not
mint.

Out of scope entirely:

- Workspace / org admin (`admin.*` methods): user provisioning,
  channel admin, retention, Enterprise Grid management. These need
  admin scopes and an Enterprise Grid plan, neither of which this
  recipe mints.
- Webhooks, slash commands, interactive components, Socket Mode,
  Events API. These are write / push paths, not part of the read
  loop.
- File content download. The recipe mentions the `url_private`
  field on file metadata but does not encode a download wrapper.
- Bot tokens (`xoxb-...`). A bot only sees channels it has been
  explicitly invited to; this recipe uses a user token so the
  coordinator's read surface tracks the human user's view of the
  workspace.

## Requirements

Operator-managed env var, sourced via `spore-with-secrets`:

- `SLACK_USER_TOKEN` -- a user OAuth token (`xoxp-...`) minted
  from a workspace-installed Slack app with the read scopes
  listed below.

Placement is layered. `spore-with-secrets` sources
`~/.config/spore/secrets.env` first, then
`~/.config/spore/<project>/secrets.env` (per-project wins on
collisions). Typical setup:

- A default `SLACK_USER_TOKEN` in the global file so every
  coordinator on the host can read the workspace.
- A per-project override only if a project targets a different
  workspace (each user token is bound to exactly one workspace).

Mode 0600 on each file, 0700 on each containing dir.

No `SLACK_WORKSPACE` env var. The workspace name is whatever the
token resolves to; the verify-auth call below reflects it back.

## Mint the token

A user OAuth token comes from installing a Slack app to the
workspace with user scopes. Create a new app for this recipe
rather than reusing one minted for another tool -- a dedicated
app keeps the audit log clean and lets you revoke this token
without breaking the other tool.

1. Open `https://api.slack.com/apps` and pick "Create New App" ->
   "From scratch". Name it after the host and purpose
   (`spore-coordinator-<hostname>-readonly`); pick the target
   workspace.
2. On the new app's page, open "OAuth & Permissions" in the left
   nav.
3. Under "User Token Scopes" (NOT "Bot Token Scopes" -- this
   recipe uses a user token), add the read scopes:
   - `channels:read`
   - `channels:history`
   - `groups:read`
   - `groups:history`
   - `im:read`
   - `im:history`
   - `mpim:read`
   - `mpim:history`
   - `users:read`
   - `search:read`

   Skip optional scopes (`users:read.email`, `files:read`) unless
   a workflow needs them; smaller scope set = smaller blast
   radius.
4. Scroll up to "OAuth Tokens for Your Workspace" and click
   "Install to Workspace". If the workspace requires admin
   approval for app installs, this submits a request -- a
   workspace admin approves from "Settings & administration" ->
   "Manage apps" -> "Approval queue" before the token mints.
5. Once installed, copy the "User OAuth Token" -- it starts with
   `xoxp-`. The "Bot User OAuth Token" (`xoxb-`) is not what this
   recipe uses.
6. Drop into the chosen secrets file as
   `SLACK_USER_TOKEN=xoxp-...`.

User OAuth tokens do not expire by default. There is no rotation
reminder; rotate manually on the cadence your team prefers and
revoke the old one from the same OAuth & Permissions page
("Revoke Tokens").

## Auth gotcha

Four traps, all easy to misread as the wrong thing:

- **Token prefix decoder.** Slack issues several token shapes
  that look superficially similar:
  - `xoxp-...` -- user OAuth token. This recipe.
  - `xoxb-...` -- bot OAuth token. Different scope semantics,
    not what this recipe uses.
  - `xapp-...` -- app-level token (Socket Mode, Events API).
    Not for Web API calls.
  - `xoxe.xoxp-...` -- refresh token. Used to mint new user
    tokens, not for API calls directly.

  If a token does not start with `xoxp-`, it is the wrong kind
  for this recipe.

- **HTTP 200 on errors.** Slack returns HTTP 200 with
  `{"ok": false, "error": "..."}` on every logical failure
  (invalid token, missing scope, channel not found, rate limit).
  Do not trust the HTTP code -- always check `.ok` in the JSON.
  `curl -f` silently passes on these and would mask the failure;
  do not use it.

- **Bearer header, not query string.** Slack still accepts
  `?token=` in the URL for compatibility with older code, but
  that path leaks the token via access logs and proxies. Use
  `Authorization: Bearer $SLACK_USER_TOKEN` for every call. The
  recipe's examples all do.

- **`search.messages` requires a paid workspace.** Free
  workspaces return `{"ok": false, "error": "not_allowed_token_type"}`
  for `search.messages` even with `search:read`. Pro,
  Business+, and Enterprise Grid workspaces all support it. If
  the search call fails with that error, the workspace plan is
  the issue, not the scope set.

## Worked examples

All examples assume `spore-with-secrets` is on PATH (it is, via
the nix derivation) and `SLACK_USER_TOKEN` resolves.

### Verify auth

```
spore-with-secrets bash -c '
curl -sS -H "Authorization: Bearer $SLACK_USER_TOKEN" \
  "https://slack.com/api/auth.test" \
  | jq "{ok, team, team_id, user, user_id, url, error}"
'
```

`auth.test` is the canonical sanity check. On success, `ok` is
`true` and `team` / `url` name the workspace the token is bound
to. On failure (`{"ok": false, "error": "invalid_auth"}`), the
token is wrong, revoked, or the wrong kind -- see "Auth gotcha".

### List channels the user is in

```
spore-with-secrets bash -c '
curl -sS -G -H "Authorization: Bearer $SLACK_USER_TOKEN" \
  "https://slack.com/api/users.conversations" \
  --data-urlencode "types=public_channel,private_channel" \
  --data-urlencode "limit=200" \
  --data-urlencode "exclude_archived=true" \
  | jq "{ok, channels: [.channels[] | {id, name, is_private, num_members}]}"
'
```

`users.conversations` returns channels the token's user is a
member of. The `id` (starts with `C` for public/private channels,
`G` for legacy private groups, `D` for DMs) is what every other
method takes as the `channel=` parameter. The `name` is the
human-readable handle in the UI.

Pagination is cursor-based via `response_metadata.next_cursor`.
If the result has `next_cursor != ""`, pass it back as
`--data-urlencode "cursor=<value>"` to fetch the next page.

### Read recent messages from a channel

```
spore-with-secrets bash -c '
CHANNEL="$1"
curl -sS -G -H "Authorization: Bearer $SLACK_USER_TOKEN" \
  "https://slack.com/api/conversations.history" \
  --data-urlencode "channel=$CHANNEL" \
  --data-urlencode "limit=20" \
  | jq "{ok, messages: [.messages[] | {ts, user, text, thread_ts, reply_count}]}"
' _ C0123456789
```

`ts` is the message timestamp (e.g. `1717420000.001200`). It
doubles as the message id -- pass it to `conversations.replies`
as `ts=` to fetch a thread, or build a permalink with
`https://<team-domain>.slack.com/archives/<channel>/p<ts-with-dot-removed>`.

`thread_ts` on a message identifies the parent of a thread.
Top-level messages without replies have no `thread_ts`; thread
parents have `thread_ts == ts`; thread replies have
`thread_ts == <parent-ts>`.

### Read a thread

```
spore-with-secrets bash -c '
CHANNEL="$1"
THREAD_TS="$2"
curl -sS -G -H "Authorization: Bearer $SLACK_USER_TOKEN" \
  "https://slack.com/api/conversations.replies" \
  --data-urlencode "channel=$CHANNEL" \
  --data-urlencode "ts=$THREAD_TS" \
  --data-urlencode "limit=200" \
  | jq "{ok, messages: [.messages[] | {ts, user, text}]}"
' _ C0123456789 1717420000.001200
```

`conversations.replies` returns the parent plus every reply in
the thread, in chronological order. The first element of
`.messages[]` is the parent; the rest are replies.

### Search messages by query

```
spore-with-secrets bash -c '
QUERY="$1"
curl -sS -G -H "Authorization: Bearer $SLACK_USER_TOKEN" \
  "https://slack.com/api/search.messages" \
  --data-urlencode "query=$QUERY" \
  --data-urlencode "count=20" \
  --data-urlencode "sort=timestamp" \
  --data-urlencode "sort_dir=desc" \
  | jq "{ok, total: .messages.total, matches: [.messages.matches[] | {ts, user, text, channel: .channel.name, permalink}]}"
' _ "from:@alice has:link in:#engineering"
```

Slack search supports the same operator DSL as the UI:
`from:@user`, `in:#channel`, `has:link`, `has:pin`, `before:`,
`after:`, `during:`, plain free-text. Quote multi-word phrases.

The response wraps results in `.messages.matches[]` (not
`.messages[]` -- different shape from `conversations.history`).
`permalink` is the most useful field to hand back to the
operator since it deep-links to the message in the UI.

Requires a paid workspace -- see "Auth gotcha".

### Resolve a user id to display name

```
spore-with-secrets bash -c '
USER_ID="$1"
curl -sS -G -H "Authorization: Bearer $SLACK_USER_TOKEN" \
  "https://slack.com/api/users.info" \
  --data-urlencode "user=$USER_ID" \
  | jq "{ok, user: {id: .user.id, name: .user.name, real_name: .user.real_name, display_name: .user.profile.display_name, is_bot: .user.is_bot}}"
' _ U01ABCDEFGH
```

Message payloads carry the user as a `U`-prefixed id; this
resolves it to a name. For bulk lookups, `users.list` paginates
through every user in the workspace -- prefer caching its output
in a local jq map over hammering `users.info` per id.

### Find or open a DM channel id for a user

```
spore-with-secrets bash -c '
USER_ID="$1"
curl -sS -G -H "Authorization: Bearer $SLACK_USER_TOKEN" \
  "https://slack.com/api/conversations.open" \
  --data-urlencode "users=$USER_ID" \
  --data-urlencode "return_im=true" \
  | jq "{ok, channel_id: .channel.id}"
' _ U01ABCDEFGH
```

`conversations.open` is idempotent -- if a DM with that user
already exists, it returns the existing channel id; otherwise it
creates one. The returned `channel.id` (starts with `D`) is what
`conversations.history` takes for DM reads.

`return_im=true` keeps the response shape consistent regardless
of whether the DM was newly opened or already existed.

## Scope reference

Minimum scopes for the read loop:

- `channels:read` -- list public channels.
- `channels:history` -- read public channel messages.
- `groups:read` -- list private channels.
- `groups:history` -- read private channel messages.
- `im:read` -- list DMs.
- `im:history` -- read DM messages.
- `mpim:read` -- list group DMs.
- `mpim:history` -- read group DM messages.
- `users:read` -- resolve user ids to display names.
- `search:read` -- run `search.messages` (paid workspace only).

Optional, only useful for narrow workflows:

- `users:read.email` -- email addresses on `users.info` /
  `users.list`. Off by default; only enable if a workflow needs
  to cross-reference Slack identities with another system by
  email.
- `files:read` -- file metadata via `files.list` / `files.info`.
  Out of scope for the v1 read loop above; cheap to add later if
  needed.

Out of scope with the read-only set:

- Any write. `chat.postMessage`, `chat.update`, `chat.delete`,
  `reactions.add`, `pins.add`, file uploads all return
  `{"ok": false, "error": "missing_scope"}` (or similar) without
  the matching `*:write` scope.
- Admin endpoints. `admin.users.*`, `admin.conversations.*`,
  `admin.apps.*`, retention policy, workspace settings all need
  admin scopes plus an Enterprise Grid plan; intentionally not
  part of this recipe.

## Hygiene

- Never echo `$SLACK_USER_TOKEN` to a pane or log. Use
  length-and-prefix shape checks (`${#v}`, `${v:0:5}` -- a
  user token starts with `xoxp-`) for debugging.
- Rotate on whatever cadence your team prefers. User OAuth
  tokens have no built-in expiry; the app's audit log on the
  Slack side is the only trail.
- Revoke at any time from the app's "OAuth & Permissions" page
  ("Revoke Tokens"). Revocation is immediate. The next
  `auth.test` call returns `{"ok": false, "error":
  "token_revoked"}`.
- Removing the app entirely (via "Manage apps" in the workspace
  admin) also revokes every token it issued.

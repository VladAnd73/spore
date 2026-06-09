# Recipe: Slack Web API (read)

Read access to a single Slack workspace from a coordinator or worker
pane via a bot user OAuth token. The bot can only read channels it
has been explicitly `/invite`'d to, which gives a small, auditable
visibility set: "what does the coordinator see?" answers to "which
channels has the bot been invited to?". In-scope operations: audit
the bot's visibility, list channels, read channel history, walk
threads, resolve user ids, find messages within a known channel.

Writes (`chat.postMessage`, reactions, file uploads) are out of
scope and require user-write scopes that this recipe intentionally
does not mint.

Out of scope entirely:

- `search.messages`. Bot tokens cannot use the search method
  (`not_allowed_token_type`); search is a user-token-only surface
  by Slack design. The find-a-message workflow in this recipe
  uses `conversations.history` plus a `jq` filter on a known
  channel.
- DM and group-DM reads. A bot can only see DMs sent directly to
  the bot user, not arbitrary user-to-user DMs. The `im:*` and
  `mpim:*` scopes are not in the minimum set.
- Workspace / org admin (`admin.*` methods): user provisioning,
  channel admin, retention, Enterprise Grid management. These
  need admin scopes and an Enterprise Grid plan, neither of which
  this recipe mints.
- Webhooks, slash commands, interactive components, Socket Mode,
  Events API. Write / push paths, not part of the read loop.
- File content download. The recipe mentions the `url_private`
  field on inline message file attachments but does not encode a
  download wrapper.
- User OAuth tokens (`xoxp-...`). A user token would inherit the
  installing user's full visibility (every private channel they
  are in, every DM), which is much broader than the coordinator
  needs and has a correspondingly larger blast radius if leaked.
  Bot tokens give a smaller, auditable surface and are this
  recipe's choice.

## Requirements

Operator-managed env var, sourced via `spore-with-secrets`:

- `SLACK_BOT_TOKEN` -- a bot user OAuth token (`xoxb-...`) minted
  from a workspace-installed Slack app with the read scopes
  listed below.

Slack's own SDKs (Python `slack_sdk`, Node `@slack/web-api`)
consume the name `SLACK_BOT_TOKEN` by default. The recipe matches
that convention because the token is bot-scoped and read-only,
the blast radius of an accidental SDK pickup is small, and the
convention keeps the env var friendly to any later Python / Node
tool an operator might add.

Placement is layered. `spore-with-secrets` sources
`~/.config/spore/secrets.env` first, then
`~/.config/spore/<project>/secrets.env` (per-project wins on
collisions). Typical setup:

- A default `SLACK_BOT_TOKEN` in the global file so every
  coordinator on the host can read the workspace.
- A per-project override only if a project targets a different
  workspace (each bot token is bound to exactly one workspace).

Mode 0600 on each file, 0700 on each containing dir.

No `SLACK_WORKSPACE` env var. The workspace name is whatever the
token resolves to; the verify-auth call below reflects it back.

## Mint the token

A Slack app declares the scopes you want; installing the app to a
workspace mints a token that carries those scopes. The "Bot User
OAuth Token" (`xoxb-...`) is the credential every API call uses --
the app itself just exists so Slack knows what scopes the token
carries and which workspace it belongs to.

Create a new app for this recipe rather than reusing one minted
for another tool. A dedicated app keeps the audit log clean and
lets you revoke this token without breaking the other tool.

### Faster path: from a manifest

Slack's "From a manifest" install path takes a YAML blob and
preconfigures the app's name, description, bot user, and scope
set in one paste. Use this in preference to the per-scope click
path below.

1. Open `https://api.slack.com/apps` and pick "Create New App" ->
   "From a manifest". Select the target workspace.
2. Switch the manifest format to **YAML** and paste:

   ```yaml
   display_information:
     name: spore-coordinator
     description: Read-only Slack workspace access for a spore coordinator.
     long_description: >-
       Issues a bot user OAuth token (xoxb-) scoped to read public
       and private channels the bot is explicitly invited to via
       /invite @spore-coordinator. No DM access, no search, no
       write scopes, no admin scopes, no event subscriptions, no
       Socket Mode. Visibility set is auditable via
       users.conversations. See bootstrap/recipes/slack.md in the
       spore repo for the recipe this app pairs with.
   features:
     bot_user:
       display_name: spore-coordinator
       always_online: false
   oauth_config:
     scopes:
       bot:
         - channels:read
         - channels:history
         - groups:read
         - groups:history
         - users:read
   settings:
     org_deploy_enabled: false
     socket_mode_enabled: false
     token_rotation_enabled: false
   ```

   Edit `display_information.name` and `features.bot_user.display_name`
   together if you want the bot named per host
   (`spore-coordinator-<hostname>`) so the audit log distinguishes
   installs from multiple spore hosts. The 35-char cap on app
   names applies.

3. Review, then "Create". Slack lands you on the new app's page.
4. Open "OAuth & Permissions" in the left nav, scroll to "OAuth
   Tokens for Your Workspace", click "Install to Workspace". If
   the workspace requires admin approval for app installs, this
   submits a request -- a workspace admin approves from "Settings
   & administration" -> "Manage apps" -> "Approval queue" before
   the token mints.
5. Once installed, copy the "Bot User OAuth Token" -- it starts
   with `xoxb-`. The "User OAuth Token" (`xoxp-`) is NOT what
   this recipe uses and the manifest above does not mint one
   (no user scopes declared).
6. Drop into the chosen secrets file as
   `SLACK_BOT_TOKEN=xoxb-...`.

### Manual path: from scratch

Equivalent to the manifest path, useful when you want to read
every screen before committing.

1. Open `https://api.slack.com/apps` and pick "Create New App" ->
   "From scratch". Name it `spore-coordinator` (or per host); pick
   the target workspace.
2. Under "Features" -> "App Home" -> "Your App's Presence in
   Slack", enable the bot user. Set the display name to match
   the app name.
3. Under "Features" -> "OAuth & Permissions" -> "Bot Token Scopes"
   (NOT "User Token Scopes" -- this recipe uses a bot token), add
   the read scopes:
   - `channels:read`
   - `channels:history`
   - `groups:read`
   - `groups:history`
   - `users:read`

   Skip optional scopes (`users:read.email`, `im:*`, `mpim:*`,
   `files:read`) unless a workflow needs them; smaller scope set =
   smaller blast radius.
4. Scroll up to "OAuth Tokens for Your Workspace" and click
   "Install to Workspace". If the workspace requires admin
   approval for app installs, this submits a request -- a
   workspace admin approves from "Settings & administration" ->
   "Manage apps" -> "Approval queue" before the token mints.
5. Once installed, copy the "Bot User OAuth Token" -- it starts
   with `xoxb-`.
6. Drop into the chosen secrets file as
   `SLACK_BOT_TOKEN=xoxb-...`.

Bot OAuth tokens do not expire by default. There is no rotation
reminder; rotate manually on the cadence your team prefers and
revoke the old one from the same OAuth & Permissions page
("Revoke Tokens").

## Invite the bot to channels

The bot token's scope set is the **upper bound** of what the bot
CAN read. Channel-by-channel `/invite` controls what the bot
actually sees within that upper bound. A bot with `channels:history`
but invited to zero channels reads nothing; that is by design.

To grant access to a channel, a member of the channel runs:

```
/invite @spore-coordinator
```

To revoke access to a channel:

```
/remove @spore-coordinator
```

Substitute the actual bot display name if you renamed it during
mint. Private channels (`groups:*`) need the same invite step; a
member of the private channel runs `/invite` from inside it.

Audit the resulting visibility set any time with the
`users.conversations` worked example below. That call is the
source of truth for "what does the coordinator currently see?".

## Auth gotcha

Four traps, all easy to misread as the wrong thing:

- **Token prefix decoder.** Slack issues several token shapes
  that look superficially similar:
  - `xoxb-...` -- bot OAuth token. This recipe.
  - `xoxp-...` -- user OAuth token. Different visibility model
    (inherits the installing user's view); not what this recipe
    uses.
  - `xapp-...` -- app-level token (Socket Mode, Events API).
    Not for Web API calls.
  - `xoxe.xoxb-...` -- bot refresh token. Used to mint new bot
    tokens, not for API calls directly.

  If a token does not start with `xoxb-`, it is the wrong kind
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
  `Authorization: Bearer $SLACK_BOT_TOKEN` for every call. The
  recipe's examples all do.

- **`not_in_channel` means invite first.** Reading a channel the
  bot has not been invited to returns
  `{"ok": false, "error": "not_in_channel"}`. The fix is to
  `/invite @<bot-name>` in the channel from a member's account
  and retry -- this is the deliberate per-channel grant. Don't
  add `channels:join` to "fix" it; that lets the bot
  self-invite, which defeats the audit posture this recipe is
  built around.

## Worked examples

All examples assume `spore-with-secrets` is on PATH (it is, via
the nix derivation) and `SLACK_BOT_TOKEN` resolves.

### Verify auth

```
spore-with-secrets bash -c '
curl -sS -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
  "https://slack.com/api/auth.test" \
  | jq "{ok, team, team_id, user, user_id, bot_id, url, error}"
'
```

`auth.test` is the canonical sanity check. On success, `ok` is
`true`, `team` / `url` name the workspace, and `user` / `user_id`
are the bot user's handle and id. On failure
(`{"ok": false, "error": "invalid_auth"}`), the token is wrong,
revoked, or the wrong kind -- see "Auth gotcha".

### Visibility audit: list channels the bot is in

```
spore-with-secrets bash -c '
curl -sS -G -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
  "https://slack.com/api/users.conversations" \
  --data-urlencode "types=public_channel,private_channel" \
  --data-urlencode "limit=200" \
  --data-urlencode "exclude_archived=true" \
  | jq "{ok, channels: [.channels[] | {id, name, is_private, num_members}]}"
'
```

This is the source of truth for "what does the coordinator
currently read?". Run it after every `/invite` or `/remove` to
confirm the new visibility set. An empty `channels` array means
the bot has not been invited anywhere yet.

The `id` (starts with `C` for public, `G` for legacy private
groups) is what every other method takes as the `channel=`
parameter. The `name` is the human-readable handle in the UI.

Pagination is cursor-based via `response_metadata.next_cursor`.
If the result has `next_cursor != ""`, pass it back as
`--data-urlencode "cursor=<value>"` to fetch the next page.

### Read recent messages from a channel

```
spore-with-secrets bash -c '
CHANNEL="$1"
curl -sS -G -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
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

Returns `{"ok": false, "error": "not_in_channel"}` if the bot
has not been `/invite`'d to the channel -- see "Auth gotcha".

### Read a thread

```
spore-with-secrets bash -c '
CHANNEL="$1"
THREAD_TS="$2"
curl -sS -G -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
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

### Resolve a user id to display name

```
spore-with-secrets bash -c '
USER_ID="$1"
curl -sS -G -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
  "https://slack.com/api/users.info" \
  --data-urlencode "user=$USER_ID" \
  | jq "{ok, user: {id: .user.id, name: .user.name, real_name: .user.real_name, display_name: .user.profile.display_name, is_bot: .user.is_bot}}"
' _ U01ABCDEFGH
```

Message payloads carry the user as a `U`-prefixed id; this
resolves it to a name. For bulk lookups, `users.list` paginates
through every user in the workspace -- prefer caching its output
in a local jq map over hammering `users.info` per id.

### Find a message by query in a channel

`search.messages` is unavailable on bot tokens. The fallback for
"find a message that says X in channel Y" is walking
`conversations.history` and filtering with `jq`:

```
spore-with-secrets bash -c '
CHANNEL="$1"
NEEDLE="$2"
curl -sS -G -H "Authorization: Bearer $SLACK_BOT_TOKEN" \
  "https://slack.com/api/conversations.history" \
  --data-urlencode "channel=$CHANNEL" \
  --data-urlencode "limit=1000" \
  | jq --arg n "$NEEDLE" "{ok, matches: [.messages[] | select(.text | contains($n)) | {ts, user, text}]}"
' _ C0123456789 "deploy failed"
```

For longer history, paginate via `latest=` / `oldest=` cursors
(unix timestamps). The 1000-message page size is the Slack API
maximum per request.

If a workflow genuinely needs `search.messages` across the whole
workspace, that requires a separate user-token-backed recipe;
this recipe stays bot-only by design.

## Scope reference

Minimum bot scopes for the read loop:

- `channels:read` -- list public channels the bot is in.
- `channels:history` -- read public channel messages (the bot
  must be `/invite`'d to each channel; scope alone is not
  enough).
- `groups:read` -- list private channels the bot is in.
- `groups:history` -- read private channel messages (same
  per-channel invite requirement).
- `users:read` -- resolve user ids to display names.

Optional, only useful for narrow workflows:

- `users:read.email` -- email addresses on `users.info` /
  `users.list`. Off by default; only enable if a workflow needs
  to cross-reference Slack identities with another system by
  email.
- `im:read` / `im:history` -- DMs sent TO the bot user. Enable
  only for the "bot as operator inbox" pattern (operator DMs
  the bot; coordinator reads).
- `mpim:read` / `mpim:history` -- group DMs the bot has been
  added to. Same niche as `im:*`.
- `files:read` -- file metadata via `files.list` / `files.info`.
  Out of scope for the v1 read loop above; cheap to add later
  if needed.

Out of scope with the bot-token read set:

- `search.messages` (search:read scope is user-only).
- Any write. `chat.postMessage`, `chat.update`, `chat.delete`,
  `reactions.add`, `pins.add`, file uploads all return
  `{"ok": false, "error": "missing_scope"}` (or similar) without
  the matching `*:write` scope.
- Admin endpoints. `admin.users.*`, `admin.conversations.*`,
  `admin.apps.*`, retention policy, workspace settings all need
  admin scopes plus an Enterprise Grid plan; intentionally not
  part of this recipe.

## Hygiene

- Never echo `$SLACK_BOT_TOKEN` to a pane or log. Use
  length-and-prefix shape checks (`${#v}`, `${v:0:5}` -- a bot
  token starts with `xoxb-`) for debugging.
- Rotate on whatever cadence your team prefers. Bot OAuth
  tokens have no built-in expiry; the app's audit log on the
  Slack side is the only trail.
- Revoke at any time from the app's "OAuth & Permissions" page
  ("Revoke Tokens"). Revocation is immediate. The next
  `auth.test` call returns `{"ok": false, "error":
  "token_revoked"}`.
- Removing the app entirely from the workspace (via "Manage
  apps" -> the app -> "Remove app") also revokes the bot token
  and forgets every channel invite. Re-installing requires
  re-inviting the bot to every channel.

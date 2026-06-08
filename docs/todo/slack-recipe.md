**Status**: spec draft; recipe body not yet written. Open questions
below need operator answers before the recipe lands.

## Resolutions

- Token type: **user OAuth token** (`xoxp-...`). The coordinator's
  read surface matches whatever the human user behind the token
  can see (public channels, private channels they have joined,
  DMs, group DMs). Bot tokens (`xoxb-...`) are out -- a bot only
  sees channels it has been explicitly invited to, which would
  drift out of sync with operator expectations as new channels
  appear.
- Scope: **read-only**. No `chat:write`, no `reactions:write`, no
  admin / workspace-management surfaces. Matches the `jira` and
  `sentry` recipes' posture.
- Token placement: **layered, same as `GH_TOKEN` /
  `SENTRY_AUTH_TOKEN`**. Default in `~/.config/spore/secrets.env`
  so every coordinator on the host can read the workspace; allow
  per-project override at `~/.config/spore/<project>/secrets.env`
  where a project needs a different identity (e.g. a token minted
  in a different workspace).
- Recipe shape: **one combined `slack` recipe**. Channel reads,
  thread reads, search, and user lookup all share the same auth
  setup and fit comfortably under ~200 lines.



# `slack` recipe: scoped Slack read access from a spore pane

Jira: MT3-9450 -- "Further evolve M360 spore to give it read
access to Slack".

## Goal

Let any coordinator or worker pane pull the data needed to follow
an operator-referenced Slack thread or search for a message, with
the minimum privilege set the platform can express, and with a
recipe that documents the auth path, the gotchas, and worked
examples (same shape as `bootstrap/recipes/jira.md`,
`bootstrap/recipes/github.md`, `bootstrap/recipes/sentry.md`).

In-scope operations (v1):

1. List channels the token's user is a member of (public + private).
2. Read recent messages from a channel (`conversations.history`).
3. Read a thread by its parent message timestamp
   (`conversations.replies`).
4. Search messages across the workspace by query
   (`search.messages`).
5. Resolve user IDs to display names / real names
   (`users.info`, `users.list`).
6. Find or open a DM channel id for a user
   (`conversations.open` with `users=`).

Out of scope for v1:

- Any write: `chat.postMessage`, `chat.update`, `chat.delete`,
  `reactions.add`, `pins.add`, file uploads. Possible v2;
  read-only first matches the `jira` / `sentry` posture and keeps
  the token's blast radius small.
- Workspace / org admin surfaces (`admin.*` methods): user
  provisioning, channel admin, workspace settings, retention,
  enterprise grid management. These need an Enterprise Grid
  workspace plus admin scopes and are intentionally not part of
  this recipe.
- Slack Connect external-workspace surfaces beyond what the user
  token naturally sees.
- Webhooks, slash commands, interactive components, Socket Mode,
  Events API. These are write / push paths, not the read loop.
- File content download. File metadata via `files.list` /
  `files.info` is in-scope as a side effect of message reads; the
  recipe will mention the `url_private` field but not encode a
  download wrapper.

## Deliverables

1. `bootstrap/recipes/slack.md` -- the recipe body. Picked up
   automatically by the existing `//go:embed all:bootstrap/recipes`
   directive in `embed.go`; no Go code changes needed.
2. A small memory entry on Slack token env-var conventions
   (`project_slack_token_env_var.md`) mirroring
   `project_gh_token_env_var.md` and the implied Sentry entry:
   which env var name, which scope set, which vault.
3. No CLI changes, no flake changes. `curl` + `jq` are already on
   PATH everywhere this recipe will run.

No migration needed: existing vaults that already carry a Slack
token keep working; vaults that don't get a one-time operator-side
mint per the recipe.

Cross-instance portability is automatic. The recipe is a markdown
file under `bootstrap/recipes/`, embedded into every spore build
and shipped to every host that pulls the same flake input. The
moment this branch lands on upstream `main` and the operator bumps
the flake pin on a downstream host, `spore recipes show slack`
prints the new body there too. Identical to how `jira` and
`github` recipes propagate today.

## Open design questions

**Q1. Token env var name: `SLACK_USER_TOKEN`, `SLACK_TOKEN`, or
`SLACK_API_TOKEN`?**

Recommendation: **`SLACK_USER_TOKEN`**. Three reasons:

- Slack's own SDKs (`slack_sdk`, `@slack/web-api`) silently pick
  up `SLACK_BOT_TOKEN` or `SLACK_TOKEN` as defaults in some
  versions. Using a name those SDKs do not consume means a
  package we add later cannot accidentally inherit the token.
- The `_USER_` infix encodes which kind of token this is. Bot vs
  user vs app tokens look superficially similar (`xox?-`) but
  carry very different scope semantics; the env name should not
  paper over that.
- Matches the `GH_TOKEN` vs `GITHUB_TOKEN` reasoning in the
  `github` recipe -- pick the narrow name that tooling won't
  blindly grab.

Asking the operator only to confirm.

**Q2. `search.messages` requires a paid workspace.**

Slack's `search.messages` method requires the workspace to be on
a paid plan (Pro / Business+ / Enterprise Grid). Free workspaces
return `{"ok": false, "error": "not_allowed_token_type"}` or
similar.

Recommendation: **document the requirement and keep search in
v1**. The recipe will:

- List the paid-plan dependency under "Auth gotcha".
- Show `search.messages` in worked examples (assumes paid).
- Show the fallback for free workspaces: walk
  `conversations.history` on a known channel and filter
  client-side via `jq`.

Asking the operator to confirm the workspace plan so the recipe
can state the right default.

**Q3. Workspace identifier env var: yes or no?**

The user token is workspace-scoped, so API calls do not need a
workspace slug. But operators often want to know "which workspace
is this token pointing at?" without hitting the API.

Recommendation: **no separate env var**. The recipe's verify-auth
call (`auth.test`) returns the workspace name + URL on every
invocation, which is the source of truth. Adding a `SLACK_WORKSPACE`
variable would either duplicate that or risk diverging from it.

**Q4. Token install path: workspace-installed app or already-minted
token?**

A user token (`xoxp-...`) is issued by installing a Slack app to
the workspace with user scopes. Two ways to get one:

- (a) Create a new Slack app at `https://api.slack.com/apps`,
  declare user scopes, install to the workspace, copy the "User
  OAuth Token" from the OAuth & Permissions page.
- (b) If the operator already has a personal Slack app for
  another tool (e.g. a CLI like `slack-cli`), reuse it -- add
  the read scopes this recipe needs and reinstall.

Recommendation: **document (a) as the default, mention (b) as a
reuse path**. New app keeps audit logs clean; reuse keeps the app
list short. Operator choice.

**Q5. App install approval.**

Slack workspaces can require admin approval for app installs. If
the workspace has that policy on, the operator submits the app
for approval; an admin approves; then the token mints. One-time
cost per app.

Recommendation: **document the approval flow as a note**, with
the click path for an admin to approve from the workspace
settings. Nothing the recipe can do client-side.

## Recipe outline (for the writeup)

Following the `jira.md` / `github.md` / `sentry.md` shape:

1. **Header** -- one paragraph stating read-only scope, the
   user-token-vs-bot-token reasoning, and the explicit "no writes,
   no admin, no webhooks" exclusions.
2. **Requirements** -- `SLACK_USER_TOKEN` in the layered vault
   (global default + per-project override). Pointer to the memory
   entry.
3. **Mint the token** -- click path on
   `https://api.slack.com/apps`: create app, add user scopes
   (full set listed), install to workspace, copy the User OAuth
   Token. Admin-approval note if the workspace requires it.
4. **Auth gotcha** -- four points:
   - Token prefix decoder: `xoxb-` (bot), `xoxp-` (user), `xapp-`
     (app-level), `xoxe.xoxp-` (refresh). This recipe uses
     `xoxp-` and rejects the others.
   - Slack API returns HTTP 200 on logical errors; always check
     `.ok` in the JSON, not the HTTP code. A token failure looks
     like `{"ok": false, "error": "invalid_auth"}` with HTTP 200.
   - Use `Authorization: Bearer $SLACK_USER_TOKEN`, not the
     deprecated `?token=` query string (leaks via access logs).
   - `search.messages` requires a paid workspace.
5. **Worked examples**:
   - Verify auth (`auth.test`) -- returns workspace name, user
     id, user name; the canonical sanity check.
   - List channels the user is in (`users.conversations` filtered
     by `types=public_channel,private_channel`).
   - Read recent messages from a channel
     (`conversations.history` with `channel=`, `limit=20`).
   - Read a thread (`conversations.replies` with `channel=`,
     `ts=<parent-ts>`).
   - Search messages by query (`search.messages` with
     `query="from:@alice has:link"`). Note the paid-plan
     requirement.
   - Resolve a user id to display name (`users.info` with
     `user=Uxxxxx`).
   - Open / find a DM channel id for a user
     (`conversations.open` with `users=Uxxxxx`).
6. **Scope reference** -- minimum scopes for the read loop:
   - `channels:read` -- list public channels.
   - `channels:history` -- read public channel messages.
   - `groups:read` -- list private channels.
   - `groups:history` -- read private channel messages.
   - `im:read` -- list DMs.
   - `im:history` -- read DM messages.
   - `mpim:read` -- list group DMs.
   - `mpim:history` -- read group DM messages.
   - `users:read` -- resolve user ids.
   - `search:read` -- run `search.messages` (paid workspaces only).

   Optional, narrow uses:
   - `users:read.email` -- email addresses on user lookup. Off by
     default; only enable if a workflow needs to cross-reference
     with another system by email.
   - `files:read` -- file metadata via `files.list` / `files.info`.
     Out of scope for v1 but cheap to add if needed.
7. **Hygiene** -- never echo `$SLACK_USER_TOKEN`; rotation
   cadence; revocation URL (the app's OAuth & Permissions page,
   "Revoke Tokens").

## Validation plan

- `just check` green on the branch (markdown-only change; the
  `internal/recipes` test that walks `bootstrap/recipes/*.md` and
  checks the H1 will exercise the new file).
- `spore recipes ls` shows the new entry.
- `spore recipes show slack` prints the body.
- End-to-end smoke: once the operator mints the token and drops
  it into the vault, run each worked example from this pane
  against the operator's Slack workspace. Quote `auth.test`'s
  workspace name back to the operator as proof, plus one real
  channel id and one real user resolution.

## Operator owes (post-merge)

- Decide which workspace the recipe targets (one workspace per
  token; multi-workspace pulls take multiple tokens).
- Create the Slack app, declare the user scopes listed above,
  install to the workspace, copy the User OAuth Token.
- Drop into `~/.config/spore/secrets.env` as
  `SLACK_USER_TOKEN=xoxp-...`. Per-project override only if a
  project needs to target a different workspace.
- Approve the app install if workspace policy requires admin
  approval.

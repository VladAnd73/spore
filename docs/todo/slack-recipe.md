**Status**: spec drafted; recipe body landed; pivoted from user-token
to bot-token model after first review. Open questions below.

## Resolutions

- Token type: **bot OAuth token** (`xoxb-...`). Pivoted from the
  initial user-token (`xoxp-...`) draft after review. Rationale:
  a bot only sees channels it has been explicitly invited to,
  which gives a small auditable visibility set ("which channels
  is the coordinator reading?" = "which channels has the bot
  been invited to?"). A user token would inherit the human user's
  full visibility (every private channel they are in, every DM),
  which is much broader than the coordinator's actual need and
  has a correspondingly larger blast radius if the token leaks.
  This trade is the same shape as the `github` recipe choosing
  fine-grained PATs over classic PATs for per-repo scoping.
- Scope: **read-only**. No `chat:write`, no `reactions:write`,
  no admin / workspace-management surfaces. Matches the `jira`
  and `sentry` recipes' posture.
- Token placement: **layered, same as `GH_TOKEN` /
  `SENTRY_AUTH_TOKEN`**. Default in `~/.config/spore/secrets.env`
  so every coordinator on the host can read the workspace; allow
  per-project override at `~/.config/spore/<project>/secrets.env`
  where a project needs a different identity (e.g. a token minted
  for a different workspace).
- Recipe shape: **one combined `slack` recipe**. Channel reads,
  thread reads, user lookup, and the visibility audit all share
  the same auth setup.
- Env var name: **`SLACK_BOT_TOKEN`**. Slack's own SDKs (Python
  `slack_sdk`, Node `@slack/web-api`) consume this name silently
  by default. The github recipe rejected `GITHUB_TOKEN` for a
  similar reason but the trade-offs differ here: the Slack SDK
  pickup is narrow (only `slack_sdk.WebClient()` invocations
  pick it up), the token is bot-scoped and read-only so the
  blast radius of an accidental import is small, and matching
  Slack's convention keeps the recipe friendlier to any later
  Python / Node tool an operator might add. Documented in the
  recipe.

## Capability changes vs. the user-token draft

- **`search.messages` is dropped from the recipe.** Bot tokens
  cannot use `search.messages` -- it returns
  `not_allowed_token_type`. The "find a message" workflow now
  uses `conversations.history` on a known channel and a `jq`
  filter; the spec's earlier paid-workspace requirement no
  longer applies.
- **DM reads are dropped.** A bot can only see DMs sent directly
  to the bot user, not arbitrary user-to-user DMs. The `im:*`
  and `mpim:*` scopes are dropped from the minimum set; the
  recipe mentions them as optional for the niche "bot as
  operator inbox" use case.
- **Channel access is per-channel-invite.** Every channel the
  coordinator should read needs an `/invite @<bot-name>` from a
  member. New section in the recipe walks the invite step and
  shows how to audit the resulting visibility set with
  `users.conversations`.



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

1. Audit the bot's visibility: list channels the bot has been
   invited to (`users.conversations`).
2. Read recent messages from a joined channel (`conversations.history`).
3. Read a thread by its parent message timestamp
   (`conversations.replies`).
4. Resolve user IDs to display names / real names
   (`users.info`, `users.list`).

Out of scope for v1:

- Any write: `chat.postMessage`, `chat.update`, `chat.delete`,
  `reactions.add`, `pins.add`, file uploads. Possible v2;
  read-only first matches the `jira` / `sentry` posture and keeps
  the token's blast radius small.
- `search.messages`. Bot tokens cannot use the search method
  (`not_allowed_token_type`). The find-a-message workflow uses
  `conversations.history` + a `jq` filter on a known channel.
- DM reads (`im:*`) and group DM reads (`mpim:*`). A bot can only
  see DMs sent directly to it. Out of scope for this recipe;
  scopes are easy to add later for the niche "bot as operator
  inbox" pattern.
- Workspace / org admin surfaces (`admin.*` methods): user
  provisioning, channel admin, workspace settings, retention,
  enterprise grid management. These need an Enterprise Grid
  workspace plus admin scopes and are intentionally not part of
  this recipe.
- Slack Connect external-workspace surfaces.
- Webhooks, slash commands, interactive components, Socket Mode,
  Events API. These are write / push paths, not the read loop.
- File content download. File metadata via `files.list` /
  `files.info` is out of scope for v1; the recipe will mention
  the `url_private` field on inline message file attachments but
  not encode a download wrapper.

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

**Q1. Bot display name.**

The manifest defaults the bot user's display name to
`spore-coordinator`. On a host running multiple spore projects
against the same workspace, the display name appears once per
install regardless of how many projects use the token; on a
deployment with multiple spore hosts all installing the same app
to the same workspace, each install needs a distinct app name
(Slack does not allow two installs of the same app in one
workspace anyway, but distinct app names help the audit log
distinguish them).

Recommendation: **default name `spore-coordinator` in the
manifest, recipe instructs the operator to suffix the hostname
if installing on more than one host**. The 35-char app-name cap
applies to both name and bot display name.

**Q2. Workspace identifier env var: yes or no?**

The bot token is workspace-scoped, so API calls do not need a
workspace slug. But operators often want to know "which workspace
is this token pointing at?" without hitting the API.

Recommendation: **no separate env var**. The recipe's verify-auth
call (`auth.test`) returns the workspace name + URL on every
invocation, which is the source of truth. Adding a `SLACK_WORKSPACE`
variable would either duplicate that or risk diverging from it.

**Q3. App install approval.**

Slack workspaces can require admin approval for app installs. If
the workspace has that policy on, the operator submits the app
for approval; an admin approves; then the token mints. One-time
cost per app.

Recommendation: **document the approval flow as a note**, with
the click path for an admin to approve from the workspace
settings. Nothing the recipe can do client-side.

**Q4. Channel-invite hygiene.**

The bot only sees channels it has been `/invite`'d to. Operator
workflow: when an operator wants the coordinator to follow a new
channel, they run `/invite @spore-coordinator` in that channel.
Removing the bot from a channel is the reverse: `/remove
@spore-coordinator`.

Recommendation: **document both, plus the visibility audit
example**. The visibility audit (`users.conversations` filtered)
is the source of truth for "what does the coordinator see right
now?" -- the recipe shows it as the first worked example after
verify-auth.

## Recipe outline (for the writeup)

Following the `jira.md` / `github.md` / `sentry.md` shape:

1. **Header** -- one paragraph stating read-only scope, the
   bot-token-vs-user-token reasoning, the explicit "no writes,
   no admin, no DMs, no search" exclusions, and the per-channel
   invite requirement.
2. **Requirements** -- `SLACK_BOT_TOKEN` in the layered vault
   (global default + per-project override). Pointer to the
   memory entry.
3. **Mint the token** -- two paths:
   - "Faster path: from a manifest" with the YAML manifest body
     (bot scopes, `features.bot_user`, no socket-mode / event
     subscriptions / token rotation).
   - "Manual path: from scratch" walking the equivalent click
     path through "OAuth & Permissions" -> Bot Token Scopes ->
     Install to Workspace -> copy "Bot User OAuth Token".

   Admin-approval note if the workspace requires it.
4. **Invite the bot to channels** -- the `/invite @<bot-name>`
   step the operator runs in each channel the coordinator should
   read. Note that this is the granular permission grant; the
   token's scope set is the *upper bound* of what the bot CAN
   read once invited.
5. **Auth gotcha** -- four points:
   - Token prefix decoder: `xoxb-` (bot, this recipe), `xoxp-`
     (user), `xapp-` (app-level), `xoxe.xoxp-` (refresh). This
     recipe uses `xoxb-` and rejects the others.
   - Slack API returns HTTP 200 on logical errors; always check
     `.ok` in the JSON, not the HTTP code. A token failure looks
     like `{"ok": false, "error": "invalid_auth"}` with HTTP 200.
   - Use `Authorization: Bearer $SLACK_BOT_TOKEN`, not the
     deprecated `?token=` query string (leaks via access logs).
   - `not_in_channel` -- the bot has not been invited to the
     channel yet. Run `/invite @<bot-name>` in the channel and
     retry.
6. **Worked examples**:
   - Verify auth (`auth.test`) -- returns workspace name, bot
     user id, bot user name.
   - **Visibility audit**: list every channel the bot is in
     (`users.conversations` filtered by `types=public_channel,private_channel`,
     `exclude_archived=true`). The source of truth for "what
     does the coordinator read?".
   - Read recent messages from a channel
     (`conversations.history` with `channel=`, `limit=20`).
   - Read a thread (`conversations.replies` with `channel=`,
     `ts=<parent-ts>`).
   - Resolve a user id to display name (`users.info` with
     `user=Uxxxxx`).
   - Find a message by query in a known channel
     (`conversations.history` + `jq` filter). Notes that
     `search.messages` is unavailable for bot tokens; this is
     the fallback.
7. **Scope reference** -- minimum bot scopes for the read loop:
   - `channels:read` -- list public channels the bot can see.
   - `channels:history` -- read public channel messages (joined
     channels only).
   - `groups:read` -- list private channels the bot is in.
   - `groups:history` -- read private channel messages (joined
     only).
   - `users:read` -- resolve user ids.

   Optional, narrow uses:
   - `users:read.email` -- email addresses on user lookup. Off
     by default; only enable if a workflow needs to
     cross-reference with another system by email.
   - `im:read` / `im:history` / `mpim:read` / `mpim:history` --
     enable only for the "bot as operator inbox" pattern (the
     operator DMs the bot; the coordinator reads). Off by
     default.
   - `files:read` -- file metadata via `files.list` /
     `files.info`. Out of scope for v1.
8. **Hygiene** -- never echo `$SLACK_BOT_TOKEN`; rotation
   cadence; revocation URL (the app's OAuth & Permissions page,
   "Revoke Tokens"); how to fully uninstall the app to revoke
   the token.

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
- Create the Slack app from the recipe's YAML manifest, install
  to the workspace, copy the Bot User OAuth Token.
- Drop into `~/.config/spore/secrets.env` as
  `SLACK_BOT_TOKEN=xoxb-...`. Per-project override only if a
  project needs to target a different workspace.
- Approve the app install if workspace policy requires admin
  approval.
- `/invite @spore-coordinator` (or whatever the bot was named at
  manifest time) in each channel the coordinator should read.
  Re-audit any time via the `users.conversations` worked
  example.

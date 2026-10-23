# chaebot
personal bot for Twitch on www.twitch.tv/chablades

## Status

Built so far: **dynamic custom commands** (spec feature 1), on Twitch chat.
The YouTube listener and the Wails desktop UI are not built yet.

## Running

Chaebot needs a Twitch account for the bot and a user OAuth token for it
with the `chat:read` and `chat:edit` scopes (for example from
https://twitchtokengenerator.com, or your own Twitch app).

Copy `.env.example` to `.env`, paste the token into
`CHAEBOT_TWITCH_TOKEN` (the `oauth:` prefix is optional), then from the repo
root:

```powershell
go run ./cmd/chaebot
```

`.env` is gitignored. Real environment variables take precedence over it.

### Token refresh

Twitch user tokens expire after a few hours. The bot keeps working on its
current connection, but can't log back in after a disconnect. To have it
refresh the token on its own, register an app at
https://dev.twitch.tv/console/apps, get a refresh token for the bot account
issued to that app, and set `CHAEBOT_TWITCH_CLIENT_ID`,
`CHAEBOT_TWITCH_CLIENT_SECRET` and `CHAEBOT_TWITCH_REFRESH_TOKEN` in `.env`
(see `.env.example`). Refreshed tokens are saved to `twitch_token.json` in
the data folder; pasting a different refresh token into `.env` replaces
them.

Saved data lives in `%AppData%\Chaebot` (override with `CHAEBOT_DATA_DIR`).

Make the bot account a moderator in the channel (`/mod chae_bot`) so Twitch
gives it the higher chat rate limit (100 messages per 30 seconds instead of
20); the bot detects this on its own.

## Custom commands

| Command | Who | Example |
|---|---|---|
| `$addcommand $trigger response` | mods + broadcaster | `$addcommand $discord Join us at discord.gg/xyz` |
| `$editcommand $trigger response` | mods + broadcaster | `$editcommand $discord New link: discord.gg/abc` |
| `$delcommand $trigger` | mods + broadcaster | `$delcommand $discord` |
| `$cooldown $trigger seconds` | mods + broadcaster | `$cooldown $discord 30` |
| `$commands` | everyone | lists all custom commands |
| `$trigger` | everyone | `$discord` |

All commands start with `$` (set by `chat.Prefix` in `internal/chat/chat.go`). Triggers are case-insensitive. The bot ignores management commands from
non-mods and rejects responses that start with `/` or `.` or are over
Twitch's 500-character limit.

Responses can include variables:

| Variable | Becomes | Example response → output |
|---|---|---|
| `{user}` | who used the command | `{user} hugs {touser}!` → `cha hugs Friend!` for `$hug @Friend` |
| `{touser}` | first word after the command, without `@`; `{user}` if none | |
| `{args}` | everything after the command | `You said: {args}` |
| `{count}` | times the command has been used | `Deaths: {count}` → `Deaths: 7` |

Each command has a per-platform cooldown, 5 seconds by default, during
which viewers' uses are ignored. Mods and the broadcaster skip cooldowns.

## Layout

- `internal/chat`: platform-neutral message type and handler interface
- `internal/commands`: custom command store (JSON) and chat handler
- `internal/jsonfile`: atomic JSON file saves shared by the stores
- `internal/twitch`: Twitch IRC client
- `cmd/chaebot`: entry point

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

Saved data lives in `%AppData%\Chaebot` (override with `CHAEBOT_DATA_DIR`).

Make the bot account a moderator in the channel (`/mod chae_bot`) so Twitch
gives it the higher chat rate limit.

## Custom commands

| Command | Who | Example |
|---|---|---|
| `$addcommand $trigger response` | mods + broadcaster | `$addcommand $discord Join us at discord.gg/xyz` |
| `$editcommand $trigger response` | mods + broadcaster | `$editcommand $discord New link: discord.gg/abc` |
| `$delcommand $trigger` | mods + broadcaster | `$delcommand $discord` |
| `$trigger` | everyone | `$discord` |

All commands start with `$` (set by `chat.Prefix` in `internal/chat/chat.go`). Triggers are case-insensitive. The bot ignores management commands from
non-mods and rejects responses that start with `/` or `.` or are over
Twitch's 500-character limit.

## Layout

- `internal/chat`: platform-neutral message type and handler interface
- `internal/commands`: custom command store (JSON) and chat handler
- `internal/twitch`: Twitch IRC client
- `cmd/chaebot`: entry point

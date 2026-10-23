# chaebot
personal bot for Twitch on www.twitch.tv/chablades

## Status

Built so far, on Twitch and YouTube chat: **dynamic custom commands** (spec
feature 1) with variables and cooldowns, and **timed messages**. Commands
and timers are shared across both platforms. The Wails desktop UI is not
built yet.

## Running

For Twitch, Chaebot needs a Twitch account for the bot and a user OAuth token for it
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

### YouTube

YouTube is enabled when `CHAEBOT_YOUTUBE_CLIENT_ID` is set. You need:

1. A Google Cloud project with the **YouTube Data API v3** enabled, and an
   OAuth client (type "Desktop app"). Publish the app's consent screen to
   production: while it's in testing, Google expires refresh tokens after
   7 days.
2. A refresh token for **the channel that streams**, with the
   `https://www.googleapis.com/auth/youtube` scope. One way is the
   [OAuth 2.0 Playground](https://developers.google.com/oauthplayground):
   under settings, tick "Use your own OAuth credentials" and enter your
   client ID and secret, authorize that scope, then exchange the code for
   tokens.

The bot finds that channel's live stream by itself, checking every two
minutes while offline, and **posts as that channel**. The streamer's own
messages still run commands; the bot recognizes and skips its own replies.
Replies are cut to YouTube's 200-character limit.

YouTube API calls use quota, 10,000 units a day by default. Reading chat
costs 5 units per read and each bot reply costs about 20, so the default of
one read every 15 seconds uses roughly 1,200 units an hour of streaming
plus replies. Raise `CHAEBOT_YOUTUBE_POLL_SECONDS` for long streams, or
request more quota from Google. If quota runs out, the bot waits 30 minutes
between retries.

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

## Timers

Timers post a message on a schedule, for example a reminder to follow.

| Command | Who | Example |
|---|---|---|
| `$addtimer name minutes message` | mods + broadcaster | `$addtimer socials 15 Follow me on twitter.com/x` |
| `$deltimer name` | mods + broadcaster | `$deltimer socials` |
| `$timers` | mods + broadcaster | lists timers |

Intervals are 5 to 1440 minutes. A timer only posts in a chat that has had
at least 5 messages since it last posted there, so it won't fill a quiet
chat, and at most one timer posts per chat every 30 seconds. To change a
timer, delete and re-add it.

## Layout

- `internal/chat`: platform-neutral message type and handler interface
- `internal/commands`: custom command store (JSON) and chat handler
- `internal/timers`: timed messages
- `internal/jsonfile`: atomic JSON file saves shared by the stores
- `internal/twitch`: Twitch IRC client
- `internal/youtube`: YouTube live chat client (Data API polling)
- `cmd/chaebot`: entry point

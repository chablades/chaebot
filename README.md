# chaebot
personal bot for Twitch on www.twitch.tv/chablades

## Status

Built so far: **dynamic custom commands** (spec feature 1), on Twitch chat,
and a **Wails desktop app** to run and control the bot. The YouTube listener
is not built yet.

Chaebot needs a Twitch account for the bot and a user OAuth token for it
with the `chat:read` and `chat:edit` scopes (for example from
https://twitchtokengenerator.com, or your own Twitch app).

## Desktop app

Build it with the [Wails CLI](https://wails.io) (no npm needed, the UI in
`frontend/` is plain HTML/CSS/JS):

```powershell
wails build      # produces build\bin\Chaebot.exe
wails dev        # or run with live reload while editing the UI
```

Chaebot's face is `frontend/chaebot.png` (in the app) and
`build/appicon.png` (the `.exe` and taskbar icon). Wails only turns
`appicon.png` into `build/windows/icon.ico` when the `.ico` is missing, so
delete the `.ico` after swapping the picture.

`Chaebot.exe` is self-contained, so copy it or pin it anywhere. On first
launch it asks for the bot account, channel and token (prefilled from `.env`
if you start it from the repo root) and saves them to `settings.json` in the
data folder, token included in plain text.

From the window you can start and stop the bot, watch chat live, send
messages as the bot, and add, edit, delete or post custom commands. Changes
mods make from chat show up immediately. Settings has an option to connect
automatically when the app opens. Only one copy runs at a time.

## Command-line bot

`cmd/chaebot` runs the same bot without a window. Copy `.env.example` to
`.env`, paste the token into `CHAEBOT_TWITCH_TOKEN` (the `oauth:` prefix is
optional), then from the repo root:

```powershell
go run ./cmd/chaebot
```

`.env` is gitignored. Real environment variables take precedence over it.

## Data

Commands (`commands.json`), desktop settings and the desktop log live in
`%AppData%\Chaebot` (override with `CHAEBOT_DATA_DIR`). Both versions share
the same command list, but don't run them at the same time or every command
gets answered twice.

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
- `internal/config`: data folder and `.env` loading
- repo root (`main.go`, `app.go`, `settings.go`, `frontend/`): desktop app
- `cmd/chaebot`: command-line entry point

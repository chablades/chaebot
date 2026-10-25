"use strict";

// Talks to the Go backend (app.go) through the bindings Wails injects as
// window.go.main.App and listens for its "status", "chat", "commands" and
// "log" events. Chat text is untrusted, so everything is rendered with
// textContent, never innerHTML.

const MAX_RESPONSE = 500;
const MAX_FEED = 500;
const MAX_LOG = 300;
const RUNNING = ["connecting", "connected", "reconnecting"];
const STATE_LABELS = {
  stopped: "Stopped",
  connecting: "Connecting…",
  connected: "Connected",
  reconnecting: "Reconnecting…",
  error: "Error",
};

const $ = (id) => document.getElementById(id);
const api = () => window.go.main.App;

let status = { state: "stopped", detail: "" };
let settings = {};
let commands = [];
let editing = null; // { trigger, draft } while a command is being edited
const logLines = [];

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function button(label, className, onClick) {
  const b = el("button", "btn " + className, label);
  b.type = "button";
  b.addEventListener("click", onClick);
  return b;
}

// Wails rejects promises with the Go error's text.
const message = (err) => {
  const text = String(err && err.message ? err.message : err);
  return text.charAt(0).toUpperCase() + text.slice(1);
};

const length = (s) => [...s].length;

/* Status and start/stop */

function setStatus(next) {
  status = next;
  const running = RUNNING.includes(status.state);
  $("status").dataset.state = status.state;
  $("status-label").textContent = STATE_LABELS[status.state] || status.state;
  $("status-detail").textContent = status.detail || "";
  $("status").title = status.detail || "";

  const toggle = $("toggle-btn");
  toggle.textContent = running ? "Stop bot" : "Start bot";
  toggle.classList.toggle("primary", !running);
  toggle.classList.toggle("danger", running);
  toggle.disabled = false;

  const connected = status.state === "connected";
  $("say-input").disabled = !connected;
  $("say-btn").disabled = !connected;
  $("say-input").placeholder = connected
    ? `Send a message as ${settings.username || "the bot"}`
    : "Start the bot to send messages";
  document.querySelectorAll("[data-post]").forEach((b) => (b.disabled = !connected));
}

async function toggleBot() {
  const toggle = $("toggle-btn");
  toggle.disabled = true;
  try {
    if (RUNNING.includes(status.state)) {
      await api().Stop();
    } else {
      await api().Start();
    }
  } catch (err) {
    setStatus({ state: "error", detail: message(err) });
    if (!complete(settings)) openSettings();
  } finally {
    toggle.disabled = false;
  }
}

/* Chat feed */

function hue(name) {
  let h = 0;
  for (const ch of name.toLowerCase()) h = (h * 31 + ch.codePointAt(0)) % 360;
  return h;
}

function addChat(line) {
  $("feed-empty")?.remove();
  const feed = $("feed");
  const atBottom = feed.scrollHeight - feed.scrollTop - feed.clientHeight < 48;

  const li = el("li", "line" + (line.fromBot ? " bot" : "") + (line.error ? " failed" : ""));
  li.append(el("time", "", line.time));

  const who = el("span", "who");
  if (line.fromBot) {
    const face = el("img", "avatar");
    face.src = "chaebot.png";
    face.alt = "";
    who.append(face);
  } else if (line.isMod) {
    who.append(el("span", "badge", "MOD"));
  }
  who.append(line.user);
  who.style.setProperty("--hue", hue(line.user));
  li.append(who);

  const text = el("span", "text", line.text);
  if (line.error) text.append(el("span", "send-error", "Not sent: " + line.error));
  li.append(text);

  feed.append(li);
  while (feed.children.length > MAX_FEED) feed.firstElementChild.remove();
  if (atBottom || line.fromBot) feed.scrollTop = feed.scrollHeight;
}

function clearChat() {
  const empty = el("li", "empty");
  empty.id = "feed-empty";
  const art = el("img", "empty-art");
  art.src = "chaebot.png";
  art.alt = "";
  empty.append(art, el("span", "", "Chat cleared."));
  $("feed").replaceChildren(empty);
}

async function sendMessage(event) {
  event.preventDefault();
  const input = $("say-input");
  const text = input.value.trim();
  if (!text) return;
  try {
    await api().Send(text);
    input.value = "";
  } catch (err) {
    setStatus({ ...status, detail: message(err) });
  }
  input.focus();
}

/* Commands */

function renderCommands() {
  const list = $("cmd-list");
  const filter = $("cmd-filter").value.trim().toLowerCase();
  $("cmd-count").textContent = commands.length;

  if (editing && !commands.some((c) => c.trigger === editing.trigger)) editing = null;

  const shown = commands.filter(
    (c) => !filter || c.trigger.includes(filter) || c.response.toLowerCase().includes(filter),
  );
  if (shown.length === 0) {
    list.replaceChildren(
      el("li", "empty", commands.length ? "No commands match that filter." : "No commands yet. Add one here, or have a mod type $addcommand in chat."),
    );
    return;
  }
  list.replaceChildren(...shown.map(commandItem));
}

function commandItem(cmd) {
  const li = el("li", "cmd");
  const top = el("div", "cmd-top");
  top.append(el("span", "trigger", cmd.trigger));
  const actions = el("div", "cmd-actions");
  top.append(actions);
  li.append(top);

  if (editing && editing.trigger === cmd.trigger) {
    li.classList.add("editing");
    li.append(editor(cmd));
    return li;
  }

  const post = button("Post", "ghost small", () => api().Send(cmd.response).catch(() => {}));
  post.dataset.post = "";
  post.title = "Post this response in chat now";
  post.disabled = status.state !== "connected";

  const edit = button("Edit", "ghost small", () => {
    editing = { trigger: cmd.trigger, draft: cmd.response };
    renderCommands();
    document.querySelector(".cmd.editing textarea")?.focus();
  });

  const del = button("Delete", "ghost small danger-text", async () => {
    if (!del.classList.contains("confirm")) {
      del.classList.add("confirm");
      del.textContent = "Delete?";
      setTimeout(() => {
        del.classList.remove("confirm");
        del.textContent = "Delete";
      }, 3000);
      return;
    }
    try {
      await api().DeleteCommand(cmd.trigger);
    } catch (err) {
      alert(message(err));
    }
  });

  actions.append(post, edit, del);
  li.append(el("p", "response", cmd.response));
  return li;
}

function editor(cmd) {
  const wrap = el("form", "editor");
  const area = el("textarea");
  area.rows = 3;
  area.value = editing.draft;
  const counter = el("span", "counter");
  const error = el("span", "error");
  const foot = el("div", "form-foot");

  const update = () => {
    editing.draft = area.value;
    const n = length(area.value);
    counter.textContent = `${n}/${MAX_RESPONSE}`;
    counter.classList.toggle("over", n > MAX_RESPONSE);
  };
  area.addEventListener("input", update);
  area.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      wrap.requestSubmit();
    } else if (e.key === "Escape") {
      editing = null;
      renderCommands();
    }
  });
  update();

  wrap.addEventListener("submit", async (e) => {
    e.preventDefault();
    error.textContent = "";
    try {
      await api().EditCommand(cmd.trigger, area.value);
      editing = null;
      renderCommands();
    } catch (err) {
      error.textContent = message(err);
    }
  });

  const save = el("button", "btn primary small", "Save");
  save.type = "submit";
  foot.append(
    error,
    counter,
    button("Cancel", "ghost small", () => {
      editing = null;
      renderCommands();
    }),
    save,
  );
  wrap.append(area, foot);
  return wrap;
}

function toggleNewForm(show) {
  const form = $("new-cmd");
  form.hidden = !show;
  $("new-cmd-btn").hidden = show;
  $("new-error").textContent = "";
  if (show) {
    $("new-trigger").focus();
  } else {
    form.reset();
    updateNewCounter();
  }
}

function updateNewCounter() {
  const n = length($("new-response").value);
  const counter = $("new-counter");
  counter.textContent = `${n}/${MAX_RESPONSE}`;
  counter.classList.toggle("over", n > MAX_RESPONSE);
}

async function addCommand(event) {
  event.preventDefault();
  $("new-error").textContent = "";
  try {
    await api().AddCommand($("new-trigger").value, $("new-response").value);
    toggleNewForm(false);
  } catch (err) {
    $("new-error").textContent = message(err);
  }
}

/* Log */

function addLog(line) {
  logLines.push(line);
  if (logLines.length > MAX_LOG) logLines.splice(0, logLines.length - MAX_LOG);
  const pre = $("log-lines");
  const atBottom = pre.scrollHeight - pre.scrollTop - pre.clientHeight < 24;
  pre.textContent = logLines.join("\n");
  if (atBottom) pre.scrollTop = pre.scrollHeight;
}

/* Settings */

const complete = (s) => Boolean(s.username && s.channel && s.token);

function openSettings() {
  $("set-username").value = settings.username || "";
  $("set-channel").value = settings.channel || "";
  $("set-token").value = settings.token || "";
  $("set-token").type = "password";
  $("toggle-token").textContent = "Show";
  $("set-autostart").checked = Boolean(settings.autoStart);
  $("settings-error").textContent = "";
  $("settings").showModal();
  const first = ["set-username", "set-channel", "set-token"].find((id) => !$(id).value);
  $(first || "settings-save").focus();
}

async function saveSettings(event) {
  event.preventDefault();
  const next = {
    username: $("set-username").value,
    channel: $("set-channel").value,
    token: $("set-token").value,
    autoStart: $("set-autostart").checked,
  };
  try {
    await api().SaveSettings(next);
    settings = await api().GetSettings();
    $("settings").close();
    updateChannelLabel();
    if (RUNNING.includes(status.state)) {
      setStatus({ ...status, detail: "Settings saved. Restart the bot to use them." });
    }
  } catch (err) {
    $("settings-error").textContent = message(err);
  }
}

function updateChannelLabel() {
  $("channel-label").textContent = settings.channel ? "#" + settings.channel : "";
}

/* Startup */

function waitForBackend() {
  return new Promise((resolve) => {
    const check = () => (window.go && window.runtime ? resolve() : setTimeout(check, 20));
    check();
  });
}

async function init() {
  await waitForBackend();

  $("toggle-btn").addEventListener("click", toggleBot);
  $("settings-btn").addEventListener("click", openSettings);
  $("clear-chat").addEventListener("click", clearChat);
  $("composer").addEventListener("submit", sendMessage);
  $("new-cmd-btn").addEventListener("click", () => toggleNewForm(true));
  $("new-cancel").addEventListener("click", () => toggleNewForm(false));
  $("new-cmd").addEventListener("submit", addCommand);
  $("new-response").addEventListener("input", updateNewCounter);
  $("new-response").addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      $("new-cmd").requestSubmit();
    }
  });
  $("new-trigger").addEventListener("input", (e) => {
    e.target.value = e.target.value.replace(/^\$+/, "").replace(/\s+/g, "");
  });
  $("cmd-filter").addEventListener("input", renderCommands);
  $("settings-form").addEventListener("submit", saveSettings);
  $("settings-cancel").addEventListener("click", () => $("settings").close());
  $("toggle-token").addEventListener("click", () => {
    const input = $("set-token");
    input.type = input.type === "password" ? "text" : "password";
    $("toggle-token").textContent = input.type === "password" ? "Show" : "Hide";
  });
  $("token-link").addEventListener("click", (e) => {
    e.preventDefault();
    window.runtime.BrowserOpenURL("https://twitchtokengenerator.com");
  });
  $("open-data").addEventListener("click", () => api().OpenDataFolder());

  window.runtime.EventsOn("status", setStatus);
  window.runtime.EventsOn("chat", addChat);
  window.runtime.EventsOn("log", addLog);
  window.runtime.EventsOn("commands", (list) => {
    commands = list || [];
    renderCommands();
  });

  const state = await api().GetState();
  settings = state.settings || {};
  commands = state.commands || [];
  $("open-data").textContent = state.dataDir || "(unavailable)";
  (state.logs || []).forEach(addLog);
  updateChannelLabel();
  setStatus(state.status);
  renderCommands();
  if (!complete(settings)) openSettings();
}

init();

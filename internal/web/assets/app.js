// swarm remote control.
//
// The server does the terminal emulation and sends ready-made HTML lines, so
// this file only has to: keep a websocket per visible agent, patch the lines
// that changed, and translate key presses into the bytes a terminal expects.

const $ = (sel) => document.querySelector(sel);

const state = {
  agents: [],
  selected: null,
  view: "screen", // screen | grid | log
  readOnly: document.body.dataset.readonly === "1",
  ws: null,
  gridWs: new Map(),
  gridCells: new Map(), // name -> the parts of a cell that change with state
  lines: [],
  cols: 0,
};

// ---------------------------------------------------------------- key mapping

// Sequences a terminal sends for the keys that matter when driving an agent.
const KEYS = {
  Enter: "\r",
  Tab: "\t",
  Backspace: "\x7f",
  Escape: "\x1b",
  Delete: "\x1b[3~",
  Insert: "\x1b[2~",
  ArrowUp: "\x1b[A",
  ArrowDown: "\x1b[B",
  ArrowRight: "\x1b[C",
  ArrowLeft: "\x1b[D",
  Home: "\x1b[H",
  End: "\x1b[F",
  PageUp: "\x1b[5~",
  PageDown: "\x1b[6~",
  F1: "\x1bOP", F2: "\x1bOQ", F3: "\x1bOR", F4: "\x1bOS",
  F5: "\x1b[15~", F6: "\x1b[17~", F7: "\x1b[18~", F8: "\x1b[19~",
  F9: "\x1b[20~", F10: "\x1b[21~", F11: "\x1b[23~", F12: "\x1b[24~",
};

function keyToBytes(e) {
  if (e.key === "Tab" && e.shiftKey) return "\x1b[Z";
  if (e.key === "Enter" && (e.altKey || e.shiftKey)) return "\x1b\r";

  if (e.ctrlKey && e.key.length === 1) {
    const c = e.key.toLowerCase();
    if (c >= "a" && c <= "z") return String.fromCharCode(c.charCodeAt(0) - 96);
    const specials = { "[": "\x1b", "\\": "\x1c", "]": "\x1d", "^": "\x1e", _: "\x1f", "@": "\0", " ": "\0" };
    if (specials[c] !== undefined) return specials[c];
    return null;
  }
  if (KEYS[e.key] !== undefined) {
    return e.altKey ? "\x1b" + KEYS[e.key] : KEYS[e.key];
  }
  if (e.key.length === 1 && !e.metaKey) {
    return e.altKey ? "\x1b" + e.key : e.key;
  }
  return null;
}

// ---------------------------------------------------------------- api helpers

async function api(path, options) {
  const res = await fetch(path, options);
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

async function action(payload) {
  try {
    const res = await api("/api/action", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (!res.ok) throw new Error(res.error || "failed");
    say(res.message || describe(res.results));
  } catch (err) {
    say(err.message, true);
  }
}

function describe(results) {
  if (!results || !results.length) return "done";
  const ok = results.filter((r) => r.ok).length;
  const bad = results.filter((r) => !r.ok);
  if (!bad.length) return `done on ${ok} agent(s)`;
  return `${ok} ok, ${bad.length} failed: ${bad[0].error}`;
}

let sayTimer = null;
function say(text, isError) {
  const el = $("#status");
  el.textContent = text || "";
  el.classList.toggle("err", !!isError);
  clearTimeout(sayTimer);
  if (text) sayTimer = setTimeout(() => (el.textContent = ""), 6000);
}

// ---------------------------------------------------------------- agent list

const GLYPH = { working: "●", idle: "○", starting: "◐", exited: "✖", stopped: "·" };

function stateClass(a) {
  return a.attention ? "s-attention" : "s-" + a.state;
}

function glyph(a) {
  return a.attention ? "▲" : GLYPH[a.state] || "·";
}

function renderSidebar() {
  const ul = $("#agents");
  ul.textContent = "";
  for (const a of state.agents) {
    const li = document.createElement("li");
    li.className = a.name === state.selected ? "sel" : "";
    li.onclick = () => select(a.name);

    const dot = document.createElement("span");
    dot.className = "dot " + stateClass(a);
    dot.textContent = glyph(a);

    const nm = document.createElement("span");
    nm.className = "nm";
    nm.textContent = a.name;

    li.append(dot, nm);
    if (a.unread > 0) {
      const un = document.createElement("span");
      un.className = "un";
      un.textContent = a.unread + "✉";
      li.append(un);
    }
    ul.append(li);
  }
}

function renderCounts() {
  const c = { working: 0, idle: 0, attention: 0, dead: 0, unread: 0 };
  for (const a of state.agents) {
    if (a.attention) c.attention++;
    else if (a.state === "working") c.working++;
    else if (a.state === "idle") c.idle++;
    else if (a.state === "exited" || a.state === "stopped") c.dead++;
    c.unread += a.unread || 0;
  }
  const parts = [`<span>${state.agents.length} agents</span>`];
  if (c.working) parts.push(`<span class="c-working">${c.working} working</span>`);
  if (c.idle) parts.push(`<span class="c-idle">${c.idle} idle</span>`);
  if (c.attention) parts.push(`<span class="c-attention">${c.attention} need you</span>`);
  if (c.dead) parts.push(`<span class="c-dead">${c.dead} down</span>`);
  if (c.unread) parts.push(`<span class="c-unread">${c.unread} unread</span>`);
  $("#counts").innerHTML = parts.join("");
}

function renderTitle() {
  const a = state.agents.find((x) => x.name === state.selected);
  const el = $("#title");
  el.textContent = "";
  if (!a) return;

  const nm = document.createElement("span");
  nm.className = "nm";
  nm.textContent = a.name;

  const st = document.createElement("span");
  st.className = stateClass(a);
  st.textContent = a.state;
  el.append(nm, st);

  if (a.attention) {
    const at = document.createElement("span");
    at.className = "attn";
    at.textContent = "▲ " + a.attention;
    el.append(at);
  }
  const bits = [];
  if (a.role) bits.push(a.role);
  if (a.pid) bits.push("pid " + a.pid);
  if (a.cols) bits.push(a.cols + "×" + a.rows);
  if (a.exit) bits.push(a.exit);
  if (bits.length) {
    const meta = document.createElement("span");
    meta.className = "meta";
    meta.textContent = bits.join(" · ");
    el.append(meta);
  }

  for (const [label, act] of [["restart", "restart"], ["stop", "stop"], ["start", "start"]]) {
    const b = document.createElement("button");
    b.className = "ghost";
    b.textContent = label;
    b.onclick = () => action({ action: act, target: a.name });
    el.append(b);
  }
}

// ------------------------------------------------------------------- terminal

// The screen is monospaced and cols×rows; scale the type so the whole of it
// fits the pane — all the width, or all the height, whichever runs out first —
// and let the stylesheet centre what is left over.
//
// Fitting the width alone was not enough: a 37-row agent in a short pane had
// its bottom below the fold, which is where an agent's prompt and its last
// answer live. Both dimensions, or neither is worth much.
function fitFont(el, cols, rows) {
  if (!cols) return;
  const probe = document.createElement("span");
  probe.style.cssText = "position:absolute;visibility:hidden;white-space:pre;font-family:var(--mono);font-size:100px";
  probe.textContent = "0".repeat(10);
  el.append(probe);
  const per100 = probe.getBoundingClientRect().width / 10;
  probe.remove();
  if (!per100) return;
  const byWidth = ((el.clientWidth - 18) / cols) * (100 / per100);
  let size = byWidth;
  if (rows) {
    const byHeight = (el.clientHeight - 10) / (rows * 1.2); // 1.2 is the line-height below
    size = Math.min(byWidth, byHeight);
  }
  // A floor all the same: past a certain size nothing is readable and the pane
  // scrolls instead, which loses nothing. The grid is where whole screens are
  // meant to be taken in at a glance.
  size = Math.max(6, Math.min(16, size));
  el.style.fontSize = size.toFixed(2) + "px";
}

function paintFull(lines, cols) {
  const el = $("#screen");
  state.lines = lines.slice();
  state.cols = cols;
  el.innerHTML = lines.join("");
  fitFont(el, cols, lines.length);
}

function paintDiff(changed, cols) {
  const el = $("#screen");
  // If the DOM and our idea of it have drifted apart — after an offline
  // notice, say — patching by index would land on the wrong lines. Reconnect
  // instead: the server starts a new stream with a full screen.
  if (el.children.length !== state.lines.length) {
    if (state.ws) state.ws.close();
    return;
  }
  const children = el.children;
  for (const [idx, html] of Object.entries(changed)) {
    const i = Number(idx);
    state.lines[i] = html;
    if (children[i]) children[i].outerHTML = html;
  }
  if (cols && cols !== state.cols) {
    state.cols = cols;
    fitFont(el, cols, state.lines.length);
  }
}

function showOffline(text) {
  const el = $("#screen");
  el.innerHTML = "";
  const hint = document.createElement("div");
  hint.className = "hint";
  hint.textContent = text;
  el.append(hint);
  state.lines = [];
}

// -------------------------------------------------------------------- sockets

function wsURL(agent, rate) {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  let u = `${proto}//${location.host}/ws?agent=${encodeURIComponent(agent)}`;
  if (rate) u += "&rate=" + rate;
  return u;
}

function connect(agent) {
  if (state.ws) {
    state.ws.onclose = null;
    state.ws.close();
  }
  const ws = new WebSocket(wsURL(agent));
  state.ws = ws;
  ws.onmessage = (ev) => {
    const f = JSON.parse(ev.data);
    if (f.info) mergeInfo(f.info);
    if (f.type === "full") paintFull(f.full, f.cols);
    else if (f.type === "diff") paintDiff(f.lines || {}, f.cols);
    else if (f.type === "off") showOffline(f.text || "not running");
  };
  ws.onclose = () => {
    if (state.ws === ws && state.selected === agent) {
      setTimeout(() => state.selected === agent && connect(agent), 1200);
    }
  };
}

function mergeInfo(info) {
  const i = state.agents.findIndex((a) => a.name === info.name);
  if (i >= 0) state.agents[i] = info;
  renderSidebar();
  renderCounts();
  paintCellState(info);
  if (info.name === state.selected) renderTitle();
}

// paintCellState keeps a grid cell's dot in step with the agent.
//
// The cells were built once and never touched again, so a grid left open
// showed the states an agent had when it was opened — which is most of what a
// grid is for. The screens were live; the dots beside them were not.
function paintCellState(a) {
  const parts = state.gridCells.get(a.name);
  if (!parts) return;
  parts.dot.className = "dot " + stateClass(a);
  parts.dot.textContent = glyph(a);
  parts.attn.textContent = a.attention ? "▲ " + a.attention : "";
  parts.attn.hidden = !a.attention; // an empty span still costs a gap
}

function send(msg) {
  if (state.readOnly) return say("this swarm is served read-only", true);
  if (state.ws && state.ws.readyState === WebSocket.OPEN) state.ws.send(JSON.stringify(msg));
}

// ----------------------------------------------------------------- grid view

function openGrid() {
  const grid = $("#grid");
  grid.textContent = "";
  closeGrid();
  layoutGrid(state.agents);
  for (const a of state.agents) {
    const cell = document.createElement("div");
    cell.className = "cell" + (a.name === state.selected ? " sel" : "");
    cell.onclick = () => {
      select(a.name);
      setView("screen");
    };

    const head = document.createElement("div");
    head.className = "ch";
    const dot = document.createElement("span");
    dot.className = "dot " + stateClass(a);
    dot.textContent = glyph(a);
    const nm = document.createElement("strong");
    nm.textContent = a.name;
    const attn = document.createElement("span");
    attn.className = "s-attention";
    head.append(dot, nm, attn);
    state.gridCells.set(a.name, { dot, attn });
    paintCellState(a);

    const body = document.createElement("div");
    body.className = "cs";
    body.textContent = "";
    // No height is set on purpose: the type is scaled to the width, so the
    // height that follows is the terminal's own. Imposing one as well cropped
    // the last line whenever the two rounded differently.

    cell.append(head, body);
    grid.append(cell);

    const ws = new WebSocket(wsURL(a.name, "slow"));
    let lines = [];
    ws.onmessage = (ev) => {
      const f = JSON.parse(ev.data);
      if (f.info) mergeInfo(f.info);
      if (f.type === "full") {
        lines = f.full.slice();
        body.innerHTML = lines.join("");
        fitWhole(body, f.cols, lines.length);
      } else if (f.type === "diff") {
        for (const [i, html] of Object.entries(f.lines || {})) lines[Number(i)] = html;
        body.innerHTML = lines.join("");
      } else if (f.type === "off") {
        body.textContent = f.text || "not running";
      }
    };
    state.gridWs.set(a.name, ws);
  }
}

// fitWhole scales a cell's type so the agent's entire screen fits inside it,
// width and height both.
//
// The grid used to show the last fourteen lines of each agent, which is where
// a prompt lives — and which is exactly what one is not watching a grid for.
// Whole screens at a glance is the point: what is legible at this size is not
// the words but the shape, and a shape that changes is an agent doing
// something.
//
// The floor is low on purpose. A 200-column agent in a 300-pixel cell lands
// around two pixels, unreadable and still worth showing; clicking the cell
// opens it full size, which is what reading is for.
// layoutGrid picks how many columns the grid gets, and how tall a cell may be.
//
// auto-fill packed the cells at their minimum width and left the rest of the
// page empty — four agents in a strip across the top of a screen with room for
// three times that. What is wanted is the opposite: the fewest columns whose
// rows still fit on the page, since fewer columns means wider cells and a wider
// cell is a bigger screen.
//
// A cell's height follows from its width, because the terminal inside keeps its
// proportions — see screenRatio, which measures the font rather than guessing
// at it.
// narrowGrid is where the grid gives up on columns entirely.
//
// Lower than the 620 at which the sidebar stops being a column, and
// deliberately so: those answer different questions. The sidebar is a list of
// names and gives up its width early; the grid holds terminals, and a terminal
// half of 450 pixels wide is not worth the room it saves. Below this, one
// column and a scroll.
const narrowGrid = 450;

// cellEm is the width of one character of the terminal font, in ems, measured
// rather than assumed: a guess at it is a guess at the shape of every tile.
// 2.3 was the guess, against about 1.92 in fact, which reserved tiles a fifth
// taller than their screens and left a band of nothing under each one.
let cellEmCache = 0;
function charEm() {
  if (cellEmCache) return cellEmCache;
  const probe = document.createElement("span");
  probe.style.cssText = "position:absolute;visibility:hidden;white-space:pre;font-family:var(--mono);font-size:100px";
  probe.textContent = "0".repeat(10);
  document.body.append(probe);
  const w = probe.getBoundingClientRect().width / 10;
  probe.remove();
  cellEmCache = w / 100 || 0.6;
  return cellEmCache;
}

// screenRatio is a terminal's height divided by its width, at any type size:
// rows of line-height 1.15 over columns of charEm.
function screenRatio(cols, rows) {
  return (rows * 1.15) / (cols * charEm());
}

// minTileFont is the smallest type a tile may draw its screen at. It is the
// real constraint, and a width in pixels was the wrong way to say it: 280
// pixels is comfortable for an 80-column agent and a smear for a 164-column
// one, which is how a grid ended up rendering at 2.96px. What a tile needs is
// therefore cols × minTileFont × charEm, and the grid scrolls rather than going
// below that.
const minTileFont = 6;

function layoutGrid(agents) {
  const grid = document.querySelector("#grid");
  const w = grid.clientWidth;
  const h = grid.clientHeight;
  if (!w || !h || !agents.length) return maxCellHeight;

  const gap = 8;
  const headHeight = 26; // the cell's own title bar
  // The tallest ratio decides, so that no agent is the one that overflows.
  let tallest = 0;
  for (const a of agents) {
    if (a.cols && a.rows) tallest = Math.max(tallest, screenRatio(a.cols, a.rows));
  }
  if (!tallest) tallest = 0.5;

  // Below the width where the sidebar stops being a column, so does the grid.
  // The arithmetic below would sometimes agree and sometimes not — on a 360
  // pixel screen it picks one column over two by thirteen pixels — and that is
  // not a decision worth leaving to a rounding: a phone scrolls down without
  // being asked, and half of 360 is a cell nobody can read anything in.
  if (w < narrowGrid) {
    grid.style.gridTemplateColumns = "1fr";
    return Math.floor(w * tallest);
  }

  // Wide enough for the widest agent to be drawn at minTileFont, whatever that
  // costs in tiles per screen. A fleet is not bounded by the page — at a
  // hundred agents, fitting them all on it means rows a few pixels tall — so
  // past what fits, the grid scrolls. That is what a page does.
  let widest = 80;
  for (const a of agents) if (a.cols) widest = Math.max(widest, a.cols);
  // The 10 pixels are the padding fitWhole takes off before scaling: without
  // them here, a column count is allowed whose type then lands just under the
  // floor — 5.8px where 6 was asked for.
  const minCellWidth = widest * minTileFont * charEm() + 10;
  const columnCap = Math.max(1, Math.floor((w + gap) / (minCellWidth + gap)));
  const most = Math.min(agents.length, columnCap);

  // Among the arrangements that do fit the page, the widest cell wins: four
  // agents in three columns fit at once and leave a row of one with the page
  // half empty, where two columns of two fill it with cells a third wider.
  let best = null;
  for (let columns = 1; columns <= most; columns++) {
    const rows = Math.ceil(agents.length / columns);
    // A cell may be narrower than its column, to leave room for the rows below
    // it — that is what fills a page instead of overflowing it.
    const byWidth = (w - gap * (columns - 1)) / columns;
    const byHeight = ((h - gap * rows) / rows - headHeight) / tallest;
    const cellW = Math.min(byWidth, byHeight);
    if (cellW >= minCellWidth && (!best || cellW > best.width)) {
      best = { columns, width: cellW, height: Math.round(cellW * tallest) };
    }
  }

  // Nothing fits: fill the width with as many as stay readable, and scroll.
  if (!best) {
    const cellW = (w - gap * (most - 1)) / most;
    best = { columns: most, width: cellW, height: Math.round(cellW * tallest) };
  }
  grid.style.gridTemplateColumns = "repeat(" + best.columns + ", 1fr)";
  return best.height;
}

// maxCellHeight is the fallback when the page has not been measured yet.
const maxCellHeight = 320;

function fitWhole(el, cols, rows) {
  if (!cols || !rows) return;
  const probe = document.createElement("span");
  probe.style.cssText = "position:absolute;visibility:hidden;white-space:pre;font-family:var(--mono);font-size:100px";
  probe.textContent = "0".repeat(10);
  el.append(probe);
  const per100 = probe.getBoundingClientRect().width / 10;
  probe.remove();
  if (!per100) return;

  // Width only. How many columns the grid has already decided that the height
  // works out, and constraining it here as well made the two calculations
  // disagree by a pixel or two — enough to crop the bottom line of every
  // screen. What is left is exactly the terminal's proportions.
  const size = Math.max(1.5, Math.min(16, ((el.clientWidth - 10) / cols) * (100 / per100)));
  el.style.fontSize = size.toFixed(2) + "px";
}

function closeGrid() {
  for (const ws of state.gridWs.values()) {
    ws.onclose = null;
    ws.close();
  }
  state.gridWs.clear();
  state.gridCells.clear();
  $("#grid").textContent = "";
}

// ------------------------------------------------------------------- log view

async function openLog() {
  const el = $("#log");
  try {
    const events = await api("/api/events?n=200");
    el.textContent = "";
    for (const e of events.reverse()) {
      const row = document.createElement("div");
      row.className = "e sev" + severity(e);
      row.innerHTML =
        `<span class="t">${new Date(e.at).toLocaleTimeString()}</span>` +
        `<span class="k">${e.kind}</span>` +
        `<span class="a">${e.agent || "swarm"}</span>` +
        `<span class="m"></span>`;
      row.querySelector(".m").textContent = e.text;
      el.append(row);
    }
  } catch (err) {
    el.textContent = err.message;
  }
}

function severity(e) {
  if (e.kind === "error") return 2;
  if (e.kind === "exited" || e.kind === "pattern" || e.kind === "bell") return 1;
  return 0;
}

// ---------------------------------------------------------------------- views

function setView(view) {
  state.view = view;
  $("#screen").hidden = view !== "screen";
  $("#title").hidden = view === "log";
  $("#grid").hidden = view !== "grid";
  $("#log").hidden = view !== "log";
  $("#btn-grid").classList.toggle("on", view === "grid");
  $("#btn-log").classList.toggle("on", view === "log");

  if (view === "grid") openGrid();
  else closeGrid();
  if (view === "log") openLog();
}

function select(name) {
  if (!name) return;
  state.selected = name;
  localStorage.setItem("swarm.selected", name);
  renderSidebar();
  renderTitle();
  showOffline("connecting…");
  connect(name);
}

// ----------------------------------------------------------------------- boot

async function refresh() {
  try {
    const s = await api("/api/state");
    state.agents = s.agents || [];
    state.readOnly = !!s.read_only;
    if (!state.selected || !state.agents.some((a) => a.name === state.selected)) {
      const saved = localStorage.getItem("swarm.selected");
      const pick = state.agents.find((a) => a.name === saved) || state.agents[0];
      if (pick) select(pick.name);
    }
    renderSidebar();
    renderCounts();
    renderTitle();
    if (state.view === "grid" && state.gridWs.size !== state.agents.length) openGrid();
  } catch (err) {
    say(err.message, true);
  }
}

function wire() {
  // Typing on the screen goes straight to the agent.
  const screen = $("#screen");
  screen.addEventListener("keydown", (e) => {
    if (e.metaKey) return;
    const bytes = keyToBytes(e);
    if (bytes === null) return;
    e.preventDefault();
    send({ type: "data", data: bytes });
  });
  screen.addEventListener("paste", (e) => {
    e.preventDefault();
    const text = e.clipboardData.getData("text");
    if (text) send({ type: "text", data: text, submit: false });
  });

  for (const b of document.querySelectorAll(".keys button")) {
    b.onclick = () => {
      send({ type: "keys", keys: b.dataset.keys });
      screen.focus();
    };
  }

  $("#composer").onsubmit = (e) => {
    e.preventDefault();
    const input = $("#prompt");
    const text = input.value;
    if (!text.trim()) return;
    if ($("#mode").value === "send") {
      action({ action: "send", target: state.selected, text });
    } else {
      send({ type: "text", data: text, submit: true });
    }
    input.value = "";
  };

  $("#file").onchange = async (e) => {
    const file = e.target.files[0];
    if (!file) return;
    const body = new FormData();
    body.append("file", file);
    try {
      const res = await api("/api/upload", { method: "POST", body });
      send({ type: "text", data: res.path, submit: true });
      say("staged " + res.path);
    } catch (err) {
      say(err.message, true);
    }
    e.target.value = "";
  };

  $("#btn-grid").onclick = () => setView(state.view === "grid" ? "screen" : "grid");
  $("#btn-log").onclick = () => setView(state.view === "log" ? "screen" : "log");

  window.addEventListener("resize", () => {
    if (state.view === "screen") fitFont($("#screen"), state.cols, state.lines.length);
    else if (state.view === "grid") openGrid();
  });

  document.addEventListener("keydown", (e) => {
    if (e.target.tagName === "INPUT" || e.target === screen) return;
    if (e.key === "g") setView(state.view === "grid" ? "screen" : "grid");
    if (e.key === "l") setView(state.view === "log" ? "screen" : "log");
    if (e.key === "j" || e.key === "k") {
      const i = state.agents.findIndex((a) => a.name === state.selected);
      const next = e.key === "j" ? i + 1 : i - 1;
      if (state.agents[next]) select(state.agents[next].name);
    }
  });
}

wire();
setView("screen");
refresh();
setInterval(refresh, 3000);

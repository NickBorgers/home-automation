// Send commands to the Home Assistant WebSocket API and print the replies as JSON.
//
// Usage (run from homeautomation-go so .env loads; Node 20 needs the flag):
//   cd homeautomation-go && set -a && . ./.env && set +a
//   node --experimental-websocket ../scripts/ha-ws.mjs '[{"type":"config/entity_registry/list"}]'
//
// Input: one JSON array of command objects. Output: one JSON array of replies, same order.
// Reads HA_URL and HA_TOKEN from the environment. Never prints the token.
// HA_URL may be https://host, wss://host, or end in /api/websocket.
const base = (process.env.HA_URL || "").replace(/\/+$/, "").replace(/\/api\/websocket$/, "");
if (!base || !process.env.HA_TOKEN) {
  console.error("HA_URL and HA_TOKEN must be set");
  process.exit(1);
}
const url = base.replace(/^http/, "ws") + "/api/websocket";
const cmds = JSON.parse(process.argv[2] || "[]");

let nextId = 0;
const pending = new Map();
const ws = new WebSocket(url);
const timer = setTimeout(() => {
  console.error("timeout waiting for Home Assistant");
  process.exit(1);
}, 30000);

const send = (cmd) =>
  new Promise((resolve) => {
    cmd.id = ++nextId;
    pending.set(cmd.id, resolve);
    ws.send(JSON.stringify(cmd));
  });

ws.onmessage = async (ev) => {
  const msg = JSON.parse(ev.data);
  if (msg.type === "auth_required") {
    ws.send(JSON.stringify({ type: "auth", access_token: process.env.HA_TOKEN }));
  } else if (msg.type === "auth_ok") {
    const replies = [];
    for (const cmd of cmds) replies.push(await send(cmd));
    console.log(JSON.stringify(replies));
    clearTimeout(timer);
    ws.close();
  } else if (msg.type === "auth_invalid") {
    console.error("auth invalid");
    process.exit(1);
  } else if (msg.id && pending.has(msg.id)) {
    pending.get(msg.id)(msg);
    pending.delete(msg.id);
  }
};
ws.onerror = (e) => {
  console.error("websocket error:", e.message);
  process.exit(1);
};

// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Two snippets for agent-browser's eval in the scratch T3 Code web client
// (README.md, step 4). Typing a steer through the composer takes too long to
// land it at a set time around a turn's end, so the helper sends T3 Code's own
// steer command over the page's WebSocket, with fresh ids.
//
// 1. RECORD keeps every frame the page sends, so that one steer sent by hand
//    gives the helper a template:
//      agent-browser --session <name> eval "$(sed -n '/^\/\/ RECORD$/,/^\/\/ END$/p' steer-helper.js)"
// 2. HELPER takes the last steer frame as the template and defines
//    window.__steer(text), which sends a steer with that text:
//      agent-browser --session <name> eval "$(sed -n '/^\/\/ HELPER$/,/^\/\/ END$/p' steer-helper.js)"

// RECORD
(() => {
  if (window.__cap) return "already";
  window.__cap = 1;
  const send = WebSocket.prototype.send;
  WebSocket.prototype.send = function (d) {
    window.__ws = this;
    (window.__frames = window.__frames || []).push(typeof d === "string" ? d : "[binary " + (d.byteLength || d.size) + "]");
    return send.call(this, d);
  };
  return "ok";
})()
// END

// HELPER
(() => {
  const f = (window.__frames || []).filter((x) => x.includes('"deliveryIntent":"steer"')).pop();
  if (!f) return "send one steer by hand first";
  window.__tpl = f;
  window.__n = 900000;
  window.__steer = (text) => {
    const o = JSON.parse(window.__tpl);
    o.id = ++window.__n;
    o.payload.commandId = crypto.randomUUID();
    o.payload.messageId = crypto.randomUUID();
    o.payload.text = text;
    o.payload.titleSeed = text.slice(0, 50) + "...";
    o.spanId = crypto.randomUUID().replace(/-/g, "").slice(0, 16);
    o.traceId = crypto.randomUUID().replace(/-/g, "");
    window.__ws.send(JSON.stringify(o));
    return o.id;
  };
  return "ready";
})()
// END

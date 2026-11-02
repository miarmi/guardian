// Turns on push notifications for this device (Nastavení).
(function () {
  var button = document.getElementById("push-on");
  if (!button) return;
  var note = document.getElementById("push-note");
  var d = button.dataset;
  function say(text) { note.textContent = text; note.hidden = false; }
  function label() {
    var ua = navigator.userAgent;
    if (/iPhone/.test(ua)) return "iPhone";
    if (/iPad/.test(ua)) return "iPad";
    if (/Android/.test(ua)) return "Android";
    if (/Mac/.test(ua)) return "Mac";
    if (/Windows/.test(ua)) return "Windows";
    return "Browser";
  }
  function save(body) {
    return fetch("/push/subscribe", { method: "POST", credentials: "same-origin",
      headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) })
      .then(function (r) { if (!r.ok) throw new Error("HTTP " + r.status); });
  }
  function keyBytes(b64) {
    var s = (b64 + "===".slice((b64.length + 3) % 4)).replace(/-/g, "+").replace(/_/g, "/");
    var raw = atob(s), out = new Uint8Array(raw.length);
    for (var i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
    return out;
  }
  button.addEventListener("click", function () {
    button.disabled = true;
    fetch("/push/key", { credentials: "same-origin", cache: "no-store" })
      .then(function (r) { return r.json(); })
      .then(function (k) {
        if (k.practice) return save({ practice: true, label: label() });
        if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) {
          throw new Error(d.unsupported);
        }
        return Notification.requestPermission().then(function (p) {
          if (p !== "granted") throw new Error(d.denied);
          return navigator.serviceWorker.register("/sw.js");
        }).then(function () { return navigator.serviceWorker.ready; })
          .then(function (reg) {
            return reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(k.key) });
          }).then(function (sub) {
            var j = sub.toJSON();
            return save({ endpoint: j.endpoint, keys: j.keys, label: label() });
          });
      })
      .then(function () { say(d.added); location.reload(); })
      .catch(function (e) { say(e.message || String(e)); button.disabled = false; });
  });
})();

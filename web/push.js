// Turns on push notifications for this device (Nastavení).
(function () {
  var button = document.getElementById("push-on");
  if (!button) return;
  var note = document.getElementById("push-note");
  var d = button.dataset;
  function say(text) { note.textContent = text; note.hidden = false; }
  function label() {
    var ua = navigator.userAgent;
    var browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "";
    if (/iPhone/.test(ua)) return "iPhone";
    if (/iPad/.test(ua)) return "iPad";
    if (/Android/.test(ua)) return "Android" + (browser ? " – " + browser : "");
    if (/Mac/.test(ua)) return "Mac" + (browser ? " – " + browser : "");
    if (/Windows/.test(ua)) return "Windows" + (browser ? " – " + browser : "");
    return d.browser;
  }
  var KEY = "guardian-push-endpoint";
  function remembered() { try { return localStorage.getItem(KEY) || ""; } catch (e) { return ""; } }
  function remember(ep) { try { localStorage.setItem(KEY, ep); } catch (e) {} }
  // Mark this device in the list and offer a test instead of a second "on".
  function markHere(ep) {
    if (!ep) return false;
    var found = false;
    Array.prototype.forEach.call(document.querySelectorAll("li[data-endpoint]"), function (li) {
      if (li.getAttribute("data-endpoint") === ep) {
        li.querySelector(".this-device").hidden = false;
        found = true;
      }
    });
    if (found) { button.hidden = true; document.getElementById("push-here").hidden = false; }
    return found;
  }
  var ios = /iPhone|iPad/.test(navigator.userAgent);
  var standalone = window.navigator.standalone === true || (window.matchMedia && window.matchMedia("(display-mode: standalone)").matches);
  if (ios && !standalone) {
    document.getElementById("push-home").hidden = false;
    button.disabled = true;
  }
  if (!markHere(remembered()) && "serviceWorker" in navigator && navigator.serviceWorker.getRegistration) {
    navigator.serviceWorker.getRegistration().then(function (reg) {
      if (reg && reg.pushManager) return reg.pushManager.getSubscription();
    }).then(function (sub) { if (sub) markHere(sub.endpoint); }).catch(function () {});
  }
  function save(body) {
    return fetch("/push/subscribe", { method: "POST", credentials: "same-origin",
      headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) })
      .then(function (r) { if (!r.ok) throw new Error("HTTP " + r.status); return r.json(); })
      .then(function (j) { if (j && j.endpoint) remember(j.endpoint); });
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
        if (k.practice) {
          if (remembered()) return; // this browser is already on
          return save({ practice: true, label: label() });
        }
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

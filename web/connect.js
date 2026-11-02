// Keeps the QR code fresh and leaves the page once the phone has scanned it.
(function () {
  var img = document.getElementById("qr-image");
  var waiting = document.getElementById("qr-waiting");
  var retry = document.getElementById("retry");
  var status = document.getElementById("status");
  var shown = "";

  function poll() {
    fetch("/whatsapp/status.json", { credentials: "same-origin", cache: "no-store" })
      .then(function (r) { return r.json(); })
      .then(function (s) {
        if (s.state === "connected") {
          status.textContent = "Connected. Taking you back…";
          window.location.replace("/");
          return;
        }
        if (s.state !== "pairing") {
          img.hidden = true;
          waiting.hidden = true;
          retry.hidden = false;
          return;
        }
        if (s.qr && s.qr !== shown) {
          shown = s.qr;
          img.src = "/whatsapp/qr.png?v=" + s.qr;
          img.hidden = false;
          waiting.hidden = true;
        }
        setTimeout(poll, 1500);
      })
      .catch(function () { setTimeout(poll, 3000); });
  }
  poll();
})();

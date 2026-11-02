// Keeps the QR code fresh, counts down to the next one, and leaves the page
// once the phone has scanned it.
(function () {
  var box = document.getElementById("qr");
  var img = document.getElementById("qr-image");
  var message = document.getElementById("qr-message");
  var retry = document.getElementById("retry");
  var countdown = document.getElementById("countdown");
  var shown = "";      // hash of the code on screen
  var everShown = false;
  var secondsLeft = 0;
  var ticker = null;

  function showCode(hash) {
    instruction.classList.remove("dim");
    shown = hash;
    everShown = true;
    img.src = "/whatsapp/qr.png?v=" + hash;
    img.hidden = false;
    box.classList.add("has-code");
    message.textContent = "";
    retry.hidden = true;
  }

  var instruction = document.getElementById("instruction");

  retry.addEventListener("submit", function () {
    retry.querySelector("button").disabled = true;
    message.textContent = "Getting a code…";
  });

  function showMessage(text, canRetry) {
    instruction.classList.toggle("dim", canRetry);
    img.hidden = true;
    box.classList.remove("has-code");
    message.textContent = text;
    retry.hidden = !canRetry;
    countdown.hidden = true;
    if (ticker) { clearInterval(ticker); ticker = null; }
  }

  function tick() {
    if (secondsLeft > 0) { secondsLeft -= 1; }
    countdown.textContent = "New code in " + secondsLeft + " s";
    countdown.hidden = secondsLeft <= 0;
  }

  function poll() {
    fetch("/whatsapp/status.json", { credentials: "same-origin", cache: "no-store" })
      .then(function (r) { return r.json(); })
      .then(function (s) {
        if (s.state === "connected") {
          showMessage("Connected. Taking you back…", false);
          window.location.replace("/");
          return;
        }
        if (s.state !== "pairing") {
          showMessage(everShown ? "The code expired." : "Could not get a code.", true);
          return;
        }
        if (s.qr && s.qr !== shown) { showCode(s.qr); }
        if (s.qr) {
          secondsLeft = s.seconds || 0;
          if (!ticker) { ticker = setInterval(tick, 1000); }
          tick();
        }
        setTimeout(poll, 1500);
      })
      .catch(function () { setTimeout(poll, 3000); });
  }

  if (box.dataset.error) {
    showMessage(message.textContent, true); // the server already said why
  } else {
    poll();
  }
})();

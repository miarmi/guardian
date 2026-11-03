// Shows the fields that fit the chosen model type, fills in the Anthropic
// defaults, says at once where the messages would go, and warns when the
// saved key no longer fits the type or address.
(function () {
  // Bring a result into view after the page reloads (e.g. test e-mail).
  var here = document.querySelector(".scroll-here");
  if (here && here.scrollIntoView) here.scrollIntoView({ block: "center" });
  var mailType = document.getElementById("mail_type");
  if (mailType) {
    mailType.addEventListener("change", function () {
      document.getElementById("graph-fields").hidden = mailType.value !== "graph";
      document.getElementById("smtp-fields").hidden = mailType.value !== "smtp";
      document.getElementById("sender-field").hidden = mailType.value === "practice" || mailType.value === "gmail";
      document.getElementById("gmail-sending").hidden = mailType.value !== "gmail";
    });
  }
  var form = document.getElementById("settings-form");
  if (!form) return;
  var d = form.dataset;
  var type = document.getElementById("type");
  var remote = document.getElementById("remote-fields");
  var hint = document.getElementById("openai-hint");
  var address = document.getElementById("address");
  var model = document.getElementById("model");
  var dest = document.getElementById("destination");
  var keyNote = document.getElementById("key-note");
  var keyNoteText = keyNote ? keyNote.textContent : "";
  var keyFor = d.keyHost; // "type address" the saved key belongs to, "" if none
  var keyInput = document.getElementById("key");
  var keyPlaceholder = keyInput ? keyInput.placeholder : "";
  var dirty = false;

  function host(addr) {
    try { return new URL(addr).hostname.toLowerCase(); } catch (e) { return ""; }
  }
  function isHome(h) {
    if (!h) return false;
    if (h === "localhost" || /\.local$/.test(h) || /\.internal$/.test(h) || h === "::1" || h === "[::1]") return true;
    var m = h.match(/^(\d+)\.(\d+)\.\d+\.\d+$/);
    if (!m) return false;
    var a = +m[1], b = +m[2];
    return a === 127 || a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254);
  }
  function update() {
    remote.hidden = type.value === "practice";
    hint.hidden = type.value !== "openai";
    var h = type.value === "practice" ? "" : host(address.value);
    if (type.value === "practice" || isHome(h)) {
      dest.textContent = d.staysHome;
    } else if (!h) {
      dest.textContent = d.destPending;
    } else {
      dest.textContent = d.goesTo.replace("%s", h);
    }
    if (keyNote && keyFor) {
      var parts = keyFor.split(" ");
      var same = parts[0] === type.value && host(parts.slice(1).join(" ")) === host(address.value);
      keyNote.textContent = same ? keyNoteText : d.keyForgotten;
      keyInput.placeholder = same ? keyPlaceholder : "";
    }
    if (dirty) {
      Array.prototype.forEach.call(document.querySelectorAll(".transient, .status-line[role=status]"), function (el) { el.hidden = true; });
    }
  }
  form.addEventListener("input", function () { dirty = true; update(); });
  type.addEventListener("change", function () {
    if (type.value === "anthropic") {
      address.value = d.anthropicAddress;
      model.value = d.anthropicModel;
    } else if (type.value === "openai") {
      if (address.value === d.anthropicAddress) address.value = "";
      if (model.value === d.anthropicModel) model.value = "";
    }
    dirty = true;
    update();
  });
  form.addEventListener("submit", function (e) {
    var b = e.submitter;
    if (b && b.id === "test-button") {
      setTimeout(function () { b.disabled = true; b.textContent = d.testing; }, 0);
    }
  });
  update();
})();

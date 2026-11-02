// Shows the fields that fit the chosen model type and fills in the
// Anthropic defaults when that type is chosen.
(function () {
  var form = document.getElementById("settings-form");
  if (!form) return;
  var type = document.getElementById("type");
  var remote = document.getElementById("remote-fields");
  var hint = document.getElementById("openai-hint");
  var address = document.getElementById("address");
  var model = document.getElementById("model");
  var defAddr = form.dataset.anthropicAddress, defModel = form.dataset.anthropicModel;
  type.addEventListener("change", function () {
    remote.hidden = type.value === "practice";
    hint.hidden = type.value !== "openai";
    if (type.value === "anthropic") {
      if (!address.value) address.value = defAddr;
      if (!model.value) model.value = defModel;
    } else if (type.value === "openai") {
      if (address.value === defAddr) address.value = "";
      if (model.value === defModel) model.value = "";
    }
  });
})();

// Bildschirmtastatur für die eigenen Seiten des Terminals (Kachelseite, Leiste,
// Admin-Seite am Terminal). Die Tastatur selbst (wvkbd) steuert der Kiosk-Dienst über
// POST /api/keyboard/show|hide|toggle am eigenen Ursprung.
//   <body data-osk="auto">    zeigen bei Fokus auf einem Textfeld, verbergen beim Verlassen
//   <body data-osk="manual">  nie von selbst (Maus-Bedienung)
//   [data-osk-toggle]         Knopf zum Umschalten (Leiste)
(function () {
  "use strict";
  var TEXT_TYPES = { text: 1, password: 1, number: 1, search: 1, email: 1, tel: 1, url: 1 };
  var auto = document.body && document.body.dataset.osk === "auto";
  var hideTimer = null;

  function send(action) {
    return fetch("/api/keyboard/" + action, { method: "POST" }).catch(function () {});
  }

  function isTextField(el) {
    if (!el || el.disabled || el.readOnly) { return false; }
    if (el.tagName === "TEXTAREA") { return true; }
    if (el.tagName !== "INPUT") { return false; }
    return TEXT_TYPES[(el.getAttribute("type") || "text").toLowerCase()] === 1;
  }

  document.addEventListener("focusin", function (e) {
    if (!auto || !isTextField(e.target)) { return; }
    clearTimeout(hideTimer);
    send("show");
  });

  // Beim Wechsel zwischen zwei Feldern nicht kurz verbergen und wieder zeigen.
  document.addEventListener("focusout", function (e) {
    if (!auto || !isTextField(e.target)) { return; }
    clearTimeout(hideTimer);
    hideTimer = setTimeout(function () {
      if (!isTextField(document.activeElement)) { send("hide"); }
    }, 150);
  });

  document.addEventListener("submit", function () {
    if (auto) { send("hide"); }
  });

  document.addEventListener("click", function (e) {
    var t = e.target.closest && e.target.closest("[data-osk-toggle]");
    if (t) { send("toggle"); }
  });

  // Seite verlassen (Zurück zum Kiosk, Abmelden): die Tastatur bleibt nicht stehen.
  window.addEventListener("pagehide", function () {
    if (auto && navigator.sendBeacon) { navigator.sendBeacon("/api/keyboard/hide"); }
  });
})();

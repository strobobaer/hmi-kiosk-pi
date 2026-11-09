// Nur am Terminal (http://127.0.0.1:8081): Nach 5 Minuten ohne Eingabe abmelden und
// zurück zur Kachelseite. Die letzte Eingabe steht in sessionStorage, damit ein
// Neuladen der Seite (Statusseiten laden sich selbst neu) die Frist nicht verlängert.
(function () {
  "use strict";
  var body = document.body;
  var idle = parseInt(body.dataset.idle, 10) * 1000;
  if (!idle) { return; }
  var KEY = "hmiKioskLastInput";

  function mark() { try { sessionStorage.setItem(KEY, String(Date.now())); } catch (e) {} }
  function forget() { try { sessionStorage.removeItem(KEY); } catch (e) {} }
  function last() {
    try {
      var v = parseInt(sessionStorage.getItem(KEY), 10);
      return isNaN(v) ? 0 : v;
    } catch (e) { return 0; }
  }

  // Neu von der Kachelseite (anderer Ursprung) oder ohne Stand: die Frist beginnt jetzt.
  if (!last() || document.referrer.indexOf(location.origin + "/") !== 0) { mark(); }

  ["pointerdown", "touchstart", "keydown", "input", "wheel"].forEach(function (ev) {
    document.addEventListener(ev, mark, { passive: true, capture: true });
  });

  function leave() {
    forget();
    var back = document.getElementById("kiosk-back");
    if (back && back.tagName === "FORM") { back.submit(); return; }
    location.href = body.dataset.kiosk;
  }

  var back = document.getElementById("kiosk-back");
  if (back) { back.addEventListener("submit", forget); back.addEventListener("click", forget); }

  setInterval(function () {
    if (Date.now() - last() >= idle) { leave(); }
  }, 5000);
})();

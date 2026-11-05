(function () {
  "use strict";
  var done = {};
  var count = 0;
  var total = document.querySelectorAll(".st-target").length;

  function mark(btn, n) {
    btn.classList.add("hit");
    if (!done[n]) {
      done[n] = true;
      count++;
      document.getElementById("st-count").textContent = count + " von " + total;
    }
  }

  document.querySelector(".st-board").addEventListener("pointerdown", function (e) {
    var btn = e.target.closest(".st-target");
    if (!btn) { return; }
    var n = parseInt(btn.dataset.target, 10);
    fetch("/api/selftest/touch", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ target: n })
    }).then(function (r) { if (r.ok) { mark(btn, n); } }).catch(function () {});
  });
})();

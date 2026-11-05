(function () {
  "use strict";
  var page = document.body.dataset.page;
  var NOTICE_MS = 15000;
  var noticeTimer = null;
  var lastNotice = "";
  var state = "";

  function $(id) { return document.getElementById(id); }

  function post(path) {
    return fetch(path, { method: "POST" }).then(function (r) {
      if (r.ok) { return; }
      return r.json().then(function (j) { showNotice(j.error || "Aktion nicht möglich"); }, function () {});
    }).catch(function () {});
  }

  function showNotice(text) {
    var n = $("notice");
    if (!n) { return; }
    clearTimeout(noticeTimer);
    if (!text) { n.hidden = true; return; }
    n.textContent = text;
    n.hidden = false;
    noticeTimer = setTimeout(hideNotice, NOTICE_MS);
  }

  function hideNotice() {
    clearTimeout(noticeTimer);
    var n = $("notice");
    if (n) { n.hidden = true; }
  }

  function renderHints(s) {
    var box = $("hints");
    box.textContent = "";
    (s.badKey || []).forEach(function (t) { addHint(box, "Schlüssel falsch: " + t); });
    (s.unreachable || []).forEach(function (t) { addHint(box, "Nicht erreichbar: " + t); });
    box.hidden = box.children.length === 0;
  }

  function addHint(box, text) {
    var d = document.createElement("div");
    d.className = "hint";
    d.textContent = text;
    box.appendChild(d);
  }

  function renderTiles(s) {
    var busy = s.state === "requesting" || s.state === "starting" || s.state === "reconnecting";
    $("overlay").hidden = !busy;
    if (busy) {
      $("overlay-text").textContent = "Verbinde mit " + s.hmiName + " …";
      $("overlay-cancel").hidden = s.state === "reconnecting";
    }
    var confirming = s.state === "confirming";
    $("confirm").hidden = !confirming;
    if (confirming) {
      $("confirm-text").textContent = "Wird genutzt von " + s.confirmName + " – übernehmen?";
    }
    renderHints(s);
    if (s.notice !== lastNotice) {
      lastNotice = s.notice;
      showNotice(s.notice);
    }
    if (state && state !== s.state && s.state === "tiles") { refreshGrid(); }
    state = s.state;
  }

  function refreshGrid() {
    fetch("/").then(function (r) { return r.text(); }).then(function (html) {
      var doc = new DOMParser().parseFromString(html, "text/html");
      var g = doc.getElementById("grid");
      if (g && g.innerHTML !== $("grid").innerHTML) { $("grid").innerHTML = g.innerHTML; }
    }).catch(function () {});
  }

  function renderBar(s) {
    $("bar-name").textContent = s.hmiName;
    var m = $("bar-mode");
    m.textContent = s.viewOnly ? "Nur ansehen" : "Bedienen";
    m.className = "badge " + (s.viewOnly ? "view" : "operate");
    var n = $("bar-notice");
    n.textContent = s.state === "warned" ? s.notice : "";
    n.hidden = s.state !== "warned";
  }

  function connect() {
    var es = new EventSource("/api/events");
    es.onmessage = function (e) {
      var s = JSON.parse(e.data);
      if (page === "tiles") { renderTiles(s); } else { renderBar(s); }
    };
    es.onerror = function () {
      es.close();
      setTimeout(function () { location.reload(); }, 2000);
    };
  }

  if (page === "tiles") {
    $("grid").addEventListener("click", function (e) {
      var b = e.target.closest("button.tile");
      if (!b || b.disabled) { return; }
      hideNotice();
      post("/api/hmi/" + encodeURIComponent(b.dataset.id) + "/open");
    });
    $("confirm-yes").addEventListener("click", function () { hideNotice(); post("/api/confirm"); });
    $("confirm-no").addEventListener("click", function () { hideNotice(); post("/api/cancel"); });
    $("overlay-cancel").addEventListener("click", function () { hideNotice(); post("/api/back"); });
    setInterval(function () { if (state === "tiles") { refreshGrid(); } }, 10000);
  } else {
    $("bar-back").addEventListener("click", function () { post("/api/back"); });
  }
  connect();
})();

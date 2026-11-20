// Rückfrage vor Formularen mit data-confirm.
document.addEventListener('submit', function (e) {
  var msg = e.target.getAttribute('data-confirm');
  if (msg && !window.confirm(msg)) {
    e.preventDefault();
  }
});

// HMI-Formular: Die Auswahl der Art zeigt nur die passenden Felder. Der verborgene Teil
// ist abgeschaltet, damit seine Pflichtfelder das Absenden nicht aufhalten.
document.addEventListener('change', function (e) {
  if (!e.target.matches('[data-kind-select]')) {
    return;
  }
  var kind = e.target.value;
  var parts = e.target.form.querySelectorAll('[data-kind]');
  for (var i = 0; i < parts.length; i++) {
    var on = parts[i].getAttribute('data-kind') === kind;
    parts[i].hidden = !on;
    parts[i].disabled = !on;
  }
});

// Formulare mit data-once (lange Wartezeit, z. B. „Karte vorhalten“): Nach dem Absenden ist der Knopf gesperrt und
// ein zweites Absenden (etwa Enter des Kartenlesers auf dem fokussierten Knopf) fällt weg.
document.addEventListener('submit', function (e) {
  var f = e.target;
  if (!f.hasAttribute || !f.hasAttribute('data-once')) {
    return;
  }
  if (f.getAttribute('data-busy')) {
    e.preventDefault();
    return;
  }
  if (e.defaultPrevented) {
    return;
  }
  setTimeout(function () {
    f.setAttribute('data-busy', '1');
    var b = f.querySelectorAll('button');
    for (var i = 0; i < b.length; i++) {
      b[i].disabled = true;
    }
  }, 0);
});
window.addEventListener('pageshow', function () {
  var fs = document.querySelectorAll('form[data-busy]');
  for (var i = 0; i < fs.length; i++) {
    fs[i].removeAttribute('data-busy');
    var b = fs[i].querySelectorAll('button');
    for (var j = 0; j < b.length; j++) {
      b[j].disabled = false;
    }
  }
});

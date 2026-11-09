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

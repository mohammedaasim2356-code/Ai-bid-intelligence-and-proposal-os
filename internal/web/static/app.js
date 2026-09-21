// BidOS UI behaviour: theme toggle, confirms, busy forms, pipeline polling. No framework.
(function () {
  var root = document.documentElement;
  function setTheme(t) {
    root.setAttribute('data-theme', t);
    try { localStorage.setItem('bidos-theme', t); } catch (e) {}
    document.querySelectorAll('[data-theme-set]').forEach(function (b) {
      b.classList.toggle('active', b.getAttribute('data-theme-set') === t);
    });
  }
  document.querySelectorAll('[data-theme-set]').forEach(function (b) {
    b.addEventListener('click', function () { setTheme(b.getAttribute('data-theme-set')); });
  });
  setTheme(root.getAttribute('data-theme') || 'dark');

  document.querySelectorAll('form[data-confirm]').forEach(function (f) {
    f.addEventListener('submit', function (e) { if (!confirm(f.getAttribute('data-confirm'))) e.preventDefault(); });
  });
  document.querySelectorAll('button[data-confirm]').forEach(function (b) {
    b.addEventListener('click', function (e) { if (!confirm(b.getAttribute('data-confirm'))) e.preventDefault(); });
  });
  document.querySelectorAll('form[data-busy]').forEach(function (f) {
    f.addEventListener('submit', function () {
      f.classList.add('busy');
      var btn = f.querySelector('button[type=submit]:not([name])') || f.querySelector('button[type=submit]');
      if (btn) { btn.dataset.label = btn.textContent; btn.innerHTML = '<span class="spinner"></span> ' + f.getAttribute('data-busy'); }
    });
  });

  // Poll an active pipeline run and reload when its stage or status changes.
  var pipe = document.getElementById('pipeline');
  if (pipe && pipe.dataset.run) {
    var status = pipe.dataset.runStatus;
    if (status === 'queued' || status === 'running') {
      var last = null;
      var tick = function () {
        fetch('/api/runs/' + pipe.dataset.run, { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (j) {
          var key = j.status + ':' + j.stage;
          if (last && key !== last) { location.reload(); return; }
          last = last || key;
          if (j.status === 'queued' || j.status === 'running') setTimeout(tick, 2500); else location.reload();
        }).catch(function () { setTimeout(tick, 5000); });
      };
      setTimeout(tick, 2500);
    }
  }
  // Poll a running job (evals) and reload when done.
  var job = document.querySelector('[data-job]');
  if (job) {
    var poll = function () {
      fetch('/api/jobs/' + job.dataset.job, { credentials: 'same-origin' }).then(function (r) { return r.json(); }).then(function (j) {
        if (j.status === 'queued' || j.status === 'running') setTimeout(poll, 3000); else location.reload();
      }).catch(function () { setTimeout(poll, 6000); });
    };
    setTimeout(poll, 3000);
  }
  // Keyboard: j/k move through the requirement list.
  var rows = Array.prototype.slice.call(document.querySelectorAll('.req-rows li a'));
  if (rows.length) {
    document.addEventListener('keydown', function (e) {
      if (e.target.matches('input,textarea,select')) return;
      if (e.key !== 'j' && e.key !== 'k') return;
      var i = rows.findIndex(function (a) { return a.parentNode.classList.contains('selected'); });
      var n = e.key === 'j' ? i + 1 : i - 1;
      if (rows[n]) location.href = rows[n].href;
    });
  }
})();

(function () {
  'use strict';

  function sync(form) {
    if (!form) {
      return;
    }
    var shared = form.querySelector('input[name="split_mode"][value="shared"]');
    var fields = form.querySelector('[data-recurring-shared-fields]');
    if (!shared || !fields) {
      return;
    }
    fields.hidden = !shared.checked;
  }

  document.addEventListener('change', function (event) {
    if (!event.target || !event.target.matches || !event.target.matches('[data-recurring-split-mode]')) {
      return;
    }
    sync(event.target.closest('form'));
  });

  document.addEventListener('htmx:afterSwap', function (event) {
    var target = event.detail && event.detail.target;
    if (!target || !target.querySelector) {
      return;
    }
    if (target.querySelector('[data-recurring-shared-fields]')) {
      sync(target.querySelector('[data-recurring-shared-fields]').closest('form'));
    }
  });
})();

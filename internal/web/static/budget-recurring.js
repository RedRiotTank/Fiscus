(function () {
  'use strict';

  function syncSplit(form) {
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

  function syncCategories(form) {
    if (!form) {
      return;
    }
    var pillar = form.querySelector('[data-recurring-pillar]');
    var select = form.querySelector('[data-recurring-category]');
    if (!pillar || !select) {
      return;
    }
    var options;
    try {
      options = JSON.parse(select.dataset.recurringOptions || '[]');
    } catch (error) {
      return;
    }
    if (!Array.isArray(options) || options.length === 0) {
      return;
    }
    var current = select.value;
    var matching = options.filter(function (option) {
      return option.pillar === pillar.value;
    });
    if (matching.length === 0) {
      matching = options.slice();
    }
    select.innerHTML = '';
    matching.forEach(function (option) {
      var element = document.createElement('option');
      element.value = option.name;
      element.textContent = option.name;
      element.dataset.pillar = option.pillar;
      if (option.name === current) {
        element.selected = true;
      }
      select.appendChild(element);
    });
    if (!select.value && select.options.length > 0) {
      select.options[0].selected = true;
    }
  }

  function sync(form) {
    syncSplit(form);
    syncCategories(form);
  }

  document.addEventListener('change', function (event) {
    if (!event.target || !event.target.matches) {
      return;
    }
    if (event.target.matches('[data-recurring-split-mode]') ||
        event.target.matches('[data-recurring-pillar]')) {
      sync(event.target.closest('form'));
    }
  });

  function init(scope) {
    if (!scope || !scope.querySelector) {
      return;
    }
    var form = scope.querySelector('form');
    if (form && form.querySelector('[data-recurring-category]')) {
      sync(form);
    }
  }

  document.addEventListener('htmx:afterSwap', function (event) {
    init(event.detail && event.detail.target ? event.detail.target : document);
  });
  document.addEventListener('htmx:afterSettle', function (event) {
    init(event.detail && event.detail.target ? event.detail.target : document);
  });
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { init(document); });
  } else {
    init(document);
  }
})();

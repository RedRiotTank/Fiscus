(function () {
  'use strict';

  function shell() {
    return document.getElementById('transaction-search');
  }

  function input() {
    var root = shell();
    return root ? root.querySelector('[data-search-input]') : null;
  }

  function toggle() {
    var root = shell();
    return root ? root.querySelector('[data-search-toggle]') : null;
  }

  function setExpanded(expanded, focusInput) {
    var root = shell();
    if (!root) {
      return;
    }
    root.classList.toggle('expanded', expanded);
    var button = toggle();
    if (button) {
      button.setAttribute('aria-expanded', expanded ? 'true' : 'false');
    }
    if (expanded && focusInput) {
      var field = input();
      if (field) {
        field.focus();
        var length = field.value.length;
        try {
          field.setSelectionRange(length, length);
        } catch (error) {
          // Search inputs support selection in all modern browsers.
        }
      }
    }
  }

  document.addEventListener('click', function (event) {
    if (event.target && event.target.closest && event.target.closest('[data-search-toggle]')) {
      var root = shell();
      if (root) {
        setExpanded(!root.classList.contains('expanded'), true);
      }
      return;
    }
    var root = shell();
    if (!root || !root.classList.contains('expanded')) {
      return;
    }
    var field = input();
    if (field && root.contains(event.target)) {
      return;
    }
    if (!field || field.value.trim() === '') {
      setExpanded(false, false);
    }
  });

  document.addEventListener('keydown', function (event) {
    if (event.key !== 'Escape') {
      return;
    }
    var root = shell();
    if (!root || !root.classList.contains('expanded')) {
      return;
    }
    var field = input();
    if (!field || field.value.trim() === '') {
      setExpanded(false, false);
      return;
    }
    field.blur();
  });
})();

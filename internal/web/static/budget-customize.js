(function () {
  'use strict';

  function dialog() {
    return document.getElementById('budget-customize-dialog');
  }

  function openDialog() {
    var el = dialog();
    if (el && !el.open) {
      el.showModal();
    }
  }

  function closeDialog() {
    var el = dialog();
    if (el && el.open) {
      el.close();
    }
  }

  document.addEventListener('click', function (event) {
    if (event.target && event.target.closest && event.target.closest('[data-budget-customize-close]')) {
      closeDialog();
      return;
    }
    if (event.target && event.target.id === 'budget-customize-dialog') {
      closeDialog();
    }
  });

  document.addEventListener('htmx:afterSwap', function (event) {
    if (event.detail && event.detail.target && event.detail.target.id === 'budget-customize-content') {
      openDialog();
    }
  });

  document.addEventListener('htmx:afterSettle', function (event) {
    if (event.detail && event.detail.target && event.detail.target.id === 'budget-customize-content') {
      openDialog();
    }
  });
})();

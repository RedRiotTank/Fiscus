(function () {
  'use strict';

  function dialog() {
    return document.getElementById('accruing-account-dialog');
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
    if (event.target && event.target.closest && event.target.closest('[data-accruing-close]')) {
      closeDialog();
      return;
    }
    if (event.target && event.target.id === 'accruing-account-dialog') {
      closeDialog();
    }
  });

  document.addEventListener('htmx:afterSwap', function (event) {
    if (event.detail && event.detail.target && event.detail.target.id === 'accruing-account-content') {
      openDialog();
    }
  });

  document.addEventListener('htmx:afterSettle', function (event) {
    if (event.detail && event.detail.target && event.detail.target.id === 'accruing-account-content') {
      openDialog();
    }
  });
})();

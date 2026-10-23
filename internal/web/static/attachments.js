(() => {
  const dialog = document.getElementById('receipt-dialog');
  if (!dialog || typeof dialog.showModal !== 'function') {
    return;
  }
  const form = document.getElementById('receipt-form');
  const transactionInput = document.getElementById('receipt-transaction-id');
  const target = document.getElementById('receipt-target');
  const fileInput = document.getElementById('receipt-file');
  const dropZone = document.getElementById('receipt-drop');

  const open = (transactionID) => {
    if (transactionInput) transactionInput.value = transactionID || '';
    if (target) {
      target.textContent = transactionID
        ? 'This receipt will be linked to transaction #' + transactionID + ' immediately.'
        : 'No transaction selected. Fiscus will auto-pair by date and amount.';
    }
    dialog.showModal();
  };

  document.addEventListener('click', (event) => {
    const opener = event.target.closest('[data-receipt-open]');
    if (opener) {
      event.preventDefault();
      open(opener.dataset.transactionId || '');
      return;
    }
    if (event.target.closest('[data-receipt-close]')) {
      event.preventDefault();
      dialog.close();
    }
  });

  dialog.addEventListener('click', (event) => {
    if (event.target === dialog) dialog.close();
  });

  if (dropZone && fileInput) {
    ['dragenter', 'dragover'].forEach((name) => {
      dropZone.addEventListener(name, (event) => {
        event.preventDefault();
        dropZone.classList.add('dragging');
      });
    });
    ['dragleave', 'drop'].forEach((name) => {
      dropZone.addEventListener(name, (event) => {
        event.preventDefault();
        dropZone.classList.remove('dragging');
      });
    });
    dropZone.addEventListener('drop', (event) => {
      if (event.dataTransfer && event.dataTransfer.files.length > 0) {
        fileInput.files = event.dataTransfer.files;
      }
    });
  }

  if (form) {
    form.addEventListener('submit', () => {
      const submit = form.querySelector('button[type="submit"]');
      if (submit) {
        submit.disabled = true;
        submit.textContent = 'Storing…';
      }
    });
  }
})();

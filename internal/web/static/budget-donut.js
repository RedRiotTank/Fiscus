(function () {
  'use strict';

  var currencySymbols = {
    EUR: '€',
    USD: '$',
    GBP: '£',
    CHF: 'CHF',
    MXN: '$',
    ARS: '$'
  };

  function formatMoney(minor, currency) {
    var value = (Number(minor) || 0) / 100;
    var formatted = new Intl.NumberFormat(undefined, {
      minimumFractionDigits: 2,
      maximumFractionDigits: 2
    }).format(value);
    return formatted + ' ' + (currencySymbols[currency] || currency);
  }

  function spentColor(spent, budget, base) {
    if (!budget || budget <= 0) {
      return '#fb7185';
    }
    var ratio = spent / budget;
    if (ratio >= 1.2) {
      return '#fb7185';
    }
    if (ratio > 1) {
      return '#fbbf24';
    }
    return base;
  }

  function initBudgetDonut(root) {
    if (typeof Chart === 'undefined') {
      return;
    }
    var canvas = root && root.querySelector
      ? root.querySelector('#budget-donut')
      : document.getElementById('budget-donut');
    if (!canvas || canvas.dataset.donutInitialized === 'true') {
      return;
    }

    var data;
    try {
      data = JSON.parse(canvas.dataset.budgetDonut || '{}');
    } catch (error) {
      return;
    }
    if (!Array.isArray(data.labels) || !Array.isArray(data.budgets) ||
        !Array.isArray(data.spent) || !Array.isArray(data.colors)) {
      return;
    }

    var panel = getComputedStyle(document.documentElement).getPropertyValue('--panel').trim() || '#0f1720';
    var spentColors = data.colors.map(function (color, index) {
      return spentColor(data.spent[index], data.budgets[index], color);
    });
    // Keep zero-spend categories as negligible inner arcs so the remaining
    // slices stay radially aligned with the outer budget ring.
    var spentData = data.spent.map(function (value) {
      return value > 0 ? value : 0.0001;
    });

    canvas.dataset.donutInitialized = 'true';
    new Chart(canvas, {
      type: 'doughnut',
      data: {
        labels: data.labels,
        datasets: [
          {
            label: 'Planned budget',
            data: data.budgets,
            backgroundColor: data.colors,
            borderColor: panel,
            borderWidth: 2,
            spacing: 1,
            hoverOffset: 4
          },
          {
            label: 'Month-to-date spent',
            data: spentData,
            backgroundColor: spentColors,
            borderColor: panel,
            borderWidth: 2,
            spacing: 1,
            hoverOffset: 4
          }
        ]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        cutout: '42%',
        animation: false,
        plugins: {
          legend: {
            display: false
          },
          tooltip: {
            backgroundColor: 'rgba(7, 11, 16, 0.96)',
            titleColor: '#e7eef6',
            bodyColor: '#b8c5d3',
            borderColor: 'rgba(148, 163, 184, 0.35)',
            borderWidth: 1,
            padding: 10,
            callbacks: {
              title: function (items) {
                return 'Category: ' + items[0].label;
              },
              label: function (context) {
                var index = context.dataIndex;
                var spent = data.spent[index] || 0;
                var budget = data.budgets[index] || 0;
                var percent = budget > 0 ? Math.round(spent * 100 / budget) : 0;
                return 'Spent: ' + formatMoney(spent, data.currency) +
                  ' / Budget: ' + formatMoney(budget, data.currency) +
                  ' (' + percent + '%)';
              }
            }
          }
        }
      }
    });
  }

  function initAll() {
    initBudgetDonut(document);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', initAll);
  } else {
    initAll();
  }

  document.addEventListener('htmx:afterSwap', function (event) {
    initBudgetDonut(event.detail && event.detail.target ? event.detail.target : document);
  });
  document.addEventListener('htmx:afterSettle', function (event) {
    initBudgetDonut(event.detail && event.detail.target ? event.detail.target : document);
  });
})();

(function () {
  'use strict';

  var storageKey = 'fiscus-budget-chart-view';
  var chartInstances = new WeakMap();

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

  function parsePayload(root) {
    var raw = root.dataset.budgetChart || '';
    if (!raw) {
      var canvas = root.querySelector ? root.querySelector('#budget-donut') : null;
      raw = canvas && canvas.dataset.budgetDonut ? canvas.dataset.budgetDonut : '';
    }
    if (!raw) {
      return null;
    }
    try {
      var data = JSON.parse(raw);
      if (!Array.isArray(data.pillars) || !Array.isArray(data.categories)) {
        return null;
      }
      return data;
    } catch (error) {
      return null;
    }
  }

  function soften(hex) {
    var value = String(hex || '#60a5fa').replace('#', '');
    if (value.length === 3) {
      value = value.split('').map(function (part) { return part + part; }).join('');
    }
    var r = parseInt(value.slice(0, 2), 16) || 96;
    var g = parseInt(value.slice(2, 4), 16) || 165;
    var b = parseInt(value.slice(4, 6), 16) || 250;
    return 'rgba(' + r + ', ' + g + ', ' + b + ', 0.34)';
  }

  function overBudgetColor(category) {
    if (category.budget > 0 && category.spent > category.budget) {
      return '#fb7185';
    }
    return category.color;
  }

  function preferredView(root) {
    var query = '';
    try {
      query = new URLSearchParams(window.location.search).get('budget_view') || '';
    } catch (error) {
      query = '';
    }
    if (query === 'bars' || query === 'donut') {
      return query;
    }
    try {
      var stored = window.localStorage.getItem(storageKey);
      if (stored === 'bars' || stored === 'donut') {
        return stored;
      }
    } catch (error) {
      // localStorage can be unavailable in hardened browser profiles.
    }
    return root.dataset.budgetView === 'bars' ? 'bars' : 'donut';
  }

  function panel(root, name) {
    return root.querySelector('[data-budget-view-panel="' + name + '"]');
  }

  function renderLegend(root, pillars) {
    var legend = root.querySelector('[data-budget-macro-legend]');
    if (!legend) {
      return;
    }
    legend.innerHTML = '';
    pillars.forEach(function (pillar) {
      var item = document.createElement('span');
      var swatch = document.createElement('i');
      swatch.className = 'budget-legend-swatch';
      swatch.style.background = pillar.color;
      item.appendChild(swatch);
      item.appendChild(document.createTextNode(pillar.label));
      legend.appendChild(item);
    });
  }

  function buildDonut(root, payload) {
    var canvas = root.querySelector('#budget-donut');
    if (!canvas || typeof Chart === 'undefined') {
      return null;
    }
    var panelColor = getComputedStyle(document.documentElement).getPropertyValue('--panel').trim() || '#0f1720';
    return new Chart(canvas, {
      type: 'doughnut',
      data: {
        labels: payload.pillars.map(function (pillar) { return pillar.label; }),
        datasets: [{
          label: 'Macro allocation',
          data: payload.pillars.map(function (pillar) { return pillar.allocated; }),
          backgroundColor: payload.pillars.map(function (pillar) { return pillar.color; }),
          borderColor: panelColor,
          borderWidth: 2,
          spacing: 2,
          hoverOffset: 5
        }]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        cutout: '68%',
        animation: false,
        plugins: {
          legend: { display: false },
          tooltip: {
            backgroundColor: 'rgba(7, 11, 16, 0.96)',
            titleColor: '#e7eef6',
            bodyColor: '#b8c5d3',
            borderColor: 'rgba(148, 163, 184, 0.35)',
            borderWidth: 1,
            padding: 10,
            callbacks: {
              label: function (context) {
                var pillar = payload.pillars[context.dataIndex] || {};
                var share = payload.baseline_minor > 0
                  ? Math.round((pillar.allocated || 0) * 100 / payload.baseline_minor)
                  : 0;
                return 'Allocated ' + formatMoney(pillar.allocated, payload.currency) +
                  ' (' + share + '%) · spent ' + formatMoney(pillar.spent, payload.currency);
              }
            }
          }
        }
      }
    });
  }

  var paceMarkerPlugin = {
    id: 'budgetPaceMarker',
    afterDatasetsDraw: function (chart, args, options) {
      if (!options || !options.enabled) {
        return;
      }
      var area = chart.chartArea;
      var pace = Number(options.pace) || 0;
      if (!area || pace <= 0) {
        return;
      }
      var x = area.left + (area.right - area.left) * Math.min(pace, 100) / 100;
      var context = chart.ctx;
      context.save();
      context.beginPath();
      context.setLineDash([5, 4]);
      context.lineWidth = 1.5;
      context.strokeStyle = 'rgba(148, 163, 184, 0.72)';
      context.moveTo(x, area.top);
      context.lineTo(x, area.bottom);
      context.stroke();
      context.setLineDash([]);
      context.fillStyle = '#cbd5e1';
      context.font = '600 10px system-ui, sans-serif';
      context.textAlign = x > area.right - 90 ? 'right' : 'left';
      context.fillText(options.label || '', x + (context.textAlign === 'right' ? -6 : 6), area.top + 12);
      context.restore();
    }
  };

  function buildBars(root, payload) {
    var canvas = root.querySelector('#budget-bars');
    if (!canvas || typeof Chart === 'undefined') {
      return null;
    }
    var labels = payload.categories.map(function (category) { return category.label; });
    var planned = payload.categories.map(function (category) { return category.budget; });
    var actual = payload.categories.map(function (category) { return category.spent; });
    var actualColors = payload.categories.map(overBudgetColor);
    return new Chart(canvas, {
      type: 'bar',
      data: {
        labels: labels,
        datasets: [
          {
            label: 'Planned cap',
            data: planned,
            backgroundColor: payload.categories.map(function (category) { return soften(category.color); }),
            borderColor: payload.categories.map(function (category) { return category.color; }),
            borderWidth: 1,
            borderRadius: 4,
            borderSkipped: false
          },
          {
            label: 'Actual spent',
            data: actual,
            backgroundColor: actualColors,
            borderColor: actualColors,
            borderWidth: 0,
            borderRadius: 4,
            borderSkipped: false
          }
        ]
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        interaction: { mode: 'index', intersect: false },
        scales: {
          x: {
            grid: { display: false },
            ticks: { color: '#94a3b8', maxRotation: 45, minRotation: 0, autoSkip: false }
          },
          y: {
            beginAtZero: true,
            grid: { color: 'rgba(148, 163, 184, 0.12)' },
            ticks: {
              color: '#94a3b8',
              callback: function (value) { return formatMoney(value, payload.currency); }
            }
          }
        },
        plugins: {
          legend: {
            display: true,
            position: 'bottom',
            labels: { color: '#b8c5d3', boxWidth: 12, boxHeight: 12, usePointStyle: true }
          },
          tooltip: {
            backgroundColor: 'rgba(7, 11, 16, 0.96)',
            titleColor: '#e7eef6',
            bodyColor: '#b8c5d3',
            borderColor: 'rgba(148, 163, 184, 0.35)',
            borderWidth: 1,
            padding: 10,
            callbacks: {
              label: function (context) {
                return context.dataset.label + ': ' + formatMoney(context.parsed.y, payload.currency);
              },
              afterBody: function (items) {
                var category = payload.categories[items[0].dataIndex] || {};
                var percent = category.budget > 0 ? Math.round(category.spent * 100 / category.budget) : 0;
                return percent > 100 ? 'Over cap by ' + (percent - 100) + '%' : percent + '% of planned cap';
              }
            }
          },
          budgetPaceMarker: {
            enabled: true,
            pace: payload.pace_percent,
            label: 'Day ' + payload.day_of_month + ' · ' + payload.pace_percent + '% elapsed'
          }
        }
      },
      plugins: [paceMarkerPlugin]
    });
  }

  function applyView(root, view, persist) {
    var next = view === 'bars' ? 'bars' : 'donut';
    root.dataset.budgetView = next;
    var donutPanel = panel(root, 'donut');
    var barsPanel = panel(root, 'bars');
    if (donutPanel) {
      donutPanel.hidden = next !== 'donut';
    }
    if (barsPanel) {
      barsPanel.hidden = next !== 'bars';
    }
    var toggle = root.querySelector('[data-budget-view-toggle]');
    if (toggle) {
      var barMode = next === 'bars';
      toggle.setAttribute('aria-pressed', barMode ? 'true' : 'false');
      toggle.setAttribute('title', barMode ? 'Switch to macro donut' : 'Switch to plan vs actual bars');
    }
    if (persist) {
      try {
        window.localStorage.setItem(storageKey, next);
      } catch (error) {
        // Preference persistence is best-effort.
      }
    }
  }

  function initBudgetCharts(root) {
    if (!root || !root.matches || !root.matches('[data-budget-chart]')) {
      return;
    }
    if (chartInstances.has(root)) {
      return;
    }
    if (typeof Chart === 'undefined') {
      return;
    }
    var payload = parsePayload(root);
    if (!payload) {
      return;
    }

    var instances = {};
    chartInstances.set(root, instances);
    renderLegend(root, payload.pillars);

    var toggle = root.querySelector('[data-budget-view-toggle]');
    if (toggle) {
      toggle.addEventListener('click', function () {
        var next = root.dataset.budgetView === 'bars' ? 'donut' : 'bars';
        applyView(root, next, true);
        ensureChart(root, payload, instances, next);
      });
    }

    var initial = preferredView(root);
    applyView(root, initial, false);
    ensureChart(root, payload, instances, initial);
  }

  function ensureChart(root, payload, instances, view) {
    if (view === 'bars' && !instances.bars) {
      instances.bars = buildBars(root, payload);
      return;
    }
    if (view === 'donut' && !instances.donut) {
      instances.donut = buildDonut(root, payload);
    }
  }

  function initAll(scope) {
    var root = scope || document;
    if (root.matches && root.matches('[data-budget-chart]')) {
      initBudgetCharts(root);
      return;
    }
    if (!root.querySelectorAll) {
      return;
    }
    root.querySelectorAll('[data-budget-chart]').forEach(initBudgetCharts);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { initAll(document); });
  } else {
    initAll(document);
  }

  document.addEventListener('htmx:afterSwap', function (event) {
    initAll(event.detail && event.detail.target ? event.detail.target : document);
  });
  document.addEventListener('htmx:afterSettle', function (event) {
    initAll(event.detail && event.detail.target ? event.detail.target : document);
  });
})();

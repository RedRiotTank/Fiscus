(function () {
  "use strict";

  const A = window.FiscusAnalytics;
  if (!A) {
    return;
  }

function initProjections() {
    const canvas = document.getElementById("projection-chart");
    if (!canvas) {
      return;
    }
    const horizonButtons = Array.from(document.querySelectorAll("[data-projection-horizon]"));
    const extraInput = document.getElementById("projection-extra");
    const yieldInput = document.getElementById("projection-yield");
    const extraOutput = document.getElementById("projection-extra-value");
    const yieldOutput = document.getElementById("projection-yield-value");
    const summary = document.getElementById("projection-summary");
    let horizon = "1y";
    let payload = null;
    let chart = null;
    let timer = null;
    let currency = "EUR";
    let minorScale = 2;

    function updateControlLabels() {
      const extra = Number(extraInput ? extraInput.value : 0);
      const yieldValue = Number(yieldInput ? yieldInput.value : 0);
      if (extraOutput) {
        extraOutput.textContent = A.formatMoney(Math.round(extra * Math.pow(10, minorScale)), currency, minorScale);
      }
      if (yieldOutput) {
        yieldOutput.textContent = yieldValue.toFixed(2).replace(/0$/, "") + "%";
      }
    }

    function render() {
      if (!payload || !payload.horizons) {
        return;
      }
      const projection = payload.horizons[horizon];
      if (!projection) {
        return;
      }
      const history = payload.history || [];
      const labels = history.map((point) => point.label).concat(projection.labels.slice(1));
      const last = history.length - 1;
      const actual = history.map((point) => point.balance_minor).concat(projection.labels.slice(1).map(() => null));
      const baseline = history.map(function (point, index) {
        return index === last ? point.balance_minor : null;
      }).concat(projection.baseline.slice(1));
      const optimistic = history.map(function (point, index) {
        return index === last ? point.balance_minor : null;
      }).concat(projection.optimistic.slice(1));
      const conservative = history.map(function (point, index) {
        return index === last ? point.balance_minor : null;
      }).concat(projection.conservative.slice(1));
      if (chart) {
        chart.destroy();
      }
      A.setEmpty("projection-chart", "projection-chart-empty", false);
      chart = new Chart(canvas, {
        type: "line",
        data: {
          labels: labels,
          datasets: [
            {
              label: "Actual balance",
              data: actual,
              borderColor: "#34d399",
              backgroundColor: "rgba(52, 211, 153, 0.08)",
              borderWidth: 2.5,
              pointRadius: 0,
              tension: 0.22,
              fill: false
            },
            {
              label: "Conservative",
              data: conservative,
              borderColor: "rgba(251, 191, 36, 0)",
              pointRadius: 0,
              fill: false
            },
            {
              label: "Optimistic / conservative band",
              data: optimistic,
              borderColor: "rgba(56, 189, 248, 0)",
              backgroundColor: "rgba(56, 189, 248, 0.14)",
              pointRadius: 0,
              fill: "-1"
            },
            {
              label: "Median projection",
              data: baseline,
              borderColor: "#38bdf8",
              borderWidth: 2.5,
              borderDash: [7, 5],
              pointRadius: 0,
              tension: 0.22,
              fill: false
            }
          ]
        },
        options: Object.assign(A.chartBase(), {
          interaction: { mode: "index", intersect: false },
          plugins: {
            legend: {
              labels: {
                boxWidth: 10,
                usePointStyle: true,
                color: "#b8c5d3",
                filter: function (item) { return item.text !== "Conservative"; }
              }
            },
            tooltip: {
              callbacks: {
                label: function (context) {
                  return context.dataset.label + ": " + A.formatMoney(context.raw || 0, payload.currency, payload.minor_scale);
                }
              }
            }
          },
          scales: {
            x: {
              grid: { display: false },
              ticks: { color: "#7f91a5", maxTicksLimit: 12, maxRotation: 0 }
            },
            y: {
              grid: { color: "rgba(34, 48, 65, 0.65)" },
              ticks: {
                color: "#7f91a5",
                callback: function (value) { return A.formatCompact(value, payload.currency, payload.minor_scale); }
              }
            }
          }
        })
      });
      if (summary) {
        summary.textContent = "Now " + A.formatMoney(payload.current_balance_minor, payload.currency, payload.minor_scale) +
          " · projected net savings " + A.formatMoney(payload.effective_mu_minor, payload.currency, payload.minor_scale) +
          "/mo · volatility " + A.formatMoney(payload.sigma_minor, payload.currency, payload.minor_scale);
      }
    }

    async function load() {
      try {
        const extra = Number(extraInput ? extraInput.value : 0);
        const yieldValue = Number(yieldInput ? yieldInput.value : 0);
        const query = new URLSearchParams();
        query.set("extra_monthly_minor", String(Math.round(extra * Math.pow(10, minorScale))));
        query.set("yield_bp", String(Math.round(yieldValue * 100)));
        if (payload && payload.currency) {
          query.set("currency", payload.currency);
        }
        payload = await A.fetchJSON("/api/analytics/projections?" + query.toString());
        currency = payload.currency || currency;
        minorScale = payload.minor_scale == null ? 2 : payload.minor_scale;
        updateControlLabels();
        render();
      } catch (error) {
        A.setEmpty("projection-chart", "projection-chart-empty", true, "Could not load wealth projections.");
        if (summary) {
          summary.textContent = "Projection unavailable";
        }
        console.error(error);
      }
    }

    function scheduleLoad() {
      window.clearTimeout(timer);
      timer = window.setTimeout(load, 180);
    }

    horizonButtons.forEach((button) => {
      button.addEventListener("click", function () {
        horizon = button.getAttribute("data-projection-horizon") || "1y";
        A.activate(horizonButtons, "data-projection-horizon", horizon);
        render();
      });
    });
    if (extraInput) {
      extraInput.addEventListener("input", function () {
        updateControlLabels();
        scheduleLoad();
      });
    }
    if (yieldInput) {
      yieldInput.addEventListener("input", function () {
        updateControlLabels();
        scheduleLoad();
      });
    }
    updateControlLabels();
    load();
  }

  A.register(initProjections);
})();

(function () {
  "use strict";

  const A = window.FiscusAnalytics;
  if (!A) {
    return;
  }

  function initScenarios() {
    const dialog = document.getElementById("scenario-dialog");
    const openButton = document.querySelector("[data-scenario-open]");
    const canvas = document.getElementById("scenario-chart");
    if (!dialog || !openButton || !canvas) {
      return;
    }
    const incomeInput = document.getElementById("scenario-income");
    const expenseInput = document.getElementById("scenario-expense");
    const oneOffInput = document.getElementById("scenario-oneoff");
    const oneOffMonthInput = document.getElementById("scenario-oneoff-month");
    const horizonInput = document.getElementById("scenario-horizon");
    const runButton = document.querySelector("[data-scenario-run]");
    const resetButton = document.querySelector("[data-scenario-reset]");
    const summary = document.getElementById("scenario-summary");
    const incomeOutput = document.getElementById("scenario-income-value");
    const expenseOutput = document.getElementById("scenario-expense-value");
    const oneOffOutput = document.getElementById("scenario-oneoff-value");
    let chart = null;
    let payload = null;
    let currency = "EUR";
    let minorScale = 2;
    let timer = null;

    function toMinor(value) {
      return Math.round(Number(value || 0) * Math.pow(10, minorScale));
    }

    function updateLabels() {
      if (incomeOutput) {
        incomeOutput.textContent = A.formatMoney(toMinor(incomeInput.value), currency, minorScale);
      }
      if (expenseOutput) {
        expenseOutput.textContent = A.formatMoney(toMinor(expenseInput.value), currency, minorScale);
      }
      if (oneOffOutput) {
        oneOffOutput.textContent = A.formatMoney(toMinor(oneOffInput.value), currency, minorScale);
      }
      if (oneOffMonthInput) {
        const months = Number(horizonInput ? horizonInput.value : 12);
        oneOffMonthInput.max = String(months);
        const month = Math.min(Math.max(Number(oneOffMonthInput.value || 1), 1), months);
        oneOffMonthInput.value = String(month);
      }
    }

    function render() {
      if (!payload || !payload.labels) {
        return;
      }
      A.setEmpty("scenario-chart", "scenario-chart-empty", false);
      if (chart) {
        chart.destroy();
      }
      chart = new Chart(canvas, {
        type: "line",
        data: {
          labels: payload.labels,
          datasets: [
            {
              label: "Baseline",
              data: payload.baseline,
              borderColor: "#38bdf8",
              borderWidth: 2,
              borderDash: [7, 5],
              pointRadius: 0,
              tension: 0.22,
              fill: false
            },
            {
              label: "Simulated",
              data: payload.simulated,
              borderColor: "#34d399",
              backgroundColor: "rgba(52, 211, 153, 0.08)",
              borderWidth: 2.5,
              pointRadius: 0,
              tension: 0.22,
              fill: false
            }
          ]
        },
        options: Object.assign(A.chartBase(), {
          interaction: { mode: "index", intersect: false },
          plugins: {
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
                callback: function (value) {
                  return A.formatCompact(value, payload.currency, payload.minor_scale);
                }
              }
            }
          }
        })
      });
      if (summary) {
        const delta = payload.ending_delta_minor;
        summary.textContent =
          "Baseline " + A.formatMoney(payload.ending_baseline_minor, payload.currency, payload.minor_scale) +
          " · scenario " + A.formatMoney(payload.ending_scenario_minor, payload.currency, payload.minor_scale) +
          " · net change " + (delta >= 0 ? "+" : "") +
          A.formatMoney(delta, payload.currency, payload.minor_scale) +
          " · scenario cash flow " + A.formatMoney(payload.scenario_monthly_minor, payload.currency, payload.minor_scale) + "/mo";
      }
    }

    async function load() {
      if (!dialog.open) {
        return;
      }
      updateLabels();
      try {
        payload = await A.postJSON("/api/analytics/scenarios/simulate", {
          currency: currency,
          months: Number(horizonInput ? horizonInput.value : 12),
          income_delta_minor: toMinor(incomeInput ? incomeInput.value : 0),
          expense_delta_minor: toMinor(expenseInput ? expenseInput.value : 0),
          one_off_minor: toMinor(oneOffInput ? oneOffInput.value : 0),
          one_off_month: Number(oneOffMonthInput ? oneOffMonthInput.value : 1),
          annual_yield_bp: 300
        });
        currency = payload.currency || currency;
        minorScale = payload.minor_scale == null ? 2 : payload.minor_scale;
        updateLabels();
        render();
      } catch (error) {
        A.setEmpty("scenario-chart", "scenario-chart-empty", true, "Could not load the scenario projection.");
        if (summary) {
          summary.textContent = "Scenario simulation failed.";
        }
        console.error(error);
      }
    }

    function scheduleLoad() {
      window.clearTimeout(timer);
      timer = window.setTimeout(load, 220);
    }

    openButton.addEventListener("click", function () {
      if (typeof dialog.showModal === "function") {
        dialog.showModal();
      } else {
        dialog.setAttribute("open", "open");
      }
      load();
    });

    [incomeInput, expenseInput, oneOffInput, oneOffMonthInput, horizonInput].forEach(function (input) {
      if (!input) {
        return;
      }
      input.addEventListener("input", function () {
        updateLabels();
        scheduleLoad();
      });
      input.addEventListener("change", function () {
        updateLabels();
        scheduleLoad();
      });
    });
    if (runButton) {
      runButton.addEventListener("click", load);
    }
    if (resetButton) {
      resetButton.addEventListener("click", function () {
        if (incomeInput) incomeInput.value = "0";
        if (expenseInput) expenseInput.value = "0";
        if (oneOffInput) oneOffInput.value = "0";
        if (oneOffMonthInput) oneOffMonthInput.value = "1";
        if (horizonInput) horizonInput.value = "12";
        updateLabels();
        load();
      });
    }

    updateLabels();
  }

  A.register(initScenarios);
})();

(function () {
  "use strict";

  const A = window.FiscusAnalytics;
  if (!A) {
    return;
  }

function initSpending() {
    const canvas = document.getElementById("spending-donut");
    if (!canvas) {
      return;
    }
    const periodButtons = Array.from(document.querySelectorAll("[data-spending-period]"));
    const periodLabel = document.getElementById("spending-period-label");
    let period = "month";
    let donutChart;
    let trendChart;
    let waterfallChart;

    function renderDonut(data) {
      const categories = data.categories || [];
      if (donutChart) {
        donutChart.destroy();
        donutChart = null;
      }
      A.setEmpty("spending-donut", "spending-donut-empty", categories.length === 0);
      if (categories.length === 0) {
        return;
      }
      donutChart = new Chart(canvas, {
        type: "doughnut",
        data: {
          labels: categories.map((item) => item.category),
          datasets: [{
            data: categories.map((item) => item.minor),
            backgroundColor: A.solid,
            borderColor: "#0f1720",
            borderWidth: 2,
            hoverOffset: 7
          }]
        },
        options: Object.assign(A.chartBase(), {
          cutout: "64%",
          plugins: {
            legend: {
              position: "bottom",
              labels: { boxWidth: 9, usePointStyle: true, color: "#b8c5d3" }
            },
            tooltip: {
              callbacks: {
                label: function (context) {
                  const value = Number(context.raw) || 0;
                  const share = data.total_minor > 0 ? (value * 100 / data.total_minor).toFixed(1) : "0.0";
                  return context.label + ": " + A.formatMoney(value, data.currency, data.minor_scale) + " (" + share + "%)";
                }
              }
            }
          }
        })
      });
    }

    function renderTrend(data) {
      const trend = data.trend || [];
      if (trendChart) {
        trendChart.destroy();
        trendChart = null;
      }
      const hasValues = trend.some((point) => point.income_minor !== 0 || point.expense_minor !== 0);
      A.setEmpty("spending-trend", "spending-trend-empty", !hasValues);
      if (!hasValues) {
        return;
      }
      trendChart = new Chart(document.getElementById("spending-trend"), {
        type: "bar",
        data: {
          labels: trend.map((point) => point.label),
          datasets: [
            {
              label: "Income",
              data: trend.map((point) => point.income_minor),
              backgroundColor: "rgba(52, 211, 153, 0.55)",
              borderRadius: 5
            },
            {
              label: "Expenses",
              data: trend.map((point) => point.expense_minor),
              backgroundColor: "rgba(251, 113, 133, 0.55)",
              borderRadius: 5
            }
          ]
        },
        options: Object.assign(A.chartBase(), {
          plugins: {
            legend: { labels: { boxWidth: 10, usePointStyle: true, color: "#b8c5d3" } },
            tooltip: {
              callbacks: {
                label: function (context) {
                  return context.dataset.label + ": " + A.formatMoney(context.raw, data.currency, data.minor_scale);
                }
              }
            }
          },
          scales: {
            x: { grid: { display: false }, ticks: { color: "#7f91a5" } },
            y: {
              grid: { color: "rgba(34, 48, 65, 0.65)" },
              ticks: {
                color: "#7f91a5",
                callback: function (value) { return A.formatCompact(value, data.currency, data.minor_scale); }
              }
            }
          }
        })
      });
    }

    function renderWaterfall(data) {
      const waterfall = data.waterfall || { opening_minor: 0, inflow_minor: 0, outflows: [], closing_minor: 0 };
      const outflows = waterfall.outflows || [];
      const labels = ["Opening", "Inflows"].concat(outflows.map((item) => item.label)).concat(["Closing"]);
      const values = [];
      const colors = [];
      let running = waterfall.opening_minor;
      values.push([0, waterfall.opening_minor]);
      colors.push("rgba(56, 189, 248, 0.72)");
      values.push([running, running + waterfall.inflow_minor]);
      colors.push("rgba(52, 211, 153, 0.70)");
      running += waterfall.inflow_minor;
      outflows.forEach((item) => {
        values.push([running - item.minor, running]);
        colors.push("rgba(251, 113, 133, 0.68)");
        running -= item.minor;
      });
      values.push([0, waterfall.closing_minor]);
      colors.push("rgba(167, 139, 250, 0.74)");

      if (waterfallChart) {
        waterfallChart.destroy();
        waterfallChart = null;
      }
      A.setEmpty("spending-waterfall", "spending-waterfall-empty", false);
      waterfallChart = new Chart(document.getElementById("spending-waterfall"), {
        type: "bar",
        data: {
          labels: labels,
          datasets: [{
            label: "Cash flow",
            data: values,
            backgroundColor: colors,
            borderRadius: 4,
            borderSkipped: false,
            barPercentage: 0.72
          }]
        },
        options: Object.assign(A.chartBase(), {
          plugins: {
            legend: { display: false },
            tooltip: {
              callbacks: {
                label: function (context) {
                  const raw = context.raw;
                  const value = Array.isArray(raw) ? raw[1] - raw[0] : raw;
                  return A.formatMoney(value, data.currency, data.minor_scale);
                }
              }
            }
          },
          scales: {
            x: {
              grid: { display: false },
              ticks: { color: "#7f91a5", maxRotation: 42, minRotation: 0 }
            },
            y: {
              grid: { color: "rgba(34, 48, 65, 0.65)" },
              ticks: {
                color: "#7f91a5",
                callback: function (value) { return A.formatCompact(value, data.currency, data.minor_scale); }
              }
            }
          }
        })
      });
    }

    async function load() {
      try {
        const data = await A.fetchJSON("/api/analytics/spending?period=" + encodeURIComponent(period));
        if (periodLabel) {
          const labels = { month: "Current month", quarter: "Current quarter", ytd: "Year to date" };
          periodLabel.textContent = (labels[period] || "Current month") + " · " + A.formatMoney(data.total_minor, data.currency, data.minor_scale) + " spent";
        }
        renderDonut(data);
        renderTrend(data);
        renderWaterfall(data);
      } catch (error) {
        A.setEmpty("spending-donut", "spending-donut-empty", true, "Could not load spending analytics.");
        A.setEmpty("spending-trend", "spending-trend-empty", true, "Could not load spending analytics.");
        A.setEmpty("spending-waterfall", "spending-waterfall-empty", true, "Could not load spending analytics.");
        console.error(error);
      }
    }

    periodButtons.forEach((button) => {
      button.addEventListener("click", function () {
        period = button.getAttribute("data-spending-period") || "month";
        A.activate(periodButtons, "data-spending-period", period);
        load();
      });
    });
    load();
  }

  A.register(initSpending);
})();

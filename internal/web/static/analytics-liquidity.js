(function () {
  "use strict";

  const A = window.FiscusAnalytics;
  if (!A) {
    return;
  }

function initLiquidity() {
    const canvas = document.getElementById("liquidity-chart");
    if (!canvas) {
      return;
    }
    const buttons = Array.from(document.querySelectorAll("[data-liquidity-days]"));
    let days = 90;
    let chart = null;

    async function load() {
      try {
        const data = await A.fetchJSON("/api/analytics/liquidity?days=" + days);
        if (chart) {
          chart.destroy();
          chart = null;
        }
        const series = data.series || [];
        A.setEmpty("liquidity-chart", "liquidity-chart-empty", series.length === 0);
        if (series.length === 0) {
          return;
        }
        const datasets = series.map(function (item, index) {
          const color = A.palette[index % palette.length];
          return {
            label: item.institution,
            data: item.points,
            borderColor: color.border,
            backgroundColor: color.background,
            borderWidth: 1.5,
            pointRadius: 0,
            tension: 0.22,
            fill: true,
            stack: "liquidity"
          };
        });
        chart = new Chart(canvas, {
          type: "line",
          data: { labels: data.labels, datasets: datasets },
          options: Object.assign(A.chartBase(), {
            interaction: { mode: "index", intersect: false },
            plugins: {
              legend: { labels: { boxWidth: 10, usePointStyle: true, color: "#b8c5d3" } },
              tooltip: {
                callbacks: {
                  label: function (context) {
                    return context.dataset.label + ": " + A.formatMoney(context.raw || 0, data.currency, data.minor_scale);
                  }
                }
              }
            },
            scales: {
              x: {
                grid: { display: false },
                ticks: { color: "#7f91a5", maxTicksLimit: 10, maxRotation: 0 }
              },
              y: {
                stacked: true,
                grid: { color: "rgba(34, 48, 65, 0.65)" },
                ticks: {
                  color: "#7f91a5",
                  callback: function (value) { return A.formatCompact(value, data.currency, data.minor_scale); }
                }
              }
            }
          })
        });
        const current = series.reduce(function (sum, item) {
          const points = item.points || [];
          return sum + (points.length ? points[points.length - 1] : 0);
        }, 0);
        const summary = document.getElementById("liquidity-summary");
        if (summary) {
          summary.textContent = "Current liquidity " + A.formatMoney(current, data.currency, data.minor_scale) + " across " + series.length + " institution" + (series.length === 1 ? "" : "s");
        }
      } catch (error) {
        A.setEmpty("liquidity-chart", "liquidity-chart-empty", true, "Could not load liquidity distribution.");
        const summary = document.getElementById("liquidity-summary");
        if (summary) {
          summary.textContent = "Liquidity unavailable";
        }
        console.error(error);
      }
    }

    buttons.forEach((button) => {
      button.addEventListener("click", function () {
        days = Number(button.getAttribute("data-liquidity-days")) || 90;
        A.activate(buttons, "data-liquidity-days", String(days));
        load();
      });
    });
    load();
  }

  A.register(initLiquidity);
})();

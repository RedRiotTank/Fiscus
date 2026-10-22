(function () {
  "use strict";

  const A = window.FiscusAnalytics;
  if (!A) {
    return;
  }

function initPace() {
    const canvas = document.getElementById("pace-chart");
    if (!canvas) {
      return;
    }
    let chart;
    async function load() {
      try {
        const data = await A.fetchJSON("/api/analytics/pace");
        const labels = Array.from({ length: 31 }, (_, index) => String(index + 1));
        const datasets = (data.baseline || []).map(function (baseline) {
          return {
            label: baseline.label,
            data: baseline.points,
            borderColor: "rgba(148, 163, 184, 0.24)",
            borderWidth: 1,
            pointRadius: 0,
            tension: 0.25,
            fill: false
          };
        });
        datasets.push({
          label: "Six-month average",
          data: data.average,
          borderColor: "rgba(251, 191, 36, 0.80)",
          borderWidth: 2,
          borderDash: [6, 5],
          pointRadius: 0,
          tension: 0.25,
          fill: false
        });
        datasets.push({
          label: "This month",
          data: data.current,
          borderColor: "#38bdf8",
          backgroundColor: "rgba(56, 189, 248, 0.12)",
          borderWidth: 3,
          pointRadius: 0,
          pointHoverRadius: 4,
          tension: 0.25,
          fill: true
        });
        if (chart) {
          chart.destroy();
        }
        A.setEmpty("pace-chart", "pace-chart-empty", false);
        chart = new Chart(canvas, {
          type: "line",
          data: { labels: labels, datasets: datasets },
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
                title: { display: true, text: "Day of month", color: "#7f91a5" },
                grid: { color: "rgba(34, 48, 65, 0.45)" },
                ticks: { color: "#7f91a5" }
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
        const indicator = document.getElementById("pace-velocity");
        if (indicator) {
          indicator.className = "velocity-pill";
          const percent = Math.abs(data.delta_bp || 0) / 100;
          if (data.velocity === "above") {
            indicator.classList.add("velocity-above");
            indicator.textContent = "Above normal pace · +" + percent.toFixed(1) + "%";
          } else if (data.velocity === "below") {
            indicator.classList.add("velocity-below");
            indicator.textContent = "Below normal pace · −" + percent.toFixed(1) + "%";
          } else {
            indicator.classList.add("velocity-ok");
            indicator.textContent = "Tracking near normal pace";
          }
        }
      } catch (error) {
        A.setEmpty("pace-chart", "pace-chart-empty", true, "Could not load spending pace.");
        console.error(error);
      }
    }
    load();
  }

  A.register(initPace);
})();

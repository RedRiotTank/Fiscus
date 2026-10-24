(function () {
  "use strict";

  const A = window.FiscusAnalytics;
  if (!A) {
    return;
  }

function initActivity() {
    const container = document.getElementById("activity-matrix");
    if (!container) {
      return;
    }
    function render(data) {
      container.textContent = "";
      const buckets = data.buckets || [];
      const weekdays = data.weekdays || [];
      let maximum = 0;
      let total = 0;
      buckets.forEach((bucket) => {
        (bucket.counts || []).forEach((count) => {
          maximum = Math.max(maximum, count);
          total += count;
        });
      });
      const summary = document.getElementById("activity-summary");
      if (summary) {
        summary.textContent = (data.transactions || 0) + " outbound transactions · " + (data.currency || "EUR") + " · last 90 days";
      }
      const empty = document.getElementById("activity-empty");
      if (empty) {
        empty.hidden = total !== 0;
      }
      if (total === 0) {
        return;
      }
      const table = document.createElement("table");
      table.className = "matrix-table";
      const thead = document.createElement("thead");
      const headerRow = document.createElement("tr");
      const corner = document.createElement("th");
      corner.scope = "col";
      corner.textContent = "Amount";
      headerRow.appendChild(corner);
      weekdays.forEach((weekday) => {
        const th = document.createElement("th");
        th.scope = "col";
        th.textContent = weekday;
        headerRow.appendChild(th);
      });
      thead.appendChild(headerRow);
      table.appendChild(thead);
      const tbody = document.createElement("tbody");
      buckets.forEach((bucket) => {
        const row = document.createElement("tr");
        const label = document.createElement("th");
        label.scope = "row";
        label.className = "matrix-row-label";
        label.textContent = "€" + bucket.label;
        row.appendChild(label);
        (bucket.counts || []).forEach(function (count, index) {
          const cell = document.createElement("td");
          const ratio = maximum > 0 ? count / maximum : 0;
          const level = count === 0 ? 0 : Math.min(4, Math.max(1, Math.ceil(ratio * 4)));
          cell.className = "matrix-cell heat-" + level;
          cell.textContent = count === 0 ? "·" : String(count);
          cell.title = count + " transactions · " + A.formatMoney((bucket.totals || [])[index] || 0, data.currency, data.minor_scale);
          row.appendChild(cell);
        });
        tbody.appendChild(row);
      });
      table.appendChild(tbody);
      container.appendChild(table);
    }
    A.fetchJSON("/api/analytics/activity").then(render).catch(function (error) {
      const empty = document.getElementById("activity-empty");
      if (empty) {
        empty.hidden = false;
        empty.textContent = "Could not load spending activity.";
      }
      const summary = document.getElementById("activity-summary");
      if (summary) {
        summary.textContent = "Activity unavailable";
      }
      console.error(error);
    });
  }

  A.register(initActivity);
})();

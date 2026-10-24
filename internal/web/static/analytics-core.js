(function () {
  "use strict";

  const palette = [
    { border: "#38bdf8", background: "rgba(56, 189, 248, 0.32)" },
    { border: "#34d399", background: "rgba(52, 211, 153, 0.30)" },
    { border: "#fbbf24", background: "rgba(251, 191, 36, 0.28)" },
    { border: "#fb7185", background: "rgba(251, 113, 133, 0.28)" },
    { border: "#a78bfa", background: "rgba(167, 139, 250, 0.28)" },
    { border: "#22d3ee", background: "rgba(34, 211, 238, 0.28)" },
    { border: "#f472b6", background: "rgba(244, 114, 182, 0.26)" },
    { border: "#94a3b8", background: "rgba(148, 163, 184, 0.26)" }
  ];

  function formatMoney(minor, currency, scale) {
    const divisor = Math.pow(10, scale == null ? 2 : scale);
    const value = Number(minor) / divisor;
    try {
      return new Intl.NumberFormat(undefined, {
        style: "currency",
        currency: currency || "EUR",
        maximumFractionDigits: scale == null ? 2 : scale
      }).format(value);
    } catch (_) {
      return value.toFixed(scale == null ? 2 : scale) + " " + (currency || "EUR");
    }
  }

  function formatCompact(minor, currency, scale) {
    const divisor = Math.pow(10, scale == null ? 2 : scale);
    const value = Number(minor) / divisor;
    try {
      return new Intl.NumberFormat(undefined, {
        style: "currency",
        currency: currency || "EUR",
        notation: "compact",
        maximumFractionDigits: 1
      }).format(value);
    } catch (_) {
      return formatMoney(minor, currency, scale);
    }
  }

  async function fetchJSON(url) {
    const response = await fetch(url, {
      credentials: "same-origin",
      headers: { Accept: "application/json" }
    });
    if (!response.ok) {
      throw new Error("Request failed with status " + response.status);
    }
    return response.json();
  }

  async function postJSON(url, payload) {
    const response = await fetch(url, {
      method: "POST",
      credentials: "same-origin",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json"
      },
      body: JSON.stringify(payload || {})
    });
    if (!response.ok) {
      throw new Error("Request failed with status " + response.status);
    }
    return response.json();
  }

  function setEmpty(canvasId, emptyId, empty, message) {
    const canvas = document.getElementById(canvasId);
    const note = document.getElementById(emptyId);
    if (canvas) {
      canvas.hidden = empty;
    }
    if (note) {
      if (message) {
        note.textContent = message;
      }
      note.hidden = !empty;
    }
  }

  function activate(buttons, attributeName, value) {
    buttons.forEach((button) => {
      const selected = button.getAttribute(attributeName) === value;
      button.classList.toggle("active", selected);
      button.setAttribute("aria-pressed", selected ? "true" : "false");
    });
  }

  function chartBase() {
    return {
      responsive: true,
      maintainAspectRatio: false,
      animation: { duration: 350 },
      plugins: {
        legend: {
          labels: { boxWidth: 10, usePointStyle: true, color: "#b8c5d3" }
        }
      }
    };
  }

  let initialized = false;

  function runInitializer(initializer) {
    try {
      initializer();
    } catch (error) {
      console.error("Fiscus analytics initializer failed", error);
    }
  }

  function markUnavailable(message) {
    ["pace-velocity", "activity-summary", "projection-summary", "liquidity-summary", "scenario-summary"]
      .forEach(function (id) {
        const element = document.getElementById(id);
        if (element && /Loading|^$/.test(element.textContent.trim())) {
          element.textContent = message;
        }
      });
  }

  const api = {
    palette: palette,
    solid: palette.map((item) => item.border),
    formatMoney: formatMoney,
    formatCompact: formatCompact,
    fetchJSON: fetchJSON,
    postJSON: postJSON,
    setEmpty: setEmpty,
    activate: activate,
    chartBase: chartBase,
    initializers: [],
    register: function (initializer) {
      api.initializers.push(initializer);
      if (initialized) {
        runInitializer(initializer);
      }
    }
  };

  window.FiscusAnalytics = api;

  function init() {
    if (initialized) {
      return;
    }
    if (typeof Chart === "undefined") {
      console.error("Fiscus analytics: Chart.js failed to load");
      markUnavailable("Analytics charts unavailable: Chart.js did not load.");
      return;
    }
    initialized = true;
    Chart.defaults.color = "#b8c5d3";
    Chart.defaults.borderColor = "rgba(34, 48, 65, 0.65)";
    Chart.defaults.font.family = "ui-sans-serif, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif";
    api.initializers.forEach(runInitializer);
  }

  // Deferred scripts execute in order before DOMContentLoaded. Waiting for
  // DOMContentLoaded ensures every analytics module has registered its
  // initializer before Chart.js starts rendering.
  if (document.readyState === "loading" || document.readyState === "interactive") {
    document.addEventListener("DOMContentLoaded", init, { once: true });
  } else {
    init();
  }
  window.addEventListener("load", init, { once: true });
})();

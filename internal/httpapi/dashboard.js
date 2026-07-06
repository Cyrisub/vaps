(function () {
  "use strict";

  function safeStorage(key) {
    return {
      get: function () {
        try { return localStorage.getItem(key); } catch (e) { return null; }
      },
      set: function (value) {
        try { localStorage.setItem(key, value); } catch (e) {}
      },
      clear: function () {
        try { localStorage.removeItem(key); } catch (e) {}
      }
    };
  }

  var themeStore = safeStorage("vaps-theme");

  function apply(theme) {
    if (theme) document.documentElement.setAttribute("data-theme", theme);
    else document.documentElement.removeAttribute("data-theme");
  }

  function getEffective(stored) {
    if (stored) return stored;
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }

  apply(themeStore.get());

  var NAV = [
    { href: "/dashboard", page: "overview", label: "Overview" },
    { href: "/dashboard/errors", page: "errors", label: "Errors" },
    { href: "/dashboard/metadata", page: "metadata", label: "Payloads" },
    { href: "/dashboard/auth", page: "auth", label: "Auth" },
    { href: "/dashboard/telemetry", page: "telemetry", label: "Telemetry" },
    { href: "/dashboard/info", page: "info", label: "Info" }
  ];

  function currentPage() {
    var body = document.body;
    return (body && body.getAttribute("data-page")) || "overview";
  }

  function buildShell() {
    var body = document.body;
    if (!body || body.querySelector("#container")) return;

    var page = currentPage();
    var children = Array.prototype.slice.call(body.childNodes);

    var container = document.createElement("div");
    container.id = "container";

    var banner = document.createElement("a");
    banner.className = "vaps-banner";
    banner.href = "/dashboard";
    banner.innerHTML =
      '<div class="vaps-banner-text">' +
      '  <div class="vaps-banner-wordmark">VAPS<span>STORAGE</span></div>' +
      '  <div class="vaps-banner-tagline">Virtual Asset Payload Storage</div>' +
      "</div>";

    var nav = document.createElement("nav");
    nav.className = "vaps-nav";
    nav.setAttribute("aria-label", "Dashboard");
    NAV.forEach(function (item) {
      var link = document.createElement("a");
      link.href = item.href;
      link.textContent = item.label;
      if (item.page === page) link.className = "active";
      nav.appendChild(link);
    });

    var pageWrap = document.createElement("div");
    pageWrap.className = "vaps-page";
    children.forEach(function (node) {
      pageWrap.appendChild(node);
    });

    container.appendChild(banner);
    container.appendChild(nav);
    container.appendChild(pageWrap);
    body.appendChild(container);
  }

  function createThemeToggle() {
    if (document.getElementById("vaps_theme_toggle")) return;

    var btn = document.createElement("button");
    btn.id = "vaps_theme_toggle";
    btn.className = "vaps-theme-toggle";
    btn.type = "button";
    btn.title = "Toggle theme";

    function updateIcon() {
      var effective = getEffective(themeStore.get());
      btn.textContent = effective === "dark" ? "\u2600" : "\u263E";
      var isManual = themeStore.get() != null;
      btn.title = isManual
        ? "Theme: " + effective + " (click to change, double-click for system)"
        : "Theme: system (click to change)";
    }

    btn.addEventListener("click", function () {
      var effective = getEffective(themeStore.get());
      var next = effective === "dark" ? "light" : "dark";
      themeStore.set(next);
      apply(next);
      updateIcon();
    });

    btn.addEventListener("dblclick", function (e) {
      e.preventDefault();
      themeStore.clear();
      apply(null);
      updateIcon();
    });

    window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", function () {
      if (themeStore.get() == null) {
        apply(null);
        updateIcon();
      }
    });

    updateIcon();
    document.body.appendChild(btn);
  }

  function boot() {
    buildShell();
    createThemeToggle();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();

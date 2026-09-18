/* ============================================================
   Locus VPN — shared layout components
   Injects the brand mark, sticky nav, icon set and footer on
   every page. Pages supply <title> and <main> only; the active
   nav link and section highlighting are driven by
   <body data-page="…">.
   ============================================================ */

(function () {
  "use strict";

  /* ---------------------------------------------------------- *
   * Icon set — one stroke weight and viewBox across the site,
   * matching the client's icon treatment.
   * ---------------------------------------------------------- */

  var ICON_PATHS = {
    mark:
      '<path d="M4 12a8 8 0 1 1 8 8" stroke="currentColor" stroke-width="2" stroke-linecap="round" fill="none"/><circle cx="12" cy="12" r="2" fill="currentColor"/>',
    lock:
      '<rect x="4.5" y="10.5" width="15" height="9.5" rx="2"/><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5"/>',
    shield:
      '<path d="M12 3l7 3v5c0 4.4-3 8-7 10-4-2-7-5.6-7-10V6z"/><path d="M9 12l2 2 4-4"/>',
    route:
      '<circle cx="6" cy="18" r="2.4"/><circle cx="18" cy="6" r="2.4"/><path d="M8 16.5c3-1 5-3 6.5-6"/>',
    globe:
      '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 3 2.5 15 0 18M12 3c-2.5 3-2.5 15 0 18"/>',
    noTrace:
      '<path d="M4 7h16M4 12h10M4 17h7"/><path d="M17 15l4 4M21 15l-4 4"/>',
    code:
      '<path d="M9 8l-4 4 4 4M15 8l4 4-4 4"/>',
    speed:
      '<path d="M4 18a8 8 0 1 1 16 0"/><path d="M12 18l4-5"/>',
    devices:
      '<rect x="3" y="5" width="13" height="10" rx="2"/><path d="M6.5 19h6"/><rect x="17.5" y="9" width="4.5" height="9" rx="1.5"/>',
    leaf:
      '<path d="M5 19c0-8 5-13 14-14 1 9-4 14-12 14H5z"/><path d="M5 19c3-3 6-5 10-6"/>',
    refresh:
      '<path d="M4 12a8 8 0 0 1 13.7-5.7L20 8.5"/><path d="M20 4v4.5h-4.5"/><path d="M20 12a8 8 0 0 1-13.7 5.7L4 15.5"/><path d="M4 20v-4.5h4.5"/>',
    check:
      '<path d="M4 12l5 5L20 6"/>',
    flag:
      '<path d="M6 3v18"/><path d="M6 4h11l-2.5 4L17 12H6z"/>',
    server:
      '<rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01"/>',
    clock:
      '<circle cx="12" cy="12" r="9"/><path d="M12 7.5V12l3 2"/>',
    wallet:
      '<rect x="3" y="6" width="18" height="13" rx="2.5"/><path d="M3 10h18"/><circle cx="16.5" cy="14.5" r="1"/>',
    key:
      '<circle cx="8" cy="15" r="3.2"/><path d="M10.4 12.8L19 4.2M15.6 7.6l2.2 2.2M17.8 5.4l2.2 2.2"/>',
    layers:
      '<path d="M12 3l8 4.5-8 4.5-8-4.5z"/><path d="M4 12l8 4.5 8-4.5"/><path d="M4 16.5L12 21l8-4.5"/>',
    warn:
      '<path d="M12 4l8.5 15h-17z"/><path d="M12 10v4M12 17h.01"/>',
    monitor:
      '<rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/>',
    window:
      '<rect x="3" y="4" width="18" height="16" rx="2.5"/><path d="M3 9h18"/><path d="M6.5 6.5h.01M9 6.5h.01"/>',
    power:
      '<path d="M12 3v9"/><path d="M6.5 6.5a8 8 0 1 0 11 0"/>',
    filter:
      '<path d="M4 6h16M7 12h10M10 18h4"/>',
    bolt:
      '<path d="M13 3L5 13h5l-1 8 8-10h-5z"/>'
  };

  function icon(name) {
    var body = ICON_PATHS[name] || "";
    return (
      '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" ' +
      'stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" ' +
      'aria-hidden="true" focusable="false">' + body + "</svg>"
    );
  }

  // Exposed so page scripts can fill [data-icon] elements.
  window.locusIcon = icon;

  /* ---------------------------------------------------------- *
   * Site chrome
   * ---------------------------------------------------------- */

  var NAV = [
    ["index", "Home", "index.html"],
    ["features", "Features", "features.html"],
    ["client", "Client", "client.html"],
    ["architecture", "Architecture", "architecture.html"],
    ["pricing", "Pricing", "pricing.html"],
    ["faq", "FAQ", "faq.html"]
  ];

  /* Legal pages are not in the primary nav, but they should still
     highlight their parent section rather than showing nothing. */
  var NAV_PARENT = {
    signup: "pricing",
    privacy: "faq",
    terms: "faq",
    refunds: "pricing",
    transparency: "faq"
  };

  function currentPage() {
    return document.body.getAttribute("data-page") || "";
  }

  function navHTML() {
    var page = currentPage();
    var active = NAV_PARENT[page] || page;

    var items = NAV.map(function (l) {
      var attrs = l[0] === active ? ' aria-current="page"' : "";
      return '<li><a href="' + l[2] + '"' + attrs + ">" + l[1] + "</a></li>";
    }).join("");

    return (
      '<header class="site-nav" id="siteNav">' +
        '<div class="wrap site-nav__inner">' +
          '<a class="brand" href="index.html" aria-label="Locus VPN — home">' +
            '<span class="brand-mark">' + icon("mark") + "</span>" +
            "Locus<small>VPN</small>" +
          "</a>" +
          '<nav class="nav-links" id="navLinks" aria-label="Primary">' +
            items +
          "</nav>" +
          '<div class="nav-cta">' +
            '<a class="btn btn--primary btn--sm" href="signup.html">Create an account</a>' +
          "</div>" +
          '<button class="nav-toggle" id="navToggle" type="button" ' +
            'aria-expanded="false" aria-controls="navLinks" aria-label="Open navigation menu">' +
            '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" ' +
            'stroke-linecap="round" aria-hidden="true"><path d="M4 7h16M4 12h16M4 17h16"/></svg>' +
          "</button>" +
        "</div>" +
      "</header>"
    );
  }

  function footerHTML() {
    var y = new Date().getFullYear();

    function col(title, links) {
      var lis = links.map(function (l) {
        return '<li><a href="' + l[1] + '">' + l[0] + "</a></li>";
      }).join("");
      return "<div><h4>" + title + "</h4><ul>" + lis + "</ul></div>";
    }

    return (
      '<footer class="site-footer">' +
        '<div class="wrap">' +
          '<div class="demo-notice">' +
            icon("warn") +
            "<span><strong>Demonstration product.</strong> This site describes a fictional " +
            "service built as a design exercise. Company details, server counts, audits, " +
            "billing and the figures in the transparency report are illustrative and do not " +
            "describe real events, real people or a real service.</span>" +
          "</div>" +
          '<div class="footer-grid">' +
            '<div class="footer-brand">' +
              '<a class="brand" href="index.html">' +
                '<span class="brand-mark">' + icon("mark") + "</span>Locus" +
              "</a>" +
              "<p>An open-source VPN client with published builds and a no-logs policy. " +
              "Operated by Locus Networks Ltd., registered in the Netherlands.</p>" +
            "</div>" +
            col("Product", [
              ["Features", "features.html"],
              ["The client", "client.html"],
              ["Architecture", "architecture.html"],
              ["Pricing", "pricing.html"],
              ["FAQ", "faq.html"]
            ]) +
            col("Legal", [
              ["Privacy policy", "privacy.html"],
              ["Terms of service", "terms.html"],
              ["Refund policy", "refunds.html"],
              ["Transparency report", "transparency.html"]
            ]) +
            "<div><h4>Contact</h4><ul>" +
              '<li><a href="faq.html#contact">Support</a></li>' +
              '<li><a href="signup.html">Create an account</a></li>' +
              '<li><a href="mailto:hello@locus.example">hello@locus.example</a></li>' +
            "</ul></div>" +
          "</div>" +
          '<div class="footer-legal">' +
            "<span>&copy; " + y + " Locus Networks Ltd. Registered in the Netherlands.</span>" +
            "<span>Demonstration site — no accounts are created and no payments are taken.</span>" +
          "</div>" +
        "</div>" +
      "</footer>"
    );
  }

  /* ---------------------------------------------------------- *
   * Behaviour
   * ---------------------------------------------------------- */

  function mountNavState() {
    var nav = document.getElementById("siteNav");
    if (!nav) return;

    var onScroll = function () {
      nav.classList.toggle("is-stuck", window.scrollY > 8);
    };

    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
  }

  function mountMobileNav() {
    var btn = document.getElementById("navToggle");
    var panel = document.getElementById("navLinks");
    if (!btn || !panel) return;

    function setOpen(open) {
      panel.classList.toggle("open", open);
      btn.setAttribute("aria-expanded", open ? "true" : "false");
      btn.setAttribute("aria-label", open ? "Close navigation menu" : "Open navigation menu");
    }

    btn.addEventListener("click", function () {
      setOpen(!panel.classList.contains("open"));
    });

    panel.addEventListener("click", function (e) {
      if (e.target.closest("a")) setOpen(false);
    });

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && panel.classList.contains("open")) {
        setOpen(false);
        btn.focus();
      }
    });

    // A resize past the breakpoint should never leave the panel stranded open.
    window.addEventListener("resize", function () {
      if (window.innerWidth > 780) setOpen(false);
    });
  }

  /* Fill every [data-icon] element with its glyph, keeping any
     existing label text after the icon. */
  function mountIcons() {
    document.querySelectorAll("[data-icon]").forEach(function (el) {
      el.insertAdjacentHTML("afterbegin", icon(el.getAttribute("data-icon")));
    });
  }

  /* Reveal sections as they enter the viewport. Elements are only
     hidden when JS is present to reveal them again. */
  function mountReveal() {
    var targets = document.querySelectorAll("[data-reveal]");
    if (!targets.length) return;

    if (!("IntersectionObserver" in window)) {
      targets.forEach(function (el) { el.classList.add("is-visible"); });
      return;
    }

    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (!entry.isIntersecting) return;
        entry.target.classList.add("is-visible");
        io.unobserve(entry.target);
      });
    }, { rootMargin: "0px 0px -8% 0px", threshold: 0.06 });

    targets.forEach(function (el) { io.observe(el); });
  }

  function mountSkipLink() {
    var main = document.querySelector("main");
    if (!main) return;
    if (!main.id) main.id = "main";
    if (document.querySelector(".skip-link")) return;
    document.body.insertAdjacentHTML(
      "afterbegin",
      '<a class="skip-link" href="#' + main.id + '">Skip to content</a>'
    );
  }

  /* The document is authored as if JavaScript is available (FAQ panels
     animate, tab panels switch). This marker lets the stylesheet undo
     anything that depends on script — see the no-js rules. */
  function mountJsFlag() {
    document.documentElement.classList.remove("no-js");
  }

  function init() {
    mountJsFlag();
    document.body.insertAdjacentHTML("afterbegin", navHTML());
    document.body.insertAdjacentHTML("beforeend", footerHTML());
    mountSkipLink();
    mountIcons();
    mountNavState();
    mountMobileNav();
    mountReveal();
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();

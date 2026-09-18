/* ============================================================
   Locus VPN — shared page behaviour
   Loaded after components.js on pages that need it. Everything
   here reads its numbers from content.json so prices, caps and
   device counts exist in exactly one place.
   ============================================================ */

(function () {
  "use strict";

  var TIER_ORDER = ["basic", "stream", "gaming"];
  var ICON_OK = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 12l5 5L20 6"/></svg>';
  var ICON_NO = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>';

  function money(n) {
    return "$" + Number(n).toFixed(2);
  }

  function fetchContent() {
    return fetch("content.json", { cache: "no-cache" }).then(function (r) {
      if (!r.ok) throw new Error("content.json " + r.status);
      return r.json();
    });
  }

  /* Replace {{token}} placeholders throughout the document with
     values from content.json. This keeps prose prices, caps and
     counts single-sourced instead of hand-copied per page.

     Text nodes and attribute values are both covered, so tokens
     work in <title>, meta descriptions and hrefs as well as body
     copy. <title> is outside <body>, so it is substituted on its
     own rather than being skipped by the tree walk. */
  function applyTokens(content) {
    var map = {
      "network.countries": content.network.countries,
      "network.cities": content.network.cities,
      "network.servers": content.network.servers,
      "network.uptime": content.network.uptime,
      "policy.refundDays": content.policy.refundDays,
      "policy.graceDays": content.policy.graceDays,
      "policy.auditCadence": content.policy.auditCadence,
      "brand.company": content.brand.company,
      "brand.jurisdiction": content.brand.jurisdiction,
      "brand.domain": content.brand.domain
    };

    TIER_ORDER.forEach(function (key) {
      var t = content.tiers[key];
      map["tiers." + key + ".name"] = t.name;
      map["tiers." + key + ".price"] = t.price;
      map["tiers." + key + ".annual"] = t.annual;
      map["tiers." + key + ".annualTotal"] = t.annualTotal;
      map["tiers." + key + ".cap"] = t.cap;
      map["tiers." + key + ".devices"] = t.devices;
      map["tiers." + key + ".tagline"] = t.tagline;
    });

    function substitute(text) {
      return text.replace(/\{\{([\w.]+)\}\}/g, function (m, key) {
        var value = map[key];
        return value === undefined ? m : value;
      });
    }

    // <title> and any <head> content the body walk cannot reach.
    if (document.title.indexOf("{{") !== -1) {
      document.title = substitute(document.title);
    }
    document.querySelectorAll("meta[content]").forEach(function (el) {
      var v = el.getAttribute("content");
      if (v && v.indexOf("{{") !== -1) el.setAttribute("content", substitute(v));
    });

    // Text nodes and attribute values inside <body>.
    var walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT | NodeFilter.SHOW_ELEMENT, {
      acceptNode: function (node) {
        if (node.nodeType === Node.TEXT_NODE) {
          return node.nodeValue.indexOf("{{") === -1
            ? NodeFilter.FILTER_REJECT
            : NodeFilter.FILTER_ACCEPT;
        }
        return NodeFilter.FILTER_ACCEPT;
      }
    });

    var nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);

    nodes.forEach(function (node) {
      if (node.nodeType === Node.TEXT_NODE) {
        node.nodeValue = substitute(node.nodeValue);
        return;
      }

      Array.prototype.slice.call(node.attributes).forEach(function (attr) {
        if (attr.value.indexOf("{{") === -1) return;
        node.setAttribute(attr.name, substitute(attr.value));
      });
    });
  }

  /* ---------------------------------------------------------- *
   * Pricing page
   * ---------------------------------------------------------- */

  function mountBilling() {
    var toggle = document.getElementById("billToggle");
    if (!toggle) return Promise.resolve(null);

    return fetchContent().then(function (content) {
      var monthLbl = document.getElementById("monthLbl");
      var yearLbl = document.getElementById("yearLbl");

      function render() {
        var annual = toggle.checked;

        monthLbl.classList.toggle("is-active", !annual);
        yearLbl.classList.toggle("is-active", annual);

        document.querySelectorAll("[data-tier]").forEach(function (el) {
          var tier = content.tiers[el.getAttribute("data-tier")];
          if (!tier) return;

          var amount = el.querySelector("[data-price-amount]");
          var note = el.querySelector("[data-price-note]");
          var cta = el.querySelector("[data-plan-cta]");

          if (amount) {
            amount.textContent = annual ? Number(tier.annual).toFixed(2) : tier.price;
          }

          if (note) {
            note.textContent = annual
              ? money(tier.annualTotal) + " billed once a year"
              : "billed monthly, cancel any time";
          }

          if (cta) {
            cta.setAttribute("href", "signup.html?plan=" + el.getAttribute("data-tier") +
              (annual ? "&billing=annual" : ""));
          }
        });
      }

      toggle.addEventListener("change", render);
      render();
      return content;
    });
  }

  /* ---------------------------------------------------------- *
   * FAQ accordion
   * ---------------------------------------------------------- */

  function mountFaq() {
    var items = document.querySelectorAll(".faq-item");
    if (!items.length) return;

    function setOpen(item, open) {
      var btn = item.querySelector(".faq-q");
      item.classList.toggle("open", open);
      btn.setAttribute("aria-expanded", open ? "true" : "false");
    }

    items.forEach(function (item) {
      var btn = item.querySelector(".faq-q");

      btn.addEventListener("click", function () {
        var willOpen = !item.classList.contains("open");
        // One panel at a time keeps long lists navigable.
        items.forEach(function (other) { if (other !== item) setOpen(other, false); });
        setOpen(item, willOpen);
      });

      setOpen(item, item.classList.contains("open"));
    });

    // Deep links, e.g. faq.html#logs
    var hash = location.hash.slice(1);
    if (hash) {
      var target = document.getElementById(hash);
      var item = target && target.closest(".faq-item");
      if (item) {
        items.forEach(function (other) { setOpen(other, false); });
        setOpen(item, true);
      }
    }
  }

  /* ---------------------------------------------------------- *
   * Sticky table of contents
   * ---------------------------------------------------------- */

  function mountToc() {
    var toc = document.querySelector(".toc");
    if (!toc) return;

    var links = Array.prototype.slice.call(toc.querySelectorAll('a[href^="#"]'));
    if (!links.length) return;

    var sections = links
      .map(function (a) { return document.getElementById(a.getAttribute("href").slice(1)); })
      .filter(Boolean);

    if (!sections.length || !("IntersectionObserver" in window)) return;

    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (!entry.isIntersecting) return;
        links.forEach(function (a) {
          a.classList.toggle(
            "is-active",
            a.getAttribute("href").slice(1) === entry.target.id
          );
        });
      });
    }, { rootMargin: "-6rem 0px -70% 0px", threshold: 0 });

    sections.forEach(function (s) { io.observe(s); });
  }

  /* ---------------------------------------------------------- *
   * Page wiring
   * ---------------------------------------------------------- */

  function init() {
    mountFaq();
    mountToc();

    // mountBilling resolves with the content it already fetched on the
    // pricing page, and with null everywhere else. Chain onto it so
    // content.json is fetched exactly once per page.
    Promise.resolve(mountBilling())
      .then(function (c) { return c || fetchContent(); })
      .then(function (c) { if (c) applyTokens(c); })
      .catch(function () { /* tokens stay literal if content.json is unavailable */ });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }

  // Shared helpers for page-level scripts (signup checkout).
  window.locusSite = {
    fetchContent: fetchContent,
    money: money,
    iconOk: ICON_OK,
    iconNo: ICON_NO,
    tierOrder: TIER_ORDER
  };
})();

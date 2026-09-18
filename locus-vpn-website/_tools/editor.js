/* ============================================================
   Locus site editor — in-page overlay
   Loaded only by edit.js. Provides click-to-edit text, a shared
   values panel, undo, and save-to-disk.
   ============================================================ */

(function () {
  "use strict";

  var PAGE_FILE = document.currentScript.getAttribute("data-page-file");
  var state = { edits: [], dirty: false, editing: null };

  /* ---------------------------------------------------------- *
   * Mapping a DOM text node back to an offset in the raw file
   * ---------------------------------------------------------- */

  /*
   * The served HTML differs from the file on disk because tokens and
   * shared values were resolved at serve time. Rather than trying to
   * invert that, we parse the raw file once and walk it in lockstep
   * with the live DOM, matching text nodes in document order. The
   * Nth editable text node in the DOM is the Nth in the file.
   */
  var rawSource = null;
  var fileTextNodes = [];

  function loadRaw(cb) {
    fetch("/" + PAGE_FILE, { headers: { "x-raw": "1" } })
      .then(function (r) {
        if (!r.ok) throw new Error("cannot read " + PAGE_FILE);
        return r.text();
      })
      .then(function (text) {
        rawSource = text;
        fileTextNodes = collectFileTextNodes(text);
        cb(null);
      })
      .catch(cb);
  }

  /*
   * Walk the raw HTML, tracking a character offset, and record every
   * run of text that sits between a '>' and the next '<'. These are
   * the editable spans; their order matches the DOM walk below.
   *
   * <head> is skipped, because the DOM walk starts at <body> — the
   * <title> would otherwise shift every index by one. Leading and
   * trailing whitespace is trimmed from the recorded range so the
   * offsets point at the text itself rather than the indentation,
   * which is what the DOM node's value contains.
   */
  function collectFileTextNodes(src) {
    var nodes = [];
    var inTag = false;
    var inScript = false;
    var inStyle = false;
    var inHead = true;
    var start = -1;
    var i = 0;

    while (i < src.length) {
      var ch = src[i];

      if (!inTag && ch === "<") {
        if (start > -1) {
          nodes.push(makeNode(src, start, i));
          start = -1;
        }
        var rest = src.slice(i, i + 9).toLowerCase();
        if (rest.startsWith("<body")) inHead = false;
        else if (rest.startsWith("<script")) inScript = true;
        else if (rest.startsWith("<style")) inStyle = true;
        else if (rest.startsWith("</script")) inScript = false;
        else if (rest.startsWith("</style")) inStyle = false;
        inTag = true;
      } else if (inTag && ch === ">") {
        inTag = false;
      } else if (!inTag && !inScript && !inStyle && !inHead && start === -1 &&
                 !/\s/.test(ch)) {
        start = i;
      }
      i++;
    }
    return nodes;
  }

  /*
   * Trim surrounding whitespace from a candidate range, and drop the
   * node entirely if nothing but space is left. The DOM walk rejects
   * whitespace-only and indentation-only nodes too, so this keeps the
   * two lists aligned.
   */
  function makeNode(src, from, to) {
    var s = from;
    var e = to;
    while (s < e && /\s/.test(src[s])) s++;
    while (e > s && /\s/.test(src[e - 1])) e--;
    return { from: s, to: e, text: src.slice(s, e) };
  }

  /*
   * Collect the corresponding editable text nodes from the live DOM,
   * in the same document order, skipping script/style/editor chrome.
   */
  function collectDomTextNodes() {
    var out = [];
    var walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
      acceptNode: function (node) {
        var p = node.parentNode;
        if (!p) return NodeFilter.FILTER_REJECT;
        var tag = p.nodeName;
        if (tag === "SCRIPT" || tag === "STYLE" || tag === "NOSCRIPT") {
          return NodeFilter.FILTER_REJECT;
        }
        if (p.closest && p.closest(".lc-bar, .lc-panel, .lc-toast")) {
          return NodeFilter.FILTER_REJECT;
        }
        if (!node.nodeValue || !node.nodeValue.trim()) {
          return NodeFilter.FILTER_REJECT;
        }
        return NodeFilter.FILTER_ACCEPT;
      }
    });
    var n;
    while ((n = walker.nextNode())) out.push(n);
    return out;
  }

  var domNodes = [];

  function pair() {
    domNodes = collectDomTextNodes();
    // Only pair up to the shorter length; extra nodes are ignored rather
    // than mis-assigned.
    var limit = Math.min(domNodes.length, fileTextNodes.length);
    if (domNodes.length !== fileTextNodes.length) {
      console.warn(
        "[editor] text-node count differs (dom " + domNodes.length +
        ", file " + fileTextNodes.length + ") — pairing the first " + limit
      );
    }
    for (var i = 0; i < limit; i++) {
      domNodes[i].__lcIndex = i;
    }
    return limit;
  }

  /* ---------------------------------------------------------- *
   * Editing
   * ---------------------------------------------------------- */

  function beginEdit(node) {
    var el = node.parentNode;
    el.setAttribute("contenteditable", "true");
    el.classList.add("lc-editing");
    el.focus();
    state.editing = { node: node, el: el, original: node.nodeValue };
  }

  function endEdit(commit) {
    var e = state.editing;
    if (!e) return;
    e.el.removeAttribute("contenteditable");
    e.el.classList.remove("lc-editing");
    state.editing = null;

    if (!commit) {
      e.node.nodeValue = e.original;
      renderToolbar();
      return;
    }

    var current = e.node.nodeValue;
    if (current === e.original) {
      renderToolbar();
      return;
    }

    var idx = e.node.__lcIndex;
    var fileNode = fileTextNodes[idx];
    if (!fileNode) {
      toast("Cannot map this text back to the file.", "error");
      return;
    }

    state.edits.push({
      index: idx,
      from: fileNode.from,
      to: fileNode.to,
      expected: fileNode.text,
      text: current,
      node: e.node,
      previous: e.original
    });
    // Later edits to the same node supersede earlier ones.
    state.edits = state.edits.filter(function (ed, i, arr) {
      return arr.findIndex(function (o) { return o.index === idx; }) === i ||
             ed.index !== idx;
    });
    state.dirty = true;
    renderToolbar();
  }

  /* ---------------------------------------------------------- *
   * Saving
   * ---------------------------------------------------------- */

  function save() {
    if (!state.edits.length) {
      toast("Nothing to save.");
      return;
    }

    /*
     * Edits are applied one at a time, back to front within a single
     * request each, because each write shifts offsets for the ones
     * after it. Saving from the end backwards keeps earlier offsets valid.
     */
    var queue = state.edits.slice().sort(function (a, b) { return b.from - a.from; });

    function step(i) {
      if (i >= queue.length) {
        state.edits = [];
        state.dirty = false;
        renderToolbar();
        toast("Saved " + queue.length + " change" + (queue.length === 1 ? "" : "s") + ".");
        setTimeout(function () { location.reload(); }, 600);
        return;
      }
      var ed = queue[i];

      fetch("/__edit/save", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          file: PAGE_FILE,
          from: ed.from,
          to: ed.to,
          expected: ed.expected,
          text: ed.text
        })
      })
        .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, body: j }; }); })
        .then(function (res) {
          if (!res.ok) throw new Error(res.body.error || "save failed");
          step(i + 1);
        })
        .catch(function (err) {
          setStatus("error", "Save failed");
          toast(err.message, "error");
        });
    }

    setStatus("saving", "Saving…");
    step(0);
  }

  function undo() {
    fetch("/__edit/undo", { method: "POST" })
      .then(function (r) { return r.json(); })
      .then(function (j) {
        if (j.ok) {
          toast("Reverted " + j.restored + ".");
          setTimeout(function () { location.reload(); }, 600);
        } else {
          toast(j.error || "Nothing to undo.", "error");
        }
      })
      .catch(function () { toast("Undo failed.", "error"); });
  }

  /* ---------------------------------------------------------- *
   * UI
   * ---------------------------------------------------------- */

  var bar, statusEl, panel, toastEl;

  function renderToolbar() {
    if (!bar) return;
    bar.querySelector("[data-act='edit']").setAttribute("data-on", state.mode ? "true" : "false");
    bar.querySelector("[data-act='save']").disabled = !state.dirty;
    if (!state.dirty) setStatus("clean", state.edits.length ? state.edits.length + " pending" : "Saved");
  }

  function setStatus(kind, text) {
    if (!statusEl) return;
    statusEl.setAttribute("data-state", kind);
    statusEl.textContent = text;
  }

  function toast(msg, kind) {
    if (!toastEl) return;
    toastEl.textContent = msg;
    toastEl.setAttribute("data-kind", kind || "info");
    toastEl.setAttribute("data-show", "true");
    clearTimeout(toast._t);
    toast._t = setTimeout(function () {
      toastEl.setAttribute("data-show", "false");
    }, 2600);
  }

  function buildToolbar() {
    bar = document.createElement("div");
    bar.className = "lc-bar";
    bar.innerHTML =
      '<button data-act="edit" data-on="true">Edit text</button>' +
      '<span class="lc-sep"></span>' +
      '<button data-act="values">Shared values</button>' +
      '<span class="lc-sep"></span>' +
      '<button data-act="save" disabled>Save</button>' +
      '<button data-act="undo">Undo last</button>' +
      '<span class="lc-status" data-state="clean">Ready</span>';
    document.body.appendChild(bar);

    statusEl = bar.querySelector(".lc-status");

    bar.addEventListener("click", function (e) {
      var btn = e.target.closest("button");
      if (!btn) return;
      var act = btn.getAttribute("data-act");
      if (act === "edit") {
        state.mode = !state.mode;
        document.body.style.cursor = state.mode ? "" : "default";
        renderToolbar();
        toast(state.mode ? "Click any text to edit." : "Editing paused.");
      }
      if (act === "save") save();
      if (act === "undo") undo();
      if (act === "values") togglePanel();
    });

    renderToolbar();
  }

  function buildToast() {
    toastEl = document.createElement("div");
    toastEl.className = "lc-toast";
    toastEl.setAttribute("data-show", "false");
    document.body.appendChild(toastEl);
  }

  /* ---------------------------------------------------------- *
   * Shared values panel
   * ---------------------------------------------------------- */

  function togglePanel() {
    if (!panel) buildPanel();
    var open = panel.getAttribute("data-open") === "true";
    panel.setAttribute("data-open", open ? "false" : "true");
    if (!open) loadValues();
  }

  function buildPanel() {
    panel = document.createElement("div");
    panel.className = "lc-panel";
    panel.setAttribute("data-open", "false");
    panel.innerHTML =
      '<div class="lc-panel__head">' +
        '<h2>Shared values</h2>' +
        '<button data-close aria-label="Close">&times;</button>' +
      "</div>" +
      '<div class="lc-panel__body" data-body></div>';
    document.body.appendChild(panel);
    panel.querySelector("[data-close]").addEventListener("click", function () {
      panel.setAttribute("data-open", "false");
    });
  }

  var LABELS = {
    tiers: "Tiers",
    network: "Network",
    policy: "Policy",
    brand: "Company"
  };

  var FIELD_LABELS = {
    name: "Name",
    price: "Monthly price",
    annual: "Annual price (per month)",
    cap: "Speed cap (Mbps)",
    devices: "Devices",
    udp: "UDP relay",
    countries: "Countries",
    cities: "Cities",
    servers: "Servers",
    uptime: "Uptime",
    refundDays: "Refund window (days)",
    graceDays: "Grace period (days)",
    auditCadence: "Audit cadence",
    company: "Legal name",
    jurisdiction: "Jurisdiction"
  };

  function loadValues() {
    fetch("/__edit/content")
      .then(function (r) { return r.json(); })
      .then(renderValues)
      .catch(function () { toast("Could not load content.json", "error"); });
  }

  function renderValues(content) {
    var body = panel.querySelector("[data-body]");
    var html = '<p class="lc-hint">These drive the whole site. Save to write content.json — every page picks the change up on reload.</p>';

    Object.keys(content).forEach(function (group) {
      var val = content[group];
      if (!val || typeof val !== "object") return;
      html += '<div class="lc-group"><h3>' + (LABELS[group] || group) + "</h3>";

      Object.keys(val).forEach(function (key) {
        var v = val[key];
        if (v && typeof v === "object") {
          html += '<div class="lc-group" style="margin:.4rem 0 .8rem"><h3 style="text-transform:none;letter-spacing:0">' + key + "</h3>";
          Object.keys(v).forEach(function (k2) {
            html += field(group + "." + key + "." + k2, (FIELD_LABELS[k2] || k2), v[k2]);
          });
          html += "</div>";
        } else {
          html += field(group + "." + key, (FIELD_LABELS[key] || key), v);
        }
      });
      html += "</div>";
    });

    html +=
      '<div class="lc-actions">' +
        '<button class="lc-primary" data-save-values>Save values</button>' +
        '<button class="lc-secondary" data-reload-values>Reset</button>' +
      "</div>" +
      '<div class="lc-hist"><h3>Recent changes</h3><ul data-hist></ul></div>';

    body.innerHTML = html;

    body.querySelector("[data-save-values]").addEventListener("click", saveValues);
    body.querySelector("[data-reload-values]").addEventListener("click", loadValues);
    loadHistory();
  }

  function field(pathKey, label, value) {
    return (
      '<div class="lc-field"><label for="lc-' + pathKey + '">' + label + "</label>" +
      '<input id="lc-' + pathKey + '" data-key="' + pathKey + '" value="' +
      String(value).replace(/"/g, "&quot;") + '"></div>'
    );
  }

  function saveValues() {
    var out = {};
    panel.querySelectorAll("[data-key]").forEach(function (input) {
      var parts = input.getAttribute("data-key").split(".");
      var cur = out;
      for (var i = 0; i < parts.length - 1; i++) {
        cur[parts[i]] = cur[parts[i]] || {};
        cur = cur[parts[i]];
      }
      cur[parts[parts.length - 1]] = input.value;
    });

    fetch("/__edit/content", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(out)
    })
      .then(function (r) { return r.json(); })
      .then(function (j) {
        if (j.ok) {
          toast("Values saved. Reloading…");
          setTimeout(function () { location.reload(); }, 700);
        } else {
          toast(j.error || "Save failed", "error");
        }
      })
      .catch(function () { toast("Save failed", "error"); });
  }

  function loadHistory() {
    fetch("/__edit/history")
      .then(function (r) { return r.json(); })
      .then(function (list) {
        var ul = panel.querySelector("[data-hist]");
        if (!ul) return;
        if (!list.length) {
          ul.innerHTML = '<li><span>No changes yet</span></li>';
          return;
        }
        ul.innerHTML = list.slice(0, 12).map(function (h) {
          var t = new Date(h.at);
          var short = h.inserted.length > 28 ? h.inserted.slice(0, 28) + "…" : h.inserted;
          return "<li><span>" + escapeHtml(short) + "</span><span>" +
            h.file.replace(".html", "") + " · " +
            t.getHours() + ":" + String(t.getMinutes()).padStart(2, "0") + "</span></li>";
        }).join("");
      })
      .catch(function () {});
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"]/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c];
    });
  }

  /* ---------------------------------------------------------- *
   * Wiring
   * ---------------------------------------------------------- */

  function wireEditing() {
    document.addEventListener("mouseover", function (e) {
      if (!state.mode || state.editing) return;
      var el = e.target;
      if (!el || el.closest(".lc-bar, .lc-panel, .lc-toast")) return;
      if (!el.__lcEditable) return;
      el.setAttribute("data-edit-hover", "");
    });

    document.addEventListener("mouseout", function (e) {
      var el = e.target;
      if (el && el.removeAttribute) el.removeAttribute("data-edit-hover");
    });

    document.addEventListener("click", function (e) {
      if (!state.mode) return;
      var el = e.target;
      if (!el || el.closest(".lc-bar, .lc-panel, .lc-toast")) return;

      // Find the nearest ancestor that holds one of our paired text nodes.
      var target = null;
      var node = el;
      while (node && node !== document.body) {
        if (node.__lcEditable) { target = node; break; }
        node = node.parentNode;
      }
      if (!target) return;

      e.preventDefault();
      e.stopPropagation();
      if (state.editing && state.editing.el !== target) endEdit(true);

      var tn = null;
      for (var i = 0; i < target.childNodes.length; i++) {
        if (target.childNodes[i].nodeType === 3 && target.childNodes[i].__lcIndex !== undefined) {
          tn = target.childNodes[i];
          break;
        }
      }
      if (!tn) return;
      beginEdit(tn);
    }, true);

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && state.editing) {
        e.preventDefault();
        endEdit(false);
        toast("Reverted that edit.");
      }
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        save();
      }
      if (e.key === "Enter" && state.editing && !e.shiftKey) {
        e.preventDefault();
        endEdit(true);
        toast("Edit staged. Save to write it to disk.");
      }
    });

    document.addEventListener(
      "blur",
      function () {
        if (state.editing) endEdit(true);
      },
      true
    );
  }

  function markEditable() {
    domNodes.forEach(function (n) {
      var el = n.parentNode;
      if (el && el.__lcIndex === undefined) {
        n.__lcEditableParent = true;
        el.__lcEditable = true;
      }
    });
  }

  function init() {
    state.mode = true;
    buildToolbar();
    buildToast();
    wireEditing();

    loadRaw(function (err) {
      if (err) {
        toast("Cannot read file: " + err.message, "error");
        setStatus("error", "No file access");
        return;
      }
      var count = pair();
      markEditable();
      setStatus("clean", count + " editable");
      console.log("[editor] paired " + count + " text nodes with " + PAGE_FILE);
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", init);
  } else {
    init();
  }
})();

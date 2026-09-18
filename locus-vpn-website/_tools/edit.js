#!/usr/bin/env node
/*
 * Locus site editor — local, dependency-free.
 *
 *   node edit.js            # http://localhost:4321
 *   node edit.js --port 5000
 *
 * Serves the site with an editing overlay injected, and accepts writes
 * back to the real .html / .css files on disk. Nothing is bundled or
 * transformed: the files stay plain static HTML.
 *
 * Two kinds of change are supported:
 *   1. Text edits — click any text node in the page and type over it.
 *   2. Shared values — prices, speed caps, counts — held in content.json
 *      and applied across every page from that one place.
 *
 * Safety: every write is preceded by a backup of the original file into
 * .edits/backups/, and the whole session's changes can be undone from the
 * editor's History panel.
 */

"use strict";

const http = require("http");
const fs = require("fs");
const path = require("path");
const url = require("url");

// The site files live one level up, next to this _tools/ directory.
// (This file previously sat in the site root, where __dirname was the root.)
const ROOT = path.join(__dirname, "..");
const PORT = (() => {
  const i = process.argv.indexOf("--port");
  if (i > -1 && process.argv[i + 1]) return Number(process.argv[i + 1]);
  return 4321;
})();

const CONTENT_FILE = path.join(ROOT, "content.json");
const BACKUP_DIR = path.join(ROOT, ".edits", "backups");
const HISTORY_FILE = path.join(ROOT, ".edits", "history.json");

const htmlFiles = () => fs.readdirSync(ROOT).filter((f) => f.endsWith(".html"));
const isEditable = (f) => /\.(html|css|js|json)$/.test(f) && !f.startsWith(".");
const isEditorAsset = (f) => f === "editor.js" || f === "editor.css" || f === "edit.js";

/* ------------------------------------------------------------------ *
 * Shared content values
 * ------------------------------------------------------------------ */

const DEFAULT_CONTENT = {
  tiers: {
    basic: { name: "Basic", price: "3.99", annual: "2.99", cap: "5", devices: "3", udp: "No" },
    stream: { name: "Stream", price: "7.99", annual: "5.99", cap: "100", devices: "8", udp: "No" },
    gaming: { name: "Gaming", price: "14.99", annual: "11.99", cap: "200", devices: "Unlimited", udp: "Yes" }
  },
  network: {
    countries: "112",
    cities: "142",
    servers: "3,000+",
    uptime: "99.9%"
  },
  policy: {
    refundDays: "30",
    graceDays: "7",
    auditCadence: "once a year"
  },
  brand: {
    company: "Locus Networks Ltd.",
    jurisdiction: "the Netherlands"
  }
};

function readContent() {
  if (!fs.existsSync(CONTENT_FILE)) {
    fs.writeFileSync(CONTENT_FILE, JSON.stringify(DEFAULT_CONTENT, null, 2) + "\n");
    return DEFAULT_CONTENT;
  }
  try {
    const parsed = JSON.parse(fs.readFileSync(CONTENT_FILE, "utf8"));
    // Shallow-merge each group so new defaults are picked up.
    const merged = {};
    for (const group of Object.keys(DEFAULT_CONTENT)) {
      merged[group] = Object.assign({}, DEFAULT_CONTENT[group], parsed[group] || {});
      if (typeof DEFAULT_CONTENT[group] === "object") {
        for (const key of Object.keys(parsed[group] || {})) {
          if (typeof DEFAULT_CONTENT[group][key] === "object") {
            merged[group][key] = Object.assign({}, DEFAULT_CONTENT[group][key], parsed[group][key]);
          }
        }
      }
    }
    return merged;
  } catch (err) {
    console.error("content.json is not valid JSON — using defaults.", err.message);
    return DEFAULT_CONTENT;
  }
}

/*
 * Tokens let a page reference a shared value inline:
 *   {{price.basic}}  {{cap.stream}}  {{countries}}  {{refundDays}}
 * They are resolved at serve time, so the HTML on disk stays readable
 * and the value lives in exactly one place.
 */
function resolveTokens(html, content) {
  const flat = {};
  for (const [group, val] of Object.entries(content)) {
    if (val && typeof val === "object") {
      for (const [k, v] of Object.entries(val)) {
        if (v && typeof v === "object") {
          for (const [k2, v2] of Object.entries(v)) flat[`${k}.${k2}`] = v2;
        } else {
          flat[k] = v;
        }
      }
    }
  }
  return html.replace(/\{\{\s*([\w.]+)\s*\}\}/g, (m, key) =>
    Object.prototype.hasOwnProperty.call(flat, key) ? String(flat[key]) : m
  );
}

/*
 * Some shared values are written as literal text rather than tokens
 * (price spans carry data-annual/data-monthly, prose says "5 Mbps", etc).
 * These rewrite the literals directly so a single edit propagates.
 */
function applySharedValues(html, content, filename) {
  let out = html;

  /*
   * Pricing page: plan cards plus the comparison table.
   *
   * Everything is substituted by tier position rather than by matching
   * the number already in the file. Matching the old value would
   * silently do nothing the second time a value changes, because the
   * file would hold the previous value and the new one would no longer
   * be found.
   *
   * The card rewrite runs first over the whole card body, then prices
   * are applied within that result — running prices first would let the
   * broader card regex clobber them.
   */
  if (filename === "pricing.html") {
    /*
     * Map an offset in the output to the tier whose card contains it.
     * The nearest preceding <h3> names the tier, so take the LAST match
     * rather than the first — a greedy leading match would always return
     * the first card on the page.
     */
    function cardOffsetToTier(offset) {
      const before = out.slice(0, offset);
      const re = /<h3>([^<]*?)(?:\s*<span[^>]*>[^<]*<\/span>)?<\/h3>/g;
      let m;
      let last = null;
      while ((m = re.exec(before))) last = m[1].trim().toLowerCase();
      return last ? content.tiers[last] : null;
    }

    // Plan cards: price spans.
    out = out.replace(
      /data-annual="([\d.]+)" data-monthly="([\d.]+)">([\d.]+)(<\/span>)/g,
      (match, annual, monthly, shown, closeTag, offset) => {
        const tier = cardOffsetToTier(offset);
        if (!tier) return match;
        return `data-annual="${tier.annual}" data-monthly="${tier.price}">${tier.price}${closeTag}`;
      }
    );

    // Plan cards: speed cap line.
    out = out.replace(
      /(<strong>)[\d.]+( Mbps<\/strong> speed cap)/g,
      (match, open, tail, offset) => {
        const tier = cardOffsetToTier(offset);
        return tier ? open + tier.cap + tail : match;
      }
    );

    // Cards: device limits. Two phrasings exist — "N devices at once"
    // and "Unlimited devices" — so both are handled.
    out = out.replace(
      /(<li>)[\dA-Za-z ]+( devices at once<\/li>)/g,
      (match, open, tail, offset) => {
        const tier = cardOffsetToTier(offset);
        return tier ? open + tier.devices + tail : match;
      }
    );
    out = out.replace(
      /(<li>)[\dA-Za-z ]+ devices(<\/li>)/g,
      (match, open, tail, offset) => {
        const tier = cardOffsetToTier(offset);
        if (!tier) return match;
        return tier.devices === "Unlimited"
          ? open + "Unlimited" + tail
          : open + tier.devices + " devices" + tail;
      }
    );

    // Comparison table: three columns, in tier order.
    out = out.replace(
      /(<tr><th scope="row">Speed cap<\/th>)(<td>[^<]*<\/td>)(<td>[^<]*<\/td>)(<td>[^<]*<\/td>)/,
      `$1<td>${content.tiers.basic.cap} Mbps</td>` +
      `<td>${content.tiers.stream.cap} Mbps</td>` +
      `<td>${content.tiers.gaming.cap} Mbps</td>`
    );
    out = out.replace(
      /(<tr><th scope="row">Devices at once<\/th>)(<td>[^<]*<\/td>)(<td>[^<]*<\/td>)(<td>[^<]*<\/td>)/,
      `$1<td>${content.tiers.basic.devices}</td>` +
      `<td>${content.tiers.stream.devices}</td>` +
      `<td>${content.tiers.gaming.devices}</td>`
    );
    out = out.replace(
      /(<tr><th scope="row">UDP relay<\/th>)(<td>[^<]*<\/td>)(<td>[^<]*<\/td>)(<td>[^<]*<\/td>)/,
      `$1<td>${content.tiers.basic.udp}</td>` +
      `<td>${content.tiers.stream.udp}</td>` +
      `<td>${content.tiers.gaming.udp}</td>`
    );
  }

  /*
   * Anywhere else a cap is written as "N Mbps" in prose, map the known
   * tier caps onto their current values so a change propagates.
   */
  const capPairs = [
    ["5", content.tiers.basic.cap],
    ["100", content.tiers.stream.cap],
    ["200", content.tiers.gaming.cap]
  ];
  out = out.replace(/\b(5|100|200)\s?Mbps\b/g, (match, num) => {
    const pair = capPairs.find((p) => p[0] === num);
    return pair ? `${pair[1]} Mbps` : match;
  });

  // Counts and policy numbers written as prose.
  out = out.replace(/\b\d[\d,]*\+?\s+countries\b/g, content.network.countries + " countries");
  out = out.replace(/\b\d[\d,]*\+?\s+cities\b/g, content.network.cities + " cities");
  out = out.replace(/\b[\d,]+\+\s+servers\b/g, content.network.servers + " servers");
  out = out.replace(/\b\d+-day(?=\s+refund)/g, content.policy.refundDays + "-day");
  out = out.replace(/\bseven-day grace\b/g, content.policy.graceDays + "-day grace");
  out = out.replace(/Locus Networks Ltd\./g, content.brand.company);

  return out;
}

/* ------------------------------------------------------------------ *
 * Backups + history
 * ------------------------------------------------------------------ */

function ensureDirs() {
  fs.mkdirSync(BACKUP_DIR, { recursive: true });
}

function backup(file) {
  ensureDirs();
  const src = path.join(ROOT, file);
  if (!fs.existsSync(src)) return null;
  const stamp = new Date().toISOString().replace(/[:.]/g, "-");
  const dest = path.join(BACKUP_DIR, `${path.basename(file)}.${stamp}.bak`);
  fs.copyFileSync(src, dest);
  return path.relative(ROOT, dest);
}

function readHistory() {
  try {
    return JSON.parse(fs.readFileSync(HISTORY_FILE, "utf8"));
  } catch {
    return [];
  }
}

function writeHistory(list) {
  ensureDirs();
  fs.writeFileSync(HISTORY_FILE, JSON.stringify(list.slice(-200), null, 2));
}

/* ------------------------------------------------------------------ *
 * Edit application
 * ------------------------------------------------------------------ */

/*
 * A text edit is identified by a path to the node rather than by an
 * offset, so it survives the page being re-served. The client sends:
 *   { file, from, to, text }        — character range in the raw file
 * and we re-verify the old text matches before writing, refusing if not.
 */
function applyTextEdit(file, from, to, expected, text) {
  if (!isEditable(file)) throw new Error(`refusing to edit ${file}`);
  const abs = path.join(ROOT, file);
  if (!abs.startsWith(ROOT)) throw new Error("path escapes project root");

  const raw = fs.readFileSync(abs, "utf8");
  if (raw.slice(from, to) !== expected) {
    throw new Error("file changed since the page was loaded — reload and try again");
  }

  const updated = raw.slice(0, from) + text + raw.slice(to);
  const rel = backup(file);
  fs.writeFileSync(abs, updated);

  const hist = readHistory();
  hist.push({
    at: new Date().toISOString(),
    file,
    from,
    to,
    removed: expected,
    inserted: text,
    backup: rel
  });
  writeHistory(hist);
  return { ok: true, backup: rel };
}

function undoLast() {
  const hist = readHistory();
  const last = hist.pop();
  if (!last) return { ok: false, error: "nothing to undo" };
  const abs = path.join(ROOT, last.file);
  const raw = fs.readFileSync(abs, "utf8");
  const updated = raw.slice(0, last.from) + last.removed + raw.slice(last.from + last.inserted.length);
  fs.writeFileSync(abs, updated);
  writeHistory(hist);
  return { ok: true, restored: last.file };
}

/* ------------------------------------------------------------------ *
 * HTTP
 * ------------------------------------------------------------------ */

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "application/javascript; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".ico": "image/x-icon"
};

function send(res, code, body, type) {
  res.writeHead(code, {
    "Content-Type": type || "text/plain; charset=utf-8",
    "Cache-Control": "no-store"
  });
  res.end(body);
}

function readBody(req) {
  return new Promise((resolve, reject) => {
    let data = "";
    req.on("data", (c) => {
      data += c;
      if (data.length > 5e6) reject(new Error("body too large"));
    });
    req.on("end", () => {
      try {
        resolve(data ? JSON.parse(data) : {});
      } catch (e) {
        reject(e);
      }
    });
  });
}

const server = http.createServer(async (req, res) => {
  const parsed = url.parse(req.url, true);
  let pathname = decodeURIComponent(parsed.pathname);
  if (pathname === "/") pathname = "/index.html";

  // ---- API ----
  if (pathname === "/__edit/save" && req.method === "POST") {
    try {
      const body = await readBody(req);
      const result = applyTextEdit(body.file, body.from, body.to, body.expected, body.text);
      return send(res, 200, JSON.stringify(result), "application/json");
    } catch (err) {
      return send(res, 400, JSON.stringify({ error: err.message }), "application/json");
    }
  }

  if (pathname === "/__edit/undo" && req.method === "POST") {
    try {
      return send(res, 200, JSON.stringify(undoLast()), "application/json");
    } catch (err) {
      return send(res, 400, JSON.stringify({ error: err.message }), "application/json");
    }
  }

  if (pathname === "/__edit/history" && req.method === "GET") {
    return send(res, 200, JSON.stringify(readHistory().slice(-50).reverse()), "application/json");
  }

  if (pathname === "/__edit/content" && req.method === "GET") {
    return send(res, 200, JSON.stringify(readContent()), "application/json");
  }

  if (pathname === "/__edit/content" && req.method === "POST") {
    try {
      const body = await readBody(req);
      backup("content.json");
      fs.writeFileSync(CONTENT_FILE, JSON.stringify(body, null, 2) + "\n");
      return send(res, 200, JSON.stringify({ ok: true }), "application/json");
    } catch (err) {
      return send(res, 400, JSON.stringify({ error: err.message }), "application/json");
    }
  }

  // ---- editor assets ----
  if (pathname === "/__editor.js" || pathname === "/__editor.css") {
    const asset = pathname === "/__editor.js" ? "editor.js" : "editor.css";
    const abs = path.join(ROOT, asset);
    if (!fs.existsSync(abs)) return send(res, 404, "editor asset missing");
    return send(res, 200, fs.readFileSync(abs), MIME[path.extname(asset)]);
  }

  // ---- static site ----
  const target = path.join(ROOT, pathname);
  if (!target.startsWith(ROOT)) return send(res, 403, "forbidden");
  if (!fs.existsSync(target) || fs.statSync(target).isDirectory()) {
    return send(res, 404, "not found");
  }

  const ext = path.extname(target);
  let body = fs.readFileSync(target);

  // The editor asks for the file exactly as it is on disk, so it can map
  // DOM text nodes back to character offsets without inverting the
  // token/substitution pass below.
  if (req.headers["x-raw"] === "1" && (ext === ".html" || ext === ".css")) {
    return send(res, 200, body, MIME[ext] || "application/octet-stream");
  }

  if (ext === ".html") {
    const content = readContent();
    let html = body.toString("utf8");
    html = resolveTokens(html, content);
    html = applySharedValues(html, content, path.basename(target));
    // Inject the editor before </body>.
    const inject =
      '\n<link rel="stylesheet" href="/__editor.css">\n' +
      '<script src="/__editor.js" data-page-file="' +
      path.basename(target) +
      '"></script>\n';
    html = html.replace(/<\/body>/i, inject + "</body>");
    body = Buffer.from(html, "utf8");
  }

  send(res, 200, body, MIME[ext] || "application/octet-stream");
});

server.listen(PORT, () => {
  const pages = htmlFiles();
  console.log("");
  console.log("  Locus site editor");
  console.log("  ----------------------------------------");
  console.log(`  Editing:  ${ROOT}`);
  console.log(`  Open:     http://localhost:${PORT}`);
  console.log(`  Pages:    ${pages.length} (${pages.join(", ")})`);
  console.log("");
  console.log("  Click any text to edit it. Ctrl+S saves.");
  console.log("  Backups go to .edits/backups/. Stop with Ctrl+C.");
  console.log("");
});

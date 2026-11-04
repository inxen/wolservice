// 快速面板的离线外壳：只缓存静态资源，设备状态永远走网络，避免展示过期数据。
const VERSION = {{VERSION}};
const CACHE = "wol-shell-" + VERSION;
const SHELL = [
  "/",
  "/manifest.webmanifest",
  "/favicon.svg",
  "/assets/app.css",
  "/assets/app.js",
  "/assets/theme.js",
  "/assets/icon-192.png",
  "/assets/icon-512.png",
  "/assets/icon-maskable-192.png",
  "/assets/icon-maskable-512.png",
  "/assets/apple-touch-icon.png"
];

self.addEventListener("install", function (event) {
  event.waitUntil((async function () {
    const cache = await caches.open(CACHE);
    // 单个资源失败（例如面板要求先登录）不应导致整个安装失败
    await Promise.all(SHELL.map(function (url) { return cache.add(url).catch(function () {}); }));
    await self.skipWaiting();
  })());
});

self.addEventListener("activate", function (event) {
  event.waitUntil((async function () {
    const keys = await caches.keys();
    await Promise.all(keys.filter(function (key) { return key !== CACHE; }).map(function (key) { return caches.delete(key); }));
    await self.clients.claim();
  })());
});

self.addEventListener("fetch", function (event) {
  const request = event.request;
  if (request.method !== "GET") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  // 配置面板是另一套页面，不接管
  if (url.pathname.startsWith("/api/") || url.pathname === "/config") return;

  if (request.mode === "navigate") {
    event.respondWith(networkFirst(request, url));
    return;
  }
  if (url.pathname.startsWith("/assets/") || url.pathname === "/favicon.svg" || url.pathname === "/manifest.webmanifest") {
    event.respondWith(cacheFirst(request));
  }
});

async function networkFirst(request, url) {
  try {
    const response = await fetch(request);
    const isApp = url.pathname === "/" || url.pathname === "/quickcontrol";
    if (response.ok && isApp) {
      const cache = await caches.open(CACHE);
      await cache.put("/", response.clone());
    }
    return response;
  } catch (error) {
    const isApp = url.pathname === "/" || url.pathname === "/quickcontrol";
    const cached = isApp ? await caches.match("/") : null;
    return cached || offlinePage();
  }
}

async function cacheFirst(request) {
  const cached = await caches.match(request);
  if (cached) return cached;
  const response = await fetch(request);
  if (response.ok) {
    const cache = await caches.open(CACHE);
    await cache.put(request, response.clone());
  }
  return response;
}

function offlinePage() {
  const html = [
    '<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">',
    '<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">',
    '<title>离线 · Offline</title><style>',
    ':root{color-scheme:light dark}',
    'body{margin:0;min-height:100vh;display:grid;place-items:center;padding:env(safe-area-inset-top) env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left);',
    'background:#f2f5fa;color:#0f172a;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif}',
    'main{text-align:center;padding:32px}',
    'img{width:96px;height:96px;border-radius:22px;box-shadow:0 12px 30px rgba(15,23,42,.12)}',
    'h1{font-size:1.15rem;margin:22px 0 6px}',
    'p{margin:0 0 18px;color:#64748b;font-size:.9rem;line-height:1.6}',
    'nav{display:flex;gap:10px;justify-content:center;font-size:.9rem}',
    'a{color:#6d6af0;text-decoration:none;font-weight:600}',
    '@media (prefers-color-scheme:dark){body{background:#070b13;color:#e6edf7}p{color:#8b9ab0}a{color:#a5a6ff}img{box-shadow:none}}',
    '</style></head><body><main>',
    '<img src="/assets/icon-192.png" alt="">',
    '<h1>当前离线</h1>',
    '<p>无法连接 WOL 服务，请确认设备仍在同一局域网内。</p>',
    '<nav><a href="/">重试</a><a href="/">Retry</a></nav>',
    '</main></body></html>'
  ].join("");
  return new Response(html, {
    headers: { "Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store" }
  });
}

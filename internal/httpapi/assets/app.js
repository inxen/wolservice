(function () {
  "use strict";

  var REFRESH_MS = 5000;

  var I18N = {
    zh: {
      stateOnline: "在线",
      statePartial: "部分在线",
      stateOffline: "离线",
      stateUnknown: "未知",
      reachDisabled: "未启用探测",
      powerOn: "已开机",
      powerOff: "已关机",
      powerUnknown: "状态未知",
      powerOnHint: "点击关机",
      powerOffHint: "点击开机",
      powerUnknownHint: "点击唤醒",
      poweringOn: "正在开机",
      poweringOff: "正在关机",
      powerWaiting: "等待设备响应…",
      powerWakeSent: "唤醒指令已发送，等待设备上线…",
      powerShutdownSent: "关机指令已发送，等待设备离线…",
      powerOnConfirmed: "设备已开机",
      powerOffConfirmed: "设备已关机",
      powerOnTimeout: "唤醒指令已发送，但设备尚未上线",
      powerOffTimeout: "关机指令已发送，但设备仍在线",
      powerNoProbe: "指令已发送；未启用在线探测，状态未确认",
      shutdownTitle: "发送关机指令",
      shutdownCopy: "确认向「{name}」发送关机指令？会执行配置里的远程命令。",
      cancel: "取消",
      confirm: "确认",
      uncertain: "命令可能已经执行，但连接提前断开了",
      actionFailed: "操作失败",
      requestFailed: "请求失败",
      offlineCopy: "无法连接服务，请确认手机仍在同一局域网内。",
      loadingTitle: "正在加载",
      loadingCopy: "正在读取设备列表…",
      emptyTitle: "还没有设备",
      emptyCopy: "在 /config 里添加设备后，会显示在这里。",
      retry: "重试",
      tokenTitle: "访问密码",
      tokenCopyRequired: "这个面板需要访问密码，输入后才能继续。",
      tokenCopyOptional: "请输入访问密码。",
      tokenPlaceholder: "访问密码",
      clear: "退出登录",
      save: "登录并刷新",
      tokenSaved: "登录成功",
      unauthorized: "访问密码无效",
      noWake: "当前无法唤醒",
      noShutdown: "未配置 SSH 关机",
      mac: "MAC",
      broadcast: "广播地址",
      autoBroadcast: "自动检测",
      warning: "提示",
      close: "关闭",
      theme: "主题",
      config: "配置",
      cameraAddress: "地址",
      maskOn: "已遮蔽",
      maskOff: "画面正常",
      maskUnknown: "状态未知",
      maskOnHint: "点击取消遮蔽",
      maskOffHint: "点击遮蔽镜头",
      maskUnknownHint: "点击读取状态",
      maskBusy: "正在切换",
      maskBusyHint: "请稍候…",
      maskSetOn: "镜头已遮蔽",
      maskSetOff: "已取消遮蔽",
      maskErrorHint: "无法连接摄像头，点击重试",
      maskFailed: "镜头遮蔽操作失败"
    },
    en: {
      stateOnline: "Online",
      statePartial: "Partial",
      stateOffline: "Offline",
      stateUnknown: "Unknown",
      reachDisabled: "Probe off",
      powerOn: "Powered on",
      powerOff: "Powered off",
      powerUnknown: "Unknown",
      powerOnHint: "Click to shut down",
      powerOffHint: "Click to power on",
      powerUnknownHint: "Click to wake",
      poweringOn: "Powering on",
      poweringOff: "Shutting down",
      powerWaiting: "Waiting for the device…",
      powerWakeSent: "Wake request sent. Waiting for the device to come online…",
      powerShutdownSent: "Shutdown request sent. Waiting for the device to go offline…",
      powerOnConfirmed: "Device is on",
      powerOffConfirmed: "Device is off",
      powerOnTimeout: "Wake request sent, but the device has not come online yet",
      powerOffTimeout: "Shutdown request sent, but the device is still online",
      powerNoProbe: "Request sent; online probing is disabled, so the state is unconfirmed",
      shutdownTitle: "Send shutdown request",
      shutdownCopy: "Send a shutdown request to \"{name}\"? The configured remote command will run.",
      cancel: "Cancel",
      confirm: "Confirm",
      uncertain: "The command may have run, but the connection closed early.",
      actionFailed: "Action failed",
      requestFailed: "Request failed",
      offlineCopy: "Cannot reach the service. Check that this device is still on the same network.",
      loadingTitle: "Loading",
      loadingCopy: "Reading the device list…",
      emptyTitle: "No devices yet",
      emptyCopy: "Add a device in /config and it will show up here.",
      retry: "Retry",
      tokenTitle: "Access password",
      tokenCopyRequired: "This panel needs an access password before you can continue.",
      tokenCopyOptional: "Enter the access password.",
      tokenPlaceholder: "Access password",
      clear: "Log out",
      save: "Log in and refresh",
      tokenSaved: "Logged in",
      unauthorized: "Invalid access password",
      noWake: "This device cannot be woken right now",
      noShutdown: "No SSH shutdown configured",
      mac: "MAC",
      broadcast: "Broadcast",
      autoBroadcast: "Auto detect",
      warning: "Notice",
      close: "Close",
      theme: "Theme",
      config: "Configuration",
      cameraAddress: "Address",
      maskOn: "Masked",
      maskOff: "Live view",
      maskUnknown: "Unknown",
      maskOnHint: "Click to unmask",
      maskOffHint: "Click to mask the lens",
      maskUnknownHint: "Click to read the state",
      maskBusy: "Updating",
      maskBusyHint: "Please wait…",
      maskSetOn: "Lens masked",
      maskSetOff: "Lens unmasked",
      maskErrorHint: "Cannot reach the camera. Click to retry.",
      maskFailed: "Could not update the lens mask"
    }
  };

  var state = {
    lang: localStorage.getItem("wolLang") || "zh",
    theme: localStorage.getItem("wolTheme") || "auto",
    devices: [],
    power: new Map(),
    maskBusy: new Map(),
    localPower: new Map(),
    server: { title: "", version: "" },
    loaded: false,
    toastTimer: null,
    refreshTimer: null,
    pending: null
  };
  if (!I18N[state.lang]) state.lang = "zh";

  function t(key) {
    var pack = I18N[state.lang] || I18N.zh;
    return pack[key] || I18N.zh[key] || key;
  }

  function formatText(template, values) {
    return String(template).replace(/\{(\w+)\}/g, function (_, key) {
      return values && values[key] !== undefined ? values[key] : "";
    });
  }

  function $(id) { return document.getElementById(id); }

  function el(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  }

  var SVG_NS = "http://www.w3.org/2000/svg";
  function icon(name, className) {
    var svg = document.createElementNS(SVG_NS, "svg");
    if (className) svg.setAttribute("class", className);
    var use = document.createElementNS(SVG_NS, "use");
    use.setAttribute("href", "#i-" + name);
    svg.appendChild(use);
    return svg;
  }

  function setText(id, text) {
    var node = $(id);
    if (node) node.textContent = text;
  }

  function applyI18n() {
    document.documentElement.lang = state.lang === "zh" ? "zh-CN" : "en";
    setText("stateRetry", t("retry"));
    setText("confirmCancel", t("cancel"));
    setText("tokenTitle", t("tokenTitle"));
    setText("tokenClear", t("clear"));
    setText("tokenSave", t("save"));
    var input = $("tokenInput");
    if (input) input.placeholder = t("tokenPlaceholder");
    var title = $("appTitle");
    if (title) document.title = title.textContent;
    var langLabel = $("langLabel");
    if (langLabel) langLabel.textContent = state.lang === "zh" ? "EN" : "中";

    $("themeButton").title = t("theme");
    var configLink = $("configLink");
    if (configLink) configLink.title = t("config");
    if (state.loaded) render();
  }

  function themeIconName(theme) {
    return theme === "light" ? "sun" : theme === "dark" ? "moon" : "auto";
  }

  function applyTheme(theme, persist) {
    if (theme !== "auto" && theme !== "light" && theme !== "dark") theme = "auto";
    state.theme = theme;
    document.documentElement.setAttribute("data-theme", theme);
    var use = document.querySelector("#themeIcon use");
    if (use) use.setAttribute("href", "#i-" + themeIconName(theme));
    updateThemeMeta();
    if (persist) {
      try { localStorage.setItem("wolTheme", theme); } catch (error) { /* ignore */ }
    }
  }

  function updateThemeMeta() {
    var dark = state.theme === "dark" || (state.theme === "auto" && window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
    var meta = document.querySelector('meta[name="theme-color"]');
    if (meta) meta.setAttribute("content", dark ? "#0b1220" : "#f3f6fb");
  }

  function cycleTheme() {
    var order = ["auto", "light", "dark"];
    var next = order[(order.indexOf(state.theme) + 1) % order.length];
    applyTheme(next, true);
  }

  async function loginWithPassword(password) {
    var response = await fetch("/api/auth/login", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-WOL-Config": "1" },
      body: JSON.stringify({ password: password })
    });
    var payload = null;
    try { payload = await response.json(); } catch (error) { payload = null; }
    if (!response.ok) {
      var failure = new Error(response.status === 429
        ? "登录失败次数过多，请稍后再试"
        : (payload && payload.error) || t("unauthorized"));
      failure.status = response.status;
      throw failure;
    }
    return payload || {};
  }

  async function logoutSession() {
    try {
      await fetch("/api/auth/logout", {
        method: "POST",
        credentials: "same-origin",
        headers: { "X-WOL-Config": "1" }
      });
    } catch (error) { /* ignore */ }
    state.loaded = false;
    state.devices = [];
    window.location.reload();
  }

  async function api(path, options) {
    var headers = { Accept: "application/json", "X-WOL-Config": "1" };
    var body;
    if (options && options.body) {
      headers["Content-Type"] = "application/json";
      body = options.body;
    }
    var response = await fetch(path, {
      method: options && options.method ? options.method : "GET",
      credentials: "same-origin",
      cache: "no-store",
      headers: headers,
      body: body
    });
    var payload = null;
    try { payload = await response.json(); } catch (error) { payload = null; }
    if (response.status === 401) {
      openTokenModal(true);
      var unauthorized = new Error(t("unauthorized"));
      unauthorized.unauthorized = true;
      throw unauthorized;
    }
    if (!response.ok) {
      var message = payload && payload.error ? payload.error : "HTTP " + response.status;
      throw new Error(message);
    }
    return payload || {};
  }

  function showToast(message, kind, duration) {
    var toast = $("toast");
    if (!toast) return;
    toast.textContent = message;
    toast.className = "toast is-visible" + (kind ? " is-" + kind : "");
    clearTimeout(state.toastTimer);
    state.toastTimer = setTimeout(function () {
      toast.className = "toast";
    }, duration || 2800);
  }

  function showState(kind, title, copy) {
    var panel = $("statePanel");
    var iconBox = panel.querySelector(".state-icon");
    iconBox.textContent = "";
    if (kind === "loading") {
      var loader = el("div", "loader");
      loader.setAttribute("aria-hidden", "true");
      iconBox.appendChild(loader);
    } else {
      iconBox.appendChild(icon("router"));
    }
    setText("stateTitle", title);
    setText("stateCopy", copy);
    $("stateRetry").hidden = kind !== "error";
    panel.hidden = false;
    $("deviceGrid").hidden = true;
  }

  function hideState() {
    $("statePanel").hidden = true;
    $("deviceGrid").hidden = false;
  }

  function deviceTone(device) {
    switch (device.reachability_state) {
      case "Reachable": return "online";
      case "PartiallyReachable": return "partial";
      case "Unreachable": return "offline";
    }
    return "unknown";
  }

  function toneLabel(tone) {
    switch (tone) {
      case "online": return t("stateOnline");
      case "partial": return t("statePartial");
      case "offline": return t("stateOffline");
      default: return t("stateUnknown");
    }
  }

  function cameraTone(device) {
    if (device.mask_error) return "offline";
    if (device.mask_state === "on") return "masked";
    if (device.mask_state === "off") return "online";
    return "unknown";
  }

  function cameraBadgeLabel(device) {
    if (device.mask_error) return t("stateOffline");
    return maskLabel(device.mask_state);
  }

  function maskLabel(maskState) {
    if (maskState === "on") return t("maskOn");
    if (maskState === "off") return t("maskOff");
    return t("maskUnknown");
  }

  function cameraAddress(device) {
    var host = device.camera_host || "--";
    var port = device.camera_port || 80;
    return host + ":" + port;
  }

  function actualPowerState(device) {
    if (device.reachability_state === "Reachable" || device.reachability_state === "PartiallyReachable")
      return "on";
    if (device.reachability_state === "Unreachable")
      return "off";
    return "unknown";
  }

  function powerStateOf(device) {
    var actual = actualPowerState(device);
    if (actual !== "unknown")
      return actual;
    return state.localPower.get(device.key) || "unknown";
  }

  function wait(ms) {
    return new Promise(function (resolve) { window.setTimeout(resolve, ms); });
  }

  function updateDevice(status) {
    state.devices = state.devices.map(function (device) {
      return device.key === status.key ? status : device;
    });
  }

  function listSummary(items, empty) {
    if (!items || items.length === 0) return empty;
    if (items.length === 1) return items[0];
    return items[0] + " +" + (items.length - 1);
  }

  function buildComet(power) {
    var track = el("div", "comet-track" + (power.finishing ? "" : " visible") +
      (power.target === "off" ? " off" : ""));
    var rotator = el("div", "comet-rotator");
    rotator.appendChild(el("div", "comet-tail"));
    rotator.appendChild(el("div", "comet-head"));
    track.appendChild(rotator);

    var elapsed = Math.max(0, (Date.now() - power.startedAt) / 1000);
    track.style.setProperty("--comet-delay", (-elapsed).toFixed(2) + "s");
    if (!power.finishing && elapsed < 0.2) {
      track.style.opacity = "0";
      requestAnimationFrame(function () {
        requestAnimationFrame(function () { track.style.opacity = ""; });
      });
    }
    return track;
  }

  function buildPowerArea(device, power, current) {
    var area = el("div", "power-area" + (power ? " loading" : ""));
    var wrap = el("div", "power-wrap");
    if (power)
      wrap.appendChild(buildComet(power));

    var button = el("button", "power-button state-" + current + (power ? " is-loading" : ""));
    button.type = "button";

    var label, hint;
    if (power) {
      label = power.target === "on" ? t("poweringOn") : t("poweringOff");
      hint = t("powerWaiting");
      button.disabled = true;
    }
    else if (current === "on") {
      label = t("powerOn");
      hint = device.can_shutdown ? t("powerOnHint") : t("noShutdown");
      button.disabled = !device.can_shutdown;
    }
    else if (current === "off") {
      label = t("powerOff");
      hint = device.can_wakeup ? t("powerOffHint") : t("noWake");
      button.disabled = !device.can_wakeup;
    }
    else {
      label = t("powerUnknown");
      hint = device.can_wakeup ? t("powerUnknownHint") : t("noWake");
      button.disabled = !device.can_wakeup;
    }

    button.appendChild(icon("power"));
    button.appendChild(el("span", "power-label", label));
    button.appendChild(el("span", "power-hint", hint));
    button.setAttribute("aria-label", label + " · " + hint);
    button.addEventListener("click", function () { onPowerClick(device); });
    wrap.appendChild(button);
    area.appendChild(wrap);
    return area;
  }

  function buildCameraArea(device) {
    var busy = state.maskBusy.get(device.key) || false;
    var maskState = device.mask_state || "unknown";
    var offline = Boolean(device.mask_error);
    var uiState = offline ? "unknown" : (maskState === "on" ? "masked" : (maskState === "off" ? "on" : "unknown"));
    var area = el("div", "power-area camera-area" + (busy ? " loading" : ""));
    var wrap = el("div", "power-wrap");
    var button = el("button", "power-button camera-button state-" + uiState + (busy ? " is-loading" : ""));
    button.type = "button";

    var label, hint;
    if (busy) {
      label = t("maskBusy");
      hint = t("maskBusyHint");
      button.disabled = true;
    } else if (offline) {
      label = t("stateOffline");
      hint = t("maskErrorHint");
    } else if (maskState === "on") {
      label = t("maskOn");
      hint = t("maskOnHint");
    } else if (maskState === "off") {
      label = t("maskOff");
      hint = t("maskOffHint");
    } else {
      label = t("maskUnknown");
      hint = t("maskUnknownHint");
    }

    button.appendChild(icon(maskState === "on" ? "eye-off" : "eye"));
    button.appendChild(el("span", "power-label", label));
    button.appendChild(el("span", "power-hint", hint));
    button.setAttribute("aria-label", label + " · " + hint);
    button.addEventListener("click", function () { onMaskClick(device); });
    wrap.appendChild(button);
    area.appendChild(wrap);
    return area;
  }

  async function onMaskClick(device) {
    var key = device.key;
    if (state.maskBusy.get(key)) return;
    var current = device.mask_state;
    state.maskBusy.set(key, true);
    render();

    var changed = false;
    try {
      var data;
      if (!device.mask_error && (current === "on" || current === "off")) {
        var target = current !== "on";
        data = await api("/api/devices/" + encodeURIComponent(key) + "/mask", {
          method: "POST",
          body: JSON.stringify({ masked: target })
        });
        if (data.device) updateDevice(data.device);
        changed = true;
        showToast(target ? t("maskSetOn") : t("maskSetOff"), "success");
      } else {
        data = await api("/api/devices/" + encodeURIComponent(key) + "/mask");
        if (data.device) updateDevice(data.device);
        changed = true;
      }
    } catch (error) {
      if (!error.unauthorized) showToast(error.message || t("maskFailed"), "error", 5200);
    } finally {
      state.maskBusy.delete(key);
      render();
      if (changed) load(false);
    }
  }

  function createDeviceCard(device) {
    var camera = Boolean(device.camera_enabled);
    var power = state.power.get(device.key) || null;
    var current = power ? power.from : powerStateOf(device);
    var tone = deviceTone(device);
    if (camera) tone = cameraTone(device);
    var card = el("article", "device-card tone-" + tone + (power ? " is-power-busy" : ""));
    card.dataset.deviceKey = device.key;
    if (!camera) card.classList.add("is-computer");

    var top = el("div", "card-top");
    var iconBox = el("div", "device-icon");
    iconBox.appendChild(icon(camera ? "camera" : (tone === "offline" ? "wifi-off" : "monitor")));
    top.appendChild(iconBox);

    var titleWrap = el("div", "device-title");
    titleWrap.appendChild(el("h3", null, device.name || device.key));

    if (camera) {
      var addressLine = el("p", "device-sub");
      addressLine.appendChild(el("span", "device-sub-label", t("cameraAddress")));
      addressLine.appendChild(el("span", "device-sub-value mono", cameraAddress(device)));
      titleWrap.appendChild(addressLine);
    } else {
      var macLine = el("p", "device-sub");
      macLine.appendChild(el("span", "device-sub-label", t("mac")));
      macLine.appendChild(el("span", "device-sub-value mono", listSummary(device.macs, "--")));
      titleWrap.appendChild(macLine);

      var broadcastLine = el("p", "device-sub");
      broadcastLine.appendChild(el("span", "device-sub-label", t("broadcast")));
      broadcastLine.appendChild(el("span", "device-sub-value", listSummary(device.broadcast_ips, t("autoBroadcast"))));
      titleWrap.appendChild(broadcastLine);
    }

    top.appendChild(titleWrap);
    var badgeText = camera ? cameraBadgeLabel(device) : toneLabel(tone);
    top.appendChild(el("span", "badge", badgeText));

    card.appendChild(top);
    card.appendChild(camera ? buildCameraArea(device) : buildPowerArea(device, power, current));

    return card;
  }

  function render() {
    var grid = $("deviceGrid");
    grid.textContent = "";
    grid.classList.toggle("is-compact", state.devices.length > 2);

    if (state.devices.length === 0) {
      showState("empty", t("emptyTitle"), t("emptyCopy"));
      return;
    }
    hideState();
    state.devices.forEach(function (device) {
      grid.appendChild(createDeviceCard(device));
    });
  }

  async function load(showLoading) {
    if (showLoading && !state.loaded) {
      showState("loading", t("loadingTitle"), t("loadingCopy"));
    }
    try {
      var data = await api("/api/devices");
      state.devices = Array.isArray(data.devices) ? data.devices : [];
      state.devices.forEach(function (device) {
        if (actualPowerState(device) !== "unknown")
          state.localPower.delete(device.key);
      });
      var knownKeys = new Set(state.devices.map(function (device) { return device.key; }));
      state.power.forEach(function (value, key) {
        if (!knownKeys.has(key))
          state.power.delete(key);
      });
      state.maskBusy.forEach(function (value, key) {
        if (!knownKeys.has(key))
          state.maskBusy.delete(key);
      });
      state.server = data.server || state.server;
      state.loaded = true;
      if (state.server.title) {
        setText("appTitle", state.server.title);
        document.title = state.server.title;
      }
      var configLink = $("configLink");
      if (configLink) configLink.hidden = !state.server.config_panel_enabled;
      render();
    } catch (error) {
      if (error.unauthorized) {
        if (!state.loaded) showState("empty", t("tokenTitle"), t("tokenCopyRequired"));
        return;
      }
      if (!state.loaded) {
        var offline = !navigator.onLine || error instanceof TypeError;
        showState("error", t("requestFailed"), offline ? t("offlineCopy") : (error.message || t("requestFailed")));
      } else {
        showToast(error.message || t("requestFailed"), "error");
      }
    }
  }

  function onPowerClick(device) {
    if (state.power.has(device.key))
      return;
    var current = powerStateOf(device);
    if (current === "on") {
      if (!device.can_shutdown) {
        showToast(t("noShutdown"), "warn");
        return;
      }
      openConfirm("shutdown", device);
      return;
    }
    if (!device.can_wakeup) {
      showToast(t("noWake"), "warn");
      return;
    }
    startPower(device, "on");
  }

  function startPower(device, target) {
    var key = device.key;
    if (state.power.has(key))
      return;
    if (target === "off" && !device.can_shutdown) {
      showToast(t("noShutdown"), "warn");
      return;
    }
    if (target === "on" && !device.can_wakeup) {
      showToast(t("noWake"), "warn");
      return;
    }

    var power = {
      target: target,
      from: powerStateOf(device),
      startedAt: Date.now(),
      finishing: false
    };
    state.power.set(key, power);
    render();

    api("/api/devices/" + encodeURIComponent(key) + "/" + (target === "on" ? "wake" : "shutdown"), { method: "POST" })
      .then(function (data) {
        if (state.power.get(key) !== power)
          return;
        var result = data.result || {};
        if (result.uncertain)
          showToast(t("uncertain"), "warn", 5200);
        showToast(target === "on" ? t("powerWakeSent") : t("powerShutdownSent"), "warn", 3400);
        if (result.warnings && result.warnings.length) {
          window.setTimeout(function () {
            showToast(result.warnings.join("; "), "warn", 5200);
          }, 900);
        }
        if (!device.reachability_enabled) {
          wait(900).then(function () {
            if (state.power.get(key) !== power)
              return;
            finishPower(key, {
              localState: target,
              toast: t("powerNoProbe"),
              kind: "warn",
              duration: 5200
            });
          });
          return;
        }
        pollPower(device, target, power);
      })
      .catch(function (error) {
        if (state.power.get(key) !== power)
          return;
        if (error.unauthorized) {
          state.power.delete(key);
          render();
          load(false);
          return;
        }
        finishPower(key, {
          toast: error.message || t("actionFailed"),
          kind: "error",
          duration: 5200
        });
      });
  }

  async function pollPower(device, target, power) {
    var key = device.key;
    var deadline = Date.now() + (target === "on" ? 90000 : 60000);
    while (Date.now() < deadline) {
      await wait(3000);
      if (state.power.get(key) !== power)
        return;
      var status = null;
      try {
        var data = await api("/api/devices/" + encodeURIComponent(key) + "/probe", { method: "POST" });
        status = data.device || null;
      } catch (error) {
        if (error.unauthorized) {
          if (state.power.get(key) === power) {
            state.power.delete(key);
            render();
          }
          return;
        }
      }
      if (state.power.get(key) !== power)
        return;
      if (status) {
        updateDevice(status);
        if (actualPowerState(status) === target) {
          finishPower(key, {
            toast: target === "on" ? t("powerOnConfirmed") : t("powerOffConfirmed"),
            kind: "success"
          });
          return;
        }
      }
    }
    finishPower(key, {
      toast: target === "on" ? t("powerOnTimeout") : t("powerOffTimeout"),
      kind: "warn",
      duration: 5600
    });
  }

  function finishPower(key, outcome) {
    var power = state.power.get(key);
    if (!power || power.finishing)
      return;
    power.finishing = true;

    var card = null;
    var cards = document.querySelectorAll(".device-card");
    for (var i = 0; i < cards.length; i++) {
      if (cards[i].dataset.deviceKey === key) {
        card = cards[i];
        break;
      }
    }
    if (card) {
      var comet = card.querySelector(".comet-track");
      var button = card.querySelector(".power-button");
      if (comet) comet.classList.remove("visible");
      if (button) button.disabled = true;
    }

    window.setTimeout(function () {
      if (state.power.get(key) !== power)
        return;
      state.power.delete(key);
      if (outcome.localState)
        state.localPower.set(key, outcome.localState);
      render();
      load(false);
      if (outcome.toast)
        showToast(outcome.toast, outcome.kind || "success", outcome.duration || 3600);
    }, 750);
  }

  function openConfirm(kind, device) {
    state.pending = { kind: kind, device: device };
    $("confirmIcon").className = "modal-icon danger";
    setText("confirmTitle", t("shutdownTitle"));
    setText("confirmCopy", formatText(t("shutdownCopy"), { name: device.name || device.key }));
    var ok = $("confirmOk");
    ok.textContent = t("confirm");
    ok.className = "btn danger";
    $("confirmModal").hidden = false;
    ok.focus();
  }

  function closeConfirm() {
    state.pending = null;
    $("confirmModal").hidden = true;
  }

  function openTokenModal(required) {
    $("tokenModal").hidden = false;
    $("tokenError").hidden = true;
    setText("tokenCopy", required ? t("tokenCopyRequired") : t("tokenCopyOptional"));
    var input = $("tokenInput");
    input.value = "";
    input.focus();
    input.select();
  }

  function closeTokenModal() {
    $("tokenModal").hidden = true;
  }

  function bindEvents() {
    $("themeButton").addEventListener("click", cycleTheme);
    $("langButton").addEventListener("click", function () {
      state.lang = state.lang === "zh" ? "en" : "zh";
      try { localStorage.setItem("wolLang", state.lang); } catch (error) { /* ignore */ }
      applyI18n();
      render();
    });
    $("stateRetry").addEventListener("click", function () { load(false); });

    $("confirmCancel").addEventListener("click", closeConfirm);
    $("confirmClose").addEventListener("click", closeConfirm);
    $("confirmOk").addEventListener("click", function () {
      var pending = state.pending;
      closeConfirm();
      if (pending) startPower(pending.device, "off");
    });
    $("confirmModal").addEventListener("click", function (event) {
      if (event.target === $("confirmModal")) closeConfirm();
    });

    $("tokenCancel") && $("tokenCancel").addEventListener("click", closeTokenModal);
    $("tokenClose").addEventListener("click", closeTokenModal);
    $("tokenModal").addEventListener("click", function (event) {
      if (event.target === $("tokenModal")) closeTokenModal();
    });
    $("tokenSave").addEventListener("click", function () {
      var password = $("tokenInput").value;
      if (!password) {
        $("tokenError").textContent = "请输入访问密码";
        $("tokenError").hidden = false;
        return;
      }
      $("tokenError").hidden = true;
      loginWithPassword(password).then(function () {
        closeTokenModal();
        showToast(t("tokenSaved"), "success", 1800);
        state.loaded = false;
        state.devices = [];
        load(false);
      }).catch(function (error) {
        $("tokenError").textContent = error.message || t("unauthorized");
        $("tokenError").hidden = false;
      });
    });
    $("tokenClear").addEventListener("click", function () {
      logoutSession();
    });
    $("tokenInput").addEventListener("keydown", function (event) {
      if (event.key === "Enter") $("tokenSave").click();
    });

    document.addEventListener("keydown", function (event) {
      if (event.key === "Escape") {
        closeConfirm();
        closeTokenModal();
      }
    });

    if (window.matchMedia) {
      var media = window.matchMedia("(prefers-color-scheme: dark)");
      if (media.addEventListener) media.addEventListener("change", updateThemeMeta);
      else if (media.addListener) media.addListener(updateThemeMeta);
    }

    document.addEventListener("visibilitychange", function () {
      if (!document.hidden && state.loaded) load(false);
    });

    state.refreshTimer = setInterval(function () {
      if (document.hidden) return;
      if (!$("confirmModal").hidden || !$("tokenModal").hidden) return;
      load(false);
    }, REFRESH_MS);
  }

  function registerServiceWorker() {
    // 局域网走 http 时不是安全上下文，浏览器会直接拒绝注册，这里静默跳过
    if (!("serviceWorker" in navigator) || !window.isSecureContext) return;
    window.addEventListener("load", function () {
      navigator.serviceWorker.register("/sw.js", { scope: "/", updateViaCache: "none" }).catch(function () {});
    });
  }

  function init() {
    applyTheme(state.theme, false);
    applyI18n();
    bindEvents();
    registerServiceWorker();
    showState("loading", t("loadingTitle"), t("loadingCopy"));
    load(true);
  }

  init();
})();

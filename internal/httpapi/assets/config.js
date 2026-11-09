(function () {
  "use strict";

  const THEMES = ["auto", "light", "dark"];
  const SVG_NS = "http://www.w3.org/2000/svg";
  const DEVICE_TYPE_COMPUTER = "computer";
  const DEVICE_TYPE_CAMERA = "tplink-camera";

  const state = {
    cfg: null,
    meta: null,
    secrets: null,
    status: null,
    dirty: false,
    saving: false,
    secretsVisible: false,
    theme: localStorage.getItem("wolTheme") || "auto",
    editingIndex: -1,
    editingDevice: null,
    editingSecretStatus: null,
    editingSecretRef: null,
    confirmCallback: null,
    toastTimer: null
  };

  function $(id) { return document.getElementById(id); }

  function el(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined && text !== null) node.textContent = text;
    return node;
  }

  function icon(name, className) {
    const svg = document.createElementNS(SVG_NS, "svg");
    if (className) svg.setAttribute("class", className);
    const use = document.createElementNS(SVG_NS, "use");
    use.setAttribute("href", "#i-" + name);
    svg.appendChild(use);
    return svg;
  }

  function setText(id, text) {
    const node = $(id);
    if (node) node.textContent = text === undefined || text === null ? "" : String(text);
  }

  function clone(value) { return JSON.parse(JSON.stringify(value)); }

  function val(id) {
    const node = $(id);
    return node ? node.value.trim() : "";
  }

  function raw(id) {
    const node = $(id);
    return node ? node.value : "";
  }

  function checked(id) {
    const node = $(id);
    return Boolean(node && node.checked);
  }

  function num(id, fallback) {
    const node = $(id);
    if (!node) return fallback;
    const parsed = parseInt(node.value, 10);
    return Number.isFinite(parsed) ? parsed : fallback;
  }

  function lines(id) {
    const node = $(id);
    if (!node) return [];
    return node.value.split(/\r?\n/).map(function (line) { return line.trim(); }).filter(Boolean);
  }

  function duration(id) {
    const value = val(id);
    return value || "0s";
  }

  function displayDuration(value) {
    if (!value || value === "0s" || value === "0") return "";
    return value;
  }

  function parseListen(value) {
    const text = String(value || "").trim();
    if (/^\d+$/.test(text)) return { host: "", port: text };
    const ipv6 = text.match(/^\[([^\]]+)\]:(\d+)$/);
    if (ipv6) return { host: ipv6[1], port: ipv6[2] };
    const index = text.lastIndexOf(":");
    if (index >= 0) return { host: text.slice(0, index), port: text.slice(index + 1) };
    return { host: "", port: text };
  }

  function buildListen(host, port) {
    const cleanHost = String(host || "").trim();
    const cleanPort = String(port || "").trim();
    if (!cleanHost) return ":" + cleanPort;
    if (cleanHost.startsWith("[")) return cleanHost + ":" + cleanPort;
    if (cleanHost.includes(":")) return "[" + cleanHost + "]:" + cleanPort;
    return cleanHost + ":" + cleanPort;
  }

  function basename(path) {
    if (!path) return "";
    const parts = String(path).split(/[\\/]/);
    return parts[parts.length - 1];
  }

  function setVal(id, value) {
    const node = $(id);
    if (node) node.value = value === undefined || value === null ? "" : String(value);
  }

  function setChecked(id, value) {
    const node = $(id);
    if (node) node.checked = Boolean(value);
  }

  function showToast(message, kind, duration) {
    const toast = $("toast");
    if (!toast) return;
    toast.textContent = message;
    toast.className = "toast is-visible" + (kind ? " is-" + kind : "");
    clearTimeout(state.toastTimer);
    state.toastTimer = setTimeout(function () {
      toast.className = "toast";
    }, duration || 3000);
  }

  function applyTheme(theme, persist) {
    if (THEMES.indexOf(theme) === -1) theme = "auto";
    state.theme = theme;
    document.documentElement.setAttribute("data-theme", theme);
    const use = document.querySelector("#themeIcon use");
    if (use) use.setAttribute("href", "#i-" + (theme === "light" ? "sun" : theme === "dark" ? "moon" : "auto"));
    const meta = document.querySelector('meta[name="theme-color"]');
    if (meta) {
      const dark = theme === "dark" || (theme === "auto" && window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
      meta.setAttribute("content", dark ? "#0b1220" : "#f3f6fb");
    }
    if (persist) {
      try { localStorage.setItem("wolTheme", theme); } catch (error) { /* ignore */ }
    }
  }

  function cycleTheme() {
    const next = THEMES[(THEMES.indexOf(state.theme) + 1) % THEMES.length];
    applyTheme(next, true);
  }

  async function request(path, options) {
    options = options || {};
    const headers = { Accept: "application/json", "X-WOL-Config": "1" };
    if (options.body) headers["Content-Type"] = "application/json";
    let response;
    try {
      response = await fetch(path, {
        method: options.method || "GET",
        credentials: "same-origin",
        cache: "no-store",
        headers: headers,
        body: options.body
      });
    } catch (error) {
      throw new Error("无法连接服务，请确认服务正在运行");
    }
    let payload = null;
    try { payload = await response.json(); } catch (error) { payload = null; }
    if (response.status === 401 && options.auth !== false) {
      const unauthorized = new Error("登录状态已失效");
      unauthorized.unauthorized = true;
      unauthorized.status = 401;
      showLogin("登录状态已失效，请重新登录");
      throw unauthorized;
    }
    if (!response.ok) {
      const failure = new Error(payload && payload.error ? payload.error : "HTTP " + response.status);
      failure.status = response.status;
      throw failure;
    }
    return payload || {};
  }

  function showFatal(message) {
    const screen = $("loadingScreen");
    screen.textContent = "";
    const iconBox = el("div", "state-icon");
    iconBox.appendChild(icon("alert"));
    const title = el("h2", null, "配置面板不可用");
    const copy = el("p", null, message);
    const back = el("a", "btn ghost", "返回快速面板");
    back.href = "/";
    screen.appendChild(iconBox);
    screen.appendChild(title);
    screen.appendChild(copy);
    screen.appendChild(back);
    screen.hidden = false;
    $("loginScreen").hidden = true;
    $("configApp").hidden = true;
  }

  function showLogin(message) {
    $("loadingScreen").hidden = true;
    $("configApp").hidden = true;
    $("loginScreen").hidden = false;
    $("logoutButton").hidden = true;
    const error = $("loginError");
    if (message) {
      error.textContent = message;
      error.hidden = false;
    } else {
      error.textContent = "";
      error.hidden = true;
    }
    const input = $("loginPassword");
    input.value = "";
    setTimeout(function () { input.focus(); }, 40);
  }

  function hideLogin() {
    $("loginScreen").hidden = true;
  }

  async function handleLogin(event) {
    event.preventDefault();
    const button = $("loginSubmit");
    const input = $("loginPassword");
    button.disabled = true;
    button.textContent = "验证中…";
    $("loginError").hidden = true;
    try {
      await request("/api/config/login", {
        method: "POST",
        body: JSON.stringify({ password: input.value }),
        auth: false
      });
      hideLogin();
      await loadConfig();
    } catch (error) {
      $("loginError").textContent = error.status === 429 ? error.message : "访问密码错误，请重试";
      $("loginError").hidden = false;
      input.focus();
      input.select();
    } finally {
      button.disabled = false;
      button.textContent = "进入配置";
    }
  }

  function logout() {
    if (state.dirty) {
      showConfirm("退出配置中心", "当前有未保存的修改，退出后将丢失。确认退出？", "确认退出", function () {
        performLogout();
      });
      return;
    }
    performLogout();
  }

  async function performLogout() {
    try {
      await request("/api/config/logout", { method: "POST", auth: false });
    } catch (error) { /* ignore */ }
    state.cfg = null;
    state.meta = null;
    state.dirty = false;
    showLogin("已退出配置中心");
  }

  function adoptConfig(cfg) {
    state.cfg = cfg || {};
    if (!Array.isArray(state.cfg.devices)) state.cfg.devices = [];
  }

  async function loadConfig() {
    const data = await request("/api/config");
    adoptConfig(data.config);
    state.meta = data.meta || {};
    state.secrets = data.secrets || {};
    state.dirty = false;
    state.secretsVisible = false;
    renderAll();
    $("loadingScreen").hidden = true;
    hideLogin();
    $("configApp").hidden = false;
  }

  function renderAll() {
    if (!state.cfg) return;
    renderGlobalForm();
    renderDevices();
    renderMeta();
    setDirty(false);
  }

  function renderGlobalForm() {
    const cfg = state.cfg;
    setVal("sTitle", cfg.server.title);
    const listen = parseListen(cfg.server.listen);
    setVal("sListenPort", listen.port);
    setVal("sListenHost", listen.host);
    setVal("sTLSCert", cfg.server.tls_cert);
    setVal("sTLSKey", cfg.server.tls_key);
    setVal("sReadTimeout", displayDuration(cfg.server.read_timeout));
    setVal("sShutdownTimeout", displayDuration(cfg.server.shutdown_timeout));

    setChecked("cpEnabled", cfg.config_panel && cfg.config_panel.enabled);
    setVal("cpPassword", "");
    setChecked("cpClear", false);
    const auth = cfg.auth || {};
    setChecked("authQuickPanel", auth.quick_panel);
    setChecked("authConfigPanel", auth.config_panel);
    setChecked("authTrustEnabled", auth.trusted_networks_enabled);
    setVal("authTrustedNetworks", (auth.trusted_networks || []).join("\n"));
    refreshSecretHints();
    updateConfigPanelState();

    setVal("probeInterval", displayDuration(cfg.probe.interval));
    setVal("probeTimeout", displayDuration(cfg.probe.timeout));
    setVal("logLevel", cfg.log.level);
    setVal("logFormat", cfg.log.format);
  }

  function refreshSecretHints() {
    const meta = state.meta || {};
    $("cpPassword").placeholder = meta.password_set
      ? "已设置（哈希保存），留空保持"
      : "尚未设置访问密码";
  }

  function updateConfigPanelState() {
    const enabled = checked("cpEnabled");
    const hasPassword = raw("cpPassword").length > 0 ||
      ((state.meta && state.meta.password_set) && !checked("cpClear"));
    $("authConfigPanelField").hidden = !enabled;
    $("cpDisabledNote").hidden = enabled;
    const trustEnabled = checked("authTrustEnabled");
    $("authTrustedNetworks").disabled = !trustEnabled;
    const scopes = checked("authQuickPanel") || (enabled && checked("authConfigPanel"));
    const warning = $("authPasswordWarning");
    const warningText = warning.querySelector("span");
    if (scopes && !hasPassword) {
      warning.hidden = false;
      warningText.textContent = "已经勾选需要密码的入口，但还没设置访问密码，这些入口现在仍然开放。";
    }
    else if (hasPassword && enabled && !checked("authConfigPanel")) {
      warning.hidden = false;
      warningText.textContent = "配置面板保护没启用：/config 和 /api/config* 不需要密码，能访问端口的人都能改配置。";
    }
    else if (hasPassword && !scopes) {
      warning.hidden = false;
      warningText.textContent = "访问密码已设置，但快速面板/API 和配置面板都没有启用保护，两个入口现在都不校验密码。";
    }
    else {
      warning.hidden = true;
      warningText.textContent = "";
    }
    const logout = $("logoutButton");
    logout.hidden = !(state.status && state.status.auth_required && (state.meta && state.meta.password_set));
  }

  function renderMeta() {
    const meta = state.meta || {};
    const cfg = state.cfg;
    setText("cfgPath", meta.config_path || "");
    setText("aboutPath", meta.config_path || "--");
    setText("aboutSecretsPath", meta.secrets_path || "--");
    setText("aboutSecretsKeyPath", meta.secrets_key_path || "--");
    setText("aboutVersion", meta.version || "--");
    setText("footerVersion", "WOL v" + (meta.version || "--"));
    setText("aboutToken", meta.password_set ? "已设置（哈希）" : "未设置");
    const auth = cfg.auth || {};
    let panel = "已关闭";
    if (cfg.config_panel && cfg.config_panel.enabled) {
      panel = (meta.password_set && auth.config_panel) ? "已启用 · 需要密码" : "已启用";
    }
    setText("aboutPanel", panel);
    setText("aboutConfigName", basename(meta.config_path) || "wol.yaml");
    setText("authClientIP", meta.client_ip || "--");
    $("logoutButton").hidden = !(state.status && state.status.auth_required && meta.password_set);
    if (cfg.server.title) document.title = cfg.server.title + " · 配置中心";
  }

  function setDirty(dirty) {
    state.dirty = Boolean(dirty);
    const indicator = $("saveState");
    indicator.classList.toggle("is-dirty", state.dirty);
    indicator.textContent = "";
    const dot = el("span");
    indicator.appendChild(dot);
    indicator.appendChild(document.createTextNode(state.dirty ? "有未保存的修改" : "已同步"));
  }

  function updateSaveButton() {
    const button = $("saveButton");
    button.disabled = state.saving;
    setText("saveLabel", state.saving ? "保存中…" : "保存并应用");
  }

  function showSaveError(message) {
    setText("saveErrorText", message || "未知错误");
    $("saveError").hidden = false;
    $("saveError").scrollIntoView({ behavior: "smooth", block: "nearest" });
  }

  function clearSaveError() {
    $("saveError").hidden = true;
    setText("saveErrorText", "");
  }

  function collectConfig() {
    const listenPort = val("sListenPort");
    if (!/^\d+$/.test(listenPort) || Number(listenPort) < 1 || Number(listenPort) > 65535) {
      throw new Error("监听端口必须是 1-65535 的数字");
    }
    const cfg = clone(state.cfg);
    cfg.server = {
      listen: buildListen(val("sListenHost"), listenPort),
      title: val("sTitle"),
      tls_cert: val("sTLSCert"),
      tls_key: raw("sTLSKey"),
      read_timeout: duration("sReadTimeout"),
      shutdown_timeout: duration("sShutdownTimeout")
    };
    cfg.config_panel = {
      enabled: checked("cpEnabled"),
      password: raw("cpPassword"),
      clear_password: checked("cpClear")
    };
    const panelEnabled = checked("cpEnabled");
    cfg.auth = {
      quick_panel: checked("authQuickPanel"),
      config_panel: panelEnabled && checked("authConfigPanel"),
      trusted_networks_enabled: checked("authTrustEnabled"),
      trusted_networks: lines("authTrustedNetworks")
    };
    cfg.log = {
      level: $("logLevel").value,
      format: $("logFormat").value
    };
    cfg.probe = {
      interval: duration("probeInterval"),
      timeout: duration("probeTimeout")
    };
    cfg.devices = state.cfg.devices;
    return cfg;
  }

  async function saveConfig() {
    if (state.saving || !state.cfg) return;
    clearSaveError();
    state.saving = true;
    updateSaveButton();
    try {
      const payload = collectConfig();
      const response = await request("/api/config", { method: "PUT", body: JSON.stringify(payload) });
      adoptConfig(response.config);
      state.meta = response.meta || {};
      state.secrets = response.secrets || {};
      state.dirty = false;
      renderAll();
      let message = "配置已保存并应用";
      if (response.backup) message += " · 配置备份：" + basename(response.backup);
      if (response.secrets_backup) message += " · 密钥备份：" + basename(response.secrets_backup);
      showToast(message, "success", 4200);
      if (response.restart_required && response.restart_fields && response.restart_fields.length) {
        setTimeout(function () {
          showToast("以下设置需要重启进程：" + response.restart_fields.join("、"), "warn", 7000);
        }, 800);
      }
      if (!response.meta.config_panel_enabled) {
        $("disabledModal").hidden = false;
        setTimeout(function () { window.location.href = "/"; }, 1600);
        return;
      }
      if (response.reauth_required) {
        setTimeout(function () { window.location.reload(); }, 1400);
      }
    } catch (error) {
      if (!error.unauthorized) showSaveError(error.message);
    } finally {
      state.saving = false;
      updateSaveButton();
    }
  }

  function renderDevices() {
    const list = $("deviceList");
    list.textContent = "";
    const devices = state.cfg.devices || [];
    setText("deviceCount", devices.length);
    $("deviceEmpty").hidden = devices.length > 0;
    list.hidden = devices.length === 0;
    devices.forEach(function (device, index) {
      list.appendChild(createDeviceCard(device, index));
    });
  }

  function secretStatusFor(device) {
    const ref = device.secret_ref || device.key;
    return (state.secrets && state.secrets.devices && state.secrets.devices[ref]) || {};
  }

  // 已保存的凭据在配置里是空的，刚填写还没保存的凭据只在内存对象里；
  // 两者都算“已有”，否则再次编辑会被要求重新输入。
  function deviceSecretPresence(device) {
    const stored = device.clear_secrets ? {} : secretStatusFor(device);
    const ssh = device.ssh || {};
    const bemfa = (device.iot && device.iot.bemfa) || {};
    const blinker = (device.iot && device.iot.blinker) || {};
    const camera = device.camera || {};
    return {
      ssh_password: Boolean(stored.ssh_password || ssh.password),
      ssh_private_key: Boolean(stored.ssh_private_key || ssh.private_key),
      ssh_passphrase: Boolean(stored.ssh_passphrase || ssh.passphrase),
      bemfa_secret_key: Boolean(stored.bemfa_secret_key || bemfa.secret_key),
      blinker_auth_key: Boolean(stored.blinker_auth_key || blinker.auth_key),
      camera_password: Boolean(stored.camera_password || camera.password)
    };
  }

  function createDeviceCard(device, index) {
    const isCamera = device.type === DEVICE_TYPE_CAMERA;
    const card = el("article", "cfg-device-card");
    const top = el("div", "cfg-device-top");
    const iconBox = el("div", "cfg-device-icon");
    iconBox.appendChild(icon(isCamera ? "camera" : "monitor"));
    top.appendChild(iconBox);

    const title = el("div", "cfg-device-title");
    title.appendChild(el("h3", null, device.name || "未命名设备"));
    title.appendChild(el("p", null, device.key ? device.key : "key 将自动生成"));
    top.appendChild(title);

    const actions = el("div", "cfg-device-actions");
    const edit = el("button", "cfg-mini-btn");
    edit.type = "button";
    edit.title = "编辑设备";
    edit.setAttribute("aria-label", "编辑设备");
    edit.appendChild(icon("pencil"));
    edit.addEventListener("click", function () { openDeviceModal(index); });
    const remove = el("button", "cfg-mini-btn danger");
    remove.type = "button";
    remove.title = "删除设备";
    remove.setAttribute("aria-label", "删除设备");
    remove.appendChild(icon("trash"));
    remove.addEventListener("click", function () { deleteDevice(index); });
    actions.appendChild(edit);
    actions.appendChild(remove);
    top.appendChild(actions);
    card.appendChild(top);

    const meta = el("div", "cfg-device-meta");
    const stored = deviceSecretPresence(device);
    if (isCamera) meta.appendChild(el("span", "cfg-chip blue", "摄像头"));
    if (device.ssh && device.ssh.enabled) meta.appendChild(el("span", "cfg-chip blue", "SSH"));
    else if (device.ssh && (sshHasConfig(device.ssh) || stored.ssh_password || stored.ssh_private_key || stored.ssh_passphrase))
      meta.appendChild(el("span", "cfg-chip amber", "SSH 未启用"));
    if (device.reachability && device.reachability.enabled) meta.appendChild(el("span", "cfg-chip", "探测"));
    if (device.iot && device.iot.bemfa && device.iot.bemfa.enabled) meta.appendChild(el("span", "cfg-chip amber", "巴法"));
    if (device.iot && device.iot.blinker && device.iot.blinker.enabled) meta.appendChild(el("span", "cfg-chip amber", "点灯"));
    const priority = Number(device.priority) || 0;
    if (priority !== 0) meta.appendChild(el("span", "cfg-chip", "优先级 " + priority));
    card.appendChild(meta);

    const macs = device.macs || [];
    const macText = macs.length
      ? macs[0] + (macs.length > 1 ? " +" + (macs.length - 1) : "")
      : (isCamera ? "摄像头设备 · 仅镜头遮蔽" : "未设置 MAC");
    card.appendChild(el("div", "cfg-device-macs", macText));
    return card;
  }

  function sshHasConfig(ssh) {
    if (!ssh) return false;
    return Boolean(
      String(ssh.user || "").trim() ||
      String(ssh.password || "") ||
      String(ssh.private_key || "") ||
      String(ssh.private_key_file || "").trim() ||
      String(ssh.passphrase || "") ||
      String(ssh.host_key || "").trim() ||
      String(ssh.shutdown_command || "").trim() ||
      String(ssh.wake_command || "").trim()
    );
  }

  function formHasSSHConfig() {
    const stored = state.editingSecretStatus || {};
    return Boolean(
      val("dSSHUser") ||
      raw("dSSHPassword") ||
      raw("dSSHKey") ||
      val("dSSHKeyFile") ||
      raw("dSSHPassphrase") ||
      val("dSSHHostKey") ||
      val("dSSHShutdownCommand") ||
      val("dSSHWakeCommand") ||
      stored.ssh_password ||
      stored.ssh_private_key ||
      stored.ssh_passphrase
    );
  }

  function updateSSHDisabledWarning() {
    const warning = $("sshDisabledWarning");
    if (!warning) return;
    warning.hidden = checked("dSSHEnabled") || !formHasSSHConfig();
  }

  function defaultDevice() {
    return {
      key: "",
      type: DEVICE_TYPE_COMPUTER,
      name: "",
      priority: 0,
      macs: [],
      broadcast_ips: [],
      port: 9,
      repeat: 5,
      reachability: { enabled: false, targets: [], method: "ping", tcp_port: 0, interval: "0s", timeout: "0s" },
      ssh: {
        enabled: false, host: "", port: 22, user: "", password: "", private_key: "",
        private_key_file: "", passphrase: "", host_key: "", allow_unknown_host_key: false,
        shutdown_command: "", wake_command: "", timeout: "0s"
      },
      iot: {
        bemfa: { enabled: false, secret_key: "", topic: "" },
        blinker: { enabled: false, auth_key: "", device_type: "outlet" }
      },
      camera: { enabled: false, host: "", port: 80, username: "admin", password: "" }
    };
  }

  function setSecretValue(id, present, placeholder) {
    const node = $(id);
    node.value = "";
    node.placeholder = present ? "已设置，留空保持" : placeholder;
  }

  function currentDeviceType() {
    const node = $("dType");
    return node && node.value === DEVICE_TYPE_CAMERA ? DEVICE_TYPE_CAMERA : DEVICE_TYPE_COMPUTER;
  }

  function applyDeviceTypeUI(type) {
    const isCamera = type === DEVICE_TYPE_CAMERA;
    $("basicComputerFields").hidden = isCamera;
    $("basicCameraFields").hidden = !isCamera;
    $("tabReach").hidden = isCamera;
    $("tabSSH").hidden = isCamera;
    const active = document.querySelector("#deviceTabs .cfg-tab.is-active");
    if (!active || active.hidden) setDeviceTab("basic");
    setDeviceHint("");
  }

  function openDeviceModal(index) {
    state.editingIndex = index;
    state.editingDevice = index >= 0 ? clone(state.cfg.devices[index]) : defaultDevice();
    const device = state.editingDevice;
    state.editingSecretStatus = deviceSecretPresence(device);
    state.editingSecretRef = index >= 0 ? (device.secret_ref || device.key) : null;
    setText("deviceModalTitle", index >= 0 ? "编辑设备" : "添加设备");
    setText("deviceModalSub", index >= 0 ? "修改后点击“应用到列表”，再保存配置。" : "填写设备信息后点击“应用到列表”，再保存配置。");
    setDeviceHint("");

    setVal("dName", device.name);
    setVal("dKey", device.key);
    setVal("dPriority", device.priority || 0);
    setVal("dMACs", (device.macs || []).join("\n"));
    setVal("dBroadcasts", (device.broadcast_ips || []).join("\n"));
    setVal("dPort", device.port || 9);
    setVal("dRepeat", device.repeat || 5);

    const reach = device.reachability || {};
    setChecked("dReachEnabled", reach.enabled);
    setVal("dReachTargets", (reach.targets || []).join("\n"));
    setVal("dReachMethod", reach.method || "ping");
    setVal("dReachTCPPort", reach.tcp_port || "");
    setVal("dReachInterval", displayDuration(reach.interval));
    setVal("dReachTimeout", displayDuration(reach.timeout));

    const ssh = device.ssh || {};
    setChecked("dSSHEnabled", ssh.enabled);
    setVal("dSSHHost", ssh.host);
    setVal("dSSHPort", ssh.port || 22);
    setVal("dSSHUser", ssh.user);
    const stored = state.editingSecretStatus || {};
    setSecretValue("dSSHPassword", stored.ssh_password, "与私钥至少填写一种");
    setVal("dSSHKeyFile", ssh.private_key_file);
    setSecretValue("dSSHKey", stored.ssh_private_key, "-----BEGIN OPENSSH PRIVATE KEY-----");
    setSecretValue("dSSHPassphrase", stored.ssh_passphrase, "没有则留空");
    setVal("dSSHHostKey", ssh.host_key);
    setChecked("dSSHAllowUnknown", ssh.allow_unknown_host_key);
    setVal("dSSHShutdownCommand", ssh.shutdown_command);
    setVal("dSSHWakeCommand", ssh.wake_command);
    setVal("dSSHTimeout", displayDuration(ssh.timeout));
    setChecked("dClearSecrets", false);
    setProbeStatus("", "");
    updateSSHDisabledWarning();

    const iot = device.iot || {};
    const bemfa = iot.bemfa || {};
    const blinker = iot.blinker || {};
    setChecked("dBemfaEnabled", bemfa.enabled);
    setSecretValue("dBemfaSecret", stored.bemfa_secret_key, "巴法云私钥");
    setVal("dBemfaTopic", bemfa.topic);
    setChecked("dBlinkerEnabled", blinker.enabled);
    setSecretValue("dBlinkerAuth", stored.blinker_auth_key, "点灯科技设备 Auth Key");
    setVal("dBlinkerType", blinker.device_type || "outlet");

    const camera = device.camera || {};
    setVal("dType", device.type === DEVICE_TYPE_CAMERA ? DEVICE_TYPE_CAMERA : DEVICE_TYPE_COMPUTER);
    setVal("dCameraHost", camera.host);
    setVal("dCameraPort", camera.port || 80);
    setVal("dCameraUsername", camera.username || "admin");
    setSecretValue("dCameraPassword", stored.camera_password, "TP-Link App 中设置的设备密码");
    applyDeviceTypeUI(currentDeviceType());

    setDeviceTab("basic");
    $("deviceModal").hidden = false;
    document.body.classList.add("modal-open");
    setTimeout(function () { $("dName").focus(); }, 40);
  }

  function closeDeviceModal() {
    $("deviceModal").hidden = true;
    document.body.classList.remove("modal-open");
    state.editingIndex = -1;
    state.editingDevice = null;
    state.editingSecretStatus = null;
    state.editingSecretRef = null;
  }

  function setDeviceTab(tab) {
    document.querySelectorAll("#deviceTabs .cfg-tab").forEach(function (button) {
      button.classList.toggle("is-active", button.getAttribute("data-tab") === tab);
    });
    document.querySelectorAll(".cfg-tab-panel").forEach(function (panel) {
      panel.hidden = panel.getAttribute("data-panel") !== tab;
    });
  }

  function readDeviceForm() {
    const device = clone(state.editingDevice);
    const prevSSH = device.ssh || {};
    const prevIoT = device.iot || {};
    const prevBemfa = prevIoT.bemfa || {};
    const prevBlinker = prevIoT.blinker || {};
    const prevCamera = device.camera || {};
    const type = currentDeviceType();
    const isCamera = type === DEVICE_TYPE_CAMERA;
    // 输入框留空表示“保持”——保留本设备已填写但还没保存的凭据；
    // 勾选清除时不再保留，空就是空。
    const clearing = checked("dClearSecrets");
    const keep = function (input, previous) {
      return input || (clearing ? "" : previous || "");
    };
    device.key = val("dKey");
    if (state.editingIndex >= 0 && state.editingSecretRef && device.key !== state.editingSecretRef && !device.secret_ref)
      device.secret_ref = state.editingSecretRef;
    device.type = type;
    device.name = val("dName");
    device.priority = num("dPriority", 0);
    device.macs = lines("dMACs");
    device.broadcast_ips = lines("dBroadcasts");
    device.port = num("dPort", 0);
    device.repeat = num("dRepeat", 0);
    device.reachability = {
      enabled: isCamera ? false : checked("dReachEnabled"),
      targets: lines("dReachTargets"),
      method: $("dReachMethod").value,
      tcp_port: num("dReachTCPPort", 0),
      interval: duration("dReachInterval"),
      timeout: duration("dReachTimeout")
    };
    device.ssh = {
      enabled: isCamera ? false : checked("dSSHEnabled"),
      host: val("dSSHHost"),
      port: num("dSSHPort", 22),
      user: val("dSSHUser"),
      password: keep(raw("dSSHPassword"), prevSSH.password),
      private_key: keep(raw("dSSHKey"), prevSSH.private_key),
      private_key_file: val("dSSHKeyFile"),
      passphrase: keep(raw("dSSHPassphrase"), prevSSH.passphrase),
      host_key: val("dSSHHostKey"),
      allow_unknown_host_key: checked("dSSHAllowUnknown"),
      shutdown_command: val("dSSHShutdownCommand"),
      wake_command: val("dSSHWakeCommand"),
      timeout: duration("dSSHTimeout")
    };
    const iot = device.iot || {};
    iot.bemfa = {
      enabled: checked("dBemfaEnabled"),
      secret_key: keep(val("dBemfaSecret"), prevBemfa.secret_key),
      topic: val("dBemfaTopic")
    };
    iot.blinker = {
      enabled: checked("dBlinkerEnabled"),
      auth_key: keep(val("dBlinkerAuth"), prevBlinker.auth_key),
      device_type: $("dBlinkerType").value
    };
    device.iot = iot;
    device.camera = {
      enabled: isCamera,
      host: val("dCameraHost"),
      port: num("dCameraPort", 80),
      username: val("dCameraUsername"),
      password: keep(raw("dCameraPassword"), prevCamera.password)
    };
    device.clear_secrets = clearing;
    return device;
  }

  function setDeviceHint(message) {
    const node = $("deviceModalHint");
    node.textContent = message || "";
    node.classList.toggle("is-error", Boolean(message));
  }

  function applyDevice() {
    const device = readDeviceForm();
    const isCamera = device.type === DEVICE_TYPE_CAMERA;
    if (!isCamera && device.macs.length === 0) {
      setDeviceTab("basic");
      setDeviceHint("计算机类型至少填写一个 MAC 地址");
      $("dMACs").focus();
      return;
    }
    if (isCamera) {
      if (!device.camera.host) {
        setDeviceTab("basic");
        setDeviceHint("摄像头主机不能为空");
        $("dCameraHost").focus();
        return;
      }
      const stored = state.editingSecretStatus || {};
      const keptCameraPassword = !device.clear_secrets && stored.camera_password;
      if (!device.camera.password && !keptCameraPassword) {
        setDeviceTab("basic");
        setDeviceHint("摄像头设备密码不能为空");
        $("dCameraPassword").focus();
        return;
      }
    }
    const key = device.key.trim();
    if (key) {
      const duplicate = (state.cfg.devices || []).some(function (other, index) {
        return index !== state.editingIndex && other.key === key;
      });
      if (duplicate) {
        setDeviceTab("basic");
        setDeviceHint("设备 key 已存在，请换一个");
        $("dKey").focus();
        return;
      }
    }
    const hasReachTargets = device.reachability.targets.length > 0;
    const effectiveTargets = hasReachTargets || (device.ssh.enabled && Boolean(device.ssh.host));
    if (device.reachability.enabled && !effectiveTargets) {
      setDeviceTab("reach");
      setDeviceHint("启用探测后至少填写一个探测目标");
      $("dReachTargets").focus();
      return;
    }
    if (!isCamera && device.ssh.enabled) {
      if (!device.ssh.host && !hasReachTargets) {
        setDeviceTab("ssh");
        setDeviceHint("SSH 主机不能为空");
        $("dSSHHost").focus();
        return;
      }
      // 已保存的凭据在表单里永远是空的（占位提示“已设置，留空保持”），
      // 这里必须把状态算进去，否则每次编辑都会被要求重新输入密码。
      const stored = state.editingSecretStatus || {};
      const hasCredential = device.ssh.password || device.ssh.private_key || device.ssh.private_key_file;
      const keptCredential = !device.clear_secrets && (stored.ssh_password || stored.ssh_private_key);
      if (!hasCredential && !keptCredential) {
        setDeviceTab("ssh");
        setDeviceHint("SSH 需要密码或私钥");
        $("dSSHPassword").focus();
        return;
      }
      if (!device.ssh.allow_unknown_host_key && !device.ssh.host_key) {
        setDeviceTab("ssh");
        setDeviceHint("请填写主机指纹，或勾选“首次连接允许未知主机指纹”");
        $("dSSHHostKey").focus();
        return;
      }
      if (!device.ssh.shutdown_command && !device.ssh.wake_command) {
        setDeviceTab("ssh");
        setDeviceHint("请至少填写关机命令或 SSH 唤醒命令");
        $("dSSHShutdownCommand").focus();
        return;
      }
    }
    if (state.editingIndex >= 0) {
      state.cfg.devices[state.editingIndex] = device;
    } else {
      state.cfg.devices.push(device);
    }
    state.cfg.devices.sort(function (a, b) {
      return (Number(a.priority) || 0) - (Number(b.priority) || 0);
    });
    setDirty(true);
    renderDevices();
    closeDeviceModal();
  }

  function deleteDevice(index) {
    const device = state.cfg.devices[index];
    showConfirm("删除设备", "确认删除「" + (device.name || device.key || "未命名设备") + "」？点击保存后才会写入配置文件。", "确认删除", function () {
      state.cfg.devices.splice(index, 1);
      setDirty(true);
      renderDevices();
    });
  }

  function showConfirm(title, copy, okLabel, callback) {
    state.confirmCallback = callback;
    setText("confirmTitle", title);
    setText("confirmCopy", copy);
    $("confirmOk").textContent = okLabel || "确认";
    $("confirmModal").hidden = false;
    document.body.classList.add("modal-open");
    setTimeout(function () { $("confirmOk").focus(); }, 30);
  }

  function closeConfirm() {
    $("confirmModal").hidden = true;
    state.confirmCallback = null;
    document.body.classList.remove("modal-open");
  }

  function setProbeStatus(message, kind) {
    const node = $("sshProbeStatus");
    node.textContent = message || "";
    node.className = "cfg-probe-status" + (kind ? " is-" + kind : "");
  }

  async function probeSSH() {
    const host = val("dSSHHost");
    if (!host) {
      setDeviceTab("ssh");
      setProbeStatus("请先填写 SSH 主机", "error");
      $("dSSHHost").focus();
      return;
    }
    const button = $("sshProbeButton");
    button.disabled = true;
    setProbeStatus("正在连接主机…", "");
    try {
      const device = readDeviceForm();
      const info = await request("/api/config/ssh/hostkey", {
        method: "POST",
        body: JSON.stringify(device.ssh)
      });
      setVal("dSSHHostKey", info.fingerprint);
      setChecked("dSSHAllowUnknown", false);
      setProbeStatus("已获取指纹：" + info.fingerprint, "ok");
    } catch (error) {
      if (!error.unauthorized) setProbeStatus(error.message, "error");
    } finally {
      button.disabled = false;
    }
  }

  function toggleSecrets() {
    state.secretsVisible = !state.secretsVisible;
    document.querySelectorAll(".cfg-secret").forEach(function (node) {
      if (node.tagName !== "INPUT") return;
      node.type = state.secretsVisible ? "text" : "password";
    });
    const use = document.querySelector("#secretsToggle use");
    if (use) use.setAttribute("href", state.secretsVisible ? "#i-eye-off" : "#i-eye");
    $("secretsToggle").title = state.secretsVisible ? "隐藏输入内容" : "显示输入内容";
  }

  function bindPresets() {
    document.querySelectorAll(".cfg-preset-row").forEach(function (row) {
      row.addEventListener("click", function (event) {
        const button = event.target.closest(".cfg-preset");
        if (!button) return;
        const input = document.getElementById(row.getAttribute("data-preset-target"));
        if (!input) return;
        input.value = button.getAttribute("data-value") || "";
        input.dispatchEvent(new Event("input", { bubbles: true }));
        input.focus();
        showToast("已填入预设命令，保存后生效", "success", 1800);
      });
    });
  }

  function bindEvents() {
    $("themeButton").addEventListener("click", cycleTheme);
    $("secretsToggle").addEventListener("click", toggleSecrets);
    bindPresets();
    $("logoutButton").addEventListener("click", logout);
    $("saveButton").addEventListener("click", saveConfig);
    $("saveErrorClose").addEventListener("click", clearSaveError);
    $("loginForm").addEventListener("submit", handleLogin);

    function authUIChanged(event) {
      if (event.target && event.target.id === "cpPassword" && raw("cpPassword").length > 0)
        $("cpClear").checked = false;
      if (event.target && event.target.id === "cpClear" && event.target.checked)
        setVal("cpPassword", "");
      if (event.target && /^(cpEnabled|cpPassword|cpClear|authQuickPanel|authConfigPanel|authTrustEnabled)$/.test(event.target.id))
        updateConfigPanelState();
      setDirty(true);
    }
    $("configApp").addEventListener("input", authUIChanged);
    $("configApp").addEventListener("change", authUIChanged);

    $("addDeviceButton").addEventListener("click", function () { openDeviceModal(-1); });
    $("emptyAddDevice").addEventListener("click", function () { openDeviceModal(-1); });
    $("dType").addEventListener("change", function () { applyDeviceTypeUI(currentDeviceType()); });
    $("deviceModalClose").addEventListener("click", closeDeviceModal);
    $("deviceCancel").addEventListener("click", closeDeviceModal);
    $("deviceApply").addEventListener("click", applyDevice);
    $("sshProbeButton").addEventListener("click", probeSSH);
    document.querySelectorAll("#deviceTabs .cfg-tab").forEach(function (button) {
      button.addEventListener("click", function () { setDeviceTab(button.getAttribute("data-tab")); });
    });

    $("confirmCancel").addEventListener("click", closeConfirm);
    $("confirmOk").addEventListener("click", function () {
      const callback = state.confirmCallback;
      closeConfirm();
      if (callback) callback();
    });
    $("confirmModal").addEventListener("click", function (event) {
      if (event.target === $("confirmModal")) closeConfirm();
    });
    $("deviceModal").addEventListener("click", function (event) {
      if (event.target === $("deviceModal")) closeDeviceModal();
    });
    $("deviceModal").addEventListener("input", updateSSHDisabledWarning);
    $("deviceModal").addEventListener("change", updateSSHDisabledWarning);

    document.addEventListener("keydown", function (event) {
      if (event.key !== "Escape") return;
      if (!$("confirmModal").hidden) closeConfirm();
      else if (!$("deviceModal").hidden) closeDeviceModal();
    });

    document.querySelectorAll(".cfg-nav-link").forEach(function (link) {
      link.addEventListener("click", function (event) {
        event.preventDefault();
        const target = document.querySelector(link.getAttribute("href"));
        if (target) target.scrollIntoView({ behavior: "smooth", block: "start" });
        document.querySelectorAll(".cfg-nav-link").forEach(function (other) { other.classList.remove("is-active"); });
        link.classList.add("is-active");
      });
    });

    window.addEventListener("beforeunload", function (event) {
      if (!state.dirty) return;
      event.preventDefault();
      event.returnValue = "";
    });

    if (window.matchMedia) {
      const media = window.matchMedia("(prefers-color-scheme: dark)");
      const update = function () {
        if (state.theme !== "auto") return;
        const meta = document.querySelector('meta[name="theme-color"]');
        if (meta) meta.setAttribute("content", media.matches ? "#0b1220" : "#f3f6fb");
      };
      if (media.addEventListener) media.addEventListener("change", update);
      else if (media.addListener) media.addListener(update);
    }
  }

  async function init() {
    applyTheme(state.theme, false);
    bindEvents();
    updateSaveButton();
    try {
      const status = await request("/api/config/status", { auth: false });
      state.status = status;
      if (!status.enabled) {
        showFatal("配置面板已关闭。请在配置文件中设置 config_panel.enabled: true 并重启服务。");
        return;
      }
      if (status.auth_required && !status.authenticated) {
        showLogin("");
        return;
      }
      hideLogin();
      await loadConfig();
    } catch (error) {
      if (error.status === 404) {
        showFatal("配置面板已关闭。请在配置文件中设置 config_panel.enabled: true 并重启服务。");
      } else {
        showFatal("无法读取配置面板状态：" + error.message);
      }
    }
  }

  init();
})();

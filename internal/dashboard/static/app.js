function fmtBytes(n) {
  if (!n) return "0";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  let v = Number(n);
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return v.toFixed(v >= 10 || i === 0 ? 0 : 1) + " " + units[i];
}

function fmtMillis(n) {
  if (!n) return "0m";
  return n + "m";
}

function fmtUnixTime(unix) {
  if (!unix) return "never";
  return new Date(Number(unix) * 1000).toLocaleString();
}

function el(tag, className, text) {
  const e = document.createElement(tag);
  if (className) e.className = className;
  if (text !== undefined) e.textContent = text;
  return e;
}

function badge(text, kind) {
  return el("span", "badge " + kind, text);
}

function setRows(tableId, rows, emptyColSpan, renderRow) {
  const tbody = document.querySelector("#" + tableId + " tbody");
  tbody.innerHTML = "";
  if (rows.length === 0) {
    const tr = document.createElement("tr");
    const td = el("td", "empty", "none");
    td.colSpan = emptyColSpan;
    tr.appendChild(td);
    tbody.appendChild(tr);
    return;
  }
  for (const row of rows) {
    tbody.appendChild(renderRow(row));
  }
}

async function fetchJSON(path, options) {
  const resp = await fetch(path, Object.assign({ cache: "no-store" }, options));
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new Error(text || (path + ": " + resp.status));
  }
  return resp.json();
}

function initTabs() {
  const tabs = document.querySelectorAll(".tab");
  tabs.forEach((tab) => {
    tab.addEventListener("click", () => {
      tabs.forEach((t) => t.classList.remove("active"));
      document.querySelectorAll(".tab-panel").forEach((p) => p.classList.remove("active"));
      tab.classList.add("active");
      document.getElementById("tab-" + tab.dataset.tab).classList.add("active");
    });
  });
}

async function refreshNodes() {
  const data = await fetchJSON("/api/nodes");
  const items = data.items || [];
  document.getElementById("nodes-count").textContent = items.length;
  setRows("nodes-table", items, 5, (n) => {
    const tr = document.createElement("tr");
    const status = n.status || {};
    const alloc = status.allocatable || {};
    tr.appendChild(el("td", null, n.metadata?.name || ""));
    const readyTd = document.createElement("td");
    readyTd.appendChild(badge(status.ready ? "ready" : "not ready", status.ready ? "ok" : "bad"));
    tr.appendChild(readyTd);
    tr.appendChild(el("td", null, status.internalIp || "—"));
    tr.appendChild(el("td", null, fmtMillis(alloc.cpuMillis)));
    tr.appendChild(el("td", null, fmtBytes(alloc.memoryBytes)));
    return tr;
  });
}

const metricHistory = {};
const METRIC_HISTORY_MAX = 40;

function pushHistory(key, value) {
  const arr = metricHistory[key] || (metricHistory[key] = []);
  arr.push(value);
  if (arr.length > METRIC_HISTORY_MAX) arr.shift();
  return arr;
}

function sparklineSVG(values) {
  const w = 200;
  const h = 30;
  if (values.length < 2) return `<svg class="sparkline" viewBox="0 0 ${w} ${h}"></svg>`;
  const stepX = w / (values.length - 1);
  const points = values
    .map((v, i) => `${(i * stepX).toFixed(1)},${(h - (Math.min(Math.max(v, 0), 100) / 100) * h).toFixed(1)}`)
    .join(" ");
  return `<svg class="sparkline" viewBox="0 0 ${w} ${h}" preserveAspectRatio="none"><polyline points="${points}"></polyline></svg>`;
}

function metricBlock(label, used, total, fmt, historyKey) {
  const pct = total > 0 ? Math.min(100, (used / total) * 100) : 0;
  const block = el("div", "metric-block");
  const header = el("div", "metric-header");
  header.appendChild(el("span", null, label));
  header.appendChild(el("span", "metric-value", `${fmt(used)} / ${fmt(total)} (${pct.toFixed(0)}%)`));
  block.appendChild(header);

  const bar = el("div", "bar");
  const fill = el("div", "bar-fill" + (pct > 90 ? " danger" : pct > 70 ? " warn" : ""));
  fill.style.width = pct + "%";
  bar.appendChild(fill);
  block.appendChild(bar);

  const history = pushHistory(historyKey, pct);
  const svgWrap = document.createElement("div");
  svgWrap.innerHTML = sparklineSVG(history);
  block.appendChild(svgWrap.firstChild);
  return block;
}

async function refreshMetrics() {
  const data = await fetchJSON("/api/metrics");
  const container = document.getElementById("cluster-resources");
  container.innerHTML = "";

  const clusterGrid = el("div", "metrics-grid");
  clusterGrid.appendChild(metricBlock("CPU requested (cluster)", data.cpuRequestedMillis || 0, data.cpuAllocatableMillis || 0, fmtMillis, "cluster-cpu"));
  clusterGrid.appendChild(metricBlock("Memory requested (cluster)", data.memoryRequestedBytes || 0, data.memoryAllocatableBytes || 0, fmtBytes, "cluster-mem-req"));
  clusterGrid.appendChild(metricBlock("Memory used, live (cluster)", data.memoryUsedBytes || 0, data.memoryAllocatableBytes || 0, fmtBytes, "cluster-mem-used"));
  container.appendChild(clusterGrid);

  const nodes = data.nodes || [];
  if (nodes.length === 0) {
    container.appendChild(el("p", "empty", "no nodes joined yet"));
    return;
  }

  const perNode = el("div", "per-node-metrics");
  for (const n of nodes) {
    const row = el("div", "node-metric-row");
    row.appendChild(el("div", "node-metric-name", n.name + (n.ready ? "" : " (not ready)")));
    const bars = el("div", "node-metric-bars");
    bars.appendChild(metricBlock("CPU requested", n.cpuRequestedMillis || 0, n.cpuAllocatableMillis || 0, fmtMillis, "node-cpu-" + n.name));
    bars.appendChild(metricBlock("Memory used", n.memoryUsedBytes || 0, n.memoryAllocatableBytes || 0, fmtBytes, "node-mem-" + n.name));
    row.appendChild(bars);
    perNode.appendChild(row);
  }
  container.appendChild(perNode);
}

function phaseKind(phase) {
  if (phase === "POD_PHASE_RUNNING" || phase === "POD_PHASE_SUCCEEDED") return "ok";
  if (phase === "POD_PHASE_FAILED") return "bad";
  return "muted";
}

let logsRefreshTimer = null;
let logsCurrentPod = null;
let logsCurrentStream = "runtime";

async function refreshLogs() {
  if (!logsCurrentPod) return;
  const { namespace, name } = logsCurrentPod;
  const content = document.getElementById("logs-content");
  const streamParam = logsCurrentStream === "build" ? "&stream=build" : "";
  try {
    const resp = await fetch(`/api/logs?namespace=${encodeURIComponent(namespace)}&name=${encodeURIComponent(name)}&tail=300${streamParam}`, {
      cache: "no-store",
    });
    const text = await resp.text();
    content.textContent = resp.ok ? (text || "(empty)") : `Error: ${text}`;
    content.scrollTop = content.scrollHeight;
  } catch (err) {
    content.textContent = "Error: " + (err.message || String(err));
  }
}

function setLogsStream(stream) {
  logsCurrentStream = stream;
  document.querySelectorAll("#logs-modal .tab-toggle").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.stream === stream);
  });
  document.getElementById("logs-content").textContent = "loading…";
  refreshLogs();
}

function openLogsModal(namespace, name) {
  const modal = document.getElementById("logs-modal");
  logsCurrentPod = { namespace, name };
  document.getElementById("logs-modal-title").textContent = `Logs: ${namespace}/${name}`;
  modal.classList.add("open");
  setLogsStream("runtime");
  clearInterval(logsRefreshTimer);
  logsRefreshTimer = setInterval(refreshLogs, 3000);
}

function closeLogsModal() {
  document.getElementById("logs-modal").classList.remove("open");
  clearInterval(logsRefreshTimer);
  logsRefreshTimer = null;
  logsCurrentPod = null;
}

function initLogsModal() {
  const modal = document.getElementById("logs-modal");
  document.getElementById("close-logs-btn").addEventListener("click", closeLogsModal);
  modal.addEventListener("click", (ev) => {
    if (ev.target === modal) closeLogsModal();
  });
  document.querySelectorAll("#logs-modal .tab-toggle").forEach((btn) => {
    btn.addEventListener("click", () => setLogsStream(btn.dataset.stream));
  });
}

async function refreshPods() {
  const data = await fetchJSON("/api/pods");
  const items = data.items || [];
  document.getElementById("pods-count").textContent = items.length;
  setRows("pods-table", items, 6, (p) => {
    const tr = document.createElement("tr");
    const status = p.status || {};
    const spec = p.spec || {};
    const namespace = p.metadata?.namespace || "";
    const name = p.metadata?.name || "";
    tr.appendChild(el("td", null, namespace));
    tr.appendChild(el("td", null, name));
    const phaseTd = document.createElement("td");
    const phase = status.phase || "POD_PHASE_UNSPECIFIED";
    phaseTd.appendChild(badge(phase.replace("POD_PHASE_", "").toLowerCase(), phaseKind(phase)));
    tr.appendChild(phaseTd);
    tr.appendChild(el("td", null, spec.nodeName || "—"));
    tr.appendChild(el("td", null, String(status.restartCount || 0)));
    const actionsTd = document.createElement("td");
    const logsBtn = el("button", "btn-ghost btn-small", "Logs");
    logsBtn.addEventListener("click", () => openLogsModal(namespace, name));
    actionsTd.appendChild(logsBtn);
    tr.appendChild(actionsTd);
    return tr;
  });
}

async function deleteService(namespace, name) {
  if (!confirm(`Delete service ${namespace}/${name}?`)) return;
  await fetchJSON(`/api/services?namespace=${encodeURIComponent(namespace)}&name=${encodeURIComponent(name)}`, {
    method: "DELETE",
  });
  await refreshServices();
}

async function refreshServices() {
  const data = await fetchJSON("/api/services");
  const items = data.items || [];
  document.getElementById("services-count").textContent = items.length;
  setRows("services-table", items, 5, (s) => {
    const tr = document.createElement("tr");
    const spec = s.spec || {};
    const status = s.status || {};
    const namespace = s.metadata?.namespace || "";
    const name = s.metadata?.name || "";
    const seenNodePorts = new Set();
    const endpointsTd = document.createElement("td");
    const endpoints = status.endpoints || [];
    if (endpoints.length === 0) {
      endpointsTd.textContent = "—";
    } else {
      for (const e of endpoints) {
        if (seenNodePorts.has(e.nodeIp + ":" + e.nodePort)) continue;
        seenNodePorts.add(e.nodeIp + ":" + e.nodePort);
        const link = el("a", "endpoint-link", `${e.nodeIp}:${e.nodePort}`);
        link.href = `http://${e.nodeIp}:${e.nodePort}`;
        link.target = "_blank";
        link.rel = "noopener noreferrer";
        endpointsTd.appendChild(link);
        endpointsTd.appendChild(document.createTextNode(" "));
      }
    }
    tr.appendChild(el("td", null, namespace));
    tr.appendChild(el("td", null, name));
    tr.appendChild(el("td", null, String(spec.port || 0)));
    tr.appendChild(endpointsTd);
    const actionsTd = document.createElement("td");
    const deleteBtn = el("button", "btn-ghost btn-small", "Delete");
    deleteBtn.addEventListener("click", () => deleteService(namespace, name));
    actionsTd.appendChild(deleteBtn);
    tr.appendChild(actionsTd);
    return tr;
  });
}

async function refreshFinops() {
  const data = await fetchJSON("/api/finops");
  const container = document.getElementById("finops-summary");
  container.innerHTML = "";

  const totalCard = el("div", "card");
  totalCard.appendChild(el("div", "label", "total / hour so far"));
  totalCard.appendChild(el("div", "value", "$" + (data.total || 0).toFixed(4)));
  container.appendChild(totalCard);

  for (const [ns, cost] of Object.entries(data.byNamespace || {})) {
    const card = el("div", "card");
    card.appendChild(el("div", "label", ns));
    card.appendChild(el("div", "value", "$" + cost.toFixed(4)));
    container.appendChild(card);
  }
}

const lastKnownReplicas = {};

async function deleteDeployment(namespace, name) {
  if (!confirm(`Delete deployment ${namespace}/${name}? This also removes its pods.`)) return;
  await fetchJSON(`/api/deployments?namespace=${encodeURIComponent(namespace)}&name=${encodeURIComponent(name)}`, {
    method: "DELETE",
  });
  await Promise.allSettled([refreshDeployments(), refreshCICD()]);
}

async function scaleDeployment(namespace, name, replicas) {
  await fetchJSON("/api/deployments", {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ namespace, name, replicas }),
  });
  await Promise.allSettled([refreshDeployments(), refreshCICD()]);
}

async function refreshDeployments() {
  const data = await fetchJSON("/api/deployments");
  const items = data.items || [];
  document.getElementById("deployments-count").textContent = items.length;
  setRows("deployments-table", items, 6, (d) => {
    const tr = document.createElement("tr");
    const spec = d.spec || {};
    const status = d.status || {};
    const namespace = d.metadata?.namespace || "";
    const name = d.metadata?.name || "";
    const replicas = spec.replicas || 0;
    const key = namespace + "/" + name;
    if (replicas > 0) lastKnownReplicas[key] = replicas;
    const container0 = (spec.template?.containers || [])[0] || {};
    const image = container0.image || (container0.buildSource ? "git: " + container0.buildSource.repoUrl : "—");
    tr.appendChild(el("td", null, namespace));
    tr.appendChild(el("td", null, name));
    tr.appendChild(el("td", null, image));
    tr.appendChild(el("td", null, String(replicas)));
    tr.appendChild(el("td", null, String(status.readyReplicas || 0)));

    const actionsTd = document.createElement("td");
    actionsTd.className = "row-actions";
    const manageBtn = el("button", "btn-ghost btn-small", "Manage");
    manageBtn.addEventListener("click", () => openDeploymentPanel(d));
    actionsTd.appendChild(manageBtn);
    tr.appendChild(actionsTd);
    tr.classList.add("clickable-row");
    tr.addEventListener("click", (ev) => {
      if (ev.target === manageBtn) return;
      openDeploymentPanel(d);
    });
    return tr;
  });
}

let panelDeployment = null;
let panelLogsTimer = null;
let panelLogsStream = "runtime";

function addPanelEnvVarRow(key, value) {
  const rows = document.getElementById("panel-env-vars-rows");
  const row = el("div", "env-var-row");
  const keyInput = document.createElement("input");
  keyInput.placeholder = "KEY";
  keyInput.className = "env-key";
  keyInput.value = key || "";
  const valueInput = document.createElement("input");
  valueInput.placeholder = "value";
  valueInput.className = "env-value";
  valueInput.value = value || "";
  const removeBtn = el("button", "btn-ghost btn-small", "×");
  removeBtn.type = "button";
  removeBtn.addEventListener("click", () => row.remove());
  row.appendChild(keyInput);
  row.appendChild(valueInput);
  row.appendChild(removeBtn);
  rows.appendChild(row);
}

function collectPanelEnvVars() {
  const env = {};
  document.querySelectorAll("#panel-env-vars-rows .env-var-row").forEach((row) => {
    const k = row.querySelector(".env-key").value.trim();
    if (!k) return;
    env[k] = row.querySelector(".env-value").value;
  });
  return env;
}

function setPanelDeploymentSource(source) {
  document.querySelectorAll("#panel-tab-settings .tab-toggle").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.panelSource === source);
  });
  document.getElementById("panel-source-image-fields").classList.toggle("hidden", source !== "image");
  document.getElementById("panel-source-git-fields").classList.toggle("hidden", source !== "git");
}

function setPanelTab(tabName) {
  document.querySelectorAll(".side-tab").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.panelTab === tabName);
  });
  document.querySelectorAll(".panel-tab-content").forEach((el) => {
    el.classList.toggle("active", el.id === "panel-tab-" + tabName);
  });
  if (tabName === "logs") refreshPanelLogs();
}

function panelOverviewCard(label, value) {
  const card = el("div", "card");
  card.appendChild(el("div", "label", label));
  card.appendChild(el("div", "value", String(value)));
  return card;
}

function openDeploymentPanel(d) {
  const namespace = d.metadata?.namespace || "default";
  const name = d.metadata?.name || "";
  const spec = d.spec || {};
  const status = d.status || {};
  const container = (spec.template?.containers || [])[0] || {};
  const replicas = spec.replicas || 0;

  panelDeployment = { namespace, name };

  document.getElementById("panel-title").textContent = name;
  document.getElementById("panel-subtitle").textContent = namespace;

  const cards = document.getElementById("panel-overview-cards");
  cards.innerHTML = "";
  cards.appendChild(panelOverviewCard("Replicas", replicas));
  cards.appendChild(panelOverviewCard("Ready", status.readyReplicas || 0));
  cards.appendChild(panelOverviewCard("Source", container.buildSource ? "Git repo" : "Image"));
  cards.appendChild(panelOverviewCard("Image", container.image || (container.buildSource?.repoUrl ?? "—")));

  const toggleBtn = document.getElementById("panel-toggle-btn");
  toggleBtn.textContent = replicas > 0 ? "Stop" : "Start";
  toggleBtn.onclick = async () => {
    const target = replicas > 0 ? 0 : lastKnownReplicas[namespace + "/" + name] || 1;
    await scaleDeployment(namespace, name, target);
    const data = await fetchJSON("/api/deployments");
    const updated = (data.items || []).find(
      (item) => item.metadata?.namespace === namespace && item.metadata?.name === name
    );
    if (updated) openDeploymentPanel(updated);
  };
  document.getElementById("panel-delete-btn").onclick = async () => {
    await deleteDeployment(namespace, name);
    closeDeploymentPanel();
  };

  document.getElementById("panel-form-error").textContent = "";
  document.getElementById("panel-env-vars-rows").innerHTML = "";
  for (const [k, v] of Object.entries(container.env || {})) {
    addPanelEnvVarRow(k, v);
  }

  const form = document.getElementById("panel-form");
  form.elements["replicas"].value = replicas || 1;
  form.elements["port"].value = (container.containerPorts || [])[0] || "";
  form.elements["command"].value = (container.command || []).join(" ");
  if (container.buildSource) {
    setPanelDeploymentSource("git");
    form.elements["gitRepoUrl"].value = container.buildSource.repoUrl || "";
    form.elements["gitBranch"].value = container.buildSource.branch || "";
    form.elements["gitDockerfilePath"].value = container.buildSource.dockerfilePath || "";
    form.elements["gitContextPath"].value = container.buildSource.contextPath || "";
  } else {
    setPanelDeploymentSource("image");
    form.elements["image"].value = container.image || "";
  }

  setPanelTab("overview");
  document.getElementById("deployment-panel").classList.add("open");
  document.getElementById("panel-backdrop").classList.add("open");
}

function closeDeploymentPanel() {
  document.getElementById("deployment-panel").classList.remove("open");
  document.getElementById("panel-backdrop").classList.remove("open");
  clearInterval(panelLogsTimer);
  panelLogsTimer = null;
  panelDeployment = null;
}

async function refreshPanelLogs() {
  if (!panelDeployment) return;
  const content = document.getElementById("panel-logs-content");
  const streamParam = panelLogsStream === "build" ? "&stream=build" : "";
  const podName = panelDeployment.name + "-0";
  try {
    const resp = await fetch(
      `/api/logs?namespace=${encodeURIComponent(panelDeployment.namespace)}&name=${encodeURIComponent(podName)}&tail=300${streamParam}`,
      { cache: "no-store" }
    );
    const text = await resp.text();
    content.textContent = resp.ok ? (text || "(empty)") : `Error: ${text}`;
    content.scrollTop = content.scrollHeight;
  } catch (err) {
    content.textContent = "Error: " + (err.message || String(err));
  }
}

function initDeploymentPanel() {
  document.getElementById("close-panel-btn").addEventListener("click", closeDeploymentPanel);
  document.getElementById("panel-backdrop").addEventListener("click", closeDeploymentPanel);
  document.getElementById("panel-add-env-var-btn").addEventListener("click", () => addPanelEnvVarRow());

  document.querySelectorAll(".side-tab").forEach((btn) => {
    btn.addEventListener("click", () => setPanelTab(btn.dataset.panelTab));
  });
  document.querySelectorAll("#panel-tab-settings .tab-toggle").forEach((btn) => {
    btn.addEventListener("click", () => setPanelDeploymentSource(btn.dataset.panelSource));
  });
  document.querySelectorAll("#panel-tab-logs .tab-toggle").forEach((btn) => {
    btn.addEventListener("click", () => {
      panelLogsStream = btn.dataset.panelStream;
      document.querySelectorAll("#panel-tab-logs .tab-toggle").forEach((b) => b.classList.toggle("active", b === btn));
      document.getElementById("panel-logs-content").textContent = "loading…";
      refreshPanelLogs();
    });
  });

  clearInterval(panelLogsTimer);
  panelLogsTimer = setInterval(() => {
    if (panelDeployment) refreshPanelLogs();
  }, 3000);

  const form = document.getElementById("panel-form");
  const errorEl = document.getElementById("panel-form-error");
  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    if (!panelDeployment) return;
    errorEl.textContent = "";
    const fd = new FormData(form);
    const command = String(fd.get("command") || "").trim();
    const usingGit = !document.getElementById("panel-source-git-fields").classList.contains("hidden");

    const payload = {
      name: panelDeployment.name,
      namespace: panelDeployment.namespace,
      replicas: parseInt(fd.get("replicas"), 10) || 1,
      port: parseInt(fd.get("port"), 10) || 0,
      command: command ? command.split(/\s+/) : [],
      env: collectPanelEnvVars(),
    };
    if (usingGit) {
      payload.gitRepoUrl = String(fd.get("gitRepoUrl") || "").trim();
      payload.gitBranch = String(fd.get("gitBranch") || "").trim();
      payload.gitDockerfilePath = String(fd.get("gitDockerfilePath") || "").trim();
      payload.gitContextPath = String(fd.get("gitContextPath") || "").trim();
      if (!payload.gitRepoUrl) {
        errorEl.textContent = "Repository URL is required";
        return;
      }
    } else {
      payload.image = String(fd.get("image") || "").trim();
      if (!payload.image) {
        errorEl.textContent = "Image is required";
        return;
      }
    }

    const saveBtn = document.getElementById("panel-save-btn");
    const originalLabel = saveBtn.textContent;
    saveBtn.disabled = true;
    saveBtn.textContent = usingGit ? "Building…" : "Saving…";

    try {
      await fetchJSON("/api/deployments", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      await Promise.allSettled([refreshDeployments(), refreshCICD(), refreshServices()]);
      showToast(`${payload.name}: updated.`);
    } catch (err) {
      errorEl.textContent = err.message || String(err);
    } finally {
      saveBtn.disabled = false;
      saveBtn.textContent = originalLabel;
    }
  });
}

function addEnvVarRow(key, value) {
  const rows = document.getElementById("env-vars-rows");
  const row = el("div", "env-var-row");
  const keyInput = document.createElement("input");
  keyInput.placeholder = "KEY";
  keyInput.className = "env-key";
  keyInput.value = key || "";
  const valueInput = document.createElement("input");
  valueInput.placeholder = "value";
  valueInput.className = "env-value";
  valueInput.value = value || "";
  const removeBtn = el("button", "btn-ghost btn-small", "×");
  removeBtn.type = "button";
  removeBtn.addEventListener("click", () => row.remove());
  row.appendChild(keyInput);
  row.appendChild(valueInput);
  row.appendChild(removeBtn);
  rows.appendChild(row);
}

function collectEnvVars() {
  const env = {};
  document.querySelectorAll("#env-vars-rows .env-var-row").forEach((row) => {
    const k = row.querySelector(".env-key").value.trim();
    if (!k) return;
    env[k] = row.querySelector(".env-value").value;
  });
  return env;
}

function setDeploymentSource(source) {
  document.querySelectorAll("#new-deployment-modal .tab-toggle").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.source === source);
  });
  document.getElementById("source-image-fields").classList.toggle("hidden", source !== "image");
  document.getElementById("source-git-fields").classList.toggle("hidden", source !== "git");
}

function openDeploymentModal() {
  const modal = document.getElementById("new-deployment-modal");
  const form = document.getElementById("new-deployment-form");
  const errorEl = document.getElementById("deployment-form-error");

  errorEl.textContent = "";
  form.reset();
  document.getElementById("env-vars-rows").innerHTML = "";
  setDeploymentSource("image");
  form.elements["name"].disabled = false;
  form.elements["namespace"].disabled = false;

  modal.classList.add("open");
}

function initDeploymentForm() {
  const modal = document.getElementById("new-deployment-modal");
  const form = document.getElementById("new-deployment-form");
  const errorEl = document.getElementById("deployment-form-error");

  document.getElementById("new-deployment-btn").addEventListener("click", () => openDeploymentModal());
  document.getElementById("add-env-var-btn").addEventListener("click", () => addEnvVarRow());
  document.getElementById("cancel-deployment-btn").addEventListener("click", () => {
    modal.classList.remove("open");
  });
  modal.addEventListener("click", (ev) => {
    if (ev.target === modal) modal.classList.remove("open");
  });
  document.querySelectorAll("#new-deployment-modal .tab-toggle").forEach((btn) => {
    btn.addEventListener("click", () => setDeploymentSource(btn.dataset.source));
  });

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    errorEl.textContent = "";
    const fd = new FormData(form);
    const command = String(fd.get("command") || "").trim();
    const usingGit = !document.getElementById("source-git-fields").classList.contains("hidden");

    const payload = {
      name: String(fd.get("name") || "").trim(),
      namespace: String(fd.get("namespace") || "").trim() || "default",
      replicas: parseInt(fd.get("replicas"), 10) || 1,
      port: parseInt(fd.get("port"), 10) || 0,
      command: command ? command.split(/\s+/) : [],
      env: collectEnvVars(),
    };
    if (usingGit) {
      payload.gitRepoUrl = String(fd.get("gitRepoUrl") || "").trim();
      payload.gitBranch = String(fd.get("gitBranch") || "").trim();
      payload.gitDockerfilePath = String(fd.get("gitDockerfilePath") || "").trim();
      payload.gitContextPath = String(fd.get("gitContextPath") || "").trim();
      if (!payload.gitRepoUrl) {
        errorEl.textContent = "Repository URL is required";
        return;
      }
    } else {
      payload.image = String(fd.get("image") || "").trim();
      if (!payload.image) {
        errorEl.textContent = "Image is required";
        return;
      }
    }

    const submitBtn = document.getElementById("deployment-submit-btn");
    const originalLabel = submitBtn.textContent;
    submitBtn.disabled = true;
    submitBtn.textContent = usingGit ? "Building…" : "Creating…";

    try {
      const result = await fetchJSON("/api/deployments", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      modal.classList.remove("open");
      await Promise.allSettled([refreshDeployments(), refreshCICD(), refreshServices()]);

      if (result.service) {
        const nodePort = result.service.spec?.nodePort;
        showToast(`${payload.name}: exposed on NodePort ${nodePort} (see Services panel for node IPs).`);
      } else if (result.serviceError) {
        showToast(`${payload.name}: created, but could not expose port ${payload.port}: ${result.serviceError}`, true);
      } else if (usingGit) {
        showToast(`${payload.name}: built from ${payload.gitRepoUrl} and deployed.`);
      }
    } catch (err) {
      errorEl.textContent = err.message || String(err);
    } finally {
      submitBtn.disabled = false;
      submitBtn.textContent = originalLabel;
    }
  });
}

function showToast(message, isError) {
  const toast = document.getElementById("toast");
  toast.textContent = message;
  toast.className = "toast show" + (isError ? " error" : "");
  clearTimeout(showToast._timer);
  showToast._timer = setTimeout(() => {
    toast.className = "toast";
  }, 6000);
}

function renderGitOpsPanel(gitops) {
  const container = document.getElementById("gitops-panel");
  container.innerHTML = "";

  if (!gitops.configured) {
    container.appendChild(el("p", "empty", "No GitOps source configured (-gitops-repo-url)."));
    return;
  }

  const grid = el("div", "gitops-grid");
  const item = (label, value, isError) => {
    const box = el("div", "gitops-item");
    box.appendChild(el("div", "label", label));
    box.appendChild(el("div", "value" + (isError ? " error" : ""), value || "—"));
    return box;
  };
  grid.appendChild(item("Repository", gitops.repoUrl));
  grid.appendChild(item("Branch", gitops.branch));
  grid.appendChild(item("Path", gitops.path));
  grid.appendChild(item("Last sync", fmtUnixTime(gitops.lastSyncUnix)));
  grid.appendChild(item("Last commit", gitops.lastCommit ? gitops.lastCommit.slice(0, 12) : ""));
  grid.appendChild(item("Last error", gitops.lastError || "none", !!gitops.lastError));
  container.appendChild(grid);
}

async function refreshCICD() {
  const data = await fetchJSON("/api/cicd");
  renderGitOpsPanel(data.gitops || {});

  setRows("cicd-table", data.deployments || [], 5, (d) => {
    const tr = document.createElement("tr");
    tr.appendChild(el("td", null, d.namespace || ""));
    tr.appendChild(el("td", null, d.name || ""));
    tr.appendChild(el("td", null, d.image || "—"));
    const sourceTd = document.createElement("td");
    sourceTd.appendChild(badge(d.source, d.source === "gitops" ? "gitops" : "manual"));
    tr.appendChild(sourceTd);
    tr.appendChild(el("td", null, `${d.readyReplicas || 0} / ${d.replicas || 0}`));
    return tr;
  });
}

let canvasScale = 1;
let canvasOffsetX = 0;
let canvasOffsetY = 0;

function applyCanvasTransform() {
  const surface = document.getElementById("canvas-surface");
  surface.style.transform = `translate(${canvasOffsetX}px, ${canvasOffsetY}px) scale(${canvasScale})`;
}

function selectorMatches(selector, labels) {
  if (!selector || Object.keys(selector).length === 0) return false;
  labels = labels || {};
  return Object.entries(selector).every(([k, v]) => labels[k] === v);
}

function deploymentStatusClass(replicas, ready) {
  if (replicas <= 0) return "";
  if (ready >= replicas) return "ok";
  return "pending";
}

async function refreshCanvas() {
  const [depData, svcData, podData] = await Promise.all([
    fetchJSON("/api/deployments"),
    fetchJSON("/api/services"),
    fetchJSON("/api/pods"),
  ]);
  const deployments = depData.items || [];
  const services = svcData.items || [];
  const pods = podData.items || [];

  const groups = {};
  const groupOf = (ns) => groups[ns] || (groups[ns] = { deployments: [], pods: [] });

  for (const d of deployments) {
    groupOf(d.metadata?.namespace || "default").deployments.push(d);
  }
  for (const p of pods) {
    if (p.metadata?.labels?.["nimbuscore.io/owner-deployment"]) continue;
    groupOf(p.metadata?.namespace || "default").pods.push(p);
  }

  const surface = document.getElementById("canvas-surface");
  surface.innerHTML = "";

  const namespaces = Object.keys(groups).sort();
  if (namespaces.length === 0) {
    surface.appendChild(el("div", "canvas-empty", "No deployments yet — click + Create to add one."));
    applyCanvasTransform();
    return;
  }

  for (const ns of namespaces) {
    const group = groups[ns];
    const groupEl = el("div", "canvas-group");
    groupEl.appendChild(el("div", "canvas-group-title", ns));
    const cardsEl = el("div", "canvas-group-cards");

    for (const d of group.deployments) {
      const spec = d.spec || {};
      const status = d.status || {};
      const container = (spec.template?.containers || [])[0] || {};
      const replicas = spec.replicas || 0;
      const ready = status.readyReplicas || 0;
      const podLabels = spec.selector || {};

      const card = el("div", "canvas-card");
      const head = el("div", "canvas-card-head");
      head.appendChild(el("span", "canvas-status-dot " + deploymentStatusClass(replicas, ready)));
      head.appendChild(el("span", "canvas-card-name", d.metadata?.name || ""));
      card.appendChild(head);
      const image = container.image || (container.buildSource ? "git: " + container.buildSource.repoUrl : "—");
      card.appendChild(el("div", "canvas-card-meta", image));
      card.appendChild(el("div", "canvas-card-meta", `${ready} / ${replicas} ready`));

      const matchingService = services.find(
        (s) => (s.metadata?.namespace || "default") === ns && selectorMatches(s.spec?.selector, podLabels)
      );
      if (matchingService) {
        const endpoints = matchingService.status?.endpoints || [];
        const first = endpoints[0];
        const text = first ? `${first.nodeIp}:${first.nodePort}` : `nodePort ${matchingService.spec?.nodePort ?? "?"}`;
        card.appendChild(el("div", "canvas-card-service", "→ " + text));
      }

      card.addEventListener("click", () => openDeploymentPanel(d));
      cardsEl.appendChild(card);
    }

    for (const p of group.pods) {
      const status = p.status || {};
      const card = el("div", "canvas-card");
      const head = el("div", "canvas-card-head");
      head.appendChild(el("span", "canvas-status-dot " + phaseKind(status.phase)));
      head.appendChild(el("span", "canvas-card-name", p.metadata?.name || ""));
      card.appendChild(head);
      card.appendChild(el("div", "canvas-card-meta", "standalone pod"));
      card.appendChild(el("div", "canvas-card-meta", (status.phase || "").replace("POD_PHASE_", "")));
      cardsEl.appendChild(card);
    }

    groupEl.appendChild(cardsEl);
    surface.appendChild(groupEl);
  }

  applyCanvasTransform();
}

function initCanvas() {
  const viewport = document.getElementById("canvas-viewport");

  document.getElementById("canvas-zoom-in").addEventListener("click", () => {
    canvasScale = Math.min(2, canvasScale + 0.15);
    applyCanvasTransform();
  });
  document.getElementById("canvas-zoom-out").addEventListener("click", () => {
    canvasScale = Math.max(0.4, canvasScale - 0.15);
    applyCanvasTransform();
  });
  document.getElementById("canvas-zoom-fit").addEventListener("click", () => {
    canvasScale = 1;
    canvasOffsetX = 0;
    canvasOffsetY = 0;
    applyCanvasTransform();
  });
  document.getElementById("new-deployment-btn-canvas").addEventListener("click", () => openDeploymentModal());

  let dragging = false;
  let lastX = 0;
  let lastY = 0;
  viewport.addEventListener("mousedown", (ev) => {
    if (ev.target.closest(".canvas-card")) return;
    dragging = true;
    lastX = ev.clientX;
    lastY = ev.clientY;
    viewport.classList.add("dragging");
  });
  window.addEventListener("mousemove", (ev) => {
    if (!dragging) return;
    canvasOffsetX += ev.clientX - lastX;
    canvasOffsetY += ev.clientY - lastY;
    lastX = ev.clientX;
    lastY = ev.clientY;
    applyCanvasTransform();
  });
  window.addEventListener("mouseup", () => {
    dragging = false;
    viewport.classList.remove("dragging");
  });
  viewport.addEventListener(
    "wheel",
    (ev) => {
      ev.preventDefault();
      const delta = ev.deltaY > 0 ? -0.1 : 0.1;
      canvasScale = Math.min(2, Math.max(0.4, canvasScale + delta));
      applyCanvasTransform();
    },
    { passive: false }
  );
}

function updateClock() {
  document.getElementById("clock").textContent = new Date().toLocaleString();
}

async function refreshAll() {
  updateClock();
  await Promise.allSettled([
    refreshNodes(),
    refreshPods(),
    refreshServices(),
    refreshFinops(),
    refreshDeployments(),
    refreshCICD(),
    refreshMetrics(),
    refreshCanvas(),
  ]);
}

initTabs();
initDeploymentForm();
initLogsModal();
initDeploymentPanel();
initCanvas();
refreshAll();
setInterval(refreshAll, 5000);
setInterval(updateClock, 1000);

function formatCommandForInput(args) {
  return (args || [])
    .map((a) => (/\s/.test(a) ? '"' + a.replace(/\\/g, "\\\\").replace(/"/g, '\\"') + '"' : a))
    .join(" ");
}

function parseCommandFromInput(str) {
  const args = [];
  let current = "";
  let hasCurrent = false;
  let quote = null;
  let escaped = false;
  for (const c of str) {
    if (escaped) {
      current += c;
      hasCurrent = true;
      escaped = false;
      continue;
    }
    if (c === "\\") {
      escaped = true;
      continue;
    }
    if (quote) {
      if (c === quote) {
        quote = null;
      } else {
        current += c;
      }
      continue;
    }
    if (c === '"' || c === "'") {
      quote = c;
      hasCurrent = true;
      continue;
    }
    if (/\s/.test(c)) {
      if (hasCurrent) {
        args.push(current);
        current = "";
        hasCurrent = false;
      }
      continue;
    }
    current += c;
    hasCurrent = true;
  }
  if (hasCurrent) args.push(current);
  return args;
}

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
  if (resp.status === 401) {
    window.location.href = "/login";
    return new Promise(() => {}); // stop the caller; navigation is already underway
  }
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
  const items = (data.items || []).filter((d) => (d.metadata?.namespace || "default") === currentNamespace);
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
  if (tabName === "files") refreshPanelFiles();
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
  const volumeMount = (spec.template?.volumes || [])[0] || null;

  panelDeployment = { namespace, name, volumeName: volumeMount?.volumeName || null };

  const filesTabBtn = document.getElementById("panel-files-tab-btn");
  filesTabBtn.classList.toggle("hidden", !volumeMount);
  if (!volumeMount && filesTabBtn.classList.contains("active")) setPanelTab("overview");

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
  form.elements["command"].value = formatCommandForInput(container.command);
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

  refreshLinkableServices(name).then(() => populateLinkSelect("panel-link-service-select"));
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

let panelCurrentFile = null;

function filesQueryBase() {
  return `namespace=${encodeURIComponent(panelDeployment.namespace)}&name=${encodeURIComponent(panelDeployment.volumeName)}`;
}

async function refreshPanelFiles() {
  if (!panelDeployment || !panelDeployment.volumeName) return;
  const list = document.getElementById("panel-files-list");
  try {
    const resp = await fetch(`/api/files?${filesQueryBase()}`, { cache: "no-store" });
    if (!resp.ok) {
      list.innerHTML = "";
      list.appendChild(el("div", "empty", "Error loading files"));
      return;
    }
    const entries = await resp.json();
    list.innerHTML = "";
    if (!entries || entries.length === 0) {
      await writePanelFile("index.html", "<!doctype html>\n<html>\n  <head><title>Hello from NimbusCore</title></head>\n  <body>\n    <h1>It works!</h1>\n    <p>Edit this file from the Files tab to change what's served.</p>\n  </body>\n</html>\n");
      return refreshPanelFiles();
    }
    for (const entry of entries) {
      if (entry.isDir) continue;
      const btn = el("button", "file-item", entry.name);
      btn.type = "button";
      btn.classList.toggle("active", panelCurrentFile === entry.name);
      btn.addEventListener("click", () => openPanelFile(entry.name));
      list.appendChild(btn);
    }
  } catch (err) {
    list.innerHTML = "";
    list.appendChild(el("div", "empty", "Error: " + (err.message || String(err))));
  }
}

async function openPanelFile(path) {
  panelCurrentFile = path;
  document.querySelectorAll("#panel-files-list .file-item").forEach((btn) => {
    btn.classList.toggle("active", btn.textContent === path);
  });
  const editor = document.getElementById("panel-files-editor");
  const pathLabel = document.getElementById("panel-files-current-path");
  pathLabel.textContent = path;
  editor.disabled = true;
  editor.value = "loading…";
  try {
    const resp = await fetch(`/api/files?op=read&${filesQueryBase()}&path=${encodeURIComponent(path)}`, { cache: "no-store" });
    editor.value = resp.ok ? await resp.text() : "";
    editor.disabled = false;
    document.getElementById("panel-file-save-btn").disabled = false;
    document.getElementById("panel-file-delete-btn").disabled = false;
  } catch (err) {
    editor.value = "Error: " + (err.message || String(err));
  }
}

async function writePanelFile(path, content) {
  await fetch(`/api/files?${filesQueryBase()}&path=${encodeURIComponent(path)}`, {
    method: "PUT",
    body: content,
  });
}

async function savePanelFile() {
  if (!panelCurrentFile) return;
  const editor = document.getElementById("panel-files-editor");
  try {
    await writePanelFile(panelCurrentFile, editor.value);
    showToast(`Saved ${panelCurrentFile}`);
  } catch (err) {
    showToast("Error saving file: " + (err.message || String(err)), true);
  }
}

async function deletePanelFile() {
  if (!panelCurrentFile) return;
  if (!confirm(`Delete ${panelCurrentFile}?`)) return;
  try {
    await fetch(`/api/files?${filesQueryBase()}&path=${encodeURIComponent(panelCurrentFile)}`, { method: "DELETE" });
    panelCurrentFile = null;
    document.getElementById("panel-files-editor").value = "";
    document.getElementById("panel-files-editor").disabled = true;
    document.getElementById("panel-files-current-path").textContent = "Select a file";
    document.getElementById("panel-file-save-btn").disabled = true;
    document.getElementById("panel-file-delete-btn").disabled = true;
    await refreshPanelFiles();
  } catch (err) {
    showToast("Error deleting file: " + (err.message || String(err)), true);
  }
}

function initDeploymentPanel() {
  document.getElementById("close-panel-btn").addEventListener("click", closeDeploymentPanel);
  document.getElementById("panel-backdrop").addEventListener("click", closeDeploymentPanel);
  document.getElementById("panel-add-env-var-btn").addEventListener("click", () => addPanelEnvVarRow());
  document.getElementById("panel-file-save-btn").addEventListener("click", savePanelFile);
  document.getElementById("panel-file-delete-btn").addEventListener("click", deletePanelFile);
  document.getElementById("panel-new-file-btn").addEventListener("click", async () => {
    const name = prompt("New file name (e.g. about.html):");
    if (!name) return;
    await writePanelFile(name, "");
    await refreshPanelFiles();
    openPanelFile(name);
  });
  document.getElementById("panel-upload-file-btn").addEventListener("click", () => {
    document.getElementById("panel-upload-file-input").click();
  });
  document.getElementById("panel-upload-file-input").addEventListener("change", async (ev) => {
    const file = ev.target.files[0];
    if (!file) return;
    const content = await file.text();
    await writePanelFile(file.name, content);
    ev.target.value = "";
    await refreshPanelFiles();
    openPanelFile(file.name);
  });
  document.getElementById("panel-link-service-btn").addEventListener("click", () => {
    const name = document.getElementById("panel-link-service-select").value;
    if (!name) return;
    const target = linkableServices.find((s) => s.name === name);
    if (!target) return;
    const env = computeLinkEnvVars(target);
    for (const [k, v] of Object.entries(env)) addPanelEnvVarRow(k, v);
    showToast(`Linked ${name}: added ${Object.keys(env).length} environment variables.`);
  });

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
      command: command ? parseCommandFromInput(command) : [],
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

async function openDeploymentModal() {
  const modal = document.getElementById("new-deployment-modal");
  const form = document.getElementById("new-deployment-form");
  const errorEl = document.getElementById("deployment-form-error");

  errorEl.textContent = "";
  form.reset();
  document.getElementById("env-vars-rows").innerHTML = "";
  document.getElementById("mount-path-field").classList.add("hidden");
  setDeploymentSource("image");
  form.elements["name"].disabled = false;
  form.elements["namespace"].value = currentNamespace;
  form.elements["namespace"].readOnly = true;

  modal.classList.add("open");

  await refreshLinkableServices();
  populateLinkSelect("link-service-select");
}

function initDeploymentForm() {
  const modal = document.getElementById("new-deployment-modal");
  const form = document.getElementById("new-deployment-form");
  const errorEl = document.getElementById("deployment-form-error");

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
  document.getElementById("persistent-storage-checkbox").addEventListener("change", (ev) => {
    document.getElementById("mount-path-field").classList.toggle("hidden", !ev.target.checked);
  });
  document.getElementById("link-service-btn").addEventListener("click", () => {
    const name = document.getElementById("link-service-select").value;
    if (!name) return;
    const target = linkableServices.find((s) => s.name === name);
    if (!target) return;
    const env = computeLinkEnvVars(target);
    for (const [k, v] of Object.entries(env)) addEnvVarRow(k, v);
    showToast(`Linked ${name}: added ${Object.keys(env).length} environment variables.`);
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
      command: command ? parseCommandFromInput(command) : [],
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
      if (fd.get("addPersistentStorage")) {
        payload.addPersistentStorage = true;
        payload.mountPath = String(fd.get("mountPath") || "").trim();
        if (!payload.mountPath) {
          errorEl.textContent = "Mount path is required when persistent storage is enabled";
          return;
        }
        if (payload.replicas > 1) {
          errorEl.textContent = "Deployments with persistent storage must have replicas = 1";
          return;
        }
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

let linkableServices = [];

async function refreshLinkableServices(excludeName) {
  const [depData, svcData] = await Promise.all([fetchJSON("/api/deployments"), fetchJSON("/api/services")]);
  const deployments = (depData.items || []).filter(
    (d) => (d.metadata?.namespace || "default") === currentNamespace && d.metadata?.name !== excludeName
  );
  const services = (svcData.items || []).filter((s) => (s.metadata?.namespace || "default") === currentNamespace);

  linkableServices = [];
  for (const d of deployments) {
    const podLabels = d.spec?.selector || {};
    const svc = services.find((s) => selectorMatches(s.spec?.selector, podLabels));
    if (!svc || !svc.spec?.nodePort) continue;
    linkableServices.push({
      name: d.metadata.name,
      nodePort: svc.spec.nodePort,
      container: (d.spec?.template?.containers || [])[0] || {},
    });
  }
}

function populateLinkSelect(selectId) {
  const select = document.getElementById(selectId);
  select.innerHTML = "";
  const placeholder = document.createElement("option");
  placeholder.value = "";
  placeholder.textContent = linkableServices.length ? "Select a service in this project…" : "No linkable services with an exposed port";
  select.appendChild(placeholder);
  for (const svc of linkableServices) {
    const opt = document.createElement("option");
    opt.value = svc.name;
    opt.textContent = `${svc.name} (nodePort ${svc.nodePort})`;
    select.appendChild(opt);
  }
}

function computeLinkEnvVars(target) {
  const image = (target.container.image || "").toLowerCase();
  const env = target.container.env || {};
  const host = "127.0.0.1";
  const port = String(target.nodePort);

  if (image.includes("postgres")) {
    const user = env.POSTGRES_USER || "postgres";
    const pass = env.POSTGRES_PASSWORD || "";
    const db = env.POSTGRES_DB || "postgres";
    return {
      PGHOST: host,
      PGPORT: port,
      PGUSER: user,
      PGPASSWORD: pass,
      PGDATABASE: db,
      DATABASE_URL: `postgres://${user}:${pass}@${host}:${port}/${db}`,
    };
  }
  if (image.includes("redis")) {
    return { REDIS_HOST: host, REDIS_PORT: port, REDIS_URL: `redis://${host}:${port}` };
  }
  if (image.includes("mysql")) {
    const pass = env.MYSQL_ROOT_PASSWORD || "";
    const db = env.MYSQL_DATABASE || "mysql";
    return {
      MYSQL_HOST: host,
      MYSQL_PORT: port,
      MYSQL_PASSWORD: pass,
      DATABASE_URL: `mysql://root:${pass}@${host}:${port}/${db}`,
    };
  }
  if (image.includes("mongo")) {
    return { MONGO_HOST: host, MONGO_PORT: port, MONGO_URL: `mongodb://${host}:${port}` };
  }
  const prefix = target.name.toUpperCase().replace(/[^A-Z0-9]/g, "_");
  return { [prefix + "_HOST"]: host, [prefix + "_PORT"]: port };
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
  const ns = currentNamespace;
  const deployments = (depData.items || []).filter((d) => (d.metadata?.namespace || "default") === ns);
  const services = (svcData.items || []).filter((s) => (s.metadata?.namespace || "default") === ns);
  const pods = (podData.items || []).filter(
    (p) => (p.metadata?.namespace || "default") === ns && !p.metadata?.labels?.["nimbuscore.io/owner-deployment"]
  );

  const surface = document.getElementById("canvas-surface");
  surface.innerHTML = "";

  if (deployments.length === 0 && pods.length === 0) {
    surface.appendChild(el("div", "canvas-empty", `No services in "${ns}" yet — click + Create to add one.`));
    applyCanvasTransform();
    return;
  }

  const groupEl = el("div", "canvas-group");
  groupEl.appendChild(el("div", "canvas-group-title", ns));
  const cardsEl = el("div", "canvas-group-cards");

  for (const d of deployments) {
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

    const matchingService = services.find((s) => selectorMatches(s.spec?.selector, podLabels));
    if (matchingService) {
      const endpoints = matchingService.status?.endpoints || [];
      const first = endpoints[0];
      const text = first ? `${first.nodeIp}:${first.nodePort}` : `nodePort ${matchingService.spec?.nodePort ?? "?"}`;
      card.appendChild(el("div", "canvas-card-service", "→ " + text));
    }

    card.addEventListener("click", () => openDeploymentPanel(d));
    cardsEl.appendChild(card);
  }

  for (const p of pods) {
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

let currentNamespace = localStorage.getItem("nimbus-current-namespace") || "default";

async function refreshNamespaces() {
  const [nsData, depData, podData] = await Promise.all([
    fetchJSON("/api/namespaces"),
    fetchJSON("/api/deployments"),
    fetchJSON("/api/pods"),
  ]);
  const known = new Set((nsData.items || []).map((n) => n.metadata?.name).filter(Boolean));
  for (const d of depData.items || []) known.add(d.metadata?.namespace || "default");
  for (const p of podData.items || []) known.add(p.metadata?.namespace || "default");
  known.add("default");

  const names = Array.from(known).sort();
  if (!names.includes(currentNamespace)) currentNamespace = names[0];
  localStorage.setItem("nimbus-current-namespace", currentNamespace);

  const select = document.getElementById("project-select");
  select.innerHTML = "";
  for (const name of names) {
    const opt = document.createElement("option");
    opt.value = name;
    opt.textContent = name;
    opt.selected = name === currentNamespace;
    select.appendChild(opt);
  }
}

async function switchProject(name) {
  currentNamespace = name;
  localStorage.setItem("nimbus-current-namespace", name);
  await Promise.allSettled([refreshCanvas(), refreshDeployments(), refreshCICD()]);
}

function initProjectSwitcher() {
  document.getElementById("project-select").addEventListener("change", (ev) => switchProject(ev.target.value));
  document.getElementById("new-project-btn").addEventListener("click", () => {
    const modal = document.getElementById("new-project-modal");
    document.getElementById("project-form-error").textContent = "";
    document.getElementById("new-project-form").reset();
    modal.classList.add("open");
  });
  document.getElementById("cancel-project-btn").addEventListener("click", () => {
    document.getElementById("new-project-modal").classList.remove("open");
  });
  document.getElementById("new-project-modal").addEventListener("click", (ev) => {
    if (ev.target.id === "new-project-modal") ev.target.classList.remove("open");
  });
  document.getElementById("new-project-form").addEventListener("submit", async (ev) => {
    ev.preventDefault();
    const errorEl = document.getElementById("project-form-error");
    errorEl.textContent = "";
    const name = new FormData(ev.target).get("name")?.toString().trim();
    if (!name) {
      errorEl.textContent = "Name is required";
      return;
    }
    try {
      await fetchJSON("/api/namespaces", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      document.getElementById("new-project-modal").classList.remove("open");
      await refreshNamespaces();
      await switchProject(name);
      showToast(`Project "${name}" created.`);
    } catch (err) {
      errorEl.textContent = err.message || String(err);
    }
  });
}

function setDeploymentsView(view) {
  document.querySelectorAll("#tab-deployments .view-toggle .tab-toggle").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.view === view);
  });
  document.getElementById("canvas-viewport").classList.toggle("hidden", view !== "canvas");
  document.querySelector("#tab-deployments .canvas-zoom-controls").classList.toggle("hidden", view !== "canvas");
  document.getElementById("deployments-table-view").classList.toggle("hidden", view !== "table");
}

function initViewToggle() {
  document.querySelectorAll("#tab-deployments .view-toggle .tab-toggle").forEach((btn) => {
    btn.addEventListener("click", () => setDeploymentsView(btn.dataset.view));
  });
}

const ICONS = {
  docker:
    '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M13.983 11.078h2.119a.186.186 0 00.186-.185V9.006a.186.186 0 00-.186-.186h-2.119a.185.185 0 00-.185.185v1.888c0 .102.083.185.185.185m-2.954-5.43h2.118a.186.186 0 00.186-.186V3.574a.186.186 0 00-.186-.185h-2.118a.185.185 0 00-.185.185v1.888c0 .102.082.185.185.185m0 2.716h2.118a.187.187 0 00.186-.186V6.29a.186.186 0 00-.186-.185h-2.118a.185.185 0 00-.185.185v1.887c0 .102.082.185.185.186m-2.93 0h2.12a.186.186 0 00.184-.186V6.29a.185.185 0 00-.185-.185H8.1a.185.185 0 00-.185.185v1.887c0 .102.083.185.185.186m-2.964 0h2.119a.186.186 0 00.185-.186V6.29a.185.185 0 00-.185-.185H5.136a.186.186 0 00-.186.185v1.887c0 .102.084.185.186.186m5.893 2.715h2.118a.186.186 0 00.186-.185V9.006a.186.186 0 00-.186-.186h-2.118a.185.185 0 00-.185.185v1.888c0 .102.082.185.185.185m-2.93 0h2.12a.185.185 0 00.184-.185V9.006a.185.185 0 00-.184-.186h-2.12a.185.185 0 00-.184.185v1.888c0 .102.083.185.185.185m-2.964 0h2.119a.185.185 0 00.185-.185V9.006a.185.185 0 00-.184-.186h-2.12a.186.186 0 00-.186.186v1.887c0 .102.084.185.186.185m-2.92 0h2.12a.185.185 0 00.184-.185V9.006a.185.185 0 00-.184-.186h-2.12a.185.185 0 00-.184.185v1.888c0 .102.082.185.185.185M23.763 9.89c-.065-.051-.672-.51-1.954-.51-.338.001-.676.03-1.01.087-.248-1.7-1.653-2.53-1.716-2.566l-.344-.199-.226.327c-.284.438-.49.922-.612 1.43-.23.97-.09 1.882.403 2.661-.595.332-1.55.413-1.744.42H.751a.751.751 0 00-.75.748 11.376 11.376 0 00.692 4.062c.545 1.428 1.355 2.48 2.41 3.124 1.18.723 3.1 1.137 5.275 1.137.983.003 1.963-.086 2.93-.266a12.248 12.248 0 003.823-1.389c.98-.567 1.86-1.288 2.61-2.136 1.252-1.418 1.998-2.997 2.553-4.4h.221c1.372 0 2.215-.549 2.68-1.009.309-.293.55-.65.707-1.046l.098-.288Z"/></svg>',
  github:
    '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12"/></svg>',
  postgres:
    "<svg viewBox=\"0 0 24 24\" fill=\"currentColor\"><path d=\"M23.5594 14.7228a.5269.5269 0 0 0-.0563-.1191c-.139-.2632-.4768-.3418-1.0074-.2321-1.6533.3411-2.2935.1312-2.5256-.0191 1.342-2.0482 2.445-4.522 3.0411-6.8297.2714-1.0507.7982-3.5237.1222-4.7316a1.5641 1.5641 0 0 0-.1509-.235C21.6931.9086 19.8007.0248 17.5099.0005c-1.4947-.0158-2.7705.3461-3.1161.4794a9.449 9.449 0 0 0-.5159-.0816 8.044 8.044 0 0 0-1.3114-.1278c-1.1822-.0184-2.2038.2642-3.0498.8406-.8573-.3211-4.7888-1.645-7.2219.0788C.9359 2.1526.3086 3.8733.4302 6.3043c.0409.818.5069 3.334 1.2423 5.7436.4598 1.5065.9387 2.7019 1.4334 3.582.553.9942 1.1259 1.5933 1.7143 1.7895.4474.1491 1.1327.1441 1.8581-.7279.8012-.9635 1.5903-1.8258 1.9446-2.2069.4351.2355.9064.3625 1.39.3772a.0569.0569 0 0 0 .0004.0041 11.0312 11.0312 0 0 0-.2472.3054c-.3389.4302-.4094.5197-1.5002.7443-.3102.064-1.1344.2339-1.1464.8115-.0025.1224.0329.2309.0919.3268.2269.4231.9216.6097 1.015.6331 1.3345.3335 2.5044.092 3.3714-.6787-.017 2.231.0775 4.4174.3454 5.0874.2212.5529.7618 1.9045 2.4692 1.9043.2505 0 .5263-.0291.8296-.0941 1.7819-.3821 2.5557-1.1696 2.855-2.9059.1503-.8707.4016-2.8753.5388-4.1012.0169-.0703.0357-.1207.057-.1362.0007-.0005.0697-.0471.4272.0307a.3673.3673 0 0 0 .0443.0068l.2539.0223.0149.001c.8468.0384 1.9114-.1426 2.5312-.4308.6438-.2988 1.8057-1.0323 1.5951-1.6698zM2.371 11.8765c-.7435-2.4358-1.1779-4.8851-1.2123-5.5719-.1086-2.1714.4171-3.6829 1.5623-4.4927 1.8367-1.2986 4.8398-.5408 6.108-.13-.0032.0032-.0066.0061-.0098.0094-2.0238 2.044-1.9758 5.536-1.9708 5.7495-.0002.0823.0066.1989.0162.3593.0348.5873.0996 1.6804-.0735 2.9184-.1609 1.1504.1937 2.2764.9728 3.0892.0806.0841.1648.1631.2518.2374-.3468.3714-1.1004 1.1926-1.9025 2.1576-.5677.6825-.9597.5517-1.0886.5087-.3919-.1307-.813-.5871-1.2381-1.3223-.4796-.839-.9635-2.0317-1.4155-3.5126zm6.0072 5.0871c-.1711-.0428-.3271-.1132-.4322-.1772.0889-.0394.2374-.0902.4833-.1409 1.2833-.2641 1.4815-.4506 1.9143-1.0002.0992-.126.2116-.2687.3673-.4426a.3549.3549 0 0 0 .0737-.1298c.1708-.1513.2724-.1099.4369-.0417.156.0646.3078.26.3695.4752.0291.1016.0619.2945-.0452.4444-.9043 1.2658-2.2216 1.2494-3.1676 1.0128zm2.094-3.988-.0525.141c-.133.3566-.2567.6881-.3334 1.003-.6674-.0021-1.3168-.2872-1.8105-.8024-.6279-.6551-.9131-1.5664-.7825-2.5004.1828-1.3079.1153-2.4468.079-3.0586-.005-.0857-.0095-.1607-.0122-.2199.2957-.2621 1.6659-.9962 2.6429-.7724.4459.1022.7176.4057.8305.928.5846 2.7038.0774 3.8307-.3302 4.7363-.084.1866-.1633.3629-.2311.5454zm7.3637 4.5725c-.0169.1768-.0358.376-.0618.5959l-.146.4383a.3547.3547 0 0 0-.0182.1077c-.0059.4747-.054.6489-.115.8693-.0634.2292-.1353.4891-.1794 1.0575-.11 1.4143-.8782 2.2267-2.4172 2.5565-1.5155.3251-1.7843-.4968-2.0212-1.2217a6.5824 6.5824 0 0 0-.0769-.2266c-.2154-.5858-.1911-1.4119-.1574-2.5551.0165-.5612-.0249-1.9013-.3302-2.6462.0044-.2932.0106-.5909.019-.8918a.3529.3529 0 0 0-.0153-.1126 1.4927 1.4927 0 0 0-.0439-.208c-.1226-.4283-.4213-.7866-.7797-.9351-.1424-.059-.4038-.1672-.7178-.0869.067-.276.1831-.5875.309-.9249l.0529-.142c.0595-.16.134-.3257.213-.5012.4265-.9476 1.0106-2.2453.3766-5.1772-.2374-1.0981-1.0304-1.6343-2.2324-1.5098-.7207.0746-1.3799.3654-1.7088.5321a5.6716 5.6716 0 0 0-.1958.1041c.0918-1.1064.4386-3.1741 1.7357-4.4823a4.0306 4.0306 0 0 1 .3033-.276.3532.3532 0 0 0 .1447-.0644c.7524-.5706 1.6945-.8506 2.802-.8325.4091.0067.8017.0339 1.1742.081 1.939.3544 3.2439 1.4468 4.0359 2.3827.8143.9623 1.2552 1.9315 1.4312 2.4543-1.3232-.1346-2.2234.1268-2.6797.779-.9926 1.4189.543 4.1729 1.2811 5.4964.1353.2426.2522.4522.2889.5413.2403.5825.5515.9713.7787 1.2552.0696.087.1372.1714.1885.245-.4008.1155-1.1208.3825-1.0552 1.717-.0123.1563-.0423.4469-.0834.8148-.0461.2077-.0702.4603-.0994.7662zm.8905-1.6211c-.0405-.8316.2691-.9185.5967-1.0105a2.8566 2.8566 0 0 0 .135-.0406 1.202 1.202 0 0 0 .1342.103c.5703.3765 1.5823.4213 3.0068.1344-.2016.1769-.5189.3994-.9533.6011-.4098.1903-1.0957.333-1.7473.3636-.7197.0336-1.0859-.0807-1.1721-.151zm.5695-9.2712c-.0059.3508-.0542.6692-.1054 1.0017-.055.3576-.112.7274-.1264 1.1762-.0142.4368.0404.8909.0932 1.3301.1066.887.216 1.8003-.2075 2.7014a3.5272 3.5272 0 0 1-.1876-.3856c-.0527-.1276-.1669-.3326-.3251-.6162-.6156-1.1041-2.0574-3.6896-1.3193-4.7446.3795-.5427 1.3408-.5661 2.1781-.463zm.2284 7.0137a12.3762 12.3762 0 0 0-.0853-.1074l-.0355-.0444c.7262-1.1995.5842-2.3862.4578-3.4385-.0519-.4318-.1009-.8396-.0885-1.2226.0129-.4061.0666-.7543.1185-1.0911.0639-.415.1288-.8443.1109-1.3505.0134-.0531.0188-.1158.0118-.1902-.0457-.4855-.5999-1.938-1.7294-3.253-.6076-.7073-1.4896-1.4972-2.6889-2.0395.5251-.1066 1.2328-.2035 2.0244-.1859 2.0515.0456 3.6746.8135 4.8242 2.2824a.908.908 0 0 1 .0667.1002c.7231 1.3556-.2762 6.2751-2.9867 10.5405zm-8.8166-6.1162c-.025.1794-.3089.4225-.6211.4225a.5821.5821 0 0 1-.0809-.0056c-.1873-.026-.3765-.144-.5059-.3156-.0458-.0605-.1203-.178-.1055-.2844.0055-.0401.0261-.0985.0925-.1488.1182-.0894.3518-.1226.6096-.0867.3163.0441.6426.1938.6113.4186zm7.9305-.4114c.0111.0792-.049.201-.1531.3102-.0683.0717-.212.1961-.4079.2232a.5456.5456 0 0 1-.075.0052c-.2935 0-.5414-.2344-.5607-.3717-.024-.1765.2641-.3106.5611-.352.297-.0414.6111.0088.6356.1851z\"/></svg>",
  redis:
    '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M22.71 13.145c-1.66 2.092-3.452 4.483-7.038 4.483-3.203 0-4.397-2.825-4.48-5.12.701 1.484 2.073 2.685 4.214 2.63 4.117-.133 6.94-3.852 6.94-7.239 0-4.05-3.022-6.972-8.268-6.972-3.752 0-8.4 1.428-11.455 3.685C2.59 6.937 3.885 9.958 4.35 9.626c2.648-1.904 4.748-3.13 6.784-3.744C8.12 9.244.886 17.05 0 18.425c.1 1.261 1.66 4.648 2.424 4.648.232 0 .431-.133.664-.365a100.49 100.49 0 0 0 5.54-6.765c.222 3.104 1.748 6.898 6.014 6.898 3.819 0 7.604-2.756 9.33-8.965.2-.764-.73-1.361-1.261-.73zm-4.349-5.013c0 1.959-1.926 2.922-3.685 2.922-.941 0-1.664-.247-2.235-.568 1.051-1.592 2.092-3.225 3.21-4.973 1.972.334 2.71 1.43 2.71 2.619z"/></svg>',
  mysql:
    "<svg viewBox=\"0 0 24 24\" fill=\"currentColor\"><path d=\"M16.405 5.501c-.115 0-.193.014-.274.033v.013h.014c.054.104.146.18.214.273.054.107.1.214.154.32l.014-.015c.094-.066.14-.172.14-.333-.04-.047-.046-.094-.08-.14-.04-.067-.126-.1-.18-.153zM5.77 18.695h-.927a50.854 50.854 0 00-.27-4.41h-.008l-1.41 4.41H2.45l-1.4-4.41h-.01a72.892 72.892 0 00-.195 4.41H0c.055-1.966.192-3.81.41-5.53h1.15l1.335 4.064h.008l1.347-4.064h1.095c.242 2.015.384 3.86.428 5.53zm4.017-4.08c-.378 2.045-.876 3.533-1.492 4.46-.482.716-1.01 1.073-1.583 1.073-.153 0-.34-.046-.566-.138v-.494c.11.017.24.026.386.026.268 0 .483-.075.647-.222.197-.18.295-.382.295-.605 0-.155-.077-.47-.23-.944L6.23 14.615h.91l.727 2.36c.164.536.233.91.205 1.123.4-1.064.678-2.227.835-3.483zm12.325 4.08h-2.63v-5.53h.885v4.85h1.745zm-3.32.135l-1.016-.5c.09-.076.177-.158.255-.25.433-.506.648-1.258.648-2.253 0-1.83-.718-2.746-2.155-2.746-.704 0-1.254.232-1.65.697-.43.508-.646 1.256-.646 2.245 0 .972.19 1.686.574 2.14.35.41.877.615 1.583.615.264 0 .506-.033.725-.098l1.325.772.36-.622zM15.5 17.588c-.225-.36-.337-.94-.337-1.736 0-1.393.424-2.09 1.27-2.09.443 0 .77.167.977.5.224.362.336.936.336 1.723 0 1.404-.424 2.108-1.27 2.108-.445 0-.77-.167-.978-.5zm-1.658-.425c0 .47-.172.856-.516 1.156-.344.3-.803.45-1.384.45-.543 0-1.064-.172-1.573-.515l.237-.476c.438.22.833.328 1.19.328.332 0 .593-.073.783-.22a.754.754 0 00.3-.615c0-.33-.23-.61-.648-.845-.388-.213-1.163-.657-1.163-.657-.422-.307-.632-.636-.632-1.177 0-.45.157-.81.47-1.085.315-.278.72-.415 1.22-.415.512 0 .98.136 1.4.41l-.213.476a2.726 2.726 0 00-1.064-.23c-.283 0-.502.068-.654.206a.685.685 0 00-.248.524c0 .328.234.61.666.85.393.215 1.187.67 1.187.67.433.305.648.63.648 1.168zm9.382-5.852c-.535-.014-.95.04-1.297.188-.1.04-.26.04-.274.167.055.053.063.14.11.214.08.134.218.313.346.407.14.11.28.216.427.31.26.16.555.255.81.416.145.094.293.213.44.313.073.05.12.14.214.172v-.02c-.046-.06-.06-.147-.105-.214-.067-.067-.134-.127-.2-.193a3.223 3.223 0 00-.695-.675c-.214-.146-.682-.35-.77-.595l-.013-.014c.146-.013.32-.066.46-.106.227-.06.435-.047.67-.106.106-.027.213-.06.32-.094v-.06c-.12-.12-.21-.283-.334-.395a8.867 8.867 0 00-1.104-.823c-.21-.134-.476-.22-.697-.334-.08-.04-.214-.06-.26-.127-.12-.146-.19-.34-.275-.514a17.69 17.69 0 01-.547-1.163c-.12-.262-.193-.523-.34-.763-.69-1.137-1.437-1.826-2.586-2.5-.247-.14-.543-.2-.856-.274-.167-.008-.334-.02-.5-.027-.11-.047-.216-.174-.31-.235-.38-.24-1.364-.76-1.644-.072-.18.434.267.862.422 1.082.115.153.26.328.34.5.047.116.06.235.107.356.106.294.207.622.347.897.073.14.153.287.247.413.054.073.146.107.167.227-.094.136-.1.334-.154.5-.24.757-.146 1.693.194 2.25.107.166.362.534.703.393.3-.12.234-.5.32-.835.02-.08.007-.133.048-.187v.015c.094.188.188.367.274.555.206.328.566.668.867.895.16.12.287.328.487.402v-.02h-.015c-.043-.058-.1-.086-.154-.133a3.445 3.445 0 01-.35-.4 8.76 8.76 0 01-.747-1.218c-.11-.21-.202-.436-.29-.643-.04-.08-.04-.2-.107-.24-.1.146-.247.273-.32.453-.127.288-.14.642-.188 1.01-.027.007-.014 0-.027.014-.214-.052-.287-.274-.367-.46-.2-.475-.233-1.238-.06-1.785.047-.14.247-.582.167-.716-.042-.127-.174-.2-.247-.303a2.478 2.478 0 01-.24-.427c-.16-.374-.24-.788-.414-1.162-.08-.173-.22-.354-.334-.513-.127-.18-.267-.307-.368-.52-.033-.073-.08-.194-.027-.274.014-.054.042-.075.094-.09.088-.072.335.022.422.062.247.1.455.194.662.334.094.066.195.193.315.226h.14c.214.047.455.014.655.073.355.114.675.28.962.46a5.953 5.953 0 012.085 2.286c.08.154.115.295.188.455.14.33.313.663.455.982.14.315.275.636.476.897.1.14.502.213.682.286.133.06.34.115.46.188.23.14.454.3.67.454.11.076.443.243.463.378z\"/></svg>",
  mongodb:
    '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M17.193 9.555c-1.264-5.58-4.252-7.414-4.573-8.115-.28-.394-.53-.954-.735-1.44-.036.495-.055.685-.523 1.184-.723.566-4.438 3.682-4.74 10.02-.282 5.912 4.27 9.435 4.888 9.884l.07.05A73.49 73.49 0 0111.91 24h.481c.114-1.032.284-2.056.51-3.07.417-.296.604-.463.85-.693a11.342 11.342 0 003.639-8.464c.01-.814-.103-1.662-.197-2.218zm-5.336 8.195s0-8.291.275-8.29c.213 0 .49 10.695.49 10.695-.381-.045-.765-1.76-.765-2.405z"/></svg>',
  nginx:
    '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M12 0L1.605 6v12L12 24l10.395-6V6L12 0zm6 16.59c0 .705-.646 1.29-1.529 1.29-.631 0-1.351-.255-1.801-.81l-6-7.141v6.66c0 .721-.57 1.29-1.274 1.29H7.32c-.721 0-1.29-.6-1.29-1.29V7.41c0-.705.63-1.29 1.5-1.29.646 0 1.38.255 1.83.81l5.97 7.141V7.41c0-.721.6-1.29 1.29-1.29h.075c.72 0 1.29.6 1.29 1.29v9.18H18z"/></svg>',
  nodejs:
    '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M11.998,24c-0.321,0-0.641-0.084-0.922-0.247l-2.936-1.737c-0.438-0.245-0.224-0.332-0.08-0.383 c0.585-0.203,0.703-0.25,1.328-0.604c0.065-0.037,0.151-0.023,0.218,0.017l2.256,1.339c0.082,0.045,0.197,0.045,0.272,0l8.795-5.076 c0.082-0.047,0.134-0.141,0.134-0.238V6.921c0-0.099-0.053-0.192-0.137-0.242l-8.791-5.072c-0.081-0.047-0.189-0.047-0.271,0 L3.075,6.68C2.99,6.729,2.936,6.825,2.936,6.921v10.15c0,0.097,0.054,0.189,0.139,0.235l2.409,1.392 c1.307,0.654,2.108-0.116,2.108-0.89V7.787c0-0.142,0.114-0.253,0.256-0.253h1.115c0.139,0,0.255,0.112,0.255,0.253v10.021 c0,1.745-0.95,2.745-2.604,2.745c-0.508,0-0.909,0-2.026-0.551L2.28,18.675c-0.57-0.329-0.922-0.945-0.922-1.604V6.921 c0-0.659,0.353-1.275,0.922-1.603l8.795-5.082c0.557-0.315,1.296-0.315,1.848,0l8.794,5.082c0.57,0.329,0.924,0.944,0.924,1.603 v10.15c0,0.659-0.354,1.273-0.924,1.604l-8.794,5.078C12.643,23.916,12.324,24,11.998,24z M19.099,13.993 c0-1.9-1.284-2.406-3.987-2.763c-2.731-0.361-3.009-0.548-3.009-1.187c0-0.528,0.235-1.233,2.258-1.233 c1.807,0,2.473,0.389,2.747,1.607c0.024,0.115,0.129,0.199,0.247,0.199h1.141c0.071,0,0.138-0.031,0.186-0.081 c0.048-0.054,0.074-0.123,0.067-0.196c-0.177-2.098-1.571-3.076-4.388-3.076c-2.508,0-4.004,1.058-4.004,2.833 c0,1.925,1.488,2.457,3.895,2.695c2.88,0.282,3.103,0.703,3.103,1.269c0,0.983-0.789,1.402-2.642,1.402 c-2.327,0-2.839-0.584-3.011-1.742c-0.02-0.124-0.126-0.215-0.253-0.215h-1.137c-0.141,0-0.254,0.112-0.254,0.253 c0,1.482,0.806,3.248,4.655,3.248C17.501,17.007,19.099,15.91,19.099,13.993z"/></svg>',
  database:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v6c0 1.66 3.58 3 8 3s8-1.34 8-3V5"/><path d="M4 11v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6"/></svg>',
  template:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 21V9"/></svg>',
  empty:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2" stroke-dasharray="4 3"/></svg>',
  function:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M9 21v-8m0 0V8.5C9 5.5 10.5 3 13.5 3M9 13H6m3 0h3M6 17h4"/></svg>',
  bucket:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M5 8h14l-1.8 11a2 2 0 01-2 1.7H8.8a2 2 0 01-2-1.7L5 8z"/><path d="M3 8c0-1.5 4-2.5 9-2.5s9 1 9 2.5"/></svg>',
  overview:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="9" rx="1"/><rect x="14" y="3" width="7" height="5" rx="1"/><rect x="14" y="12" width="7" height="9" rx="1"/><rect x="3" y="16" width="7" height="5" rx="1"/></svg>',
  variables:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M8 4c-2 0-3 1-3 3v3c0 1-.5 2-2 2 1.5 0 2 1 2 2v3c0 2 1 3 3 3M16 4c2 0 3 1 3 3v3c0 1 .5 2 2 2-1.5 0-2 1-2 2v3c0 2-1 3-3 3"/></svg>',
  logs:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9l3 3-3 3M13 15h4"/></svg>',
  settings:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 00.34 1.87l.06.06a2 2 0 11-2.83 2.83l-.06-.06a1.7 1.7 0 00-1.87-.34 1.7 1.7 0 00-1.04 1.56V21a2 2 0 11-4 0v-.09A1.7 1.7 0 008.6 19.4a1.7 1.7 0 00-1.87.34l-.06.06a2 2 0 11-2.83-2.83l.06-.06A1.7 1.7 0 004.6 15a1.7 1.7 0 00-1.56-1.04H3a2 2 0 110-4h.09A1.7 1.7 0 004.6 8.6a1.7 1.7 0 00-.34-1.87l-.06-.06a2 2 0 112.83-2.83l.06.06A1.7 1.7 0 008.6 4.6a1.7 1.7 0 001.04-1.56V3a2 2 0 114 0v.09a1.7 1.7 0 001.04 1.56 1.7 1.7 0 001.87-.34l.06-.06a2 2 0 112.83 2.83l-.06.06a1.7 1.7 0 00-.34 1.87V8.6a1.7 1.7 0 001.56 1.04H21a2 2 0 110 4h-.09a1.7 1.7 0 00-1.56 1.04z"/></svg>',
  files:
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6a2 2 0 012-2h4l2 2h8a2 2 0 012 2v9a2 2 0 01-2 2H5a2 2 0 01-2-2V6z"/></svg>',
};

function iconForImage(image) {
  const img = (image || "").toLowerCase();
  if (img.includes("postgres")) return "postgres";
  if (img.includes("redis")) return "redis";
  if (img.includes("mysql")) return "mysql";
  if (img.includes("mongo")) return "mongodb";
  if (img.includes("nginx")) return "nginx";
  if (img.includes("node")) return "nodejs";
  return null;
}

function hydrateIcons(root) {
  (root || document).querySelectorAll("[data-icon]").forEach((node) => {
    node.innerHTML = ICONS[node.dataset.icon] || "";
  });
}

const DATABASE_PRESETS = [
  { name: "Postgres", desc: "postgres:16-alpine, port 5432", image: "postgres:16-alpine", port: 5432, env: { POSTGRES_PASSWORD: "changeme" } },
  { name: "Redis", desc: "redis:7-alpine, port 6379", image: "redis:7-alpine", port: 6379, env: {} },
  { name: "MySQL", desc: "mysql:8, port 3306", image: "mysql:8", port: 3306, env: { MYSQL_ROOT_PASSWORD: "changeme" } },
  { name: "MongoDB", desc: "mongo:7, port 27017", image: "mongo:7", port: 27017, env: {} },
];

const TEMPLATE_PRESETS = [
  { name: "Nginx web server", desc: "nginx:alpine, port 80", image: "nginx:alpine", port: 80, command: [] },
  {
    name: "Node.js hello world",
    desc: "node:21-alpine, port 3000, inline server",
    image: "node:21-alpine",
    port: 3000,
    command: ["node", "-e", "require('http').createServer((_,r)=>r.end('hello from NimbusCore')).listen(3000)"],
  },
  { name: "Static file server", desc: "nginx:alpine, port 80", image: "nginx:alpine", port: 80, command: [] },
];

function applyPresetToModal(preset) {
  openDeploymentModal();
  const form = document.getElementById("new-deployment-form");
  setDeploymentSource("image");
  form.elements["image"].value = preset.image;
  form.elements["port"].value = preset.port || "";
  if (preset.command) form.elements["command"].value = formatCommandForInput(preset.command);
  document.getElementById("env-vars-rows").innerHTML = "";
  for (const [k, v] of Object.entries(preset.env || {})) addEnvVarRow(k, v);

  const isNginx = (preset.image || "").toLowerCase().startsWith("nginx");
  form.elements["addPersistentStorage"].checked = isNginx;
  form.elements["mountPath"].value = isNginx ? "/usr/share/nginx/html" : "";
  document.getElementById("mount-path-field").classList.toggle("hidden", !isNginx);
}

function openPresetModal(kind) {
  const presets = kind === "database" ? DATABASE_PRESETS : TEMPLATE_PRESETS;
  document.getElementById("preset-modal-title").textContent = kind === "database" ? "Choose a database" : "Choose a template";
  const list = document.getElementById("preset-list");
  list.innerHTML = "";
  for (const preset of presets) {
    const btn = el("button", "preset-item");
    btn.type = "button";
    const row = el("span", "preset-item-row");
    const iconName = iconForImage(preset.image);
    if (iconName) {
      const icon = el("span", "preset-icon");
      icon.innerHTML = ICONS[iconName];
      row.appendChild(icon);
    }
    row.appendChild(el("span", "preset-title", preset.name));
    btn.appendChild(row);
    const desc = el("span", "preset-desc", preset.desc);
    btn.appendChild(desc);
    btn.addEventListener("click", () => {
      document.getElementById("preset-modal").classList.remove("open");
      applyPresetToModal(preset);
    });
    list.appendChild(btn);
  }
  document.getElementById("preset-modal").classList.add("open");
}

function initPresetModal() {
  document.getElementById("cancel-preset-btn").addEventListener("click", () => {
    document.getElementById("preset-modal").classList.remove("open");
  });
  document.getElementById("preset-modal").addEventListener("click", (ev) => {
    if (ev.target.id === "preset-modal") ev.target.classList.remove("open");
  });
}

function initCreateMenu() {
  const menu = document.getElementById("create-menu");
  const btn = document.getElementById("create-menu-btn");
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    menu.classList.toggle("hidden");
  });
  document.addEventListener("click", () => menu.classList.add("hidden"));
  menu.addEventListener("click", (ev) => ev.stopPropagation());

  document.querySelectorAll(".create-menu-item[data-create]").forEach((item) => {
    item.addEventListener("click", () => {
      menu.classList.add("hidden");
      const kind = item.dataset.create;
      if (kind === "image") {
        openDeploymentModal();
        setDeploymentSource("image");
      } else if (kind === "git") {
        openDeploymentModal();
        setDeploymentSource("git");
      } else if (kind === "database") {
        openPresetModal("database");
      } else if (kind === "template") {
        openPresetModal("template");
      } else if (kind === "empty") {
        openDeploymentModal();
        const form = document.getElementById("new-deployment-form");
        setDeploymentSource("image");
        form.elements["image"].value = "alpine";
        form.elements["command"].value = "sleep 3600";
      }
    });
  });
}

function updateClock() {
  document.getElementById("clock").textContent = new Date().toLocaleString();
}

async function refreshAll() {
  updateClock();
  await refreshNamespaces();
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

function initLogout() {
  const btn = document.getElementById("logout-btn");
  if (!btn) return;
  btn.addEventListener("click", async () => {
    try {
      await fetch("/api/logout", { method: "POST" });
    } catch (err) {
      // ignore — redirect to /login regardless, the cookie will simply expire on its own
    }
    window.location.href = "/login";
  });
}

initTabs();
initLogout();
initDeploymentForm();
initLogsModal();
initDeploymentPanel();
initCanvas();
initProjectSwitcher();
initViewToggle();
initPresetModal();
initCreateMenu();
hydrateIcons();
setDeploymentsView("canvas");
refreshAll();
setInterval(refreshAll, 5000);
setInterval(updateClock, 1000);

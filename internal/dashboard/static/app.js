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

async function refreshPods() {
  const data = await fetchJSON("/api/pods");
  const items = data.items || [];
  document.getElementById("pods-count").textContent = items.length;
  setRows("pods-table", items, 5, (p) => {
    const tr = document.createElement("tr");
    const status = p.status || {};
    const spec = p.spec || {};
    tr.appendChild(el("td", null, p.metadata?.namespace || ""));
    tr.appendChild(el("td", null, p.metadata?.name || ""));
    const phaseTd = document.createElement("td");
    const phase = status.phase || "POD_PHASE_UNSPECIFIED";
    phaseTd.appendChild(badge(phase.replace("POD_PHASE_", "").toLowerCase(), phaseKind(phase)));
    tr.appendChild(phaseTd);
    tr.appendChild(el("td", null, spec.nodeName || "—"));
    tr.appendChild(el("td", null, String(status.restartCount || 0)));
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
    const image = (spec.template?.containers || [])[0]?.image || "—";
    tr.appendChild(el("td", null, namespace));
    tr.appendChild(el("td", null, name));
    tr.appendChild(el("td", null, image));
    tr.appendChild(el("td", null, String(replicas)));
    tr.appendChild(el("td", null, String(status.readyReplicas || 0)));

    const actionsTd = document.createElement("td");
    actionsTd.className = "row-actions";
    const editBtn = el("button", "btn-ghost btn-small", "Edit");
    editBtn.addEventListener("click", () => openDeploymentModal(d));
    const toggleBtn = el("button", "btn-ghost btn-small", replicas > 0 ? "Stop" : "Start");
    toggleBtn.addEventListener("click", () => {
      const target = replicas > 0 ? 0 : lastKnownReplicas[key] || 1;
      scaleDeployment(namespace, name, target);
    });
    const deleteBtn = el("button", "btn-ghost btn-small", "Delete");
    deleteBtn.addEventListener("click", () => deleteDeployment(namespace, name));
    actionsTd.appendChild(editBtn);
    actionsTd.appendChild(toggleBtn);
    actionsTd.appendChild(deleteBtn);
    tr.appendChild(actionsTd);
    return tr;
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

let editingDeployment = null;

function openDeploymentModal(existing) {
  const modal = document.getElementById("new-deployment-modal");
  const form = document.getElementById("new-deployment-form");
  const title = document.getElementById("deployment-modal-title");
  const submitBtn = document.getElementById("deployment-submit-btn");
  const errorEl = document.getElementById("deployment-form-error");

  errorEl.textContent = "";
  form.reset();
  document.getElementById("env-vars-rows").innerHTML = "";

  if (existing) {
    const namespace = existing.metadata?.namespace || "default";
    const name = existing.metadata?.name || "";
    editingDeployment = { namespace, name };
    title.textContent = `Edit ${namespace}/${name}`;
    submitBtn.textContent = "Save";

    const spec = existing.spec || {};
    const container = (spec.template?.containers || [])[0] || {};
    form.elements["name"].value = name;
    form.elements["name"].disabled = true;
    form.elements["namespace"].value = namespace;
    form.elements["namespace"].disabled = true;
    form.elements["image"].value = container.image || "";
    form.elements["replicas"].value = spec.replicas || 1;
    form.elements["port"].value = (container.containerPorts || [])[0] || "";
    form.elements["command"].value = (container.command || []).join(" ");
    for (const [k, v] of Object.entries(container.env || {})) {
      addEnvVarRow(k, v);
    }
  } else {
    editingDeployment = null;
    title.textContent = "New deployment";
    submitBtn.textContent = "Create";
    form.elements["name"].disabled = false;
    form.elements["namespace"].disabled = false;
  }

  modal.classList.add("open");
}

function initDeploymentForm() {
  const modal = document.getElementById("new-deployment-modal");
  const form = document.getElementById("new-deployment-form");
  const errorEl = document.getElementById("deployment-form-error");

  document.getElementById("new-deployment-btn").addEventListener("click", () => openDeploymentModal(null));
  document.getElementById("add-env-var-btn").addEventListener("click", () => addEnvVarRow());
  document.getElementById("cancel-deployment-btn").addEventListener("click", () => {
    modal.classList.remove("open");
  });
  modal.addEventListener("click", (ev) => {
    if (ev.target === modal) modal.classList.remove("open");
  });

  form.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    errorEl.textContent = "";
    const fd = new FormData(form);
    const command = String(fd.get("command") || "").trim();

    const payload = {
      name: editingDeployment ? editingDeployment.name : String(fd.get("name") || "").trim(),
      namespace: editingDeployment ? editingDeployment.namespace : String(fd.get("namespace") || "").trim() || "default",
      image: String(fd.get("image") || "").trim(),
      replicas: parseInt(fd.get("replicas"), 10) || 1,
      port: parseInt(fd.get("port"), 10) || 0,
      command: command ? command.split(/\s+/) : [],
      env: collectEnvVars(),
    };

    try {
      const result = await fetchJSON("/api/deployments", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const wasEditing = !!editingDeployment;
      modal.classList.remove("open");
      await Promise.allSettled([refreshDeployments(), refreshCICD(), refreshServices()]);

      if (result.service) {
        const nodePort = result.service.spec?.nodePort;
        showToast(`${payload.name}: exposed on NodePort ${nodePort} (see Services panel for node IPs).`);
      } else if (result.serviceError) {
        showToast(`${payload.name}: created, but could not expose port ${payload.port}: ${result.serviceError}`, true);
      } else if (wasEditing) {
        showToast(`${payload.name}: updated.`);
      }
    } catch (err) {
      errorEl.textContent = err.message || String(err);
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
  ]);
}

initTabs();
initDeploymentForm();
refreshAll();
setInterval(refreshAll, 5000);
setInterval(updateClock, 1000);

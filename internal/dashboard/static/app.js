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
    const endpoints = (status.endpoints || [])
      .map((e) => e.nodeIp + ":" + e.nodePort)
      .join(", ") || "—";
    tr.appendChild(el("td", null, namespace));
    tr.appendChild(el("td", null, name));
    tr.appendChild(el("td", null, String(spec.port || 0)));
    tr.appendChild(el("td", null, endpoints));
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
    const toggleBtn = el("button", "btn-ghost btn-small", replicas > 0 ? "Stop" : "Start");
    toggleBtn.addEventListener("click", () => {
      const target = replicas > 0 ? 0 : lastKnownReplicas[key] || 1;
      scaleDeployment(namespace, name, target);
    });
    const deleteBtn = el("button", "btn-ghost btn-small", "Delete");
    deleteBtn.addEventListener("click", () => deleteDeployment(namespace, name));
    actionsTd.appendChild(toggleBtn);
    actionsTd.appendChild(deleteBtn);
    tr.appendChild(actionsTd);
    return tr;
  });
}

function initDeploymentForm() {
  const modal = document.getElementById("new-deployment-modal");
  const form = document.getElementById("new-deployment-form");
  const errorEl = document.getElementById("deployment-form-error");

  document.getElementById("new-deployment-btn").addEventListener("click", () => {
    errorEl.textContent = "";
    form.reset();
    modal.classList.add("open");
  });
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
      name: String(fd.get("name") || "").trim(),
      namespace: String(fd.get("namespace") || "").trim() || "default",
      image: String(fd.get("image") || "").trim(),
      replicas: parseInt(fd.get("replicas"), 10) || 1,
      port: parseInt(fd.get("port"), 10) || 0,
      command: command ? command.split(/\s+/) : [],
    };

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
  ]);
}

initTabs();
initDeploymentForm();
refreshAll();
setInterval(refreshAll, 5000);
setInterval(updateClock, 1000);

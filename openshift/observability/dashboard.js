// This project was developed with assistance from AI tools.
"use strict";
const label = (value) => value.replaceAll("_", " ");
function card(name, value, good, detail) {
  const result = document.createElement("div");
  result.className = "card";
  const title = document.createElement("h3");
  title.textContent = label(name);
  const number = document.createElement("div");
  number.className = `value ${good ? "good" : "bad"}`;
  number.textContent = value;
  const caption = document.createElement("div");
  caption.className = "detail";
  caption.textContent = detail;
  result.append(title, number, caption);
  return result;
}
function drawHistory(history) {
  const svg = document.getElementById("history");
  svg.replaceChildren();
  if (history.length < 2) return;
  const first = history[0].time;
  const span = Math.max(30, history.at(-1).time - first);
  const points = history.map((p) => `${20 + 860 * (p.time - first) / span},${130 - 110 * p.ready / Math.max(1, p.total)}`).join(" ");
  const path = document.createElementNS("http://www.w3.org/2000/svg", "polyline");
  path.setAttribute("points", points);
  path.setAttribute("fill", "none");
  path.setAttribute("stroke", "#17664f");
  path.setAttribute("stroke-width", "3");
  svg.append(path);
}
async function refresh() {
  const freshness = document.getElementById("freshness");
  try {
    const response = await fetch("/api/status", {cache: "no-store"});
    if (!response.ok) throw new Error("unavailable");
    const snapshot = await response.json();
    const fresh = snapshot.collection_success;
    freshness.className = fresh ? "good" : "bad";
    freshness.textContent = fresh ? `Updated ${Math.floor(snapshot.age_seconds)}s ago` : "Collection unavailable or stale";
    if (!snapshot.data) {
      for (const id of ["components", "assignments", "warnings", "native-signals", "unavailable", "history"]) {
        document.getElementById(id).replaceChildren();
      }
      return;
    }
    const data = snapshot.data;
    document.getElementById("components").replaceChildren(...Object.entries(data.components).map(([name, item]) => card(name, fresh ? (item.ready ? "Ready" : "Not ready") : "Unverified", fresh && item.ready, `Current Pod restarts: ${item.restarts} · Allocated GPUs: ${item.gpu_allocated}`)));
    document.getElementById("assignments").replaceChildren(...Object.entries(data.assignments).map(([name, item]) => card(name, fresh ? (item.ready ? "Settled & ready" : "Needs attention") : "Unverified", fresh && item.ready, item.settle_seconds === null ? "Settlement duration unavailable" : `Latest settlement: ${item.settle_seconds.toFixed(1)}s`)));
    document.getElementById("warnings").replaceChildren(...Object.entries(data.warnings).map(([name, count]) => card(name, fresh ? String(count) : "Unverified", fresh && count === 0, "Retained warning events")));
    const unavailable = data.unavailable_signals.map((name) => {const item = document.createElement("li"); item.textContent = label(name); return item;});
    document.getElementById("unavailable").replaceChildren(...unavailable);
    const signals = data.native?.signals ?? {};
    document.getElementById("native-signals").replaceChildren(...Object.entries(signals).map(([name, item]) => {
      let value = "No completed requests";
      if (!fresh) value = "Unverified";
      else if (item.value !== null) {
        value = item.unit === "seconds" ? `${item.value.toFixed(3)} s`
          : item.unit === "ratio" ? `${(item.value * 100).toFixed(1)}%`
          : item.unit === "enabled" ? (item.value === 1 ? "Connected" : "Needs attention")
          : String(item.value);
      }
      const good = fresh && (item.unit === "enabled" ? item.value === 1
        : item.unit === "ratio" ? item.value < 0.9
        : true);
      const detail = item.unit === "seconds" ? "Mean since source restart; alerts use recent rates"
        : item.unit === "total" ? "Cumulative source count; alerts use recent increases"
        : item.unit === "ratio" ? "Memory on GPU devices visible to inference"
        : "ICON operational and cell hardware status; no motion request";
      return card(name, value, good, detail);
    }));
    drawHistory(snapshot.history);
  } catch {
    freshness.className = "bad";
    freshness.textContent = "Dashboard connection unavailable";
    document.getElementById("components").replaceChildren();
    document.getElementById("assignments").replaceChildren();
    document.getElementById("warnings").replaceChildren();
    document.getElementById("native-signals").replaceChildren();
    document.getElementById("unavailable").replaceChildren();
    document.getElementById("history").replaceChildren();
  }
}
refresh();
setInterval(refresh, 15000);

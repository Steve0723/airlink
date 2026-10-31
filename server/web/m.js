const $ = (id) => document.getElementById(id);
const params = new URLSearchParams(location.search);
const token = params.get("t") || "";

const fmtSize = (n) => {
  if (n < 1024) return n + " B";
  const u = ["KB", "MB", "GB", "TB"];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(n < 10 ? 1 : 0) + " " + u[i];
};

const fmtTime = (iso) => {
  const d = new Date(iso);
  const now = new Date();
  const hm = d.toTimeString().slice(0, 5);
  return d.toDateString() === now.toDateString() ? hm : `${d.getMonth() + 1}/${d.getDate()} ${hm}`;
};

let tipTimer = null;
let clipKind = "text"; // text=文字互传 clip=剪贴板
function tip(msg) {
  const el = $("tip");
  el.textContent = msg;
  el.classList.add("show");
  clearTimeout(tipTimer);
  tipTimer = setTimeout(() => el.classList.remove("show"), 2200);
}

async function api(method, path, body) {
  const opt = { method, headers: { "X-AirLink-Token": token } };
  if (body !== undefined) {
    opt.headers["Content-Type"] = "application/json";
    opt.body = JSON.stringify(body);
  }
  const r = await fetch(path, opt);
  if (!r.ok) {
    const j = await r.json().catch(() => ({}));
    throw new Error(j.message || "HTTP " + r.status);
  }
  return r.json();
}

function addBar(name) {
  const wrap = document.createElement("div");
  wrap.className = "bar";
  const label = document.createElement("div");
  label.textContent = name + " · 准备中";
  const track = document.createElement("div");
  track.className = "track";
  const fill = document.createElement("div");
  fill.className = "fill";
  track.appendChild(fill);
  wrap.append(label, track);
  $("bars").appendChild(wrap);
  return {
    set(pct) {
      fill.style.width = Math.round(pct * 100) + "%";
      label.textContent = `${name} · ${Math.round(pct * 100)}%`;
    },
    done(ok) {
      label.textContent = ok ? `${name} · 已发送` : `${name} · 失败`;
      if (!ok) fill.style.background = "#c0453f";
      setTimeout(() => wrap.remove(), ok ? 1600 : 5000);
    },
  };
}

// 分片上传：服务端返回已收到的分片位图，断线后重传只补缺口。
async function uploadFile(file) {
  const bar = addBar(file.name);
  try {
    const init = await api("POST", "/api/v1/upload/init", { name: file.name, size: file.size });
    const { id, chunkSize, totalChunks, received } = init;
    let done = received.filter(Boolean).length;
    if (done > 0) tip(`从 ${Math.round((done / totalChunks) * 100)}% 继续`);
    bar.set(done / totalChunks);

    for (let i = 0; i < totalChunks; i++) {
      if (received[i]) continue;
      const start = i * chunkSize;
      const blob = file.slice(start, Math.min(start + chunkSize, file.size));
      const r = await fetch(`/api/v1/upload/${id}/${i}?t=${encodeURIComponent(token)}`, {
        method: "PUT",
        body: blob,
      });
      if (!r.ok) throw new Error("第 " + (i + 1) + " 片传输失败");
      done++;
      bar.set(done / totalChunks);
    }
    await api("POST", `/api/v1/upload/${id}/finish`, {});
    bar.done(true);
  } catch (e) {
    bar.done(false);
    tip("上传失败：" + e.message);
  }
}

const queue = [];
let busy = false;

async function pump() {
  if (busy) return;
  busy = true;
  while (queue.length) await uploadFile(queue.shift());
  busy = false;
  refresh();
}

function enqueue(files) {
  for (const f of files) queue.push(f);
  pump();
}

async function refresh() {
  let data;
  try {
    const r = await fetch("/api/v1/files", { headers: { "X-AirLink-Token": token } });
    if (!r.ok) throw new Error();
    data = await r.json();
  } catch {
    $("dev").textContent = "连接已断开";
    $("stat").textContent = "";
    return;
  }

  const files = data.files || [];
  $("count").textContent = files.length ? files.length + " 个" : "";

  const list = $("files");
  list.innerHTML = "";
  if (!files.length) {
    list.innerHTML = '<li class="empty">电脑上还没有文件</li>';
  }
  files.forEach((f) => {
    const li = document.createElement("li");
    const name = document.createElement("span");
    name.className = "fname";
    name.textContent = f.name;

    const meta = document.createElement("span");
    meta.className = "fmeta";
    meta.textContent = fmtSize(f.size);

    // 直接给下载链接，交给安卓系统下载管理器流式落盘，不占内存。
    const a = document.createElement("a");
    a.href = `/api/v1/files/${f.id}?t=${encodeURIComponent(token)}`;
    a.setAttribute("download", f.name);
    a.textContent = "下载";

    li.append(name, meta, a);
    list.appendChild(li);
  });

  try {
    const d = await api("GET", `/api/v1/clipboard?kind=${clipKind}`);
    const list = $("texts");
    list.innerHTML = "";
    (d.clips || []).slice(0, 8).forEach((item) => {
      const li = document.createElement("li");
      const body = document.createElement("span");
      body.textContent = item.content;
      const meta = document.createElement("span");
      meta.className = "meta";
      meta.textContent = `${item.from} · ${fmtTime(item.at)}`;
      const btn = document.createElement("button");
      btn.className = "copy";
      btn.textContent = "复制";
      btn.onclick = () => copyText(item.content);

      const del = document.createElement("button");
      del.className = "del";
      del.textContent = "删除";
      del.onclick = () => delClip(item.id);

      li.append(body, meta, btn, del);
      list.appendChild(li);
    });
  } catch {}
}

// 标签切换
document.querySelectorAll(".tab").forEach((t) => {
  t.onclick = () => {
    clipKind = t.dataset.kind;
    document.querySelectorAll(".tab").forEach((x) => x.classList.toggle("active", x === t));
    refresh();
  };
});

// 删除一条记录
async function delClip(id) {
  try {
    await api("DELETE", `/api/v1/clipboard/${id}`);
    refresh();
  } catch {}
}

// 非 HTTPS 下没有 navigator.clipboard，只能用 execCommand 兜底。
function copyText(text) {
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  try {
    document.execCommand("copy");
    tip("已复制");
  } catch {
    tip("复制失败，请长按选择");
  }
  document.body.removeChild(ta);
}

$("pick").onclick = () => $("file").click();
$("file").onchange = (e) => {
  enqueue(e.target.files);
  e.target.value = "";
};

$("sendText").onclick = async () => {
  const el = $("text");
  const content = el.value.trim();
  if (!content) return;
  try {
    await api("POST", "/api/v1/clipboard", { content, from: "手机" });
    el.value = "";
    tip("已发送");
    refresh();
  } catch (e) {
    tip("发送失败：" + e.message);
  }
};

$("text").addEventListener("keydown", (e) => {
  if (e.key === "Enter") $("sendText").click();
});

(async () => {
  try {
    const info = await api("POST", "/api/v1/handshake", { device: "browser" });
    $("dev").textContent = info.device;
    $("stat").textContent = "已连接";
  } catch {
    $("dev").textContent = "配对码无效";
    $("stat").textContent = "";
    tip("请重新扫描电脑上的二维码");
    return;
  }
  refresh();
  setInterval(refresh, 4000);
})();

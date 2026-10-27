const $ = (id) => document.getElementById(id);

let token = "";
let lastSig = "";
let polling = null;

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
  const sameDay = d.toDateString() === now.toDateString();
  const hm = d.toTimeString().slice(0, 5);
  return sameDay ? hm : `${d.getMonth() + 1}/${d.getDate()} ${hm}`;
};

async function refresh(force) {
  let s;
  try {
    const r = await fetch("/api/console/state", { cache: "no-store" });
    if (!r.ok) return;
    s = await r.json();
  } catch {
    $("device").textContent = "服务已断开";
    return;
  }

  token = s.token;
  $("device").textContent = s.device;
  $("version").textContent = `AirLink ${s.version} · 端口 ${s.port}`;
  $("qr").src = "/api/console/qr.png?v=" + s.token.slice(0, 8);

  const sig = JSON.stringify([
    s.files.map((f) => f.id + ":" + f.size),
    s.texts.map((t) => t.id),
  ]);
  if (!force && sig === lastSig) return;
  lastSig = sig;

  $("token").textContent = s.token.slice(0, 8).toUpperCase();

  const eps = $("endpoints");
  eps.innerHTML = "";
  if (!s.endpoints.length) {
    $("hint").textContent = "没有找到局域网地址，请检查是否已连接 WiFi 或网线。";
  } else {
    const lan = s.endpoints.filter((e) => e.kind === "lan");
    $("hint").textContent = lan.length
      ? ""
      : "当前只有虚拟网卡地址，手机可能连不上。请检查是否开着 VPN 或代理软件。";
    s.endpoints.forEach((e) => {
      const li = document.createElement("li");
      li.textContent = `${e.ip}:${e.port}`;
      li.className = e.kind;
      li.title = e.iface;
      eps.appendChild(li);
    });
  }

  $("count").textContent = s.files.length ? s.files.length + " 个" : "";
  const list = $("files");
  list.innerHTML = "";
  if (!s.files.length) {
    list.innerHTML = '<li class="empty">还没有文件</li>';
  }
  s.files.forEach((f) => {
    const li = document.createElement("li");
    const name = document.createElement("span");
    name.className = "fname";
    name.textContent = f.name;
    name.title = f.name;

    const meta = document.createElement("span");
    meta.className = "fmeta";
    meta.textContent = fmtSize(f.size);

    const a = document.createElement("a");
    a.href = `/api/v1/files/${f.id}?t=${encodeURIComponent(s.token)}`;
    a.textContent = "下载";

    const del = document.createElement("button");
    del.className = "btn ghost danger";
    del.textContent = "删除";
    del.onclick = async () => {
      if (!confirm(`删除「${f.name}」？`)) return;
      await fetch(`/api/v1/files/${f.id}?t=${encodeURIComponent(s.token)}`, { method: "DELETE" });
      refresh(true);
    };

    li.append(name, meta, a, del);
    list.appendChild(li);
  });

  const texts = $("texts");
  texts.innerHTML = "";
  s.texts.slice(0, 20).forEach((t) => {
    const li = document.createElement("li");
    li.textContent = t.content;
    const meta = document.createElement("span");
    meta.className = "meta";
    meta.textContent = `${t.from} · ${fmtTime(t.at)}`;
    li.appendChild(meta);
    texts.appendChild(li);
  });
}

$("copy").onclick = async () => {
  try {
    await navigator.clipboard.writeText(token);
    $("copy").textContent = "已复制";
    setTimeout(() => ($("copy").textContent = "复制"), 1400);
  } catch {
    alert("复制失败，配对码：" + token);
  }
};

$("rotate").onclick = async () => {
  if (!confirm("重新生成后，已配对的手机需要重新扫码。继续？")) return;
  await fetch("/api/console/rotate", { method: "POST" });
  refresh(true);
};

$("sendText").onclick = async () => {
  const el = $("text");
  const content = el.value.trim();
  if (!content) return;
  await fetch("/api/console/text", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ content }),
  });
  el.value = "";
  refresh(true);
};

const queue = [];
let uploading = false;

function addBar(name) {
  const wrap = document.createElement("div");
  wrap.className = "bar";
  const label = document.createElement("div");
  label.textContent = name + " · 0%";
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
      label.textContent = ok ? `${name} · 完成` : `${name} · 失败`;
      if (!ok) fill.style.background = "#c0453f";
      setTimeout(() => wrap.remove(), ok ? 1500 : 4000);
    },
  };
}

function uploadOne(file) {
  return new Promise((resolve) => {
    const bar = addBar(file.name);
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/console/upload?name=" + encodeURIComponent(file.name));
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) bar.set(e.loaded / e.total);
    };
    xhr.onload = () => {
      bar.done(xhr.status === 200);
      resolve();
    };
    xhr.onerror = () => {
      bar.done(false);
      resolve();
    };
    xhr.send(file);
  });
}

async function pump() {
  if (uploading) return;
  uploading = true;
  $("uploadState").textContent = "上传中…";
  while (queue.length) await uploadOne(queue.shift());
  uploading = false;
  $("uploadState").textContent = "";
  refresh(true);
}

function enqueue(files) {
  for (const f of files) queue.push(f);
  pump();
}

$("drop").onclick = () => $("picker").click();
$("picker").onchange = (e) => {
  enqueue(e.target.files);
  e.target.value = "";
};

const drop = $("drop");
["dragenter", "dragover"].forEach((ev) =>
  drop.addEventListener(ev, (e) => {
    e.preventDefault();
    drop.classList.add("over");
  })
);
["dragleave", "drop"].forEach((ev) =>
  drop.addEventListener(ev, (e) => {
    e.preventDefault();
    drop.classList.remove("over");
  })
);
drop.addEventListener("drop", (e) => {
  if (e.dataTransfer?.files?.length) enqueue(e.dataTransfer.files);
});

document.addEventListener("paste", (e) => {
  if (document.activeElement === $("text")) return;
  const items = e.clipboardData?.items || [];
  const files = [];
  for (const it of items) if (it.kind === "file") files.push(it.getAsFile());
  if (files.length) enqueue(files);
});

refresh(true);
polling = setInterval(() => refresh(false), 3000);
window.addEventListener("beforeunload", () => clearInterval(polling));

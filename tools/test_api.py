import json, os, random, sys, urllib.parse, urllib.request, urllib.error

BASE = sys.argv[2] if len(sys.argv) > 2 else "http://127.0.0.1:6027"
DATA = sys.argv[1] if len(sys.argv) > 1 else "/tmp/airlink-data"

state = json.load(open(os.path.join(DATA, "state.json"), encoding="utf-8"))
TOKEN = state["token"]

passed = failed = 0


def check(name, cond, extra=""):
    global passed, failed
    if cond:
        passed += 1
        print(f"  PASS  {name}")
    else:
        failed += 1
        print(f"  FAIL  {name}  {extra}")


_opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))


def req(method, path, body=None, headers=None, raw=False):
    url = path if path.startswith("http") else BASE + path
    data = None
    h = dict(headers or {})
    if body is not None:
        if isinstance(body, (dict, list)):
            data = json.dumps(body).encode()
            h["Content-Type"] = "application/json"
        else:
            data = body
    r = urllib.request.Request(url, data=data, headers=h, method=method)
    try:
        with _opener.open(r) as resp:
            payload = resp.read()
            return resp.status, (payload if raw else json.loads(payload or b"{}")), dict(resp.headers)
    except urllib.error.HTTPError as e:
        payload = e.read()
        try:
            parsed = json.loads(payload or b"{}")
        except Exception:
            parsed = {"raw": payload[:200]}
        return e.code, parsed, dict(e.headers)


AUTH = {"X-AirLink-Token": TOKEN}

print("\n[1] 基础连通")
s, j, _ = req("GET", "/api/v1/ping")
check("ping 无需配对码", s == 200 and j.get("ok"), j)

s, body, _ = req("GET", "/j", raw=True)
check("/j 手机页面可访问", s == 200 and b"<html" in body.lower())

s, j, _ = req("POST", "/api/v1/handshake", {"device": "test"}, AUTH)
check("握手成功", s == 200 and j.get("ok"), j)

print("\n[2] 鉴权")
s, j, _ = req("GET", "/api/v1/files")
check("无配对码被拒绝", s == 401, s)
s, j, _ = req("GET", "/api/v1/files", headers={"X-AirLink-Token": "wrong"})
check("错误配对码被拒绝", s == 401, s)
s, j, _ = req("GET", "/api/v1/files?t=" + TOKEN)
check("query 传配对码可用", s == 200, s)

print("\n[3] 分片上传（8MB，模拟断点续传）")
blob = bytes(random.getrandbits(8) for _ in range(8 * 1024 * 1024))
name = "test-big.bin"
s, j, _ = req("POST", "/api/v1/upload/init", {"name": name, "size": len(blob)}, AUTH)
check("初始化上传", s == 200 and j.get("totalChunks") == 2, j)
uid, cs, total = j["id"], j["chunkSize"], j["totalChunks"]

# 只传第 0 片，模拟中断
chunk0 = blob[0:cs]
s, j, _ = req("PUT", f"/api/v1/upload/{uid}/0", chunk0, AUTH)
check("上传第 0 片", s == 200, j)

s, j, _ = req("GET", f"/api/v1/upload/{uid}/status", headers=AUTH)
check("状态显示 1/2 已收", j.get("received", [None])[0] is True and j.get("received")[1] is False, j)

# 重新 init 同名同大小，应该复用进度
s, j, _ = req("POST", "/api/v1/upload/init", {"name": name, "size": len(blob)}, AUTH)
check("重新初始化复用进度", j["id"] == uid and j["received"][0] is True, j)

chunk1 = blob[cs:]
s, j, _ = req("PUT", f"/api/v1/upload/{uid}/1", chunk1, AUTH)
check("上传第 1 片", s == 200, j)

s, j, _ = req("POST", f"/api/v1/upload/{uid}/finish", {}, AUTH)
check("合并完成", s == 200 and j.get("file", {}).get("size") == len(blob), j)
fid = j.get("file", {}).get("id")

print("\n[4] 内容校验与 Range 下载")
s, data, _ = req("GET", f"/api/v1/files/{fid}", headers=AUTH, raw=True)
check("下载内容与上传一致", data == blob, f"{len(data)} vs {len(blob)}")

s, part, hdrs = req("GET", f"/api/v1/files/{fid}", headers={**AUTH, "Range": "bytes=100-199"}, raw=True)
check("Range 请求返回 206", s == 206, s)
check("Range 内容正确", part == blob[100:200], len(part))
check("Content-Range 正确", hdrs.get("Content-Range", "").startswith("bytes 100-199/"), hdrs.get("Content-Range"))

print("\n[5] 文件列表与删除")
s, j, _ = req("GET", "/api/v1/files", headers=AUTH)
check("列表包含新文件", any(f["name"] == name for f in j["files"]), [f["name"] for f in j["files"]])

s, j, _ = req("DELETE", f"/api/v1/files/{fid}", headers=AUTH)
check("删除成功", s == 200, j)
s, j, _ = req("GET", "/api/v1/files", headers=AUTH)
check("列表已移除", not any(f["id"] == fid for f in j["files"]))

print("\n[6] 电脑端上传与文本")
s, j, _ = req("POST", "/api/console/upload?name=" + urllib.parse.quote("电脑文件.txt"), "hello from pc".encode())
check("控制台上传", s == 200 and j.get("file", {}).get("size") == 13, j)

s, j, _ = req("POST", "/api/v1/text", {"content": "手机发来的文字", "from": "手机"}, AUTH)
check("写入文本", s == 200 and j["text"]["content"] == "手机发来的文字", j)
s, j, _ = req("GET", "/api/v1/text", headers=AUTH)
check("读取文本", len(j["texts"]) >= 1 and j["texts"][0]["from"] == "手机", j)

print("\n[7] 边界与安全")
s, j, _ = req("PUT", f"/api/v1/upload/{uid}/99", b"x", AUTH)
check("越界分片被拒", s in (400, 404), s)
s, j, _ = req("POST", "/api/v1/upload/init", {"name": "../../etc/passwd", "size": 10}, AUTH)
check("路径穿越被净化", ".." not in j.get("name", "") and "/" not in j.get("name", ""), j.get("name"))
s, j, _ = req("GET", "/api/console/state")
check("控制台本机可读", s == 200 and j.get("token") == TOKEN, s)
s, j, _ = req("POST", "/api/v1/upload/init", {"name": "x", "size": 0}, AUTH)
check("零字节被拒", s == 400, s)

print("\n[8] 剪贴板")
s, j, _ = req("POST", "/api/v1/clipboard", {"content": "手机剪贴板内容", "from": "手机"}, AUTH)
check("手机推送剪贴板", s == 200 and j.get("clip", {}).get("content") == "手机剪贴板内容", j)
s, j, _ = req("GET", "/api/v1/clipboard", headers=AUTH)
check("读取剪贴板列表", len(j.get("clips", [])) >= 1, j)
s, j, _ = req("POST", "/api/console/clipboard", {"content": "电脑剪贴板内容"})
check("电脑端推送剪贴板", s == 200 and j.get("clip", {}).get("from") == "电脑", j)
s, j, _ = req("POST", "/api/v1/clipboard", {"content": "x" * (300 * 1024), "from": "手机"}, AUTH)
check("超大文本被拒", s == 400, s)
s, j, _ = req("POST", "/api/v1/clipboard", {"content": "   ", "from": "手机"}, AUTH)
check("空内容被拒", s == 400, s)

# 电脑发的文字必须写进 clips——手机读的是这个列表。
# 之前 sendText 走的是 texts，导致「发送文字」在手机上永远看不到。
s, j, _ = req("POST", "/api/console/clipboard", {"content": "电脑发的文字"})
check("电脑发文字成功", s == 200 and j.get("clip", {}).get("from") == "电脑", j)
s, j, _ = req("GET", "/api/v1/clipboard", headers=AUTH)
check(
    "手机能读到电脑发的文字",
    any(c["content"] == "电脑发的文字" for c in j.get("clips", [])),
    [c["content"] for c in j.get("clips", [])],
)

cid = j["clips"][0]["id"]
s, j, _ = req("DELETE", f"/api/v1/clipboard/{cid}", headers=AUTH)
check("删除单条记录", s == 200, j)
s, j, _ = req("GET", "/api/v1/clipboard", headers=AUTH)
check("删掉的记录不在了", all(c["id"] != cid for c in j.get("clips", [])), j)

s, j, _ = req("DELETE", "/api/v1/clipboard/nonexistent", headers=AUTH)
check("删不存在的记录返回 404", s == 404, s)

s, j, _ = req("DELETE", "/api/v1/clipboard", headers=AUTH)
check("清空记录", s == 200, j)
s, j, _ = req("GET", "/api/v1/clipboard", headers=AUTH)
check("清空后列表为空", len(j.get("clips", [])) == 0, j)

s, j, _ = req("DELETE", "/api/v1/clipboard", headers={})
check("删除需要鉴权", s == 401, s)

print("\n[9] 重复上传去重")


def upload_chunked(name, data):
    s, j, _ = req("POST", "/api/v1/upload/init", {"name": name, "size": len(data)}, AUTH)
    uid, cs, total = j["id"], j["chunkSize"], j["totalChunks"]
    for i in range(total):
        req("PUT", f"/api/v1/upload/{uid}/{i}", data[i * cs:(i + 1) * cs], AUTH)
    s, j, _ = req("POST", f"/api/v1/upload/{uid}/finish", {}, AUTH)
    return j.get("file", {})


first = upload_chunked("dup-test.jpg", b"x" * 300000)
second = upload_chunked("dup-test.jpg", b"x" * 300000)
check("同名同大小不产生副本", first.get("id") == second.get("id"), (first, second))
check("文件名未被加后缀", second.get("name") == "dup-test.jpg", second.get("name"))

s, j, _ = req("GET", "/api/v1/files", headers=AUTH)
same = [f for f in j["files"] if f["name"].startswith("dup-test")]
check("列表里只有一份", len(same) == 1, [f["name"] for f in same])

other = upload_chunked("dup-test.jpg", b"y" * 300000)
check("同名不同大小仍会保留两份", other.get("id") != first.get("id"), (first, other))

print("\n[10] 手机登记")
s, j, _ = req("POST", "/api/v1/phone/register", {"id": "phone-test-1", "name": "测试机"}, AUTH)
check("登记手机", s == 200, j)
s, j, _ = req("POST", "/api/v1/phone/register", {"id": "", "name": "x"}, AUTH)
check("空 ID 被拒", s == 400, s)
s, j, _ = req("POST", "/api/v1/phone/register", {"id": "phone-test-1", "name": "改个名"}, AUTH)
check("重复登记幂等", s == 200, s)

state = json.load(open(os.path.join(DATA, "state.json"), encoding="utf-8"))
phones = state.get("phones", [])
check("手机已持久化", any(p["id"] == "phone-test-1" for p in phones), phones)
check("重复登记不产生两条", len([p for p in phones if p["id"] == "phone-test-1"]) == 1, phones)

print(f"\n结果：{passed} 通过，{failed} 失败\n")
sys.exit(1 if failed else 0)

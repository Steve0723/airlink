"""relay 中转服务测试。用法：python test_relay.py <base_url> <key>"""

import hashlib
import json
import sys
import urllib.error
import urllib.parse
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:8090").rstrip("/")
KEY = sys.argv[2] if len(sys.argv) > 2 else ""

_opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
passed = failed = 0


def check(name, cond, extra=""):
    global passed, failed
    if cond:
        passed += 1
        print(f"  PASS  {name}")
    else:
        failed += 1
        print(f"  FAIL  {name}  {extra}")


def req(method, path, body=None, headers=None, raw=False):
    url = path if path.startswith("http") else BASE + path
    h = dict(headers or {})
    data = body
    if isinstance(body, (dict, list)):
        data = json.dumps(body).encode()
        h["Content-Type"] = "application/json"
    r = urllib.request.Request(url, data=data, headers=h, method=method)
    try:
        with _opener.open(r) as resp:
            payload = resp.read()
            return resp.status, (payload if raw else json.loads(payload or b"{}"))
    except urllib.error.HTTPError as e:
        payload = e.read()
        try:
            parsed = json.loads(payload or b"{}")
        except Exception:
            parsed = {"raw": payload[:120]}
        return e.code, parsed


AUTH = {"X-AirLink-Key": KEY}
DEV_A = "dev-aaaa-0001"
DEV_B = "dev-bbbb-0002"

print("\n[1] 健康检查")
s, j = req("GET", "/healthz")
check("healthz 可访问", s == 200 and j.get("ok"), j)

print("\n[2] 鉴权")
s, j = req("POST", "/api/v1/inbox?to=" + DEV_A + "&name=x.txt", b"hi")
check("无密钥投递被拒", s == 401, s)
s, j = req("GET", "/api/v1/inbox?device=" + DEV_A)
check("无密钥拉取被拒", s == 401, s)
s, j = req("POST", "/api/v1/inbox?to=" + DEV_A + "&name=x.txt", b"hi",
           {"X-AirLink-Key": "wrong"})
check("错误密钥被拒", s == 401, s)

print("\n[3] 投递与取回")
blob = bytes(range(256)) * 800
s, j = req("POST", "/api/v1/inbox?to=" + DEV_A + "&name=" +
           urllib.parse.quote("外网照片.jpg"), blob, AUTH)
check("投递成功", s == 200 and len(j.get("code", "")) == 8, j)
code = j.get("code", "")
check("返回大小正确", j.get("size") == len(blob), j.get("size"))

s, j = req("GET", "/api/v1/inbox?device=" + DEV_A, headers=AUTH)
items = j.get("items", [])
check("目标设备能看到", len(items) == 1 and items[0]["code"] == code, items)
check("文件名保真", items[0]["name"] == "外网照片.jpg", items[0].get("name") if items else None)

s, j = req("GET", "/api/v1/inbox?device=" + DEV_B, headers=AUTH)
check("别的设备看不到", len(j.get("items", [])) == 0, j)

s, body = req("GET", "/api/v1/inbox/" + code, headers=AUTH, raw=True)
check("下载内容一致", s == 200 and hashlib.sha256(body).hexdigest() ==
      hashlib.sha256(blob).hexdigest(), s)

print("\n[4] 取走即删")
s, j = req("DELETE", "/api/v1/inbox/" + code, headers=AUTH)
check("删除成功", s == 200, j)
s, j = req("GET", "/api/v1/inbox?device=" + DEV_A, headers=AUTH)
check("列表已清空", len(j.get("items", [])) == 0, j)
s, j = req("GET", "/api/v1/inbox/" + code, headers=AUTH)
check("下载返回 404", s == 404, s)

print("\n[5] 边界")
s, j = req("POST", "/api/v1/inbox?name=x.txt", b"hi", AUTH)
check("缺目标设备被拒", s == 400, s)
s, j = req("POST", "/api/v1/inbox?to=" + DEV_A + "&name=x.txt", b"", AUTH)
check("空文件被拒", s == 400, s)
s, j = req("POST", "/api/v1/inbox?to=" + DEV_A + "&name=" +
           urllib.parse.quote("../../etc/passwd"), b"x", AUTH)
check("路径穿越被净化", s == 200, s)
if s == 200:
    s2, j2 = req("GET", "/api/v1/inbox?device=" + DEV_A, headers=AUTH)
    names = [i["name"] for i in j2.get("items", [])]
    check("文件名不含目录", all("/" not in n and ".." not in n for n in names), names)
    for i in j2.get("items", []):
        req("DELETE", "/api/v1/inbox/" + i["code"], headers=AUTH)

print(f"\n结果：{passed} 通过，{failed} 失败\n")
sys.exit(1 if failed else 0)

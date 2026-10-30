"""用 Edge 应用模式打开页面并截图。

只截 AirLink 自己的窗口：按标题匹配，PrintWindow 抓窗口内容，
截完发 WM_CLOSE 关掉，不碰用户其它的 Edge 窗口。
"""

import ctypes
import ctypes.wintypes as wt
import struct
import subprocess
import sys
import time
import zlib

EDGE_CANDIDATES = [
    r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
]

u = ctypes.WinDLL("user32", use_last_error=True)
gdi = ctypes.WinDLL("gdi32", use_last_error=True)

try:
    ctypes.WinDLL("shcore").SetProcessDpiAwareness(2)
except Exception:
    pass


class RECT(ctypes.Structure):
    _fields_ = [("l", ctypes.c_long), ("t", ctypes.c_long),
                ("r", ctypes.c_long), ("b", ctypes.c_long)]


class BITMAPINFOHEADER(ctypes.Structure):
    _fields_ = [("biSize", wt.DWORD), ("biWidth", wt.LONG), ("biHeight", wt.LONG),
                ("biPlanes", wt.WORD), ("biBitCount", wt.WORD),
                ("biCompression", wt.DWORD), ("biSizeImage", wt.DWORD),
                ("biXPelsPerMeter", wt.LONG), ("biYPelsPerMeter", wt.LONG),
                ("biClrUsed", wt.DWORD), ("biClrImportant", wt.DWORD)]


def find_edge():
    for p in EDGE_CANDIDATES:
        try:
            open(p, "rb").close()
            return p
        except OSError:
            continue
    return None


def find_window(title_kw="AirLink", min_w=400):
    hits = []

    @ctypes.WINFUNCTYPE(ctypes.c_bool, wt.HWND, wt.LPARAM)
    def cb(hwnd, lp):
        if not u.IsWindowVisible(hwnd):
            return True
        buf = ctypes.create_unicode_buffer(256)
        u.GetWindowTextW(hwnd, buf, 256)
        if title_kw in buf.value:
            r = RECT()
            u.GetWindowRect(hwnd, ctypes.byref(r))
            if r.r - r.l >= min_w:
                hits.append((hwnd, buf.value, r))
        return True

    u.EnumWindows(cb, 0)
    return hits


def grab(hwnd, rect, out_path):
    w, h = rect.r - rect.l, rect.b - rect.t
    hdc_screen = u.GetDC(0)
    hdc = gdi.CreateCompatibleDC(hdc_screen)
    hbm = gdi.CreateCompatibleBitmap(hdc_screen, w, h)
    gdi.SelectObject(hdc, hbm)

    u.PrintWindow(hwnd, hdc, 2)

    bi = BITMAPINFOHEADER()
    bi.biSize = ctypes.sizeof(BITMAPINFOHEADER)
    bi.biWidth = w
    bi.biHeight = -h
    bi.biPlanes = 1
    bi.biBitCount = 32

    buf = ctypes.create_string_buffer(w * h * 4)
    gdi.GetDIBits(hdc, hbm, 0, h, buf, ctypes.byref(bi), 0)

    raw = buf.raw
    lines = []
    for y in range(h):
        off = y * w * 4
        line = bytearray(b"\x00")
        for x in range(w):
            b_, g_, r_, _ = raw[off + x * 4: off + x * 4 + 4]
            line += bytes((r_, g_, b_))
        lines.append(bytes(line))

    def chunk(tag, data):
        return (struct.pack(">I", len(data)) + tag + data
                + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF))

    ihdr = struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)
    png = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", ihdr)
           + chunk(b"IDAT", zlib.compress(b"".join(lines), 6)) + chunk(b"IEND", b""))
    open(out_path, "wb").write(png)

    gdi.DeleteObject(hbm)
    gdi.DeleteDC(hdc)
    u.ReleaseDC(0, hdc_screen)
    return w, h


def main():
    url = sys.argv[1] if len(sys.argv) > 1 else "http://127.0.0.1:6027/ui"
    out = sys.argv[2] if len(sys.argv) > 2 else "ui-shot.png"
    size = sys.argv[3] if len(sys.argv) > 3 else "920x780"
    if "x" in size:
        width, height = size.split("x")
    else:
        width, height = size, "780"

    exe = find_edge()
    if not exe:
        sys.exit("没找到 Edge")

    before = {h[0] for h in find_window()}
    subprocess.Popen(
        [exe, "--app=" + url, f"--window-size={width},{height}"],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )

    hwnd = None
    for _ in range(20):
        time.sleep(0.5)
        for h, title, rect in find_window():
            if h not in before:
                hwnd = (h, title, rect)
                break
        if hwnd:
            break

    if not hwnd:
        sys.exit("没等到窗口")

    h, title, rect = hwnd

    # 按 DPI 换算成物理像素，强制窗口尺寸
    dpi = u.GetDpiForWindow(h) if hasattr(u, "GetDpiForWindow") else 96
    try:
        factor = dpi / 96.0
    except Exception:
        factor = 1.0
    if factor <= 0:
        factor = 1.0

    target_w = int(int(width) * factor)
    target_h = int(int(height) * factor)
    SWP_NOZORDER = 0x0004
    u.SetWindowPos(h, 0, 0, 0, target_w, target_h, SWP_NOZORDER)

    time.sleep(1.5)  # 等页面重新布局和渲染

    r2 = RECT()
    u.GetWindowRect(h, ctypes.byref(r2))
    rect = r2
    w, ht = grab(h, rect, out)
    print(f"saved {out} {w}x{ht}")

    u.PostMessageW(h, 0x0010, 0, 0)  # WM_CLOSE


if __name__ == "__main__":
    main()

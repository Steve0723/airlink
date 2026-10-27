import ctypes
import ctypes.wintypes as wt
import struct
import sys
import zlib

user32 = ctypes.WinDLL("user32", use_last_error=True)
gdi32 = ctypes.WinDLL("gdi32", use_last_error=True)

try:
    ctypes.WinDLL("shcore").SetProcessDpiAwareness(2)
except Exception:
    pass


class RECT(ctypes.Structure):
    _fields_ = [("left", ctypes.c_long), ("top", ctypes.c_long),
                ("right", ctypes.c_long), ("bottom", ctypes.c_long)]


class BITMAPINFOHEADER(ctypes.Structure):
    _fields_ = [("biSize", wt.DWORD), ("biWidth", ctypes.c_long), ("biHeight", ctypes.c_long),
                ("biPlanes", wt.WORD), ("biBitCount", wt.WORD), ("biCompression", wt.DWORD),
                ("biSizeImage", wt.DWORD), ("biXPelsPerMeter", ctypes.c_long),
                ("biYPelsPerMeter", ctypes.c_long), ("biClrUsed", wt.DWORD), ("biClrImportant", wt.DWORD)]


target_pid = int(sys.argv[1])
out_path = sys.argv[2]

found = []


@ctypes.WINFUNCTYPE(ctypes.c_bool, wt.HWND, wt.LPARAM)
def enum_proc(hwnd, lparam):
    pid = wt.DWORD()
    user32.GetWindowThreadProcessId(hwnd, ctypes.byref(pid))
    if pid.value == target_pid and user32.IsWindowVisible(hwnd):
        buf = ctypes.create_unicode_buffer(256)
        user32.GetWindowTextW(hwnd, buf, 256)
        if buf.value:
            found.append((hwnd, buf.value))
    return True


user32.EnumWindows(enum_proc, 0)
if not found:
    print("NO_WINDOW")
    sys.exit(1)

hwnd, title = found[0]
print(f"window: {title!r} hwnd={hwnd}")

rect = RECT()
user32.GetWindowRect(hwnd, ctypes.byref(rect))
w = rect.right - rect.left
h = rect.bottom - rect.top
print(f"size: {w}x{h}")

hdc_screen = user32.GetDC(0)
hdc_mem = gdi32.CreateCompatibleDC(hdc_screen)
hbm = gdi32.CreateCompatibleBitmap(hdc_screen, w, h)
old = gdi32.SelectObject(hdc_mem, hbm)

ok = user32.PrintWindow(hwnd, hdc_mem, 2)
print(f"PrintWindow={ok}")

bmi = BITMAPINFOHEADER()
bmi.biSize = 40
bmi.biWidth = w
bmi.biHeight = -h
bmi.biPlanes = 1
bmi.biBitCount = 32
bmi.biCompression = 0

buf = ctypes.create_string_buffer(w * h * 4)
lines = gdi32.GetDIBits(hdc_mem, hbm, 0, h, buf, ctypes.byref(bmi), 0)
print(f"GetDIBits lines={lines}")

gdi32.SelectObject(hdc_mem, old)
gdi32.DeleteObject(hbm)
gdi32.DeleteDC(hdc_mem)
user32.ReleaseDC(0, hdc_screen)

raw = buf.raw
rows = []
for y in range(h):
    row = raw[y * w * 4:(y + 1) * w * 4]
    px = bytearray()
    for x in range(w):
        b, g, r, a = row[x * 4:x * 4 + 4]
        if a == 0:
            r = g = b = 255
        px += bytes((r, g, b))
    rows.append(bytes(px))


def chunk(tag, data):
    c = tag + data
    return struct.pack(">I", len(data)) + c + struct.pack(">I", zlib.crc32(c) & 0xFFFFFFFF)


png = b"\x89PNG\r\n\x1a\n"
png += chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0))
png += chunk(b"IDAT", zlib.compress(b"".join(b"\x00" + r for r in rows), 6))
png += chunk(b"IEND", b"")
open(out_path, "wb").write(png)
print(f"saved {out_path}")

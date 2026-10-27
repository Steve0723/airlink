import struct, zlib, os

def inside_rounded(x, y, w, r):
    if x < r and y < r:
        return (x - r + 0.5) ** 2 + (y - r + 0.5) ** 2 <= r * r
    if x >= w - r and y < r:
        return (x - (w - r - 0.5)) ** 2 + (y - r + 0.5) ** 2 <= r * r
    if x < r and y >= w - r:
        return (x - r + 0.5) ** 2 + (y - (w - r - 0.5)) ** 2 <= r * r
    if x >= w - r and y >= w - r:
        return (x - (w - r - 0.5)) ** 2 + (y - (w - r - 0.5)) ** 2 <= r * r
    return True

TEAL = (0x1D, 0x9E, 0x75)
WHITE = (0xFF, 0xFF, 0xFF)

def render(w):
    px = [[(0, 0, 0, 0) for _ in range(w)] for _ in range(w)]
    r = max(2, round(w * 0.22))
    for y in range(w):
        for x in range(w):
            if inside_rounded(x, y, w, r):
                px[y][x] = (TEAL[0], TEAL[1], TEAL[2], 255)

    bar = max(2, round(w * 0.14))
    top_y = round(w * 0.28)
    bot_y = round(w * 0.58)
    x0 = round(w * 0.20)
    x1 = round(w * 0.80)
    head = max(2, round(w * 0.20))

    for i in range(bar):
        for x in range(x0, x1 + 1):
            if 0 <= top_y + i < w:
                px[top_y + i][x] = (*WHITE, 255)
            if 0 <= bot_y + i < w:
                px[bot_y + i][x] = (*WHITE, 255)

    for i in range(head):
        span = head - i
        ty = top_y + i - (head - bar) // 2
        if 0 <= ty < w:
            for x in range(x1 - span + 1, x1 + 1):
                if 0 <= x < w:
                    px[ty][x] = (*WHITE, 255)
        by = bot_y + bar - 1 - i + (head - bar) // 2
        if 0 <= by < w:
            for x in range(x0, x0 + span):
                if 0 <= x < w:
                    px[by][x] = (*WHITE, 255)
    return px

def to_png(px, path):
    w = len(px)
    raw = b""
    for row in px:
        raw += b"\x00" + b"".join(struct.pack("BBBB", p[0], p[1], p[2], p[3]) for p in row)
    def chunk(tag, data):
        c = tag + data
        return struct.pack(">I", len(data)) + c + struct.pack(">I", zlib.crc32(c) & 0xFFFFFFFF)
    png = b"\x89PNG\r\n\x1a\n"
    png += chunk(b"IHDR", struct.pack(">IIBBBBB", w, w, 8, 6, 0, 0, 0))
    png += chunk(b"IDAT", zlib.compress(raw, 9))
    png += chunk(b"IEND", b"")
    open(path, "wb").write(png)

def to_bmp(px):
    w = len(px)
    hdr = struct.pack("<IiiHHIIiiII", 40, w, w * 2, 1, 32, 0, w * w * 4, 0, 0, 0, 0)
    xor = b""
    for y in range(w - 1, -1, -1):
        for x in range(w):
            r, g, b, a = px[y][x]
            xor += struct.pack("BBBB", b, g, r, a)
    and_row = ((w + 31) // 32) * 4
    and_mask = b"\x00" * (and_row * w)
    return hdr + xor + and_mask

sizes = [16, 32, 48]
images = [to_bmp(render(s)) for s in sizes]
out = struct.pack("<HHH", 0, 1, len(sizes))
offset = 6 + 16 * len(sizes)
entries = b""
for s, img in zip(sizes, images):
    entries += struct.pack("<BBBBHHII", s % 256, s % 256, 0, 0, 1, 32, len(img), offset)
    offset += len(img)
open("C:/Users/zwy07/WorkBuddy AI/code/airlink/server/icon.ico", "wb").write(out + entries + b"".join(images))
to_png(render(64), "C:/Users/zwy07/WorkBuddy AI/code/airlink/server/icon-preview.png")
print("ok")

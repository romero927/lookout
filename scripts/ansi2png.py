"""Render a 24-bit ANSI text frame to PNG. Usage: ansi2png.py in.ansi out.png"""
import re, sys
from PIL import Image, ImageDraw, ImageFont

src, dst = sys.argv[1], sys.argv[2]
text = open(src, encoding="utf-8").read()
BG, FG = (24, 26, 33), (205, 210, 220)
SIZE, PAD = 28, 36
reg = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf", SIZE)
bold = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf", SIZE)
cw = round(reg.getlength("M"))
ch = round(SIZE * 1.32)

rows = []
for line in text.split("\n"):
    cells, fg, bg, b = [], None, None, False
    for tok in re.split(r"(\x1b\[[0-9;]*m)", line):
        m = re.fullmatch(r"\x1b\[([0-9;]*)m", tok)
        if m:
            p = [int(x) for x in m.group(1).split(";") if x] or [0]
            i = 0
            while i < len(p):
                c = p[i]
                if c == 0: fg, bg, b = None, None, False
                elif c == 1: b = True
                elif c == 22: b = False
                elif c == 39: fg = None
                elif c == 49: bg = None
                elif c in (38, 48) and p[i+1] == 2:
                    col = tuple(p[i+2:i+5]); i += 4
                    if c == 38: fg = col
                    else: bg = col
                i += 1
            continue
        for chr_ in tok:
            cells.append((chr_, fg, bg, b))
    rows.append(cells)
while rows and not "".join(c[0] for c in rows[-1]).strip():
    rows.pop()
cols = max(len(r) for r in rows)
img = Image.new("RGB", (cols * cw + 2 * PAD, len(rows) * ch + 2 * PAD), BG)
d = ImageDraw.Draw(img)
for y, r in enumerate(rows):
    for x, (c, fg, bg, b) in enumerate(r):
        X, Y = PAD + x * cw, PAD + y * ch
        if bg:
            d.rectangle([X, Y, X + cw, Y + ch], fill=bg)
        if c == " ":
            continue
        if c in "█░━─│╭╮╰╯":  # box/block glyphs: draw to fill the cell exactly
            col = fg or FG
            if c == "█":
                d.rectangle([X, Y + 4, X + cw, Y + ch - 4], fill=col)
            elif c == "░":
                d.rectangle([X, Y + 4, X + cw, Y + ch - 4], fill=tuple(int(a * 0.25 + bb * 0.75) for a, bb in zip(col, BG)))
            elif c in "━─":
                t = 3 if c == "━" else 1
                d.rectangle([X, Y + ch // 2 - t // 2, X + cw, Y + ch // 2 + (t + 1) // 2 - 1 + (t == 1)], fill=col)
            elif c == "│":
                d.rectangle([X + cw // 2, Y, X + cw // 2 + 1, Y + ch], fill=col)
            else:
                mx, my = X + cw // 2, Y + ch // 2
                hx = (mx, X + cw) if c in "╭╰" else (X, mx)
                vy = (my, Y + ch) if c in "╭╮" else (Y, my)
                d.rectangle([hx[0], my, hx[1], my + 1], fill=col)
                d.rectangle([mx, vy[0], mx + 1, vy[1]], fill=col)
            continue
        d.text((X, Y + (ch - SIZE) // 2 - 2), c, font=bold if b else reg, fill=fg or FG)
img.save(dst, optimize=True)
print(img.size)

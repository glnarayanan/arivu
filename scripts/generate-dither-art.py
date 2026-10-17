#!/usr/bin/env python3
"""Generate the static ordered-dither art used by the Arivu web app.

A numpy port of the warped-noise Bayer shader used by the visual reference:
a warped fbm field is cut into four flat colours, and the steps between them
break into fine pixel patterns. The output is static (no WebGL, no pointer
glow) and deterministic. One image pixel is one dither cell; the CSS shows it
at two CSS pixels per cell.

    python3 scripts/generate-dither-art.py

Requires numpy and Pillow. Writes internal/app/web/art/auth-dither.webp.
"""
from pathlib import Path

import numpy as np
from PIL import Image

OUT = Path(__file__).resolve().parent.parent / "internal" / "app" / "web" / "art"


def oklch_to_rgb(l, c, h):
    a = c * np.cos(np.radians(h))
    b = c * np.sin(np.radians(h))
    l_ = (l + 0.3963377774 * a + 0.2158037573 * b) ** 3
    m_ = (l - 0.1055613458 * a - 0.0638541728 * b) ** 3
    s_ = (l - 0.0894841775 * a - 1.2914855480 * b) ** 3
    rgb = np.array(
        [
            4.0767416621 * l_ - 3.3077115913 * m_ + 0.2309699292 * s_,
            -1.2684380046 * l_ + 2.6097574011 * m_ - 0.3413193965 * s_,
            -0.0041960863 * l_ - 0.7034186147 * m_ + 1.7076147010 * s_,
        ]
    )
    rgb = np.clip(rgb, 0, 1)
    rgb = np.where(rgb <= 0.0031308, 12.92 * rgb, 1.055 * rgb ** (1 / 2.4) - 0.055)
    return tuple(int(round(v * 255)) for v in rgb)


def fract(x):
    return x - np.floor(x)


def smoothstep(a, b, x):
    t = np.clip((x - a) / (b - a), 0.0, 1.0)
    return t * t * (3.0 - 2.0 * t)


def hash2(x, y):
    return fract(np.sin(x * 127.1 + y * 311.7) * 43758.5453)


def noise(px, py):
    ix, iy = np.floor(px), np.floor(py)
    fx, fy = px - ix, py - iy
    ux = fx * fx * (3.0 - 2.0 * fx)
    uy = fy * fy * (3.0 - 2.0 * fy)
    a = hash2(ix, iy)
    b = hash2(ix + 1.0, iy)
    c = hash2(ix, iy + 1.0)
    d = hash2(ix + 1.0, iy + 1.0)
    return (a + (b - a) * ux) * (1.0 - uy) + (c + (d - c) * ux) * uy


def fbm(px, py):
    value = np.zeros_like(px)
    amplitude = 0.5
    for _ in range(4):
        value += amplitude * noise(px, py)
        px, py = (0.8 * px + 0.6 * py) * 2.03, (-0.6 * px + 0.8 * py) * 2.03
        amplitude *= 0.5
    return value


def bayer2(ax, ay):
    ax, ay = np.floor(ax), np.floor(ay)
    return fract(ax * 0.5 + ay * (ay * 0.75))


def bayer4(ax, ay):
    return bayer2(0.5 * ax, 0.5 * ay) * 0.25 + bayer2(ax, ay)


def bayer8(ax, ay):
    return bayer4(0.5 * ax, 0.5 * ay) * 0.25 + bayer2(ax, ay)


def render(name, width, height, colors, time=2.0):
    """Render width x height dither cells; colors run from the ground to the glow."""
    cy, cx = np.indices((height, width)).astype(np.float64)
    # GL's y axis points up, so flip rows to keep the glow orientation.
    gy = (height - 1) - cy
    centerx, centery = cx + 0.5, gy + 0.5
    unit = float(min(width, height))
    t = time * 0.08
    px, py = centerx / unit * 1.3, centery / unit * 1.3
    wx = fbm(px + t, py + t)
    wy = fbm(px + 4.1 - t, py + 1.7 - t)
    value = fbm(px + 1.6 * wx + t * 0.5, py + 1.6 * wy)
    u = centerx / width
    v = centery / height
    value = smoothstep(0.2, 0.85, value) * 0.8 + u * 0.25 - v * 0.15
    level = np.clip(np.floor(value * 3.0 + bayer8(cx, gy)), 0, 3).astype(int)
    palette = np.array(colors, dtype=np.uint8)
    OUT.mkdir(parents=True, exist_ok=True)
    Image.fromarray(palette[level]).save(OUT / name, "WEBP", lossless=True, quality=100, method=6)


if __name__ == "__main__":
    # Dark panel: charcoal ground glowing through deep coral to light coral.
    render(
        "auth-dither.webp",
        800,
        800,
        [
            oklch_to_rgb(0.2645, 0.0, 89.88),
            oklch_to_rgb(0.3502, 0.102, 35.05),
            oklch_to_rgb(0.5596, 0.174, 34.81),
            oklch_to_rgb(0.7767, 0.096, 34.47),
        ],
    )

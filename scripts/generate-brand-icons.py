#!/usr/bin/env python3
"""Regenerate Deepzora's app artwork from the three desktop/branding masters.

Requires Pillow. Run from any directory with `python3 scripts/generate-brand-icons.py`.
SVG files wrap the actual selected raster artwork with no font dependencies.
"""
from base64 import b64encode
from io import BytesIO
from pathlib import Path
import struct

from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parents[1]
DESKTOP = ROOT / "desktop"
BUILD = DESKTOP / "build"
FRONTEND = DESKTOP / "frontend"
SOURCE = DESKTOP / "branding/logo-ribbon.png"
ICON_SOURCE = DESKTOP / "branding/appicon-ceramic.png"
WORDMARK_SOURCE = DESKTOP / "branding/wordmark-humanist.png"
LANCZOS = Image.Resampling.LANCZOS


def png_bytes(image):
    image = image.copy()
    # Resampling can leave a faint alpha fringe at tiny icon corners. Keep that
    # imperceptible fringe truly transparent in every exported representation.
    image.putalpha(image.getchannel("A").point(lambda value: value if value >= 4 else 0))
    stream = BytesIO()
    image.save(stream, format="PNG", optimize=True)
    return stream.getvalue()


def data_uri(image):
    return "data:image/png;base64," + b64encode(png_bytes(image)).decode("ascii")


def canvas(artwork, size, inset):
    result = Image.new("RGBA", (size, size))
    scaled = artwork.copy()
    scaled.thumbnail((size - inset * 2, size - inset * 2), LANCZOS)
    result.alpha_composite(scaled, ((size - scaled.width) // 2, (size - scaled.height) // 2))
    return result


def icon_artwork():
    image = Image.open(ICON_SOURCE).convert("RGBA")
    # The selected concept already includes its ceramic plate. Normalize that
    # plate once instead of placing a padded transparent symbol on another tile.
    # Clip the generated exterior fringe to a clean, antialiased native contour.
    inset = round(image.width * 0.08)
    image = image.crop((inset, inset, image.width - inset, image.height - inset))
    mask = Image.new("L", (image.width * 3, image.height * 3))
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, mask.width - 1, mask.height - 1),
                                          radius=round(mask.width * 0.24), fill=255)
    image.putalpha(mask.resize(image.size, LANCZOS))
    return image


def svg_image(image):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{image.width}" height="{image.height}" '
            f'viewBox="0 0 {image.width} {image.height}" role="img" aria-label="Deepzora">\n'
            f'  <image width="{image.width}" height="{image.height}" href="{data_uri(image)}"/>\n</svg>\n')


def write_ico(path, images):
    chunks = [png_bytes(image) for image in images]
    offset = 6 + 16 * len(images)
    entries = []
    for image, chunk in zip(images, chunks):
        size = image.width
        entries.append(struct.pack("<BBBBHHII", size % 256, size % 256, 0, 0, 1, 32, len(chunk), offset))
        offset += len(chunk)
    path.write_bytes(struct.pack("<HHH", 0, 1, len(images)) + b"".join(entries) + b"".join(chunks))


def main():
    artwork = Image.open(SOURCE).convert("RGBA")
    # Remove barely visible alpha fringe before determining the fitting bounds.
    artwork.putalpha(artwork.getchannel("A").point(lambda value: value if value >= 8 else 0))
    artwork = artwork.crop(artwork.getbbox())
    symbol = artwork.copy()
    symbol.thumbnail((384, 384), LANCZOS)
    (FRONTEND / "src/assets/logo-symbol.svg").write_text(svg_image(symbol))

    wordmark_image = Image.open(WORDMARK_SOURCE).convert("RGBA")
    wordmark_image.putalpha(wordmark_image.getchannel("A").point(lambda value: value if value >= 8 else 0))
    wordmark_image = wordmark_image.crop(wordmark_image.getbbox())
    wordmark_image.thumbnail((1536, 512), LANCZOS)
    wordmark = svg_image(wordmark_image)
    (FRONTEND / "src/assets/logo-wordmark.svg").write_text(wordmark)
    (ROOT / "internal/serve/logo-wordmark.svg").write_text(wordmark)

    plate = icon_artwork()
    app = canvas(plate, 1024, 24)
    mac = canvas(plate, 1024, 100)
    (BUILD / "appicon.png").write_bytes(png_bytes(app))
    (BUILD / "darwin/appicon.png").write_bytes(png_bytes(mac))
    (BUILD / "appicon.svg").write_text(svg_image(app))
    (BUILD / "darwin/appicon.svg").write_text(svg_image(mac))

    chunks = []
    # PNG-backed ICNS representations, including Retina sizes.
    for tag, size in [("icp4", 16), ("icp5", 32), ("icp6", 64), ("ic07", 128),
                      ("ic08", 256), ("ic09", 512), ("ic10", 1024), ("ic11", 32),
                      ("ic12", 64), ("ic13", 256), ("ic14", 512)]:
        payload = png_bytes(mac.resize((size, size), LANCZOS))
        chunks.append(tag.encode("ascii") + struct.pack(">I", len(payload) + 8) + payload)
    body = b"".join(chunks)
    (BUILD / "darwin/icon.icns").write_bytes(b"icns" + struct.pack(">I", len(body) + 8) + body)
    write_ico(BUILD / "windows/icon.ico", [app.resize((size, size), LANCZOS) for size in [16, 24, 32, 48, 64, 128, 256]])

    hicolor = BUILD / "linux/icons/hicolor"
    for size in [16, 24, 32, 48, 64, 128, 256, 512]:
        folder = hicolor / f"{size}x{size}/apps"
        (folder / "deepzora-desktop.png").write_bytes(png_bytes(app.resize((size, size), LANCZOS)))
        # The current packager and .desktop entry use the Deepzora name.
        (folder / "nix-desktop.png").unlink(missing_ok=True)
    (hicolor / "scalable/apps/deepzora-desktop.svg").write_text(svg_image(app))

    # Template icons use the ribbon's silhouette with its ivory fold as a
    # negative-space counter. macOS supplies the actual menu-bar color.
    mono = Image.new("RGBA", artwork.size)
    mono.putdata([(0, 0, 0, 0 if r > 190 and g > 190 and b > 170 else alpha)
                  for r, g, b, alpha in artwork.getdata()])
    for scale in [1, 2]:
        tray = canvas(mono, 18 * scale, scale)
        name = "trayTemplate.png" if scale == 1 else "trayTemplate@2x.png"
        (BUILD / name).write_bytes(png_bytes(tray))

    (FRONTEND / "public/favicon.svg").write_text(svg_image(app.resize((128, 128), LANCZOS)))
    (FRONTEND / "public/favicon-32x32.png").write_bytes(png_bytes(app.resize((32, 32), LANCZOS)))
    write_ico(FRONTEND / "public/favicon.ico", [app.resize((size, size), LANCZOS) for size in [16, 32, 48]])
    print("Generated shared symbols, wordmarks, macOS/Windows/Linux icons, tray templates and favicons.")


if __name__ == "__main__":
    main()

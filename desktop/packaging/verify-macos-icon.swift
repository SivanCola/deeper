// Native acceptance for the packaged app icon, including the system's mask
// and compatibility tile. Run on macOS, after extracting the release archive:
// swift desktop/packaging/verify-macos-icon.swift <Deepzora.app> <output-dir>
import AppKit

func fail(_ message: String) -> Never {
    fputs("FAIL  " + message + "\n", stderr)
    exit(1)
}

guard CommandLine.arguments.count == 3 else {
    fail("usage: verify-macos-icon.swift <Deepzora.app> <output-dir>")
}
let bundleURL = URL(fileURLWithPath: CommandLine.arguments[1]).standardizedFileURL
let outputURL = URL(fileURLWithPath: CommandLine.arguments[2], isDirectory: true)
guard let bundle = Bundle(url: bundleURL),
      bundle.bundleIdentifier == "com.deepzora.desktop",
      let iconFile = bundle.object(forInfoDictionaryKey: "CFBundleIconFile") as? String else {
    fail("expected a packaged com.deepzora.desktop app with CFBundleIconFile")
}
let iconName = iconFile.hasSuffix(".icns") ? iconFile : iconFile + ".icns"
guard FileManager.default.fileExists(atPath: bundleURL.appendingPathComponent("Contents/Resources/" + iconName).path) else {
    fail("bundle icon is missing: " + iconName)
}
try FileManager.default.createDirectory(at: outputURL, withIntermediateDirectories: true)
var failures = 0
for size in [32, 64, 128, 256, 512, 1024] {
    // Match a Retina window's backing scale so IconServices selects the same
    // representation as Finder and Dock; 1024px is 512pt at this scale.
    let logicalSize = size / 2
    let icon = NSWorkspace.shared.icon(forFile: bundleURL.path)
    icon.size = NSSize(width: logicalSize, height: logicalSize)
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    bitmap.size = NSSize(width: size, height: size)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
    NSGraphicsContext.current!.cgContext.scaleBy(x: 2, y: 2)
    icon.draw(in: NSRect(x: 0, y: 0, width: logicalSize, height: logicalSize))
    NSGraphicsContext.restoreGraphicsState()
    let imageURL = outputURL.appendingPathComponent("system-icon-\(size).png")
    try bitmap.representation(using: .png, properties: [:])!.write(to: imageURL)
    // A compatibility tile replaces these background samples with neutral
    // gray. Allow native lighting and color management while requiring orange
    // at all four edges, outside the shrunken artwork of a rejected icon.
    let edge = Int(Double(size) * 0.16)
    var valid = true
    for (x, y) in [(edge, size / 2), (size - 1 - edge, size / 2),
                   (size / 2, edge), (size / 2, size - 1 - edge)] {
        guard let c = bitmap.colorAt(x: x, y: y)?.usingColorSpace(.sRGB),
              c.alphaComponent > 0.9, c.redComponent > 0.65,
              c.redComponent - c.greenComponent > 0.20,
              c.greenComponent - c.blueComponent > 0.10 else {
            fputs("FAIL  gray compatibility tile or missing orange background at \(size)px (\(x), \(y)); inspect \(imageURL.path)\n", stderr)
            valid = false
            break
        }
    }
    if valid {
        print("PASS  native macOS icon \(logicalSize)pt @2x (\(size)px): orange background reaches every edge; \(imageURL.path)")
    } else {
        failures += 1
    }
}
if failures > 0 { exit(1) }

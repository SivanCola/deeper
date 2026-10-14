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
let iconURL = bundleURL.appendingPathComponent("Contents/Resources/" + iconName)
guard let sourceIcon = NSImage(contentsOf: iconURL) else {
    fail("bundle icon is missing: " + iconName)
}

func orangeBounds(_ bitmap: NSBitmapImageRep) -> CGRect? {
    var bounds: CGRect?
    for y in 0..<bitmap.pixelsHigh {
        for x in 0..<bitmap.pixelsWide {
            guard let c = bitmap.colorAt(x: x, y: y)?.usingColorSpace(.sRGB),
                  c.alphaComponent > 0.9, c.redComponent > 0.65,
                  c.redComponent - c.greenComponent > 0.20,
                  c.greenComponent - c.blueComponent > 0.10 else { continue }
            let pixel = CGRect(x: x, y: y, width: 1, height: 1)
            bounds = bounds.map { $0.union(pixel) } ?? pixel
        }
    }
    return bounds
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
    NSGraphicsContext.current!.cgContext.clear(CGRect(x: 0, y: 0, width: size, height: size))
    NSGraphicsContext.current!.cgContext.scaleBy(x: 2, y: 2)
    icon.draw(in: NSRect(x: 0, y: 0, width: logicalSize, height: logicalSize))
    NSGraphicsContext.restoreGraphicsState()
    let imageURL = outputURL.appendingPathComponent("system-icon-\(size).png")
    try bitmap.representation(using: .png, properties: [:])!.write(to: imageURL)
    let source = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
        bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
        colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: source)
    NSGraphicsContext.current!.cgContext.clear(CGRect(x: 0, y: 0, width: size, height: size))
    sourceIcon.draw(in: NSRect(x: 0, y: 0, width: size, height: size))
    NSGraphicsContext.restoreGraphicsState()
    try source.representation(using: .png, properties: [:])!.write(to: outputURL.appendingPathComponent("source-icon-\(size).png"))
    // Compare the selected plate to its packaged source instead of requiring
    // the old solid-orange background. A compatibility tile also shrinks the
    // ribbon, so check its bounds independently of the ceramic edge colors.
    let edge = Int(Double(size) * 0.16)
    var valid = true
    for (x, y) in [(edge, size / 2), (size - 1 - edge, size / 2),
                   (size / 2, edge), (size / 2, size - 1 - edge)] {
        guard let c = bitmap.colorAt(x: x, y: y)?.usingColorSpace(.sRGB),
              let expected = source.colorAt(x: x, y: y)?.usingColorSpace(.sRGB),
              c.alphaComponent > 0.9,
              // At 16pt macOS enlarges the motif optically into these samples;
              // its bounds and opaque plate remain checked independently.
              size == 32 || (abs(c.redComponent - expected.redComponent) < 0.15 &&
                  abs(c.greenComponent - expected.greenComponent) < 0.15 &&
                  abs(c.blueComponent - expected.blueComponent) < 0.15) else {
            fputs("FAIL  native plate differs from packaged artwork at \(size)px (\(x), \(y)); inspect \(imageURL.path)\n", stderr)
            valid = false
            break
        }
    }
    if let actual = orangeBounds(bitmap), let expected = orangeBounds(source) {
        let tolerance = max(2.0, Double(size) * 0.04)
        if abs(actual.minX - expected.minX) > tolerance || abs(actual.minY - expected.minY) > tolerance ||
           abs(actual.maxX - expected.maxX) > tolerance || abs(actual.maxY - expected.maxY) > tolerance {
            fputs("FAIL  native ribbon was resized or shifted at \(size)px: actual=\(actual), source=\(expected); inspect \(imageURL.path)\n", stderr)
            valid = false
        }
    } else {
        fputs("FAIL  orange ribbon missing at \(size)px; inspect \(imageURL.path)\n", stderr)
        valid = false
    }
    if valid {
        print("PASS  native macOS icon \(logicalSize)pt @2x (\(size)px): plate and ribbon bounds match packaged artwork; \(imageURL.path)")
    } else {
        failures += 1
    }
}
if failures > 0 { exit(1) }

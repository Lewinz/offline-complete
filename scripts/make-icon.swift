import AppKit
import Foundation

let size = 1024
let image = NSImage(size: NSSize(width: size, height: size))
image.lockFocus()
NSColor.clear.setFill()
NSBezierPath(rect: NSRect(x: 0, y: 0, width: size, height: size)).fill()

let inset = CGFloat(48)
let background = NSBezierPath(
    roundedRect: NSRect(x: inset, y: inset, width: CGFloat(size) - inset * 2, height: CGFloat(size) - inset * 2),
    xRadius: 208,
    yRadius: 208
)
NSColor(srgbRed: 0.42, green: 0.0, blue: 0.91, alpha: 1).setFill()
background.fill()

let attributes: [NSAttributedString.Key: Any] = [
    .font: NSFont.systemFont(ofSize: 420, weight: .bold),
    .foregroundColor: NSColor.white,
]
let text = "OC" as NSString
let textSize = text.size(withAttributes: attributes)
text.draw(
    at: NSPoint(
        x: (CGFloat(size) - textSize.width) / 2,
        y: (CGFloat(size) - textSize.height) / 2
    ),
    withAttributes: attributes
)
image.unlockFocus()

guard let tiff = image.tiffRepresentation,
      let bitmap = NSBitmapImageRep(data: tiff),
      let png = bitmap.representation(using: .png, properties: [:]) else {
    fputs("make-icon: could not render PNG\n", stderr)
    exit(1)
}
try png.write(to: URL(fileURLWithPath: CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "AppIcon-1024.png"))

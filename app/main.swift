import AppKit
import ApplicationServices
import CoreGraphics
import Darwin
import Foundation
import IOKit.hid

func writeError(_ message: String) {
    FileHandle.standardError.write(Data((message + "\n").utf8))
}

// MARK: - Wire protocol

struct Item: Codable {
    let name: String
    let description: String?
    let path: String?
}

struct WindowMessage: Codable {
    let type: String
    let version: UInt64?
    let offer: UInt64?
    let selected: Int?
    let index: Int?
    let items: [Item]?
    let ok: Bool?
    let error: String?
}

struct Handshake: Codable {
    let version: Int
    let session_id: String
}

enum AgentError: Error, CustomStringConvertible {
    case socket(String)
    case handshake(String)
    case protocolViolation(String)

    var description: String {
        switch self {
        case .socket(let message): return message
        case .handshake(let message): return message
        case .protocolViolation(let message): return message
        }
    }
}

final class AgentConnection {
    private let fd: Int32
    private let session: String
    private var buffer = Data()
    private var source: DispatchSourceRead?
    private let readQueue = DispatchQueue(label: "offline-complete.read")
    private let writeLock = NSLock()
    private var closed = false

    /// Check mode runs without an AppKit main loop, so callbacks must be
    /// delivered on the read queue instead of the main queue.
    var deliverOnMain = true

    var onMessage: ((WindowMessage) -> Void)?
    var onClose: (() -> Void)?

    init(path: String, session: String, timeout: TimeInterval = 5) throws {
        self.session = session
        let descriptor = socket(AF_UNIX, SOCK_STREAM, 0)
        guard descriptor >= 0 else {
            throw AgentError.socket("socket: \(String(cString: strerror(errno)))")
        }
        var noSignal: Int32 = 1
        setsockopt(descriptor, SOL_SOCKET, SO_NOSIGPIPE, &noSignal, socklen_t(MemoryLayout<Int32>.size))

        var address = sockaddr_un()
        address.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8)
        guard bytes.count < 104 else {
            Darwin.close(descriptor)
            throw AgentError.socket("socket path too long")
        }
        withUnsafeMutableBytes(of: &address.sun_path) { raw in
            raw.copyBytes(from: bytes)
        }
        let result = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                connect(descriptor, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard result == 0 else {
            let message = String(cString: strerror(errno))
            Darwin.close(descriptor)
            throw AgentError.socket("connect: \(message)")
        }

        var uid: uid_t = 0
        var gid: gid_t = 0
        guard getpeereid(descriptor, &uid, &gid) == 0, uid == geteuid() else {
            Darwin.close(descriptor)
            throw AgentError.socket("agent is not owned by the current user")
        }

        self.fd = descriptor
        var timeoutValue = timeval(tv_sec: Int(timeout), tv_usec: 0)
        setsockopt(descriptor, SOL_SOCKET, SO_RCVTIMEO, &timeoutValue, socklen_t(MemoryLayout<timeval>.size))
        setsockopt(descriptor, SOL_SOCKET, SO_SNDTIMEO, &timeoutValue, socklen_t(MemoryLayout<timeval>.size))
        try handshake()
        _ = fcntl(descriptor, F_SETFL, O_NONBLOCK)
    }

    private func handshake() throws {
        let request = Handshake(version: 1, session_id: session)
        let payload = try JSONEncoder().encode(request)
        try writeFrame(payload)
        let header = try readExact(4)
        let length = header.withUnsafeBytes { $0.load(as: UInt32.self).bigEndian }
        guard length > 0, length <= 64 * 1024 else {
            throw AgentError.handshake("invalid handshake length")
        }
        let body = try readExact(Int(length))
        let response = try JSONDecoder().decode(Handshake.self, from: body)
        guard response.version == 1, response.session_id == session else {
            throw AgentError.handshake("handshake mismatch")
        }
    }

    private func writeFrame(_ payload: Data) throws {
        var length = UInt32(payload.count).bigEndian
        var frame = Data(bytes: &length, count: 4)
        frame.append(payload)
        try writeAll(frame)
    }

    private func readExact(_ count: Int) throws -> Data {
        var data = Data(count: count)
        var offset = 0
        while offset < count {
            let received = data.withUnsafeMutableBytes { raw -> Int in
                guard let base = raw.baseAddress else { return -1 }
                return Darwin.recv(fd, base.advanced(by: offset), count - offset, 0)
            }
            if received <= 0 {
                throw AgentError.handshake("connection closed during handshake")
            }
            offset += received
        }
        return data
    }

    private func writeAll(_ data: Data) throws {
        var offset = 0
        while offset < data.count {
            let sent = data.withUnsafeBytes { raw -> Int in
                guard let base = raw.baseAddress else { return -1 }
                return Darwin.send(fd, base.advanced(by: offset), data.count - offset, 0)
            }
            if sent <= 0 {
                if errno == EINTR { continue }
                throw AgentError.socket("send: \(String(cString: strerror(errno)))")
            }
            offset += sent
        }
    }

    func start() {
        let source = DispatchSource.makeReadSource(fileDescriptor: fd, queue: readQueue)
        source.setEventHandler { [weak self] in
            self?.drain()
        }
        source.setCancelHandler { [fd] in
            Darwin.close(fd)
        }
        self.source = source
        source.resume()
    }

    private func drain() {
        var chunk = [UInt8](repeating: 0, count: 4096)
        while !closed {
            let received = chunk.withUnsafeMutableBytes { raw -> Int in
                guard let base = raw.baseAddress else { return -1 }
                return Darwin.recv(fd, base, raw.count, 0)
            }
            if received > 0 {
                buffer.append(chunk, count: received)
                if buffer.count > 64 * 1024 {
                    fail("message too large")
                    return
                }
                parseLines()
            } else if received == 0 {
                fail("agent closed the connection")
                return
            } else if errno == EAGAIN || errno == EWOULDBLOCK {
                return
            } else if errno == EINTR {
                continue
            } else {
                fail("recv: \(String(cString: strerror(errno)))")
                return
            }
        }
    }

    private func deliver(_ block: @escaping () -> Void) {
        if deliverOnMain {
            DispatchQueue.main.async(execute: block)
        } else {
            block()
        }
    }

    private func parseLines() {
        while let newline = buffer.firstIndex(of: 0x0a) {
            let line = buffer[buffer.startIndex..<newline]
            buffer.removeSubrange(buffer.startIndex...newline)
            guard !line.isEmpty else { continue }
            do {
                let message = try JSONDecoder().decode(WindowMessage.self, from: Data(line))
                deliver { self.onMessage?(message) }
            } catch {
                fail("invalid agent message")
                return
            }
        }
    }

    func send(_ object: [String: Any]) {
        guard !closed else { return }
        do {
            let data = try JSONSerialization.data(withJSONObject: object)
            var payload = data
            payload.append(0x0a)
            writeLock.lock()
            defer { writeLock.unlock() }
            try writeAll(payload)
        } catch {
            fail("send: \(error)")
        }
    }

    private func fail(_ reason: String) {
        if closed { return }
        closed = true
        source?.cancel()
        writeError("oc-window: \(reason)")
        deliver { self.onClose?() }
    }

    func close() {
        if closed { return }
        closed = true
        source?.cancel()
    }
}

// MARK: - Caret position

final class CaretLocator {
    private let application: AXUIElement
    private let trusted: Bool

    init(pid: pid_t) {
        application = AXUIElementCreateApplication(pid)
        // TCC's trust check can take tens of milliseconds. Checking it on every
        // popup added a visible delay even when the permission was denied.
        trusted = AXIsProcessTrusted()
    }

    /// Returns the caret rectangle in Cocoa screen coordinates, or nil when
    /// the focused element does not expose an accessibility text range.
    func rect() -> NSRect? {
        guard trusted else { return nil }
        var focusedValue: CFTypeRef?
        guard AXUIElementCopyAttributeValue(application, kAXFocusedUIElementAttribute as CFString, &focusedValue) == .success,
              let focusedValue else {
            return nil
        }
        let element = focusedValue as! AXUIElement

        var rangeValue: CFTypeRef?
        guard AXUIElementCopyAttributeValue(element, kAXSelectedTextRangeAttribute as CFString, &rangeValue) == .success,
              let rangeValue else {
            return nil
        }
        let range = rangeValue as! AXValue
        var selected = CFRange()
        guard AXValueGetValue(range, .cfRange, &selected) else { return nil }

        var boundsValue: CFTypeRef?
        guard AXUIElementCopyParameterizedAttributeValue(
            element,
            kAXBoundsForRangeParameterizedAttribute as CFString,
            range,
            &boundsValue
        ) == .success, let boundsValue else {
            return nil
        }
        let bounds = boundsValue as! AXValue
        var axRect = CGRect.zero
        guard AXValueGetValue(bounds, .cgRect, &axRect), axRect.width.isFinite, axRect.height.isFinite else {
            return nil
        }

        // Accessibility uses a top-left origin; AppKit uses bottom-left.
        let mainHeight = NSScreen.screens.first?.frame.maxY ?? axRect.maxY
        let rect = NSRect(
            x: axRect.minX,
            y: mainHeight - axRect.maxY,
            width: max(axRect.width, 1),
            height: max(axRect.height, 1)
        )
        let visible = NSScreen.screens.contains { $0.frame.intersects(rect) }
        return visible ? rect : nil
    }
}

func requestAccessibility() -> Bool {
    let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary
    return AXIsProcessTrustedWithOptions(options)
}

// MARK: - Candidate panel

final class CandidateView: NSView {
    var items: [Item] = [] {
        didSet {
            firstVisibleRow = 0
            needsDisplay = true
        }
    }
    var onSelect: ((Int) -> Void)?
    var onDismiss: (() -> Void)?
    var selected = 0 {
        didSet {
            updateViewport()
            needsDisplay = true
        }
    }
    private var hovered = -1
    private var firstVisibleRow = 0
    private lazy var nameFont = NSFont(name: "Monaco", size: 12.8) ?? NSFont.monospacedSystemFont(ofSize: 12.8, weight: .regular)
    private lazy var selectedFont = NSFont(name: "Monaco-Bold", size: 12.8) ?? NSFont.monospacedSystemFont(ofSize: 12.8, weight: .semibold)
    private lazy var descriptionFont: NSFont = {
        let base = NSFont.systemFont(ofSize: 11.5)
        let italic = base.fontDescriptor.withSymbolicTraits(.italic)
        return NSFont(descriptor: italic, size: 11.5) ?? base
    }()
    private lazy var shortcutFont = NSFont.monospacedSystemFont(ofSize: 10.5, weight: .medium)
    private var symbolCache: [String: NSImage] = [:]

    let rowHeight: CGFloat = 22
    let verticalPadding: CGFloat = 4
    let horizontalPadding: CGFloat = 9
    let maximumRows = 7
    let minimumPanelWidth: CGFloat = 400
    let maximumPanelWidth: CGFloat = 560
    let descriptionHeight: CGFloat = 22
    let iconSize: CGFloat = 14
    let iconLeading: CGFloat = 10
    let textLeading: CGFloat = 32
    let shortcutWidth: CGFloat = 30

    override var isFlipped: Bool { true }
    override var acceptsFirstResponder: Bool { false }

    struct Palette {
        let background: NSColor
        let text: NSColor
        let descriptionText: NSColor
        let descriptionBackground: NSColor
        let border: NSColor
        let selectedBackground: NSColor
        let selectedText: NSColor
        let selectedIcon: NSColor
        let icon: NSColor
        let shortcut: NSColor

        static let light = Palette(
            background: NSColor(srgbRed: 254 / 255, green: 254 / 255, blue: 254 / 255, alpha: 1),
            text: NSColor(srgbRed: 7 / 255, green: 7 / 255, blue: 7 / 255, alpha: 1),
            descriptionText: NSColor(srgbRed: 70 / 255, green: 70 / 255, blue: 75 / 255, alpha: 1),
            descriptionBackground: NSColor(srgbRed: 244 / 255, green: 244 / 255, blue: 246 / 255, alpha: 1),
            border: NSColor(srgbRed: 199 / 255, green: 199 / 255, blue: 199 / 255, alpha: 1),
            selectedBackground: NSColor(srgbRed: 41 / 255, green: 105 / 255, blue: 218 / 255, alpha: 1),
            selectedText: NSColor(srgbRed: 253 / 255, green: 253 / 255, blue: 253 / 255, alpha: 1),
            selectedIcon: NSColor(srgbRed: 210 / 255, green: 230 / 255, blue: 255 / 255, alpha: 1),
            icon: NSColor(srgbRed: 41 / 255, green: 105 / 255, blue: 218 / 255, alpha: 1),
            shortcut: NSColor(srgbRed: 120 / 255, green: 120 / 255, blue: 126 / 255, alpha: 1)
        )

        static let dark = Palette(
            background: NSColor(srgbRed: 45 / 255, green: 45 / 255, blue: 45 / 255, alpha: 1),
            text: NSColor(srgbRed: 184 / 255, green: 184 / 255, blue: 184 / 255, alpha: 1),
            descriptionText: NSColor(srgbRed: 174 / 255, green: 174 / 255, blue: 178 / 255, alpha: 1),
            descriptionBackground: NSColor(srgbRed: 54 / 255, green: 54 / 255, blue: 54 / 255, alpha: 1),
            border: NSColor(srgbRed: 78 / 255, green: 78 / 255, blue: 78 / 255, alpha: 1),
            selectedBackground: NSColor(srgbRed: 35 / 255, green: 111 / 255, blue: 224 / 255, alpha: 1),
            selectedText: NSColor(srgbRed: 253 / 255, green: 253 / 255, blue: 253 / 255, alpha: 1),
            selectedIcon: NSColor(srgbRed: 190 / 255, green: 217 / 255, blue: 255 / 255, alpha: 1),
            icon: NSColor(srgbRed: 96 / 255, green: 165 / 255, blue: 250 / 255, alpha: 1),
            shortcut: NSColor(srgbRed: 150 / 255, green: 150 / 255, blue: 154 / 255, alpha: 1)
        )
    }

    private var palette: Palette {
        // Kiro CLI's autocomplete window is dark even when the terminal is
        // running under a light macOS appearance. Keep the candidate list on
        // the same palette so the two integrations look consistent.
        .dark
    }

    private var widthCache: [String: CGFloat] = [:]

    private func textWidth(_ text: String, font: NSFont) -> CGFloat {
        let size = String(format: "%.1f", font.pointSize)
        let key = "\(font.fontName)|\(size)|\(text)"
        if let cached = widthCache[key] {
            return cached
        }
        let width = (text as NSString).size(withAttributes: [.font: font]).width
        if widthCache.count > 1024 {
            widthCache.removeAll(keepingCapacity: true)
        }
        widthCache[key] = width
        return width
    }

    func warmup() {
        _ = nameFont
        _ = selectedFont
        _ = descriptionFont
        _ = shortcutFont
        _ = textWidth("M", font: nameFont)
        _ = textWidth("M", font: descriptionFont)
    }

    func preferredSize(count: Int) -> NSSize {
        let rows = min(max(count, 1), maximumRows)
        let visible = items.prefix(maximumRows)
        let nameWidth = visible.map { textWidth($0.name, font: nameFont) }.max() ?? 0
        let descriptionWidth = visible
            .compactMap { $0.description }
            .map { textWidth($0, font: descriptionFont) }
            .max() ?? 0
        let contentWidth = max(nameWidth, descriptionWidth)
        let width = min(
            maximumPanelWidth,
            max(minimumPanelWidth, horizontalPadding * 2 + textLeading + contentWidth + shortcutWidth)
        )
        return NSSize(
            width: width,
            height: CGFloat(rows) * rowHeight + verticalPadding * 2 + descriptionHeight
        )
    }

    private var selectedDescription: String? {
        guard items.indices.contains(selected) else { return nil }
        if let path = items[selected].path?.trimmingCharacters(in: .whitespacesAndNewlines), !path.isEmpty {
            return path
        }
        let value = items[selected].description?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return value.isEmpty ? nil : value
    }

    private var firstVisible: Int { firstVisibleRow }

    private func updateViewport() {
        let rows = min(items.count, maximumRows)
        guard rows > 0 else {
            firstVisibleRow = 0
            return
        }
        // Keep the viewport stable while the selection moves inside it. Only
        // scroll when the selection reaches the top or bottom edge.
        if selected < firstVisibleRow {
            firstVisibleRow = selected
        } else if selected >= firstVisibleRow + rows {
            firstVisibleRow = selected - rows + 1
        }
        let maxOffset = max(0, items.count - rows)
        firstVisibleRow = min(max(0, firstVisibleRow), maxOffset)
    }

    override func draw(_ dirtyRect: NSRect) {
        let colors = palette
        let outer = bounds.insetBy(dx: 0.5, dy: 0.5)
        let background = NSBezierPath(roundedRect: outer, xRadius: 7, yRadius: 7)
        colors.background.setFill()
        background.fill()
        colors.border.setStroke()
        background.lineWidth = 1
        background.stroke()

        let nameStyle = NSMutableParagraphStyle()
        nameStyle.lineBreakMode = .byTruncatingTail

        let visible = min(items.count, maximumRows)
        let first = firstVisible
        for index in first..<(first + visible) {
            let position = index - first
            let row = NSRect(
                x: 0,
                y: verticalPadding + CGFloat(position) * rowHeight,
                width: bounds.width,
                height: rowHeight
            )
            if index == selected {
                let highlight = NSBezierPath(roundedRect: row.insetBy(dx: 4, dy: 0), xRadius: 4, yRadius: 4)
                colors.selectedBackground.setFill()
                highlight.fill()
            } else if index == hovered {
                let highlight = NSBezierPath(roundedRect: row.insetBy(dx: 4, dy: 0), xRadius: 4, yRadius: 4)
                colors.selectedBackground.withAlphaComponent(0.28).setFill()
                highlight.fill()
            }

            let iconColor = index == selected ? colors.selectedIcon : colors.icon
            if let image = symbolImage(for: items[index], color: iconColor) {
                let iconRect = NSRect(
                    x: iconLeading,
                    y: row.minY + (rowHeight - iconSize) / 2,
                    width: iconSize,
                    height: iconSize
                )
                image.draw(
                    in: iconRect,
                    from: .zero,
                    operation: .sourceOver,
                    fraction: 1,
                    respectFlipped: true,
                    hints: nil
                )
            }

            let nameAttributes: [NSAttributedString.Key: Any] = [
                .font: index == selected ? selectedFont : nameFont,
                .foregroundColor: index == selected ? colors.selectedText : colors.text,
                .paragraphStyle: nameStyle,
            ]
            let nameRect = NSRect(
                x: textLeading,
                y: row.minY + 4,
                width: row.maxX - textLeading - horizontalPadding,
                height: 15
            )
            (items[index].name as NSString).draw(in: nameRect, withAttributes: nameAttributes)
        }

        let dividerY = verticalPadding + CGFloat(visible) * rowHeight
        NSGraphicsContext.saveGraphicsState()
        background.addClip()
        colors.descriptionBackground.setFill()
        NSRect(x: outer.minX, y: dividerY, width: outer.width, height: bounds.height - dividerY - 0.5).fill()
        let divider = NSBezierPath()
        divider.move(to: NSPoint(x: outer.minX, y: dividerY + 0.5))
        divider.line(to: NSPoint(x: outer.maxX, y: dividerY + 0.5))
        colors.border.setStroke()
        divider.lineWidth = 1
        divider.stroke()
        NSGraphicsContext.restoreGraphicsState()

        let descriptionStyle = NSMutableParagraphStyle()
        descriptionStyle.lineBreakMode = .byTruncatingTail
        let descriptionAttributes: [NSAttributedString.Key: Any] = [
            .font: descriptionFont,
            .foregroundColor: colors.descriptionText,
            .paragraphStyle: descriptionStyle,
        ]
        if let description = selectedDescription {
            let descriptionRect = NSRect(
                x: horizontalPadding,
                y: dividerY + 4,
                width: bounds.width - horizontalPadding * 2 - shortcutWidth - 6,
                height: 16
            )
            (description as NSString).draw(in: descriptionRect, withAttributes: descriptionAttributes)
        }

        let shortcutStyle = NSMutableParagraphStyle()
        shortcutStyle.alignment = .right
        let shortcutAttributes: [NSAttributedString.Key: Any] = [
            .font: shortcutFont,
            .foregroundColor: colors.shortcut,
            .paragraphStyle: shortcutStyle,
        ]
        let shortcutRect = NSRect(
            x: bounds.width - horizontalPadding - shortcutWidth,
            y: dividerY + 5,
            width: shortcutWidth,
            height: 15
        )
        ("↑↓" as NSString).draw(in: shortcutRect, withAttributes: shortcutAttributes)
    }

    private func symbolName(for item: Item) -> String {
        let description = (item.description ?? "").lowercased()
        if description.contains("目录") || description.contains("folder") ||
            description.hasPrefix("/") || item.name.hasSuffix("/") {
            return "folder.fill"
        }
        if description.contains("文件") || description.contains("file") {
            return "doc.fill"
        }
        if description.contains("分支") || description.contains("branch") {
            return "arrow.triangle.branch"
        }
        if description.contains("历史") || description.contains("history") {
            return "clock.arrow.circlepath"
        }
        if item.name.hasPrefix("-") {
            return "slider.horizontal.3"
        }
        return "terminal"
    }

    private func symbolImage(for item: Item, color: NSColor) -> NSImage? {
        let name = symbolName(for: item)
        let key = name + "|" + color.description
        if let cached = symbolCache[key] {
            return cached
        }
        let configuration = NSImage.SymbolConfiguration(pointSize: 12, weight: .medium)
            .applying(NSImage.SymbolConfiguration(paletteColors: [color]))
        let image = NSImage(systemSymbolName: name, accessibilityDescription: nil)
            ?? NSImage(systemSymbolName: "circle.fill", accessibilityDescription: nil)
        let configured = image?.withSymbolConfiguration(configuration)
        if let configured {
            symbolCache[key] = configured
        }
        return configured
    }

    override func updateTrackingAreas() {
        for area in trackingAreas {
            removeTrackingArea(area)
        }
        addTrackingArea(NSTrackingArea(
            rect: bounds,
            options: [.mouseMoved, .mouseEnteredAndExited, .activeAlways, .inVisibleRect],
            owner: self,
            userInfo: nil
        ))
        super.updateTrackingAreas()
    }

    override func mouseMoved(with event: NSEvent) {
        let point = convert(event.locationInWindow, from: nil)
        let index = index(at: point) ?? -1
        if index != hovered {
            hovered = index
            needsDisplay = true
        }
    }

    override func mouseExited(with event: NSEvent) {
        if hovered != -1 {
            hovered = -1
            needsDisplay = true
        }
    }

    override func mouseDown(with event: NSEvent) {
        let point = convert(event.locationInWindow, from: nil)
        if let index = index(at: point) {
            onSelect?(index)
        } else {
            onDismiss?()
        }
    }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    private func index(at point: NSPoint) -> Int? {
        let relative = point.y - verticalPadding
        guard relative >= 0 else { return nil }
        let position = Int(relative / rowHeight)
        let visible = min(items.count, maximumRows)
        guard position >= 0, position < visible else { return nil }
        let rowY = verticalPadding + CGFloat(position) * rowHeight
        guard point.y >= rowY, point.y < rowY + rowHeight else { return nil }
        return firstVisible + position
    }
}

final class CandidatePanel: NSPanel {
    override var canBecomeKey: Bool { false }
    override var canBecomeMain: Bool { false }
}

// MARK: - Application

final class AppDelegate: NSObject, NSApplicationDelegate {
    private let socketPath: String
    private let session: String
    private let requestedOrigin: NSPoint?

    private var connection: AgentConnection?
    private var panel: CandidatePanel!
    private var view: CandidateView!
    private var version: UInt64 = 0
    private var offer: UInt64 = 0
    private var items: [Item] = []
    private var suppressOffersUntil = Date.distantPast
    private var caretLocator: CaretLocator?
    private var terminalPID: pid_t?
    private var focused = true

    init(socket: String, session: String, origin: NSPoint?) {
        self.socketPath = socket
        self.session = session
        self.requestedOrigin = origin
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        if let terminal = NSWorkspace.shared.frontmostApplication {
            terminalPID = terminal.processIdentifier
            caretLocator = CaretLocator(pid: terminal.processIdentifier)
        }
        NSWorkspace.shared.notificationCenter.addObserver(
            self,
            selector: #selector(applicationActivated(_:)),
            name: NSWorkspace.didActivateApplicationNotification,
            object: nil
        )
        panel = CandidatePanel(
            contentRect: NSRect(x: 0, y: 0, width: 320, height: 100),
            styleMask: [.borderless, .nonactivatingPanel],
            backing: .buffered,
            defer: false
        )
        panel.level = .floating
        panel.isOpaque = false
        panel.backgroundColor = .clear
        panel.hasShadow = true
        panel.animationBehavior = .none
        panel.hidesOnDeactivate = false
        panel.isFloatingPanel = true
        panel.becomesKeyOnlyIfNeeded = true
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]

        view = CandidateView(frame: NSRect(x: 0, y: 0, width: 320, height: 100))
        view.onSelect = { [weak self] index in self?.select(index) }
        view.onDismiss = { [weak self] in self?.dismiss() }
        panel.contentView = view
        view.warmup()
        // Keep the transparent overlay ordered in upfront. Toggling alpha is
        // much cheaper than a first orderFront for every new completion.
        panel.alphaValue = 0
        panel.ignoresMouseEvents = true
        panel.orderFrontRegardless()

        do {
            let connection = try AgentConnection(path: socketPath, session: session)
            connection.onMessage = { [weak self] message in self?.handle(message) }
            connection.onClose = { [weak self] in self?.connectionClosed() }
            connection.start()
            self.connection = connection
        } catch {
            writeError("oc-window: \(error)")
            NSApp.terminate(nil)
        }
    }

    @objc private func applicationActivated(_ notification: Notification) {
        guard let active = notification.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication else {
            return
        }
        // The candidate panel is a nonactivating overlay. If AppKit ever
        // reports it as active, keep the terminal session focused.
        if active.processIdentifier == getpid() {
            return
        }
        setFocused(active.processIdentifier == terminalPID)
    }

    private func setFocused(_ value: Bool) {
        guard value != focused else { return }
        focused = value
        connection?.send(["type": "focus", "focused": value])
        if !value {
            hide()
        }
    }

    private func handle(_ message: WindowMessage) {
        switch message.type {
        case "offer":
            if Date() < suppressOffersUntil {
                return
            }
            version = message.version ?? 0
            offer = message.offer ?? 0
            items = message.items ?? []
            view.items = items
            view.selected = message.selected ?? 0
            show()
        case "selection":
            view.selected = message.index ?? 0
        case "clear":
            hide()
        case "result":
            if message.ok == true {
                suppressOffersUntil = Date().addingTimeInterval(0.25)
                hide()
            }
        default:
            break
        }
    }

    private func show() {
        guard !items.isEmpty else {
            hide()
            return
        }
        let size = view.preferredSize(count: items.count)
        let screen = NSScreen.main?.visibleFrame ?? NSRect(x: 0, y: 0, width: 1440, height: 900)
        var origin = requestedOrigin ?? NSPoint(
            x: screen.maxX - size.width - 24,
            y: screen.maxY - size.height - 24
        )
        if requestedOrigin == nil, let caret = caretLocator?.rect() {
            let below = NSRect(
                x: caret.minX,
                y: caret.minY - size.height - 6,
                width: size.width,
                height: size.height
            )
            let above = NSRect(
                x: caret.minX,
                y: caret.maxY + 6,
                width: size.width,
                height: size.height
            )
            let chosen = below.minY >= screen.minY ? below : above
            origin = NSPoint(
                x: min(max(chosen.minX, screen.minX), screen.maxX - size.width),
                y: min(max(chosen.minY, screen.minY), screen.maxY - size.height)
            )
        }
        let frame = NSRect(origin: origin, size: size)
        if panel.frame.size != frame.size {
            panel.setFrame(frame, display: false)
        } else if panel.frame.origin != frame.origin {
            panel.setFrameOrigin(frame.origin)
        }
        if !panel.isVisible {
            panel.orderFrontRegardless()
        }
        panel.alphaValue = 1
        panel.ignoresMouseEvents = false
        view.needsDisplay = true
    }

    private func hide() {
        panel.alphaValue = 0
        panel.ignoresMouseEvents = true
    }

    private func select(_ index: Int) {
        guard offer != 0, index >= 0, index < items.count else { return }
        connection?.send([
            "type": "select",
            "version": version,
            "offer": offer,
            "index": index,
        ])
        hide()
    }

    private func dismiss() {
        if offer != 0 {
            connection?.send(["type": "dismiss", "version": version, "offer": offer])
        }
        hide()
    }

    private func connectionClosed() {
        hide()
        NSApp.terminate(nil)
    }
}

// MARK: - Check mode

var retainedCheckConnection: AgentConnection?

func runCheck(socket: String, session: String) -> Never {
    do {
        let connection = try AgentConnection(path: socket, session: session)
        retainedCheckConnection = connection
        connection.deliverOnMain = false
        connection.onMessage = { message in
            if message.type == "offer" {
                let names = (message.items ?? []).map(\.name).joined(separator: ",")
                print("offer \(message.version ?? 0) \(message.offer ?? 0) \(names)")
                fflush(stdout)
                exit(0)
            }
        }
        connection.onClose = {
            writeError("oc-window: agent closed the connection")
            exit(1)
        }
        connection.start()
    } catch {
        writeError("oc-window: \(error)")
        exit(1)
    }
    DispatchQueue.main.asyncAfter(deadline: .now() + 5) {
        writeError("oc-window: timed out waiting for candidates")
        exit(2)
    }
    RunLoop.main.run()
    exit(2)
}

// MARK: - Entry point

var socketPath = ""
var session = ""
var check = false
var accessibilityPrompt = false
var permissionStatus = false
var printCaret = false
var origin: NSPoint?
var index = 1
while index < CommandLine.arguments.count {
    switch CommandLine.arguments[index] {
    case "--socket":
        index += 1
        if index < CommandLine.arguments.count { socketPath = CommandLine.arguments[index] }
    case "--session":
        index += 1
        if index < CommandLine.arguments.count { session = CommandLine.arguments[index] }
    case "--x":
        index += 1
        if index < CommandLine.arguments.count, let x = Double(CommandLine.arguments[index]) {
            origin = NSPoint(x: x, y: origin?.y ?? 200)
        }
    case "--y":
        index += 1
        if index < CommandLine.arguments.count, let y = Double(CommandLine.arguments[index]) {
            origin = NSPoint(x: origin?.x ?? 200, y: y)
        }
    case "--check":
        check = true
    case "--accessibility-prompt":
        accessibilityPrompt = true
    case "--permission-status":
        permissionStatus = true
    case "--print-caret":
        printCaret = true
    default:
        writeError("oc-window: unknown argument \(CommandLine.arguments[index])")
        exit(2)
    }
    index += 1
}

if accessibilityPrompt {
    let trusted = requestAccessibility()
    print(trusted ? "accessibility: granted" : "accessibility: prompt requested")
    exit(trusted ? 0 : 1)
}

if permissionStatus {
    let accessibility = AXIsProcessTrusted()
    let inputMonitoring = IOHIDCheckAccess(kIOHIDRequestTypeListenEvent) == kIOHIDAccessTypeGranted
    let screenRecording = CGPreflightScreenCaptureAccess()
    print("accessibility: \(accessibility ? "granted" : "denied")")
    print("input-monitoring: \(inputMonitoring ? "granted" : "denied")")
    print("screen-recording: \(screenRecording ? "granted" : "denied")")
    exit(0)
}

if printCaret {
    guard let app = NSWorkspace.shared.frontmostApplication else {
        writeError("oc-window: no frontmost application")
        exit(1)
    }
    guard let rect = CaretLocator(pid: app.processIdentifier).rect() else {
        writeError("oc-window: caret unavailable (permission or unsupported element)")
        exit(1)
    }
    print("caret \(rect.minX) \(rect.minY) \(rect.width) \(rect.height)")
    exit(0)
}

guard !socketPath.isEmpty, !session.isEmpty else {
    writeError("usage: oc-window --socket PATH --session ID [--x X --y Y] [--check] [--print-caret] [--permission-status] [--accessibility-prompt]")
    exit(2)
}

if check {
    runCheck(socket: socketPath, session: session)
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let delegate = AppDelegate(socket: socketPath, session: session, origin: origin)
app.delegate = delegate
app.run()

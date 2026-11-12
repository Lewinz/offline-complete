import AppKit
import ApplicationServices
import Darwin
import Foundation

func writeError(_ message: String) {
    FileHandle.standardError.write(Data((message + "\n").utf8))
}

// MARK: - Wire protocol

struct Item: Codable {
    let name: String
    let description: String?
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

    init(pid: pid_t) {
        application = AXUIElementCreateApplication(pid)
    }

    /// Returns the caret rectangle in Cocoa screen coordinates, or nil when
    /// the focused element does not expose an accessibility text range.
    func rect() -> NSRect? {
        guard AXIsProcessTrusted() else { return nil }
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
        didSet { needsDisplay = true }
    }
    var onSelect: ((Int) -> Void)?
    var onDismiss: (() -> Void)?
    var selected = 0 {
        didSet { needsDisplay = true }
    }
    private var hovered = -1

    let rowHeight: CGFloat = 20
    let verticalPadding: CGFloat = 4
    let horizontalPadding: CGFloat = 8
    let maximumRows = 7
    let panelWidth: CGFloat = 320
    let descriptionHeight: CGFloat = 24

    override var isFlipped: Bool { true }
    override var acceptsFirstResponder: Bool { false }

    struct Palette {
        let background: NSColor
        let text: NSColor
        let descriptionText: NSColor
        let border: NSColor
        let selectedBackground: NSColor
        let selectedText: NSColor

        static let light = Palette(
            background: NSColor(srgbRed: 254 / 255, green: 254 / 255, blue: 254 / 255, alpha: 1),
            text: NSColor(srgbRed: 7 / 255, green: 7 / 255, blue: 7 / 255, alpha: 1),
            descriptionText: NSColor(srgbRed: 7 / 255, green: 7 / 255, blue: 7 / 255, alpha: 1),
            border: NSColor(srgbRed: 199 / 255, green: 199 / 255, blue: 199 / 255, alpha: 1),
            selectedBackground: NSColor(srgbRed: 41 / 255, green: 105 / 255, blue: 218 / 255, alpha: 1),
            selectedText: NSColor(srgbRed: 253 / 255, green: 253 / 255, blue: 253 / 255, alpha: 1)
        )

        static let dark = Palette(
            background: NSColor(srgbRed: 48 / 255, green: 48 / 255, blue: 48 / 255, alpha: 1),
            text: NSColor(srgbRed: 180 / 255, green: 180 / 255, blue: 180 / 255, alpha: 1),
            descriptionText: NSColor(srgbRed: 180 / 255, green: 180 / 255, blue: 180 / 255, alpha: 1),
            border: NSColor(srgbRed: 65 / 255, green: 65 / 255, blue: 65 / 255, alpha: 1),
            selectedBackground: NSColor(srgbRed: 30 / 255, green: 90 / 255, blue: 199 / 255, alpha: 1),
            selectedText: NSColor(srgbRed: 253 / 255, green: 253 / 255, blue: 253 / 255, alpha: 1)
        )
    }

    private var palette: Palette {
        // Kiro CLI's autocomplete window is dark even when the terminal is
        // running under a light macOS appearance. Keep the candidate list on
        // the same palette so the two integrations look consistent.
        .dark
    }

    func preferredSize(count: Int) -> NSSize {
        let rows = min(max(count, 1), maximumRows)
        return NSSize(
            width: panelWidth,
            height: CGFloat(rows) * rowHeight + verticalPadding * 2 + descriptionHeight
        )
    }

    private var selectedDescription: String? {
        guard items.indices.contains(selected) else { return nil }
        let value = items[selected].description?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return value.isEmpty ? nil : value
    }

    private var firstVisible: Int {
        let rows = min(items.count, maximumRows)
        let maxOffset = max(0, items.count - rows)
        return min(max(0, selected), maxOffset)
    }

    override func draw(_ dirtyRect: NSRect) {
        let colors = palette
        let background = NSBezierPath(roundedRect: bounds.insetBy(dx: 0.5, dy: 0.5), xRadius: 6, yRadius: 6)
        colors.background.setFill()
        background.fill()
        colors.border.setStroke()
        background.lineWidth = 1
        background.stroke()

        let nameStyle = NSMutableParagraphStyle()
        nameStyle.lineBreakMode = .byTruncatingTail
        let regularFont = NSFont(name: "Monaco", size: 12.8) ?? NSFont.monospacedSystemFont(ofSize: 12.8, weight: .regular)
        let selectedFont = NSFont(name: "Monaco-Bold", size: 12.8) ?? NSFont.monospacedSystemFont(ofSize: 12.8, weight: .semibold)

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
                let highlight = NSBezierPath(roundedRect: row.insetBy(dx: 4, dy: 1), xRadius: 4, yRadius: 4)
                colors.selectedBackground.setFill()
                highlight.fill()
            } else if index == hovered {
                let highlight = NSBezierPath(roundedRect: row.insetBy(dx: 4, dy: 1), xRadius: 4, yRadius: 4)
                colors.selectedBackground.withAlphaComponent(0.28).setFill()
                highlight.fill()
            }
            let nameAttributes: [NSAttributedString.Key: Any] = [
                .font: index == selected ? selectedFont : regularFont,
                .foregroundColor: index == selected ? colors.selectedText : colors.text,
                .paragraphStyle: nameStyle,
            ]
            let nameRect = NSRect(x: horizontalPadding, y: row.minY + 3, width: row.maxX - horizontalPadding * 2, height: 15)
            (items[index].name as NSString).draw(in: nameRect, withAttributes: nameAttributes)
        }

        let dividerY = verticalPadding + CGFloat(visible) * rowHeight
        let divider = NSBezierPath()
        divider.move(to: NSPoint(x: 4, y: dividerY))
        divider.line(to: NSPoint(x: bounds.width - 4, y: dividerY))
        colors.border.setStroke()
        divider.lineWidth = 1
        divider.stroke()

        if let description = selectedDescription {
            let baseFont = NSFont.systemFont(ofSize: 11.5)
            let italic = baseFont.fontDescriptor.withSymbolicTraits(.italic)
            let descriptionFont = NSFont(descriptor: italic, size: 11.5) ?? baseFont
            let descriptionStyle = NSMutableParagraphStyle()
            descriptionStyle.lineBreakMode = .byTruncatingTail
            let attributes: [NSAttributedString.Key: Any] = [
                .font: descriptionFont,
                .foregroundColor: colors.descriptionText,
                .paragraphStyle: descriptionStyle,
            ]
            let descriptionRect = NSRect(
                x: horizontalPadding,
                y: dividerY + 4,
                width: bounds.width - horizontalPadding * 2,
                height: 16
            )
            (description as NSString).draw(in: descriptionRect, withAttributes: attributes)
        }
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

    init(socket: String, session: String, origin: NSPoint?) {
        self.socketPath = socket
        self.session = session
        self.requestedOrigin = origin
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        if let terminal = NSWorkspace.shared.frontmostApplication {
            caretLocator = CaretLocator(pid: terminal.processIdentifier)
        }
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
        panel.hidesOnDeactivate = false
        panel.isFloatingPanel = true
        panel.becomesKeyOnlyIfNeeded = true
        panel.collectionBehavior = [.canJoinAllSpaces, .fullScreenAuxiliary]

        view = CandidateView(frame: NSRect(x: 0, y: 0, width: 320, height: 100))
        view.onSelect = { [weak self] index in self?.select(index) }
        view.onDismiss = { [weak self] in self?.dismiss() }
        panel.contentView = view

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
        panel.setFrame(NSRect(origin: origin, size: size), display: true)
        panel.orderFrontRegardless()
        view.needsDisplay = true
    }

    private func hide() {
        panel.orderOut(nil)
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
    writeError("usage: oc-window --socket PATH --session ID [--x X --y Y] [--check] [--print-caret]")
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

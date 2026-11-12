import AppKit
import CryptoKit
import Darwin
import Foundation

// MARK: - Paths and small helpers

enum AppPaths {
    static let fm = FileManager.default

    static var environment: [String: String] { ProcessInfo.processInfo.environment }

    static var home: URL {
        if let value = environment["HOME"], !value.isEmpty {
            return URL(fileURLWithPath: value)
        }
        return fm.homeDirectoryForCurrentUser
    }

    static var appVersion: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "dev"
    }

    static var rulesURL: URL? {
        if let value = environment["OFFLINE_COMPLETE_RULES"], !value.isEmpty {
            return URL(fileURLWithPath: value)
        }
        for name in ["rules.bin", "rules.json"] {
            let bundled = Bundle.main.bundleURL.appendingPathComponent("Contents/Resources/\(name)")
            if fm.fileExists(atPath: bundled.path) {
                return bundled
            }
        }
        for name in ["base.bin", "base.json"] {
            let current = URL(fileURLWithPath: fm.currentDirectoryPath).appendingPathComponent("rules/\(name)")
            if fm.fileExists(atPath: current.path) {
                return current
            }
        }
        if let executable = Bundle.main.executableURL {
            let root = executable.deletingLastPathComponent().deletingLastPathComponent()
            for name in ["rules.bin", "rules.json"] {
                let sibling = root.appendingPathComponent("Resources/\(name)")
                if fm.fileExists(atPath: sibling.path) {
                    return sibling
                }
            }
        }
        return nil
    }

    static var dataDir: URL {
        if let value = environment["OFFLINE_COMPLETE_DATA_DIR"], !value.isEmpty {
            return URL(fileURLWithPath: value)
        }
        return home.appendingPathComponent("Library/Application Support/offline-complete")
    }

    static var learningURL: URL { dataDir.appendingPathComponent("learning.json") }

    static var configURL: URL {
        if let value = environment["OFFLINE_COMPLETE_CONFIG"], !value.isEmpty {
            return URL(fileURLWithPath: value)
        }
        return home.appendingPathComponent(".oc/config.json")
    }

    static var defaultLogDirectory: URL { home.appendingPathComponent(".oc/logs") }

    static var socketBase: URL {
        if let value = environment["OFFLINE_COMPLETE_SOCKET_BASE"], !value.isEmpty {
            return URL(fileURLWithPath: value)
        }
        return dataDir.appendingPathComponent("run")
    }

    static func tool(_ name: String) -> URL? {
        if name == "oc-manage", let value = environment["OFFLINE_COMPLETE_MANAGE"], !value.isEmpty {
            return URL(fileURLWithPath: value)
        }
        let candidates: [URL?] = [
            Bundle.main.bundleURL.appendingPathComponent("Contents/MacOS/\(name)"),
            Bundle.main.executableURL?.deletingLastPathComponent().appendingPathComponent(name),
            URL(fileURLWithPath: fm.currentDirectoryPath).appendingPathComponent("bin/\(name)"),
        ]
        for candidate in candidates.compactMap({ $0 }) where fm.isExecutableFile(atPath: candidate.path) {
            return candidate
        }
        if let path = environment["PATH"] {
            for directory in path.split(separator: ":") {
                let candidate = URL(fileURLWithPath: String(directory)).appendingPathComponent(name)
                if fm.isExecutableFile(atPath: candidate.path) {
                    return candidate
                }
            }
        }
        return nil
    }
}


struct LogSettings {
    var enabled: Bool
    var directory: URL
    var retentionDays: Int
    var currentURL: URL
    var error: String?
}

struct AISettings {
    var enabled: Bool
    var protocolName: String
    var url: String
    var key: String
    var model: String
    var maxTokens: Int
    var error: String?
}

func loadAISettings() -> AISettings {
    var result = AISettings(enabled: false, protocolName: "openai", url: "", key: "", model: "", maxTokens: 1024, error: nil)
    let config = AppPaths.configURL
    guard let data = try? Data(contentsOf: config) else { return result }
    do {
        guard let root = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw NSError(domain: "oc", code: 1, userInfo: [NSLocalizedDescriptionKey: "不是 JSON 对象"])
        }
        if let ai = root["ai"] as? [String: Any] {
            result.enabled = ai["enabled"] as? Bool ?? result.enabled
            result.protocolName = ai["protocol"] as? String ?? result.protocolName
            result.url = ai["url"] as? String ?? result.url
            result.key = ai["key"] as? String ?? result.key
            result.model = ai["model"] as? String ?? result.model
            if let value = ai["maxTokens"] as? NSNumber {
                result.maxTokens = value.intValue
            }
        }
    } catch {
        result.error = error.localizedDescription
    }
    if result.protocolName != "openai" && result.protocolName != "anthropic" {
        result.error = result.error ?? "不支持的 AI 协议：\(result.protocolName)"
        result.protocolName = "openai"
    }
    if result.maxTokens < 1 || result.maxTokens > 32768 {
        result.error = result.error ?? "AI max tokens 必须在 1 到 32768 之间"
        result.maxTokens = 1024
    }
    return result
}

func logDateString(_ date: Date = Date()) -> String {
    let formatter = DateFormatter()
    formatter.locale = Locale(identifier: "en_US_POSIX")
    formatter.timeZone = .current
    formatter.dateFormat = "yyyy-MM-dd"
    return formatter.string(from: date)
}

func expandTilde(_ value: String) -> String {
    NSString(string: value).expandingTildeInPath
}

func parseBoolValue(_ value: String, fallback: Bool) -> Bool {
    switch value.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
    case "1", "true", "yes", "on": return true
    case "0", "false", "no", "off": return false
    default: return fallback
    }
}

func loadLogSettings() -> LogSettings {
    let env = AppPaths.environment
    var enabled = true
    var directory = AppPaths.defaultLogDirectory
    var retention = 3
    var error: String?

    let config = AppPaths.configURL
    if let data = try? Data(contentsOf: config) {
        do {
            guard let root = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
                throw NSError(domain: "oc", code: 1, userInfo: [NSLocalizedDescriptionKey: "不是 JSON 对象"])
            }
            guard let schema = root["schemaVersion"] as? NSNumber else {
                throw NSError(domain: "oc", code: 2, userInfo: [NSLocalizedDescriptionKey: "缺少设置版本"])
            }
            if schema.intValue != 1 {
                throw NSError(domain: "oc", code: 2, userInfo: [NSLocalizedDescriptionKey: "不支持的设置版本"])
            }
            if let logging = root["logging"] as? [String: Any] {
                enabled = logging["enabled"] as? Bool ?? enabled
                if let value = logging["directory"] as? String, !value.isEmpty {
                    directory = URL(fileURLWithPath: expandTilde(value))
                }
                if let value = logging["retentionDays"] as? NSNumber {
                    retention = value.intValue
                }
            }
        } catch let parseError {
            error = "设置文件无效：\(parseError.localizedDescription)"
        }
    } else if FileManager.default.fileExists(atPath: config.path) {
        error = "无法读取设置文件：\(config.path)"
    }

    let expanded = expandTilde(directory.path)
    if expanded.hasPrefix("/") {
        directory = URL(fileURLWithPath: expanded)
    } else {
        error = error ?? "日志目录必须是绝对路径"
        directory = AppPaths.defaultLogDirectory
    }
    if retention < 1 || retention > 365 {
        error = error ?? "保留天数必须在 1 到 365 之间"
        retention = 3
    }

    var explicitLog: URL?
    if let value = env["OFFLINE_COMPLETE_LOG_DIR"], !value.isEmpty {
        directory = URL(fileURLWithPath: expandTilde(value))
    }
    if let value = env["OFFLINE_COMPLETE_LOG"], !value.isEmpty {
        explicitLog = URL(fileURLWithPath: expandTilde(value))
        directory = explicitLog!.deletingLastPathComponent()
        enabled = true
    }
    if let value = env["OFFLINE_COMPLETE_LOG_ENABLED"] {
        enabled = parseBoolValue(value, fallback: enabled)
    }
    if let value = env["OFFLINE_COMPLETE_LOG_RETENTION_DAYS"], let days = Int(value), (1...365).contains(days) {
        retention = days
    }
    if !directory.path.hasPrefix("/") {
        error = error ?? "日志目录必须是绝对路径"
        directory = AppPaths.defaultLogDirectory
    }
    let current = explicitLog ?? directory.appendingPathComponent("agent-\(logDateString()).log")
    return LogSettings(
        enabled: enabled,
        directory: directory,
        retentionDays: retention,
        currentURL: current,
        error: error
    )
}

func listLogFiles(_ settings: LogSettings) -> [URL] {
    let fm = FileManager.default
    var files: [URL] = []
    if let entries = try? fm.contentsOfDirectory(
        at: settings.directory,
        includingPropertiesForKeys: [.isRegularFileKey],
        options: [.skipsHiddenFiles]
    ) {
        for url in entries {
            let name = url.lastPathComponent
            let isLog = name == "agent.log" || name == "agent.log.1" ||
                (name.hasPrefix("agent-") && (name.hasSuffix(".log") || name.hasSuffix(".log.1")))
            guard isLog else { continue }
            let values = try? url.resourceValues(forKeys: [.isRegularFileKey])
            if values?.isRegularFile == false { continue }
            files.append(url)
        }
    }
    files.removeAll { $0.path == settings.currentURL.path }
    files.sort { $0.lastPathComponent > $1.lastPathComponent }
    files.insert(settings.currentURL, at: 0)
    return files
}

func makeLabel(_ text: String, size: CGFloat = 12, weight: NSFont.Weight = .regular) -> NSTextField {
    let label = NSTextField(labelWithString: text)
    label.font = .systemFont(ofSize: size, weight: weight)
    label.textColor = .labelColor
    label.lineBreakMode = .byTruncatingMiddle
    return label
}

func makeButton(_ title: String, target: AnyObject?, action: Selector) -> NSButton {
    KiroButton(title: title, style: .secondary, target: target, action: action)
}

func makeTextScroll() -> (NSScrollView, NSTextView) {
    let scroll = NSScrollView()
    scroll.hasVerticalScroller = true
    scroll.autohidesScrollers = true
    scroll.borderType = .noBorder
    scroll.drawsBackground = false
    scroll.wantsLayer = true
    scroll.layer?.cornerRadius = 8
    scroll.layer?.borderWidth = 1
    scroll.layer?.borderColor = KiroTheme.border.cgColor
    let text = NSTextView(frame: NSRect(x: 0, y: 0, width: 900, height: 500))
    text.isEditable = false
    text.isSelectable = true
    text.isRichText = false
    text.font = .monospacedSystemFont(ofSize: 11.5, weight: .regular)
    text.textColor = KiroTheme.title
    text.backgroundColor = KiroTheme.surface
    text.textContainerInset = NSSize(width: 12, height: 10)
    text.minSize = NSSize(width: 0, height: 0)
    text.maxSize = NSSize(width: CGFloat.greatestFiniteMagnitude, height: CGFloat.greatestFiniteMagnitude)
    text.isVerticallyResizable = true
    text.isHorizontallyResizable = false
    text.autoresizingMask = [.width]
    text.textContainer?.widthTracksTextView = true
    text.textContainer?.containerSize = NSSize(width: 900, height: CGFloat.greatestFiniteMagnitude)
    scroll.documentView = text
    return (scroll, text)
}

// MARK: - Kiro-style appearance

enum KiroTheme {
    static let accent = NSColor(srgbRed: 0.42, green: 0.0, blue: 0.91, alpha: 1)
    static let accentPressed = NSColor(srgbRed: 0.35, green: 0.0, blue: 0.78, alpha: 1)
    static let background = NSColor.white
    static let surface = NSColor(srgbRed: 0.97, green: 0.97, blue: 0.98, alpha: 1)
    static let border = NSColor(srgbRed: 0.90, green: 0.90, blue: 0.92, alpha: 1)
    static let muted = NSColor(srgbRed: 0.55, green: 0.55, blue: 0.58, alpha: 1)
    static let title = NSColor(srgbRed: 0.08, green: 0.08, blue: 0.10, alpha: 1)
    static let sidebarWidth: CGFloat = 260
    static let contentInset: CGFloat = 40
}

func kiroSectionTitle(_ text: String) -> NSTextField {
    let label = makeLabel(text, size: 16, weight: .semibold)
    label.textColor = KiroTheme.muted
    label.lineBreakMode = .byTruncatingTail
    return label
}

func kiroTitle(_ text: String) -> NSTextField {
    let label = makeLabel(text, size: 15, weight: .semibold)
    label.textColor = KiroTheme.title
    label.lineBreakMode = .byWordWrapping
    label.maximumNumberOfLines = 0
    return label
}

func kiroBody(_ text: String) -> NSTextField {
    let label = makeLabel(text, size: 12.5, weight: .regular)
    label.textColor = NSColor.secondaryLabelColor
    label.lineBreakMode = .byWordWrapping
    label.maximumNumberOfLines = 0
    return label
}

final class KiroButton: NSButton {
    enum Style { case primary, secondary, quiet }

    private let style: Style
    private var hovered = false

    init(title: String, style: Style = .secondary, target: AnyObject?, action: Selector?) {
        self.style = style
        super.init(frame: .zero)
        self.target = target
        self.action = action
        isBordered = false
        wantsLayer = true
        layer?.cornerRadius = 7
        alignment = .center
        font = .systemFont(ofSize: 13, weight: .semibold)
        setDisplayTitle(title)
        updateAppearance()
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func setDisplayTitle(_ value: String) {
        title = value
        let color: NSColor
        switch style {
        case .primary: color = .white
        case .secondary, .quiet: color = KiroTheme.title
        }
        attributedTitle = NSAttributedString(string: value, attributes: [
            .font: NSFont.systemFont(ofSize: 13, weight: .semibold),
            .foregroundColor: color,
        ])
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        for area in trackingAreas { removeTrackingArea(area) }
        addTrackingArea(NSTrackingArea(
            rect: bounds,
            options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect],
            owner: self,
            userInfo: nil
        ))
    }

    override func mouseEntered(with event: NSEvent) {
        hovered = true
        updateAppearance()
    }

    override func mouseExited(with event: NSEvent) {
        hovered = false
        updateAppearance()
    }

    private func updateAppearance() {
        switch style {
        case .primary:
            layer?.backgroundColor = (hovered ? KiroTheme.accentPressed : KiroTheme.accent).cgColor
        case .secondary:
            layer?.backgroundColor = (hovered ? NSColor(srgbRed: 0.92, green: 0.92, blue: 0.94, alpha: 1) : NSColor(srgbRed: 0.95, green: 0.95, blue: 0.96, alpha: 1)).cgColor
        case .quiet:
            layer?.backgroundColor = (hovered ? NSColor(srgbRed: 0.95, green: 0.95, blue: 0.96, alpha: 1) : NSColor.clear).cgColor
        }
    }
}

final class KiroSwitch: NSControl {
    var state: NSControl.StateValue = .off {
        didSet { needsDisplay = true }
    }

    init(target: AnyObject?, action: Selector?) {
        super.init(frame: .zero)
        self.target = target
        self.action = action
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    override var intrinsicContentSize: NSSize { NSSize(width: 44, height: 26) }
    override var acceptsFirstResponder: Bool { false }

    override func draw(_ dirtyRect: NSRect) {
        let track = NSBezierPath(roundedRect: bounds.insetBy(dx: 1, dy: 1), xRadius: 12, yRadius: 12)
        let on = state == .on
        (on ? KiroTheme.accent : NSColor(srgbRed: 0.82, green: 0.82, blue: 0.85, alpha: 1)).setFill()
        track.fill()
        let diameter: CGFloat = 22
        let knobX = on ? bounds.width - diameter - 2 : 2
        let knob = NSBezierPath(ovalIn: NSRect(x: knobX, y: 2, width: diameter, height: diameter))
        NSColor.white.setFill()
        knob.fill()
        NSColor.black.withAlphaComponent(0.08).setStroke()
        knob.lineWidth = 1
        knob.stroke()
    }

    override func mouseDown(with event: NSEvent) {
        guard isEnabled else { return }
        state = state == .on ? .off : .on
        sendAction(action, to: target)
    }
}

final class KiroSidebarItem: NSView {
    var isSelected = false {
        didSet { updateAppearance() }
    }
    var onClick: (() -> Void)?
    private let iconView = NSImageView()
    private let label = NSTextField(labelWithString: "")
    private var hovered = false

    init(title: String, symbol: String) {
        super.init(frame: .zero)
        wantsLayer = true
        layer?.cornerRadius = 7
        layer?.masksToBounds = true
        iconView.image = NSImage(systemSymbolName: symbol, accessibilityDescription: nil)
        iconView.imageScaling = .scaleProportionallyDown
        iconView.translatesAutoresizingMaskIntoConstraints = false
        label.stringValue = title
        label.font = .systemFont(ofSize: 14, weight: .medium)
        label.lineBreakMode = .byTruncatingTail
        label.translatesAutoresizingMaskIntoConstraints = false
        addSubview(iconView)
        addSubview(label)
        NSLayoutConstraint.activate([
            iconView.leadingAnchor.constraint(equalTo: leadingAnchor, constant: 12),
            iconView.centerYAnchor.constraint(equalTo: centerYAnchor),
            iconView.widthAnchor.constraint(equalToConstant: 19),
            iconView.heightAnchor.constraint(equalToConstant: 19),
            label.leadingAnchor.constraint(equalTo: iconView.trailingAnchor, constant: 12),
            label.trailingAnchor.constraint(equalTo: trailingAnchor, constant: -10),
            label.centerYAnchor.constraint(equalTo: centerYAnchor),
        ])
        updateAppearance()
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    override var intrinsicContentSize: NSSize { NSSize(width: NSView.noIntrinsicMetric, height: 40) }

    override func hitTest(_ point: NSPoint) -> NSView? {
        // The point is expressed in the superview's coordinates. Let AppKit
        // resolve the subviews (icon/label), then route the whole item to this
        // view so mouseDown fires even when the click lands on the label.
        super.hitTest(point) == nil ? nil : self
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        for area in trackingAreas { removeTrackingArea(area) }
        addTrackingArea(NSTrackingArea(
            rect: bounds,
            options: [.mouseEnteredAndExited, .activeAlways, .inVisibleRect],
            owner: self,
            userInfo: nil
        ))
    }

    override func mouseEntered(with event: NSEvent) {
        hovered = true
        updateAppearance()
    }

    override func mouseExited(with event: NSEvent) {
        hovered = false
        updateAppearance()
    }

    override func mouseDown(with event: NSEvent) {
        onClick?()
    }

    private func updateAppearance() {
        if isSelected {
            layer?.backgroundColor = KiroTheme.accent.cgColor
            iconView.contentTintColor = .white
            label.textColor = .white
        } else {
            layer?.backgroundColor = (hovered ? NSColor(srgbRed: 0.95, green: 0.95, blue: 0.96, alpha: 1) : NSColor.clear).cgColor
            iconView.contentTintColor = NSColor(srgbRed: 0.42, green: 0.42, blue: 0.45, alpha: 1)
            label.textColor = KiroTheme.title
        }
        iconView.needsDisplay = true
    }
}

final class KiroInfoRow: NSView {
    let valueLabel = NSTextField(labelWithString: "")

    init(title: String, detail: String) {
        super.init(frame: .zero)
        translatesAutoresizingMaskIntoConstraints = false
        let titleLabel = kiroTitle(title)
        let detailLabel = kiroBody(detail)
        detailLabel.lineBreakMode = .byTruncatingTail
        detailLabel.maximumNumberOfLines = 1
        valueLabel.font = .systemFont(ofSize: 12.5, weight: .semibold)
        valueLabel.alignment = .right
        valueLabel.lineBreakMode = .byTruncatingTail
        valueLabel.setContentHuggingPriority(.required, for: .horizontal)
        valueLabel.setContentCompressionResistancePriority(.required, for: .horizontal)

        let textStack = NSStackView(views: [titleLabel, detailLabel])
        textStack.orientation = .vertical
        textStack.alignment = .leading
        textStack.spacing = 2
        textStack.translatesAutoresizingMaskIntoConstraints = false
        addSubview(textStack)
        addSubview(valueLabel)
        valueLabel.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            textStack.leadingAnchor.constraint(equalTo: leadingAnchor),
            textStack.topAnchor.constraint(equalTo: topAnchor),
            textStack.bottomAnchor.constraint(equalTo: bottomAnchor),
            textStack.trailingAnchor.constraint(lessThanOrEqualTo: valueLabel.leadingAnchor, constant: -16),
            valueLabel.trailingAnchor.constraint(equalTo: trailingAnchor),
            valueLabel.centerYAnchor.constraint(equalTo: titleLabel.centerYAnchor),
            valueLabel.widthAnchor.constraint(lessThanOrEqualToConstant: 180),
            heightAnchor.constraint(greaterThanOrEqualToConstant: 46),
        ])
    }

    required init?(coder: NSCoder) { fatalError("init(coder:) has not been implemented") }

    func setStatus(_ text: String, color: NSColor = KiroTheme.muted) {
        valueLabel.stringValue = text
        valueLabel.textColor = color
    }
}

func kiroSidebarHeader(_ text: String) -> NSTextField {
    let label = makeLabel(text, size: 12.5, weight: .semibold)
    label.textColor = KiroTheme.muted
    return label
}

func formatBytes(_ bytes: Int) -> String {
    ByteCountFormatter.string(fromByteCount: Int64(bytes), countStyle: .file)
}

func showAlert(_ title: String, _ message: String) {
    let alert = NSAlert()
    alert.messageText = title
    alert.informativeText = message
    alert.alertStyle = .warning
    alert.addButton(withTitle: "好")
    alert.runModal()
}

func runTool(_ name: String, _ arguments: [String]) -> (Int32, String) {
    guard let url = AppPaths.tool(name) else {
        return (-1, "找不到 \(name)")
    }
    let process = Process()
    process.executableURL = url
    process.arguments = arguments
    let pipe = Pipe()
    process.standardOutput = pipe
    process.standardError = pipe
    do {
        try process.run()
    } catch {
        return (-1, "无法运行 \(name): \(error.localizedDescription)")
    }
    let data = pipe.fileHandleForReading.readDataToEndOfFile()
    process.waitUntilExit()
    return (process.terminationStatus, String(decoding: data, as: UTF8.self))
}


func shellQuote(_ value: String) -> String {
    "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'"
}

func installedApplicationPath() -> String? {
    let bundle = Bundle.main.bundleURL.standardizedFileURL.path
    let home = AppPaths.home.standardizedFileURL.path
    if bundle.hasPrefix("/Applications/") || bundle.hasPrefix(home + "/Applications/") {
        return bundle
    }
    return nil
}

func launcherMatches(_ path: String, appPath: String) -> Bool {
    guard let text = try? String(contentsOfFile: path, encoding: .utf8) else { return false }
    return text.contains("offline-complete launcher") && text.contains(appPath)
}

func writeLauncher(_ url: URL, content: String) -> String? {
    let manager = FileManager.default
    if manager.fileExists(atPath: url.path) {
        let existing = (try? String(contentsOf: url, encoding: .utf8)) ?? ""
        if !existing.contains("offline-complete launcher") {
            return "\(url.path) 已存在，且不是 Offline Complete 安装的，未覆盖"
        }
    }
    do {
        try manager.createDirectory(
            at: url.deletingLastPathComponent(),
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o755]
        )
        try Data(content.utf8).write(to: url, options: .atomic)
        try manager.setAttributes([.posixPermissions: 0o755], ofItemAtPath: url.path)
    } catch {
        return "写入 \(url.path) 失败：\(error.localizedDescription)"
    }
    return nil
}

func writeUserCommandWrappers(appPath: String) -> String? {
    let bin = AppPaths.home.appendingPathComponent(".local/bin")
    let quoted = shellQuote(appPath)
    let oc = """
    #!/bin/sh
    # offline-complete launcher (installed by Offline Complete.app)
    APP=\(quoted)
    if [ ! -x "$APP/Contents/MacOS/offline-complete" ]; then
      export OFFLINE_COMPLETE_ACTIVE=1
      exec /bin/zsh -i
    fi
    if [ "$#" -eq 0 ]; then
      set -- zsh -i
    fi
    export OFFLINE_COMPLETE_RULES="$APP/Contents/Resources/rules.bin"
    exec "$APP/Contents/MacOS/offline-complete" "$@"
    """
    if let error = writeLauncher(bin.appendingPathComponent("oc"), content: oc) {
        return error
    }
    for name in ["oc-manage", "oc-window", "oc-client", "oc-app"] {
        let content = """
        #!/bin/sh
        # offline-complete launcher (installed by Offline Complete.app)
        APP=\(quoted)
        exec "$APP/Contents/MacOS/\(name)" "$@"
        """
        if let error = writeLauncher(bin.appendingPathComponent(name), content: content) {
            return error
        }
    }
    return nil
}

// ensureCommandLineIntegration is deliberately silent on success. It only
// runs when this bundle is installed in /Applications or ~/Applications; the
// app mounted directly from a DMG leaves the user's shell untouched.
func appPIDPath() -> URL {
    AppPaths.dataDir.appendingPathComponent("app.pid")
}

// markAppRunning is the presence check shared with the zsh block and the
// agent. A stale PID is ignored by kill -0, so a crash does not leave the
// terminal integration stuck on.
func markAppRunning() -> String? {
    let directory = AppPaths.dataDir
    let manager = FileManager.default
    do {
        try manager.createDirectory(
            at: directory,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
        try manager.setAttributes([.posixPermissions: 0o700], ofItemAtPath: directory.path)
        let url = appPIDPath()
        try Data("\(getpid())".utf8).write(to: url, options: .atomic)
        try manager.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    } catch {
        return "写入运行标记失败：\(error.localizedDescription)"
    }
    return nil
}

func clearAppRunning() {
    let url = appPIDPath()
    guard let text = try? String(contentsOf: url, encoding: .utf8),
          let pid = Int32(text.trimmingCharacters(in: .whitespacesAndNewlines)),
          pid == getpid() else {
        return
    }
    try? FileManager.default.removeItem(at: url)
}

func ensureCommandLineIntegration() -> String? {
    guard let appPath = installedApplicationPath() else { return nil }
    if let error = markAppRunning() {
        return error
    }
    let systemOC = "/usr/local/bin/oc"
    let userOC = AppPaths.home.appendingPathComponent(".local/bin/oc").path
    let chosen: String
    if launcherMatches(systemOC, appPath: appPath) {
        chosen = systemOC
    } else {
        if let error = writeUserCommandWrappers(appPath: appPath) {
            return error
        }
        chosen = userOC
    }
    let zshrc = AppPaths.home.appendingPathComponent(".zshrc")
    let text = (try? String(contentsOf: zshrc, encoding: .utf8)) ?? ""
    if text.contains("# offline-complete integration v2") && text.contains(shellQuote(chosen)) {
        return nil
    }
    let (code, output) = runTool("oc-manage", [
        "shell", "install",
        "--home", AppPaths.home.path,
        "--oc", chosen,
    ])
    if code != 0 {
        return output.isEmpty ? "oc-manage shell install 返回 \(code)" : output
    }
    return nil
}

func reveal(_ url: URL, createDirectory: Bool = false) {
    let fm = FileManager.default
    if createDirectory {
        try? fm.createDirectory(at: url, withIntermediateDirectories: true)
    }
    if fm.fileExists(atPath: url.path) {
        NSWorkspace.shared.activateFileViewerSelecting([url])
    } else {
        let parent = url.deletingLastPathComponent()
        if fm.fileExists(atPath: parent.path) {
            NSWorkspace.shared.open(parent)
        } else {
            showAlert("路径不存在", url.path)
        }
    }
}

// MARK: - Rules

final class RuleNode: NSObject {
    let name: String
    let detail: String
    let children: [RuleNode]

    init(name: String, detail: String, children: [RuleNode]) {
        self.name = name
        self.detail = detail
        self.children = children
    }
}

struct RulesSnapshot {
    let path: String
    let commands: [RuleNode]
    let topCount: Int
    let total: Int
    let sha: String
    let error: String?
}

func buildRuleNode(_ object: [String: Any], kind: String) -> RuleNode {
    let name = object["name"] as? String ?? "?"
    let description = object["description"] as? String ?? ""
    var children: [RuleNode] = []
    for option in object["options"] as? [[String: Any]] ?? [] {
        children.append(buildRuleNode(option, kind: "选项"))
    }
    for subcommand in object["subcommands"] as? [[String: Any]] ?? [] {
        children.append(buildRuleNode(subcommand, kind: "子命令"))
    }
    let detail = description.isEmpty ? kind : "\(kind) · \(description)"
    return RuleNode(name: name, detail: detail, children: children)
}

func countRuleNodes(_ nodes: [RuleNode]) -> Int {
    nodes.reduce(0) { $0 + 1 + countRuleNodes($1.children) }
}

enum RuleCrypto {
    static let magic = Data("OCR1".utf8)
    static let key = Data(hex: "0688a50bf48feece1d731b0f135630a51b933d55ec256e81c80685e83cc9cad5")

    static func read(_ url: URL) throws -> Data {
        let data = try Data(contentsOf: url)
        guard data.starts(with: magic) else { return data }
        guard data.count > magic.count else { throw CocoaError(.fileReadCorruptFile) }
        let box = try AES.GCM.SealedBox(combined: data.subdata(in: magic.count..<data.count))
        return try AES.GCM.open(box, using: SymmetricKey(data: key))
    }
}

private extension Data {
    init(hex: String) {
        var bytes: [UInt8] = []
        var index = hex.startIndex
        while index < hex.endIndex {
            let next = hex.index(index, offsetBy: 2, limitedBy: hex.endIndex) ?? hex.endIndex
            bytes.append(UInt8(hex[index..<next], radix: 16) ?? 0)
            index = next
        }
        self.init(bytes)
    }
}

func loadRules() -> RulesSnapshot {
    guard let url = AppPaths.rulesURL else {
        return RulesSnapshot(path: "", commands: [], topCount: 0, total: 0, sha: "", error: "未找到 rules.bin")
    }
    do {
        let data = try RuleCrypto.read(url)
        guard let root = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return RulesSnapshot(path: url.path, commands: [], topCount: 0, total: 0, sha: "", error: "规则文件不是 JSON 对象")
        }
        guard let schema = root["schemaVersion"] as? NSNumber, schema.intValue == 1 else {
            return RulesSnapshot(path: url.path, commands: [], topCount: 0, total: 0, sha: "", error: "不支持的规则版本")
        }
        guard let raw = root["commands"] as? [[String: Any]] else {
            return RulesSnapshot(path: url.path, commands: [], topCount: 0, total: 0, sha: "", error: "缺少 commands")
        }
        let commands = raw.map { buildRuleNode($0, kind: "命令") }
        let digest = SHA256.hash(data: data).map { String(format: "%02x", $0) }.joined()
        return RulesSnapshot(
            path: url.path,
            commands: commands,
            topCount: commands.count,
            total: countRuleNodes(commands),
            sha: digest,
            error: nil
        )
    } catch {
        return RulesSnapshot(path: url.path, commands: [], topCount: 0, total: 0, sha: "", error: error.localizedDescription)
    }
}

// MARK: - Learning

struct LearningEntry {
    let context: String
    let candidate: String
    let count: UInt64
    let last: Date?
}

struct LearningSnapshot {
    let path: String
    let learning: Bool
    let entries: [LearningEntry]
    let bytes: Int
    let error: String?
}

func parseDate(_ value: String) -> Date? {
    let fractional = ISO8601DateFormatter()
    fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    if let date = fractional.date(from: value) {
        return date
    }
    let plain = ISO8601DateFormatter()
    plain.formatOptions = [.withInternetDateTime]
    if let date = plain.date(from: value) {
        return date
    }
    if let dot = value.firstIndex(of: "."),
       let end = value[dot...].firstIndex(where: { $0 == "Z" || $0 == "+" || $0 == "-" }) {
        return plain.date(from: String(value[..<dot]) + String(value[end...]))
    }
    return nil
}

func loadLearning() -> LearningSnapshot {
    let url = AppPaths.learningURL
    guard let data = try? Data(contentsOf: url) else {
        return LearningSnapshot(path: url.path, learning: true, entries: [], bytes: 0, error: nil)
    }
    do {
        guard let root = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return LearningSnapshot(path: url.path, learning: true, entries: [], bytes: data.count, error: "学习数据不是 JSON 对象")
        }
        let learning = root["learning"] as? Bool ?? true
        let selections = root["selections"] as? [String: Any] ?? [:]
        var entries: [LearningEntry] = []
        entries.reserveCapacity(selections.count)
        for (key, raw) in selections {
            guard let selection = raw as? [String: Any] else { continue }
            let parts = key.components(separatedBy: "\0")
            guard parts.count >= 5 else { continue }
            let command = parts[0]
            let subcommands = parts[1]
            let kindCode = parts[2]
            let slot = parts[3]
            let candidate = parts[4]
            let kind: String
            switch kindCode {
            case "opt": kind = "选项"
            case "sub": kind = "子命令"
            case "pos": kind = "位置"
            default: kind = kindCode
            }
            var context = command
            if !subcommands.isEmpty {
                context += " " + subcommands.replacingOccurrences(of: "/", with: " ")
            }
            context += " · \(kind)"
            if kindCode == "pos" {
                context += " #\(slot)"
            }
            let count = (selection["count"] as? NSNumber)?.uint64Value ?? 0
            let last = (selection["last"] as? String).flatMap(parseDate)
            entries.append(LearningEntry(context: context, candidate: candidate, count: count, last: last))
        }
        entries.sort {
            if $0.count != $1.count { return $0.count > $1.count }
            return ($0.last ?? .distantPast) > ($1.last ?? .distantPast)
        }
        return LearningSnapshot(path: url.path, learning: learning, entries: entries, bytes: data.count, error: nil)
    } catch {
        return LearningSnapshot(path: url.path, learning: true, entries: [], bytes: data.count, error: error.localizedDescription)
    }
}

// MARK: - Overview

final class OverviewPanel: NSObject {
    let view = NSView()
    private let autoRow = KiroInfoRow(title: "自动补全", detail: "关闭窗口会留在后台；退出应用后停止")
    private let rulesRow = KiroInfoRow(title: "规则", detail: "离线补全规则")
    private let learningRow = KiroInfoRow(title: "学习", detail: "本机选择记录")
    private let logRow = KiroInfoRow(title: "日志", detail: "agent 运行日志")
    private let scroll: NSScrollView
    private let text: NSTextView
    private let copyButton: KiroButton

    override init() {
        view.frame = NSRect(x: 0, y: 0, width: 840, height: 732)
        view.wantsLayer = true
        view.layer?.backgroundColor = KiroTheme.background.cgColor
        (scroll, text) = makeTextScroll()
        copyButton = KiroButton(title: "复制 oc 命令", style: .primary, target: nil, action: nil)
        super.init()
        copyButton.target = self
        copyButton.action = #selector(copyCommand(_:))

        let stack = NSStackView()
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 16
        stack.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: KiroTheme.contentInset),
            stack.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -KiroTheme.contentInset),
            stack.topAnchor.constraint(equalTo: view.topAnchor, constant: 34),
        ])

        let intro = kiroBody("Offline Complete 在本机提供离线 CLI 补全，不依赖账号或网络。")
        intro.preferredMaxLayoutWidth = 680
        stack.addArrangedSubview(kiroSectionTitle("概览"))
        stack.addArrangedSubview(intro)
        stack.setCustomSpacing(22, after: intro)

        let statusHeader = kiroSectionTitle("状态")
        stack.addArrangedSubview(statusHeader)
        stack.setCustomSpacing(6, after: statusHeader)
        for row in [autoRow, rulesRow, learningRow, logRow] {
            stack.addArrangedSubview(row)
            row.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        }

        let pathHeader = kiroSectionTitle("路径")
        stack.setCustomSpacing(22, after: logRow)
        stack.addArrangedSubview(pathHeader)
        stack.setCustomSpacing(6, after: pathHeader)
        scroll.translatesAutoresizingMaskIntoConstraints = false
        stack.addArrangedSubview(scroll)
        scroll.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        scroll.heightAnchor.constraint(equalToConstant: 138).isActive = true

        let actions = NSStackView()
        actions.orientation = .horizontal
        actions.spacing = 10
        actions.alignment = .centerY
        actions.addArrangedSubview(makeButton("刷新", target: self, action: #selector(refreshAction)))
        actions.addArrangedSubview(copyButton)
        actions.addArrangedSubview(makeButton("打开数据目录", target: self, action: #selector(openData)))
        actions.addArrangedSubview(makeButton("打开日志目录", target: self, action: #selector(openLogs)))
        stack.setCustomSpacing(20, after: scroll)
        stack.addArrangedSubview(actions)
        for button in actions.arrangedSubviews {
            button.heightAnchor.constraint(equalToConstant: 32).isActive = true
            button.widthAnchor.constraint(greaterThanOrEqualToConstant: 112).isActive = true
        }
        refresh()
    }

    func refresh() {
        let rules = loadRules()
        let learning = loadLearning()
        let log = loadLogSettings()
        let zshrc = AppPaths.home.appendingPathComponent(".zshrc")
        let autoStart = (try? String(contentsOf: zshrc, encoding: .utf8))?
            .contains("# offline-complete integration v2") ?? false
        let marker = AppPaths.dataDir.appendingPathComponent("app.pid")
        var appRunning = false
        if let value = try? String(contentsOf: marker, encoding: .utf8),
           let pid = Int32(value.trimmingCharacters(in: .whitespacesAndNewlines)) {
            appRunning = kill(pid, 0) == 0
        }

        if appRunning {
            autoRow.setStatus("运行中", color: NSColor.systemGreen)
        } else if autoStart {
            autoRow.setStatus("等待应用", color: KiroTheme.muted)
        } else {
            autoRow.setStatus("未启用", color: KiroTheme.muted)
        }
        if let error = rules.error {
            rulesRow.setStatus("加载失败", color: NSColor.systemRed)
            rulesRow.toolTip = error
        } else {
            rulesRow.setStatus("已加载", color: NSColor.systemGreen)
        }
        if learning.learning {
            learningRow.setStatus("已启用", color: NSColor.systemGreen)
        } else {
            learningRow.setStatus("已关闭", color: KiroTheme.muted)
        }
        logRow.setStatus(log.enabled ? "已启用" : "已关闭", color: log.enabled ? NSColor.systemGreen : KiroTheme.muted)
        if log.enabled {
            logRow.toolTip = log.directory.path
        }

        var paths: [String] = []
        paths.append("应用\t\(Bundle.main.bundleURL.path)")
        paths.append("规则\t\(rules.path.isEmpty ? "未找到" : rules.path)")
        paths.append("数据\t\(AppPaths.dataDir.path)")
        paths.append("套接字\t\(AppPaths.socketBase.path)")
        paths.append("日志\t\(log.currentURL.path)")
        let paragraph = NSMutableParagraphStyle()
        paragraph.tabStops = [NSTextTab(textAlignment: .left, location: 88, options: [:])]
        let attributes: [NSAttributedString.Key: Any] = [
            .font: NSFont.monospacedSystemFont(ofSize: 11.5, weight: .regular),
            .foregroundColor: KiroTheme.title,
            .paragraphStyle: paragraph,
        ]
        text.textStorage?.setAttributedString(NSAttributedString(
            string: paths.joined(separator: "\n"),
            attributes: attributes
        ))
        text.scrollToBeginningOfDocument(nil)
    }

    @objc private func refreshAction() {
        refresh()
    }

    @objc private func openData() {
        try? FileManager.default.createDirectory(
            at: AppPaths.dataDir,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
        NSWorkspace.shared.open(AppPaths.dataDir)
    }

    @objc private func openLogs() {
        let directory = loadLogSettings().directory
        try? FileManager.default.createDirectory(
            at: directory,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
        NSWorkspace.shared.open(directory)
    }

    @objc private func copyCommand(_ sender: KiroButton) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString("oc", forType: .string)
        sender.setDisplayTitle("已复制")
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
            sender.setDisplayTitle("复制 oc 命令")
        }
    }
}

// MARK: - Rules panel

final class RulesPanel: NSObject, NSOutlineViewDataSource, NSOutlineViewDelegate {
    let view = NSView()
    private let summary = makeLabel("", size: 12.5)
    private let outline = NSOutlineView()
    private var commands: [RuleNode] = []

    override init() {
        view.frame = NSRect(x: 0, y: 0, width: 840, height: 732)
        view.wantsLayer = true
        view.layer?.backgroundColor = KiroTheme.background.cgColor
        super.init()
        let title = kiroSectionTitle("CLI 补全")
        title.frame = NSRect(x: KiroTheme.contentInset, y: 664, width: 400, height: 24)
        title.autoresizingMask = [.minYMargin]
        view.addSubview(title)
        summary.frame = NSRect(x: KiroTheme.contentInset, y: 638, width: 740, height: 18)
        summary.autoresizingMask = [.width, .minYMargin]
        summary.textColor = KiroTheme.muted
        summary.lineBreakMode = .byTruncatingTail
        view.addSubview(summary)

        let name = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("name"))
        name.title = "规则"
        name.width = 300
        let detail = NSTableColumn(identifier: NSUserInterfaceItemIdentifier("detail"))
        detail.title = "说明"
        detail.width = 440
        outline.addTableColumn(name)
        outline.addTableColumn(detail)
        outline.outlineTableColumn = name
        outline.headerView = nil
        outline.rowHeight = 30
        outline.indentationPerLevel = 16
        outline.usesAlternatingRowBackgroundColors = false
        outline.backgroundColor = .clear
        outline.gridStyleMask = []
        outline.columnAutoresizingStyle = .lastColumnOnlyAutoresizingStyle
        outline.focusRingType = .none
        outline.dataSource = self
        outline.delegate = self

        let scroll = NSScrollView()
        scroll.frame = NSRect(x: KiroTheme.contentInset, y: 70, width: 760, height: 550)
        scroll.autoresizingMask = [.width, .height]
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.borderType = .noBorder
        scroll.drawsBackground = false
        scroll.wantsLayer = true
        scroll.layer?.cornerRadius = 8
        scroll.layer?.borderWidth = 1
        scroll.layer?.borderColor = KiroTheme.border.cgColor
        outline.autoresizingMask = [.width]
        scroll.documentView = outline
        view.addSubview(scroll)

        let refresh = makeButton("刷新", target: self, action: #selector(refreshAction))
        refresh.frame = NSRect(x: KiroTheme.contentInset, y: 22, width: 86, height: 32)
        refresh.autoresizingMask = [.maxYMargin]
        view.addSubview(refresh)
    }

    func refresh() {
        let snapshot = loadRules()
        commands = snapshot.commands
        if let error = snapshot.error {
            summary.stringValue = "规则加载失败：\(error)  \(snapshot.path)"
            summary.textColor = .systemRed
        } else {
            summary.stringValue = "\(snapshot.topCount) 个命令 · \(snapshot.total) 条规则 · sha256 \(snapshot.sha)"
            summary.textColor = KiroTheme.muted
        }
        outline.reloadData()
        // The full Kiro import has hundreds of top-level commands and tens of
        // thousands of descendants. Expanding everything would make the view
        // unresponsive; small packs keep the old convenience behavior.
        if commands.count < 100 {
            for command in commands {
                outline.expandItem(command, expandChildren: true)
            }
        }
    }

    @objc private func refreshAction() {
        refresh()
    }

    func outlineView(_ outlineView: NSOutlineView, numberOfChildrenOfItem item: Any?) -> Int {
        if let node = item as? RuleNode {
            return node.children.count
        }
        return commands.count
    }

    func outlineView(_ outlineView: NSOutlineView, child index: Int, ofItem item: Any?) -> Any {
        if let node = item as? RuleNode {
            return node.children[index]
        }
        return commands[index]
    }

    func outlineView(_ outlineView: NSOutlineView, isItemExpandable item: Any) -> Bool {
        (item as? RuleNode)?.children.isEmpty == false
    }

    func outlineView(_ outlineView: NSOutlineView, viewFor tableColumn: NSTableColumn?, item: Any) -> NSView? {
        guard let node = item as? RuleNode, let column = tableColumn else { return nil }
        let value = column.identifier.rawValue == "name" ? node.name : node.detail
        let cell = NSTableCellView()
        cell.identifier = column.identifier
        let field = NSTextField(labelWithString: value)
        field.font = column.identifier.rawValue == "name"
            ? .systemFont(ofSize: 13, weight: .medium)
            : .systemFont(ofSize: 12)
        field.textColor = column.identifier.rawValue == "name" ? KiroTheme.title : KiroTheme.muted
        field.lineBreakMode = .byTruncatingTail
        field.translatesAutoresizingMaskIntoConstraints = false
        cell.addSubview(field)
        cell.textField = field
        NSLayoutConstraint.activate([
            field.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 8),
            field.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -8),
            field.centerYAnchor.constraint(equalTo: cell.centerYAnchor),
        ])
        return cell
    }
}

// MARK: - Learning panel

final class LearningPanel: NSObject, NSTableViewDataSource, NSTableViewDelegate {
    let view = NSView()
    private let summary = makeLabel("", size: 12.5)
    private let table = NSTableView()
    private let toggle = KiroSwitch(target: nil, action: nil)
    private var entries: [LearningEntry] = []
    private let dateFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateFormat = "MM-dd HH:mm"
        return formatter
    }()

    override init() {
        view.frame = NSRect(x: 0, y: 0, width: 840, height: 732)
        view.wantsLayer = true
        view.layer?.backgroundColor = KiroTheme.background.cgColor
        super.init()
        let title = kiroSectionTitle("学习")
        title.frame = NSRect(x: KiroTheme.contentInset, y: 664, width: 400, height: 24)
        title.autoresizingMask = [.minYMargin]
        view.addSubview(title)
        summary.frame = NSRect(x: KiroTheme.contentInset, y: 638, width: 740, height: 18)
        summary.autoresizingMask = [.width, .minYMargin]
        summary.textColor = KiroTheme.muted
        summary.lineBreakMode = .byTruncatingTail
        view.addSubview(summary)

        let columns: [(String, String, CGFloat)] = [
            ("context", "上下文", 280),
            ("candidate", "候选", 190),
            ("count", "次数", 60),
            ("last", "最近", 150),
        ]
        for (identifier, title, width) in columns {
            let column = NSTableColumn(identifier: NSUserInterfaceItemIdentifier(identifier))
            column.title = title
            column.width = width
            table.addTableColumn(column)
        }
        table.headerView = nil
        table.rowHeight = 30
        table.usesAlternatingRowBackgroundColors = false
        table.backgroundColor = .clear
        table.gridStyleMask = []
        table.columnAutoresizingStyle = .noColumnAutoresizing
        table.focusRingType = .none
        table.frame = NSRect(x: 0, y: 0, width: 680, height: 0)
        table.dataSource = self
        table.delegate = self

        let headers: [(String, CGFloat, CGFloat)] = [
            ("上下文", 48, 270),
            ("候选", 328, 180),
            ("次数", 518, 50),
            ("最近", 578, 150),
        ]
        for (text, x, width) in headers {
            let header = makeLabel(text, size: 11.5, weight: .semibold)
            header.textColor = KiroTheme.muted
            header.frame = NSRect(x: x, y: 596, width: width, height: 18)
            header.autoresizingMask = [.maxXMargin, .minYMargin]
            view.addSubview(header)
        }

        let scroll = NSScrollView()
        scroll.frame = NSRect(x: KiroTheme.contentInset, y: 70, width: 760, height: 514)
        scroll.autoresizingMask = [.width, .height]
        scroll.hasVerticalScroller = true
        scroll.autohidesScrollers = true
        scroll.borderType = .noBorder
        scroll.drawsBackground = false
        scroll.wantsLayer = true
        scroll.layer?.cornerRadius = 8
        scroll.layer?.borderWidth = 1
        scroll.layer?.borderColor = KiroTheme.border.cgColor
        table.autoresizingMask = [.width]
        scroll.documentView = table
        view.addSubview(scroll)

        toggle.target = self
        toggle.action = #selector(toggleLearning(_:))
        toggle.frame = NSRect(x: KiroTheme.contentInset, y: 29, width: 44, height: 26)
        toggle.autoresizingMask = [.maxYMargin]
        view.addSubview(toggle)
        let toggleLabel = makeLabel("启用学习", size: 13)
        toggleLabel.textColor = KiroTheme.title
        toggleLabel.frame = NSRect(x: KiroTheme.contentInset + 54, y: 32, width: 90, height: 20)
        toggleLabel.autoresizingMask = [.maxYMargin]
        view.addSubview(toggleLabel)

        let refresh = makeButton("刷新", target: self, action: #selector(refreshAction))
        refresh.frame = NSRect(x: KiroTheme.contentInset + 156, y: 24, width: 86, height: 32)
        refresh.autoresizingMask = [.maxYMargin]
        view.addSubview(refresh)

        let clear = makeButton("清除选择…", target: self, action: #selector(clearAction))
        clear.frame = NSRect(x: KiroTheme.contentInset + 252, y: 24, width: 126, height: 32)
        clear.autoresizingMask = [.maxYMargin]
        view.addSubview(clear)
    }

    func refresh() {
        let snapshot = loadLearning()
        entries = snapshot.entries
        toggle.state = snapshot.learning ? .on : .off
        if let error = snapshot.error {
            summary.stringValue = "学习数据加载失败：\(error)  \(snapshot.path)"
            summary.textColor = .systemRed
        } else {
            let state = snapshot.learning ? "已启用" : "已关闭（已记录的选择仍保留）"
            summary.stringValue = "\(state) · \(entries.count) 条选择 · \(formatBytes(snapshot.bytes)) · \(snapshot.path)"
            summary.textColor = KiroTheme.muted
        }
        table.reloadData()
    }

    @objc private func refreshAction() {
        refresh()
    }

    @objc private func toggleLearning(_ sender: KiroSwitch) {
        let enabled = sender.state == .on
        let (code, output) = runTool("oc-manage", ["learning", enabled ? "on" : "off"])
        if code != 0 {
            showAlert("修改学习设置失败", output.isEmpty ? "oc-manage 返回 \(code)" : output)
            sender.state = enabled ? .off : .on
            return
        }
        refresh()
    }

    @objc private func clearAction() {
        let alert = NSAlert()
        alert.messageText = "清除全部学习选择？"
        alert.informativeText = "只删除本机的选择计数和最近使用时间，不会删除规则或日志。"
        alert.addButton(withTitle: "清除")
        alert.addButton(withTitle: "取消")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let (code, output) = runTool("oc-manage", ["clear", "--yes"])
        if code != 0 {
            showAlert("清除失败", output.isEmpty ? "oc-manage 返回 \(code)" : output)
            return
        }
        refresh()
    }

    func numberOfRows(in tableView: NSTableView) -> Int {
        entries.count
    }

    func tableView(_ tableView: NSTableView, viewFor tableColumn: NSTableColumn?, row: Int) -> NSView? {
        guard let column = tableColumn, row >= 0, row < entries.count else { return nil }
        let entry = entries[row]
        let value: String
        switch column.identifier.rawValue {
        case "context": value = entry.context
        case "candidate": value = entry.candidate
        case "count": value = String(entry.count)
        case "last": value = entry.last.map { dateFormatter.string(from: $0) } ?? "-"
        default: value = ""
        }
        let cell = NSTableCellView()
        cell.identifier = column.identifier
        let field = NSTextField(labelWithString: value)
        field.font = .systemFont(ofSize: 12.5)
        field.textColor = KiroTheme.title
        field.lineBreakMode = .byTruncatingTail
        if column.identifier.rawValue == "count" {
            field.alignment = .right
        }
        field.translatesAutoresizingMaskIntoConstraints = false
        cell.addSubview(field)
        cell.textField = field
        NSLayoutConstraint.activate([
            field.leadingAnchor.constraint(equalTo: cell.leadingAnchor, constant: 8),
            field.trailingAnchor.constraint(equalTo: cell.trailingAnchor, constant: -8),
            field.centerYAnchor.constraint(equalTo: cell.centerYAnchor),
        ])
        return cell
    }
}

// MARK: - Logs panel

final class LogPanel: NSObject {
    let view = NSView()
    private let summary = makeLabel("", size: 12.5)
    private let filePopup = NSPopUpButton(frame: .zero, pullsDown: false)
    private let scroll: NSScrollView
    private let text: NSTextView
    private let autoRefresh = KiroSwitch(target: nil, action: nil)
    private let autoScroll = KiroSwitch(target: nil, action: nil)
    private var settings = loadLogSettings()
    private var files: [URL] = []
    private var selectedURL: URL?
    private var lastSize = -1
    private var lastModified: Date?

    override init() {
        view.frame = NSRect(x: 0, y: 0, width: 840, height: 732)
        view.wantsLayer = true
        view.layer?.backgroundColor = KiroTheme.background.cgColor
        (scroll, text) = makeTextScroll()
        super.init()
        let title = kiroSectionTitle("日志")
        title.frame = NSRect(x: KiroTheme.contentInset, y: 664, width: 400, height: 24)
        title.autoresizingMask = [.minYMargin]
        view.addSubview(title)
        summary.frame = NSRect(x: KiroTheme.contentInset, y: 638, width: 560, height: 18)
        summary.autoresizingMask = [.maxXMargin, .minYMargin]
        summary.textColor = KiroTheme.muted
        summary.lineBreakMode = .byTruncatingMiddle
        view.addSubview(summary)

        filePopup.frame = NSRect(x: 620, y: 631, width: 180, height: 26)
        filePopup.autoresizingMask = [.minXMargin, .minYMargin]
        filePopup.target = self
        filePopup.action = #selector(fileChanged(_:))
        view.addSubview(filePopup)

        scroll.frame = NSRect(x: KiroTheme.contentInset, y: 70, width: 760, height: 550)
        scroll.autoresizingMask = [.width, .height]
        view.addSubview(scroll)

        autoRefresh.state = .on
        autoRefresh.frame = NSRect(x: KiroTheme.contentInset, y: 29, width: 44, height: 26)
        autoRefresh.autoresizingMask = [.maxYMargin]
        view.addSubview(autoRefresh)
        let refreshLabel = makeLabel("自动刷新", size: 13)
        refreshLabel.textColor = KiroTheme.title
        refreshLabel.frame = NSRect(x: KiroTheme.contentInset + 52, y: 32, width: 74, height: 20)
        refreshLabel.autoresizingMask = [.maxYMargin]
        view.addSubview(refreshLabel)

        autoScroll.state = .on
        autoScroll.frame = NSRect(x: KiroTheme.contentInset + 136, y: 29, width: 44, height: 26)
        autoScroll.autoresizingMask = [.maxYMargin]
        view.addSubview(autoScroll)
        let scrollLabel = makeLabel("自动滚动", size: 13)
        scrollLabel.textColor = KiroTheme.title
        scrollLabel.frame = NSRect(x: KiroTheme.contentInset + 188, y: 32, width: 74, height: 20)
        scrollLabel.autoresizingMask = [.maxYMargin]
        view.addSubview(scrollLabel)

        let refresh = makeButton("刷新", target: self, action: #selector(refreshAction))
        refresh.frame = NSRect(x: KiroTheme.contentInset + 272, y: 24, width: 86, height: 32)
        refresh.autoresizingMask = [.maxYMargin]
        view.addSubview(refresh)
        let revealButton = makeButton("在 Finder 中显示", target: self, action: #selector(revealAction))
        revealButton.frame = NSRect(x: KiroTheme.contentInset + 368, y: 24, width: 148, height: 32)
        revealButton.autoresizingMask = [.maxYMargin]
        view.addSubview(revealButton)
        let clear = makeButton("清空日志", target: self, action: #selector(clearAction))
        clear.frame = NSRect(x: KiroTheme.contentInset + 526, y: 24, width: 106, height: 32)
        clear.autoresizingMask = [.maxYMargin]
        view.addSubview(clear)

        reloadSettings()
    }

    func reloadSettings() {
        let previousCurrent = settings.currentURL.path
        settings = loadLogSettings()
        files = listLogFiles(settings)
        let previous = selectedURL?.path
        filePopup.removeAllItems()
        for url in files {
            filePopup.addItem(withTitle: url.lastPathComponent)
        }
        var index = 0
        if let previous, previous != previousCurrent, let found = files.firstIndex(where: { $0.path == previous }) {
            index = found
        } else if let found = files.firstIndex(where: { $0.path == settings.currentURL.path }) {
            index = found
        }
        if !files.isEmpty {
            filePopup.selectItem(at: index)
            selectedURL = files[index]
            filePopup.isEnabled = true
        } else {
            selectedURL = settings.currentURL
            filePopup.isEnabled = false
        }
        lastSize = -1
        lastModified = nil
        refresh(force: true)
    }

    func tick() {
        guard autoRefresh.state == .on else { return }
        let latest = loadLogSettings()
        if latest.currentURL.path != settings.currentURL.path ||
            latest.enabled != settings.enabled ||
            latest.retentionDays != settings.retentionDays ||
            latest.error != settings.error {
            reloadSettings()
            return
        }
        refresh(force: false)
    }

    func refresh(force: Bool) {
        let url = selectedURL ?? settings.currentURL
        let attributes = try? FileManager.default.attributesOfItem(atPath: url.path)
        let size = (attributes?[.size] as? NSNumber)?.intValue ?? 0
        let modified = attributes?[.modificationDate] as? Date
        if !force && size == lastSize && modified == lastModified {
            return
        }
        lastSize = size
        lastModified = modified

        if !settings.enabled {
            summary.stringValue = "日志已关闭 · \(settings.directory.path)"
            summary.textColor = KiroTheme.muted
        } else if let error = settings.error {
            summary.stringValue = "日志设置无效：\(error)"
            summary.textColor = .systemRed
        } else if FileManager.default.fileExists(atPath: url.path) {
            summary.stringValue = "日志 · \(formatBytes(size)) · \(url.path)"
            summary.textColor = KiroTheme.muted
        } else {
            summary.stringValue = "日志尚未生成 · \(url.path)"
            summary.textColor = KiroTheme.muted
        }

        guard let data = try? Data(contentsOf: url) else {
            if settings.enabled {
                text.string = "日志尚未生成。运行 oc 后，agent 会把启动、补全、选择和错误事件写入这里。\n\n\(url.path)"
            } else {
                text.string = "日志已关闭。可在“设置”页启用后重新启动 oc。\n\n\(settings.currentURL.path)"
            }
            return
        }
        let tail = data.count > 256 * 1024 ? data.suffix(256 * 1024) : data
        var content = String(decoding: tail, as: UTF8.self)
        if content.isEmpty {
            content = "日志为空。"
        }
        text.string = content
        if autoScroll.state == .on {
            text.scrollToEndOfDocument(nil)
        }
    }

    @objc private func fileChanged(_ sender: NSPopUpButton) {
        let index = sender.indexOfSelectedItem
        guard index >= 0, index < files.count else { return }
        selectedURL = files[index]
        lastSize = -1
        lastModified = nil
        refresh(force: true)
    }

    @objc private func refreshAction() {
        reloadSettings()
    }

    @objc private func revealAction() {
        let url = selectedURL ?? settings.currentURL
        if FileManager.default.fileExists(atPath: url.path) {
            reveal(url)
        } else {
            try? FileManager.default.createDirectory(
                at: settings.directory,
                withIntermediateDirectories: true,
                attributes: [.posixPermissions: 0o700]
            )
            NSWorkspace.shared.open(settings.directory)
        }
    }

    @objc private func clearAction() {
        let url = selectedURL ?? settings.currentURL
        let alert = NSAlert()
        alert.messageText = "清空当前日志？"
        alert.informativeText = "\(url.lastPathComponent) 会立即被截断，正在运行的 oc 会继续写入新日志。"
        alert.addButton(withTitle: "清空")
        alert.addButton(withTitle: "取消")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        do {
            try FileManager.default.createDirectory(
                at: settings.directory,
                withIntermediateDirectories: true,
                attributes: [.posixPermissions: 0o700]
            )
            try Data().write(to: url)
            refresh(force: true)
        } catch {
            showAlert("清空日志失败", error.localizedDescription)
        }
    }
}

// MARK: - AI settings panel

final class AIPanel: NSObject {
    let view = NSView()
    private let enabled = KiroSwitch(target: nil, action: nil)
    private let protocolButton = NSPopUpButton(frame: .zero, pullsDown: false)
    private let url = NSTextField()
    private let key = NSSecureTextField()
    private let model = NSTextField()
    private let maxTokens = NSTextField()
    private let status = makeLabel("", size: 12)
    private let note = makeLabel("", size: 12)

    override init() {
        super.init()
        view.frame = NSRect(x: 0, y: 0, width: 840, height: 732)
        view.wantsLayer = true
        view.layer?.backgroundColor = KiroTheme.background.cgColor

        let stack = NSStackView()
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 12
        stack.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: KiroTheme.contentInset),
            stack.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -KiroTheme.contentInset),
            stack.topAnchor.constraint(equalTo: view.topAnchor, constant: 34),
        ])

        let intro = kiroBody("可选 AI 补全只支持 OpenAI Chat Completions 和 Anthropic Messages。关闭时不会发起任何 AI 请求。")
        intro.preferredMaxLayoutWidth = 680
        stack.addArrangedSubview(kiroSectionTitle("AI 补全"))
        stack.addArrangedSubview(intro)

        let enableRow = NSView()
        enableRow.translatesAutoresizingMaskIntoConstraints = false
        enabled.target = self
        enabled.action = #selector(changed)
        enabled.translatesAutoresizingMaskIntoConstraints = false
        let enableTitle = kiroTitle("启用 AI 补全")
        let enableDetail = kiroBody("使用下方 endpoint；API key 保存在 ~/.oc/config.json（0600）。")
        enableDetail.maximumNumberOfLines = 1
        let enableText = NSStackView(views: [enableTitle, enableDetail])
        enableText.orientation = .vertical
        enableText.alignment = .leading
        enableText.spacing = 3
        enableText.translatesAutoresizingMaskIntoConstraints = false
        enableRow.addSubview(enabled)
        enableRow.addSubview(enableText)
        NSLayoutConstraint.activate([
            enabled.leadingAnchor.constraint(equalTo: enableRow.leadingAnchor),
            enabled.widthAnchor.constraint(equalToConstant: 44),
            enabled.heightAnchor.constraint(equalToConstant: 26),
            enabled.centerYAnchor.constraint(equalTo: enableTitle.centerYAnchor),
            enableText.leadingAnchor.constraint(equalTo: enabled.trailingAnchor, constant: 16),
            enableText.trailingAnchor.constraint(equalTo: enableRow.trailingAnchor),
            enableText.topAnchor.constraint(equalTo: enableRow.topAnchor),
            enableText.bottomAnchor.constraint(equalTo: enableRow.bottomAnchor),
        ])
        stack.addArrangedSubview(enableRow)
        enableRow.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true

        protocolButton.addItems(withTitles: ["OpenAI Chat Completions", "Anthropic Messages"])
        protocolButton.controlSize = .regular
        protocolButton.font = .systemFont(ofSize: 13)
        protocolButton.widthAnchor.constraint(equalToConstant: 240).isActive = true
        stack.setCustomSpacing(22, after: enableRow)
        stack.addArrangedSubview(fieldLabel("协议"))
        stack.setCustomSpacing(6, after: stack.arrangedSubviews.last!)
        stack.addArrangedSubview(protocolButton)

        configureField(url, placeholder: "https://api.openai.com/v1/chat/completions")
        configureField(key, placeholder: "API key")
        configureField(model, placeholder: "gpt-4o-mini 或 claude-3-5-sonnet-latest")
        configureField(maxTokens, placeholder: "1024")

        for (title, field) in [
            ("Endpoint URL", url),
            ("API Key", key),
            ("模型", model),
            ("Max Tokens（Anthropic）", maxTokens),
        ] {
            stack.setCustomSpacing(16, after: stack.arrangedSubviews.last!)
            stack.addArrangedSubview(fieldLabel(title))
            stack.setCustomSpacing(6, after: stack.arrangedSubviews.last!)
            stack.addArrangedSubview(field)
            field.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
            field.heightAnchor.constraint(equalToConstant: 32).isActive = true
        }

        let actions = NSStackView()
        actions.orientation = .horizontal
        actions.spacing = 10
        actions.alignment = .centerY
        let save = KiroButton(title: "保存 AI 设置", style: .primary, target: self, action: #selector(saveSettings))
        let clear = makeButton("清除 API Key", target: self, action: #selector(clearKey))
        for button in [save, clear] {
            button.heightAnchor.constraint(equalToConstant: 32).isActive = true
            button.widthAnchor.constraint(equalToConstant: 136).isActive = true
            actions.addArrangedSubview(button)
        }
        stack.setCustomSpacing(22, after: stack.arrangedSubviews.last!)
        stack.addArrangedSubview(actions)

        status.translatesAutoresizingMaskIntoConstraints = false
        note.translatesAutoresizingMaskIntoConstraints = false
        note.maximumNumberOfLines = 2
        note.lineBreakMode = .byWordWrapping
        stack.setCustomSpacing(14, after: actions)
        stack.addArrangedSubview(status)
        stack.addArrangedSubview(note)
        status.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        note.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true

        let foot = kiroBody("AI 请求只在触发 AI generator 时发生；静态和其他 generator 不受影响。")
        foot.textColor = KiroTheme.muted
        stack.setCustomSpacing(18, after: note)
        stack.addArrangedSubview(foot)
        refresh()
    }

    private func configureField(_ field: NSTextField, placeholder: String) {
        field.placeholderString = placeholder
        field.font = .systemFont(ofSize: 13)
        field.isBezeled = true
        field.isBordered = true
        field.drawsBackground = true
        field.backgroundColor = KiroTheme.surface
        field.textColor = KiroTheme.title
        field.focusRingType = .none
    }

    private func fieldLabel(_ text: String) -> NSTextField {
        let label = makeLabel(text, size: 13, weight: .semibold)
        label.textColor = KiroTheme.title
        return label
    }

    func refresh() {
        let settings = loadAISettings()
        enabled.state = settings.enabled ? .on : .off
        protocolButton.selectItem(at: settings.protocolName == "anthropic" ? 1 : 0)
        url.stringValue = settings.url
        key.stringValue = settings.key
        model.stringValue = settings.model
        maxTokens.stringValue = String(settings.maxTokens)
        if let error = settings.error {
            status.stringValue = "设置文件无效：\(error)"
            status.textColor = .systemRed
        } else {
            status.stringValue = settings.enabled ? "AI 补全已启用" : "AI 补全已关闭"
            status.textColor = settings.enabled ? .systemGreen : KiroTheme.muted
        }
        note.stringValue = settings.enabled ? "" : "关闭时不会发起 AI 请求。"
        note.textColor = .systemOrange
    }

    @objc private func changed() {
        note.stringValue = enabled.state == .on ? "" : "关闭时不会发起 AI 请求。"
        note.textColor = .systemOrange
    }

    @objc private func saveSettings() {
        let on = enabled.state == .on
        let endpoint = url.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if on && endpoint.isEmpty {
            showAlert("AI 设置不完整", "启用 AI 补全时必须填写 Endpoint URL。")
            return
        }
        var args = [
            "ai", "set",
            "--enabled", on ? "on" : "off",
            "--protocol", protocolButton.indexOfSelectedItem == 1 ? "anthropic" : "openai",
            "--url", endpoint,
            "--model", model.stringValue.trimmingCharacters(in: .whitespacesAndNewlines),
            "--max-tokens", String(max(1, min(32768, Int(maxTokens.stringValue) ?? 1024))),
        ]
        let keyValue = key.stringValue
        if keyValue.isEmpty {
            args.append("--clear-key")
        } else {
            args += ["--key", keyValue]
        }
        let (code, output) = runTool("oc-manage", args)
        if code != 0 {
            showAlert("保存 AI 设置失败", output.isEmpty ? "oc-manage 返回 \(code)" : output)
            return
        }
        refresh()
        status.stringValue = "已保存；新开的 oc 会话使用新设置。"
        status.textColor = .systemGreen
    }

    @objc private func clearKey() {
        key.stringValue = ""
        saveSettings()
    }
}

// MARK: - Settings panel

final class SettingsPanel: NSObject {
    let view = NSView()
    private let enabled = KiroSwitch(target: nil, action: nil)
    private let directory = NSTextField()
    private let retention = NSPopUpButton(frame: .zero, pullsDown: false)
    private let status = makeLabel("", size: 12)
    private let note = makeLabel("", size: 12)
    private var retentionOptions = [1, 3, 7, 14, 30]
    var onChange: (() -> Void)?

    init(onChange: (() -> Void)?) {
        self.onChange = onChange
        super.init()
        view.frame = NSRect(x: 0, y: 0, width: 840, height: 732)
        view.wantsLayer = true
        view.layer?.backgroundColor = KiroTheme.background.cgColor

        let stack = NSStackView()
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 16
        stack.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: view.leadingAnchor, constant: KiroTheme.contentInset),
            stack.trailingAnchor.constraint(equalTo: view.trailingAnchor, constant: -KiroTheme.contentInset),
            stack.topAnchor.constraint(equalTo: view.topAnchor, constant: 34),
        ])

        let intro = kiroBody("控制 agent 是否写日志、日志存储目录和保留天数。")
        intro.preferredMaxLayoutWidth = 680
        stack.addArrangedSubview(kiroSectionTitle("设置"))
        stack.addArrangedSubview(intro)
        stack.setCustomSpacing(22, after: intro)

        let logHeader = kiroSectionTitle("日志")
        stack.addArrangedSubview(logHeader)
        stack.setCustomSpacing(8, after: logHeader)

        enabled.target = self
        enabled.action = #selector(settingChanged)
        enabled.translatesAutoresizingMaskIntoConstraints = false
        let enabledRow = NSView()
        enabledRow.translatesAutoresizingMaskIntoConstraints = false
        let enabledTitle = kiroTitle("启用 agent 日志")
        let enabledDetail = kiroBody("写入启动、补全、选择和错误事件；只记录元数据。")
        enabledDetail.lineBreakMode = .byTruncatingTail
        enabledDetail.maximumNumberOfLines = 1
        let enabledText = NSStackView(views: [enabledTitle, enabledDetail])
        enabledText.orientation = .vertical
        enabledText.alignment = .leading
        enabledText.spacing = 3
        enabledText.translatesAutoresizingMaskIntoConstraints = false
        enabledRow.addSubview(enabled)
        enabledRow.addSubview(enabledText)
        NSLayoutConstraint.activate([
            enabled.leadingAnchor.constraint(equalTo: enabledRow.leadingAnchor),
            enabled.widthAnchor.constraint(equalToConstant: 44),
            enabled.heightAnchor.constraint(equalToConstant: 26),
            enabled.centerYAnchor.constraint(equalTo: enabledTitle.centerYAnchor),
            enabledText.leadingAnchor.constraint(equalTo: enabled.trailingAnchor, constant: 16),
            enabledText.trailingAnchor.constraint(equalTo: enabledRow.trailingAnchor),
            enabledText.topAnchor.constraint(equalTo: enabledRow.topAnchor),
            enabledText.bottomAnchor.constraint(equalTo: enabledRow.bottomAnchor),
        ])
        stack.addArrangedSubview(enabledRow)
        enabledRow.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true

        let directoryLabel = makeLabel("存储目录", size: 14, weight: .semibold)
        directoryLabel.textColor = KiroTheme.title
        stack.setCustomSpacing(26, after: enabledRow)
        stack.addArrangedSubview(directoryLabel)
        let directoryRow = NSStackView()
        directoryRow.orientation = .horizontal
        directoryRow.alignment = .centerY
        directoryRow.spacing = 10

        let directoryBox = NSView()
        directoryBox.wantsLayer = true
        directoryBox.layer?.cornerRadius = 7
        directoryBox.layer?.backgroundColor = KiroTheme.surface.cgColor
        directoryBox.layer?.borderWidth = 1
        directoryBox.layer?.borderColor = KiroTheme.border.cgColor
        directoryBox.translatesAutoresizingMaskIntoConstraints = false
        directory.isBordered = false
        directory.isBezeled = false
        directory.drawsBackground = false
        directory.focusRingType = .none
        directory.font = .monospacedSystemFont(ofSize: 12, weight: .regular)
        directory.textColor = KiroTheme.title
        directory.placeholderString = "~/.oc/logs"
        directory.translatesAutoresizingMaskIntoConstraints = false
        directoryBox.addSubview(directory)
        NSLayoutConstraint.activate([
            directory.leadingAnchor.constraint(equalTo: directoryBox.leadingAnchor, constant: 12),
            directory.trailingAnchor.constraint(equalTo: directoryBox.trailingAnchor, constant: -12),
            directory.centerYAnchor.constraint(equalTo: directoryBox.centerYAnchor),
            directoryBox.heightAnchor.constraint(equalToConstant: 34),
        ])
        directoryRow.addArrangedSubview(directoryBox)
        directoryBox.widthAnchor.constraint(equalTo: directoryRow.widthAnchor, constant: -222).isActive = true
        let choose = makeButton("选择…", target: self, action: #selector(chooseDirectory))
        let defaults = makeButton("恢复默认", target: self, action: #selector(useDefaults))
        for button in [choose, defaults] {
            button.heightAnchor.constraint(equalToConstant: 32).isActive = true
            button.widthAnchor.constraint(equalToConstant: 96).isActive = true
            directoryRow.addArrangedSubview(button)
        }
        stack.addArrangedSubview(directoryRow)
        directoryRow.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true

        let retentionLabel = makeLabel("保留天数", size: 14, weight: .semibold)
        retentionLabel.textColor = KiroTheme.title
        stack.setCustomSpacing(22, after: directoryRow)
        stack.addArrangedSubview(retentionLabel)
        retention.controlSize = .regular
        retention.font = .systemFont(ofSize: 13)
        retention.widthAnchor.constraint(equalToConstant: 140).isActive = true
        retention.heightAnchor.constraint(equalToConstant: 30).isActive = true
        stack.addArrangedSubview(retention)

        let actions = NSStackView()
        actions.orientation = .horizontal
        actions.spacing = 10
        actions.alignment = .centerY
        let save = KiroButton(title: "保存设置", style: .primary, target: self, action: #selector(saveSettings))
        let open = makeButton("打开日志目录", target: self, action: #selector(openDirectory))
        for button in [save, open] {
            button.heightAnchor.constraint(equalToConstant: 32).isActive = true
            button.widthAnchor.constraint(equalToConstant: 128).isActive = true
            actions.addArrangedSubview(button)
        }
        stack.setCustomSpacing(28, after: retention)
        stack.addArrangedSubview(actions)

        status.translatesAutoresizingMaskIntoConstraints = false
        status.lineBreakMode = .byTruncatingMiddle
        note.translatesAutoresizingMaskIntoConstraints = false
        note.lineBreakMode = .byWordWrapping
        note.maximumNumberOfLines = 2
        stack.setCustomSpacing(18, after: actions)
        stack.addArrangedSubview(status)
        stack.addArrangedSubview(note)
        status.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
        note.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true

        let environment = kiroBody("默认：~/.oc/logs/agent-YYYY-MM-DD.log，保留今天和前两天。运行中的 oc 在下一次日志事件时使用新设置；环境变量 OFFLINE_COMPLETE_LOG* 会覆盖设置。")
        environment.textColor = KiroTheme.muted
        environment.preferredMaxLayoutWidth = 700
        stack.setCustomSpacing(20, after: note)
        stack.addArrangedSubview(environment)
        environment.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true

        refresh()
    }

    func refresh() {
        let settings = loadLogSettings()
        enabled.state = settings.enabled ? .on : .off
        directory.stringValue = settings.directory.path
        if !retentionOptions.contains(settings.retentionDays) {
            retentionOptions.append(settings.retentionDays)
            retentionOptions.sort()
        }
        retention.removeAllItems()
        retention.addItems(withTitles: retentionOptions.map { "\($0) 天" })
        if let index = retentionOptions.firstIndex(of: settings.retentionDays) {
            retention.selectItem(at: index)
        }
        if let error = settings.error {
            status.stringValue = "设置文件无效：\(error)"
            status.textColor = .systemRed
        } else {
            status.stringValue = "当前文件：\(settings.currentURL.path)"
            status.textColor = KiroTheme.muted
        }
        note.stringValue = settings.enabled ? "" : "日志已关闭；运行中的 oc 会在下一次日志事件时停止写入。"
        note.textColor = .systemOrange
    }

    private var selectedRetentionDays: Int {
        let index = retention.indexOfSelectedItem
        guard index >= 0, index < retentionOptions.count else { return 3 }
        return retentionOptions[index]
    }

    @objc private func settingChanged() {
        note.stringValue = enabled.state == .on ? "" : "日志已关闭；运行中的 oc 会在下一次日志事件时停止写入。"
        note.textColor = .systemOrange
    }

    @objc private func chooseDirectory() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = "选择"
        let current = expandTilde(directory.stringValue)
        if !current.isEmpty {
            panel.directoryURL = URL(fileURLWithPath: current)
        }
        if panel.runModal() == .OK, let url = panel.url {
            directory.stringValue = url.path
        }
    }

    @objc private func useDefaults() {
        enabled.state = .on
        directory.stringValue = AppPaths.defaultLogDirectory.path
        retentionOptions = [1, 3, 7, 14, 30]
        retention.removeAllItems()
        retention.addItems(withTitles: retentionOptions.map { "\($0) 天" })
        retention.selectItem(at: 1)
        status.stringValue = "已恢复默认值；点击“保存设置”应用。"
        status.textColor = KiroTheme.muted
    }

    @objc private func saveSettings() {
        var path = expandTilde(directory.stringValue.trimmingCharacters(in: .whitespacesAndNewlines))
        if path.isEmpty {
            path = AppPaths.defaultLogDirectory.path
        }
        let (code, output) = runTool("oc-manage", [
            "logging", "set",
            "--enabled", enabled.state == .on ? "on" : "off",
            "--directory", path,
            "--retention-days", String(selectedRetentionDays),
        ])
        if code != 0 {
            showAlert("保存日志设置失败", output.isEmpty ? "oc-manage 返回 \(code)" : output)
            return
        }
        refresh()
        status.stringValue = "已保存；运行中的 oc 会在下一次日志事件时使用新设置。"
        status.textColor = .systemGreen
        onChange?()
    }

    @objc private func openDirectory() {
        let url = URL(fileURLWithPath: expandTilde(directory.stringValue))
        try? FileManager.default.createDirectory(
            at: url,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
        NSWorkspace.shared.open(url)
    }
}

// MARK: - Main window

final class MainWindowController: NSObject {
    let window: NSWindow
    private let container = NSView()
    private let overview = OverviewPanel()
    private let rules = RulesPanel()
    private let learning = LearningPanel()
    private let logs = LogPanel()
    private let ai = AIPanel()
    private lazy var settings = SettingsPanel(onChange: { [weak self] in
        self?.logs.reloadSettings()
        self?.overview.refresh()
    })
    private var panels: [(String, NSView)] = []
    private var sidebarItems: [(String, KiroSidebarItem)] = []
    private var timer: Timer?

    override init() {
        let contentSize = NSSize(width: 1100, height: 760)
        window = NSWindow(
            contentRect: NSRect(origin: .zero, size: contentSize),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        super.init()
        window.title = "Offline Complete"
        window.titlebarAppearsTransparent = true
        window.backgroundColor = KiroTheme.background
        window.minSize = NSSize(width: 980, height: 640)
        window.isReleasedWhenClosed = false
        window.setFrameAutosaveName("OfflineCompleteMainWindow")

        let content = NSView(frame: NSRect(origin: .zero, size: contentSize))
        content.wantsLayer = true
        content.layer?.backgroundColor = KiroTheme.background.cgColor

        let sidebar = NSView(frame: NSRect(x: 0, y: 0, width: KiroTheme.sidebarWidth, height: contentSize.height))
        sidebar.wantsLayer = true
        sidebar.layer?.backgroundColor = KiroTheme.background.cgColor
        sidebar.autoresizingMask = [.height]
        content.addSubview(sidebar)

        let divider = NSView(frame: NSRect(x: KiroTheme.sidebarWidth, y: 0, width: 1, height: contentSize.height))
        divider.wantsLayer = true
        divider.layer?.backgroundColor = KiroTheme.border.cgColor
        divider.autoresizingMask = [.height]
        content.addSubview(divider)

        container.frame = NSRect(
            x: KiroTheme.sidebarWidth + 1,
            y: 0,
            width: contentSize.width - KiroTheme.sidebarWidth - 1,
            height: contentSize.height
        )
        container.wantsLayer = true
        container.layer?.backgroundColor = KiroTheme.background.cgColor
        container.autoresizingMask = [.width, .height]
        content.addSubview(container)

        panels = [
            ("overview", overview.view),
            ("rules", rules.view),
            ("learning", learning.view),
            ("logs", logs.view),
            ("ai", ai.view),
            ("settings", settings.view),
        ]
        for (_, view) in panels {
            view.frame = container.bounds
            view.autoresizingMask = [.width, .height]
            container.addSubview(view)
        }

        buildSidebar(in: sidebar)
        window.contentView = content
        window.center()
        showPanel(0)

        let timer = Timer(timeInterval: 1.0, repeats: true) { [weak self] _ in
            guard let self, self.window.isVisible, self.panels.indices.contains(3),
                  !self.panels[3].1.isHidden else { return }
            self.logs.tick()
        }
        RunLoop.main.add(timer, forMode: .common)
        self.timer = timer
    }

    private func buildSidebar(in sidebar: NSView) {
        let stack = NSStackView()
        stack.orientation = .vertical
        stack.alignment = .leading
        stack.spacing = 3
        stack.translatesAutoresizingMaskIntoConstraints = false
        sidebar.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.leadingAnchor.constraint(equalTo: sidebar.leadingAnchor, constant: 16),
            stack.trailingAnchor.constraint(equalTo: sidebar.trailingAnchor, constant: -16),
            stack.topAnchor.constraint(equalTo: sidebar.topAnchor, constant: 24),
        ])

        func addItem(_ identifier: String, _ title: String, _ symbol: String) {
            let item = KiroSidebarItem(title: title, symbol: symbol)
            item.onClick = { [weak self] in self?.selectTab(identifier) }
            stack.addArrangedSubview(item)
            item.widthAnchor.constraint(equalTo: stack.widthAnchor).isActive = true
            sidebarItems.append((identifier, item))
        }

        func addHeader(_ title: String, beforeSpacing: CGFloat) {
            let header = kiroSidebarHeader(title)
            header.translatesAutoresizingMaskIntoConstraints = false
            stack.addArrangedSubview(header)
            stack.setCustomSpacing(beforeSpacing, after: stack.arrangedSubviews.dropLast().last ?? header)
        }

        addItem("overview", "开始", "paintbrush.pointed")
        addHeader("功能", beforeSpacing: 18)
        addItem("rules", "CLI 补全", "terminal")
        addItem("learning", "学习", "wand.and.stars")
        addItem("logs", "日志", "doc.text.magnifyingglass")
        addHeader("设置", beforeSpacing: 20)
        addItem("ai", "AI 补全", "sparkles")
        addItem("settings", "设置", "gearshape")
    }

    func show() {
        if let screen = window.screen, screen.visibleFrame.intersects(window.frame) {
            // Keep a user-positioned window on an existing display.
        } else {
            window.center()
        }
        window.makeKeyAndOrderFront(nil)
        window.makeFirstResponder(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    func selectTab(_ identifier: String) {
        guard let index = panels.firstIndex(where: { $0.0 == identifier }) else { return }
        showPanel(index)
    }

    private func showPanel(_ index: Int) {
        guard index >= 0, index < panels.count else { return }
        for (position, entry) in panels.enumerated() {
            entry.1.frame = container.bounds
            entry.1.isHidden = position != index
        }
        for (identifier, item) in sidebarItems {
            item.isSelected = identifier == panels[index].0
        }
        switch panels[index].0 {
        case "overview": overview.refresh()
        case "rules": rules.refresh()
        case "learning": learning.refresh()
        case "logs": logs.refresh(force: true)
        case "ai": ai.refresh()
        case "settings": settings.refresh()
        default: break
        }
    }

    @objc func refreshAll() {
        overview.refresh()
        rules.refresh()
        learning.refresh()
        logs.refresh(force: true)
    }

    @objc func showLogs() {
        selectTab("logs")
    }

    @objc func openLogsFolder() {
        let url = loadLogSettings().directory
        try? FileManager.default.createDirectory(
            at: url,
            withIntermediateDirectories: true,
            attributes: [.posixPermissions: 0o700]
        )
        NSWorkspace.shared.open(url)
    }
}

// MARK: - Icon and menu

func makeAppIcon() -> NSImage {
    let size = NSSize(width: 512, height: 512)
    let image = NSImage(size: size)
    image.lockFocus()
    NSColor.clear.setFill()
    NSBezierPath(rect: NSRect(origin: .zero, size: size)).fill()
    let background = NSBezierPath(
        roundedRect: NSRect(x: 24, y: 24, width: 464, height: 464),
        xRadius: 104,
        yRadius: 104
    )
    KiroTheme.accent.setFill()
    background.fill()
    let attributes: [NSAttributedString.Key: Any] = [
        .font: NSFont.systemFont(ofSize: 210, weight: .bold),
        .foregroundColor: NSColor.white,
    ]
    let text = "OC" as NSString
    let textSize = text.size(withAttributes: attributes)
    text.draw(
        at: NSPoint(x: (size.width - textSize.width) / 2, y: (size.height - textSize.height) / 2),
        withAttributes: attributes
    )
    image.unlockFocus()
    return image
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    let controller = MainWindowController()
    let screenshotPath: String?
    let initialTab: String?

    init(screenshotPath: String?, initialTab: String?) {
        self.screenshotPath = screenshotPath
        self.initialTab = initialTab
    }

    func applicationWillFinishLaunching(_ notification: Notification) {
        buildMenu()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.applicationIconImage = makeAppIcon()
        if let initialTab {
            controller.selectTab(initialTab)
        }
        controller.show()
        controller.refreshAll()
        let controller = self.controller
        DispatchQueue.main.async {
            if let error = ensureCommandLineIntegration() {
                let alert = NSAlert()
                alert.messageText = "无法启用新 zsh 窗口的自动补全"
                alert.informativeText = error
                alert.alertStyle = .warning
                alert.addButton(withTitle: "好")
                alert.runModal()
            } else {
                controller.refreshAll()
            }
        }
        if let path = screenshotPath, let view = controller.window.contentView {
            let controller = self.controller
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                controller.window.makeKey()
                NSApp.activate(ignoringOtherApps: true)
                view.layoutSubtreeIfNeeded()
                if let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) {
                    view.cacheDisplay(in: view.bounds, to: rep)
                    if let data = rep.representation(using: .png, properties: [:]) {
                        try? data.write(to: URL(fileURLWithPath: path))
                    }
                }
                NSApp.terminate(nil)
            }
        }
    }

    func applicationDidBecomeActive(_ notification: Notification) {
        controller.refreshAll()
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        controller.show()
        return true
    }

    func applicationWillTerminate(_ notification: Notification) {
        clearAppRunning()
    }

    // Closing the window keeps the app alive so automatic completion keeps
    // working in existing and new terminals. Only an explicit Quit (or a
    // process-level signal) removes the running marker and disables it.
    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        false
    }

    func applicationSupportsSecureRestorableState(_ app: NSApplication) -> Bool {
        true
    }

    private func buildMenu() {
        let mainMenu = NSMenu()

        let appItem = NSMenuItem()
        appItem.title = "Offline Complete"
        mainMenu.addItem(appItem)
        let appMenu = NSMenu()
        let about = NSMenuItem(
            title: "关于 Offline Complete",
            action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)),
            keyEquivalent: ""
        )
        about.target = NSApp
        appMenu.addItem(about)
        appMenu.addItem(.separator())
        let hide = NSMenuItem(title: "隐藏 Offline Complete", action: #selector(NSApplication.hide(_:)), keyEquivalent: "h")
        hide.target = NSApp
        appMenu.addItem(hide)
        let hideOthers = NSMenuItem(
            title: "隐藏其他",
            action: #selector(NSApplication.hideOtherApplications(_:)),
            keyEquivalent: "h"
        )
        hideOthers.keyEquivalentModifierMask = [.command, .option]
        hideOthers.target = NSApp
        appMenu.addItem(hideOthers)
        let showAll = NSMenuItem(title: "显示全部", action: #selector(NSApplication.unhideAllApplications(_:)), keyEquivalent: "")
        showAll.target = NSApp
        appMenu.addItem(showAll)
        appMenu.addItem(.separator())
        let quit = NSMenuItem(title: "退出 Offline Complete", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        quit.target = NSApp
        appMenu.addItem(quit)
        appItem.submenu = appMenu

        let fileItem = NSMenuItem()
        fileItem.title = "文件"
        mainMenu.addItem(fileItem)
        let fileMenu = NSMenu(title: "文件")
        let refresh = NSMenuItem(title: "刷新", action: #selector(MainWindowController.refreshAll), keyEquivalent: "r")
        refresh.target = controller
        fileMenu.addItem(refresh)
        let logs = NSMenuItem(title: "显示日志", action: #selector(MainWindowController.showLogs), keyEquivalent: "l")
        logs.target = controller
        fileMenu.addItem(logs)
        fileMenu.addItem(.separator())
        let close = NSMenuItem(title: "关闭窗口", action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w")
        fileMenu.addItem(close)
        fileItem.submenu = fileMenu

        let editItem = NSMenuItem()
        editItem.title = "编辑"
        mainMenu.addItem(editItem)
        let editMenu = NSMenu(title: "编辑")
        editMenu.addItem(withTitle: "撤销", action: Selector(("undo:")), keyEquivalent: "z")
        editMenu.addItem(withTitle: "重做", action: Selector(("redo:")), keyEquivalent: "Z")
        editMenu.addItem(.separator())
        editMenu.addItem(withTitle: "剪切", action: Selector(("cut:")), keyEquivalent: "x")
        editMenu.addItem(withTitle: "复制", action: Selector(("copy:")), keyEquivalent: "c")
        editMenu.addItem(withTitle: "粘贴", action: Selector(("paste:")), keyEquivalent: "v")
        editMenu.addItem(withTitle: "全选", action: Selector(("selectAll:")), keyEquivalent: "a")
        editItem.submenu = editMenu

        let windowItem = NSMenuItem()
        windowItem.title = "窗口"
        mainMenu.addItem(windowItem)
        let windowMenu = NSMenu(title: "窗口")
        windowMenu.addItem(withTitle: "最小化", action: #selector(NSWindow.performMiniaturize(_:)), keyEquivalent: "m")
        windowMenu.addItem(withTitle: "缩放", action: #selector(NSWindow.performZoom(_:)), keyEquivalent: "")
        windowMenu.addItem(.separator())
        windowMenu.addItem(withTitle: "全部前置", action: #selector(NSApplication.arrangeInFront(_:)), keyEquivalent: "")
        windowItem.submenu = windowMenu
        NSApp.windowsMenu = windowMenu

        let helpItem = NSMenuItem()
        helpItem.title = "帮助"
        mainMenu.addItem(helpItem)
        let helpMenu = NSMenu(title: "帮助")
        let logsFolder = NSMenuItem(title: "打开日志目录", action: #selector(MainWindowController.openLogsFolder), keyEquivalent: "")
        logsFolder.target = controller
        helpMenu.addItem(logsFolder)
        helpItem.submenu = helpMenu

        NSApp.mainMenu = mainMenu
    }
}

// MARK: - Check mode

func runCheck() -> Int32 {
    var failed = false
    let rules = loadRules()
    if let error = rules.error {
        FileHandle.standardError.write(Data("oc-app: rules: \(error)\n".utf8))
        failed = true
    }
    print("rules path=\(rules.path) commands=\(rules.topCount) entries=\(rules.total) sha256=\(rules.sha)")

    let learning = loadLearning()
    if let error = learning.error {
        FileHandle.standardError.write(Data("oc-app: learning: \(error)\n".utf8))
        failed = true
    }
    print("learning path=\(learning.path) enabled=\(learning.learning) selections=\(learning.entries.count) bytes=\(learning.bytes)")

    let log = loadLogSettings()
    let exists = FileManager.default.fileExists(atPath: log.currentURL.path)
    print("logging enabled=\(log.enabled) directory=\(log.directory.path) retention=\(log.retentionDays)")
    print("log path=\(log.currentURL.path) exists=\(exists)")
    if let error = log.error {
        FileHandle.standardError.write(Data("oc-app: logging settings: \(error)\n".utf8))
    }
    print("app=\(Bundle.main.bundleURL.path) manage=\(AppPaths.tool("oc-manage")?.path ?? "missing")")
    return failed ? 1 : 0
}

// MARK: - Entry point

if CommandLine.arguments.contains("--check") {
    exit(runCheck())
}

var screenshotPath: String?
if let index = CommandLine.arguments.firstIndex(of: "--screenshot"), index + 1 < CommandLine.arguments.count {
    screenshotPath = CommandLine.arguments[index + 1]
}
var initialTab: String?
if let index = CommandLine.arguments.firstIndex(of: "--tab"), index + 1 < CommandLine.arguments.count {
    initialTab = CommandLine.arguments[index + 1]
}

let application = NSApplication.shared
let delegate = AppDelegate(screenshotPath: screenshotPath, initialTab: initialTab)
application.delegate = delegate
application.setActivationPolicy(.regular)
application.run()

import AppKit
import CryptoKit
import Darwin
import Foundation
import SwiftUI

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

    static var displayVersion: String {
        let value = appVersion
        if value == "dev" { return "开发版" }
        return value.hasPrefix("v") ? value : "v\(value)"
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

    static var ocDir: URL {
        if let value = environment["OFFLINE_COMPLETE_DATA_DIR"], !value.isEmpty {
            return URL(fileURLWithPath: value)
        }
        return home.appendingPathComponent(".oc")
    }

    static var learningURL: URL { ocDir.appendingPathComponent("learning.json") }
    static var legacyLearningURL: URL { dataDir.appendingPathComponent("learning.json") }
    static var backupURL: URL { ocDir.appendingPathComponent("offline-complete-backup.zip") }

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

struct AutocompleteSettings {
    var tabAction: String
    var enterAction: String
    var exactMatchExecute: Bool
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

func loadAutocompleteSettings() -> AutocompleteSettings {
    var result = AutocompleteSettings(
        tabAction: "next",
        enterAction: "select",
        exactMatchExecute: true,
        error: nil
    )
    let config = AppPaths.configURL
    guard let data = try? Data(contentsOf: config) else { return result }
    do {
        guard let root = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw NSError(domain: "oc", code: 1, userInfo: [NSLocalizedDescriptionKey: "不是 JSON 对象"])
        }
        if let autocomplete = root["autocomplete"] as? [String: Any] {
            result.tabAction = autocomplete["tabAction"] as? String ?? result.tabAction
            result.enterAction = autocomplete["enterAction"] as? String ?? result.enterAction
            result.exactMatchExecute = autocomplete["exactMatchExecute"] as? Bool ?? result.exactMatchExecute
        }
    } catch {
        result.error = error.localizedDescription
    }
    if result.tabAction != "next" && result.tabAction != "accept" {
        result.error = result.error ?? "不支持的 Tab 操作：\(result.tabAction)"
        result.tabAction = "next"
    }
    if result.enterAction != "select" && result.enterAction != "execute" {
        result.error = result.error ?? "不支持的回车操作：\(result.enterAction)"
        result.enterAction = "select"
    }
    return result
}

func loadMenuBarSetting() -> Bool {
    let config = AppPaths.configURL
    guard let data = try? Data(contentsOf: config),
          let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          let menuBar = root["menuBar"] as? [String: Any] else {
        return true
    }
    return menuBar["enabled"] as? Bool ?? true
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


// MARK: - System permissions

// Permissions are granted to the candidate-window binary, not to the
// management window, so ask oc-window instead of calling AXIsProcessTrusted()
// in this process.
struct WindowPermissionStatus {
    var accessibility: Bool?
    var inputMonitoring: Bool?
    var screenRecording: Bool?

    static func load() -> WindowPermissionStatus {
        guard AppPaths.tool("oc-window") != nil else { return WindowPermissionStatus() }
        let (code, output) = runTool("oc-window", ["--permission-status"])
        guard code == 0 else { return WindowPermissionStatus() }
        var result = WindowPermissionStatus()
        for line in output.split(separator: "\n") {
            let parts = line.split(separator: ":", maxSplits: 1)
            guard parts.count == 2 else { continue }
            let key = parts[0].trimmingCharacters(in: .whitespaces)
            let granted = parts[1].trimmingCharacters(in: .whitespaces) == "granted"
            switch key {
            case "accessibility": result.accessibility = granted
            case "input-monitoring": result.inputMonitoring = granted
            case "screen-recording": result.screenRecording = granted
            default: break
            }
        }
        return result
    }
}

func requestWindowAccessibility() {
    guard AppPaths.tool("oc-window") != nil else {
        showAlert("找不到 oc-window", "请重新安装 Offline Complete。")
        return
    }
    _ = runTool("oc-window", ["--accessibility-prompt"])
}

func openPrivacyPane(_ anchor: String) {
    guard let url = URL(string: "x-apple.systempreferences:com.apple.preference.security?\(anchor)") else {
        return
    }
    NSWorkspace.shared.open(url)
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

struct PersonalRule {
    let context: String
    let candidate: String
    let wins: UInt64
    let losses: UInt64
    let days: Int
    let confidence: Double
    let eligible: Bool
    let last: Date?
}

struct LearningSnapshot {
    let path: String
    let learning: Bool
    let entries: [LearningEntry]
    let personal: [PersonalRule]
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

func isParentToken(_ value: String) -> Bool {
    var normalized = value.trimmingCharacters(in: .whitespacesAndNewlines)
    if normalized.hasSuffix("/") {
        normalized.removeLast()
    }
    return normalized == ".."
}

func learningContext(parts: [String]) -> String? {
    guard parts.count >= 4 else { return nil }
    let kind: String
    switch parts[2] {
    case "opt": kind = "选项"
    case "sub": kind = "子命令"
    case "pos": kind = "位置"
    default: kind = parts[2]
    }
    var context = parts[0]
    if !parts[1].isEmpty {
        context += " " + parts[1].replacingOccurrences(of: "/", with: " ")
    }
    context += " · \(kind)"
    if parts[2] == "pos" {
        context += " #\(parts[3])"
    }
    return context
}

func wilsonLower(wins: UInt64, losses: UInt64, z: Double = 1.645) -> Double {
    let total = Double(wins + losses)
    guard total > 0 else { return 0 }
    let probability = Double(wins) / total
    let z2 = z * z
    let denominator = 1 + z2 / total
    let center = probability + z2 / (2 * total)
    let margin = z * sqrt(probability * (1 - probability) / total + z2 / (4 * total * total))
    return min(1, max(0, (center - margin) / denominator))
}

func loadLearning() -> LearningSnapshot {
    let primary = AppPaths.learningURL
    let legacy = AppPaths.legacyLearningURL
    let url = FileManager.default.fileExists(atPath: primary.path) ? primary : legacy
    guard let data = try? Data(contentsOf: url) else {
        return LearningSnapshot(path: primary.path, learning: true, entries: [], personal: [], bytes: 0, error: nil)
    }
    do {
        guard let root = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return LearningSnapshot(path: url.path, learning: true, entries: [], personal: [], bytes: data.count, error: "学习数据不是 JSON 对象")
        }
        let learning = root["learning"] as? Bool ?? true
        let selections = root["selections"] as? [String: Any] ?? [:]
        var entries: [LearningEntry] = []
        entries.reserveCapacity(selections.count)
        for (key, raw) in selections {
            guard let selection = raw as? [String: Any] else { continue }
            let parts = key.components(separatedBy: "\0")
            guard parts.count >= 5, !isParentToken(parts[4]), let context = learningContext(parts: parts) else { continue }
            let count = (selection["count"] as? NSNumber)?.uint64Value ?? 0
            let last = (selection["last"] as? String).flatMap(parseDate)
            entries.append(LearningEntry(context: context, candidate: parts[4], count: count, last: last))
        }
        entries.sort {
            if $0.count != $1.count { return $0.count > $1.count }
            return ($0.last ?? .distantPast) > ($1.last ?? .distantPast)
        }

        let corrections = root["corrections"] as? [String: Any] ?? [:]
        var personal: [PersonalRule] = []
        for (key, rawValues) in corrections {
            guard let values = rawValues as? [String: Any] else { continue }
            let parts = key.components(separatedBy: "\0")
            guard let context = learningContext(parts: parts) else { continue }
            for (candidate, raw) in values {
                guard !isParentToken(candidate), let correction = raw as? [String: Any] else { continue }
                let wins = (correction["wins"] as? NSNumber)?.uint64Value ?? 0
                let losses = (correction["losses"] as? NSNumber)?.uint64Value ?? 0
                let days = (correction["days"] as? [String])?.count ?? 0
                let last = (correction["last"] as? String).flatMap(parseDate)
                personal.append(PersonalRule(
                    context: context,
                    candidate: candidate,
                    wins: wins,
                    losses: losses,
                    days: days,
                    confidence: wilsonLower(wins: wins, losses: losses),
                    eligible: wins >= 3 && days >= 2,
                    last: last
                ))
            }
        }
        personal.sort {
            if $0.eligible != $1.eligible { return $0.eligible && !$1.eligible }
            if $0.confidence != $1.confidence { return $0.confidence > $1.confidence }
            return $0.wins > $1.wins
        }
        return LearningSnapshot(path: url.path, learning: learning, entries: entries, personal: personal, bytes: data.count, error: nil)
    } catch {
        return LearningSnapshot(path: url.path, learning: true, entries: [], personal: [], bytes: data.count, error: error.localizedDescription)
    }
}

extension Notification.Name {
    static let menuBarSettingChanged = Notification.Name("OfflineCompleteMenuBarSettingChanged")
}

// MARK: - Overview

// MARK: - SwiftUI shell

final class AppShellModel: ObservableObject {
    @Published var selected = "overview"
    @Published var refreshToken = 0

    func refreshAll() { refreshToken &+= 1 }
}

struct AppShellView: View {
    @ObservedObject var model: AppShellModel

    private let items: [(String, String, String)] = [
        ("overview", "开始", "paintbrush.pointed"),
        ("learning", "本地学习", "wand.and.stars"),
        ("logs", "日志", "doc.text.magnifyingglass"),
        ("ai", "AI 补全", "sparkles"),
        ("settings", "设置", "gearshape"),
    ]

    var body: some View {
        HStack(spacing: 0) {
            sidebar
            Divider()
            content
                .id(model.refreshToken)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .background(Color(nsColor: KiroTheme.background))
    }

    private var sidebar: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("功能")
                .font(.system(size: 11, weight: .semibold))
                .foregroundColor(Color(nsColor: KiroTheme.muted))
                .padding(.horizontal, 12)
                .padding(.top, 24)
                .padding(.bottom, 4)
            ForEach(items.prefix(3), id: \.0) { item in sidebarButton(item) }

            Text("设置")
                .font(.system(size: 11, weight: .semibold))
                .foregroundColor(Color(nsColor: KiroTheme.muted))
                .padding(.horizontal, 12)
                .padding(.top, 18)
                .padding(.bottom, 4)
            ForEach(items.suffix(2), id: \.0) { item in sidebarButton(item) }

            Spacer()
            Text(AppPaths.displayVersion)
                .font(.system(size: 10))
                .foregroundColor(Color(nsColor: KiroTheme.muted))
                .padding(.horizontal, 12)
                .padding(.bottom, 16)
        }
        .frame(width: 220)
        .background(Color(nsColor: KiroTheme.background))
    }

    private func sidebarButton(_ item: (String, String, String)) -> some View {
        Button {
            model.selected = item.0
        } label: {
            HStack(spacing: 9) {
                Image(systemName: item.2)
                    .frame(width: 16)
                Text(item.1)
                    .font(.system(size: 13))
                Spacer()
            }
            .foregroundColor(model.selected == item.0 ? .white : Color(nsColor: KiroTheme.title))
            .padding(.horizontal, 10)
            .frame(maxWidth: .infinity, minHeight: 38, alignment: .leading)
            .contentShape(Rectangle())
            .background(
                RoundedRectangle(cornerRadius: 6)
                    .fill(model.selected == item.0 ? Color(nsColor: KiroTheme.accent) : .clear)
            )
        }
        .buttonStyle(.plain)
        .padding(.horizontal, 8)
        .contentShape(Rectangle())
    }

    private var content: some View {
        ZStack {
            OverviewView()
                .opacity(model.selected == "overview" ? 1 : 0)
                .allowsHitTesting(model.selected == "overview")
            LearningView()
                .opacity(model.selected == "learning" ? 1 : 0)
                .allowsHitTesting(model.selected == "learning")
            LogsView(active: model.selected == "logs")
                .opacity(model.selected == "logs" ? 1 : 0)
                .allowsHitTesting(model.selected == "logs")
            AISettingsView()
                .opacity(model.selected == "ai" ? 1 : 0)
                .allowsHitTesting(model.selected == "ai")
            SettingsView(model: model)
                .opacity(model.selected == "settings" ? 1 : 0)
                .allowsHitTesting(model.selected == "settings")
        }
    }
}

struct SettingsView: View {
    @ObservedObject var model: AppShellModel
    @State private var tabAction: String
    @State private var enterAction: String
    @State private var exactMatch: Bool
    @State private var logEnabled: Bool
    @State private var logDirectory: String
    @State private var retention: Int
    @State private var menuBar: Bool
    @State private var message: String?
    @State private var isError = false

    init(model: AppShellModel) {
        self.model = model
        let keys = loadAutocompleteSettings()
        let logs = loadLogSettings()
        _tabAction = State(initialValue: keys.tabAction)
        _enterAction = State(initialValue: keys.enterAction)
        _exactMatch = State(initialValue: keys.exactMatchExecute)
        _logEnabled = State(initialValue: logs.enabled)
        _logDirectory = State(initialValue: logs.directory.path)
        _retention = State(initialValue: logs.retentionDays)
        _menuBar = State(initialValue: loadMenuBarSetting())
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                Text("设置")
                    .font(.system(size: 24, weight: .semibold))
                    .foregroundColor(Color(nsColor: KiroTheme.title))

                settingsCard("补全") {
                    fieldRow("Tab") {
                        Picker("", selection: $tabAction) {
                            Text("下一项").tag("next")
                            Text("选择当前项").tag("accept")
                        }
                        .labelsHidden()
                        .pickerStyle(.segmented)
                        .frame(width: 220)
                        .onChange(of: tabAction) { _ in saveKeySettings() }
                    }
                    fieldRow("回车") {
                        Picker("", selection: $enterAction) {
                            Text("选择当前项").tag("select")
                            Text("直接执行命令").tag("execute")
                        }
                        .labelsHidden()
                        .pickerStyle(.segmented)
                        .frame(width: 240)
                        .onChange(of: enterAction) { _ in saveKeySettings() }
                    }
                    Toggle("精确匹配时直接执行", isOn: $exactMatch)
                        .disabled(enterAction == "execute")
                        .onChange(of: exactMatch) { _ in saveKeySettings() }
                    Text("按键修改会自动保存，并在当前终端约 1 秒内生效。")
                        .font(.system(size: 12))
                        .foregroundColor(Color(nsColor: KiroTheme.muted))
                }

                settingsCard("日志") {
                    Toggle("写入 agent 日志", isOn: $logEnabled)
                    HStack(spacing: 12) {
                        Text("目录")
                            .frame(width: 44, alignment: .leading)
                        TextField("~/.oc/logs", text: $logDirectory)
                            .textFieldStyle(.roundedBorder)
                    }
                    HStack(spacing: 12) {
                        Text("保留")
                            .frame(width: 44, alignment: .leading)
                        Picker("", selection: $retention) {
                            ForEach([1, 3, 7, 14, 30], id: \.self) { days in
                                Text("\(days) 天").tag(days)
                            }
                        }
                        .labelsHidden()
                        .frame(width: 120)
                    }
                }

                settingsCard("菜单栏") {
                    Toggle("常驻菜单栏", isOn: $menuBar)
                        .onChange(of: menuBar) { value in setMenuBar(value) }
                    Text("后台运行时从菜单栏打开主界面和查看学习指标。")
                        .font(.system(size: 12))
                        .foregroundColor(Color(nsColor: KiroTheme.muted))
                }

                settingsCard("数据与备份") {
                    Text("包含本地学习、个人纠正和设置（含 AI Key）。不包含日志、规则包和运行数据。")
                        .font(.system(size: 12))
                        .foregroundColor(Color(nsColor: KiroTheme.muted))
                    HStack(spacing: 10) {
                        Button("导出备份…") { exportBackup() }
                        Button("导入备份…") { importBackup() }
                    }
                }

                HStack(spacing: 12) {
                    Button("保存") { save() }
                        .buttonStyle(.borderedProminent)
                        .tint(Color(nsColor: KiroTheme.accent))
                    if let message {
                        Text(message)
                            .font(.system(size: 12))
                            .foregroundColor(isError ? .red : Color(nsColor: KiroTheme.muted))
                    }
                }
            }
            .padding(.horizontal, 40)
            .padding(.vertical, 34)
            .frame(maxWidth: 720, alignment: .leading)
        }
        .background(Color(nsColor: KiroTheme.background))
    }

    private func settingsCard<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(title)
                .font(.system(size: 13, weight: .semibold))
                .foregroundColor(Color(nsColor: KiroTheme.muted))
            content()
        }
        .padding(20)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
            RoundedRectangle(cornerRadius: 10)
                .fill(Color(nsColor: KiroTheme.surface))
                .overlay(
                    RoundedRectangle(cornerRadius: 10)
                        .stroke(Color(nsColor: KiroTheme.border), lineWidth: 1)
                )
        )
    }

    private func fieldRow<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        HStack(spacing: 16) {
            Text(title)
                .frame(width: 44, alignment: .leading)
            content()
            Spacer()
        }
    }

    private func save() {
        guard saveKeySettings() else { return }
        let (logCode, logOutput) = runTool("oc-manage", [
            "logging", "set",
            "--enabled", logEnabled ? "on" : "off",
            "--directory", logDirectory,
            "--retention-days", String(retention),
        ])
        if logCode != 0 {
            message = logOutput.isEmpty ? "保存日志设置失败" : logOutput
            isError = true
            return
        }
        message = "已保存"
        isError = false
    }

    @discardableResult
    private func saveKeySettings() -> Bool {
        let (keyCode, keyOutput) = runTool("oc-manage", [
            "autocomplete", "set",
            "--tab", tabAction,
            "--enter", enterAction,
            "--exact-match-execute", exactMatch ? "on" : "off",
        ])
        if keyCode != 0 {
            message = keyOutput.isEmpty ? "保存按键设置失败" : keyOutput
            isError = true
            return false
        }
        message = "补全按键已保存"
        isError = false
        return true
    }

    private func exportBackup() {
        let panel = NSSavePanel()
        panel.title = "导出完整备份"
        panel.nameFieldStringValue = AppPaths.backupURL.lastPathComponent
        panel.allowedFileTypes = ["zip"]
        if FileManager.default.fileExists(atPath: AppPaths.ocDir.path) {
            panel.directoryURL = AppPaths.ocDir
        }
        guard panel.runModal() == .OK, let url = panel.url else { return }
        let (code, output) = runTool("oc-manage", ["backup", "export", "--file", url.path])
        if code != 0 {
            message = output.isEmpty ? "导出备份失败" : output
            isError = true
            return
        }
        message = "备份已导出"
        isError = false
    }

    private func importBackup() {
        let panel = NSOpenPanel()
        panel.title = "导入完整备份"
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        panel.allowedFileTypes = ["zip"]
        guard panel.runModal() == .OK, let url = panel.url else { return }
        let alert = NSAlert()
        alert.messageText = "恢复这个备份？"
        alert.informativeText = "会替换当前学习数据和设置（含 AI Key）；导入前会备份当前数据，失败时尽量恢复。"
        alert.addButton(withTitle: "恢复")
        alert.addButton(withTitle: "取消")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let (code, output) = runTool("oc-manage", ["backup", "import", "--file", url.path])
        if code != 0 {
            message = output.isEmpty ? "导入备份失败" : output
            isError = true
            return
        }
        reloadSettings()
        NotificationCenter.default.post(name: .menuBarSettingChanged, object: nil)
        message = "备份已恢复"
        isError = false
        model.refreshAll()
    }

    private func setMenuBar(_ value: Bool) {
        let (code, output) = runTool("oc-manage", ["menu-bar", value ? "on" : "off"])
        if code != 0 {
            message = output.isEmpty ? "修改菜单栏设置失败" : output
            isError = true
            menuBar = !value
            return
        }
        NotificationCenter.default.post(name: .menuBarSettingChanged, object: nil)
        message = value ? "菜单栏已开启" : "菜单栏已关闭"
        isError = false
    }

    private func reloadSettings() {
        let keys = loadAutocompleteSettings()
        let logs = loadLogSettings()
        tabAction = keys.tabAction
        enterAction = keys.enterAction
        exactMatch = keys.exactMatchExecute
        logEnabled = logs.enabled
        logDirectory = logs.directory.path
        retention = logs.retentionDays
        menuBar = loadMenuBarSetting()
    }
}

struct OverviewView: View {
    @State private var accessibility: Bool?
    @State private var running = false
    @State private var autoStart = false
    private let timer = Timer.publish(every: 2, on: .main, in: .common).autoconnect()

    var body: some View {
        VStack(alignment: .leading, spacing: 22) {
            HStack {
                Text("开始").font(.system(size: 24, weight: .semibold))
                Spacer()
                Button("刷新") { refresh() }
            }

            VStack(alignment: .leading, spacing: 12) {
                Text("系统权限")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(Color(nsColor: KiroTheme.muted))
                HStack(spacing: 14) {
                    Image(systemName: "lock.shield")
                        .foregroundColor(Color(nsColor: KiroTheme.accent))
                    VStack(alignment: .leading, spacing: 2) {
                        Text("辅助功能").font(.system(size: 14, weight: .semibold))
                        Text("跟随输入框").font(.system(size: 12)).foregroundColor(.secondary)
                    }
                    Spacer()
                    Text(accessibility == true ? "已授权" : "未授权")
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(accessibility == true ? .green : .red)
                    if accessibility != true {
                        Button("打开设置") { openPrivacyPane("Privacy_Accessibility") }
                        Button("请求") {
                            requestWindowAccessibility()
                            openPrivacyPane("Privacy_Accessibility")
                        }
                    }
                }
                .padding(16)
                .background(
                    RoundedRectangle(cornerRadius: 10)
                        .fill(Color(nsColor: KiroTheme.surface))
                        .overlay(RoundedRectangle(cornerRadius: 10)
                            .stroke(Color(nsColor: KiroTheme.border), lineWidth: 1))
                )
            }

            VStack(alignment: .leading, spacing: 12) {
                Text("补全")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(Color(nsColor: KiroTheme.muted))
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text("自动补全").font(.system(size: 14, weight: .semibold))
                        Text(autoStart ? "后台运行" : "未启用").font(.system(size: 12)).foregroundColor(.secondary)
                    }
                    Spacer()
                    Text(running ? "运行中" : (autoStart ? "等待应用" : "未启用"))
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(running ? .green : Color(nsColor: KiroTheme.muted))
                }
                .padding(16)
                .background(
                    RoundedRectangle(cornerRadius: 10)
                        .fill(Color(nsColor: KiroTheme.surface))
                )
            }
        }
        .padding(.horizontal, 40)
        .padding(.vertical, 34)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .onAppear(perform: refresh)
        .onReceive(timer) { _ in refresh() }
    }

    private func refresh() {
        DispatchQueue.global(qos: .userInitiated).async {
            let permission = WindowPermissionStatus.load().accessibility
            let zshrc = AppPaths.home.appendingPathComponent(".zshrc")
            let auto = (try? String(contentsOf: zshrc, encoding: .utf8))?
                .contains("# offline-complete integration v2") ?? false
            let marker = AppPaths.dataDir.appendingPathComponent("app.pid")
            var isRunning = false
            if let value = try? String(contentsOf: marker, encoding: .utf8),
               let pid = Int32(value.trimmingCharacters(in: .whitespacesAndNewlines)) {
                isRunning = kill(pid, 0) == 0
            }
            DispatchQueue.main.async {
                accessibility = permission
                autoStart = auto
                running = isRunning
            }
        }
    }
}

struct AISettingsView: View {
    @State private var enabled: Bool
    @State private var protocolName: String
    @State private var url: String
    @State private var key: String
    @State private var model: String
    @State private var maxTokens: String
    @State private var models: [String] = []
    @State private var fetchingModels = false
    @State private var modelStatus: String?
    @State private var message: String?
    @State private var isError = false

    init() {
        let settings = loadAISettings()
        _enabled = State(initialValue: settings.enabled)
        _protocolName = State(initialValue: settings.protocolName)
        _url = State(initialValue: settings.url)
        _key = State(initialValue: settings.key)
        _model = State(initialValue: settings.model)
        _maxTokens = State(initialValue: String(settings.maxTokens))
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                Text("AI 补全").font(.system(size: 24, weight: .semibold))

                VStack(alignment: .leading, spacing: 16) {
                    Toggle("启用 AI 补全", isOn: $enabled)
                    HStack(spacing: 12) {
                        Text("协议").frame(width: 64, alignment: .leading)
                        Picker("", selection: $protocolName) {
                            Text("OpenAI").tag("openai")
                            Text("Anthropic").tag("anthropic")
                        }
                        .labelsHidden()
                        .frame(width: 180)
                    }
                    field("API 地址", text: $url, prompt: "https://api.openai.com/v1/chat/completions")
                    HStack(spacing: 12) {
                        Text("API Key").frame(width: 64, alignment: .leading)
                        SecureField("API Key", text: $key)
                            .textFieldStyle(.roundedBorder)
                    }
                    HStack(spacing: 12) {
                        Text("模型").frame(width: 64, alignment: .leading)
                        TextField("gpt-4o-mini", text: $model)
                            .textFieldStyle(.roundedBorder)
                        Menu("选择") {
                            ForEach(models, id: \.self) { item in
                                Button(item) { model = item }
                            }
                        }
                        .disabled(models.isEmpty)
                        Button(fetchingModels ? "获取中…" : "获取模型") { fetchModels() }
                            .disabled(fetchingModels || url.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                    }
                    if let modelStatus {
                        Text(modelStatus)
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                    }
                    HStack(spacing: 12) {
                        Text("最大 Tokens").frame(width: 80, alignment: .leading)
                        TextField("1024", text: $maxTokens)
                            .textFieldStyle(.roundedBorder)
                            .frame(width: 100)
                    }
                }
                .padding(20)
                .frame(maxWidth: 640, alignment: .leading)
                .background(
                    RoundedRectangle(cornerRadius: 10)
                        .fill(Color(nsColor: KiroTheme.surface))
                )

                HStack(spacing: 12) {
                    Button("保存") { save() }
                        .buttonStyle(.borderedProminent)
                        .tint(Color(nsColor: KiroTheme.accent))
                    Button("清除 API Key") {
                        key = ""
                        save()
                    }
                    if let message {
                        Text(message)
                            .font(.system(size: 12))
                            .foregroundColor(isError ? .red : .secondary)
                    }
                }
            }
            .padding(.horizontal, 40)
            .padding(.vertical, 34)
            .frame(maxWidth: 720, alignment: .leading)
        }
        .onChange(of: protocolName) { _ in
            models = []
            modelStatus = nil
        }
    }

    private func field(_ title: String, text: Binding<String>, prompt: String) -> some View {
        HStack(spacing: 12) {
            Text(title).frame(width: 64, alignment: .leading)
            TextField(prompt, text: text)
                .textFieldStyle(.roundedBorder)
        }
    }

    private func save() {
        let endpoint = url.trimmingCharacters(in: .whitespacesAndNewlines)
        if enabled && endpoint.isEmpty {
            message = "启用 AI 补全需要 API 地址"
            isError = true
            return
        }
        var args = [
            "ai", "set",
            "--enabled", enabled ? "on" : "off",
            "--protocol", protocolName,
            "--url", endpoint,
            "--model", model.trimmingCharacters(in: .whitespacesAndNewlines),
            "--max-tokens", String(max(1, min(32768, Int(maxTokens) ?? 1024))),
        ]
        if key.isEmpty {
            args.append("--clear-key")
        } else {
            args += ["--key", key]
        }
        let (code, output) = runTool("oc-manage", args)
        if code != 0 {
            message = output.isEmpty ? "保存 AI 设置失败" : output
            isError = true
            return
        }
        message = "已保存"
        isError = false
    }

    private func fetchModels() {
        guard let endpoint = modelListURL() else {
            modelStatus = "API 地址无效"
            return
        }
        guard !key.isEmpty else {
            modelStatus = "需要 API Key"
            return
        }
        fetchingModels = true
        modelStatus = nil
        var request = URLRequest(url: endpoint)
        request.timeoutInterval = 12
        if protocolName == "anthropic" {
            request.setValue(key, forHTTPHeaderField: "x-api-key")
            request.setValue("2023-06-01", forHTTPHeaderField: "anthropic-version")
        } else {
            request.setValue("Bearer \(key)", forHTTPHeaderField: "Authorization")
        }
        URLSession.shared.dataTask(with: request) { data, response, error in
            DispatchQueue.main.async {
                fetchingModels = false
                if let error {
                    modelStatus = "获取失败：\(error.localizedDescription)"
                    return
                }
                guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
                    let code = (response as? HTTPURLResponse)?.statusCode ?? -1
                    modelStatus = "获取失败：HTTP \(code)"
                    return
                }
                guard let data,
                      let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                      let entries = root["data"] as? [[String: Any]] else {
                    modelStatus = "响应中没有模型列表"
                    return
                }
                let ids = entries.compactMap { $0["id"] as? String }.sorted()
                guard !ids.isEmpty else {
                    modelStatus = "模型列表为空"
                    return
                }
                models = ids
                if model.isEmpty { model = ids[0] }
                modelStatus = "已获取 \(ids.count) 个模型"
            }
        }.resume()
    }

    private func modelListURL() -> URL? {
        let value = url.trimmingCharacters(in: .whitespacesAndNewlines)
        guard var components = URLComponents(string: value) else { return nil }
        var path = components.path
        for suffix in ["/chat/completions", "/messages"] where path.hasSuffix(suffix) {
            path = String(path.dropLast(suffix.count))
            break
        }
        if !path.hasSuffix("/models") {
            path = path.hasSuffix("/") ? path + "models" : path + "/models"
        }
        components.path = path
        return components.url
    }
}

struct RulesView: View {
    @State private var snapshot: RulesSnapshot?
    @State private var query = ""
    @State private var loading = false

    private var commands: [RuleNode] {
        guard let snapshot else { return [] }
        return filtered(snapshot.commands, query: query)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(alignment: .firstTextBaseline) {
                VStack(alignment: .leading, spacing: 4) {
                    Text("CLI 补全").font(.system(size: 24, weight: .semibold))
                    if let snapshot, let error = snapshot.error {
                        Text(error).font(.system(size: 12)).foregroundColor(.red)
                    } else if let snapshot {
                        Text("\(snapshot.topCount) 个命令 · \(snapshot.total) 条规则")
                            .font(.system(size: 12))
                            .foregroundColor(Color(nsColor: KiroTheme.muted))
                            .help("sha256 \(snapshot.sha)\n\(snapshot.path)")
                    }
                }
                Spacer()
                Button("刷新") { load() }
            }

            TextField("筛选命令", text: $query)
                .textFieldStyle(.roundedBorder)
                .frame(maxWidth: 280)

            if loading && snapshot == nil {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 2) {
                        ForEach(commands.indices, id: \.self) { index in
                            RuleNodeRow(node: commands[index])
                        }
                    }
                    .padding(.trailing, 12)
                }
            }
        }
        .padding(.horizontal, 40)
        .padding(.vertical, 34)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .onAppear { if snapshot == nil { load() } }
    }

    private func load() {
        if loading { return }
        loading = true
        DispatchQueue.global(qos: .userInitiated).async {
            let result = loadRules()
            DispatchQueue.main.async {
                snapshot = result
                loading = false
            }
        }
    }

    private func filtered(_ nodes: [RuleNode], query: String) -> [RuleNode] {
        guard !query.isEmpty else { return nodes }
        return nodes.compactMap { node in
            let children = filtered(node.children, query: query)
            if node.name.localizedCaseInsensitiveContains(query) ||
                node.detail.localizedCaseInsensitiveContains(query) ||
                !children.isEmpty {
                return RuleNode(name: node.name, detail: node.detail, children: children)
            }
            return nil
        }
    }
}

struct RuleNodeRow: View {
    let node: RuleNode
    @State private var expanded = false

    var body: some View {
        if node.children.isEmpty {
            row
        } else {
            DisclosureGroup(isExpanded: $expanded) {
                ForEach(node.children.indices, id: \.self) { index in
                    RuleNodeRow(node: node.children[index])
                }
            } label: {
                row
            }
        }
    }

    private var row: some View {
        HStack(spacing: 16) {
            Text(node.name)
                .font(.system(size: 13, weight: .medium, design: .monospaced))
                .frame(width: 240, alignment: .leading)
            Text(node.detail)
                .font(.system(size: 12))
                .foregroundColor(Color(nsColor: KiroTheme.muted))
                .lineLimit(1)
            Spacer()
        }
        .padding(.vertical, 2)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

struct LearningView: View {
    @State private var snapshot = loadLearning()
    @State private var learning = true
    @State private var query = ""
    @State private var section = 0

    private var entries: [LearningEntry] {
        query.isEmpty ? snapshot.entries : snapshot.entries.filter {
            $0.candidate.localizedCaseInsensitiveContains(query) ||
                $0.context.localizedCaseInsensitiveContains(query)
        }
    }

    private var personal: [PersonalRule] {
        query.isEmpty ? snapshot.personal : snapshot.personal.filter {
            $0.candidate.localizedCaseInsensitiveContains(query) ||
                $0.context.localizedCaseInsensitiveContains(query)
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    Text("本地学习").font(.system(size: 24, weight: .semibold))
                    Text("\(learning ? "已启用" : "已关闭") · \(snapshot.entries.count) 条选择 · \(snapshot.personal.count) 条个人纠正")
                        .font(.system(size: 12))
                        .foregroundColor(Color(nsColor: KiroTheme.muted))
                        .help(snapshot.path)
                }
                Spacer()
                Toggle("启用学习", isOn: $learning)
                    .toggleStyle(.switch)
                    .onChange(of: learning) { value in setLearning(value) }
            }

            HStack(spacing: 12) {
                TextField("筛选候选", text: $query)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 280)
                Picker("", selection: $section) {
                    Text("选择记录").tag(0)
                    Text("个人规则").tag(1)
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .frame(width: 180)
                Spacer()
            }

            if section == 0 {
                selectionTable
            } else {
                personalTable
            }

            HStack(spacing: 10) {
                Button("刷新") { reload() }
                Button("清除全部…") { clear() }
                Spacer()
            }
        }
        .padding(.horizontal, 40)
        .padding(.vertical, 34)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .onAppear(perform: reload)
    }

    private var selectionTable: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 16) {
                Text("上下文").frame(width: 250, alignment: .leading)
                Text("候选").frame(width: 190, alignment: .leading)
                Text("次数").frame(width: 50, alignment: .trailing)
                Text("最近")
                Spacer()
            }
            .font(.system(size: 11, weight: .semibold))
            .foregroundColor(Color(nsColor: KiroTheme.muted))
            .padding(.bottom, 8)

            List {
                ForEach(entries.indices, id: \.self) { index in
                    let entry = entries[index]
                    HStack(spacing: 16) {
                        Text(entry.context)
                            .font(.system(size: 12, design: .monospaced))
                            .frame(width: 250, alignment: .leading)
                        Text(entry.candidate)
                            .font(.system(size: 12, design: .monospaced))
                            .frame(width: 190, alignment: .leading)
                        Text("\(entry.count)")
                            .frame(width: 50, alignment: .trailing)
                        Text(entry.last.map(Self.dateFormatter.string) ?? "—")
                            .font(.system(size: 12))
                            .foregroundColor(Color(nsColor: KiroTheme.muted))
                        Spacer()
                    }
                    .padding(.vertical, 2)
                }
            }
            .listStyle(.plain)
        }
    }

    private var personalTable: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 16) {
                Text("上下文").frame(width: 230, alignment: .leading)
                Text("候选").frame(width: 170, alignment: .leading)
                Text("胜/负").frame(width: 70, alignment: .trailing)
                Text("置信度").frame(width: 60, alignment: .trailing)
                Text("状态").frame(width: 62, alignment: .leading)
                Text("最近")
                Spacer()
            }
            .font(.system(size: 11, weight: .semibold))
            .foregroundColor(Color(nsColor: KiroTheme.muted))
            .padding(.bottom, 8)

            if personal.isEmpty {
                Text("还没有个人纠正。多次手动修改同一上下文后，这里会出现个人规则。")
                    .font(.system(size: 12))
                    .foregroundColor(Color(nsColor: KiroTheme.muted))
                    .padding(.top, 8)
            } else {
                List {
                    ForEach(personal.indices, id: \.self) { index in
                        let rule = personal[index]
                        HStack(spacing: 16) {
                            Text(rule.context)
                                .font(.system(size: 12, design: .monospaced))
                                .frame(width: 230, alignment: .leading)
                            Text(rule.candidate)
                                .font(.system(size: 12, design: .monospaced))
                                .frame(width: 170, alignment: .leading)
                            Text("\(rule.wins)/\(rule.losses)")
                                .frame(width: 70, alignment: .trailing)
                            Text(String(format: "%.0f%%", rule.confidence * 100))
                                .frame(width: 60, alignment: .trailing)
                            Text(rule.eligible ? "已生效" : "观察中")
                                .frame(width: 62, alignment: .leading)
                                .foregroundColor(rule.eligible ? Color(nsColor: KiroTheme.accent) : Color(nsColor: KiroTheme.muted))
                            Text(rule.last.map(Self.dateFormatter.string) ?? "—")
                                .font(.system(size: 12))
                                .foregroundColor(Color(nsColor: KiroTheme.muted))
                            Spacer()
                        }
                        .padding(.vertical, 2)
                    }
                }
                .listStyle(.plain)
            }
        }
    }

    private static let dateFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.dateFormat = "MM-dd HH:mm"
        return formatter
    }()

    private func reload() {
        snapshot = loadLearning()
        learning = snapshot.learning
    }

    private func setLearning(_ value: Bool) {
        let (code, output) = runTool("oc-manage", ["learning", value ? "on" : "off"])
        if code != 0 {
            showAlert("修改学习设置失败", output.isEmpty ? "oc-manage 返回 \(code)" : output)
            learning = !value
        }
    }

    private func clear() {
        let alert = NSAlert()
        alert.messageText = "清除全部本地学习数据？"
        alert.informativeText = "选择记录和个人纠正都会删除。"
        alert.addButton(withTitle: "清除")
        alert.addButton(withTitle: "取消")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        let (code, output) = runTool("oc-manage", ["clear", "--yes"])
        if code != 0 {
            showAlert("清除失败", output.isEmpty ? "oc-manage 返回 \(code)" : output)
            return
        }
        reload()
    }

}

struct LogsView: View {
    let active: Bool

    @State private var settings = loadLogSettings()
    @State private var files: [URL] = []
    @State private var selectedPath = ""
    @State private var content = ""
    @State private var autoRefresh = true
    @State private var autoScroll = true
    @State private var lastSize = -1

    private let timer = Timer.publish(every: 1, on: .main, in: .common).autoconnect()

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .firstTextBaseline) {
                Text("日志").font(.system(size: 24, weight: .semibold))
                Text(summary)
                    .font(.system(size: 12))
                    .foregroundColor(Color(nsColor: KiroTheme.muted))
                    .lineLimit(1)
                Spacer()
                if files.count > 1 {
                    Picker("", selection: $selectedPath) {
                        ForEach(files, id: \.path) { url in
                            Text(url.lastPathComponent).tag(url.path)
                        }
                    }
                    .labelsHidden()
                    .frame(width: 220)
                    .onChange(of: selectedPath) { _ in
                        lastSize = -1
                        refresh(force: true)
                    }
                }
            }

            ScrollViewReader { proxy in
                ScrollView {
                    Text(content.isEmpty ? "暂无日志" : content)
                        .font(.system(size: 11, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(14)
                        .id("log-end")
                }
                .background(
                    RoundedRectangle(cornerRadius: 8)
                        .fill(Color(nsColor: KiroTheme.surface))
                        .overlay(RoundedRectangle(cornerRadius: 8)
                            .stroke(Color(nsColor: KiroTheme.border), lineWidth: 1))
                )
                .onChange(of: content) { _ in
                    if autoScroll { proxy.scrollTo("log-end", anchor: .bottom) }
                }
            }

            HStack(spacing: 14) {
                Toggle("自动刷新", isOn: $autoRefresh)
                Toggle("自动滚动", isOn: $autoScroll)
                Spacer()
                Button("刷新") { reload() }
                Button("在 Finder 中显示") { revealLog() }
                Button("清空日志") { clear() }
            }
        }
        .padding(.horizontal, 40)
        .padding(.vertical, 34)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .onAppear(perform: reload)
        .onChange(of: active) { value in if value { reload() } }
        .onReceive(timer) { _ in
            if active && autoRefresh { tick() }
        }
    }

    private var selectedURL: URL {
        if let url = files.first(where: { $0.path == selectedPath }) { return url }
        return settings.currentURL
    }

    private var summary: String {
        let url = selectedURL
        if !settings.enabled { return "已关闭" }
        let attributes = try? FileManager.default.attributesOfItem(atPath: url.path)
        let size = (attributes?[.size] as? NSNumber)?.intValue ?? 0
        return "\(formatBytes(size)) · \(url.lastPathComponent)"
    }

    private func reload() {
        settings = loadLogSettings()
        files = listLogFiles(settings)
        if !files.contains(where: { $0.path == selectedPath }) {
            selectedPath = files.first?.path ?? settings.currentURL.path
        }
        lastSize = -1
        refresh(force: true)
    }

    private func tick() {
        let latest = loadLogSettings()
        if latest.currentURL.path != settings.currentURL.path ||
            latest.enabled != settings.enabled ||
            latest.retentionDays != settings.retentionDays {
            reload()
        } else {
            refresh(force: false)
        }
    }

    private func refresh(force: Bool) {
        let url = selectedURL
        let attributes = try? FileManager.default.attributesOfItem(atPath: url.path)
        let size = (attributes?[.size] as? NSNumber)?.intValue ?? 0
        if !force && size == lastSize { return }
        lastSize = size
        guard let data = try? Data(contentsOf: url) else {
            content = settings.enabled ? "暂无日志" : "日志已关闭"
            return
        }
        let tail = data.count > 64 * 1024 ? data.suffix(64 * 1024) : data
        content = String(decoding: tail, as: UTF8.self)
    }

    private func revealLog() {
        let url = selectedURL
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

    private func clear() {
        let url = selectedURL
        let alert = NSAlert()
        alert.messageText = "清空当前日志？"
        alert.addButton(withTitle: "清空")
        alert.addButton(withTitle: "取消")
        guard alert.runModal() == .alertFirstButtonReturn else { return }
        do {
            try Data().write(to: url)
            refresh(force: true)
        } catch {
            showAlert("清空日志失败", error.localizedDescription)
        }
    }
}

// MARK: - Main window

final class MainWindowController: NSObject, NSWindowDelegate {
    let window: NSWindow
    private let model = AppShellModel()
    private var host: NSHostingView<AppShellView>!
    var onVisibilityChanged: ((Bool) -> Void)?

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
        window.minSize = NSSize(width: 960, height: 620)
        window.isReleasedWhenClosed = false
        window.setFrameAutosaveName("OfflineCompleteMainWindow")
        window.delegate = self

        host = NSHostingView(rootView: AppShellView(model: model))
        host.frame = NSRect(origin: .zero, size: contentSize)
        host.autoresizingMask = [.width, .height]
        window.contentView = host
        window.center()

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
        onVisibilityChanged?(true)
    }

    func windowWillClose(_ notification: Notification) {
        onVisibilityChanged?(false)
    }

    func selectTab(_ identifier: String) {
        model.selected = identifier
    }

    @objc func refreshAll() {
        model.refreshAll()
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

// MARK: - Menu bar

final class MenuBarController: NSObject, NSMenuDelegate {
    private let controller: MainWindowController
    private var statusItem: NSStatusItem?

    init(controller: MainWindowController) {
        self.controller = controller
        super.init()
    }

    func start() {
        guard statusItem == nil else { return }
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let button = item.button {
            let image = NSImage(systemSymbolName: "bolt.horizontal.circle", accessibilityDescription: "Offline Complete")
            image?.isTemplate = true
            button.image = image
            if image == nil {
                button.title = "OC"
            }
            button.toolTip = "Offline Complete"
            button.imagePosition = .imageOnly
        }
        let menu = NSMenu()
        menu.delegate = self
        item.menu = menu
        statusItem = item
    }

    func stop() {
        guard let item = statusItem else { return }
        NSStatusBar.system.removeStatusItem(item)
        statusItem = nil
    }

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        let learning = loadLearning()
        addMetric("选择记录", "\(learning.entries.count) 条", to: menu)
        addMetric("个人纠正", "\(learning.personal.count) 条", to: menu)
        addMetric("学习状态", learning.learning ? "已启用" : "已关闭", to: menu)
        menu.addItem(.separator())
        let open = NSMenuItem(title: "打开主界面", action: #selector(openMain), keyEquivalent: "")
        open.target = self
        menu.addItem(open)
        let quit = NSMenuItem(title: "退出 Offline Complete", action: #selector(quitApp), keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)
    }

    private func addMetric(_ title: String, _ value: String, to menu: NSMenu) {
        let item = NSMenuItem(title: "\(title)：\(value)", action: nil, keyEquivalent: "")
        item.isEnabled = false
        menu.addItem(item)
    }

    @objc private func openMain() {
        controller.show()
    }

    @objc private func quitApp() {
        NSApp.terminate(nil)
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
    private var menuBarController: MenuBarController?
    private var mainWindowVisible = false

    init(screenshotPath: String?, initialTab: String?) {
        self.screenshotPath = screenshotPath
        self.initialTab = initialTab
    }

    func applicationWillFinishLaunching(_ notification: Notification) {
        buildMenu()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.applicationIconImage = makeAppIcon()
        controller.onVisibilityChanged = { [weak self] visible in
            self?.mainWindowVisible = visible
            self?.updateActivationPolicy()
        }
        if let initialTab {
            controller.selectTab(initialTab)
        }
        controller.show()
        NotificationCenter.default.addObserver(
            self,
            selector: #selector(menuBarSettingChanged),
            name: .menuBarSettingChanged,
            object: nil
        )
        syncMenuBar()
        let controller = self.controller
        DispatchQueue.main.async {
            if let error = ensureCommandLineIntegration() {
                let alert = NSAlert()
                alert.messageText = "无法启用新 zsh 窗口的自动补全"
                alert.informativeText = error
                alert.alertStyle = .warning
                alert.addButton(withTitle: "好")
                alert.runModal()
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

    @objc private func menuBarSettingChanged() {
        syncMenuBar()
    }

    private func syncMenuBar() {
        if loadMenuBarSetting() {
            if menuBarController == nil {
                menuBarController = MenuBarController(controller: controller)
            }
            menuBarController?.start()
        } else {
            menuBarController?.stop()
        }
        updateActivationPolicy()
    }

    private func updateActivationPolicy() {
        let menuBarEnabled = loadMenuBarSetting()
        // The main window behaves like a normal app while open. With the
        // menu-bar item enabled, closing it returns the process to background
        // mode and removes the Dock/Launchpad icon.
        let regular = mainWindowVisible || !menuBarEnabled
        NSApp.setActivationPolicy(regular ? .regular : .accessory)
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

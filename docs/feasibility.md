# 首阶段调查

调查日期：2026-11-10

## 语言与复用决策（2026-11-10）

项目实现改为 Go 1.27.3；`go.mod` 固定版本，依赖通过 `go mod vendor` 纳入仓库。Amazon Q/figterm 的 Rust 源码只用于行为、终端序列和规则格式参考，不作为构建依赖，也不进入运行时。PTY 使用 `github.com/creack/pty` v1.1.24；输入解析移植为 Go 子集（见 `internal/input/README.md`）。规则盘点工具改为 `go run ./cmd/inventory-rules`，不再使用 Node。macOS 候选窗口若需 AppKit，仅窗口层可用 Swift/Objective-C。以下上游调查记录保留为决策依据；其中的 `cargo`/Rust 命令描述的是被调查的上游项目，不代表本项目的构建方式。

## 上游

- 调查版本：`aws/amazon-q-developer-cli-autocomplete` 的 `b654a1beb880951dd2343120bb867659622eecd9`，临时检出用于调查，尚未纳入本仓库。
- 上游指定 Rust 1.87.0；在本机使用 Rust 1.99.1 执行 `cargo check --locked -p figterm` 成功，但产生上游警告和未来兼容性提示。这只验证 Apple Silicon 上的 figterm 编译检查，不代表完整应用可构建或可运行。完整构建还需验证 Node、pnpm、Python、protoc 和其他平台依赖。
- `crates/fig_desktop/src/protocol/spec.rs` 从远程服务获取规则索引及 JS，索引缓存于进程内。此结论针对调查的开源版本，不能推断当前闭源 Kiro 的全部行为。
- `crates/figterm` 和 shell 集成负责终端会话，`crates/fig_desktop` 包含窗口、IPC、macOS 平台代码和规则协议。`figterm` 直接依赖认证、请求和遥测模块；离线使用之前必须拆除或替换，拆分仍需进一步分析。

## 本机规则

- 已安装 `/Applications/Kiro CLI.app`，命令位于 `~/.local/bin/kiro-cli`。
- 应用资源中找到了补全界面 JS，尚未发现独立规则文件；界面 JS 不能直接视为规则库。
- 检查的 `~/Library/Application Support/kiro-cli/autocomplete/specs` 和 `~/Library/Application Support/amazon-q/autocomplete/specs` 均不存在。Kiro 数据库的表结构包含历史、状态和认证数据，没有单独的规则表；未读取这些表中的用户数据。
- Kiro 的 WebKit 网络缓存中发现的两个 blob 均为字体，未发现规则文件。
- 离线使用 `GOFLAGS=-mod=vendor GOPROXY=off go run ./cmd/inventory-rules '/Applications/Kiro CLI.app/Contents/Resources/autocomplete'`：扫描成功，仅列出 `assets/index-Ct4Ru0Ux.js`，600478 字节，SHA-256 `d4d4538b43eeaee133a94367b6b296a6fc153b07a9a6bee9a1a5f612d1d7f244`；这是界面 bundle，不能据此认定其中包含可提取规则或有再分发许可。未扫描历史、数据库或认证数据。
- 2026-11-10 对 Kiro 安装包 `Contents/Resources` 最多四级按 `*spec*`、`*completion*`、`*autocomplete*` 名称查找，仅发现上述 `autocomplete` 目录；没有读取用户历史、数据库或认证数据。
- 2026-11-11 额外离线检查 `~/.config/fig/autocomplete/specs`、`~/.fig/autocomplete/specs`、`~/.config/kiro/autocomplete/specs`，均报告 missing；列出 Kiro 和 Amazon Q 的应用数据顶层及 `shell` 目录名称，未读取历史、数据库、认证或 shell 脚本内容。重新扫描应用资源 autocomplete 目录仍只发现上述一个界面 bundle。
- 此次限定范围内**可确认独立规则文件数为 0，可提取规则数为 0**；界面 bundle 含一个 JS 文件，但未分析其内部数据，不能计为规则或断言内部没有规则。未发现可审计的静态规则格式，无法计算 `git`、`npm`、`docker` 覆盖率；规则来源和再分发许可须在 T12 单独核查。
- 以上不是整个磁盘或所有缓存的穷尽性搜索，也不能证明不存在其他可提取规则。若以后发现明确的规则目录，可用下述工具获取相对路径、大小及哈希，再单独核验格式和许可证；当前没有经过许可核查的规则可供提取或打包，不能把文件清单当作规则导出。

使用 `go run ./cmd/inventory-rules [规则目录 ...]` 可对指定目录生成文件大小和 SHA-256 清单。默认检查上述两个规则目录；不存在的目录会明确标记，不读取命令历史或数据库。扫描时发现的符号链接记录在 `skippedSymlinks`，不主动跟随；文件通过 `O_NOFOLLOW` 打开并检查读取前后的大小、mtime 和 ctime。单文件限制为 16 MiB，每根累计 64 MiB、10000 条目录项和 64 级目录；超限时整个扫描根报告错误而不输出不完整清单。哈希流式计算。根目录初始检查后的 ENOENT 视为扫描错误，而不是根目录缺失。遍历与最终复查比较文件和目录元数据；这不是快照，不能检测所有文件系统或复查结束后的变化。目录描述符相对遍历不跟随末级链接，但根路径祖先及特殊设备替换仍有边界，因此该工具尚不能安全扫描由不可信进程同时修改的目录。Go 测试使用临时目录覆盖权限错误、扫描中删除及文件读取时的修改。

## 窗口定位的离线源码预研（2026-11-11）

以下仅为本地检出的 Amazon Q `b654a1beb880951dd2343120bb867659622eecd9` 静态源码观察，不是本项目的实现，也不证明任何终端或权限组合可用：

- `crates/fig_util/src/terminal.rs` 的 `from_bundle_id` 区分 `com.googlecode.iterm2` 和 `com.apple.Terminal`；两者均列在 `supports_macos_accessibility`，不在 `supports_macos_input_method` 或 `is_xterm` 的匹配分支中。单凭 bundle ID 不能识别当前会话或保证焦点仍属于该会话。
- `crates/fig_desktop/src/platform/macos.rs` 的 `refresh_window_position` 对这两类终端走 `get_cursor_position`；后者调用 `macos-utils/src/caret_position.rs` 的 AX 聚焦元素、选区和 `kAXBoundsForRangeParameterizedAttribute`。缺少选区或矩形等情况返回无效位置；这说明依赖目标应用暴露 AX 属性，不证明两种终端在本机或未来版本一定提供这些属性。
- 上游 `macos-utils/src/accessibility.rs` 用 `AXIsProcessTrusted` 检查权限；`fig_desktop/src/platform/macos.rs` 中焦点变化与 Space 变化会触发隐藏、重新定位或状态变更。`macos-utils/src/window_server/ui_element.rs` 还使用 `_AXUIElementGetWindow`；这是带下划线的私有接口，不应据此假设本项目可以发布或在沙盒中使用它。
- 本项目的保守验收边界仍是定位、焦点、会话或权限无法确认时隐藏候选且不影响终端输入；不能依赖旧坐标或 bundle ID 向其他窗口插入。当前尚无候选窗口或该行为的生产实现。

待人工验证：在 iTerm2 和 macOS Terminal 分别记录 macOS、终端及 zsh 版本，辅助功能许可开启/关闭时的焦点、AX 光标矩形和失败结果，以及多窗口、多显示器、缩放、Space、焦点切换；再验证签名、公证、沙盒和辅助功能权限组合。最低 macOS 版本尚无证据，不能从源码观察推定；T04 保持未完成。

## 上游模块边界（初步）

以下路径相对于调查提交 `b654a1beb880951dd2343120bb867659622eecd9`。这是源码入口及内部 crate 依赖的静态盘点，不是运行时网络或完整第三方依赖审计。

| 能力 | 上游入口和直接依赖 | 离线版的初步处理 |
| --- | --- | --- |
| PTY / 终端 | 上游 `crates/figterm/src/main.rs` 的 `open_pty` 和尺寸同步；`pty/unix.rs` 使用 `nix`、`portable-pty`、`tokio`；`term/unix.rs` 管理本地终端 | 本项目用 Go `internal/session` + `creack/pty` 重写；上游实现只作行为参考，不能整包引入 |
| 输入解析 | 上游 `crates/figterm/src/input/`，大部源自 WezTerm termwiz | 已移植为 Go 子集（键位、Alt/Escape、括号粘贴、UTF-8）；鼠标、Kitty 编码、Windows 路径未移植 |
| shell hooks | `crates/fig_integrations/src/shell/scripts/post.zsh` 的 `fig_preexec`、`fig_precmd` 与 OSC；另有 inline completion 脚本 | 分析协议后用 Go/shell 脚本单独设计显式启用的集成，不覆盖现有配置 |
| 本地 IPC | `crates/figterm/src/ipc.rs` 绑定 Unix socket；协议涉及 `fig_ipc`、`fig_proto` | Go `internal/ipc` 已实现分帧、握手、UID 校验及私有监听器原型；路径竞态和生产接入仍未完成，不假定上游协议满足安全要求 |
| macOS 窗口与定位 | `crates/fig_desktop/src/platform/macos.rs` 使用 Accessibility、`macos_utils::caret_position` 和窗口服务；桌面进程使用 WebView | 核心保持 Go；窗口层若必须用 AppKit，仅限 Swift/Objective-C，需实际验证焦点、权限及两种终端 |
| 规则获取 | `crates/fig_desktop/src/protocol/spec.rs` 使用 `fig_request` 和认证获取规则 | 不引入远程规则协议；仅加载经许可的本地声明式资源 |
| 认证、请求与遥测 | 上游 `crates/figterm/Cargo.toml` 直接依赖 `fig_auth`、`fig_request`、`fig_telemetry`；`inline/mod.rs` 使用认证和 API，`main.rs` 初始化遥测，`message.rs` 可发送事件 | 不迁入这些路径；Go 依赖需单独审计，不能沿用上游依赖图 |
| 更新 | `crates/figterm/src/update.rs` 调用 `fig_install::check_for_updates`，并引用遥测 | 不迁入更新路径 |

### 上游关键传递依赖（清单静态核查）

以下箭头表示对应 `crates/<名称>/Cargo.toml` 的生产依赖声明，不代表调用一定发生，也不是对完整依赖闭包的审计：

- `figterm` → `fig_install` → `fig_request` → `reqwest`、TLS；`fig_install` 还依赖 `fig_telemetry` 和 `reqwest`。因此只去除终端入口中显式的更新调用不足以证明依赖树不含网络代码。
- `figterm` → `fig_telemetry` → `fig_auth`、`fig_request` 及 AWS 客户端；`figterm/src/main.rs` 初始化和结束遥测，`message.rs` 可发送事件。
- `figterm` → `fig_api_client` → `fig_auth`、`fig_request` 和 AWS 客户端；`inline/mod.rs` 使用此客户端和认证。它不是应直接迁入的静态补全路径。
- `fig_desktop` → `fig_integrations`；macOS 条件依赖中 `fig_integrations` → `fig_telemetry`。shell 集成脚本作为行为参考，不意味着可将整个集成 crate 作为无遥测依赖复用。
- `figterm`、`fig_desktop` → `fig_ipc` → `fig_proto`；`fig_proto` 使用 prost 及构建时 `prost-build`。本项目的 JSON 帧不是上游协议的兼容实现。
- `fig_desktop/src/protocol/spec.rs` 使用 `fig_auth` 和 `fig_request` 取得规则；`figterm/src/update.rs` 调用 `fig_install::check_for_updates`。这些路径均不迁入。

### 内部生产依赖图（跨平台静态超集）

2026-11-10 在本机核对 `/private/tmp/offline-complete-upstream` 的 HEAD 为上述固定提交后，以 Python 3 标准库 `tomllib` 读取 `crates/*/Cargo.toml`，从 `figterm` 和 `fig_desktop` 递归遍历普通 `[dependencies]` 和所有 `[target.*.dependencies]`。下表每行箭头的**右侧是左侧 crate 的直接内部依赖**；递归追踪这些箭头可得到传递依赖。包括所有平台条件和可选依赖，因而是超集，不表示这些边在 macOS 上同时启用。未展开开发依赖、build-dependencies、外部 crate 或 feature 选择，未执行上游代码，也不代表运行时一定调用这些依赖。

| 内部 crate | 直接内部生产依赖（各平台并集） |
| --- | --- |
| figterm | alacritty_terminal, fig_api_client, fig_auth, fig_install, fig_ipc, fig_log, fig_os_shim, fig_proto, fig_request, fig_settings, fig_telemetry, fig_telemetry_core, fig_util |
| fig_desktop | dbus, fig_api_client, fig_auth, fig_desktop_api, fig_install, fig_integrations, fig_ipc, fig_log, fig_os_shim, fig_proto, fig_remote_ipc, fig_request, fig_settings, fig_telemetry, fig_util, macos-utils, zbus |
| alacritty_terminal | fig_os_shim |
| fig_api_client | amzn-codewhisperer-client, amzn-codewhisperer-streaming-client, amzn-consolas-client, amzn-qdeveloper-streaming-client, fig_auth, fig_aws_common, fig_os_shim, fig_request, fig_settings, fig_util |
| fig_auth | fig_aws_common, fig_settings, fig_telemetry_core, fig_util |
| fig_install | dbus, fig_integrations, fig_ipc, fig_os_shim, fig_request, fig_settings, fig_telemetry, fig_util |
| fig_ipc | fig_proto, fig_util |
| fig_log | fig_os_shim, fig_util |
| fig_os_shim | 无内部生产依赖 |
| fig_proto | fig_util |
| fig_request | fig_os_shim, fig_settings, fig_util |
| fig_settings | fig_util |
| fig_telemetry | amzn-codewhisperer-client, amzn-toolkit-telemetry-client, aws-toolkit-telemetry-definitions, fig_api_client, fig_auth, fig_aws_common, fig_ipc, fig_os_shim, fig_proto, fig_request, fig_settings, fig_telemetry_core, fig_util, macos-utils |
| fig_telemetry_core | amzn-codewhisperer-client, amzn-toolkit-telemetry-client, aws-toolkit-telemetry-definitions, fig_util |
| fig_util | fig_os_shim, macos-utils |
| dbus | fig_os_shim, fig_util, zbus |
| fig_desktop_api | dbus, fig_api_client, fig_auth, fig_install, fig_integrations, fig_os_shim, fig_proto, fig_settings, fig_telemetry, fig_telemetry_core, fig_util, macos-utils |
| fig_integrations | dbus, fig_os_shim, fig_settings, fig_telemetry, fig_util, macos-utils |
| fig_remote_ipc | fig_ipc, fig_proto, fig_util |
| macos-utils | 无内部生产依赖 |
| zbus | zbus_names |
| fig_aws_common | fig_request |
| aws-toolkit-telemetry-definitions | amzn-toolkit-telemetry-client |
| amzn-codewhisperer-client, amzn-codewhisperer-streaming-client, amzn-consolas-client, amzn-qdeveloper-streaming-client, amzn-toolkit-telemetry-client, zbus_names | 各自无内部生产依赖 |

`dbus`、`zbus` 在桌面入口中属于 Linux 条件依赖；`macos-utils` 是 macOS 条件依赖。该图用于识别重写边界，不证明上游模块能独立拆分，也不证明构建产物或运行时无网络。外部依赖闭包、各 feature 的实际启用、资源许可与系统权限仍需分别审计；离线运行须按 T22 实测。

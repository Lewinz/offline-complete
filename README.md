# Offline Complete

独立的离线终端补全工具（开发版），目标环境是 macOS + zsh + iTerm2/Terminal。核心用 Go 实现，候选窗口用 AppKit；默认不依赖账号、云服务或运行时网络，只有显式启用 AI generator 时才会访问配置的模型端点。

## 构建与测试

要求 macOS 13+、Xcode Command Line Tools（`swiftc`）、Go 1.27.3（由 `g` 管理在 `~/.g/go`）。依赖已 vendor，构建不需要联网。

```bash
make test      # 全量测试，vendor 离线
make vet
make build     # 编译检查
make bin       # 生成 bin/ 下的可执行文件
make dist      # 生成 Apple Silicon .app、.zip、.dmg（ad-hoc 签名）
make pkg       # 生成可直接双击的系统安装包 .pkg
```

## 安装与运行

### 系统安装包（推荐）

```bash
make pkg
open dist/Offline-Complete.pkg
```

安装器会要求管理员密码，把应用装到 `/Applications/Offline Complete.app`，把 `oc`、`oc-app`、`oc-manage`、`oc-window`、`oc-client` 装到 `/usr/local/bin`，并在 `~/.zshrc` 顶部写入一个带标记的自动启动块。**之后新开的交互式 zsh 窗口自动进入 `oc` 补全会话，不需要手动输入 `oc`。** 现有已打开的 shell 不受影响。

```bash
open "/Applications/Offline Complete.app"
# 新开一个 zsh 窗口，直接输入 git 即可
```

`oc` 命令仍然保留，用于显式启动或调试。

卸载：

```bash
sudo scripts/uninstall-pkg.sh
```

### DMG 安装（无需 pkg）

```bash
make dist
open dist/Offline-Complete.dmg
# 把 Offline Complete.app 拖到 Applications，然后打开应用
```

首次启动时，应用会自动：

- 安装/更新 `~/.local/bin/oc`、`oc-manage`、`oc-window`、`oc-client`、`oc-app` 启动器；
- 在 `~/.zshrc` 顶部写入带标记的自动启动块；
- 之后新开的 zsh 窗口自动进入补全会话。

自动启动只在 Offline Complete 应用运行时生效。关闭主窗口时应用会留在后台，自动补全继续工作；只有真正退出应用（`Cmd+Q` 或菜单“退出 Offline Complete”）后，新终端才不再自动启用，已经运行的自动会话也会同步隐藏补全窗口。显式执行 `oc` 始终可以单独启动一次补全会话。

不需要再执行 `scripts/install.sh`。DMG 里的“安装说明.txt”也有同样步骤。

### 用户级脚本安装

```bash
make dist
scripts/install.sh      # 安装到 ~/Applications，并生成启动器和 zsh 集成
```

`oc` 仍可用于显式启动。

`oc` 会带上安装包内的规则包启动 zsh，并在同一进程里自动拉起候选窗口。管理窗口有 Dock 图标，可查看规则、学习记录和 agent 日志；候选窗口是独立的非激活面板，不出现在 Dock。卸载：

```bash
scripts/uninstall.sh
```

用户级安装和 DMG 首次启动会写入 `~/Applications`（或 `/Applications`）、`~/.local/bin` 启动器和 `~/.zshrc` 顶部的带标记自动启动块；系统安装包对应 `/Applications`、`/usr/local/bin` 和当前控制台用户的 `.zshrc`。都不修改 Kiro 配置。自动启动块可以用 `oc-manage shell uninstall` 移除。

首次使用光标定位时，需要授予辅助功能权限：

```bash
oc-window --accessibility-prompt
oc-window --print-caret   # 授权后自检
```

未授权、终端不暴露文本范围或多显示器定位失败时，窗口回退到主屏右上角。

## 交互

- `↑` / `↓`、`Ctrl-P` / `Ctrl-N`、`Tab`：移动高亮
- `Enter`：插入当前候选，不会执行命令
- `Esc`：关闭候选
- 鼠标点击候选插入，点击空白关闭

键盘导航由 agent 在 PTY 输入路径拦截，不抢终端焦点；没有候选时按键原样转发给 zsh。插入使用 emacs 键位（Ctrl-E / Ctrl-U），拒绝含控制字符的整行重写。

## 管理与隐私

```bash
open "$HOME/Applications/Offline Complete.app"   # 图形管理窗口
oc-manage status
oc-manage learning off        # 关闭后不再记录或排序
oc-manage learning on
oc-manage clear --yes         # 删除全部本地选择记录
oc-manage logging status      # 日志开关、目录、保留天数
oc-manage logging off         # 运行中的 oc 在下一次日志事件停止写入
oc-manage logging set --enabled on --directory "$HOME/.oc/logs" --retention-days 3
oc-manage shell status        # 新 zsh 窗口自动启动状态
oc-manage shell install       # 写入/恢复 ~/.zshrc 标记块
oc-manage shell uninstall     # 移除标记块
oc-manage doctor              # 检查数据目录、规则、窗口、Kiro 共存
```

管理窗口的“规则”页显示命令/子命令/选项树，“学习”页显示选择记录并可开关学习或清除，“日志”页实时显示并切换保留期内的日志，“设置”页可开关日志、修改目录和保留天数。规则页只读，用户覆盖和建议管理仍未实现。

学习数据在 `~/Library/Application Support/offline-complete/learning.json`，目录 0700、文件 0600，只保存“命令/子命令/选项或槽位/候选名”的计数和最后选择时间；不保存完整命令行、参数值、路径、环境变量或输出。超过 90 天的记录会被清理。

agent 日志默认在 `~/.oc/logs/agent-YYYY-MM-DD.log`（目录 0700、文件 0600，保留 3 个自然日，单文件超过 2 MB 时下次打开轮转）。默认只记录启动、规则/学习状态、补全候选数量、选择和错误，不记录输入文本或候选名；本地调试可设 `OFFLINE_COMPLETE_LOG_TEXT=1`，它会把输入行和候选名写入日志，调试后请关闭。日志开关、目录和保留天数可在管理窗口“设置”页或 `oc-manage logging` 修改。详见 [日志](docs/LOGGING.md)。

## 开发调试

不用安装，也可以手动跑两个终端：

```bash
OFFLINE_COMPLETE_RULES=rules/base.json bin/offline-complete zsh -i
# offline-complete: session=... socket=...
bin/oc-window -socket <path> -session <id>
```

`bin/oc-client` 是终端版候选替代品；`bin/oc-window --check` 只验证 socket 协议。测试时可用 `OFFLINE_COMPLETE_NO_WINDOW=1` 禁止自动拉起窗口。

## 文档

- [安装与卸载](docs/INSTALL.md)
- [隐私与本地数据](docs/PRIVACY.md)
- [日志](docs/LOGGING.md)
- [规则包](docs/RULES.md)
- [已知限制](docs/LIMITATIONS.md)
- [发布、签名与离线审计](docs/RELEASE.md)
- [兼容性矩阵](docs/COMPATIBILITY.md)
- 实施计划：`docs/superpowers/plans/2026-11-10-offline-completion.md`

## 当前状态

已经能跑通：zsh 编辑状态采集、静态规则候选、键盘/鼠标选择、安全整行插入、光标跟随（需 Accessibility）、私有 IPC、学习排序、管理 CLI、AppKit 管理窗口（规则/学习/日志）、`.app`/`.dmg` 打包和安装脚本。

仍是开发版：未用 Developer ID 签名/公证，未在干净 VM 做离线审计，未做 iTerm2/Terminal 真机矩阵；仅支持 Apple Silicon；学习存储用版本化 JSON 而非设计中的 SQLite，管理窗口没有用户覆盖和建议操作。详见 [已知限制](docs/LIMITATIONS.md)。

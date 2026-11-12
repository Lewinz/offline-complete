# 安装与卸载

## 构建

```bash
make test-all     # 先构建 bin/，再跑全量测试
make dist         # 生成 dist/Offline Complete.app、zip、dmg
make pkg          # 在 dist/ 生成 Offline-Complete.pkg
```

`make dist` 使用 ad-hoc 签名（`codesign --sign -`），没有 Developer ID，也没有公证。`make pkg` 生成未签名的系统安装包；本地可以安装，但从网络分发时 Gatekeeper 会提示未验证开发者。DMG 里的应用首次启动会自安装用户级命令和 zsh 集成。构建需要 `swiftc` 和 Go 1.27.3。

## DMG 安装（无需 pkg）

```bash
make dist
open dist/Offline-Complete.dmg
# 把 Offline Complete.app 拖到 Applications，然后打开应用
```

首次启动会自动安装 `~/.local/bin` 启动器，并在 `~/.zshrc` 顶部写入带标记的自动启动块。之后新开的 zsh 窗口自动进入补全会话。自动启动只在主应用运行时生效。关闭主窗口时应用留在后台，自动补全继续工作；只有真正退出应用（`Cmd+Q` 或菜单“退出 Offline Complete”）后，新终端才不再自动启用，已经运行的自动会话也会隐藏候选窗口。显式执行 `oc` 不受影响。

把应用拖到 `~/Applications` 不需要管理员权限；拖到 `/Applications` 由 Finder 决定是否要求管理员权限。

不需要再执行 `scripts/install.sh`。DMG 中的“安装说明.txt”包含同样步骤。

## 系统安装包（推荐用于系统级安装）

```bash
make pkg
open dist/Offline-Complete.pkg
# 或
sudo installer -pkg dist/Offline-Complete.pkg -target /
```

安装器需要管理员权限，会：

- 把 `Offline Complete.app` 安装到 `/Applications/`；
- 把 `oc`、`oc-app`、`oc-manage`、`oc-window`、`oc-client` 安装到 `/usr/local/bin/`；
- 如果 `/usr/local/bin` 中已有同名且不是本应用生成的命令，preinstall 会中止并提示，不会覆盖。

安装器还会在控制台用户的 `~/.zshrc` 顶部写入带标记的自动启动块。之后**新开的交互式 zsh 窗口会在主应用运行时自动进入补全会话**，不需要手动输入 `oc`。关闭主窗口不会退出应用，补全继续工作；真正退出应用后自动启动停止，现有已打开的 shell 会隐藏候选窗口。`oc` 命令仍然保留。

```bash
open "/Applications/Offline Complete.app"
# 新开一个 zsh 窗口，输入 git
```

查看或移除自动启动：

```bash
oc-manage shell status
oc-manage shell uninstall
oc-manage shell install      # 重新启用
```

卸载：

```bash
sudo scripts/uninstall-pkg.sh
```

用户数据、日志和设置不会删除。

## 用户级安装（无需管理员）

```bash
scripts/install.sh
```

脚本会：

- 把 `dist/Offline Complete.app` 复制到 `~/Applications/Offline Complete.app`；
- 写入 `~/.local/bin/oc` 以及 `oc-manage`、`oc-window`、`oc-client`、`oc-app` 启动器；
- 在 `~/.zshrc` 顶部写入带标记的自动启动块；
- 不修改 Kiro 配置。

运行脚本后打开 `~/Applications/Offline Complete.app`；自动启动只在主应用运行时生效。自动启动块会临时把 `~/.local/bin` 放到 `PATH` 前面，不需要手动改 `PATH`。

## 运行

```bash
oc              # 等价于 oc zsh -i
oc /bin/zsh     # 显式指定 shell
```

完整的 Kiro generator runtime 使用应用自带的 Node.js 22（`Contents/Resources/node/node-arm64`），不依赖用户 PATH 中的 Node。`OFFLINE_COMPLETE_NODE` 只用于开发调试覆盖；如果内置运行时缺失，才退回静态补全和简单的脚本/template generator。 导入 spec 时会把 AI custom generator 中的 `fig settings` / `fig _ request /ai/chat` 精确改写为 `oc setting` / `oc ai request`；原始 spec 包是本地冻结资源，不需要运行时联网更新。可选的 OC AI generator 在应用的 **AI 补全** 页面配置，默认关闭，只支持 OpenAI Chat Completions 和 Anthropic Messages 两种协议，不调用 Kiro 的 AI 服务。

应用页面会写入 `~/.oc/config.json`（权限 0600），其中包含 API key；新开的 `oc` 会话使用配置。环境变量 `OFFLINE_COMPLETE_AI_*` 仍可用于测试/开发，但应用设置会覆盖继承的环境变量。

OpenAI endpoint 示例：

```text
https://api.openai.com/v1/chat/completions
```

Anthropic endpoint 示例：

```text
https://api.anthropic.com/v1/messages
```

协议只使用非流式文本结果，不支持 tools、图片输入或 SSE。

新 zsh 窗口的自动启动块会执行 `oc`，由它把规则路径指向应用内的 `Contents/Resources/rules.bin` 并拉起 `oc-window`。候选窗口不抢终端焦点，键盘导航在 agent 内处理。`oc` 仍可用于显式启动。

管理窗口：

```bash
# 系统安装包
open "/Applications/Offline Complete.app"
# 用户级安装
open "$HOME/Applications/Offline Complete.app"
# 任一安装方式
oc-app
```

它会出现在 Dock，包含“概览 / 规则 / 学习 / 日志 / 设置”五页；候选窗口与它分离，不会出现在 Dock。关闭管理窗口后应用仍在 Dock 中，点击图标可重新打开。“设置”页可开关日志、修改日志目录和保留天数。默认日志目录是 `~/.oc/logs`，详见 [LOGGING.md](LOGGING.md)。

首次使用光标定位：

```bash
oc-window --accessibility-prompt
```

然后在“系统设置 → 隐私与安全性 → 辅助功能”中允许 `oc-window`。未授权时窗口固定在主屏右上角。

## 卸载

```bash
scripts/uninstall.sh
```

先移除 `~/.zshrc` 中的带标记自动启动块，再删除带标记的 `~/.local/bin/oc`（以及 `oc-manage`、`oc-window`、`oc-client`、`oc-app`）和 `~/Applications/Offline Complete.app`。不会删除学习数据或日志；需要删除时运行：

```bash
oc-manage clear --yes
rm -rf "$HOME/Library/Application Support/offline-complete"
rm -rf "$HOME/.oc"          # 日志设置和日志；会一并删除
```

## 与 Kiro 共存

自动启动块会插在 `~/.zshrc` 顶部，Kiro 的 pre/post block 仍会执行。`oc-manage doctor` 会检查 Kiro；同一个 shell 中不要同时期待两套补全代理接管输入。若 Kiro 终端出现冲突，先执行 `oc-manage shell uninstall`，再用 `oc` 显式启动。

自动启动块位于：

```text
# >>> offline-complete integration >>>
...
# <<< offline-complete integration <<<
```

它只对交互式、非 root、非 SSH 的 zsh 生效；已经处于 `OFFLINE_COMPLETE_ACTIVE` 的 shell 不会重复嵌套。

## 签名与 Gatekeeper

当前产物是 ad-hoc 签名、未公证的开发构建。从网络下载后 Gatekeeper 可能阻止首次打开；正式发布必须使用 Developer ID 签名并公证，步骤见 [RELEASE.md](RELEASE.md)。

# Offline Complete

Offline Complete 是一个面向 macOS 的离线终端补全工具。它在 zsh 里提供命令、子命令、选项和文件路径补全，并根据你的实际选择和个人纠正逐步调整排序。

- 支持 Apple Silicon Mac，要求 macOS 13+
- 不需要账号或云服务
- 静态规则和本地学习都在本机完成
- 默认会检查 GitHub Releases 的更新，可在「设置 → 更新」关闭
- 只有主动开启 AI 补全后，才会访问你配置的模型接口

## 下载与安装

从 [GitHub Releases](https://github.com/Lewinz/offline-complete/releases) 下载最新的 `Offline-Complete.dmg`。

1. 打开 DMG。
2. 把 `Offline Complete.app` 拖到“Applications”。
3. 打开应用。首次启动会安装命令行启动器，并在 `~/.zshrc` 顶部写入一个简短的自动启动引用；实际逻辑在独立的 `shell-integration.zsh` 中。
4. 新开一个 zsh 窗口即可使用；已经打开的终端不会自动接管。

### 首次启动被 macOS 拦截

出现“Apple 无法验证…”时，这是未公证应用的正常提示。

单次允许：点“完成”，打开“系统设置 → 隐私与安全性”，在“安全性”中点“仍要打开”。

临时允许任何来源：

```bash
# macOS 15+
sudo spctl --global-disable

# macOS 14 及更早版本
sudo spctl --master-disable
```

然后到“系统设置 → 隐私与安全性 → 允许来自以下来源的应用程序”选择“任何来源”。

恢复默认设置：

```bash
# macOS 15+
sudo spctl --global-enable

# macOS 14 及更早版本
sudo spctl --master-enable
```

## 使用

### 菜单栏与主界面

应用默认在菜单栏常驻，不占用 Dock：

- 点击菜单栏图标可以查看学习指标、打开主界面或退出。
- 主界面打开时显示 Dock 图标；关闭主界面后回到后台，只保留菜单栏图标。
- 设置页可以关闭「常驻菜单栏」。关闭后应用恢复普通窗口模式。

主界面包含：

- **开始**：权限和自动补全状态
- **本地学习**：选择记录和个人纠正
- **日志**：agent 日志
- **AI 补全**：可选的模型补全设置
- **设置**：补全按键、菜单栏、日志、备份

### 补全

在新开的 zsh 窗口中输入命令时，候选会自动出现：

- `↑` / `↓`、`Ctrl-P` / `Ctrl-N`、`Tab`：移动候选
- `Enter`：插入当前候选
- `Esc`：关闭候选
- 鼠标点击候选插入，点击空白关闭

在候选列表第一项再按 `↑`（或 `Ctrl-P`）会关闭候选并把按键交给 shell，继续向上翻历史。选择一项候选后，如果新位置仍有候选，窗口会就地更新继续提示；没有候选时才关闭。位置参数候选会补一个空格进入下一个参数，目录候选保留 `/` 结尾继续逐级进入。可变参数（例如 autojump 的目录关键字）选中普通值后不会自动继续弹出同一参数列表。

按键行为可以在「设置 → 补全」中修改：

- Tab：下一项 / 选择当前项
- 回车：选择当前项 / 直接执行命令
- 精确匹配时直接执行

修改补全设置后，建议新开一个终端窗口。

## 自动更新

应用默认在启动后和运行期间检查 [GitHub Releases](https://github.com/Lewinz/offline-complete/releases)：

1. 读取最新 release 的版本号。
2. 发现新版本时下载 `Offline-Complete.dmg` 和对应的 `.sha256`。
3. 校验 SHA-256，挂载 DMG，检查应用标识、版本和代码签名，然后解包到本机更新目录。
4. 在「设置 → 更新」或菜单栏显示「重启并安装」；点击后退出当前应用，替换 `/Applications/Offline Complete.app` 并重新打开。

检查更新会访问 `github.com` 的 releases/latest 重定向和 release 下载地址，不发送遥测或账号信息。可以在「设置 → 更新」关闭自动检查，也可以在命令行关闭：

```bash
oc-manage update auto off
```

已经发布的旧版本还没有更新器，因此第一次升级到带更新器的版本仍需要手动安装；之后的新 tag 就可以自动下载并提示重启安装。

## 本地学习

Offline Complete 会记录你在补全列表中的选择，用来调整同一上下文下的候选顺序。

当你没有选择候选，而是手动输入了另一个结果时，系统会把它记录为一次“个人纠正”。例如：

```text
内置候选：Documents/
你实际输入：Downloads/
```

反复纠正后，`Downloads/` 可能在相同命令、子命令和参数位置下成为额外候选；原始静态规则不会被修改，个人结果保存在本地覆盖层中。

学习按 **git 仓库根目录** 隔离：同一仓库的子目录共享偏好，不同仓库互不影响。手动输入已经出现在候选中的值也会记为一次偏好，因此“经常用 `upstream main`，但候选第一项是 `dev`”会逐步让 `main` 排到前面。升级后仓库内使用按仓库的新记录，旧的全局记录会保留但不再跨仓库生效。

个人纠正至少需要 **3 次、跨 2 个日期** 才会作为候选生效。学习可以在「本地学习」中关闭或清除。

## 数据与备份

所有个人数据都在本机：

```text
~/.oc/
├── learning.json      # 选择记录和个人纠正
├── config.json        # 设置、日志设置、AI 设置
├── learning.json.bak  # 导入学习数据前的备份
└── logs/              # agent 日志
```

换电脑时，打开「设置 → 数据与备份」，使用「导出备份…」和「导入备份…」。备份是一个 zip 压缩包，包含：

```text
learning.json
config.json
manifest.json
README.txt
```

导入前会校验文件哈希，并备份当前数据。备份包含 AI API Key，请按敏感文件保管；日志、规则包和运行中的 socket 不会被打包。

也可以使用命令行：

```bash
oc-manage backup export --file ~/offline-complete-backup.zip
oc-manage backup import --file ~/offline-complete-backup.zip
```

## 隐私

- 默认只访问 GitHub Releases 检查更新；可在「设置 → 更新」或 `oc-manage update auto off` 关闭。
- 开启 AI 补全后才会请求你配置的模型端点。
- 本地学习不会保存完整命令行、环境变量或命令输出。
- 个人纠正会保存用户实际提交的 token；它可能包含路径或参数值，因此只保存在本机。
- 日志默认不记录输入文本或候选名。
- 可以随时关闭学习、清除数据或在管理窗口中删除日志。

更多细节见 [隐私与本地数据](docs/PRIVACY.md)。

## 常见问题

**看不到应用图标？**

默认应用只在菜单栏常驻。点击菜单栏的 OC 图标打开主界面；关闭主界面后 Dock 图标会消失。也可以执行：

```bash
open "/Applications/Offline Complete.app"
```

**新终端没有自动补全？**

确认应用正在运行，然后重新打开一个 zsh 窗口。已经打开的终端不会自动切换，可以执行：

```bash
oc
```

显式启动一次补全会话。

**没有候选窗口？**

在「开始」页检查辅助功能权限。需要时可以执行：

```bash
oc-window --accessibility-prompt
```

还可用 `oc-manage doctor` 检查安装、规则和 shell 集成状态。

**没有规则的自定义命令能补全吗？**

可以。静态规则和 Kiro spec 都没有收录的命令（例如你自己的 `dotfiles`）在输入到参数位置后，会尝试运行一次 `<command> --help`，解析其中的子命令、选项和 `[a|b]` 取值，并缓存在 `~/.oc/help/`。第一次需要等帮助解析完成，候选会自动补发，不需要再敲一个字符。候选窗口底部有「刷新」入口（只对 help 自省命令显示），会清除当前命令的缓存并重新解析；缓存默认保留 24 小时，命令文件变化会立即失效。只运行帮助入口，不会执行正常子命令；超时后 5 秒、解析失败后 30 秒内不重试。详见 [规则包](docs/RULES.md#无规则命令的-help-自省)。需要用环境变量关闭时：

```bash
OFFLINE_COMPLETE_NO_HELP_FALLBACK=1 oc zsh -i
```

**想完全关闭后台行为？**

退出菜单栏应用，或执行：

```bash
oc-manage shell uninstall
```

## 卸载

```bash
scripts/uninstall.sh
```

学习数据和日志默认保留。需要一起删除时：

```bash
oc-manage clear --yes
rm -rf "$HOME/.oc"
```

## 开发与发布

需要 macOS 13+、Xcode Command Line Tools、Go 1.27.3 和 vendor 依赖。

```bash
make encrypt       # 首次从源码构建时生成加密规则包
make test          # 测试
make bin           # 生成 bin/ 下的开发二进制
make dist          # 生成 Apple Silicon .app / .zip / .dmg
```

发布构建支持传入版本号：

```bash
make dist APP_VERSION=1.2.3 APP_BUILD=42
```

`APP_VERSION` 会写入 `CFBundleShortVersionString`，主界面左下角显示为 `v1.2.3`。

`make dist` 首次运行会创建本地自签名证书 `Offline Complete Dev`。正式发布使用 GitHub Actions：推送 `v*` tag 会构建 Apple Silicon DMG，并把 DMG 和 SHA-256 校验文件附加到 GitHub Release。

```bash
git tag v1.2.3
git push origin v1.2.3
```

发布构建会把 tag 去掉前导 `v` 后写入 `CFBundleShortVersionString`；主界面左下角显示的版本号来自应用 Info.plist。

## 文档

- [安装与卸载](docs/INSTALL.md)
- [隐私与本地数据](docs/PRIVACY.md)
- [日志](docs/LOGGING.md)
- [规则包](docs/RULES.md)
- [已知限制](docs/LIMITATIONS.md)
- [发布、签名与离线审计](docs/RELEASE.md)
- [兼容性矩阵](docs/COMPATIBILITY.md)

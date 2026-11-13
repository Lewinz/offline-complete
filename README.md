# Offline Complete

Offline Complete 是一个面向 macOS 的离线终端补全工具。它在 zsh 里提供命令、子命令、选项和文件路径补全，并根据你的实际选择和个人纠正逐步调整排序。

- 支持 Apple Silicon Mac，要求 macOS 13+
- 不需要账号或云服务
- 静态规则和本地学习都在本机完成
- 只有主动开启 AI 补全后，才会访问你配置的模型接口

## 下载与安装

从 [GitHub Releases](https://github.com/Lewinz/offline-complete/releases) 下载最新的 `Offline-Complete.dmg`。

1. 打开 DMG。
2. 把 `Offline Complete.app` 拖到“Applications”。
3. 打开应用。首次启动会安装命令行启动器，并在 `~/.zshrc` 顶部写入带标记的自动启动块。
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

按键行为可以在「设置 → 补全」中修改：

- Tab：下一项 / 选择当前项
- 回车：选择当前项 / 直接执行命令
- 精确匹配时直接执行

修改补全设置后，建议新开一个终端窗口。

## 本地学习

Offline Complete 会记录你在补全列表中的选择，用来调整同一上下文下的候选顺序。

当你没有选择候选，而是手动输入了另一个结果时，系统会把它记录为一次“个人纠正”。例如：

```text
内置候选：Documents/
你实际输入：Downloads/
```

反复纠正后，`Downloads/` 可能在相同命令、子命令和参数位置下成为额外候选；原始静态规则不会被修改，个人结果保存在本地覆盖层中。

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

- 默认不联网，只有开启 AI 补全后才会请求你配置的端点。
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

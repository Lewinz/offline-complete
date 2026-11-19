# 隐私与本地数据

## 存储位置

```text
~/.oc/
├── learning.json        # 选择计数和个人纠正（0600）
├── learning.json.bak    # 导入前的自动备份（0600）
├── learning-export.json # 默认导出位置（可选，0600）
├── config.json          # 日志/AI/更新设置（0600）
└── logs/
    └── agent-YYYY-MM-DD.log  # 诊断日志（0600，默认保留 3 天）

~/Library/Application Support/offline-complete/
├── learning.json        # 旧版本学习数据；首次打开时迁移到 ~/.oc
├── updates/             # 已下载并校验的更新包（目录 0700，安装后清理）
└── run/s-*/socket       # 运行中的会话 socket（目录 0700）
```

目录必须为当前用户私有（0700）；写入使用临时文件 + 原子 rename，文件权限 0600。

## 记录什么

学习数据包含两类记录：

- 选择记录：已解析的命令 ID、子命令路径、候选类型和槽位、git 仓库根路径作用域、候选名称、选择次数和最后选择时间。
- 个人纠正：同一上下文和仓库作用域下用户实际提交的候选 token、胜/负次数、出现过的日期和最后时间。它用来在相同上下文下补回静态规则或 generator 没有提供的候选。

不记录完整命令行、环境变量或命令输出。个人纠正会保存参数 token，因此 token 本身可能包含路径或敏感值；仓库作用域会保存仓库根路径。数据只在本机、目录 0700、文件 0600，学习可以随时关闭、清除或导出。升级后旧的全局作用域记录会保留，但仓库内优先使用按仓库隔离的新记录。

## 保留与清除

- 默认保留最近 90 天；打开或记录时清理过期条目。
- `oc-manage learning off` 立即停止记录和排序；已有记录保留，直到清除。
- `oc-manage clear --yes` 删除全部选择记录和个人纠正。
- `oc-manage learning export --file PATH` 导出；`import --file PATH` 导入前会备份当前文件。
- `oc-manage backup export --file PATH` 导出完整 zip（学习数据、个人纠正、`config.json` 设置，可能包含 AI Key）；`backup import` 会验证哈希并在失败时尽量恢复旧文件。
- 删除 `learning.json` 等价于清空学习数据；基础规则不受影响。

## 日志与 IPC

- 编辑缓冲区只在内存、fd 3 管道和本机私有 Unix socket 中传递，不落盘。
- 默认日志只记录事件、命令/子命令上下文、候选数量、长度、路径、耗时和错误；不记录输入行文本、参数值、路径参数或候选名。日志默认保留 3 个自然日，可在设置页修改目录和天数或关闭。
- `OFFLINE_COMPLETE_LOG_TEXT=1` 会显式开启输入行、前缀和候选名日志，仅用于本地调试；关闭后不再写入。日志不会上传。
- 候选窗口只收到候选名称和说明，不收到完整输入。
- 代理不自动执行正在编辑的命令；只有用户明确选择时才写入替换文本。
- 应用不发送遥测。

## 更新检查

默认开启的更新检查是应用主动发起的唯一非 AI 网络请求：

- 请求 `https://github.com/Lewinz/offline-complete/releases/latest`（302 重定向到最新 tag），再按该 tag 构造并下载固定名称的 release 资源；不使用 `api.github.com`，避免未认证 API 的每小时额度。
- 发现新版本后，从该 release 的 GitHub 下载地址获取 `Offline-Complete.dmg` 和 `Offline-Complete.dmg.sha256`。
- 使用临时、无 Cookie、无缓存的 URLSession；不发送账号、学习数据或命令行内容。
- 下载后校验 SHA-256、Bundle ID、版本号，执行 `codesign --verify --deep --strict`，并固定当前应用的签名证书指纹；任何一项失败都不会进入安装流程。
- 更新包暂存在 `~/Library/Application Support/offline-complete/updates/`，安装后删除；安装日志也在该目录。
- 可在「设置 → 更新」关闭自动检查，或执行 `oc-manage update auto off`。关闭后仍可手动点「检查更新」。
- 更新信任锚点是 GitHub Release 和项目签名证书；在未接入 Apple 公证前，不应对被入侵的 GitHub 账号提供更强保证。

## 已知边界

- 同一用户的恶意进程不在本设计防御范围内；文件权限和同 UID 校验不能替代进程级隔离。
- 内存中的编辑缓冲区和候选不会被安全擦除。
- 学习数据目前使用版本化 JSON 而不是设计中的 SQLite，详见 [已知限制](LIMITATIONS.md)。

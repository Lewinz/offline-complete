# 已知限制

## 发布状态

- 当前是开发版：ad-hoc 签名，未用 Developer ID，未公证。
- 未在干净虚拟机/测试账户中完成系统级断网审计；`scripts/offline-audit.sh` 只是本地 sandbox 冒烟检查。
- 仅支持 Apple Silicon（arm64）；Intel 构建已移除。
- 没有自动更新；升级需要手动替换应用包。
- 项目自身的 LICENSE 尚未确定；发布前必须选择许可证并加入应用包。

## 终端与 shell

- 只支持 zsh；未实现 bash/fish。
- 代理通过临时 `ZDOTDIR` 的 `.zshrc` 先 source 用户 `.zshrc`，再加载钩子；`.zshenv`、`.zprofile`、`.zlogin` 不会加载，用户若把 PATH 放在这些文件里会缺失。
- 插入假定 emacs 键位（Ctrl-E / Ctrl-U）；vi 模式未验证。
- `Esc` 只处理单字节；如果转义序列被拆到多次读取，可能被当作关闭或原样转发。
- 快捷键不可配置。

## 窗口与定位

- 光标定位依赖 Accessibility；未授权或终端不暴露文本范围时回退主屏右上角。
- 权限按二进制/签名识别，重新构建后可能需要在系统设置中重新授权。
- 多显示器、屏幕缩放、iTerm2/macOS Terminal 的焦点切换尚未逐项验证。
- 窗口只显示前 10 个候选，没有滚动条。
- 管理窗口提供规则树、学习记录开关/清除、日志查看和日志设置，但规则只读；用户覆盖、建议确认/撤销和快捷键设置还没有图形界面。
- 日志按自然日分文件并保留 3 天；单文件在下次打开时按 2 MB 轮转。关闭日志时，运行中的 agent 在下一次日志事件才停止写入。

## 规则与学习

- `rules/base.json` 是从 Kiro/Fig 公开 spec CDN 导出的结构（707 个顶层命令、约 112,814 个静态节点）；已接入 positional/option 参数、脚本 generator、文件/目录 template、JavaScript custom/postProcess、缓存/Fuzzy 过滤和 CWD 传递。安装包的静态规则与 spec 模块使用 AES-GCM 加密，但密钥在应用内。跨 spec 的 `loadSpec`/`generateSpec` 已接入运行时索引；shell 历史提供本地 token 联想，不等同 Kiro 的完整历史 UX；`--name=value`、insertValue 和多参数 option 已支持；OC 的可选 AI generator 在应用“AI 补全”页面开启；只支持 OpenAI Chat Completions 和 Anthropic Messages，不调用 Kiro 的账号或服务。
- 学习存储是版本化 JSON，不是设计中的 SQLite；学习页可查看记录、开关学习和清除，但没有用户覆盖层或待确认建议的图形界面。
- 学习只记录显式选择；缺失选项发现（T19）只有阈值纯函数，没有采集和确认流程。
- 解析器对别名、函数、选项值语义和复杂 shell 语法保守返回“不确定”；此时不补全。

## 共存与安装

- 安装器/DMG 首次启动通过 `~/.zshrc` 的带标记块自动启动 zsh 补全会话；自动启动依赖主应用的运行标记，关闭主窗口时会留在后台并继续工作，真正退出应用后停止。显式 `oc` 仍可启动一次补全会话。不自动停用 Kiro，两者可能争用同一终端的补全。`oc-manage shell uninstall` 可移除。
- DMG 里的应用只有在拖到 `/Applications` 或 `~/Applications` 并启动后才会自动安装命令行/zsh 集成；直接从只读 DMG 运行不会修改系统。
- 自动启动只覆盖交互式、非 root、非 SSH 的 zsh；嵌套 zsh 继承 `OFFLINE_COMPLETE_ACTIVE`，不会再次接管。
- `oc` 的临时 `ZDOTDIR` 只显式 source 用户 `.zshrc`，不加载 `.zshenv`/`.zprofile`/`.zlogin`；自动启动时外层 shell 已加载的 `.zprofile` 环境会继承。
- 没有安装包签名、公证、自动更新、回滚安装器。
- 学习数据有图形清空入口；日志清空在“日志”页；`oc-manage` 仍可用于脚本化操作。

## 安全边界

- 同 UID 的恶意进程不在防御范围内。
- PTY 关闭、进程组 ID 复用、主动脱组后代、真实内核阻塞等已知竞态按“记录为限制”处理，不再作为当前阻塞。

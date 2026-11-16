# 规则包

## 随包规则

`rules/base.json` 现在由 Kiro CLI 的公开 autocomplete spec CDN 导出，不再是手写小包：

- 索引：`https://specs.q.us-east-1.amazonaws.com/index.json`
- 已获取 1479/1484 个 spec；筛掉带 `/`、`@` 的非顶层命令后导入 707 个 shell 命令
- 约 112,814 个静态节点（命令、子命令、选项、说明、参数）
- 导入 `name`、`description`、`subcommands`、`options`、`args`
- 原始 spec 模块随包加密；Node generator runtime 执行 `script`、`custom`、`postProcess`、template、cache、trigger 和 fuzzy 过滤
- 跨 spec 的 `loadSpec`/`generateSpec`、shell history、insertValue 和多参数 option 已接入；OC 的可选 AI generator 在应用“AI 补全”页面启用，仅支持 OpenAI 和 Anthropic；导入时 AI custom generator 的 `fig` 调用已改写为 `oc setting`/`oc ai request`
- 静态包没有收录的命令（例如 autojump 的 `j` 别名）在运行时通过加密 spec 索引按需加载，不要求出现在 707 条静态命令表中
- 来源和许可说明见 `rules/KIRO-SPECS.md` 与 `rules/KIRO-SPECS-LICENSE.MIT`

安装后位于：

```text
/Applications/Offline Complete.app/Contents/Resources/rules.bin
/Applications/Offline Complete.app/Contents/Resources/specs/
```

`.bin` 是 AES-GCM 加密的规则/spec 资源。密钥嵌在应用里，因此它主要防止误改和随手替换，不是对有能力分析二进制的攻击者的强保护。

校验哈希：

```bash
shasum -a 256 rules/base.json
```

当前包的 SHA-256 记录在 `oc-manage status` 的 `rules sha256` 行。

## 自定义规则

```bash
OFFLINE_COMPLETE_RULES=/path/to/rules.json oc zsh -i
```

规则包上限 16 MiB；未知字段、未知 schema 版本、非法 UTF-8、重复或空白名称、控制字符会被拒绝。加载失败时不会回退到网络，也不会执行 JS。

## 无规则命令的 help 自省

静态包和 Kiro spec 都没有收录的本地命令（例如自定义的 `dotfiles`）在输入到参数位置后，会尝试运行一次命令自己的帮助：

```text
<command> --help
```

如果 `--help` 没有可解析内容且没有超时，再尝试 `<command> help`。只运行这两个帮助入口，不会执行正常子命令。帮助输出会被解析成子命令、选项和 `[a|b]` 形式的取值，并缓存在 `~/.oc/help/`；缓存以可执行文件的路径、大小、修改时间和 inode 为版本键，默认 24 小时过期，可执行文件一旦变化会立即失效。

行为限制：

- 在子命令/选项尚未缓存时，第一次输入会短暂没有候选；解析完成后候选会自动补发。
- 候选窗口底部的「刷新」入口只对 help 自省命令显示，会清除当前命令的缓存并重新解析；静态规则命令不显示该入口。
- 只识别常见帮助布局（Cobra、clap、urfave/cli、Go 命令表以及缩进命令表）；没有帮助输出的命令不会有候选。
- 不解析 shell alias、function 或 `sudo` 等包装命令的语义，可能为同名外部程序补全。
- 帮助进程默认 750ms 超时（可用 `OFFLINE_COMPLETE_HELP_TIMEOUT_MS` 设为 100–5000 毫秒）和 64 KiB 输出上限；超时后 5 秒、解析失败后 30 秒内不会重试。发现过程在后台进行，不阻塞终端输入。
- 帮助进程继承当前 shell 环境，以当前用户身份运行；offline-complete 不会为它发起网络请求，但帮助程序自身的行为不受控制。只应把它用于你本来就会执行的命令。

禁用：

```bash
OFFLINE_COMPLETE_NO_HELP_FALLBACK=1 oc zsh -i
```

需要立即刷新某个命令的缓存时，删除 `~/.oc/help/` 下对应文件或整个目录。

## 加载器

`internal/rules.Loader` 支持可信清单 + 本地 `fs.FS` 的按命令加载、SHA-256 校验和大小检查。当前 agent 仍直接加载单个完整包；全量导入后单包约 8 MiB，仍在 16 MiB 上限内。

## 许可与覆盖

规则结构来自 Fig/AWS Q autocomplete spec 格式。上游 Fig 仓库采用 MIT 许可；从 Kiro CDN 获取的内容在公开发布前仍需核查 Kiro 的分发条款。脚本、option 参数、JavaScript custom/postProcess、文件 template 和 fuzzy 过滤已接入；跨 spec 的 `loadSpec`/`generateSpec` 使用加密 spec 索引按需加载；历史联想是本机 token 级实现；可选 AI generator 需要显式配置端点。

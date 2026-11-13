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

## 加载器

`internal/rules.Loader` 支持可信清单 + 本地 `fs.FS` 的按命令加载、SHA-256 校验和大小检查。当前 agent 仍直接加载单个完整包；全量导入后单包约 8 MiB，仍在 16 MiB 上限内。

## 许可与覆盖

规则结构来自 Fig/AWS Q autocomplete spec 格式。上游 Fig 仓库采用 MIT 许可；从 Kiro CDN 获取的内容在公开发布前仍需核查 Kiro 的分发条款。脚本、option 参数、JavaScript custom/postProcess、文件 template 和 fuzzy 过滤已接入；跨 spec 的 `loadSpec`/`generateSpec` 使用加密 spec 索引按需加载；历史联想是本机 token 级实现；可选 AI generator 需要显式配置端点。

# 日志

## 默认位置

```text
~/.oc/
├── config.json                      # 日志设置（0600）
└── logs/
    ├── agent-2026-11-12.log         # 当天（0600）
    ├── agent-2026-11-11.log
    └── agent-2026-11-10.log
```

默认启用日志，目录为 `~/.oc/logs`，保留**今天和前两天**（3 个自然日）。日志目录 0700，文件 0600；单个文件超过 2 MB 时在下次打开时轮转为 `.log.1`。启动 agent、跨天或重读设置时会清理超过保留期的 `agent-*` 日志，不会删除目录中的其他文件。

旧版本的 `~/Library/Logs/OfflineComplete/agent.log` 不会自动迁移。

## 设置

管理窗口的“设置”页可以修改：

- 是否启用 agent 日志；
- 日志存储目录（可选择任意绝对路径）；
- 保留天数（1–365，默认 3）。

也可以使用 CLI：

```bash
oc-manage logging status
oc-manage logging status --json
oc-manage logging on
oc-manage logging off
oc-manage logging set --enabled on --directory "$HOME/.oc/logs" --retention-days 3
```

配置文件是 `~/.oc/config.json`：

```json
{
  "schemaVersion": 1,
  "logging": {
    "enabled": true,
    "directory": "/Users/you/.oc/logs",
    "retentionDays": 3
  }
}
```

运行中的 agent 在下一次日志事件时重新读取配置：关闭后停止写入，重新启用或改变目录后写入新位置。正在进行的 shell 不需要重启。

环境变量优先于配置文件：

```bash
OFFLINE_COMPLETE_CONFIG=/path/config.json
OFFLINE_COMPLETE_LOG=/path/agent.log          # 指定单个文件
OFFLINE_COMPLETE_LOG_DIR=/path/logs           # 指定目录，仍按日期分文件
OFFLINE_COMPLETE_LOG_ENABLED=0                # 0/1 覆盖开关
OFFLINE_COMPLETE_LOG_RETENTION_DAYS=7
```

从 Finder/Dock 启动管理窗口时不会继承终端环境；需要这些覆盖时，从终端运行 `oc-app`。

## 记录内容

默认记录：

- `agent_start`：规则路径、命令数、SHA-256、PID；
- `learning_enabled` / `learning_disabled`：数据路径和选择数；
- `socket_ready`、`session_start`：会话 ID、socket 路径、shell 名称；
- `offer`：命令、子命令、候选类型、候选数量、前缀长度、行长度、耗时；
- `select_applied` / `select_rejected`：上下文、候选长度或候选名（仅在文本模式下）、错误；
- `interface_connected` / `interface_closed`、`window_start` / `window_error`、`agent_stop`。

默认**不写**输入行、参数值、路径参数或候选名。日志不会上传，也没有遥测。

## 查看与清除

管理窗口“日志”页可以切换保留期内的日志文件、自动刷新、在 Finder 中显示和清空当前文件。也可以：

```bash
tail -f "$HOME/.oc/logs/agent-$(date +%F).log"
```

清空会截断当前文件，运行中的 agent 会继续追加新行。

## 本地文本调试

需要看到实际输入和候选名时，显式开启：

```bash
OFFLINE_COMPLETE_LOG_TEXT=1 oc
```

此时 `offer_text` 会记录当前输入行和前缀，`select_applied` / `learning_recorded` 会记录候选名。这些内容可能包含敏感信息；只在本机调试时使用，调试后关闭并清空日志。

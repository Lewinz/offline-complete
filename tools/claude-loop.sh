#!/usr/bin/env bash
# 无人值守的 Claude Code 实现循环。
#
# 每次迭代启动一个全新的 Claude Code 会话，读取 .claude-loop/PROGRESS.md 与计划文件，
# 继续实现下一项可自动推进的任务。遇到真正需要人类决定的问题时，代理写入
# .claude-loop/BLOCKED.md，循环随即停止并打印问题。
#
# 用法：
#   tools/claude-loop.sh
#
# 环境变量：
#   MAX_ITERATIONS       最多迭代次数（默认 30）
#   MAX_FAILURES         连续失败多少次后停止（默认 2）
#   MAX_STALLS           连续多少轮没有文件改动后停止（默认 3）
#   PER_ITER_BUDGET      单轮 Claude 预算上限，美元（默认 8）
#   TOTAL_BUDGET         累计预算上限，美元（默认 60）
#   CLAUDE_LOOP_MODEL    传给 claude --model 的模型；留空使用默认模型
#   CLAUDE_LOOP_STATE    状态目录（默认 <repo>/.claude-loop）
#
# 退出码：0 已完成；2 代理请求人工决策；3 预算用尽；4 claude 连续失败；
#         5 连续无改动；130 被中断。

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT" || exit 1

STATE_DIR="${CLAUDE_LOOP_STATE:-$ROOT/.claude-loop}"
LOG_DIR="$STATE_DIR/logs"
BLOCKED="$STATE_DIR/BLOCKED.md"
DONE="$STATE_DIR/DONE.md"
PROGRESS="$STATE_DIR/PROGRESS.md"
DECISIONS="$STATE_DIR/DECISIONS.md"
STATE_JSON="$STATE_DIR/state.json"

MAX_ITERATIONS="${MAX_ITERATIONS:-30}"
MAX_FAILURES="${MAX_FAILURES:-2}"
MAX_STALLS="${MAX_STALLS:-3}"
PER_ITER_BUDGET="${PER_ITER_BUDGET:-8}"
TOTAL_BUDGET="${TOTAL_BUDGET:-60}"

SYSTEM_PROMPT_FILE="$ROOT/tools/agent-system-prompt.md"
ITERATION_PROMPT_FILE="$ROOT/tools/agent-iteration-prompt.md"

for file in "$SYSTEM_PROMPT_FILE" "$ITERATION_PROMPT_FILE"; do
  if [ ! -f "$file" ]; then
    echo "[claude-loop] 缺少提示文件：$file" >&2
    exit 1
  fi
done
if ! command -v claude >/dev/null 2>&1; then
  echo "[claude-loop] 找不到 claude 命令" >&2
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "[claude-loop] 需要 jq 解析 claude 输出" >&2
  exit 1
fi
# The desktop app does not load interactive shell profiles. Honor the user's
# `g` Go version manager setup, then fall back to a cached toolchain.
if ! command -v go >/dev/null 2>&1 && [ -s "$HOME/.g/env" ]; then
  . "$HOME/.g/env"
fi
if ! command -v go >/dev/null 2>&1 && [ -x "$HOME/.g/go/bin/go" ]; then
  export PATH="$HOME/.g/go/bin:$PATH"
fi
if ! command -v go >/dev/null 2>&1; then
  go_candidate="$(ls -d "$HOME"/.go/pkg/mod/golang.org/toolchain@*/bin/go 2>/dev/null | sort | tail -n 1)"
  if [ -n "$go_candidate" ]; then
    export PATH="$(dirname "$go_candidate"):$PATH"
  else
    echo "[claude-loop] 找不到 go；请安装 Go 或把工具链加入 PATH" >&2
    exit 1
  fi
fi

mkdir -p "$LOG_DIR"

if [ ! -f "$PROGRESS" ]; then
  cat > "$PROGRESS" <<'EOF'
# 无人值守实现进度

## 起始状态（2026-11-10）

- `go test ./...` 基线：rules、input、session、ipc、inventory、cmd 全部通过；
  `GOFLAGS=-mod=vendor GOPROXY=off go test ./...` 通过。
- 已实现：静态规则（`internal/rules`）、输入解析（`internal/input`）、PTY 会话（`internal/session`）、
  命令行代理（`cmd/offline-complete`）、握手分帧（`internal/ipc`）、规则盘点（`cmd/inventory-rules`）。
- 计划：`docs/superpowers/plans/2026-11-10-offline-completion.md`（T01–T24）、
  `docs/superpowers/plans/2026-11-10-pty-session.md`（T05–T08）、
  `docs/superpowers/plans/2026-11-10-local-ipc.md`（T08）。

## 下一轮起点

从 T06 剩余项开始：优雅终止、后代脱离进程组与失败路径；随后 T07 的转发与终端恢复、T08 的 socket。
以计划与代码实际状态为准。
EOF
fi

if [ ! -f "$DECISIONS" ]; then
  cat > "$DECISIONS" <<'EOF'
# 自主决策记录

格式：日期 | 选择 | 理由 | 如何撤销
EOF
fi

snapshot() {
  (
    cd "$ROOT" &&
      find . \( -name .git -o -name target -o -name .claude-loop \) -prune -o -type f -print0 |
      LC_ALL=C sort -z |
      xargs -0 shasum 2>/dev/null
  ) | shasum | cut -d' ' -f1
}

write_state() {
  jq -n \
    --arg status "$1" \
    --argjson iteration "${2:-0}" \
    --argjson total_cost "${3:-0}" \
    --arg updated_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{status: $status, iteration: $iteration, totalCostUsd: $total_cost, updatedAt: $updated_at}' \
    > "$STATE_JSON"
}

run_claude() {
  if [ -n "${CLAUDE_LOOP_MODEL:-}" ]; then
    claude "$@" --model "$CLAUDE_LOOP_MODEL"
  else
    claude "$@"
  fi
}

total_cost=0
failures=0
stalls=0
iteration=0
prev_snapshot="$(snapshot)"

trap 'write_state interrupted "$iteration" "$total_cost"; echo "[claude-loop] 已中断"; exit 130' INT TERM

echo "[claude-loop] 仓库：$ROOT"
echo "[claude-loop] 模型：${CLAUDE_LOOP_MODEL:-默认} | 单轮预算 \$$PER_ITER_BUDGET | 总预算 \$$TOTAL_BUDGET | 最多 $MAX_ITERATIONS 轮"

for ((iteration = 1; iteration <= MAX_ITERATIONS; iteration++)); do
  if [ -f "$BLOCKED" ]; then
    write_state blocked "$iteration" "$total_cost"
    echo
    echo "[claude-loop] 代理请求人工决策，循环停止："
    cat "$BLOCKED"
    exit 2
  fi
  if [ -f "$DONE" ]; then
    write_state done "$iteration" "$total_cost"
    echo
    echo "[claude-loop] 代理报告可自动推进的任务已全部完成："
    cat "$DONE"
    exit 0
  fi
  if awk -v spent="$total_cost" -v limit="$TOTAL_BUDGET" 'BEGIN { exit !(spent >= limit) }'; then
    write_state budget_exhausted "$((iteration - 1))" "$total_cost"
    echo "[claude-loop] 累计花费 \$$total_cost 已达到总预算 \$${TOTAL_BUDGET}，停止。"
    exit 3
  fi

  echo
  echo "[claude-loop] 第 $iteration/$MAX_ITERATIONS 轮开始（累计 \$${total_cost}）"

  out="$LOG_DIR/iter-$iteration.json"
  err="$LOG_DIR/iter-$iteration.err"
  : > "$out"

  run_claude -p "$(cat "$ITERATION_PROMPT_FILE")" \
    --permission-mode bypassPermissions \
    --append-system-prompt "$(cat "$SYSTEM_PROMPT_FILE")" \
    --max-budget-usd "$PER_ITER_BUDGET" \
    --output-format json \
    >"$out" 2>"$err"
  rc=$?

  if [ -s "$out" ]; then
    iter_cost="$(jq -r '(.total_cost_usd // 0) | tostring' "$out" 2>/dev/null || echo 0)"
    case "$iter_cost" in ''|*[!0-9.]*) iter_cost=0 ;; esac
    total_cost="$(awk -v a="$total_cost" -v b="$iter_cost" 'BEGIN { printf "%.6f", a + b }')"
  else
    iter_cost=0
  fi

  is_error="$(jq -r '.is_error // false' "$out" 2>/dev/null || echo true)"
  num_turns="$(jq -r '.num_turns // 0' "$out" 2>/dev/null || echo 0)"
  result="$(jq -r '.result // ""' "$out" 2>/dev/null || echo "")"

  echo "[claude-loop] 本轮结束：exit=$rc turns=$num_turns cost=\$$iter_cost 累计=\$$total_cost"
  if [ -n "$result" ]; then
    printf '%s\n' "$result" | tail -n 25
  fi

  if [ "$rc" -ne 0 ] || [ "$is_error" = "true" ]; then
    failures=$((failures + 1))
    echo "[claude-loop] 本轮失败（连续 $failures 次）；stderr 末尾：" >&2
    tail -n 15 "$err" >&2
    if [ "$failures" -ge "$MAX_FAILURES" ]; then
      write_state failed "$iteration" "$total_cost"
      echo "[claude-loop] 连续失败达到上限，停止。日志：$out"
      exit 4
    fi
  else
    failures=0
  fi

  current_snapshot="$(snapshot)"
  if [ "$current_snapshot" = "$prev_snapshot" ]; then
    stalls=$((stalls + 1))
    echo "[claude-loop] 本轮没有文件改动（连续 $stalls 轮）"
    if [ "$stalls" -ge "$MAX_STALLS" ]; then
      write_state stalled "$iteration" "$total_cost"
      echo "[claude-loop] 连续 $stalls 轮无改动，停止。请查看 $PROGRESS 与 $out"
      exit 5
    fi
  else
    stalls=0
    prev_snapshot="$current_snapshot"
  fi

  write_state running "$iteration" "$total_cost"
done

write_state iteration_limit "$iteration" "$total_cost"
echo "[claude-loop] 达到最大迭代次数 ${MAX_ITERATIONS}，停止。进度：$PROGRESS"
exit 0

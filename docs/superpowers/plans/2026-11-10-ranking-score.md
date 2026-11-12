# 排序分数基础实施计划

> 使用 executing-plans 和 test-driven-development；本计划只实现 T18 独立纯计算基础，不代表学习数据、上下文隔离或生产接入完成。

**目标：** 对已经匹配、已通过规则约束的候选计算可复现的学习加权分数。
**架构：** 纯函数接收基础分数、明确选择次数及调用方计算的近期分值，不接触候选文本、上下文、磁盘或时钟；调用方负责失效、隐私和失败回退。
**技术：** Go 标准库，无新增依赖。
**设计依据：** `docs/design.md` 7.1、8 节，总计划 T18。

## 全局约束

不得访问网络、持久化输入或将本函数视为选择事件认证；生产集成须等 T17、T18 前提完成。

## 验证重点

- 未选择时近期分数非零：拒绝而不是伪造学习信号。
- NaN、无穷大、负基础分数以外的非法输入不得产生不稳定排序：拒绝无效数值；有限负基础分数可用。
- 近期分数超出 [0,1]：拒绝而不是静默钳制。
- 超大次数：频率加成按 3 截断且不会整数溢出。
- 调用方传入最大有限浮点基础分：保持有限；若计算结果非有限则拒绝。

## 任务：纯分数计算

**文件：** 新增 `internal/learning/score.go` 与 `internal/learning/score_test.go`。
**接口：** `Score(base float64, selectedCount uint64, recency float64) (float64, error)`；`base + 0.15 * min(log(1 + selectedCount), 3) + 0.10 * recency`，`selectedCount` 为零时 `recency` 必须为零。函数不排序、不存储、不识别用户选择。

- [x] 测试基础分、选中一次、频率上限、合法负基础分及非法数值边界。
- [x] 运行 `go test ./internal/learning -count=1`，确认因接口缺失失败。
- [x] 实现 `Score`；拒绝非有限值、无效近期分值和计算结果溢出。
- [x] 运行定向、全量、离线和 race 测试，均通过。该子任务完成不勾选总计划 T18。

## 任务：近期分数的保守衰减

**文件：** 新增 `internal/learning/recency.go` 与 `internal/learning/recency_test.go`。
**接口：** `Recency(lastSelection, now time.Time) (float64, error)`；显式传入时间，不读取系统时钟。最近一次选择在当前时刻为 1，距今 90 天线性衰减至 0，之后保持 0；零值时间或未来选择拒绝。这个函数不实施记录保留或计数过期。

- [x] 测试当前、45 天、90 天及更早时的分值，以及零时间和未来选择的错误。
- [x] 运行 `go test ./internal/learning -run TestRecency -count=1`，确认因接口缺失失败。
- [x] 实现 `Recency`，使用经过范围检查的时间差计算比例。
- [x] 定向 `go test ./internal/learning -count=1`、`go test ./...`、离线 `GOFLAGS=-mod=vendor GOPROXY=off go test ./...` 和 `go test -race ./internal/learning -count=1` 均通过；总计划 T18 保持未勾选。

## 任务：稳定排序及失败回退

**接口：** `Rank` 对调用方已过滤、按基础顺序排列的分数输入返回索引排列；使用 `Score` 计算后按分数降序稳定排序。任一输入无效时返回原基础顺序和错误，不能返回部分学习排序；不修改输入，不处理文本、存储或授权。

- [x] 测试权重改变次序、同分保持原次序、无效输入整体回退和不修改输入。
- [x] 测试先因 `RankInput`、`Rank` 缺失编译失败；实现后 `go test ./internal/learning -count=1`、`go test ./...`、`GOFLAGS=-mod=vendor GOPROXY=off go test ./...` 和 `go test -race ./internal/learning -count=1` 均通过。总计划 T18 保持未勾选。

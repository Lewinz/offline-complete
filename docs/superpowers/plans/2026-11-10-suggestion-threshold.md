# 缺失选项建议阈值基础实施计划

> 使用 executing-plans 和 test-driven-development；仅实现 T19 独立阈值计算，不实现采集或生产建议。

**目标：** 根据已通过验证的单个候选观察判断是否达到建议阈值。
**架构：** 纯函数只接收日历日和成功标志，去重日期并检查三日及一次成功，不接触命令、选项、输入、磁盘或时钟；调用方负责可靠关联、去重、隐私和数据保留。
**技术：** Go 标准库，不新增依赖。
**设计依据：** `docs/design.md` 7.2、8 节；总计划 T19。

## 约束及审查重点

- 不推断输入合法性或退出状态关联，不接入生产；不能将阈值满足当作建议已授权。
- 日期由调用方提供为 `YYYY-MM-DD`，必须为真实公历日期；任何无效日期使整体失败关闭，即使其他记录已达阈值。
- 同日重复记录只算一天；任意记录成功即可满足成功条件。空记录、两天、三天无成功、三天含成功、重复日期及非法日期均须测试。
- 函数不实现 90 天保留、同命令行去重、白名单、建议处理或持久化，T19 仍未完成。

## 任务：有界日期去重

- 文件：`internal/learning/suggestion.go` 和 `internal/learning/suggestion_test.go`；保持现有接口和对全部日期的验证。
- [x] 构造多个不同有效日期并测量阈值计算分配量：2000 个日期旧实现每次分配约 2029 次，测试先失败；现有测试亦检查达标后的非法日期。
- [x] 用最多三个不同日期记录满足阈值所需的信息，继续遍历所有观察；`go test ./internal/learning -count=1`、`go test ./...`、`GOFLAGS=-mod=vendor GOPROXY=off go test ./...`、`go test -race ./internal/learning -count=1` 均通过。

## 任务：纯阈值判定

**文件：** `internal/learning/suggestion.go` 和 `internal/learning/suggestion_test.go`。
**接口：** `Observation{Day string, Success bool}`；`SuggestionEligible([]Observation) (bool, error)`。

- [x] 编写独立表驱动测试，覆盖空观察、两个不同日期、多次同日、三日无成功、三日有成功、无效日期在达标记录前后、闰日及非规范格式。
- [x] 运行 `go test ./internal/learning -run TestSuggestionEligible -count=1`，确认因接口缺失失败。
- [x] 实现纯函数；遍历全部记录验证日期，按日去重，最后判断至少三日且至少一次成功，不修改输入。
- [x] 定向、`go test ./...`、`GOFLAGS=-mod=vendor GOPROXY=off go test ./...` 和 `go test -race ./internal/learning -count=1` 均通过；T19 总项不勾选。

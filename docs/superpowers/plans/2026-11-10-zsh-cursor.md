# zsh 光标偏移换算实施计划

> 使用 test-driven-development 和 executing-plans；本计划只处理 T09/T10 的独立基础，不代表采集、编辑或生产接入完成。

**目标：** 安全换算 zsh 多字节模式的字符索引与解析器的 UTF-8 字节索引。
**架构：** 纯 Go 函数只接受有效 UTF-8 与边界内偏移；字符单位为 Unicode 码点，不是屏幕列或字素簇。调用方必须确认 zsh 多字节模式，未经确认不能用该接口接入生产。
**技术：** Go 1.27.3 标准库；不新增依赖。
**设计依据：** `docs/design.md` 第 4、8、9 节及总计划 T09/T10。

## 约束与风险

不得记录输入或运行用户命令；拒绝无效编码及无效位置，不能近似换算。组合字符、非 ASCII 字符、空行、字节中间位置与超范围均需覆盖。真实交互 ZLE 的光标单位和运行 locale 需端到端验证；非多字节模式不得使用此映射。2026-11-10：macOS 伪终端、`en_US.UTF-8` 的一个真实 ZLE 移动光标场景已通过，但不代表所有 locale、组合字符移动和终端均已验证。

## 任务：偏移转换

- 文件：`internal/shellparse/cursor.go`、`internal/shellparse/cursor_test.go`。
- 输出：`ZshCursorToByte(line string, cursor int) (int, error)` 和 `ByteCursorToZsh(line string, cursor int) (int, error)`。
- [x] 编写已知字面量及非法输入测试；先运行并确认接口缺失导致失败。
- [x] 实现双向转换并拒绝无效 UTF-8、负数、超界及字节中间位置。
- [x] 运行 `go test ./internal/shellparse -count=1`、`go test -race ./internal/shellparse -count=1`、`go test ./...`、`GOFLAGS=-mod=vendor GOPROXY=off go test ./...`，均通过；不将 T09/T10 整项勾选。实际 zsh 多字节字符串长度测试是实现后补充覆盖，不是 ZLE 光标验证。
- 2026-11-10：扩展 macOS 伪终端真实 ZLE 测试，覆盖行首、行尾、组合字符中间位置与 `en_US.UTF-8` / `ja_JP.UTF-8`，并将观察到的位置与双向字节换算核对；重复 3 次、解析器及全量、离线和 race 测试通过。组合字符位置最初测试预期错误，实测四次向右为码点位置 4，修正测试；这属于对已有行为的验证，不是功能 RED→GREEN。测试不代表所有 locale、终端或生产接入已验证。

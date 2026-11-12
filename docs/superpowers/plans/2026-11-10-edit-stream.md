# 编辑状态消息流实施计划

> 使用 executing-plans 和 test-driven-development。仅完成独立消息流基础；不代表 T09 的真实 zsh 接入或生产安全认证完成。

**目标：** 消费调用方已认证的连接上的版本化编辑消息，维护有界内存快照，连接结束时失效。
**架构：** `internal/editstate` 使用四字节大端长度和严格 JSON；消息携带会话 ID、协议版本、递增状态版本及 update/clear 类型，更新光标为 UTF-8 字节偏移。任何畸形消息、过期版本、会话不匹配或读取错误立即结束消费并关闭状态；调用方负责认证连接与读取时限。
**技术：** Go 1.27.3 标准库，复用 `internal/ipc.MaxFrame` 和严格 JSON 代理项验证；无新增依赖。
**设计依据：** `docs/design.md` 第 4、8、9 节；总计划 T09、本地 IPC 子计划。

## 全局约束

- 不记录、持久化或执行编辑文本；仅内存保存，不承诺安全擦除。
- 会话 ID 非身份凭据；必须在认证连接上使用，不得接入未经验证的生产路径。
- 不能因为状态流失败阻塞 PTY；调用方负责连接关闭和正时限。
- 未覆盖真实 zsh、窗口焦点或退出状态，不视为 T09 完成。

## 审查重点

- 帧过长、截断、非法 UTF-8、无效 JSON、重复或未知字段必须失败关闭。
- 跨会话、旧版本和错误字节光标不可恢复旧快照。
- 断连、无效消息和正常读取错误均不可保留快照。
- 短读和连续帧必须正确处理；执行、取消或清除不注入 shell 输入。
- 大字符串/编码膨胀不得导致无界分配；消息不含退出状态或身份认证。

## 任务 1：编辑状态流

**文件：** 新建 `internal/editstate/stream.go`、`internal/editstate/stream_test.go`；修改本计划和总计划以记录验证。
**接口：** `ConsumeStream(reader io.Reader, session string, state *State) error`；帧采用 `version`、`session_id`、`type`、`sequence`，update 另外包含 `line`、`cursor`，clear 不包含它们。版本为 1，sequence 非零递增。消费期间允许观察快照，任何返回前调用 `Close`。

- [x] 先写管道连续 update/clear、错误会话/旧序列、错误字段和断连失效测试；运行 `go test ./internal/editstate -run TestConsumeStream -count=1` 确认接口缺失。
- [x] 实现严格、有界的消息消费和生命周期，不写日志；不接生产。
- [x] 增加短读、损坏/超限帧、UTF-8、字段重复/null/缺失、光标测试；`go test ./internal/editstate -count=1 -timeout=30s`、`go test ./...`、离线测试及 race 测试通过；并发状态由已有测试覆盖。
- [x] 在总计划、决策和进度中记录完成范围、结果、限制及下一步；T09 总项保持未完成。
- 2026-11-11：仅能装入 clear 但连空 update 也无法装入的会话 ID，此前可创建接收器且流消费者会先读取数据；两项回归分别先失败，现两处均提前拒绝。定向重复、全量、vendor 离线及 editstate/session race 测试通过。该检查不保证较长序列或非空行能装入；不改变 T09 完成状态。
- 2026-11-10：接收前验证会话 ID 能以现有发送编码生成至少一帧 clear，编码膨胀的 ID 原先消费数据后才出错；回归测试先失败后通过，定向、全量、离线及 race 测试通过。该流仍未接入生产。

## 任务 5：认证连接与编辑流组合

**接口：** Darwin 上 `ConsumeAuthenticatedConn` 接管已接受的 Unix 连接及其专属新状态；先进行双向 UID/会话握手，再按帧时限消费消息，返回时关闭连接和状态。重连使用新状态，状态发布和多连接协调由调用方负责；不接生产监听服务或 shell。

- [x] 先测试真实 Unix 连接上的认证更新、断连失效和错误会话拒绝，接口缺失时编译失败；实现后通过。
- [x] 补充停滞握手和半帧超时的真实连接覆盖；定向重复、全量、vendor 离线、editstate race 及 Darwin 双架构离线构建通过。跨用户和 Intel 真机未验证，不代表 T09 完成。
- 2026-11-11：仅能编码 clear、不能编码最短 update 的会话原在认证组合入口等待握手；真实 Unix 连接回归先超时失败，现握手前复用两种消息编码校验，拒绝并关闭连接和状态。定向五次、全量、vendor 离线与 editstate race 通过；不代表生产接入或真实 zsh 采集完成。

## 任务 6：已认证连接的单活动状态接收器

**接口：** `NewReceiver(session, timeout)` 和 `Receiver.Consume(ctx, conn)` 接收调用方已认证并完成会话握手的连接，同一时刻仅一个活动连接，断开后才能重连；`Snapshot` 仅读取当前活动状态。取消时关闭连接并丢弃状态，不处理监听、认证、shell 或生产接入。

- [x] 先写并发连接拒绝、断线失效、重连与取消测试；接口缺失时编译失败，随后实现并通过。构造器非法会话测试先复现接受无效 ID，再复用现有帧校验拒绝。
- [x] 真实 Unix socket 与 `ServeAuthenticated` 组合覆盖握手、更新、错误会话、断连及重连；该测试首次运行通过，为实现后的组合覆盖，不是 RED→GREEN。定向重复、全量、vendor 离线与 race 测试，以及 Darwin 双架构离线交叉构建通过。调用方仍负责认证，拒绝并发连接不能提供同 UID 进程身份隔离；未接生产。
- 2026-11-11：接收器接管连接但原先忽略连接 Close 失败；真实管道与注入关闭错误的测试先失败，现将该错误与读取错误合并返回，仍只关闭一次并在关闭返回后释放槽位。接收器定向重复、全量、vendor 离线和 race 测试通过。关闭报错不证明资源已释放，接收器尚未接生产。
- 2026-11-11：取消回调中的第一次 Close 已中断读取但未返回时，消费清理中的第二次 Close 可提前返回并释放槽位；受控管道连接测试先复现，现两处共享一次 Close，完成后才释放槽位，验证后可重连。Receiver 重复测试、全量、vendor 离线及 race 测试通过；Close 阻塞仍会推迟返回，未接生产。
- 2026-11-11：断连后旧连接 Close 尚未返回时原接收器提前释放活动槽位，阻塞关闭回归测试先复现新连接被接受；现先失效快照、关闭旧连接，再释放槽位。Receiver 定向重复、全量、vendor 离线与 race 测试通过；阻塞 Close 会延迟重连，不代表生产接入完成。
- 2026-11-11：`Snapshot` 检查活动 context，取消已生效而回调尚未运行时隐藏旧快照；受控延迟回调的测试先失败，随后定向重复、全量、vendor 离线和 race 测试通过。并发读取仍可能先于取消，状态释放与连接关闭仍由异步回调执行。
- 2026-11-11：取消回调中先关闭状态，再关闭连接；受控阻塞关闭的管道连接测试先复现关闭阻塞期间仍可读取旧快照，修复后定向重复、全量、vendor 离线与 race 测试通过。取消回调调度前仍可能观察到快照，阻塞的连接关闭仍可能使消费者无法及时返回；不代表真实 zsh 或生产接入完成。

## 任务 7：同会话认证接收服务

**接口：** Darwin 上 `Receiver.ServeAuthenticated(ctx, listener, handshakeTimeout, limit) error` 使用接收器自身会话 ID 启动现有 IPC 认证服务；认证后交给该接收器处理，断线和取消清除快照。监听器须由调用方安全创建；服务不输出编辑内容或逐连接错误，不接生产入口。

- [x] 先测试真实 Unix socket 的更新、错误会话拒绝、取消失效和连接关闭；新接口缺失时编译失败。实现后定向重复、全量、vendor 离线和 editstate race 测试通过。此服务尚未接生产，不验证路径安全或跨用户隔离。

## 任务 8：有状态的编辑流发送端

- 2026-11-11：发送和关闭原共用锁，发送停滞时 Close 需等待发送期限；真实 `net.Pipe` 并同步写入开始的测试先以关闭超时失败。现分离发送串行和关闭锁，Close 主动关闭连接中断写入，失败继续合并关闭错误；Publisher 定向重复、全量、vendor 离线及 race 测试通过。依赖连接遵守并发关闭约定，Close 本身可能阻塞；尚未接生产或真实 zsh。

**范围：** T09 的独立发送基础，不接入生产或真实 zsh；依赖已认证和握手的连接。

**文件：** 新建 `internal/editstate/publisher.go`、`internal/editstate/publisher_test.go`。`NewPublisher(conn net.Conn, session string, timeout time.Duration) (*Publisher, error)` 校验参数并在成功时接管连接；`Update(line string, cursor int) error`、`Clear() error` 串行递增序列并设置每帧期限；`Close() error` 幂等关闭连接。任何编码、写入、期限或序列耗尽错误均关闭并永久禁用发送端，避免对端沿用过期状态；成功 Close 也不自动发送命令或编辑内容。构造失败不接管连接。会话需与认证握手一致，调用方负责认证，不保存编辑文本。

- [x] 先用真实 `net.Pipe` 和现有消费器测试更新、清除、并发序列及关闭失效；测试无效更新、停滞写入、序列耗尽后不得复用连接；运行定向测试确认缺失接口失败。
- [x] 实现后运行定向、全量、vendor 离线及 editstate race 测试。断连与关闭错误合并、写入停滞时并发关闭为实现后补充覆盖；不把该独立层作为 T09 完成证据。连接 Close 自身及不遵守 deadline 的连接可阻塞，不能保证对端即时失效。

## 任务 10：zsh 光标发布适配

- [x] 为已认证 Publisher 增加独立 `UpdateZsh`，要求调用方确认多字节模式并转换字符光标；模式不明、越界或无效 UTF-8 触发连接关闭与旧状态失效。真实 `net.Pipe` 与流消费者测试先因接口缺失编译失败；`go test ./internal/editstate -run '^TestPublisherZsh' -count=5 -timeout=30s`、全量、vendor 离线和 editstate race 测试通过。接口未接实际 ZLE 或生产，模式断言由调用方负责，不构成 T09 完成。

## 任务 11：编辑周期事件协调

- 2026-11-11：编辑更新失败使 Publisher 开始关闭，但底层连接 Close 停滞时仍占据周期入口；受控连接测试先以新周期阻塞失败。周期入口现监听 Publisher 的关闭通知，定向十次、全量、vendor 离线及 editstate race 测试通过。活动调用及底层 Close 仍可阻塞，未接真实 ZLE 或生产；不改变 T09 完成状态。

- 2026-11-11：本轮基线因 `cycle.go` 的 `acquire` 函数缺少结束括号而编译失败；补齐括号后，工作树中已有的周期关闭和发送排队测试、全量、vendor 离线及 editstate race 测试通过。此轮开始时周期及发送端已存在通道式关闭改动与两项测试，未观察到这些测试在旧实现下失败，不能将其视为本轮 RED→GREEN；关闭仍依赖底层连接，真实 ZLE 与生产集成未完成。

- 2026-11-11：增加周期协调器主动 Close，先中断停滞连接发送再结束周期；真实 `net.Pipe` 停滞发送回归先因接口缺失编译失败。快照失效及迟到事件为实现后补充覆盖。周期定向五次、全量、vendor 离线和 editstate race 测试通过；连接 Close 可能阻塞，未接真实 ZLE 或生产，T09 保持未完成。

**范围：** 独立 T09 基础，不采集实际 ZLE 事件、不接生产。`CyclePublisher` 将显式开始、更新及结束编辑周期映射到现有 `Publisher`；周期令牌防止延迟事件复活旧快照。结束供执行或取消共用，仅清除状态，不执行命令。调用方必须按真实事件顺序提供多字节模式和光标。

- [x] 先以真实 `net.Pipe` 与状态流验证更新、结束失效、新周期和旧令牌被拒绝；定向测试因接口缺失编译失败。
- [x] 实现串行周期协调，底层发送失败时关闭并禁用；不保存输入，禁止令牌溢出后复用。溢出测试是实现后补充覆盖。
- [x] 定向重复五次、全量、vendor 离线及 editstate 竞态测试通过；调用方仍须可靠提供真实事件，周期令牌不能认证来源，T09 总项保持未完成。

## 任务 12：显式 ZLE 钩子基础

- 2026-11-11：用户启用 zsh xtrace 时 update 钩子原将缓冲区及回调参数输出到跟踪流，真实 zsh 回归先失败；现在局部关闭 xtrace，返回后恢复调用方选项。ZLE 定向五次、全量、vendor 离线和 editstate race 测试通过。测试直接调用钩子；不覆盖 DEBUG trap、回调重新启用跟踪或其他 shell 代码，也不代表真实 ZLE 传输及 T09 完成。

- 2026-11-11：直接调用钩子的测试先复现周期开始前及结束后仍发送 update/end；现活动周期外忽略这两种事件，定向五次、全量、vendor 离线及 editstate race 通过。这不证明真实 ZLE 会出现此时序，不能区分新周期中迟到的旧事件，也未解决回调阻塞或中断时状态失效；T09 未完成。

- 2026-11-11：真实交互取消测试取消跳过，在显示待取消行后发送 Ctrl-C，验证重新进入编辑周期、后续命令执行以及被取消命令未执行；定向 50 次、全量、vendor 离线和 editstate race 通过。原 update 回调通知后立即中断可复现无后续事件；重绘同步不是 ZLE 进入读取的严格证明，不证明回调期间的中断、远端及时失效或已有 widgets 共存。无生产修复，T09 保持未完成。

- 2026-11-11：显式停用期间若编辑周期仍活动，原钩子直接移除而不发 end；`TestZLEDisableEndsActiveEditing` 先观察只输出 begin，现停用前发送一次 end，重复停用不重复发送。ZLE 定向五次、全量、vendor 离线和 editstate race 测试通过；该回归直接调用钩子，并非真实交互取消测试，回调失败或阻塞时不保证远端失效，T09 仍未完成。

- 2026-11-11：未启用时调用禁用函数，在 `errexit` 下原因 hook 管理函数不存在而退出；回归先失败，禁用时初始化 zsh/zle 和管理函数后通过。定向、全量、vendor 离线和 editstate race 测试通过。真实交互式 zsh 中中断编辑的探索测试不稳定：曾在发送 Ctrl-C 或 SIGINT 后既未收到 end 也未收到下一次 begin；原因未确定，测试保留但跳过，不能用于验收取消失效。真实生产集成和其他限制不变。

- [x] 在 Go 中提供显式启用的薄钩子片段，通过调用方提供的同步回调传递 begin、当前缓冲区/字符光标/多字节模式、end；不写用户配置、不自动连接 IPC、不执行编辑内容。真实 zsh + PTY 测试先因缺少片段编译失败，再验证 Unicode 输入、光标移动、结束与禁用；禁用的第二轮覆盖为实现后补充。
- [x] 验证回调返回失败不会在 zsh `errexit` 模式下中断 shell：测试先以退出状态 1 失败，使用条件调用后通过。定向十次、全量、vendor 离线与 editstate race 测试通过。
- 此钩子同步执行调用方回调，回调阻塞仍会阻塞 shell；调用方负责有界非阻塞传输、失败失效和可信事件顺序。仅测 `vared` 接受，未验证实际 shell 执行、取消、退出状态、与现有自定义 widgets 共存或生产连接；T09 未完成。

## 任务 9：认证拨号与发送端组合

**范围：** Darwin 独立组合；调用方提供私有路径，不接生产或 zsh。

- [x] 真实 Unix socket 测试同会话认证后的 Unicode 更新、清除及错误会话拒绝，非正写入期限不得发起连接；新接口最初缺失编译失败。实现后 `go test ./internal/editstate -run '^TestDialPublisher' -count=5 -timeout=30s`、全量、vendor 离线及 editstate race 测试通过。最初长测试名导致 macOS socket 路径超限，缩短后通过。UID 与会话标识不能认证同用户进程，路径安全由调用方负责；T09 仍未完成。

## 任务 3：有界编辑消息发送

**文件：** 新建 `internal/editstate/writer.go`、`internal/editstate/writer_test.go`。接口：`WriteUpdate(io.Writer, string, uint64, string, int) error` 和 `WriteClear(io.Writer, string, uint64) error`。生成任务 1 的帧，校验会话、序列、UTF-8、字节光标、行长和编码后帧长；无效消息不写入。调用方负责串行化写入、递增序列、认证、写入期限和写入错误后关闭连接，不输出日志或接生产。

- [x] 先写真实管道互通以及非法输入、编码膨胀和短写失败测试；接口缺失时编译失败。
- [x] 实现有界帧编码及写入，正确报告部分写入错误；无效消息不写入。
- [x] `go test ./internal/editstate -run 'TestWriteEditingFrames' -count=1 -timeout=30s`、`go test ./...`、`GOFLAGS=-mod=vendor GOPROXY=off go test ./...`、`go test -race ./internal/editstate -count=1` 通过；T09 仍未完成。

## 任务 4：有界编辑消息写入期限

**文件：** `internal/editstate/writer.go`、`internal/editstate/writer_test.go`。提供连接版 update/clear 写入，单帧正总时限，结束清除写期限并报告设置或清除失败；调用方独占写期限和串行写入，认证及写入错误后关闭连接仍由调用方负责。

- [x] 先用真实 `net.Pipe` 验证对端不读取时写入超时、成功写入后旧期限被清除、非正期限拒绝；测试期限设置及清除错误传播。
- [x] 实现连接写入封装并运行定向、全量、离线及竞态测试；不接生产，T09 总项保持未完成。

## 任务 2：状态流读取期限

**文件：** `internal/editstate/stream.go`、`internal/editstate/stream_test.go`。
**接口：** `ConsumeConn(conn net.Conn, session string, state *State, timeout time.Duration) error`：每帧读取（含等待首字节）均有从开始读取起的正总时限；超时或连接中断关闭状态。独占连接读期限，结束时清除期限；不关闭连接或管理认证，调用方负责其生命周期。

- [x] 测试真实 `net.Pipe` 的完整更新后半帧停滞超时并清除快照，零时限不读取且关闭状态；先运行确认接口缺失。
- [x] 实现逐帧总时限，复用严格消息解析；期限设置或清除失败返回错误。
- [x] `go test ./internal/editstate -run 'TestConsume(Stream|Conn)' -count=1 -timeout=30s`、`go test ./...`、`GOFLAGS=-mod=vendor GOPROXY=off go test ./...`、`go test -race ./internal/editstate -count=1` 均通过；不接生产。

## 任务 13：命令退出状态采集基础

- 2026-11-11：交互测试增加先注册的已有 `precmd` 钩子，`false` 和 `true` 状态仍正确；测试首次运行通过，属于既有行为的组合覆盖，不是修复，也未覆盖失败的其他钩子或所有插件。

**范围：** T09 的独立 zsh 状态采集基础，不接生产、不传输命令文本、不执行命令。

**文件：** 新建 `internal/editstate/command_status.go`、`internal/editstate/command_status_darwin_test.go`。

- [x] 先写真实交互式 zsh + PTY 测试，验证 `false` 发送 `1`、`true` 发送 `0`，回调只接收一个状态参数，停用后不再发送；运行确认 `CommandStatusHooks` 缺失。
- [x] 提供显式启用的 `CommandStatusHooks`，以 `preexec` 标记一次待报告命令、以 `precmd` 读取退出状态并调用调用方回调；注册部分失败时清理已注册 hook，回调失败不触发 shell `errexit`。
- [x] `go test ./internal/editstate -run '^TestCommandStatusInteractive$' -count=5`、全量、vendor 离线和 editstate race 测试通过。该基础不携带命令文本，仍不能单独证明命令状态与编辑周期或候选选择可靠关联；真实生产集成和取消/多会话语义留在 T09。

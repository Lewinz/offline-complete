# 兼容性矩阵

以下状态只记录本仓库当前可验证的结果；“未验证”不等于支持或失败。

| 环境 | 自动测试 | 人工验证 | 备注 |
| --- | --- | --- | --- |
| Apple Silicon + macOS + zsh | 通过 | 部分 | 当前开发机；真实 zsh PTY 测试通过 |
| iTerm2 + zsh | 未覆盖 | 未验证 | 需验证光标定位、焦点、缩放、粘贴 |
| macOS Terminal + zsh | 未覆盖 | 未验证 | 需验证 Accessibility 文本范围是否可用 |
| Intel Mac | 未验证 | 未验证 | `make dist` 产出 arm64+x86_64 通用二进制，未在 Intel 机器运行 |
| bash / fish | 不支持 | — | 第一版只做 zsh |
| Kiro 共存 | 未覆盖 | 未验证 | 安装器不接管 shell，不做自动停用 |
| 无 Accessibility 权限 | 回退逻辑存在 | 未验证 | 窗口应回退右上角并继续可用 |
| 多显示器 / 缩放 | 未覆盖 | 未验证 | 光标坐标转换只在主屏坐标假设下实现 |
| 窗口崩溃 / 代理退出 | 部分端口测试 | 未验证 | shell 应继续可用 |
| 终端重启 / 多会话 | 未覆盖 | 未验证 | 每个 agent 会话独立 socket 目录 |

## 复验方法

```bash
make test-all
make dist
scripts/install.sh
oc
```

按 [RELEASE.md](RELEASE.md) 的回归清单逐项记录版本、终端、shell、操作和结果。

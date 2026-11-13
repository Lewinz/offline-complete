# 发布、签名与离线审计

## 1. 构建与测试

```bash
make encrypt
make test-all
make dist
```

GitHub Actions 在推送 `v*` tag 时自动构建 Apple Silicon DMG，并创建/更新 GitHub Release。tag 去掉前导 `v` 后通过 `APP_VERSION` 写入 `CFBundleShortVersionString`，例如 `v1.2.3` 会构建 `1.2.3` 并把 DMG 和 SHA-256 校验文件附加到 Release。手动构建可使用：

```bash
make dist APP_VERSION=1.2.3 APP_BUILD=42
```

`make dist` 生成 Apple Silicon (arm64) 构建，产出：

```text
dist/Offline Complete.app
dist/Offline-Complete.zip
dist/Offline-Complete.dmg
```

开发构建使用本地自签名证书 `Offline Complete Dev`，由 `scripts/sign-app.sh` 创建并签名：

```bash
make sign
# 等价于
OC_SIGN_IDENTITY="Offline Complete Dev" \
OC_SIGN_KEYCHAIN="$HOME/Library/Keychains/offline-complete-dev.keychain-db" \
scripts/sign-app.sh "dist/Offline Complete.app"
```

证书首次构建自动生成，密码文件为 `~/Library/Application Support/offline-complete/signing/keychain-password`（0600）。不要提交这份密码或 keychain。自签名证书只解决本机 TCC 权限跨重建保留的问题，不用于对外分发。

## 2. Developer ID 签名与公证（发布前必须）

需要 Apple Developer 账号和 Developer ID Application 证书：

```bash
codesign --force --deep --options runtime --timestamp \
  --sign "Developer ID Application: <TEAM>" "dist/Offline Complete.app"

xcrun notarytool submit "dist/Offline-Complete.zip" \
  --apple-id "<id>" --team-id "<team>" --password "<app-specific-password>" --wait

xcrun stapler staple "dist/Offline Complete.app"
```

公证后重新生成 zip/dmg。不要把证书、密码或 API key 写入仓库。

## 3. 离线审计

当前脚本：

```bash
scripts/offline-audit.sh
```

它在 `sandbox-exec` 的 `(deny network*)` 配置下运行普通代理和管理命令。注意：

- `deny network*` 也可能拒绝 Unix socket，因此完整的 agent 会话需要允许 AF_UNIX、拒绝 IP 的 sandbox profile，或在干净 VM 中用系统级工具观察。
- 这不是“无网络连接”的完整证明。

正式验收至少覆盖：

1. 干净 macOS 虚拟机/测试账户，从未联网启动；
2. 系统级网络阻断或观察（如 Little Snitch、pf、nettop）；
3. 首次启动、规则加载、输入、选择、设置、重启、卸载各阶段；
4. 记录应用及其子进程的进程树、网络尝试和测试限制。

## 4. 兼容性回归

至少在 iTerm2 + zsh、macOS Terminal + zsh 上人工执行：

- 普通输入、方向键、粘贴、Ctrl-C、窗口缩放；
- 触发 `git`/`npm`/`docker` 候选、键盘/鼠标选择、插入；
- 无 Accessibility 权限、规则损坏、窗口崩溃、代理退出；
- 终端重启、多会话、Kiro 共存。

结果写入 [COMPATIBILITY.md](COMPATIBILITY.md)，未覆盖项不得标成通过。

## 5. 发布检查清单

- [ ] Developer ID 签名、公证、staple 完成
- [x] Apple Silicon (arm64) 构建
- [ ] Intel 真机运行验证
- [ ] 规则包哈希写入发布清单
- [x] 第三方许可文件随包（licenses/）
- [ ] 完整第三方许可清单核对
- [ ] 项目自身许可证已选择并加入应用包
- [ ] 干净 VM 离线审计通过
- [ ] iTerm2/Terminal 兼容矩阵完成
- [ ] 安装/卸载/升级/回滚说明核对
- [ ] 隐私说明与实际数据路径一致

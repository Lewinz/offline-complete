# The desktop app does not load interactive shell profiles; use the user's
# `g` Go version manager setup when `go` is not already on PATH.
GO ?= $(shell . "$$HOME/.g/env" 2>/dev/null; command -v go)
GOFLAGS ?= -mod=vendor

ifeq ($(strip $(GO)),)
$(error go toolchain not found; source ~/.g/env or install Go)
endif

.PHONY: build test test-all vet bin arm64 node-runtime encrypt dist pkg

build:
	$(GO) build $(GOFLAGS) ./...

test:
	GOFLAGS=$(GOFLAGS) $(GO) test ./...

test-all: bin test
	$(BIN)/oc-app --check

vet:
	GOFLAGS=$(GOFLAGS) $(GO) vet ./...

BIN ?= bin

$(BIN)/encrypt-rules: cmd/encrypt-rules/main.go internal/rules/encrypted.go
	mkdir -p $(BIN)
	$(GO) build $(GOFLAGS) -o $(BIN)/encrypt-rules ./cmd/encrypt-rules

bin:
	mkdir -p $(BIN)
	rm -f $(BIN)/offline-complete $(BIN)/oc-client $(BIN)/inventory-rules $(BIN)/oc-manage $(BIN)/oc-window $(BIN)/oc-app $(BIN)/encrypt-rules
	$(GO) build $(GOFLAGS) -o $(BIN)/offline-complete ./cmd/offline-complete
	$(GO) build $(GOFLAGS) -o $(BIN)/oc-client ./cmd/oc-client
	$(GO) build $(GOFLAGS) -o $(BIN)/inventory-rules ./cmd/inventory-rules
	$(GO) build $(GOFLAGS) -o $(BIN)/oc-manage ./cmd/oc-manage
	$(GO) build $(GOFLAGS) -o $(BIN)/encrypt-rules ./cmd/encrypt-rules
	swiftc -swift-version 5 -O app/main.swift -o $(BIN)/oc-window -framework AppKit -framework ApplicationServices
	swiftc -swift-version 5 -O app/gui.swift -o $(BIN)/oc-app -framework AppKit

arm64:
	rm -f $(BIN)/offline-complete $(BIN)/oc-client $(BIN)/inventory-rules $(BIN)/oc-manage $(BIN)/oc-window $(BIN)/oc-app
	mkdir -p $(BIN)
	for name in offline-complete oc-client inventory-rules oc-manage; do \
		GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 $(GO) build $(GOFLAGS) -ldflags="-s -w" -o $(BIN)/$$name ./cmd/$$name; \
	done
	swiftc -target arm64-apple-macosx13.0 -swift-version 5 -O app/main.swift -o $(BIN)/oc-window -framework AppKit -framework ApplicationServices
	swiftc -target arm64-apple-macosx13.0 -swift-version 5 -O app/gui.swift -o $(BIN)/oc-app -framework AppKit

NODE_VERSION ?= v22.14.0
NODE_DIR := third_party/node

node-runtime:
	@mkdir -p "$(NODE_DIR)"
	@set -e; \
	if [ ! -x "$(NODE_DIR)/node-arm64" ]; then \
		tmp="$$(mktemp -d)"; \
		echo "downloading Node $(NODE_VERSION) arm64"; \
		curl -fL --retry 3 -o "$$tmp/node.tar.gz" "https://nodejs.org/dist/$(NODE_VERSION)/node-$(NODE_VERSION)-darwin-arm64.tar.gz"; \
		tar -xzf "$$tmp/node.tar.gz" -C "$$tmp" --strip-components=1; \
		cp "$$tmp/bin/node" "$(NODE_DIR)/node-arm64"; \
		cp "$$tmp/LICENSE" "$(NODE_DIR)/LICENSE"; \
		chmod 0755 "$(NODE_DIR)/node-arm64"; \
		rm -rf "$$tmp"; \
	fi

APP_NAME := Offline Complete
APP := dist/$(APP_NAME).app
DMG_STAGE := dist/.dmg-stage

encrypt: $(BIN)/encrypt-rules
	$(BIN)/encrypt-rules rules/base.json rules/base.bin rules/specs rules/specs.enc

dist: arm64 node-runtime encrypt
	rm -rf "$(APP)"
	mkdir -p "$(APP)/Contents/MacOS" "$(APP)/Contents/Resources/licenses"
	cp $(BIN)/offline-complete $(BIN)/oc-window $(BIN)/oc-client $(BIN)/oc-manage $(BIN)/oc-app "$(APP)/Contents/MacOS/"
	cp rules/base.bin "$(APP)/Contents/Resources/rules.bin"
	cp -R rules/specs.enc "$(APP)/Contents/Resources/specs"
	mkdir -p "$(APP)/Contents/Resources/node"
	cp third_party/node/node-arm64 "$(APP)/Contents/Resources/node/node-arm64"
	cp third_party/node/LICENSE "$(APP)/Contents/Resources/licenses/node-LICENSE"
	cp rules/README.md "$(APP)/Contents/Resources/RULES.md"
	cp rules/KIRO-SPECS.md "$(APP)/Contents/Resources/RULES-SOURCE.md"
	cp rules/KIRO-SPECS-LICENSE.MIT "$(APP)/Contents/Resources/licenses/kiro-specs-LICENSE.MIT"
	cp app/Info.plist "$(APP)/Contents/Info.plist"
	cp third_party/creack-pty/LICENSE.MIT "$(APP)/Contents/Resources/licenses/creack-pty-LICENSE.MIT"
	cp third_party/golang-x-sys/LICENSE "$(APP)/Contents/Resources/licenses/golang-x-sys-LICENSE"
	cp third_party/golang-x-sys/PATENTS "$(APP)/Contents/Resources/licenses/golang-x-sys-PATENTS"
	cp third_party/amazon-q/LICENSE.MIT "$(APP)/Contents/Resources/licenses/amazon-q-LICENSE.MIT"
	cp third_party/amazon-q/LICENSE.APACHE "$(APP)/Contents/Resources/licenses/amazon-q-LICENSE.APACHE"
	cp third_party/wezterm/LICENSE.MIT "$(APP)/Contents/Resources/licenses/wezterm-LICENSE.MIT"
	codesign --force --deep --sign - "$(APP)"
	rm -f "dist/Offline-Complete.zip" "dist/Offline-Complete.dmg" "dist/Offline-Complete.pkg"
	ditto -c -k --keepParent "$(APP)" "dist/Offline-Complete.zip"
	rm -rf "$(DMG_STAGE)"
	mkdir -p "$(DMG_STAGE)"
	cp -R "$(APP)" "$(DMG_STAGE)/"
	ln -s /Applications "$(DMG_STAGE)/Applications"
	printf '%s\n' 'Offline Complete' '' '1. 把 Offline Complete.app 拖到 Applications。' '2. 打开应用；首次启动会自动安装 ~/.local/bin 命令和 zsh 自动启动块。' '3. 关闭窗口会留在后台继续补全；真正退出应用后自动启动停止。关闭当前终端，新开一个 zsh 窗口即可看到补全。' '' '如果系统阻止未签名应用，右键选择“打开”。' > "$(DMG_STAGE)/安装说明.txt"
	hdiutil create -volname "$(APP_NAME)" -srcfolder "$(DMG_STAGE)" -ov -format UDZO "dist/Offline-Complete.dmg"
	rm -rf "$(DMG_STAGE)"

pkg: dist
	scripts/build-pkg.sh

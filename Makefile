GO ?= go
INSTALL_DIR ?= $(HOME)/.local/bin
# バージョンの真実源はリリースタグ（vX.Y.Z）である。タグを取得していない checkout ではコミットへ退避する。
VERSION ?= $(shell git describe --tags --match 'v[0-9]*' --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/HappyOnigiri/hhx/internal/version.Version=$(VERSION) -X github.com/HappyOnigiri/hhx/internal/version.BuildMeta=dev

.PHONY: build install fmt fmt-check vet test check

build:
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/hhx ./cmd/hhx

# バイナリを置くだけで、agent の設定には触れない。登録は `hhx install` で行う。
install: build
	install -d "$(INSTALL_DIR)"
	install -m 0755 bin/hhx "$(INSTALL_DIR)/hhx"

fmt:
	gofmt -w cmd internal

fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; echo "run make fmt"; exit 1; }

vet:
	$(GO) vet ./...

test:
	$(GO) test -shuffle=on -count=1 ./...

check: fmt-check vet test

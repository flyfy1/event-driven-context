BUILD_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_LDFLAGS = -X event-driven-context/internal/buildinfo.Commit=$(BUILD_COMMIT) -X event-driven-context/internal/buildinfo.BuiltAt=$(BUILD_DATE)

.PHONY: build test check run deploy-prod deploy-legacy-gce deploy-frontend
build:
	mkdir -p bin
	go -C backend build -ldflags "$(BUILD_LDFLAGS)" -o ../bin/edc ./cmd/edc
	go -C backend build -ldflags "$(BUILD_LDFLAGS)" -o ../bin/edc-server ./cmd/edc-server
	go -C backend build -ldflags "$(BUILD_LDFLAGS)" -o ../bin/edc-runner ./cmd/edc-runner
test:
	go -C backend test -race ./...
check:
	go -C backend vet ./...
	go -C backend test -race ./...
	node --test frontend/i18n.test.js frontend/workspace-utils.test.js frontend/navigation.test.js frontend/file-tree.test.js frontend/workspace-files.test.js frontend/workspace-plugin-config.test.js
run:
	go -C backend run ./cmd/edc-server -skill-root skills
deploy-prod:
	./scripts/deploy-pi.sh
deploy-legacy-gce:
	ALLOW_LEGACY_GCE_DEPLOY=1 ./scripts/deploy-integ-prod.sh
deploy-frontend:
	./scripts/deploy-frontend.sh

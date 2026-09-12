.PHONY: build test check run deploy-prod deploy-frontend
build:
	mkdir -p bin
	go -C backend build -o ../bin/edc ./cmd/edc
	go -C backend build -o ../bin/edc-server ./cmd/edc-server
	go -C backend build -o ../bin/edc-runner ./cmd/edc-runner
test:
	go -C backend test -race ./...
check:
	go -C backend vet ./...
	go -C backend test -race ./...
	node --test frontend/i18n.test.js frontend/workspace-utils.test.js frontend/navigation.test.js frontend/file-tree.test.js frontend/workspace-files.test.js
run:
	go -C backend run ./cmd/edc-server -skill-root skills
deploy-prod:
	./scripts/deploy-integ-prod.sh
deploy-frontend:
	./scripts/deploy-frontend.sh

.PHONY: build test check run deploy-prod deploy-frontend
build:
	mkdir -p bin
	go build -o bin/edc ./cmd/edc
	go build -o bin/edc-server ./cmd/edc-server
test:
	go test -race ./...
check:
	go vet ./...
	go test -race ./...
run:
	go run ./cmd/edc-server
deploy-prod:
	./scripts/deploy-integ-prod.sh
deploy-frontend:
	./scripts/deploy-frontend.sh

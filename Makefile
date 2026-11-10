export GOCACHE ?= /tmp/vartovyi-go-build

.PHONY: test vet build assets check pack-check integration
test:
	go test -race ./...
vet:
	go vet ./...
build:
	mkdir -p bin
	go build -o bin/ ./cmd/...
assets:
	./scripts/fetch-assets.sh
pack-check:
	python3 scripts/check-pack.py
check: test vet pack-check
integration:
	./scripts/integration.sh

.PHONY: demo
demo:
	./scripts/demo.sh

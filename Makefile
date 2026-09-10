GO ?= go
IMAGE ?= llm-d-resiliency-manager:dev

.PHONY: build test vet fmt image
build:
	CGO_ENABLED=0 $(GO) build -trimpath -o bin/manager ./cmd/manager

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

image:
	docker build -t $(IMAGE) .

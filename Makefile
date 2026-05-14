.PHONY: build test clean lint fmt

build: build-ctx build-ctxhub

build-ctx:
	go build -o bin/ctx ./cmd/ctx

build-ctxhub:
	go build -o bin/ctxhub ./cmd/ctxhub

test:
	go test ./... -v

test-story-%:
	go test ./tests/ -run TestStory$* -v

clean:
	rm -rf bin/

fmt:
	go fmt ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

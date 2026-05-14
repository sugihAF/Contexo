.PHONY: build test clean lint fmt

build: build-ctx build-contexo-server

build-ctx:
	go build -o bin/ctx ./cmd/ctx

build-contexo-server:
	go build -o bin/contexo-server ./cmd/contexo-server

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

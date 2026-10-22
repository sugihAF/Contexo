.PHONY: build test clean lint fmt test-e2e test-e2e-update

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

test-e2e:
	CONTEXO_NO_UPDATE_CHECK=1 go test ./internal/e2e -count=1 -v

test-e2e-update:
	CONTEXO_NO_UPDATE_CHECK=1 go test ./internal/e2e -run TestGolden -update -count=1

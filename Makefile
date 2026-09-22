.PHONY: test test-short lint sec build run

test:
	go test -race -count=1 ./...

test-short:
	go test -race -short -count=1 ./...

lint:
	go vet ./...
	staticcheck ./...

sec:
	gosec -quiet ./...
	govulncheck ./...

build:
	go build -o bin/sentinela ./cmd/sentinela

run: build
	./bin/sentinela --role=all

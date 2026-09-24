.PHONY: test test-short lint sec build run generate image

# A versão carimbada no binário (vigilume version, log de boot). Fora de um
# clone git, "dev".
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

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
	go build -buildvcs=false -ldflags "$(LDFLAGS)" -o bin/vigilume ./cmd/vigilume

run: build
	./bin/vigilume --role=all

generate:
	sqlc generate

# A imagem de produção, com o painel embutido (Dockerfile).
image:
	docker build --build-arg VERSION=$(VERSION) -t vigilume:$(VERSION) -t vigilume:dev .


.PHONY: test test-short lint sec build run generate image up down e2e

# A versão carimbada no binário (stillon version, log de boot). Fora de um
# clone git, "dev".
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

test:
	python3 -m unittest discover -s deploy/ci -p test_docker_build.py -v
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
	go build -buildvcs=false -ldflags "$(LDFLAGS)" -o bin/stillon ./cmd/stillon

run: build
	./bin/stillon --role=all

generate:
	sqlc generate

# A imagem de produção, com o painel embutido (Dockerfile).
image:
	./scripts/docker-build.sh local --build-arg VERSION=$(VERSION) -t stillon:$(VERSION) -t stillon:dev .

# A pilha do avaliador (compose.yaml): db → seed → app, em http://localhost:8080.
# --build: quem desenvolve quer a imagem do código que tem na mão, e não a
# publicada nem uma construída antes.
up:
	./scripts/docker-build.sh compose up --wait --build

down:
	docker compose down

# O que o job e2e do CI roda: as checagens do compose e o Playwright contra
# ele, com o usuário de demonstração. APAGA o banco do compose antes (as
# checagens contam o que o seed criou num banco vazio). Deixa a pilha de pé;
# `make down` a tira.
e2e:
	docker compose down -v
	UP_ARGS=--build sh deploy/ci/checar-compose.sh
	cd web && E2E_BASE_URL=http://127.0.0.1:$${STILLON_PORT:-8080} E2E_COMPOSE_SERVICE=app \
		E2E_EMAIL=demo@stillon.local E2E_PASSWORD=demo-stillon-2026 npx playwright test

.PHONY: image-release
image-release:
	mkdir -p .docker-artifacts
	./scripts/docker-build.sh oci .docker-artifacts/stillon.tar --target runtime --build-arg VERSION=$(VERSION) -t stillon:$(VERSION) .
	./scripts/docker-build.sh oci .docker-artifacts/stillon-tools.tar --target tools --build-arg VERSION=$(VERSION) -t stillon:$(VERSION)-tools .

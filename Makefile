# Go runs inside the official image so the host needs only Docker.
# Caches live in .cache/ (git-ignored) and files stay owned by the current user.
GO_IMAGE ?= golang:1.26-alpine
DOCKER_GO = docker run --rm -u $(shell id -u):$(shell id -g) \
	-v $(CURDIR):/src -w /src \
	-e GOCACHE=/src/.cache/go-build -e GOMODCACHE=/src/.cache/mod -e CGO_ENABLED=0 \
	$(DOCKER_NET) $(DOCKER_ENV) $(GO_IMAGE)
GO ?= $(shell command -v go >/dev/null 2>&1 && echo go || echo "$(DOCKER_GO) go")
COMPOSE = docker compose -f deploy/docker-compose.yml

# Integration tests and the load generator join the compose network and reach services by name.
# e2e uses the host network: the SSO flow needs the same Keycloak URL a browser uses (localhost:8180).
it load: DOCKER_NET = --network pulse_default
it: DOCKER_ENV = -e PULSE_CH_ADDR=clickhouse:9000 -e PULSE_REDIS_ADDR=redis:6379
e2e: DOCKER_NET = --network host
e2e: DOCKER_ENV = -e PULSE_API=http://localhost:7080 -e PULSE_MAILPIT=http://localhost:18025 -e PULSE_ADMIN_TOKEN=dev-admin-token
seed: DOCKER_NET = --network host
it e2e load seed: GO = $(DOCKER_GO) go

# make load ARGS="-contacts 100000 -rate 5000 -duration 60s"
load: DOCKER_ENV =
load:
	$(GO) run ./cmd/loadgen -api http://pulse:7070 $(ARGS)

# make seed: demo workspace with data for trying the app (credentials: deploy/dev-seed.md)
seed:
	$(GO) run ./cmd/seed -api http://localhost:7080 $(ARGS)

.PHONY: tidy build test vet it e2e load seed up down logs ps

tidy:
	$(GO) mod tidy

build:
	$(GO) build ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test -race=false -count=1 ./...

it:
	$(GO) test -count=1 -tags integration ./internal/...

# Tests that receive webhooks run in a container on the compose network, so Pulse can call them
# without opening anything on the host.
e2e:
	$(GO) test -count=1 -tags e2e -v ./test/e2e/...
	docker run --rm --name pulse-e2e-hooks --network pulse_default -u $(shell id -u):$(shell id -g) -v $(CURDIR):/src -w /src \
		-e GOCACHE=/src/.cache/go-build -e GOMODCACHE=/src/.cache/mod -e CGO_ENABLED=0 \
		-e PULSE_API=http://pulse:7070 -e PULSE_MAILPIT=http://mailpit:8025 -e PULSE_HOOK_HOST=pulse-e2e-hooks \
		$(GO_IMAGE) go test -count=1 -tags e2e -v -run 'TestOutboundWebhooks|TestWebPushAndGateway|TestHubSpotIntegration|TestRESTHooksE2E|TestAudienceSync' ./test/e2e/...

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f pulse

ps:
	$(COMPOSE) ps

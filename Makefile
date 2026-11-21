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
seed docs-seed: DOCKER_NET = --network host
it e2e load seed docs-seed: GO = $(DOCKER_GO) go

# make load ARGS="-contacts 100000 -rate 5000 -duration 60s"
load: DOCKER_ENV =
load:
	$(GO) run ./cmd/loadgen -api http://pulse:7070 $(ARGS)

# make seed: demo workspace with data for trying the app (credentials: deploy/dev-seed.md)
seed:
	$(GO) run ./cmd/seed -api http://localhost:7080 $(ARGS)

# make docs-seed: the "Acme Retail" workspace the documentation screenshots use (once; later runs
# see it exists and stop). Credentials: deploy/dev-seed.md.
DOCS_WORKSPACE ?= acme-retail
DOCS_EMAIL ?= docs.admin@pulse.test
DOCS_PASSWORD ?= pulse-docs-pass-1
docs-seed:
	$(GO) run ./cmd/seed -api http://localhost:7080 -workspace "Acme Retail" -email $(DOCS_EMAIL) \
		-password $(DOCS_PASSWORD) -if-exists $(DOCS_WORKSPACE) $(ARGS)

# make docs-shots (every guide) or make docs-shots SHOT=01 (scripts in docs/screenshots/shots/ whose
# name starts with 01). Playwright runs in its own image as a client of http://localhost:7080; the npm
# package (same version as the image) is cached in .cache/docs-shots. See docs/screenshots/README.md.
PLAYWRIGHT_VERSION ?= 1.49.1
PULSE_URL ?= http://localhost:7080
# docs-seed signs in to check the workspace exists, and Pulse allows 10 sign-ins per 15 minutes, so
# it runs only until the scripts have a saved session (.cache/docs-shots/sessions).
docs-shots: $(if $(wildcard .cache/docs-shots/sessions/$(DOCS_WORKSPACE)-*.json),,docs-seed)
	docker run --rm --network host --shm-size 1g -u $(shell id -u):$(shell id -g) -v $(CURDIR):/src -w /src/docs/screenshots \
		-e HOME=/tmp -e npm_config_update_notifier=false -e npm_config_cache=/src/.cache/npm -e PLAYWRIGHT_VERSION=$(PLAYWRIGHT_VERSION) \
		-e PULSE_URL=$(PULSE_URL) -e PULSE_DOCS_WORKSPACE=$(DOCS_WORKSPACE) -e PULSE_DOCS_EMAIL=$(DOCS_EMAIL) \
		-e PULSE_DOCS_PASSWORD=$(DOCS_PASSWORD) -e SHOT=$(SHOT) \
		mcr.microsoft.com/playwright:v$(PLAYWRIGHT_VERSION)-jammy sh run.sh

# make docs-check: every link, image and heading anchor in the tutorials and developer docs resolves,
# and no screenshot is left unreferenced. Uses the host's Node.
docs-check:
	node docs/screenshots/checklinks.mjs . docs/tutorials docs/developer

.PHONY: tidy build test vet it e2e load seed docs-seed docs-shots docs-check up down logs ps

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
	$(GO) test -count=1 -timeout 40m -tags e2e -v ./test/e2e/...
	docker run --rm --name pulse-e2e-hooks --network pulse_default -u $(shell id -u):$(shell id -g) -v $(CURDIR):/src -w /src \
		-e GOCACHE=/src/.cache/go-build -e GOMODCACHE=/src/.cache/mod -e CGO_ENABLED=0 \
		-e PULSE_API=http://pulse:7070 -e PULSE_MAILPIT=http://mailpit:8025 -e PULSE_HOOK_HOST=pulse-e2e-hooks \
		$(GO_IMAGE) go test -count=1 -timeout 40m -tags e2e -v -run 'TestOutboundWebhooks|TestWebPushAndGateway|TestHubSpotIntegration|TestRESTHooksE2E|TestAudienceSync|TestBounceMailbox|TestBounceMailboxLinks|TestM5GatewayLoginFlow|TestGatewayStatusRequests|TestM5GupshupWhatsApp|TestM6WebPushInstallCheck|TestRSSArchive' ./test/e2e/...

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down

logs:
	$(COMPOSE) logs -f pulse

ps:
	$(COMPOSE) ps

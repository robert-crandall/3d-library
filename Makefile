# This app's slug, used to name the built binary and the docker-smoke resources.
# The template's `make init` rename machinery is gone: it existed to turn the
# template into an app, that has happened, and its test asserted no tracked path
# or file content contains the app's own name - which docs/designs/3D
# Library.dc.html breaks by existing.
APP_SLUG ?= 3d-library

.PHONY: help setup build install-mcp mcp-token mcp-config run dev test check spec docker-smoke clean

# So a frontend build that fails halfway doesn't leave an index.html behind that
# makes the target below look satisfied.
.DELETE_ON_ERROR:

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

setup: ## First run after cloning: deps, upload dir, .env, and a frontend build
	cd web && bun install --frozen-lockfile
	mkdir -p uploads
	@test -f .env || { cp .env.example .env && echo "wrote .env from .env.example"; }
	cd web && bun run build

# The frontend build has to come first: web/dist.go does `//go:embed all:build`,
# so the Go compile fails outright until web/build exists. That ordering is the
# same reason CI's go job depends on the web job's artifact.
build: ## Build the frontend, then the binary into bin/
	cd web && bun run build
	go build -o bin/$(APP_SLUG) ./cmd/server

install-mcp: ## Build and install the MCP server into ~/bin
	@set -e; \
	module="$$(go list -m -f '{{.Path}}')"; \
	name="$$(printf '%s\n' "$$module" | sed -E 's#/v([2-9]|[1-9][0-9]+)$$##; s#^.*/##')"; \
	path="$(HOME)/bin/$$name-mcp"; \
	mkdir -p "$(HOME)/bin"; \
	go build -o "$$path" ./cmd/mcp; \
	echo "installed $$path"

mcp-token: ## Mint an MCP API token and write its app config
	@set -e; \
	module="$$(go list -m -f '{{.Path}}')"; \
	name="$$(printf '%s\n' "$$module" | sed -E 's#/v([2-9]|[1-9][0-9]+)$$##; s#^.*/##')"; \
	go run github.com/robert-crandall/go-home-server/cmd/token@$(shell go list -m -f '{{.Version}}' github.com/robert-crandall/go-home-server) \
		-config "$$name" -name "$$name-mcp"

mcp-config: ## Print the MCP client configuration
	@set -e; \
	module="$$(go list -m -f '{{.Path}}')"; \
	name="$$(printf '%s\n' "$$module" | sed -E 's#/v([2-9]|[1-9][0-9]+)$$##; s#^.*/##')"; \
	printf '{\n  "mcpServers": {\n    "%s": { "command": "%s/bin/%s-mcp", "args": ["serve"] }\n  }\n}\n' \
		"$$name" "$(HOME)" "$$name"

run: ## Run the built binary
	./bin/$(APP_SLUG)

dev: ## Vite on :5173 with the API server on :8080
	@scripts/dev.sh

# The embed means `go test ./...` doesn't compile without web/build either, so
# `make clean && make test` needs something there. Building only when it's
# missing keeps the Go test loop fast; `make build` always rebuilds it, because
# that one has to produce a shippable binary.
web/build/index.html:
	cd web && bun run build

test: web/build/index.html ## Run the Go tests
	go test ./...

check: ## Type-check the frontend
	cd web && bun run check

# Regenerate the API contract. cmd/openapi imports internal/app, not web, so
# this needs neither a database nor a frontend build - which is what lets CI
# check for drift in a job that has neither. Both outputs are committed; CI
# fails if running this produces a diff.
spec: ## Regenerate docs/openapi.json and the API types from the routes
	go run ./cmd/openapi
	cd web && bun run gen:api

# Builds the real image and proves each of M4's acceptance criteria against it.
# Not in CI on purpose - see the script's header.
docker-smoke: ## Build the container image and smoke-test it against a throwaway Postgres
	@scripts/docker-smoke.sh

clean: ## Remove build output
	rm -rf bin .bin web/build web/.svelte-kit

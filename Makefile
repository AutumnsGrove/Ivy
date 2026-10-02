# Ivy — local entry points. CI runs exactly these (CI.md section 2: no CI-only magic).
GO        ?= go
GENERATED := api/api.gen.go web/src/lib/api/schema.d.ts

.PHONY: check generate drift fmt vet lint test web-check dev dev-fake web-assets

# Local dev stack: mailworld + Ivy + Vite against seeded, offline-safe data.
dev:
	$(GO) run ./cmd/ivy-dev up

# Same but with the deterministic fake LLM (no network, used by tests/CI).
dev-fake:
	$(GO) run ./cmd/ivy-dev up --llm fake

# Fast pre-commit set (PERFORMANCE.md 3b).
check: drift fmt vet lint test web-check

# Regenerate the committed typed contract outputs. Never edit them by hand.
generate:
	$(GO) tool oapi-codegen -config api/oapi-codegen.yaml api/openapi.yaml
	cd web && pnpm exec openapi-typescript ../api/openapi.yaml -o src/lib/api/schema.d.ts

# A stale generated output is a bug: regenerate and fail on any diff.
drift:
	$(MAKE) --no-print-directory generate
	@git diff --exit-code -- $(GENERATED) \
		|| { echo "generated code is stale: run 'make generate' and commit the result"; exit 1; }

fmt:
	@files=$$($(GO) tool gofumpt -l .); \
	if [ -n "$$files" ]; then echo "gofumpt would reformat:"; echo "$$files"; exit 1; fi

vet:
	$(GO) vet ./...

lint:
	$(GO) tool staticcheck ./...

test:
	CGO_ENABLED=0 $(GO) test -race ./...

web-check:
	cd web && pnpm check && pnpm test

# Build the frontend into the embed directory and precompress it (brotli 11,
# zstd, gzip 9). The generated files are git-ignored; only the placeholder is
# committed. The image build performs the same steps.
web-assets:
	cd web && pnpm build
	@mkdir -p internal/webui/build
	find internal/webui/build -mindepth 1 ! -name .gitkeep -delete
	cp -R web/build/. internal/webui/build/
	$(GO) run ./cmd/ivy-assets -dir internal/webui/build

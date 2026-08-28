.PHONY: build web test bdd lint private-check run tidy install clean e2e e2e-browser e2e-visible e2e-full web-config

# build compiles the keel binary into bin/keel, embedding the React studio from
# the committed internal/studio/web/dist. The dist is a committed artifact, so a
# plain build needs no Node — but after editing the studio source you must run
# `make web` to rebuild and commit the dist, or the binary embeds the old studio.
build:
	go build -o bin/keel ./cmd/keel

# web rebuilds the embedded React studio (internal/studio/web/dist). Run it after
# changing the studio front-end, then `make build`, then commit the dist. Kept
# separate from build so compiling the Go binary never requires a Node toolchain.
web:
	cd internal/studio/web && pnpm install --frozen-lockfile && pnpm build

test:
	go test ./...

# The generated nginx and Apache configs, handed to the real servers to parse.
# Kept out of `test` because it needs Docker and pulls two images, and out of
# the e2e tiers because it proves something they cannot: those boot a handful of
# stacks, this checks every framework's config, including the ones nothing has
# booted lately. A web server that refuses its config is a stack with no way in.
web-config:
	KEEL_WEBCONF=1 go test -timeout 20m -run TestEveryWebServerConfigParses ./internal/catalog/

bdd:
	go test ./features/...

# End-to-end BDD (Gherkin, godog). `e2e` is offline — resolves every stack through
# the real keel binary. e2e-visible scaffolds + boots frontend stacks, loads each
# homepage in a browser and saves a screenshot to features/e2e/artifacts. e2e-full
# also boots DDEV/Docker stacks and tears them down (drops volumes). See features/e2e.
e2e:
	go test ./features/e2e/...

# One-time: install the pinned Playwright and its browser for the recording tier.
e2e-browser:
	cd features/e2e/browser && npm install && npx playwright install chromium

e2e-visible:
	KEEL_E2E=native go test -timeout 30m ./features/e2e/...

# 4h, not the 60m this used to allow. The container tier installs whole
# platforms: Magento alone is a Composer resolve, a database install, a static
# content deploy and a full reindex, and the scaffold step gives each row up to
# 25m on its own. At 60m the run was killed part way through and left the
# containers it had not reached yet, which is worse than not running it.
#
# Run one slice instead of all sixteen rows with KEEL_E2E_ONLY, which matches
# the framework and env as substrings:
#
#   KEEL_E2E_ONLY=magento make e2e-full     # just the Magento rows
#   KEEL_E2E_ONLY=ddev make e2e-full        # every DDEV row
e2e-full:
	KEEL_E2E=full go test -timeout 240m ./features/e2e/...

lint: private-check
	go vet ./...
	gofmt -l . | grep . && exit 1 || true

# This repository is public. Anything private that reaches it cannot be taken
# back once pushed, so it is checked rather than trusted to .gitignore staying
# correct. A pattern containing a slash is anchored to the .gitignore's own
# directory, which is how a subtree once brought its private handoff in.
private-check:
	@fail=0; \
	bad=$$(git ls-files extensions/ | grep -v '^extensions/README.md$$' || true); \
	if [ -n "$$bad" ]; then \
		echo "error: private extension source is tracked:"; \
		echo "$$bad" | sed 's/^/  /'; fail=1; \
	fi; \
	docs=$$(git ls-files | grep -E '(^|/)\.claude/|(^|/)\.lh/|^_workspace/' || true); \
	if [ -n "$$docs" ]; then \
		echo "error: working config or notes are tracked (not part of what this ships):"; \
		echo "$$docs" | sed 's/^/  /'; fail=1; \
	fi; \
	mp=$$(git ls-files | grep -E '(^|/)\.claude-plugin/' || true); \
	if [ -n "$$mp" ]; then \
		echo "error: generated assistant plugin manifests are not tracked:"; \
		echo "$$mp" | sed 's/^/  /'; fail=1; \
	fi; \
	secrets=$$(git ls-files | grep -E '(^|/)(\.env$$|\.env\..*|auth\.json$$)' | grep -v '\.env\.example$$' || true); \
	if [ -n "$$secrets" ]; then \
		echo "error: secret-bearing files are tracked:"; \
		echo "$$secrets" | sed 's/^/  /'; fail=1; \
	fi; \
	if [ $$fail -eq 1 ]; then \
		echo "remove with: git rm -r --cached <path>   (and check it is gitignored)"; \
		exit 1; \
	fi; \
	echo "private: extensions clean, no working config or secrets tracked, marketplace intact"

run:
	go run ./cmd/keel

tidy:
	go mod tidy

install:
	go install ./cmd/keel

clean:
	rm -rf bin dist

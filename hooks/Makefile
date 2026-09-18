.PHONY: all clean install release

PLATFORMS := darwin-amd64 darwin-arm64 linux-amd64

BIN_DIR := bin
HOOKS := session-start pretooluse-skill stop validate-manifest

all: build

build:
	@mkdir -p $(BIN_DIR)
	@for hook in $(HOOKS); do \
		echo "Building $$hook..."; \
		cd $$hook && go build -o ../$(BIN_DIR)/$$hook . && cd ..; \
	done
	@echo "✓ All hooks built → $(BIN_DIR)/"

clean:
	rm -rf $(BIN_DIR)

install: build
	@for hook in $(HOOKS); do \
		echo "Installing $$hook..."; \
		cp $(BIN_DIR)/$$hook ./bin/$$hook; \
	done
	@echo "✓ All hooks installed → $(BIN_DIR)/ (where hooks.json resolves them)"

# release — cross-build every hook for all supported platforms (CGO-free, so
# plain GOOS/GOARCH switches suffice). M-A1: binaries are built by CI on tag;
# the tree copies exist only while marketplace installs read the git tree.
release: build
	@for p in $(PLATFORMS); do \
		os=$${p%-*}; arch=$${p##*-}; \
		for hook in $(HOOKS); do \
			echo "Cross-building $$hook for $$os/$$arch..."; \
			(cd $$hook && CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -o ../$(BIN_DIR)/$$hook-$$p .); \
		done; \
	done
	@echo "✓ Release binaries → $(BIN_DIR)/"

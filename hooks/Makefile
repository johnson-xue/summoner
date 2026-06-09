.PHONY: all clean install

BIN_DIR := bin
HOOKS := session-start pretooluse-skill stop

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
		cp $(BIN_DIR)/$$hook ./$$hook; \
	done
	@echo "✓ All hooks installed"

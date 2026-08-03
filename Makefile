BINARY    := mempie
PKG       := ./cmd/mempie
BUILD_DIR := bin

.PHONY: all
all: build

.PHONY: build
build:
	go build -o $(BUILD_DIR)/$(BINARY) $(PKG)

.PHONY: run
run: build
	./$(BUILD_DIR)/$(BINARY)

.PHONY: test
test:
	go test ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: fmt
fmt:
	gofmt -w .

.PHONY: fmt-check
fmt-check:
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then \
		echo "gofmt needed on:"; echo "$$out"; exit 1; \
	fi

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: check
check: fmt-check vet test

.PHONY: install
install:
	go install $(PKG)

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR)

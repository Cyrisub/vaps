APP_NAME := vaps
BUILD_DIR := bin

.PHONY: fmt vet test build clean

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

build:
	go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/$(APP_NAME)

build-legacy-v1:
	go build -tags vaps_legacy_v1 -o $(BUILD_DIR)/$(APP_NAME)-legacy ./cmd/$(APP_NAME)

test-legacy-v1:
	go test -tags vaps_legacy_v1 ./...

clean:
	rm -rf $(BUILD_DIR)

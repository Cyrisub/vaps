APP_NAME := vaps
BUILD_DIR := bin

.PHONY: fmt vet test test-functional build clean

fmt:
	go fmt ./...

vet:
	go vet ./...

test:
	go test ./...

test-functional:
	go test ./test/functional/... -count=1

build:
	go build -o $(BUILD_DIR)/$(APP_NAME) ./cmd/$(APP_NAME)

clean:
	rm -rf $(BUILD_DIR)

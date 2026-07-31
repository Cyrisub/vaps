APP_NAME := vaps
BUILD_DIR := bin

ifeq ($(OS),Windows_NT)
EXE := .exe
CLEAN_CMD := if exist "$(BUILD_DIR)" rmdir /s /q "$(BUILD_DIR)"
else
EXE :=
CLEAN_CMD := rm -rf "$(BUILD_DIR)"
endif

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
	go build -o "$(BUILD_DIR)/$(APP_NAME)$(EXE)" ./cmd/$(APP_NAME)

clean:
	$(CLEAN_CMD)

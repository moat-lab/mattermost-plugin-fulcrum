PLUGIN_ID    := fulcrum
PLUGIN_VERS  := $(shell awk -F'"' '/"version"/ {print $$4; exit}' plugin.json)
BUNDLE_NAME  := $(PLUGIN_ID)-$(PLUGIN_VERS).tar.gz

GO          ?= go
GOFLAGS     ?=
LDFLAGS     ?= -s -w

SERVER_DIR  := server
DIST_DIR    := dist
BUNDLE_DIR  := $(DIST_DIR)/$(PLUGIN_ID)
BUILD_DIR   := $(SERVER_DIR)/dist

define build_target
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 GOOS=$(1) GOARCH=$(2) $(GO) build $(GOFLAGS) -trimpath \
	  -ldflags='$(LDFLAGS)' \
	  -o $(BUILD_DIR)/plugin-$(1)-$(2)$(3) ./$(SERVER_DIR)
endef

.PHONY: all
all: dist

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: test
test:
	$(GO) test ./$(SERVER_DIR)/... $(GOFLAGS)

.PHONY: vet
vet:
	$(GO) vet ./$(SERVER_DIR)/...

.PHONY: server
server: \
  $(BUILD_DIR)/plugin-linux-amd64 \
  $(BUILD_DIR)/plugin-linux-arm64 \
  $(BUILD_DIR)/plugin-darwin-amd64 \
  $(BUILD_DIR)/plugin-darwin-arm64 \
  $(BUILD_DIR)/plugin-windows-amd64.exe

$(BUILD_DIR)/plugin-linux-amd64:    $(shell find $(SERVER_DIR) -name '*.go') go.mod
	$(call build_target,linux,amd64,)
$(BUILD_DIR)/plugin-linux-arm64:    $(shell find $(SERVER_DIR) -name '*.go') go.mod
	$(call build_target,linux,arm64,)
$(BUILD_DIR)/plugin-darwin-amd64:   $(shell find $(SERVER_DIR) -name '*.go') go.mod
	$(call build_target,darwin,amd64,)
$(BUILD_DIR)/plugin-darwin-arm64:   $(shell find $(SERVER_DIR) -name '*.go') go.mod
	$(call build_target,darwin,arm64,)
$(BUILD_DIR)/plugin-windows-amd64.exe: $(shell find $(SERVER_DIR) -name '*.go') go.mod
	$(call build_target,windows,amd64,.exe)

.PHONY: bundle
bundle: server pluginctl
	@rm -rf $(BUNDLE_DIR) $(DIST_DIR)/$(BUNDLE_NAME)
	@mkdir -p $(BUNDLE_DIR)/server/dist
	@cp plugin.json $(BUNDLE_DIR)/
	@cp $(BUILD_DIR)/plugin-linux-amd64        $(BUNDLE_DIR)/server/dist/
	@cp $(BUILD_DIR)/plugin-linux-arm64        $(BUNDLE_DIR)/server/dist/
	@cp $(BUILD_DIR)/plugin-darwin-amd64       $(BUNDLE_DIR)/server/dist/
	@cp $(BUILD_DIR)/plugin-darwin-arm64       $(BUNDLE_DIR)/server/dist/
	@cp $(BUILD_DIR)/plugin-windows-amd64.exe  $(BUNDLE_DIR)/server/dist/
	@tar -C $(DIST_DIR) -czf $(DIST_DIR)/$(BUNDLE_NAME) $(PLUGIN_ID)
	@echo "wrote $(DIST_DIR)/$(BUNDLE_NAME)"

.PHONY: dist
dist: bundle

.PHONY: pluginctl
pluginctl: build/pluginctl/pluginctl

build/pluginctl/pluginctl: build/pluginctl/main.go
	$(GO) build -trimpath -o build/pluginctl/pluginctl ./build/pluginctl

.PHONY: deploy
deploy: dist
	./build/pluginctl/pluginctl deploy $(PLUGIN_ID) $(DIST_DIR)/$(BUNDLE_NAME)

.PHONY: clean
clean:
	rm -rf $(DIST_DIR) $(BUILD_DIR) build/pluginctl/pluginctl

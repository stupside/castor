# The whisper.cpp Go bindings are cgo-based: they need libwhisper.a (built
# here via cmake) plus include/library paths exported to every recipe.

WHISPER := third_party/whisper.cpp
BUILD   := $(WHISPER)/build_go
LIB     := $(BUILD)/src/libwhisper.a

export C_INCLUDE_PATH := $(CURDIR)/$(WHISPER)/include:$(CURDIR)/$(WHISPER)/ggml/include
export LIBRARY_PATH   := $(CURDIR)/$(BUILD)/src:$(CURDIR)/$(BUILD)/ggml/src
export GGML_METAL_PATH_RESOURCES := $(CURDIR)/$(WHISPER)

ifeq ($(shell uname -s),Darwin)
export LIBRARY_PATH := $(LIBRARY_PATH):$(CURDIR)/$(BUILD)/ggml/src/ggml-blas:$(CURDIR)/$(BUILD)/ggml/src/ggml-metal
FRAMEWORKS := -framework Foundation -framework Metal -framework MetalKit
LDFLAGS := -ldflags "-extldflags '$(FRAMEWORKS)'"
endif

.PHONY: build run vet test whisper-lib env clean

build: $(LIB)
	go build $(LDFLAGS) -o castor .

run: $(LIB)
	go run $(LDFLAGS) . $(ARGS)

vet: $(LIB)
	go vet ./...

test: $(LIB)
	go test $(LDFLAGS) ./...

whisper-lib: $(LIB)

# eval "$(make env)" once per shell, then plain `go run .` / `go build` work.
env:
	@echo 'export C_INCLUDE_PATH="$(C_INCLUDE_PATH)"'
	@echo 'export LIBRARY_PATH="$(LIBRARY_PATH)"'
	@echo 'export GGML_METAL_PATH_RESOURCES="$(GGML_METAL_PATH_RESOURCES)"'
ifneq ($(FRAMEWORKS),)
	@echo 'export CGO_LDFLAGS="$(FRAMEWORKS)"'
endif

clean:
	rm -rf $(BUILD) castor

$(LIB):
	cmake -S $(WHISPER) -B $(BUILD) -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_EXAMPLES=OFF
	cmake --build $(BUILD) --target whisper -j

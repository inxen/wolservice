VERSION ?= 0.6.6
EMBEDDED_KEY ?= $(shell tr -d '\r\n' < .wol-embedded-key 2>/dev/null)
LDFLAGS := -s -w -X wol/internal/version.Version=$(VERSION)
ifneq ($(strip $(EMBEDDED_KEY)),)
LDFLAGS += -X wol/internal/secrets.EmbeddedKey=$(EMBEDDED_KEY)
else
$(warning WOL_EMBEDDED_KEY/.wol-embedded-key not set; building without an embedded secrets key)
endif
EXT := $(if $(filter windows,$(shell go env GOOS)),.exe,)

.PHONY: build test vet cross clean

build:
	rm -rf dist
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/wol$(EXT) ./cmd/wol

test:
	go test ./...

vet:
	go vet ./...

cross:
	VERSION=$(VERSION) bash build.sh

clean:
	rm -rf dist

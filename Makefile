VERSION := $(shell tr -d '[:space:]' < VERSION)
MODULE := github.com/yuzumone/org-syncd
LDFLAGS := -s -w -X $(MODULE)/internal/cli.version=$(VERSION)
DIST_DIR := dist

.PHONY: build clean dist archive

build:
	go build -trimpath -ldflags="$(LDFLAGS)" -o org-syncd ./cmd

clean:
	rm -rf $(DIST_DIR)

dist: clean
	$(MAKE) archive GOOS=linux GOARCH=amd64 SUFFIX=linux_amd64
	$(MAKE) archive GOOS=linux GOARCH=arm64 SUFFIX=linux_arm64
	$(MAKE) archive GOOS=linux GOARCH=arm GOARM=7 SUFFIX=linux_armv7
	@if command -v sha256sum >/dev/null 2>&1; then \
		cd $(DIST_DIR) && sha256sum *.tar.gz > SHA256SUMS; \
	else \
		cd $(DIST_DIR) && shasum -a 256 *.tar.gz > SHA256SUMS; \
	fi

archive:
	@if [ -z "$(GOOS)" ] || [ -z "$(GOARCH)" ] || [ -z "$(SUFFIX)" ]; then \
		echo "GOOS, GOARCH, and SUFFIX are required"; \
		exit 1; \
	fi
	@dir="$(DIST_DIR)/org-syncd_$(VERSION)_$(SUFFIX)"; \
	mkdir -p "$$dir"; \
	if [ -n "$(GOARM)" ]; then \
		GOOS="$(GOOS)" GOARCH="$(GOARCH)" GOARM="$(GOARM)" CGO_ENABLED=0 \
			go build -trimpath -ldflags="$(LDFLAGS)" -o "$$dir/org-syncd" ./cmd; \
	else \
		GOOS="$(GOOS)" GOARCH="$(GOARCH)" CGO_ENABLED=0 \
			go build -trimpath -ldflags="$(LDFLAGS)" -o "$$dir/org-syncd" ./cmd; \
	fi; \
	cp README.md LICENSE "$$dir/"; \
	tar -C "$(DIST_DIR)" -czf "$(DIST_DIR)/org-syncd_$(VERSION)_$(SUFFIX).tar.gz" "org-syncd_$(VERSION)_$(SUFFIX)"; \
	rm -rf "$$dir"

VERSION ?= dev
OBS_PACKAGE ?= "prometheus-ha_cluster_exporter"
ARCHS = amd64 arm64 ppc64le s390x

default: clean mod-tidy fmt vet-check test build

download:
	go mod download
	go mod verify

build: amd64

build-all: clean-bin $(ARCHS)

$(ARCHS):
	@mkdir -p build/bin
	CGO_ENABLED=0 GOOS=linux GOARCH=$@ go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o build/bin/ha_cluster_exporter-$(VERSION)-$@

install:
	go install

static-checks: vet-check fmt-check

vet-check: download
	go vet .

fmt:
	go fmt

mod-tidy:
	go mod tidy

fmt-check:
	.ci/go_lint.sh

test: download
	go test -v

coverage: coverage.out
coverage.out:
	go test -cover -coverprofile=coverage.out
	go tool cover -html=coverage.out

clean: clean-bin clean-obs
	go clean
	rm -f coverage.out

clean-bin:
	rm -rf build/bin

clean-obs:
	rm -rf build/obs

obs-commit: clean-obs
	mkdir -p build/obs/$(OBS_PACKAGE)
	osc checkout $(OBS_PROJECT)/$(OBS_PACKAGE) -o build/obs
	cp ha_cluster_exporter.spec build/obs/$(OBS_PACKAGE).spec
	cp -r doc LICENSE *.md ha_cluster_exporter.service build/obs/$(OBS_PACKAGE)/
	cp build/bin/* build/obs/$(OBS_PACKAGE)/
	mv build/obs/$(OBS_PACKAGE)/ha_cluster_exporter-$(VERSION)-arm64 build/obs/$(OBS_PACKAGE)/ha_cluster_exporter-$(VERSION)-aarch64
	mv build/obs/$(OBS_PACKAGE)/ha_cluster_exporter-$(VERSION)-amd64 build/obs/$(OBS_PACKAGE)/ha_cluster_exporter-$(VERSION)-x86_64
	sed -i 's/%%VERSION%%/$(VERSION)/' build/obs/$(OBS_PACKAGE)/$(OBS_PACKAGE).spec
	rm build/obs/$(OBS_PACKAGE)/*.tar.gz
	tar -cvzf build/obs/$(OBS_PACKAGE)/$(OBS_PACKAGE)-$(VERSION).tar.gz -C build/obs/$(OBS_PACKAGE) .
	cd build/obs; osc addremove
	cd build/obs; osc commit -m "Automated $(VERSION) release"

.PHONY: default download install static-checks vet-check fmt fmt-check mod-tidy test clean clean-bin clean-obs build build-all obs-commit $(ARCHS)

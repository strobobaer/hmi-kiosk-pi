.PHONY: test vet build release image

# Version des Programms; für ein Release Pflicht und gleich der Version des Update-Pakets,
# denn der Update-Wächter vergleicht sie mit der Gesundmarke des Dienstes.
VERSION ?= dev

test:
	scripts/dgo test -race ./...

vet:
	scripts/dgo vet ./...

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 scripts/dgo build -ldflags "-X hmikiosk/internal/release.Version=$(VERSION)" ./...

# Release-Bau: der öffentliche Schlüssel kommt aus .secrets/release.pub (nie im Repository).
# Der private Schlüssel bleibt in .secrets/release.key und wird nur von hmi-kiosk-pack gelesen.
release:
	test -s .secrets/release.pub
	test "$(VERSION)" != dev
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 scripts/dgo build -ldflags "-X hmikiosk/internal/release.ReleasePublicKeyHex=$$(tr -d '\r\n' < .secrets/release.pub) -X hmikiosk/internal/release.Version=$(VERSION)" -o dist/ ./cmd/hmi-kiosk ./cmd/hmi-kiosk-helper

# SD-Abbild mit pi-gen in Docker (packaging/build-image.sh); braucht .secrets/release.pub und VERSION=x.y.z.
image:
	VERSION=$(VERSION) packaging/build-image.sh

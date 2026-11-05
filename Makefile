.PHONY: test vet build release

test:
	scripts/dgo test -race ./...

vet:
	scripts/dgo vet ./...

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 scripts/dgo build ./...

# Release-Bau: der öffentliche Schlüssel kommt aus .secrets/release.pub (nie im Repository).
# Der private Schlüssel bleibt in .secrets/release.key und wird nur von hmi-kiosk-pack gelesen.
release:
	test -s .secrets/release.pub
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 scripts/dgo build -ldflags "-X hmikiosk/internal/release.ReleasePublicKeyHex=$$(tr -d '\r\n' < .secrets/release.pub)" -o dist/ ./cmd/hmi-kiosk ./cmd/hmi-kiosk-helper

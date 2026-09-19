# justfile for mskill — replaces Makefile
# run `just` to list recipes, `just install` to build + install locally

binary := "mskill"
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
ldflags := "-s -w -X skill.sh/mskill/cmd.version=" + version

# default: list recipes
default:
    @just --list

# build binary to ./mskill with version ldflags
build:
    @echo "Building {{binary}} {{version}}..."
    go build -ldflags "{{ldflags}}" -o {{binary}} .
    @echo "→ ./{{binary}} ({{version}})"

# like `go vet ./...`
vet:
    go vet ./...

# run all tests
test:
    go test ./...

# run tests verbosely
test-verbose:
    go test -v ./...

# go fmt + go vet
fmt:
    go fmt ./...
    go vet ./...

# tidy modules
tidy:
    go mod tidy

# remove built binaries
clean:
    rm -f {{binary}} {{binary}}-*

# build and install to current machine (go install + copy to GOBIN)
# ensures binary is on PATH; prints installed location and version
install:
    #!/usr/bin/env bash
    set -euo pipefail
    VERSION="{{version}}"
    LDFLAGS="{{ldflags}}"
    echo "Installing {{binary}} ${VERSION}..."
    go install -ldflags "$LDFLAGS" .
    BIN="$(go env GOBIN)"
    if [ -z "$BIN" ]; then BIN="$(go env GOPATH)/bin"; fi
    echo "Installed to $BIN/{{binary}}"
    if command -v "$BIN/{{binary}}" >/dev/null 2>&1; then
        "$BIN/{{binary}}" version || true
    fi
    # also ensure ./{{binary}} is fresh
    go build -ldflags "$LDFLAGS" -o {{binary}} .
    echo "→ ./{{binary}} also updated"
    if [ -w /usr/local/bin ]; then
        cp "$BIN/{{binary}}" /usr/local/bin/{{binary}} && echo "Copied to /usr/local/bin/{{binary}}"
    else
        echo "Tip: sudo cp $BIN/{{binary}} /usr/local/bin/{{binary}} for system-wide install"
        echo "     or add $BIN to PATH: export PATH=\"$BIN:\$PATH\""
    fi

# build and install system-wide via sudo (requires password)
install-system:
    #!/usr/bin/env bash
    set -euo pipefail
    VERSION="{{version}}"
    LDFLAGS="{{ldflags}}"
    echo "Building {{binary}} ${VERSION} for system install..."
    go build -ldflags "$LDFLAGS" -o {{binary}} .
    echo "Installing to /usr/local/bin/{{binary}} (sudo)..."
    sudo cp {{binary}} /usr/local/bin/{{binary}}
    sudo chmod +x /usr/local/bin/{{binary}}
    /usr/local/bin/{{binary}} version
    echo "Installed to /usr/local/bin/{{binary}}"

# uninstall from GOBIN and /usr/local/bin
uninstall:
    #!/usr/bin/env bash
    set -euo pipefail
    BIN="$(go env GOBIN)"
    if [ -z "$BIN" ]; then BIN="$(go env GOPATH)/bin"; fi
    rm -f "$BIN/{{binary}}" ./{{binary}}
    echo "Removed $BIN/{{binary}} and ./{{binary}}"
    if [ -f /usr/local/bin/{{binary}} ]; then
        if [ -w /usr/local/bin ]; then
            rm -f /usr/local/bin/{{binary}} && echo "Removed /usr/local/bin/{{binary}}"
        else
            echo "Need sudo to remove /usr/local/bin/{{binary}}: sudo rm /usr/local/bin/{{binary}}"
        fi
    fi

set shell := ["zsh", "-cu"]
set dotenv-load := true

# Default command
default:
    @just --list

# Build the project
build:
    go build -v -o eve ./cmd/eve

# Run the project locally
run: build
    ./eve

# Clean build artifacts
clean:
    rm -f eve
    rm -f *.db

# Run tests
test:
    go test -v ./...

# Tidy go modules
tidy:
    go mod tidy

# Build Docker image
docker-build:
    docker build -t eve:latest .

# Run everything (for local dev)
dev: tidy build run

# Deploy memory infrastructure locally (requires kubectl & kustomize)
deploy-mem:
    kubectl apply -k manifests/memory/

# Full local setup (infra + build)
setup: deploy-mem tidy build

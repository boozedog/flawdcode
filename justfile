default:
    @just --list

# Build and run
dev:
    go build -o tmp/flawdcode . && ./tmp/flawdcode

# Build the binary
build:
    go build -o flawdcode .

# Run the binary directly
run: build
    ./flawdcode

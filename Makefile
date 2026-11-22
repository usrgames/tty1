.PHONY: check fmtcheck test vet mancheck fuzz

# Everything CI runs.
check: fmtcheck test vet mancheck
	@echo "All checks passed!"

# Lists the files and fails if any is not gofmt-formatted.
fmtcheck:
	@echo "Checking format..."
	@test -z "$$(gofmt -l .)" || { echo "not formatted:"; gofmt -l .; exit 1; }

# The race detector needs cgo; run `make test RACE=` where there is no C compiler.
RACE ?= -race
test:
	@echo "Testing..."
	go test $(RACE) ./...

# The 32-bit build is vetted too: the reading of signatures is all lengths and offsets, and a
# sum that fits in 64 bits can wrap in 32.
vet:
	@echo "Vetting..."
	go vet ./...
	GOOS=linux GOARCH=386 go vet ./...

# The manual page has to format without a warning. mandoc is on every Mac; where there is none
# this says so and passes, and the release's build formats the page for real.
mancheck:
	@echo "Checking the manual page..."
	@if command -v mandoc >/dev/null; then mandoc -T lint -W warning man/tty1.1; else echo "no mandoc here: not checked"; fi

# Throws made-up signatures at the reader for a while. It must refuse them all and never crash.
FUZZTIME ?= 30s
fuzz:
	go test ./internal/release -run '^$$' -fuzz FuzzVerify -fuzztime $(FUZZTIME)

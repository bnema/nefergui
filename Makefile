.PHONY: build test vet race mocks mocks-check fakes-check perf-check check examples-check

build:
	CGO_ENABLED=0 go build ./...
test:
	CGO_ENABLED=0 go test ./...
vet:
	CGO_ENABLED=0 go vet ./...
race:
	CGO_ENABLED=1 go test -race ./...
mocks:
	mockery
mocks-check: mocks
	git diff --exit-code -- ':(glob)internal/**/*_mock_test.go'
	@test -z "$$(git ls-files --others --exclude-standard -- ':(glob)internal/**/*_mock_test.go')"
fakes-check:
	@rc=0; grep -rniE '^\s*type\s+\w*(fake|stub|spy|dummy|mock)\w*|^\s*\w*(fake|stub|spy|dummy|mock)\w*(\[[^]]*\])?\s+(struct|interface)\b' --include='*_test.go' --exclude='*_mock_test.go' . 2>/dev/null || rc=$$?; \
	[ $$rc -eq 1 ] || { [ $$rc -eq 0 ] && printf '%s\n' 'handwritten test double: generate with Mockery v3' >&2; exit 1; }
# Allocation guards (testing.AllocsPerRun).
perf-check:
	CGO_ENABLED=0 go test -run 'TestAlloc' -count=1 ./...
check: build vet test fakes-check perf-check
	CGO_ENABLED=0 staticcheck ./...
# The examples module: built, vetted and unit-tested without a compositor.
examples-check:
	cd examples && CGO_ENABLED=0 go build ./... && CGO_ENABLED=0 go vet ./... && CGO_ENABLED=0 staticcheck ./... && CGO_ENABLED=0 go test ./...

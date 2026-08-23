.PHONY: test race vet build compat contract fake generate clean

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

build:
	mkdir -p dist
	go build -trimpath -o dist/gl-axi ./cmd/gl-axi
	go build -trimpath -o dist/glab-axi ./cmd/glab-axi

# The legacy executable named glab is a contract-test artifact only. It is
# deliberately separated from product build/distribution paths.
compat:
	mkdir -p dist/test-only
	go build -trimpath -o dist/test-only/glab ./cmd/glab-compat

contract:
	go test ./internal/compat -run '^TestNoMistakesV1454Contract$$' -count=1

fake:
	go test ./internal/gitlab -run '^TestFakeGitLab$$' -count=1

generate:
	go run ./cmd/gen-product -root .

clean:
	rm -rf dist coverage.out

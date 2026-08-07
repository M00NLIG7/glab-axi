.PHONY: test race vet build contract fake clean

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

build:
	mkdir -p dist
	go build -trimpath -o dist/glab-axi ./cmd/glab-axi
	go build -trimpath -o dist/glab ./cmd/glab-compat

contract:
	go test ./internal/compat -run TestNoMistakesV1454Contract -count=1

fake:
	go test ./internal/gitlab -run TestFakeGitLab -count=1

clean:
	rm -rf dist coverage.out

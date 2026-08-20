.PHONY: fmt test race vet run compose benchmark-a benchmark-b

fmt:
	gofmt -w cmd internal tests
test:
	go test ./...
race:
	go test -race ./...
vet:
	go vet ./...
run:
	go run ./cmd/controller
compose:
	docker compose -f deployments/docker-compose.yml up --build
benchmark-a:
	bash experiments/uniform.sh
benchmark-b:
	bash experiments/mixed.sh

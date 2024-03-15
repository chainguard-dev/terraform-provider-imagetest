default: testacc

# Run acceptance tests
.PHONY: testacc
testacc:
	TF_ACC=1 go test ./... -v $(TESTARGS) -timeout 120m

terraform-provider-imagetest: testacc
	CGO_ENABLED=0 go build -ldflags "-s -w -X main.version=devel -X main.commit=$(shell git rev-parse --short HEAD)" .

.PHONY: clean
clean:
	rm terraform-provider-imagetest

.PHONY: go-generate
go-generate:
	go generate -v ./...

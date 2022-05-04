API_BRANCH=$(shell ./scripts/branch.sh ../findy-agent-api/)
SRC_ROOT=$(PWD)/../../..
IDL_PATH=../findy-agent-api/idl/v1

protoc_protocol:
	protoc --proto_path=$(IDL_PATH) --go_out=$(SRC_ROOT) --go-grpc_out=$(SRC_ROOT) protocol.proto

protoc_agency:
	protoc --proto_path=$(IDL_PATH) --go_out=$(SRC_ROOT) --go-grpc_out=$(SRC_ROOT) agency.proto

protoc_agent:
	protoc --proto_path=$(IDL_PATH) --go_out=$(SRC_ROOT) --go-grpc_out=$(SRC_ROOT) agent.proto

protoc:	protoc_protocol protoc_agency protoc_agent


drop_api:
	go mod edit -dropreplace github.com/findy-network/findy-agent-api

drop_all: drop_api

repl_api:
	go mod edit -replace github.com/findy-network/findy-agent-api=../findy-agent-api

repl_all: repl_api

modules:
	@echo Syncing modules for work branches ...
	go get github.com/findy-network/findy-agent-api@$(API_BRANCH)

deps:
	go get -t ./...

build:
	go build ./...

vet:
	go vet ./...

shadow:
	@echo Running govet
	go vet -vettool=$(GOPATH)/bin/shadow ./...
	@echo Govet success

check_fmt:
	$(eval GOFILES = $(shell find . -name '*.go'))
	@gofmt -l $(GOFILES)

lint:
	@golangci-lint run

lint_e:
	@$(GOPATH)/bin/golint ./... | grep -v export | cat

test:
	go test -v -p 1 -failfast ./...

logged_test:
	go test -v -p 1 -failfast ./... -args -logtostderr=true -v=10

test_cov_out:
	go test -p 1 -failfast \
		-coverpkg=github.com/findy-network/findy-common-go/... \
		-coverprofile=coverage.txt  \
		-covermode=atomic \
		./...

test_cov: test_cov_out
	go tool cover -html=coverage.txt

check: check_fmt vet shadow

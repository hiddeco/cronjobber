TAG?=latest
VERSION:=$(shell ./scripts/image-tag)
VCS_REF:=$(shell git rev-parse HEAD)
BUILD_DATE:=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
BUILD_TARGET?=cronjobber

SOURCE_DIRS=cmd pkg/apis pkg/controller pkg/logging pkg/version
TEST_FLAGS?=

run:
	go run cmd/cronjobber/* -kubeconfig=${HOME}/.kube/config -log-level=info

build:
	docker build -t hiddeco/cronjobber:$(TAG) \
		--build-arg VERSION="$(VERSION)" \
		--build-arg VCS_REF="$(VCS_REF)" \
		--build-arg BUILD_DATE="$(BUILD_DATE)" \
		--target $(BUILD_TARGET) \
		${PWD}

push:
	docker tag hiddeco/cronjobber:$(TAG) quay.io/hiddeco/cronjobber:$(VERSION)
	docker push quay.io/hiddeco/cronjobber:$(VERSION)

fmt:
	gofmt -l -s -w $(SOURCE_DIRS)

test-fmt:
	gofmt -l -s $(SOURCE_DIRS) | grep ".*\.go"; if [ "$$?" = "0" ]; then exit 1; fi

test-codegen:
	./hack/verify-codegen.sh
	git diff --exit-code -- pkg/apis pkg/client

test: test-fmt test-codegen
	go test $(TEST_FLAGS) ./...

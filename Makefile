.PHONY: build test verify release-verify docker-build docker-test install uninstall

build:
	$(MAKE) -C gateway build

test:
	$(MAKE) -C gateway test

verify:
	./tests/verify.sh

release-verify:
	./tests/clean_release_acceptance.sh

docker-build:
	docker build -t localrouter:dev .

docker-test:
	./tests/docker_acceptance.sh

install:
	./tools/install-localrouter.sh install

uninstall:
	./tools/install-localrouter.sh uninstall

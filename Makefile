.PHONY: build test verify install uninstall

build:
	$(MAKE) -C gateway build

test:
	$(MAKE) -C gateway test

verify:
	./tests/verify.sh

install:
	./tools/install-localrouter.sh install

uninstall:
	./tools/install-localrouter.sh uninstall

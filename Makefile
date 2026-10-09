.PHONY: prepare test build
prepare:
	python3 scripts/dev.py prepare

test:
	python3 scripts/dev.py test

build:
	python3 scripts/dev.py build

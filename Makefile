.PHONY: build
build:
	@dagger call binary export --path ~/.local/bin/manager

.PHONY: run
run: build
	@LB=$$(ip route get 8.8.8.8 2>/dev/null | awk '/src/ {print $$7; exit}'); \
	if [ -z "$$LB" ]; then \
		LB=127.0.0.1; \
	fi; \
	manager --debug --load-balancer=$$LB --port-forward

.PHONY: .git/hooks .git/hooks/ .git/hooks/pre-commit
.git/hooks .git/hooks/ .git/hooks/pre-commit:
	@cp .githooks/* .git/hooks

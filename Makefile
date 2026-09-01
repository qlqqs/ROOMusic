.PHONY: dev dev-reset backend-test backend-build frontend-install frontend-check frontend-build

dev:
	./scripts/dev.sh

dev-reset:
	./scripts/dev-reset.sh

backend-test:
	cd backend && go test ./...

backend-build:
	cd backend && go build ./...

frontend-install:
	cd frontend && npm install

frontend-check:
	cd frontend && npm run lint && npm run typecheck && npm run test

frontend-build:
	cd frontend && npm run build

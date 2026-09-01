.PHONY: backend-test backend-build frontend-install frontend-check frontend-build

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

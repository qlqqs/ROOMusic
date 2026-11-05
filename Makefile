.PHONY: prod prod-stop prod-status prod-logs prod-backup prod-reset-password dev dev-reset backend-test backend-build frontend-install frontend-check frontend-build frontend-e2e

dev:
	./scripts/dev.sh

prod:
	./scripts/production.sh up $(if $(REF),--ref '$(REF)')

prod-stop prod-status prod-logs prod-backup:
	./scripts/production.sh $(patsubst prod-%,%,$@)

prod-reset-password:
	./scripts/production.sh reset-password '$(U)'

dev-reset:
	./scripts/dev-reset.sh

backend-test:
	cd backend && go test ./...

backend-build:
	cd backend && go build ./...

frontend-install:
	cd frontend && npm ci

frontend-check:
	cd frontend && npm run lint && npm run typecheck && npm run test

frontend-build:
	cd frontend && npm run build

# 端到端检查先构建生产产物再用 vite preview 提供，接口由测试内的受控后端响应，不需要启动后端。
# 首次运行前需要安装浏览器：cd frontend && npx playwright install --with-deps chromium
frontend-e2e:
	cd frontend && npm run test:e2e
